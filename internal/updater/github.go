package updater

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	maxAPIBytes             = 1 << 20
	maxCheckAttempts        = 5
	checkRetryDelay         = 15 * time.Second
	secondaryRateLimitDelay = time.Minute
)

var (
	errTooManyRedirects  = errors.New("too many redirects")
	errUntrustedRedirect = errors.New("GitHub API redirected to an untrusted host")
)

const DefaultRepository = "fcying/CommandTrayHostGo"

// Repository is replaced at build time for release binaries.
var Repository = DefaultRepository

type Outcome uint8

const (
	OutcomeCurrentVersionUnknown Outcome = iota
	OutcomeUpToDate
	OutcomeUpdateAvailable
)

type CheckOptions struct {
	CurrentVersion  string
	SkipPrereleases bool
}

type Release struct {
	Tag           string
	URL           string
	Prerelease    bool
	parsedVersion Version
}

type Result struct {
	CurrentVersion string
	Latest         Release
	Outcome        Outcome
}

type Checker struct {
	client        *http.Client
	apiURL        string
	repositoryURL string
	releasesURL   string
	retryDelay    time.Duration
	now           func() time.Time
}

type githubRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

func NewChecker(repository string) (*Checker, error) {
	if err := ValidateRepository(repository); err != nil {
		return nil, err
	}
	repositoryURL := "https://github.com/" + repository
	return &Checker{
		client: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: checkGitHubRedirect,
		},
		apiURL:        "https://api.github.com/repos/" + repository + "/releases?per_page=100",
		repositoryURL: repositoryURL,
		releasesURL:   repositoryURL + "/releases",
		retryDelay:    checkRetryDelay,
		now:           time.Now,
	}, nil
}

func (c *Checker) RepositoryURL() string {
	return c.repositoryURL
}

func (c *Checker) Check(ctx context.Context, options CheckOptions) (Result, error) {
	if c == nil || c.client == nil || c.apiURL == "" {
		return Result{}, errors.New("update checker is not configured")
	}
	for attempt := 1; ; attempt++ {
		result, err := c.checkOnce(ctx, options)
		if err == nil {
			return result, nil
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return Result{}, contextErr
		}
		var transient *transientCheckError
		if !errors.As(err, &transient) {
			return Result{}, err
		}
		if attempt >= maxCheckAttempts {
			return Result{}, fmt.Errorf("update check failed after %d attempts: %w", attempt, err)
		}
		if err := waitForRetry(ctx, checkRetryDelayFor(transient, c.retryDelay, attempt)); err != nil {
			return Result{}, err
		}
	}
}

func (c *Checker) checkOnce(ctx context.Context, options CheckOptions) (Result, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL, nil)
	if err != nil {
		return Result{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "CommandTrayHostGo-Updater")
	response, err := c.client.Do(request)
	if err != nil {
		requestErr := fmt.Errorf("request releases: %w", err)
		if ctx.Err() != nil || errors.Is(err, errTooManyRedirects) || errors.Is(err, errUntrustedRedirect) {
			return Result{}, requestErr
		}
		return Result{}, &transientCheckError{err: requestErr}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Result{}, c.responseError(response)
	}
	if !isJSONContentType(response.Header.Get("Content-Type")) {
		return Result{}, fmt.Errorf("GitHub releases returned unsupported Content-Type %q", response.Header.Get("Content-Type"))
	}
	if response.ContentLength > maxAPIBytes {
		return Result{}, fmt.Errorf("GitHub releases response exceeds %d bytes", maxAPIBytes)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAPIBytes+1))
	if err != nil {
		return Result{}, &transientCheckError{err: fmt.Errorf("read releases: %w", err)}
	}
	if len(body) > maxAPIBytes {
		return Result{}, fmt.Errorf("GitHub releases response exceeds %d bytes", maxAPIBytes)
	}
	releases, err := decodeGitHubReleases(body)
	if err != nil {
		return Result{}, err
	}
	latest, err := c.selectLatest(releases, options.SkipPrereleases)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		CurrentVersion: options.CurrentVersion,
		Latest:         latest,
		Outcome:        OutcomeCurrentVersionUnknown,
	}
	if !IsComparable(options.CurrentVersion) {
		return result, nil
	}
	if !options.SkipPrereleases && latest.Prerelease {
		if latest.Tag != options.CurrentVersion {
			result.Outcome = OutcomeUpdateAvailable
		} else {
			result.Outcome = OutcomeUpToDate
		}
		return result, nil
	}
	currentVersion, _ := ParseVersion(options.CurrentVersion)
	if Compare(latest.parsedVersion, currentVersion) > 0 {
		result.Outcome = OutcomeUpdateAvailable
	} else {
		result.Outcome = OutcomeUpToDate
	}
	return result, nil
}

