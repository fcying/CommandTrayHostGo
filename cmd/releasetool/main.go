package main

import (
	"bytes"
	"debug/pe"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fcying/CommandTrayHostGo/internal/updater"
	"github.com/tc-hib/winres"
	winversion "github.com/tc-hib/winres/version"
)

const (
	versionCompanyName     = "fcying"
	versionFileDescription = "CommandTrayHost"
	versionInternalName    = "CommandTrayHost"
	versionOriginalName    = "CommandTrayHost.exe"
	versionProductName     = "CommandTrayHost"
	versionCopyright       = "Copyright (c) 2017 rexdf; Copyright (c) 2026 fcying"
)

var sourceVersionDeclaration = regexp.MustCompile(`(?m)^var Version = "(v[^"]+)"\r?$`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("usage: releasetool <source-version|version|windows-resource|pe-resources|package|manifest|verify>")
	}
	switch arguments[0] {
	case "source-version":
		flags := flag.NewFlagSet("source-version", flag.ContinueOnError)
		source := flags.String("source", "", "path to the Go version source")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if err := rejectArguments(flags); err != nil {
			return err
		}
		if *source == "" {
			return errors.New("source-version requires --source")
		}
		version, err := readSourceVersion(*source)
		if err != nil {
			return err
		}
		fmt.Println(version)
		return nil
	case "windows-resource":
		flags := flag.NewFlagSet("windows-resource", flag.ContinueOnError)
		version := flags.String("version", "", "version shown by the executable")
		manifest := flags.String("manifest", "", "application manifest path")
		icon := flags.String("icon", "", "large application icon path")
		smallIcon := flags.String("small-icon", "", "small application icon path")
		output := flags.String("out", "", "output amd64 COFF resource path")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if err := rejectArguments(flags); err != nil {
			return err
		}
		if *version == "" || *manifest == "" || *icon == "" || *smallIcon == "" || *output == "" {
			return errors.New("windows-resource requires --version, --manifest, --icon, --small-icon, and --out")
		}
		data, err := buildWindowsResource(*version, *manifest, *icon, *smallIcon)
		if err != nil {
			return err
		}
		return writeGeneratedFile(*output, data)
	case "version":
		flags := flag.NewFlagSet("version", flag.ContinueOnError)
		version := flags.String("version", "", "exact SemVer tag")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if err := rejectArguments(flags); err != nil {
			return err
		}
		return updater.ValidateReleaseVersion(*version)
	case "package":
		flags := flag.NewFlagSet("package", flag.ContinueOnError)
		executable := flags.String("exe", "", "path to CommandTrayHost.exe")
		license := flags.String("license", "", "path to LICENSE")
		output := flags.String("out", "", "output ZIP path")
		modified := flags.String("modified", "", "RFC3339 modification time for ZIP entries")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if err := rejectArguments(flags); err != nil {
			return err
		}
		if *executable == "" || *license == "" || *output == "" || *modified == "" {
			return errors.New("package requires --exe, --license, --out, and --modified")
		}
		modifiedTime, err := time.Parse(time.RFC3339, *modified)
		if err != nil {
			return fmt.Errorf("parse package modification time: %w", err)
		}
		return updater.CreatePackage(*executable, *license, *output, modifiedTime)
	case "pe-resources":
		flags := flag.NewFlagSet("pe-resources", flag.ContinueOnError)
		executable := flags.String("exe", "", "path to CommandTrayHost.exe")
		version := flags.String("version", "", "expected version shown by the executable")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if err := rejectArguments(flags); err != nil {
			return err
		}
		if *executable == "" || *version == "" {
			return errors.New("pe-resources requires --exe and --version")
		}
		return validatePEResources(*executable, *version)
	case "manifest":
		flags := flag.NewFlagSet("manifest", flag.ContinueOnError)
		repository := flags.String("repository", "", "GitHub owner/name")
		version := flags.String("version", "", "exact SemVer tag")
		packagePath := flags.String("package", "", "release ZIP path")
		output := flags.String("out", "", "manifest output path")
		signaturePath := flags.String("signature", "", "signature output path")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if err := rejectArguments(flags); err != nil {
			return err
		}
		if *repository == "" || *version == "" || *packagePath == "" || *output == "" || *signaturePath == "" {
			return errors.New("manifest requires --repository, --version, --package, --out, and --signature")
		}
		manifest, err := updater.BuildManifest(*repository, *version, *packagePath)
		if err != nil {
			return err
		}
		data, err := updater.EncodeManifest(manifest)
		if err != nil {
			return err
		}
		privateKey, err := updater.DecodePrivateKey(os.Getenv("CTH_UPDATE_PRIVATE_KEY"))
		if err != nil {
			return fmt.Errorf("CTH_UPDATE_PRIVATE_KEY: %w", err)
		}
		signature, err := updater.SignManifest(data, privateKey)
		if err != nil {
			return err
		}
		if err := writeExclusive(*output, data); err != nil {
			return err
		}
		if err := writeExclusive(*signaturePath, []byte(base64.StdEncoding.EncodeToString(signature)+"\n")); err != nil {
			os.Remove(*output)
			return err
		}
		return nil
	case "verify":
		flags := flag.NewFlagSet("verify", flag.ContinueOnError)
		repository := flags.String("repository", "", "expected GitHub owner/name")
		version := flags.String("version", "", "expected exact SemVer tag")
		manifestPath := flags.String("manifest", "", "manifest path")
		signaturePath := flags.String("signature", "", "signature path")
		packagePath := flags.String("package", "", "release ZIP path")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if err := rejectArguments(flags); err != nil {
			return err
		}
		if *repository == "" || *version == "" || *manifestPath == "" || *signaturePath == "" || *packagePath == "" {
			return errors.New("verify requires --repository, --version, --manifest, --signature, and --package")
		}
		data, err := os.ReadFile(*manifestPath)
		if err != nil {
			return err
		}
		signatureText, err := os.ReadFile(*signaturePath)
		if err != nil {
			return err
		}
		signature, err := base64.StdEncoding.DecodeString(string(signatureText))
		if err != nil {
			return err
		}
		publicKey, err := updater.DecodePublicKey(os.Getenv("CTH_UPDATE_PUBLIC_KEY"))
		if err != nil {
			return fmt.Errorf("CTH_UPDATE_PUBLIC_KEY: %w", err)
		}
		if err := updater.VerifyManifest(data, signature, publicKey); err != nil {
			return err
		}
		manifest, err := updater.DecodeManifest(data)
		if err != nil {
			return err
		}
		if manifest.Repository != *repository || manifest.Version != *version {
			return errors.New("signed manifest does not match the expected repository and version")
		}
		actual, err := updater.BuildManifest(*repository, *version, *packagePath)
		if err != nil {
			return err
		}
		if actual.Assets[0] != manifest.Assets[0] {
			return errors.New("package does not match signed manifest")
		}
		return nil
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func readSourceVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read version source: %w", err)
	}
	matches := sourceVersionDeclaration.FindAllSubmatch(data, -1)
	if len(matches) != 1 {
		return "", errors.New("version source must contain exactly one release Version declaration")
	}
	version := string(matches[0][1])
	if err := updater.ValidateReleaseVersion(version); err != nil {
		return "", fmt.Errorf("version source: %w", err)
	}
	return version, nil
}

