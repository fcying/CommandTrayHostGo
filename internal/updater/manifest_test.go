package updater

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManifestRoundTripAndSignature(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, ManifestEntry)
	license := filepath.Join(directory, LicenseEntry)
	if err := os.WriteFile(executable, []byte("portable executable fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(license, []byte("MIT License fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(directory, "CommandTrayHost-v3.0.0-windows-amd64.zip")
	if err := CreatePackage(executable, license, packagePath, time.Date(2026, time.September, 10, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	manifest, err := BuildManifest("owner/repository", "v3.0.0", packagePath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := EncodeManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Assets[0].EntrySHA256 == decoded.Assets[0].SHA256 || decoded.Assets[0].EntrySize == decoded.Assets[0].Size {
		t.Fatalf("manifest did not distinguish ZIP and executable: %+v", decoded.Assets[0])
	}
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := SignManifest(data, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyManifest(data, signature, publicKey); err != nil {
		t.Fatal(err)
	}
	data[0] ^= 1
	if err := VerifyManifest(data, signature, publicKey); err == nil {
		t.Fatal("tampered manifest verified")
	}
}

func TestKeyDecoding(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	decodedPrivate, err := DecodePrivateKey(base64.StdEncoding.EncodeToString(privateKey.Seed()))
	if err != nil || !decodedPrivate.Equal(privateKey) {
		t.Fatalf("DecodePrivateKey() = %v, %v", decodedPrivate, err)
	}
	decodedPublic, err := DecodePublicKey(base64.StdEncoding.EncodeToString(publicKey))
	if err != nil || !decodedPublic.Equal(publicKey) {
		t.Fatalf("DecodePublicKey() = %v, %v", decodedPublic, err)
	}
}

func TestManifestRejectsInvalidPackage(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "CommandTrayHost-v3.0.0-windows-amd64.zip")
	if err := os.WriteFile(path, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildManifest("owner/repository", "v3.0.0", path); err == nil {
		t.Fatal("invalid package succeeded")
	}
}

func TestCreatePackageIsDeterministic(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, ManifestEntry)
	license := filepath.Join(directory, LicenseEntry)
	wantLicense := []byte("MIT License fixture\n")
	if err := os.WriteFile(executable, []byte("deterministic executable fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(license, wantLicense, 0o644); err != nil {
		t.Fatal(err)
	}
	modified := time.Date(2026, time.September, 10, 12, 34, 56, 0, time.UTC)
	first := filepath.Join(directory, "first.zip")
	second := filepath.Join(directory, "second.zip")
	if err := CreatePackage(executable, license, first, modified); err != nil {
		t.Fatal(err)
	}
	if err := CreatePackage(executable, license, second, modified); err != nil {
		t.Fatal(err)
	}
	firstData, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstData, secondData) {
		t.Fatal("identical executables produced different packages")
	}
	archive, err := zip.OpenReader(first)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(archive.File) != 2 || archive.File[0].Name != ManifestEntry || archive.File[1].Name != LicenseEntry {
		t.Fatalf("package entries = %+v", archive.File)
	}
	for _, entry := range archive.File {
		if !entry.Modified.Equal(modified) {
			t.Fatalf("%s modification time = %v, want %v", entry.Name, entry.Modified, modified)
		}
	}
	licenseEntry, err := archive.File[1].Open()
	if err != nil {
		t.Fatal(err)
	}
	licenseData, err := io.ReadAll(licenseEntry)
	if closeErr := licenseEntry.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(licenseData, wantLicense) {
		t.Fatalf("LICENSE = %q", licenseData)
	}
}

func TestManifestStrictDecoding(t *testing.T) {
	manifest := validTestManifest()
	data, err := EncodeManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}

	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	object["unexpected"] = true
	unknown, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeManifest(unknown); err == nil {
		t.Fatal("manifest with unknown field succeeded")
	}
	if _, err := DecodeManifest(append(data, []byte("{}")...)); err == nil {
		t.Fatal("manifest with trailing data succeeded")
	}
	duplicate := bytes.Replace(data, []byte(`"version": "v3.0.0"`), []byte(`"version": "v2.0.0", "version": "v3.0.0"`), 1)
	if _, err := DecodeManifest(duplicate); err == nil {
		t.Fatal("manifest with duplicate field succeeded")
	}
	caseAlias := bytes.Replace(data, []byte(`"schema": 1`), []byte(`"Schema": 1`), 1)
	if _, err := DecodeManifest(caseAlias); err == nil {
		t.Fatal("manifest with case-aliased field succeeded")
	}
	reformatted := bytes.ReplaceAll(data, []byte("  "), []byte("    "))
	if _, err := DecodeManifest(reformatted); err == nil {
		t.Fatal("non-canonical manifest succeeded")
	}

	manifest.Assets[0].Name = "wrong.zip"
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("manifest with wrong asset name succeeded")
	}
	manifest = validTestManifest()
	manifest.Assets[0].SHA256 = strings.ToUpper(manifest.Assets[0].SHA256)
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("manifest with uppercase hash succeeded")
	}
}

func TestManifestRejectsNonCanonicalPackages(t *testing.T) {
	tests := []struct {
		name    string
		entries []testZipEntry
	}{
		{name: "extra entry", entries: []testZipEntry{{name: ManifestEntry, data: "exe"}, {name: LicenseEntry, data: "license"}, {name: "extra.txt", data: "extra"}}},
		{name: "missing license", entries: []testZipEntry{{name: ManifestEntry, data: "exe"}}},
		{name: "executable directory", entries: []testZipEntry{{name: ManifestEntry, directory: true}, {name: LicenseEntry, data: "license"}}},
		{name: "executable symlink", entries: []testZipEntry{{name: ManifestEntry, data: "target", symlink: true}, {name: LicenseEntry, data: "license"}}},
		{name: "wrong executable name", entries: []testZipEntry{{name: "commandtrayhost.exe", data: "exe"}, {name: LicenseEntry, data: "license"}}},
		{name: "license directory", entries: []testZipEntry{{name: ManifestEntry, data: "exe"}, {name: LicenseEntry, directory: true}}},
		{name: "license symlink", entries: []testZipEntry{{name: ManifestEntry, data: "exe"}, {name: LicenseEntry, data: "target", symlink: true}}},
		{name: "empty license", entries: []testZipEntry{{name: ManifestEntry, data: "exe"}, {name: LicenseEntry}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "package.zip")
			writeTestZip(t, path, test.entries)
			if _, _, err := inspectPackage(path); err == nil {
				t.Fatal("invalid package succeeded")
			}
		})
	}
}

