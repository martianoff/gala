package version

import (
	"fmt"
	"regexp"
	"strings"
)

// Constraint represents a version constraint.
type Constraint interface {
	// Satisfies returns true if the version satisfies this constraint.
	Satisfies(v Version) bool
	// String returns the string representation of the constraint.
	String() string
}

// ParseConstraint parses a version constraint string.
// Supported formats:
//   - "v1.2.3" or "1.2.3" - exact version
//   - "^1.2.3" - compatible (>=1.2.3, <2.0.0 for major>0; >=0.2.3, <0.3.0 for major=0)
//   - "~1.2.3" - approximate (>=1.2.3, <1.3.0)
//   - ">=1.2.3" - greater than or equal
//   - ">1.2.3" - greater than
//   - "<=1.2.3" - less than or equal
//   - "<1.2.3" - less than
//   - ">=1.0.0,<2.0.0" - range (comma-separated AND)
//   - "latest" - special: accepts any version
func ParseConstraint(s string) (Constraint, error) {
	s = strings.TrimSpace(s)

	if s == "" {
		return nil, fmt.Errorf("empty constraint")
	}

	// Handle "latest" special case
	if s == "latest" {
		return &latestConstraint{}, nil
	}

	// Handle comma-separated AND constraints
	if strings.Contains(s, ",") {
		parts := strings.Split(s, ",")
		constraints := make([]Constraint, 0, len(parts))
		for _, part := range parts {
			c, err := ParseConstraint(strings.TrimSpace(part))
			if err != nil {
				return nil, err
			}
			constraints = append(constraints, c)
		}
		return &andConstraint{constraints: constraints}, nil
	}

	// Handle caret (compatible)
	if strings.HasPrefix(s, "^") {
		return parseCaretConstraint(s[1:])
	}

	// Handle tilde (approximate)
	if strings.HasPrefix(s, "~") {
		return parseTildeConstraint(s[1:])
	}

	// Handle comparison operators
	if strings.HasPrefix(s, ">=") {
		return parseComparisonConstraint(">=", s[2:])
	}
	if strings.HasPrefix(s, "<=") {
		return parseComparisonConstraint("<=", s[2:])
	}
	if strings.HasPrefix(s, ">") {
		return parseComparisonConstraint(">", s[1:])
	}
	if strings.HasPrefix(s, "<") {
		return parseComparisonConstraint("<", s[1:])
	}
	if strings.HasPrefix(s, "=") {
		return parseComparisonConstraint("=", s[1:])
	}

	// Default: exact version
	v, err := Parse(s)
	if err != nil {
		return nil, fmt.Errorf("invalid constraint %q: %w", s, err)
	}
	return &exactConstraint{version: v, raw: s}, nil
}

// MustParseConstraint is like ParseConstraint but panics on error.
func MustParseConstraint(s string) Constraint {
	c, err := ParseConstraint(s)
	if err != nil {
		panic(err)
	}
	return c
}

// exactConstraint matches only a specific version.
type exactConstraint struct {
	version Version
	raw     string
}

func (c *exactConstraint) Satisfies(v Version) bool {
	return v.Equal(c.version)
}

func (c *exactConstraint) String() string {
	return c.raw
}

// comparisonConstraint represents >=, >, <=, < constraints.
type comparisonConstraint struct {
	op      string
	version Version
}

func parseComparisonConstraint(op, versionStr string) (Constraint, error) {
	v, err := Parse(strings.TrimSpace(versionStr))
	if err != nil {
		return nil, fmt.Errorf("invalid version in constraint: %w", err)
	}
	return &comparisonConstraint{op: op, version: v}, nil
}

func (c *comparisonConstraint) Satisfies(v Version) bool {
	switch c.op {
	case ">=":
		return v.GreaterThanOrEqual(c.version)
	case ">":
		return v.GreaterThan(c.version)
	case "<=":
		return v.LessThanOrEqual(c.version)
	case "<":
		return v.LessThan(c.version)
	case "=":
		return v.Equal(c.version)
	default:
		return false
	}
}