func windowsVersionNumbers(value string) ([4]uint16, error) {
	releaseVersion := strings.TrimPrefix(value, "dev-")
	if err := updater.ValidateReleaseVersion(releaseVersion); err != nil {
		return [4]uint16{}, fmt.Errorf("version resource: %w", err)
	}
	core := strings.TrimPrefix(releaseVersion, "v")
	if separator := strings.IndexAny(core, "-+"); separator >= 0 {
		core = core[:separator]
	}
	parts := strings.Split(core, ".")
	var numbers [4]uint16
	for index, part := range parts {
		number, err := strconv.ParseUint(part, 10, 16)
		if err != nil {
			return [4]uint16{}, fmt.Errorf("version resource component %q exceeds 65535", part)
		}
		numbers[index] = uint16(number)
	}
	return numbers, nil
}

func makeVersionInfo(value string) (winversion.Info, error) {
	numbers, err := windowsVersionNumbers(value)
	if err != nil {
		return winversion.Info{}, err
	}
	info := winversion.Info{FileVersion: numbers, ProductVersion: numbers}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: winversion.CompanyName, value: versionCompanyName},
		{name: winversion.FileDescription, value: versionFileDescription},
		{name: winversion.FileVersion, value: value},
		{name: winversion.InternalName, value: versionInternalName},
		{name: winversion.LegalCopyright, value: versionCopyright},
		{name: winversion.OriginalFilename, value: versionOriginalName},
		{name: winversion.ProductName, value: versionProductName},
		{name: winversion.ProductVersion, value: value},
	} {
		if err := info.Set(winversion.LangDefault, field.name, field.value); err != nil {
			return winversion.Info{}, err
		}
	}
	return info, nil
}

