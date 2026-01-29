package version

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_Simple(t *testing.T) {
	tests := []struct {
		input    string
		major    int
		minor    int
		patch    int
		prerel   string
		build    string
		expected string
	}{
		{"v1.2.3", 1, 2, 3, "", "", "v1.2.3"},
		{"1.2.3", 1, 2, 3, "", "", "v1.2.3"},
		{"v1.0.0", 1, 0, 0, "", "", "v1.0.0"},
		{"v0.1.0", 0, 1, 0, "", "", "v0.1.0"},
		{"v10.20.30", 10, 20, 30, "", "", "v10.20.30"},
	}

	for _, tt := range tests {
		v, err := Parse(tt.input)
		require.NoError(t, err, "input: %s", tt.input)
		assert.Equal(t, tt.major, v.Major, "input: %s", tt.input)
		assert.Equal(t, tt.minor, v.Minor, "input: %s", tt.input)
		assert.Equal(t, tt.patch, v.Patch, "input: %s", tt.input)
		assert.Equal(t, tt.prerel, v.Prerelease, "input: %s", tt.input)
		assert.Equal(t, tt.build, v.Build, "input: %s", tt.input)
		assert.Equal(t, tt.expected, v.String(), "input: %s", tt.input)
	}
}

func TestParse_WithPrerelease(t *testing.T) {
	tests := []struct {
		input  string
		prerel string
	}{
		{"v1.2.3-alpha", "alpha"},
		{"v1.2.3-beta.1", "beta.1"},
		{"v1.2.3-rc.1", "rc.1"},
		{"v1.0.0-0.3.7", "0.3.7"},
		{"v1.0.0-x.7.z.92", "x.7.z.92"},
	}

	for _, tt := range tests {
		v, err := Parse(tt.input)
		require.NoError(t, err, "input: %s", tt.input)
		assert.Equal(t, tt.prerel, v.Prerelease, "input: %s", tt.input)
	}
}

func TestParse_WithBuild(t *testing.T) {
	tests := []struct {
		input string
		build string
	}{
		{"v1.2.3+build", "build"},
		{"v1.2.3+build.123", "build.123"},
		{"v1.2.3-beta+build", "build"},
	}

	for _, tt := range tests {
		v, err := Parse(tt.input)
		require.NoError(t, err, "input: %s", tt.input)
		assert.Equal(t, tt.build, v.Build, "input: %s", tt.input)
	}
}

func TestParse_PartialVersions(t *testing.T) {
	v, err := Parse("v1")
	require.NoError(t, err)
	assert.Equal(t, 1, v.Major)
	assert.Equal(t, 0, v.Minor)
	assert.Equal(t, 0, v.Patch)

	v, err = Parse("v1.2")
	require.NoError(t, err)
	assert.Equal(t, 1, v.Major)
	assert.Equal(t, 2, v.Minor)
	assert.Equal(t, 0, v.Patch)
}

func TestParse_Invalid(t *testing.T) {
	invalid := []string{
		"",
		"invalid",
		"v",
		"vX.Y.Z",
		"v1.2.3.4",
	}

	for _, s := range invalid {
		_, err := Parse(s)
		assert.Error(t, err, "expected error for: %s", s)
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0", "v2.0.0", -1},
		{"v2.0.0", "v1.0.0", 1},
		{"v1.0.0", "v1.1.0", -1},
		{"v1.1.0", "v1.0.0", 1},
		{"v1.0.0", "v1.0.1", -1},
		{"v1.0.1", "v1.0.0", 1},
		// Prerelease comparisons
		{"v1.0.0-alpha", "v1.0.0", -1},
		{"v1.0.0", "v1.0.0-alpha", 1},
		{"v1.0.0-alpha", "v1.0.0-beta", -1},
		{"v1.0.0-alpha.1", "v1.0.0-alpha.2", -1},
		{"v1.0.0-1", "v1.0.0-2", -1},
		{"v1.0.0-1", "v1.0.0-alpha", -1}, // numeric < non-numeric
	}

	for _, tt := range tests {
		a := MustParse(tt.a)
		b := MustParse(tt.b)
		result := a.Compare(b)
		assert.Equal(t, tt.expected, result, "Compare(%s, %s)", tt.a, tt.b)
	}
}

