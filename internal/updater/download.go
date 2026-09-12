package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DownloadVerified fetches a signed Windows update package and writes its executable
// to destination. destination must not exist.
func DownloadVerified(ctx context.Context, repository, version, destination string) error {
	if err := ValidateRepository(repository); err != nil {
		return err
	}
	if err := ValidateReleaseVersion(version); err != nil {
		return err
	}
	if destination == "" {
		return errors.New("update destination is required")
	}
	publicKey, err := DecodePublicKey(UpdatePublicKey)
	if err != nil {
		return fmt.Errorf("decode embedded update public key: %w", err)
	}
	base := "https://github.com/" + repository + "/releases/download/" + url.PathEscape(version) + "/"
	manifestName := "CommandTrayHostGo-" + version + "-update.json"
	manifest, err := downloadUpdateAsset(ctx, base+manifestName, maxAPIBytes)
	if err != nil {
		return fmt.Errorf("download update manifest: %w", err)
	}
	signature, err := downloadUpdateAsset(ctx, base+manifestName+".sig", 512)
	if err != nil {
		return fmt.Errorf("download update manifest signature: %w", err)
	}
	decodedSignature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signature)))
	if err != nil {
		return fmt.Errorf("decode update manifest signature: %w", err)
	}
	if err := VerifyManifest(manifest, decodedSignature, publicKey); err != nil {
		return fmt.Errorf("verify update manifest: %w", err)
	}
	decoded, err := DecodeManifest(manifest)
	if err != nil {
		return err
	}
	if decoded.Repository != repository || decoded.Version != version {
		return errors.New("update manifest does not match the requested release")
	}
	asset := decoded.Assets[0]
	packageData, err := downloadUpdateAsset(ctx, base+asset.Name, MaxPackageSize)
	if err != nil {
		return fmt.Errorf("download update package: %w", err)
	}
	if int64(len(packageData)) != asset.Size || sha256Hex(packageData) != asset.SHA256 {
		return errors.New("downloaded update package does not match its manifest")
	}
	return extractVerifiedExecutable(packageData, asset, destination)
}

func downloadUpdateAsset(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 || request.URL.Scheme != "https" || request.URL.User != nil {
			return errUntrustedRedirect
		}
		host := request.URL.Hostname()
		if host != "github.com" && host != "objects.githubusercontent.com" && !strings.HasSuffix(host, ".githubusercontent.com") {
			return errUntrustedRedirect
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update asset returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength <= 0 || response.ContentLength > limit {
		return nil, fmt.Errorf("update asset size %d is outside the allowed range", response.ContentLength)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != response.ContentLength || int64(len(data)) > limit {
		return nil, errors.New("update asset size does not match its response")
	}
	return data, nil
}

func extractVerifiedExecutable(packageData []byte, asset ManifestAsset, destination string) error {
	archive, err := zip.NewReader(bytes.NewReader(packageData), int64(len(packageData)))
	if err != nil {
		return fmt.Errorf("open update package: %w", err)
	}
	if len(archive.File) != 2 || archive.File[0].Name != asset.Entry || archive.File[1].Name != LicenseEntry {
		return errors.New("update package has unexpected entries")
	}
	entry := archive.File[0]
	if entry.UncompressedSize64 != uint64(asset.EntrySize) {
		return errors.New("update executable size does not match its manifest")
	}
	input, err := entry.Open()
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		output.Close()
		if remove {
			os.Remove(destination)
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(output, hash), io.LimitReader(input, MaxPackageSize+1))
	if err != nil {
		return err
	}
	if written != asset.EntrySize || hex.EncodeToString(hash.Sum(nil)) != asset.EntrySHA256 {
		return errors.New("update executable does not match its manifest")
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	remove = false
	return nil
}

func sha256Hex(value []byte) string {
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}