func (c *comparisonConstraint) String() string {
	return c.op + c.version.String()
}

// caretConstraint represents ^version (compatible changes).
// ^1.2.3 means >=1.2.3, <2.0.0
// ^0.2.3 means >=0.2.3, <0.3.0
// ^0.0.3 means >=0.0.3, <0.0.4
type caretConstraint struct {
	min     Version
	maxNext Version // Exclusive upper bound
	raw     string
}

func parseCaretConstraint(versionStr string) (Constraint, error) {
	v, err := Parse(strings.TrimSpace(versionStr))
	if err != nil {
		return nil, fmt.Errorf("invalid version in caret constraint: %w", err)
	}

	var maxNext Version
	if v.Major == 0 {
		if v.Minor == 0 {
			// ^0.0.x -> <0.0.(x+1)
			maxNext = Version{Major: 0, Minor: 0, Patch: v.Patch + 1}
		} else {
			// ^0.x.y -> <0.(x+1).0
			maxNext = Version{Major: 0, Minor: v.Minor + 1, Patch: 0}
		}
	} else {
		// ^x.y.z -> <(x+1).0.0
		maxNext = Version{Major: v.Major + 1, Minor: 0, Patch: 0}
	}

	return &caretConstraint{min: v, maxNext: maxNext, raw: "^" + versionStr}, nil
}

func (c *caretConstraint) Satisfies(v Version) bool {
	return v.GreaterThanOrEqual(c.min) && v.LessThan(c.maxNext)
}

func (c *caretConstraint) String() string {
	return c.raw
}

// tildeConstraint represents ~version (approximate).
// ~1.2.3 means >=1.2.3, <1.3.0
type tildeConstraint struct {
	min     Version
	maxNext Version // Exclusive upper bound
	raw     string
}

func parseTildeConstraint(versionStr string) (Constraint, error) {
	v, err := Parse(strings.TrimSpace(versionStr))
	if err != nil {
		return nil, fmt.Errorf("invalid version in tilde constraint: %w", err)
	}

	maxNext := Version{Major: v.Major, Minor: v.Minor + 1, Patch: 0}
	return &tildeConstraint{min: v, maxNext: maxNext, raw: "~" + versionStr}, nil
}

func (c *tildeConstraint) Satisfies(v Version) bool {
	return v.GreaterThanOrEqual(c.min) && v.LessThan(c.maxNext)
}

func (c *tildeConstraint) String() string {
	return c.raw
}

// andConstraint combines multiple constraints with AND logic.
type andConstraint struct {
	constraints []Constraint
}

func (c *andConstraint) Satisfies(v Version) bool {
	for _, constraint := range c.constraints {
		if !constraint.Satisfies(v) {
			return false
		}
	}
	return true
}

func (c *andConstraint) String() string {
	parts := make([]string, len(c.constraints))
	for i, constraint := range c.constraints {
		parts[i] = constraint.String()
	}
	return strings.Join(parts, ",")
}

// latestConstraint accepts any version (for "latest" keyword).
type latestConstraint struct{}

func (c *latestConstraint) Satisfies(v Version) bool {
	return true
}

func (c *latestConstraint) String() string {
	return "latest"
}

// ConstraintString regex for quick validation.
var constraintRegex = regexp.MustCompile(`^(latest|\^|~|>=?|<=?)?v?\d+(\.\d+)?(\.\d+)?(-[0-9A-Za-z\-\.]+)?(\+[0-9A-Za-z\-\.]+)?$`)

// IsValidConstraint checks if a string is a valid constraint.
func IsValidConstraint(s string) bool {
	// Handle comma-separated constraints
	parts := strings.Split(s, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "latest" {
			continue
		}
		if !constraintRegex.MatchString(part) {
			return false
		}
	}
	return true
}

// SelectBest selects the best (highest) version that satisfies the constraint.
func SelectBest(constraint Constraint, versions []Version) (Version, bool) {
	var best Version
	found := false

	for _, v := range versions {
		if constraint.Satisfies(v) {
			if !found || v.GreaterThan(best) {
				best = v
				found = true
			}
		}
	}

	return best, found
}