func TestComparisonMethods(t *testing.T) {
	v1 := MustParse("v1.0.0")
	v2 := MustParse("v2.0.0")

	assert.True(t, v1.LessThan(v2))
	assert.False(t, v2.LessThan(v1))

	assert.True(t, v1.LessThanOrEqual(v2))
	assert.True(t, v1.LessThanOrEqual(v1))

	assert.True(t, v2.GreaterThan(v1))
	assert.False(t, v1.GreaterThan(v2))

	assert.True(t, v2.GreaterThanOrEqual(v1))
	assert.True(t, v1.GreaterThanOrEqual(v1))

	assert.True(t, v1.Equal(v1))
	assert.False(t, v1.Equal(v2))
}

func TestIsPrerelease(t *testing.T) {
	assert.True(t, MustParse("v1.0.0-alpha").IsPrerelease())
	assert.False(t, MustParse("v1.0.0").IsPrerelease())
}

func TestMax(t *testing.T) {
	versions := []Version{
		MustParse("v1.0.0"),
		MustParse("v3.0.0"),
		MustParse("v2.0.0"),
	}
	max := Max(versions...)
	assert.Equal(t, "v3.0.0", max.String())
}

func TestSort(t *testing.T) {
	versions := []Version{
		MustParse("v3.0.0"),
		MustParse("v1.0.0"),
		MustParse("v2.0.0"),
		MustParse("v1.1.0"),
	}
	Sort(versions)
	assert.Equal(t, "v1.0.0", versions[0].String())
	assert.Equal(t, "v1.1.0", versions[1].String())
	assert.Equal(t, "v2.0.0", versions[2].String())
	assert.Equal(t, "v3.0.0", versions[3].String())
}

// Constraint tests

func TestParseConstraint_Exact(t *testing.T) {
	c, err := ParseConstraint("v1.2.3")
	require.NoError(t, err)

	assert.True(t, c.Satisfies(MustParse("v1.2.3")))
	assert.False(t, c.Satisfies(MustParse("v1.2.4")))
	assert.False(t, c.Satisfies(MustParse("v1.2.2")))
}

func TestParseConstraint_GreaterThanOrEqual(t *testing.T) {
	c, err := ParseConstraint(">=v1.2.3")
	require.NoError(t, err)

	assert.True(t, c.Satisfies(MustParse("v1.2.3")))
	assert.True(t, c.Satisfies(MustParse("v1.2.4")))
	assert.True(t, c.Satisfies(MustParse("v2.0.0")))
	assert.False(t, c.Satisfies(MustParse("v1.2.2")))
}

func TestParseConstraint_LessThan(t *testing.T) {
	c, err := ParseConstraint("<v2.0.0")
	require.NoError(t, err)

	assert.True(t, c.Satisfies(MustParse("v1.9.9")))
	assert.False(t, c.Satisfies(MustParse("v2.0.0")))
	assert.False(t, c.Satisfies(MustParse("v2.0.1")))
}

func TestParseConstraint_Caret(t *testing.T) {
	// ^1.2.3 means >=1.2.3, <2.0.0
	c, err := ParseConstraint("^1.2.3")
	require.NoError(t, err)

	assert.True(t, c.Satisfies(MustParse("v1.2.3")))
	assert.True(t, c.Satisfies(MustParse("v1.9.9")))
	assert.False(t, c.Satisfies(MustParse("v1.2.2")))
	assert.False(t, c.Satisfies(MustParse("v2.0.0")))
}

func TestParseConstraint_CaretZeroMajor(t *testing.T) {
	// ^0.2.3 means >=0.2.3, <0.3.0
	c, err := ParseConstraint("^0.2.3")
	require.NoError(t, err)

	assert.True(t, c.Satisfies(MustParse("v0.2.3")))
	assert.True(t, c.Satisfies(MustParse("v0.2.9")))
	assert.False(t, c.Satisfies(MustParse("v0.3.0")))
}

func TestParseConstraint_CaretZeroMinor(t *testing.T) {
	// ^0.0.3 means >=0.0.3, <0.0.4
	c, err := ParseConstraint("^0.0.3")
	require.NoError(t, err)

	assert.True(t, c.Satisfies(MustParse("v0.0.3")))
	assert.False(t, c.Satisfies(MustParse("v0.0.4")))
}