func TestKeyDecodingRejectsInvalidLengths(t *testing.T) {
	for _, value := range []string{"", base64.StdEncoding.EncodeToString(make([]byte, 31)), base64.StdEncoding.EncodeToString(make([]byte, 33))} {
		if _, err := DecodePrivateKey(value); err == nil {
			t.Fatalf("DecodePrivateKey(%q) succeeded", value)
		}
		if _, err := DecodePublicKey(value); err == nil {
			t.Fatalf("DecodePublicKey(%q) succeeded", value)
		}
	}
}

func TestValidateReleaseVersion(t *testing.T) {
	for _, version := range []string{"v1.0.0", "v1.0.0-rc.1", "v18446744073709551616.0.0"} {
		if err := ValidateReleaseVersion(version); err != nil {
			t.Fatalf("ValidateReleaseVersion(%q): %v", version, err)
		}
	}
	for _, version := range []string{"", "development", "2.3-b450", "v1.0.0+build.1", "v1.0.0-dirty", "v1.0.0-1-gabcdef"} {
		if err := ValidateReleaseVersion(version); err == nil {
			t.Fatalf("ValidateReleaseVersion(%q) succeeded", version)
		}
	}
}

func TestValidateRepository(t *testing.T) {
	for _, repository := range []string{"owner/repository", "owner-name/repository.name", "owner/repository_name"} {
		if err := ValidateRepository(repository); err != nil {
			t.Fatalf("ValidateRepository(%q): %v", repository, err)
		}
	}
	for _, repository := range []string{"", "owner", "owner/", "/repository", "owner_name/repository", "owner/.", "owner/..", "owner/repository?query"} {
		if err := ValidateRepository(repository); err == nil {
			t.Fatalf("ValidateRepository(%q) succeeded", repository)
		}
	}
}

func validTestManifest() Manifest {
	hash := strings.Repeat("ab", 32)
	return Manifest{
		Schema:     ManifestSchema,
		Repository: "owner/repository",
		Version:    "v3.0.0",
		Assets: []ManifestAsset{{
			OS:          "windows",
			Arch:        "amd64",
			Name:        "CommandTrayHost-v3.0.0-windows-amd64.zip",
			Size:        1,
			SHA256:      hash,
			Entry:       ManifestEntry,
			EntrySize:   1,
			EntrySHA256: hash,
		}},
	}
}

type testZipEntry struct {
	name      string
	data      string
	directory bool
	symlink   bool
}

func writeTestZip(t *testing.T, path string, entries []testZipEntry) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		switch {
		case entry.directory:
			header.SetMode(os.ModeDir | 0o755)
		case entry.symlink:
			header.SetMode(os.ModeSymlink | 0o777)
		default:
			header.SetMode(0o755)
		}
		writer, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(entry.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
