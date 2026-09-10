package updater

import "testing"

func TestVersionComparison(t *testing.T) {
	tests := []struct {
		left  string
		right string
		want  int
	}{
		{left: "v1.2.3", right: "v1.2.2", want: 1},
		{left: "v1.2.3", right: "v1.2.3", want: 0},
		{left: "v1.2.3-rc.2", right: "v1.2.3-rc.10", want: -1},
		{left: "v1.2.3", right: "v1.2.3-rc.1", want: 1},
	}
	for _, test := range tests {
		left, err := ParseVersion(test.left)
		if err != nil {
			t.Fatal(err)
		}
		right, err := ParseVersion(test.right)
		if err != nil {
			t.Fatal(err)
		}
		if got := Compare(left, right); got != test.want {
			t.Fatalf("Compare(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestParseVersionRejectsUnsupportedVersions(t *testing.T) {
	for _, value := range []string{"", "development", "b4adfc2", "v1.2", "v1.2.3-01", "v01.2.3", "2.3-b450", "2.3-bx"} {
		if _, err := ParseVersion(value); err == nil {
			t.Fatalf("ParseVersion(%q) succeeded", value)
		}
	}
}

func TestIsComparableRejectsUnsupportedVersions(t *testing.T) {
	for _, value := range []string{"development", "b4adfc2", "v3.0.0-1-gabc1234", "v3.0.0-dirty", "2.3-b450"} {
		if IsComparable(value) {
			t.Fatalf("IsComparable(%q) = true", value)
		}
	}
	for _, value := range []string{"v3.0.0", "v3.0.0-rc.1"} {
		if !IsComparable(value) {
			t.Fatalf("IsComparable(%q) = false", value)
		}
	}
}

func TestPrereleaseNumericIdentifiersUseArbitraryPrecision(t *testing.T) {
	newer, _ := ParseVersion("v1.0.0-100000000000000000000")
	older, _ := ParseVersion("v1.0.0-99999999999999999999")
	if Compare(newer, older) <= 0 {
		t.Fatal("larger arbitrary-precision numeric prerelease sorted older")
	}
}

func TestCoreVersionIdentifiersUseArbitraryPrecision(t *testing.T) {
	newer, err := ParseVersion("v18446744073709551616.0.0")
	if err != nil {
		t.Fatal(err)
	}
	older, err := ParseVersion("v18446744073709551615.999999999999999999999.999999999999999999999")
	if err != nil {
		t.Fatal(err)
	}
	if Compare(newer, older) <= 0 {
		t.Fatal("larger arbitrary-precision major version sorted older")
	}
}