func buildWindowsResource(value, manifestPath, iconPath, smallIconPath string) ([]byte, error) {
	info, err := makeVersionInfo(value)
	if err != nil {
		return nil, err
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read application manifest: %w", err)
	}
	if err := validateDPIManifest(manifest); err != nil {
		return nil, err
	}
	icon, err := loadIcon(iconPath)
	if err != nil {
		return nil, err
	}
	smallIcon, err := loadIcon(smallIconPath)
	if err != nil {
		return nil, err
	}
	resources := winres.ResourceSet{}
	if err := resources.SetIconTranslation(winres.ID(1), winversion.LangDefault, icon); err != nil {
		return nil, fmt.Errorf("add large application icon: %w", err)
	}
	if err := resources.SetIconTranslation(winres.ID(2), winversion.LangDefault, smallIcon); err != nil {
		return nil, fmt.Errorf("add small application icon: %w", err)
	}
	if err := resources.Set(winres.RT_MANIFEST, winres.ID(1), winversion.LangDefault, manifest); err != nil {
		return nil, fmt.Errorf("add application manifest: %w", err)
	}
	resources.SetVersionInfo(info)
	var output bytes.Buffer
	if err := resources.WriteObject(&output, winres.ArchAMD64); err != nil {
		return nil, fmt.Errorf("build amd64 Windows resource: %w", err)
	}
	return output.Bytes(), nil
}

func loadIcon(path string) (*winres.Icon, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open icon %q: %w", path, err)
	}
	defer file.Close()
	icon, err := winres.LoadICO(file)
	if err != nil {
		return nil, fmt.Errorf("read icon %q: %w", path, err)
	}
	return icon, nil
}

func writeGeneratedFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write generated resource: %w", err)
	}
	return nil
}

func validatePEResources(path, expectedVersion string) error {
	if _, err := windowsVersionNumbers(expectedVersion); err != nil {
		return err
	}
	file, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("open PE executable: %w", err)
	}
	defer file.Close()
	if file.FileHeader.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return fmt.Errorf("PE executable machine is %#x, want amd64", file.FileHeader.Machine)
	}
	header, ok := file.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		return errors.New("PE executable does not have a 64-bit optional header")
	}
	resources := header.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_RESOURCE]
	if resources.VirtualAddress == 0 || resources.Size == 0 {
		return errors.New("PE executable has no resource directory")
	}
	sections := make([]peResourceSection, 0, len(file.Sections))
	for _, section := range file.Sections {
		data, err := section.Data()
		if err != nil {
			return fmt.Errorf("read PE section %q: %w", section.Name, err)
		}
		sections = append(sections, peResourceSection{virtualAddress: section.VirtualAddress, data: data})
	}
	for _, section := range sections {
		resourceRVA := uint64(resources.VirtualAddress)
		sectionStart := uint64(section.virtualAddress)
		if resourceRVA < sectionStart || resourceRVA >= sectionStart+uint64(len(section.data)) {
			continue
		}
		rootOffset := int(resourceRVA - sectionStart)
		return validateRequiredPEResources(section.data, rootOffset, func(rva, size uint32) ([]byte, error) {
			return readPEResourceData(sections, rva, size)
		}, expectedVersion)
	}
	return errors.New("PE resource directory is outside the section table")
}

type peResourceSection struct {
	virtualAddress uint32
	data           []byte
}

func readPEResourceData(sections []peResourceSection, rva, size uint32) ([]byte, error) {
	if rva == 0 || size == 0 {
		return nil, errors.New("resource data has an empty RVA or size")
	}
	for _, section := range sections {
		if rva < section.virtualAddress {
			continue
		}
		offset := uint64(rva - section.virtualAddress)
		end := offset + uint64(size)
		if end < offset || end > uint64(len(section.data)) {
			continue
		}
		return section.data[offset:end], nil
	}
	return nil, errors.New("resource data is outside PE section raw data")
}

