// Package version provides semantic version parsing and comparison.
package version

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version represents a parsed semantic version.
type Version struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string // e.g., "alpha", "beta.1"
	Build      string // e.g., "build.123"
	Raw        string // Original string
}

var semverRegex = regexp.MustCompile(`^v?(\d+)(?:\.(\d+))?(?:\.(\d+))?(?:-([0-9A-Za-z\-\.]+))?(?:\+([0-9A-Za-z\-\.]+))?$`)

// Parse parses a semantic version string.
// Accepts versions with or without the "v" prefix.
// Examples: "v1.2.3", "1.2.3", "v1.2.3-beta+build", "v1.0"
func Parse(s string) (Version, error) {
	matches := semverRegex.FindStringSubmatch(s)
	if matches == nil {
		return Version{}, fmt.Errorf("invalid version: %q", s)
	}

	v := Version{Raw: s}

	// Major is always present
	major, _ := strconv.Atoi(matches[1])
	v.Major = major

	// Minor is optional
	if matches[2] != "" {
		minor, _ := strconv.Atoi(matches[2])
		v.Minor = minor
	}

	// Patch is optional
	if matches[3] != "" {
		patch, _ := strconv.Atoi(matches[3])
		v.Patch = patch
	}

	// Prerelease
	if matches[4] != "" {
		v.Prerelease = matches[4]
	}

	// Build metadata
	if matches[5] != "" {
		v.Build = matches[5]
	}

	return v, nil
}

// MustParse is like Parse but panics on error.
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// String returns the canonical form of the version (with v prefix).
func (v Version) String() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch))
	if v.Prerelease != "" {
		sb.WriteString("-")
		sb.WriteString(v.Prerelease)
	}
	if v.Build != "" {
		sb.WriteString("+")
		sb.WriteString(v.Build)
	}
	return sb.String()
}

// Compare compares two versions.
// Returns -1 if v < other, 0 if v == other, 1 if v > other.
// Build metadata is ignored in comparison.
func (v Version) Compare(other Version) int {
	// Compare major
	if v.Major != other.Major {
		if v.Major < other.Major {
			return -1
		}
		return 1
	}

	// Compare minor
	if v.Minor != other.Minor {
		if v.Minor < other.Minor {
			return -1
		}
		return 1
	}

	// Compare patch
	if v.Patch != other.Patch {
		if v.Patch < other.Patch {
			return -1
		}
		return 1
	}

	// Compare prerelease
	// A version without prerelease is greater than one with prerelease
	if v.Prerelease == "" && other.Prerelease != "" {
		return 1
	}
	if v.Prerelease != "" && other.Prerelease == "" {
		return -1
	}
	if v.Prerelease != other.Prerelease {
		return comparePrerelease(v.Prerelease, other.Prerelease)
	}

	return 0
}

// LessThan returns true if v < other.
func (v Version) LessThan(other Version) bool {
	return v.Compare(other) < 0
}

// LessThanOrEqual returns true if v <= other.
func (v Version) LessThanOrEqual(other Version) bool {
	return v.Compare(other) <= 0
}

// GreaterThan returns true if v > other.
func (v Version) GreaterThan(other Version) bool {
	return v.Compare(other) > 0
}

// GreaterThanOrEqual returns true if v >= other.
func (v Version) GreaterThanOrEqual(other Version) bool {
	return v.Compare(other) >= 0
}

// Equal returns true if v == other (ignoring build metadata).
func (v Version) Equal(other Version) bool {
	return v.Compare(other) == 0
}

// IsPrerelease returns true if the version has a prerelease tag.
func (v Version) IsPrerelease() bool {
	return v.Prerelease != ""
}

// comparePrerelease compares prerelease identifiers.
// Identifiers are compared as numbers if both are numeric, otherwise lexically.
func comparePrerelease(a, b string) int {
	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")

	minLen := len(partsA)
	if len(partsB) < minLen {
		minLen = len(partsB)
	}

	for i := 0; i < minLen; i++ {
		cmp := comparePrereleaseIdentifier(partsA[i], partsB[i])
		if cmp != 0 {
			return cmp
		}
	}

	// If all compared parts are equal, longer prerelease is greater
	if len(partsA) < len(partsB) {
		return -1
	}
	if len(partsA) > len(partsB) {
		return 1
	}
	return 0
}

func comparePrereleaseIdentifier(a, b string) int {
	aNum, aIsNum := parseNumber(a)
	bNum, bIsNum := parseNumber(b)

	if aIsNum && bIsNum {
		if aNum < bNum {
			return -1
		}
		if aNum > bNum {
			return 1
		}
		return 0
	}

	// Numeric identifiers always have lower precedence than non-numeric
	if aIsNum {
		return -1
	}
	if bIsNum {
		return 1
	}

	// Both are non-numeric, compare lexically
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func parseNumber(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// IsValid checks if a string is a valid semantic version.
func IsValid(s string) bool {
	_, err := Parse(s)
	return err == nil
}

// Canonical returns the canonical form of a version string.
func Canonical(s string) string {
	v, err := Parse(s)
	if err != nil {
		return ""
	}
	return v.String()
}

// Max returns the maximum version from a list.
func Max(versions ...Version) Version {
	if len(versions) == 0 {
		return Version{}
	}
	max := versions[0]
	for _, v := range versions[1:] {
		if v.GreaterThan(max) {
			max = v
		}
	}
	return max
}

// Sort sorts a slice of versions in ascending order.
func Sort(versions []Version) {
	for i := 0; i < len(versions); i++ {
		for j := i + 1; j < len(versions); j++ {
			if versions[j].LessThan(versions[i]) {
				versions[i], versions[j] = versions[j], versions[i]
			}
		}
	}
}
