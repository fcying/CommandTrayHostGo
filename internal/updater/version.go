package updater

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type Version struct {
	raw        string
	major      string
	minor      string
	patch      string
	prerelease []string
}

func ParseVersion(value string) (Version, error) {
	if version, ok := parseSemver(value); ok {
		return version, nil
	}
	return Version{}, fmt.Errorf("unsupported version %q", value)
}

func (v Version) String() string {
	return v.raw
}

func Compare(left, right Version) int {
	if comparison := compareNumericString(left.major, right.major); comparison != 0 {
		return comparison
	}
	if comparison := compareNumericString(left.minor, right.minor); comparison != 0 {
		return comparison
	}
	if comparison := compareNumericString(left.patch, right.patch); comparison != 0 {
		return comparison
	}
	return comparePrerelease(left.prerelease, right.prerelease)
}

func parseSemver(value string) (Version, bool) {
	if !strings.HasPrefix(value, "v") {
		return Version{}, false
	}
	version := strings.TrimPrefix(value, "v")
	if plus := strings.IndexByte(version, '+'); plus >= 0 {
		if !validIdentifiers(version[plus+1:], false) {
			return Version{}, false
		}
		version = version[:plus]
	}
	var prerelease []string
	if dash := strings.IndexByte(version, '-'); dash >= 0 {
		if !validIdentifiers(version[dash+1:], true) {
			return Version{}, false
		}
		prerelease = strings.Split(version[dash+1:], ".")
		version = version[:dash]
	}
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	for _, part := range parts {
		if !validCoreNumericIdentifier(part) {
			return Version{}, false
		}
	}
	return Version{raw: value, major: parts[0], minor: parts[1], patch: parts[2], prerelease: prerelease}, true
}

func validCoreNumericIdentifier(value string) bool {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return false
	}
	return isNumericIdentifier(value)
}

func validIdentifiers(value string, rejectNumericLeadingZero bool) bool {
	if value == "" {
		return false
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		numeric := true
		for _, character := range identifier {
			if character >= '0' && character <= '9' {
				continue
			}
			numeric = false
			if character < 'A' || character > 'Z' {
				if character < 'a' || character > 'z' {
					if character != '-' {
						return false
					}
				}
			}
		}
		if rejectNumericLeadingZero && numeric && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
}

func comparePrerelease(left, right []string) int {
	if len(left) == 0 && len(right) == 0 {
		return 0
	}
	if len(left) == 0 {
		return 1
	}
	if len(right) == 0 {
		return -1
	}
	for i := 0; i < len(left) && i < len(right); i++ {
		leftNumeric := isNumericIdentifier(left[i])
		rightNumeric := isNumericIdentifier(right[i])
		switch {
		case leftNumeric && rightNumeric:
			if comparison := compareNumericString(left[i], right[i]); comparison != 0 {
				return comparison
			}
		case leftNumeric:
			return -1
		case rightNumeric:
			return 1
		case left[i] < right[i]:
			return -1
		case left[i] > right[i]:
			return 1
		}
	}
	return compareUint(uint64(len(left)), uint64(len(right)))
}

func isNumericIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true

}

func compareNumericString(left, right string) int {
	if len(left) != len(right) {
		return compareUint(uint64(len(left)), uint64(len(right)))
	}
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func compareUint(left, right uint64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func IsComparable(value string) bool {
	_, err := ParseVersion(value)
	if err != nil || strings.HasSuffix(value, "-dirty") {
		return false
	}
	return !gitDescribeVersion.MatchString(value)
}

var errNoReleases = errors.New("no compatible releases found")

var gitDescribeVersion = regexp.MustCompile(`-\d+-g[0-9a-fA-F]+(?:-dirty)?$`)