func validateRequiredPEResources(data []byte, rootOffset int, readData func(uint32, uint32) ([]byte, error), expectedVersion string) error {
	for _, group := range []struct {
		id   uint32
		name string
	}{
		{id: 1, name: "large icon group"},
		{id: 2, name: "small icon group"},
	} {
		content, err := readPEResource(data, rootOffset, 14, group.id, readData)
		if err != nil {
			return fmt.Errorf("PE executable is missing %s (type 14, ID %d): %w", group.name, group.id, err)
		}
		entries, err := parseGroupIcon(content)
		if err != nil {
			return fmt.Errorf("PE executable has an invalid %s: %w", group.name, err)
		}
		for _, entry := range entries {
			icon, err := readPEResource(data, rootOffset, 3, uint32(entry.resourceID), readData)
			if err != nil {
				return fmt.Errorf("PE executable %s references missing icon ID %d: %w", group.name, entry.resourceID, err)
			}
			if uint32(len(icon)) != entry.size {
				return fmt.Errorf("PE executable %s icon ID %d has size %d, want %d", group.name, entry.resourceID, len(icon), entry.size)
			}
		}
	}
	manifest, err := readPEResource(data, rootOffset, 24, 1, readData)
	if err != nil {
		return fmt.Errorf("PE executable is missing application manifest (type 24, ID 1): %w", err)
	}
	if err := validateDPIManifest(manifest); err != nil {
		return err
	}
	versionInfo, err := readPEResource(data, rootOffset, 16, 1, readData)
	if err != nil {
		return fmt.Errorf("PE executable is missing version information (type 16, ID 1): %w", err)
	}
	return validateVersionInfo(versionInfo, expectedVersion)
}

func readPEResource(data []byte, rootOffset int, typeID, resourceID uint32, readData func(uint32, uint32) ([]byte, error)) ([]byte, error) {
	typeDirectory, err := resourceSubdirectory(data, rootOffset, rootOffset, typeID)
	if err != nil {
		return nil, err
	}
	languageDirectory, err := resourceSubdirectory(data, rootOffset, typeDirectory, resourceID)
	if err != nil {
		return nil, err
	}
	entries, err := resourceIDEntries(data, languageDirectory)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.offset&0x80000000 != 0 {
			continue
		}
		dataOffset := rootOffset + int(entry.offset)
		if dataOffset < 0 || dataOffset > len(data)-16 {
			continue
		}
		rva := binary.LittleEndian.Uint32(data[dataOffset : dataOffset+4])
		size := binary.LittleEndian.Uint32(data[dataOffset+4 : dataOffset+8])
		content, err := readData(rva, size)
		if err == nil {
			return content, nil
		}
	}
	return nil, errors.New("resource has no readable language data entry")
}

type groupIconEntry struct {
	size       uint32
	resourceID uint16
}

func parseGroupIcon(data []byte) ([]groupIconEntry, error) {
	if len(data) < 6 {
		return nil, errors.New("group icon header is truncated")
	}
	if binary.LittleEndian.Uint16(data[0:2]) != 0 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
		return nil, errors.New("group icon header is invalid")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 || len(data) != 6+count*14 {
		return nil, errors.New("group icon entries are truncated")
	}
	entries := make([]groupIconEntry, count)
	for index := range entries {
		offset := 6 + index*14
		if data[offset+3] != 0 {
			return nil, errors.New("group icon entry has a non-zero reserved byte")
		}
		entries[index] = groupIconEntry{
			size:       binary.LittleEndian.Uint32(data[offset+8 : offset+12]),
			resourceID: binary.LittleEndian.Uint16(data[offset+12 : offset+14]),
		}
		if entries[index].size == 0 || entries[index].resourceID == 0 {
			return nil, errors.New("group icon entry has an empty size or resource ID")
		}
	}
	return entries, nil
}

func validateDPIManifest(data []byte) error {
	var manifest struct {
		XMLName     xml.Name `xml:"urn:schemas-microsoft-com:asm.v1 assembly"`
		Application struct {
			WindowsSettings struct {
				DPIAwareness string  `xml:"http://schemas.microsoft.com/SMI/2016/WindowsSettings dpiAwareness"`
				GDIScaling   *string `xml:"http://schemas.microsoft.com/SMI/2017/WindowsSettings gdiScaling"`
			} `xml:"urn:schemas-microsoft-com:asm.v3 windowsSettings"`
		} `xml:"urn:schemas-microsoft-com:asm.v3 application"`
	}
	if err := xml.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("PE application manifest is invalid XML or has an invalid root element: %w", err)
	}
	if manifest.Application.WindowsSettings.GDIScaling != nil {
		return errors.New("PE application manifest must not enable gdiScaling")
	}
	modes := strings.Split(manifest.Application.WindowsSettings.DPIAwareness, ",")
	if strings.TrimSpace(modes[0]) == "PerMonitorV2" {
		return nil
	}
	if manifest.Application.WindowsSettings.DPIAwareness == "" {
		return errors.New("PE application manifest does not contain the required assembly/application/windowsSettings/dpiAwareness path")
	}
	if !strings.Contains(manifest.Application.WindowsSettings.DPIAwareness, "PerMonitorV2") {
		return errors.New("PE application manifest does not enable PerMonitorV2 dpiAwareness")
	}
	return errors.New("PE application manifest has an invalid PerMonitorV2 dpiAwareness value")
}

