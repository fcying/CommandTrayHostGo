package updater

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewCheckerValidatesRepositoryAndBuildsTrustedPages(t *testing.T) {
	checker, err := NewChecker("owner/repository")
	if err != nil {
		t.Fatal(err)
	}
	if checker.RepositoryURL() != "https://github.com/owner/repository" {
		t.Fatalf("RepositoryURL() = %q", checker.RepositoryURL())
	}
	for _, repository := range []string{"", "owner", "owner/repository?query"} {
		if _, err := NewChecker(repository); err == nil {
			t.Fatalf("NewChecker(%q) succeeded", repository)
		}
	}
}

func TestCheckerSelectsHighestCompatibleRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Accept") != "application/vnd.github+json" || request.Header.Get("X-GitHub-Api-Version") != "2022-11-28" || request.Header.Get("User-Agent") != "CommandTrayHost-Updater" {
			t.Errorf("request headers = %+v", request.Header)
		}
		writeJSON(response, `[
			{"tag_name":"v1.3.0-rc.1","draft":false,"prerelease":true},
			{"tag_name":"not-a-version","draft":false,"prerelease":false},
			{"tag_name":"v1.2.0","draft":false,"prerelease":false,"html_url":"http://attacker.invalid/release","assets":[{"browser_download_url":"http://attacker.invalid/payload.exe"}]},
			{"tag_name":"v1.4.0","draft":true,"prerelease":false},
			{"tag_name":"v1.1.0","draft":false,"prerelease":false}
		]`)
	}))
	defer server.Close()
	checker := checkerForServer(t, server)

	stable, err := checker.Check(context.Background(), CheckOptions{CurrentVersion: "v1.1.0", SkipPrereleases: true})
	if err != nil {
		t.Fatal(err)
	}
	if stable.Outcome != OutcomeUpdateAvailable || stable.Latest.Tag != "v1.2.0" || stable.Latest.Prerelease || stable.Latest.URL != "https://github.com/owner/repository/releases/tag/v1.2.0" {
		t.Fatalf("stable result = %+v", stable)
	}

	preview, err := checker.Check(context.Background(), CheckOptions{CurrentVersion: "v1.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Outcome != OutcomeUpdateAvailable || preview.Latest.Tag != "v1.3.0-rc.1" || !preview.Latest.Prerelease {
		t.Fatalf("preview result = %+v", preview)
	}
}

func TestCheckerReportsCurrentVersionState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, `[{"tag_name":"v2.3.0","draft":false,"prerelease":false}]`)
	}))
	defer server.Close()
	checker := checkerForServer(t, server)

	for _, test := range []struct {
		current string
		outcome Outcome
	}{
		{current: "development", outcome: OutcomeCurrentVersionUnknown},
		{current: "v2.3.0", outcome: OutcomeUpToDate},
		{current: "v2.4.0", outcome: OutcomeUpToDate},
		{current: "v2.2.0", outcome: OutcomeUpdateAvailable},
	} {
		result, err := checker.Check(context.Background(), CheckOptions{CurrentVersion: test.current, SkipPrereleases: true})
		if err != nil {
			t.Fatal(err)
		}
		if result.CurrentVersion != test.current || result.Latest.Tag != "v2.3.0" || result.Outcome != test.outcome {
			t.Fatalf("Check(%q) = %+v", test.current, result)
		}
	}
}

func TestCheckerRejectsInvalidSuccessfulResponses(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "wrong content type", contentType: "text/html", body: `[]`},
		{name: "invalid JSON", contentType: "application/json", body: `[`},
		{name: "trailing JSON", contentType: "application/json", body: `[] {}`},
		{name: "no compatible release", contentType: "application/json", body: `[{"tag_name":"not-semver"}]`},
		{name: "oversized", contentType: "application/json", body: strings.Repeat(" ", maxAPIBytes+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", test.contentType)
				_, _ = io.WriteString(response, test.body)
			}))
			defer server.Close()
			checker := checkerForServer(t, server)
			if _, err := checker.Check(context.Background(), CheckOptions{CurrentVersion: "v1.0.0"}); err == nil {
				t.Fatal("Check succeeded")
			}
		})
	}
}

func TestCheckerRetriesTransientFailures(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) < 3 {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeJSON(response, `[{"tag_name":"v1.1.0","draft":false,"prerelease":false}]`)
	}))
	defer server.Close()
	checker := checkerForServer(t, server)

	result, err := checker.Check(context.Background(), CheckOptions{CurrentVersion: "v1.0.0", SkipPrereleases: true})
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 3 || result.Outcome != OutcomeUpdateAvailable {
		t.Fatalf("requests = %d, result = %+v", requests.Load(), result)
	}
}

