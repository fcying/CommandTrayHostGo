package updater

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// Schema 1 defines the initial CommandTrayHost update package format.
	ManifestSchema       = 1
	ManifestEntry        = "CommandTrayHost.exe"
	LicenseEntry         = "LICENSE"
	MaxPackageSize int64 = 64 << 20
	maxLicenseSize int64 = 64 << 10
)

type Manifest struct {
	Schema     int             `json:"schema"`
	Repository string          `json:"repository"`
	Version    string          `json:"version"`
	Assets     []ManifestAsset `json:"assets"`
}

type ManifestAsset struct {
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	Entry       string `json:"entry"`
	EntrySize   int64  `json:"entry_size"`
	EntrySHA256 string `json:"entry_sha256"`
}

func BuildManifest(repository, version, packagePath string) (Manifest, error) {
	if err := ValidateRepository(repository); err != nil {
		return Manifest{}, err
	}
	if err := ValidateReleaseVersion(version); err != nil {
		return Manifest{}, err
	}
	packageInfo, err := os.Stat(packagePath)
	if err != nil {
		return Manifest{}, fmt.Errorf("stat package: %w", err)
	}
	if !packageInfo.Mode().IsRegular() {
		return Manifest{}, errors.New("package must be a regular file")
	}
	if packageInfo.Size() <= 0 || packageInfo.Size() > MaxPackageSize {
		return Manifest{}, fmt.Errorf("package size %d is outside the allowed range", packageInfo.Size())
	}
	packageHash, err := fileSHA256(packagePath)
	if err != nil {
		return Manifest{}, err
	}
	entrySize, entryHash, err := inspectPackage(packagePath)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{
		Schema:     ManifestSchema,
		Repository: repository,
		Version:    version,
		Assets: []ManifestAsset{{
			OS:          "windows",
			Arch:        "amd64",
			Name:        filepath.Base(packagePath),
			Size:        packageInfo.Size(),
			SHA256:      packageHash,
			Entry:       ManifestEntry,
			EntrySize:   entrySize,
			EntrySHA256: entryHash,
		}},
	}
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func EncodeManifest(manifest Manifest) ([]byte, error) {
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func DecodeManifest(data []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, errors.New("manifest has trailing data")
	}
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	canonical, err := EncodeManifest(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if !bytes.Equal(data, canonical) {
		return Manifest{}, errors.New("manifest is not canonically encoded")
	}
	return manifest, nil
}

func ValidateManifest(manifest Manifest) error {
	if manifest.Schema != ManifestSchema {
		return fmt.Errorf("unsupported manifest schema %d", manifest.Schema)
	}
	if err := ValidateRepository(manifest.Repository); err != nil {
		return err
	}
	if err := ValidateReleaseVersion(manifest.Version); err != nil {
		return err
	}
	if len(manifest.Assets) != 1 {
		return errors.New("manifest must contain exactly one asset")
	}
	asset := manifest.Assets[0]
	if asset.OS != "windows" || asset.Arch != "amd64" {
		return errors.New("manifest asset must target windows/amd64")
	}
	if asset.Name != "CommandTrayHost-"+manifest.Version+"-windows-amd64.zip" {
		return errors.New("manifest asset name does not match the release version")
	}
	if asset.Size <= 0 || asset.Size > MaxPackageSize || asset.EntrySize <= 0 || asset.EntrySize > MaxPackageSize {
		return errors.New("manifest asset size is outside the allowed range")
	}
	if !validSHA256(asset.SHA256) || !validSHA256(asset.EntrySHA256) {
		return errors.New("manifest SHA-256 values must be lowercase hexadecimal")
	}
	if asset.Entry != ManifestEntry {
		return errors.New("manifest entry must be CommandTrayHost.exe")
	}
	return nil
}

func ValidateReleaseVersion(version string) error {
	if !IsComparable(version) || strings.Contains(version, "+") {
		return fmt.Errorf("release version must be an exact SemVer tag: %s", version)
	}
	return nil
}

func ValidateRepository(repository string) error {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return errors.New("repository must use owner/name format")
	}
	if parts[1] == "." || parts[1] == ".." {
		return errors.New("repository name is invalid")
	}
	for index, part := range parts {
		for _, character := range part {
			allowed := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-'
			if index == 1 {
				allowed = allowed || character == '_' || character == '.'
			}
			if allowed {
				continue
			}
			return errors.New("repository contains unsupported characters")
		}
	}
	return nil
}

func SignManifest(data []byte, privateKey ed25519.PrivateKey) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid Ed25519 private key")
	}
	return ed25519.Sign(privateKey, data), nil
}