func (c *Checker) responseError(response *http.Response) error {
	body, bodyErr := io.ReadAll(io.LimitReader(response.Body, 4096))
	statusErr := fmt.Errorf("GitHub releases returned HTTP %d", response.StatusCode)
	if bodyErr != nil {
		statusErr = errors.Join(statusErr, fmt.Errorf("read GitHub error response: %w", bodyErr))
	}
	rateLimited := githubRateLimited(response.StatusCode, response.Header, body)
	if rateLimited || transientHTTPStatus(response.StatusCode) {
		minimumDelay, minimumDelaySet := retryDelayFromHeaders(response.Header, c.now())
		return &transientCheckError{
			err:             statusErr,
			minimumDelay:    minimumDelay,
			minimumDelaySet: minimumDelaySet,
			rateLimited:     rateLimited,
		}
	}
	return statusErr
}

func (c *Checker) selectLatest(releases []githubRelease, skipPrereleases bool) (Release, error) {
	var latest Release
	found := false
	for _, release := range releases {
		if release.Draft || skipPrereleases && release.Prerelease {
			continue
		}
		version, err := ParseVersion(release.TagName)
		if err != nil {
			continue
		}
		candidate := Release{Tag: release.TagName, URL: c.releasesURL + "/tag/" + url.PathEscape(release.TagName), Prerelease: release.Prerelease, parsedVersion: version}
		if !skipPrereleases && candidate.Prerelease {
			return candidate, nil
		}
		if !found || Compare(version, latest.parsedVersion) > 0 {
			latest, found = candidate, true
		}
	}
	if !found {
		return Release{}, errNoReleases
	}
	return latest, nil
}

type transientCheckError struct {
	err             error
	minimumDelay    time.Duration
	minimumDelaySet bool
	rateLimited     bool
}

func (e *transientCheckError) Error() string {
	return e.err.Error()
}

func (e *transientCheckError) Unwrap() error {
	return e.err
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func checkRetryDelayFor(transient *transientCheckError, defaultDelay time.Duration, attempt int) time.Duration {
	delay := defaultDelay
	if transient.minimumDelaySet {
		if transient.minimumDelay > delay {
			delay = transient.minimumDelay
		}
		return delay
	}
	if !transient.rateLimited {
		return delay
	}
	if attempt < 1 {
		attempt = 1
	} else if attempt > maxCheckAttempts {
		attempt = maxCheckAttempts
	}
	rateLimitDelay := secondaryRateLimitDelay * time.Duration(1<<uint(attempt-1))
	if rateLimitDelay > delay {
		return rateLimitDelay
	}
	return delay
}

func retryDelayFromHeaders(header http.Header, now time.Time) (time.Duration, bool) {
	if value := strings.TrimSpace(header.Get("Retry-After")); value != "" {
		if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second, true
		}
		if retryAt, err := http.ParseTime(value); err == nil {
			delay := retryAt.Sub(now)
			if delay < 0 {
				delay = 0
			}
			return delay, true
		}
	}
	if strings.TrimSpace(header.Get("X-RateLimit-Remaining")) != "0" {
		return 0, false
	}
	reset, err := strconv.ParseInt(strings.TrimSpace(header.Get("X-RateLimit-Reset")), 10, 64)
	if err != nil {
		return 0, false
	}
	delay := time.Unix(reset, 0).Sub(now)
	if delay < 0 {
		delay = 0
	}
	return delay, true
}

func githubRateLimited(status int, header http.Header, body []byte) bool {
	if status != http.StatusForbidden && status != http.StatusTooManyRequests {
		return false
	}
	if status == http.StatusTooManyRequests || strings.TrimSpace(header.Get("Retry-After")) != "" || strings.TrimSpace(header.Get("X-RateLimit-Remaining")) == "0" {
		return true
	}
	message := strings.ToLower(string(body))
	return strings.Contains(message, "rate limit") || strings.Contains(message, "abuse detection")
}

func transientHTTPStatus(status int) bool {
	return status == http.StatusRequestTimeout || status >= 500 && status <= 599
}

func decodeGitHubReleases(body []byte) ([]githubRelease, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var releases []githubRelease
	if err := decoder.Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("decode releases: trailing JSON data")
	}
	return releases, nil
}

func isJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && (mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"))
}

func checkGitHubRedirect(request *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errTooManyRedirects
	}
	if request.URL.Scheme != "https" || request.URL.Hostname() != "api.github.com" || request.URL.User != nil {
		return errUntrustedRedirect
	}
	if port := request.URL.Port(); port != "" && port != "443" {
		return errUntrustedRedirect
	}
	return nil
}