func TestCheckerRetriesTransportFailures(t *testing.T) {
	var requests atomic.Int32
	checker, err := NewChecker("owner/repository")
	if err != nil {
		t.Fatal(err)
	}
	checker.apiURL = "https://example.invalid/releases"
	checker.retryDelay = 0
	checker.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if requests.Add(1) < 3 {
			return nil, io.ErrUnexpectedEOF
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`[{"tag_name":"v1.1.0","draft":false,"prerelease":false}]`)),
			Request:    request,
		}, nil
	})}

	result, err := checker.Check(context.Background(), CheckOptions{CurrentVersion: "v1.0.0", SkipPrereleases: true})
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 3 || result.Outcome != OutcomeUpdateAvailable {
		t.Fatalf("requests = %d, result = %+v", requests.Load(), result)
	}
}

func TestCheckerRetriesRateLimitsAndRejectsPermanentHTTPFailures(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   int
		header   http.Header
		attempts int32
	}{
		{name: "request timeout", status: http.StatusRequestTimeout, attempts: maxCheckAttempts},
		{name: "too many requests", status: http.StatusTooManyRequests, header: http.Header{"Retry-After": []string{"0"}}, attempts: maxCheckAttempts},
		{name: "rate limited forbidden", status: http.StatusForbidden, header: http.Header{"X-RateLimit-Remaining": []string{"0"}, "X-RateLimit-Reset": []string{"0"}}, attempts: maxCheckAttempts},
		{name: "server error", status: http.StatusInternalServerError, attempts: maxCheckAttempts},
		{name: "ordinary forbidden", status: http.StatusForbidden, attempts: 1},
		{name: "not found", status: http.StatusNotFound, attempts: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				for name, values := range test.header {
					for _, value := range values {
						response.Header().Add(name, value)
					}
				}
				response.WriteHeader(test.status)
			}))
			defer server.Close()
			checker := checkerForServer(t, server)
			if _, err := checker.Check(context.Background(), CheckOptions{CurrentVersion: "v1.0.0"}); err == nil {
				t.Fatal("Check succeeded")
			}
			if requests.Load() != test.attempts {
				t.Fatalf("requests = %d, want %d", requests.Load(), test.attempts)
			}
		})
	}
}

func TestCheckerCancelsRateLimitBackoff(t *testing.T) {
	bodyClosed := make(chan struct{})
	var requests atomic.Int32
	checker, err := NewChecker("owner/repository")
	if err != nil {
		t.Fatal(err)
	}
	checker.apiURL = "https://example.invalid/releases"
	checker.retryDelay = 0
	checker.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     make(http.Header),
			Body:       &closeSignalBody{Reader: strings.NewReader(""), closed: bodyClosed},
			Request:    request,
		}, nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := checker.Check(ctx, CheckOptions{CurrentVersion: "v1.0.0"})
		done <- err
	}()
	<-bodyClosed
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if requests.Load() != 1 {
			t.Fatalf("requests = %d, want 1", requests.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("Check did not cancel during backoff")
	}
}

func TestCheckerRejectsUntrustedRedirectWithoutRetry(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Redirect(response, request, "http://example.com/releases", http.StatusFound)
	}))
	defer server.Close()
	checker, err := NewChecker("owner/repository")
	if err != nil {
		t.Fatal(err)
	}
	checker.apiURL = server.URL
	checker.retryDelay = 0

	_, err = checker.Check(context.Background(), CheckOptions{CurrentVersion: "v1.0.0"})
	if !errors.Is(err, errUntrustedRedirect) {
		t.Fatalf("error = %v, want untrusted redirect", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestRetryDelayPolicy(t *testing.T) {
	now := time.Unix(1_000, 0).UTC()
	header := make(http.Header)
	header.Set("Retry-After", "120")
	header.Set("X-RateLimit-Remaining", "0")
	header.Set("X-RateLimit-Reset", "1300")
	delay, ok := retryDelayFromHeaders(header, now)
	if !ok || delay != 2*time.Minute {
		t.Fatalf("header delay = %v, %v", delay, ok)
	}
	if fallback := checkRetryDelayFor(&transientCheckError{rateLimited: true}, checkRetryDelay, 3); fallback != 4*time.Minute {
		t.Fatalf("fallback delay = %v", fallback)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type closeSignalBody struct {
	io.Reader
	closed chan struct{}
}

func (b *closeSignalBody) Close() error {
	close(b.closed)
	return nil
}

func checkerForServer(t *testing.T, server *httptest.Server) *Checker {
	t.Helper()
	checker, err := NewChecker("owner/repository")
	if err != nil {
		t.Fatal(err)
	}
	checker.client = server.Client()
	checker.apiURL = server.URL
	checker.retryDelay = 0
	checker.now = func() time.Time { return time.Unix(1_000, 0).UTC() }
	return checker
}

func writeJSON(response http.ResponseWriter, body string) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = io.WriteString(response, body)
}
