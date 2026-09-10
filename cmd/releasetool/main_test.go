package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fcying/CommandTrayHostGo/internal/updater"
)

func TestRunReleaseFlow(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, updater.ManifestEntry)
	licensePath := filepath.Join(directory, updater.LicenseEntry)
	packagePath := filepath.Join(directory, "CommandTrayHost-v3.0.0-windows-amd64.zip")
	manifestPath := filepath.Join(directory, "CommandTrayHost-v3.0.0-update.json")
	signaturePath := manifestPath + ".sig"
	if err := os.WriteFile(executable, []byte("release executable fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(licensePath, []byte("MIT License fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"version", "--version", "v3.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"package", "--exe", executable, "--license", licensePath, "--out", packagePath, "--modified", "2026-09-10T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CTH_UPDATE_PRIVATE_KEY", base64.StdEncoding.EncodeToString(privateKey.Seed()))
	t.Setenv("CTH_UPDATE_PUBLIC_KEY", base64.StdEncoding.EncodeToString(publicKey))
	if err := run([]string{
		"manifest",
		"--repository", "owner/repository",
		"--version", "v3.0.0",
		"--package", packagePath,
		"--out", manifestPath,
		"--signature", signaturePath,
	}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{
		"verify",
		"--repository", "owner/repository",
		"--version", "v3.0.0",
		"--manifest", manifestPath,
		"--signature", signaturePath,
		"--package", packagePath,
	}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{
		"verify",
		"--repository", "other/repository",
		"--version", "v3.0.0",
		"--manifest", manifestPath,
		"--signature", signaturePath,
		"--package", packagePath,
	}); err == nil {
		t.Fatal("verify accepted the wrong repository")
	}
	if err := os.WriteFile(packagePath, []byte("replaced package"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{
		"verify",
		"--repository", "owner/repository",
		"--version", "v3.0.0",
		"--manifest", manifestPath,
		"--signature", signaturePath,
		"--package", packagePath,
	}); err == nil {
		t.Fatal("verify accepted a replaced package")
	}
}

func TestRunRejectsMissingArguments(t *testing.T) {
	for _, arguments := range [][]string{
		{},
		{"source-version"},
		{"version"},
		{"windows-resource"},
		{"windows-resource", "--version", "v3.0.0"},
		{"package"},
		{"package", "--exe", "CommandTrayHost.exe", "--out", "package.zip"},
		{"package", "--exe", "CommandTrayHost.exe", "--license", "LICENSE", "--out", "package.zip"},
		{"pe-resources"},
		{"manifest"},
		{"verify"},
		{"package", "extra"},
	} {
		if err := run(arguments); err == nil {
			t.Fatalf("run(%q) succeeded", arguments)
		}
	}
}

func TestRunPEResourcesRejectsNonPEFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-pe.exe")
	if err := os.WriteFile(path, []byte("not a PE executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"pe-resources", "--exe", path, "--version", "v3.0.0"}); err == nil {
		t.Fatal("pe-resources accepted a non-PE file")
	}
}

func TestReadSourceVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "about.go")
	if err := os.WriteFile(path, []byte("package app\r\n\r\nvar Version = \"v3.2.1-rc.4\"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	version, err := readSourceVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	if version != "v3.2.1-rc.4" {
		t.Fatalf("readSourceVersion() = %q", version)
	}
	if err := os.WriteFile(path, []byte("var Version = \"v3.2.1\"\nvar Version = \"v3.2.2\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSourceVersion(path); err == nil {
		t.Fatal("readSourceVersion accepted multiple version declarations")
	}
}

func TestVersionInfoMatchesDisplayedVersion(t *testing.T) {
	info, err := makeVersionInfo("dev-v3.2.1-rc.4")
	if err != nil {
		t.Fatal(err)
	}
	if info.FileVersion != [4]uint16{3, 2, 1, 0} || info.ProductVersion != [4]uint16{3, 2, 1, 0} {
		t.Fatalf("fixed Windows versions = %v/%v", info.FileVersion, info.ProductVersion)
	}
	if err := validateVersionInfo(info.Bytes(), "dev-v3.2.1-rc.4"); err != nil {
		t.Fatal(err)
	}
	if err := validateVersionInfo(info.Bytes(), "dev-v3.2.2"); err == nil {
		t.Fatal("version validation accepted mismatched executable metadata")
	}
	if _, err := windowsVersionNumbers("v65536.0.0"); err == nil {
		t.Fatal("version resource accepted a component larger than uint16")
	}
}

func TestValidateRequiredPEResources(t *testing.T) {
	data, contents := buildPEResourceFixture(map[uint32][]uint32{
		3:  {1, 2},
		14: {1, 2},
		16: {1},
		24: {1},
	})
	if err := validateRequiredPEResources(data, 0, fixtureResourceReader(contents), "v3.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := validateDPIManifest([]byte(`<?xml version="1.0"?><assembly xmlns="urn:schemas-microsoft-com:asm.v1"><dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2</dpiAwareness></assembly>`)); err == nil {
		t.Fatal("manifest validation accepted dpiAwareness outside application/windowsSettings")
	}
	if err := validateDPIManifest([]byte(`<?xml version="1.0"?><assembly xmlns="urn:schemas-microsoft-com:asm.v1"><application xmlns="urn:schemas-microsoft-com:asm.v3"><windowsSettings><dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitor,PerMonitorV2</dpiAwareness></windowsSettings></application></assembly>`)); err == nil {
		t.Fatal("manifest validation accepted PerMonitor before PerMonitorV2")
	}
	missingSmallIcon, missingContents := buildPEResourceFixture(map[uint32][]uint32{
		3:  {1},
		14: {1},
		16: {1},
		24: {1},
	})
	if err := validateRequiredPEResources(missingSmallIcon, 0, fixtureResourceReader(missingContents), "v3.0.0"); err == nil {
		t.Fatal("resource validation accepted a PE without the small icon group")
	}
	missingVersion, missingVersionContents := buildPEResourceFixture(map[uint32][]uint32{
		3:  {1, 2},
		14: {1, 2},
		24: {1},
	})
	if err := validateRequiredPEResources(missingVersion, 0, fixtureResourceReader(missingVersionContents), "v3.0.0"); err == nil {
		t.Fatal("resource validation accepted a PE without version information")
	}
	delete(contents, uint32(0x1000+14*0x10+1))
	if err := validateRequiredPEResources(data, 0, fixtureResourceReader(contents), "v3.0.0"); err == nil {
		t.Fatal("resource validation accepted an unreadable resource RVA")
	}
}

func fixtureResourceReader(contents map[uint32][]byte) func(uint32, uint32) ([]byte, error) {
	return func(rva, size uint32) ([]byte, error) {
		content, ok := contents[rva]
		if !ok || uint32(len(content)) != size {
			return nil, errors.New("resource data is unavailable")
		}
		return content, nil
	}
}

func buildPEResourceFixture(resources map[uint32][]uint32) ([]byte, map[uint32][]byte) {
	typeIDs := []uint32{3, 14, 16, 24}
	rootSize := 16 + len(typeIDs)*8
	typeOffsets := make(map[uint32]int, len(typeIDs))
	languageOffsets := make(map[[2]uint32]int)
	dataOffsets := make(map[[2]uint32]int)
	cursor := rootSize
	for _, typeID := range typeIDs {
		typeOffsets[typeID] = cursor
		cursor += 16 + len(resources[typeID])*8
	}
	for _, typeID := range typeIDs {
		for _, id := range resources[typeID] {
			key := [2]uint32{typeID, id}
			languageOffsets[key] = cursor
			cursor += 24
		}
	}
	for _, typeID := range typeIDs {
		for _, id := range resources[typeID] {
			key := [2]uint32{typeID, id}
			dataOffsets[key] = cursor
			cursor += 16
		}
	}
	data := make([]byte, cursor)
	contents := make(map[uint32][]byte)
	binary.LittleEndian.PutUint16(data[14:16], uint16(len(typeIDs)))
	for typeIndex, typeID := range typeIDs {
		entryOffset := 16 + typeIndex*8
		binary.LittleEndian.PutUint32(data[entryOffset:entryOffset+4], typeID)
		binary.LittleEndian.PutUint32(data[entryOffset+4:entryOffset+8], 0x80000000|uint32(typeOffsets[typeID]))
		typeOffset := typeOffsets[typeID]
		ids := resources[typeID]
		binary.LittleEndian.PutUint16(data[typeOffset+14:typeOffset+16], uint16(len(ids)))
		for idIndex, id := range ids {
			key := [2]uint32{typeID, id}
			idEntryOffset := typeOffset + 16 + idIndex*8
			binary.LittleEndian.PutUint32(data[idEntryOffset:idEntryOffset+4], id)
			binary.LittleEndian.PutUint32(data[idEntryOffset+4:idEntryOffset+8], 0x80000000|uint32(languageOffsets[key]))
			languageOffset := languageOffsets[key]
			binary.LittleEndian.PutUint16(data[languageOffset+14:languageOffset+16], 1)
			binary.LittleEndian.PutUint32(data[languageOffset+16:languageOffset+20], 1033)
			binary.LittleEndian.PutUint32(data[languageOffset+20:languageOffset+24], uint32(dataOffsets[key]))
			dataOffset := dataOffsets[key]
			rva := uint32(0x1000 + typeID*0x10 + id)
			content := []byte{1, 2, 3, 4}
			if typeID == 14 {
				content = []byte{0, 0, 1, 0, 1, 0, 16, 16, 0, 0, 1, 0, 32, 0, 4, 0, 0, 0, byte(id), 0}
			} else if typeID == 16 {
				info, err := makeVersionInfo("v3.0.0")
				if err != nil {
					panic(err)
				}
				content = info.Bytes()
			} else if typeID == 24 {
				content = []byte(`<?xml version="1.0"?><assembly xmlns="urn:schemas-microsoft-com:asm.v1"><application xmlns="urn:schemas-microsoft-com:asm.v3"><windowsSettings><dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2,PerMonitor</dpiAwareness></windowsSettings></application></assembly>`)
			}
			contents[rva] = content
			binary.LittleEndian.PutUint32(data[dataOffset:dataOffset+4], rva)
			binary.LittleEndian.PutUint32(data[dataOffset+4:dataOffset+8], uint32(len(content)))
		}
	}
	return data, contents
}