func TestParseConstraint_Tilde(t *testing.T) {
	// ~1.2.3 means >=1.2.3, <1.3.0
	c, err := ParseConstraint("~1.2.3")
	require.NoError(t, err)

	assert.True(t, c.Satisfies(MustParse("v1.2.3")))
	assert.True(t, c.Satisfies(MustParse("v1.2.9")))
	assert.False(t, c.Satisfies(MustParse("v1.3.0")))
}

func TestParseConstraint_Range(t *testing.T) {
	c, err := ParseConstraint(">=1.0.0,<2.0.0")
	require.NoError(t, err)

	assert.True(t, c.Satisfies(MustParse("v1.0.0")))
	assert.True(t, c.Satisfies(MustParse("v1.9.9")))
	assert.False(t, c.Satisfies(MustParse("v0.9.9")))
	assert.False(t, c.Satisfies(MustParse("v2.0.0")))
}

func TestParseConstraint_Latest(t *testing.T) {
	c, err := ParseConstraint("latest")
	require.NoError(t, err)

	assert.True(t, c.Satisfies(MustParse("v0.0.1")))
	assert.True(t, c.Satisfies(MustParse("v999.0.0")))
}

func TestSelectBest(t *testing.T) {
	versions := []Version{
		MustParse("v1.0.0"),
		MustParse("v1.1.0"),
		MustParse("v1.2.0"),
		MustParse("v2.0.0"),
	}

	c := MustParseConstraint("^1.0.0")
	best, found := SelectBest(c, versions)
	require.True(t, found)
	assert.Equal(t, "v1.2.0", best.String())
}

func TestSelectBest_NoMatch(t *testing.T) {
	versions := []Version{
		MustParse("v1.0.0"),
	}

	c := MustParseConstraint(">=2.0.0")
	_, found := SelectBest(c, versions)
	assert.False(t, found)
}

// MVS Resolver tests

type mockVersionProvider struct {
	versions     map[string][]Version
	requirements map[string]map[string][]Requirement
}

func (m *mockVersionProvider) AvailableVersions(path string) ([]Version, error) {
	if v, ok := m.versions[path]; ok {
		return v, nil
	}
	return nil, &NoVersionError{Path: path}
}

func (m *mockVersionProvider) Requirements(path string, version Version) ([]Requirement, error) {
	if reqs, ok := m.requirements[path]; ok {
		if r, ok := reqs[version.String()]; ok {
			return r, nil
		}
	}
	return nil, nil
}

func TestMVSResolver_Simple(t *testing.T) {
	provider := &mockVersionProvider{
		versions: map[string][]Version{
			"github.com/a": {MustParse("v1.0.0"), MustParse("v1.1.0")},
			"github.com/b": {MustParse("v2.0.0"), MustParse("v2.1.0")},
		},
		requirements: make(map[string]map[string][]Requirement),
	}

	resolver := NewResolver(provider)
	result, err := resolver.Resolve([]Requirement{
		{Path: "github.com/a", Constraint: MustParseConstraint("^1.0.0")},
		{Path: "github.com/b", Constraint: MustParseConstraint(">=2.0.0")},
	})

	require.NoError(t, err)
	assert.Empty(t, result.Errors)
	assert.Len(t, result.Selected, 2)

	// Check that MVS selected minimum satisfying versions
	selectedMap := make(map[string]string)
	for _, s := range result.Selected {
		selectedMap[s.Path] = s.Version.String()
	}
	assert.Equal(t, "v1.0.0", selectedMap["github.com/a"]) // MVS selects minimum
	assert.Equal(t, "v2.0.0", selectedMap["github.com/b"])
}

func TestMVSResolver_Transitive(t *testing.T) {
	provider := &mockVersionProvider{
		versions: map[string][]Version{
			"github.com/a": {MustParse("v1.0.0")},
			"github.com/b": {MustParse("v1.0.0"), MustParse("v1.1.0")},
		},
		requirements: map[string]map[string][]Requirement{
			"github.com/a": {
				"v1.0.0": {
					{Path: "github.com/b", Constraint: MustParseConstraint(">=1.0.0")},
				},
			},
		},
	}

	resolver := NewResolver(provider)
	result, err := resolver.Resolve([]Requirement{
		{Path: "github.com/a", Constraint: MustParseConstraint(">=1.0.0")},
	})

	require.NoError(t, err)
	assert.Empty(t, result.Errors)
	assert.Len(t, result.Selected, 2) // Should include transitive dep
}