func VerifyManifest(data, signature []byte, publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("invalid Ed25519 public key")
	}
	if len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, data, signature) {
		return errors.New("manifest signature verification failed")
	}
	_, err := DecodeManifest(data)
	return err
}

func DecodePrivateKey(value string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("decode private key: %w", err)
	}
	switch len(raw) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	default:
		return nil, errors.New("private key must contain a 32-byte seed or 64-byte private key")
	}
}

func DecodePublicKey(value string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("public key must contain 32 bytes")
	}
	return ed25519.PublicKey(raw), nil
}

func CreatePackage(executablePath, licensePath, packagePath string, modified time.Time) error {
	modified = modified.UTC().Truncate(time.Second)
	if modified.Before(time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)) || modified.After(time.Unix(int64(^uint32(0)), 0)) {
		return errors.New("package modification time is outside the ZIP timestamp range")
	}
	type packageSource struct {
		path    string
		file    *os.File
		name    string
		mode    os.FileMode
		size    int64
		maxSize int64
	}
	sources := [...]packageSource{
		{path: executablePath, name: ManifestEntry, mode: 0o755, maxSize: MaxPackageSize},
		{path: licensePath, name: LicenseEntry, mode: 0o644, maxSize: maxLicenseSize},
	}
	for i := range sources {
		source := &sources[i]
		file, err := os.Open(source.path)
		if err != nil {
			return fmt.Errorf("open %s: %w", source.name, err)
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return fmt.Errorf("stat %s: %w", source.name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s must be a regular file", source.name)
		}
		if info.Size() <= 0 || info.Size() > source.maxSize {
			return fmt.Errorf("%s size %d is outside the allowed range", source.name, info.Size())
		}
		source.file = file
		source.size = info.Size()
	}

	file, err := os.OpenFile(packagePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		file.Close()
		if remove {
			os.Remove(packagePath)
		}
	}()
	archive := zip.NewWriter(file)
	for _, source := range sources {
		header := &zip.FileHeader{Name: source.name, Method: zip.Deflate}
		header.SetMode(source.mode)
		header.Modified = modified
		entry, createErr := archive.CreateHeader(header)
		if createErr != nil {
			err = createErr
			break
		}
		written, copyErr := io.Copy(entry, io.LimitReader(source.file, source.maxSize+1))
		if copyErr != nil {
			err = copyErr
			break
		}
		if written != source.size {
			err = fmt.Errorf("%s changed while creating the package", source.name)
			break
		}
	}
	if closeErr := archive.Close(); err == nil {
		err = closeErr
	}
	if syncErr := file.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	remove = false
	return nil
}

func inspectPackage(path string) (int64, string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return 0, "", fmt.Errorf("open package: %w", err)
	}
	defer archive.Close()
	if len(archive.File) != 2 || archive.File[0].Name != ManifestEntry || archive.File[1].Name != LicenseEntry ||
		archive.File[0].FileInfo().Mode()&os.ModeType != 0 || archive.File[1].FileInfo().Mode()&os.ModeType != 0 {
		return 0, "", errors.New("package must contain exactly regular CommandTrayHost.exe and LICENSE entries")
	}
	if archive.File[1].UncompressedSize64 == 0 || archive.File[1].UncompressedSize64 > uint64(maxLicenseSize) {
		return 0, "", errors.New("package LICENSE size is outside the allowed range")
	}
	executable := archive.File[0]
	if executable.UncompressedSize64 == 0 || executable.UncompressedSize64 > uint64(MaxPackageSize) {
		return 0, "", errors.New("package executable size is outside the allowed range")
	}
	entry, err := executable.Open()
	if err != nil {
		return 0, "", err
	}
	defer entry.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(entry, MaxPackageSize+1))
	if err != nil {
		return 0, "", err
	}
	if written != int64(executable.UncompressedSize64) {
		return 0, "", errors.New("package executable size does not match ZIP metadata")
	}
	if written > MaxPackageSize {
		return 0, "", errors.New("package executable exceeds the allowed size")
	}
	return written, hex.EncodeToString(hash.Sum(nil)), nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, MaxPackageSize+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