func validateVersionInfo(data []byte, expectedVersion string) error {
	info, err := winversion.FromBytes(data)
	if err != nil {
		return fmt.Errorf("PE version information is invalid: %w", err)
	}
	numbers, err := windowsVersionNumbers(expectedVersion)
	if err != nil {
		return err
	}
	if info.FileVersion != numbers {
		return fmt.Errorf("PE fixed file version is %v, want %v", info.FileVersion, numbers)
	}
	if info.ProductVersion != numbers {
		return fmt.Errorf("PE fixed product version is %v, want %v", info.ProductVersion, numbers)
	}
	table := info.Table()[winversion.LangDefault]
	if table == nil {
		return errors.New("PE version information has no en-US string table")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: winversion.CompanyName, value: versionCompanyName},
		{name: winversion.FileDescription, value: versionFileDescription},
		{name: winversion.FileVersion, value: expectedVersion},
		{name: winversion.InternalName, value: versionInternalName},
		{name: winversion.LegalCopyright, value: versionCopyright},
		{name: winversion.OriginalFilename, value: versionOriginalName},
		{name: winversion.ProductName, value: versionProductName},
		{name: winversion.ProductVersion, value: expectedVersion},
	} {
		if actual := (*table)[field.name]; actual != field.value {
			return fmt.Errorf("PE version information %s is %q, want %q", field.name, actual, field.value)
		}
	}
	return nil
}

type resourceDirectoryEntry struct {
	name   uint32
	offset uint32
}

func resourceSubdirectory(data []byte, rootOffset, directoryOffset int, id uint32) (int, error) {
	entries, err := resourceIDEntries(data, directoryOffset)
	if err != nil {
		return 0, err
	}
	for _, entry := range entries {
		if entry.name&0x80000000 != 0 || entry.name != id {
			continue
		}
		if entry.offset&0x80000000 == 0 {
			return 0, errors.New("resource directory entry points directly to data")
		}
		offset := rootOffset + int(entry.offset&0x7fffffff)
		if offset < 0 || offset > len(data)-16 {
			return 0, errors.New("resource subdirectory is outside the section")
		}
		return offset, nil
	}
	return 0, errors.New("resource directory ID not found")
}

func resourceIDEntries(data []byte, offset int) ([]resourceDirectoryEntry, error) {
	if offset < 0 || offset > len(data)-16 {
		return nil, errors.New("resource directory is outside the section")
	}
	namedCount := int(binary.LittleEndian.Uint16(data[offset+12 : offset+14]))
	idCount := int(binary.LittleEndian.Uint16(data[offset+14 : offset+16]))
	count := namedCount + idCount
	if count > (len(data)-offset-16)/8 {
		return nil, errors.New("resource directory entries exceed the section")
	}
	for index := 0; index < namedCount; index++ {
		entryOffset := offset + 16 + index*8
		if binary.LittleEndian.Uint32(data[entryOffset:entryOffset+4])&0x80000000 == 0 {
			return nil, errors.New("resource named entry does not contain a string name")
		}
	}
	entries := make([]resourceDirectoryEntry, idCount)
	for index := range entries {
		entryOffset := offset + 16 + (namedCount+index)*8
		entries[index] = resourceDirectoryEntry{
			name:   binary.LittleEndian.Uint32(data[entryOffset : entryOffset+4]),
			offset: binary.LittleEndian.Uint32(data[entryOffset+4 : entryOffset+8]),
		}
		if entries[index].name&0x80000000 != 0 {
			return nil, errors.New("resource ID entry contains a string name")
		}
	}
	return entries, nil
}

func rejectArguments(flags *flag.FlagSet) error {
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	return nil
}

func writeExclusive(path string, data []byte) error {
	if path == "" {
		return errors.New("output path must not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		os.Remove(path)
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}
