package fetch

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// pseudoVersionTimeFormat is the layout of a pseudo-version's timestamp: the
// commit time in UTC, yyyymmddhhmmss.
const pseudoVersionTimeFormat = "20060102150405"

// pseudoVersionRE matches the three forms of a Go pseudo-version:
//
//	vX.0.0-yyyymmddhhmmss-abcdefabcdef           no earlier tag
//	vX.Y.Z-pre.0.yyyymmddhhmmss-abcdefabcdef     after the prerelease vX.Y.Z-pre
//	vX.Y.(Z+1)-0.yyyymmddhhmmss-abcdefabcdef     after the release vX.Y.Z
//
// It is the pattern golang.org/x/mod/module uses.
var pseudoVersionRE = regexp.MustCompile(`^v[0-9]+\.(0\.0-|\d+\.\d+-([^+]*\.)?0\.)\d{14}-[A-Za-z0-9]+(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// isPseudoVersion reports whether ver is a Go pseudo-version, the version Go
// gives a commit no tag names.
func isPseudoVersion(ver string) bool {
	return strings.Count(ver, "-") >= 2 && pseudoVersionRE.MatchString(ver)
}

// parsePseudoVersion returns the commit timestamp and revision a
// pseudo-version names.
func parsePseudoVersion(ver string) (timestamp, rev string, err error) {
	if !isPseudoVersion(ver) {
		return "", "", fmt.Errorf("not a pseudo-version: %s", ver)
	}
	v, _, _ := strings.Cut(ver, "+")
	j := strings.LastIndex(v, "-")
	v, rev = v[:j], v[j+1:]
	i := strings.LastIndex(v, "-")
	if j := strings.LastIndex(v, "."); j > i {
		timestamp = v[j+1:]
	} else {
		timestamp = v[i+1:]
	}
	return timestamp, rev, nil
}

// pseudoVersion returns the pseudo-version Go gives commit rev, made at
// commitTime, of a module with no tagged version: vN.0.0-yyyymmddhhmmss-rev12,
// where N is the major version the module path's /vN suffix names (0 without
// one).
func pseudoVersion(modulePath string, commitTime time.Time, rev string) string {
	if len(rev) > 12 {
		rev = rev[:12]
	}
	return fmt.Sprintf("v%d.0.0-%s-%s", pathMajor(modulePath), commitTime.UTC().Format(pseudoVersionTimeFormat), rev)
}

// pathMajor returns the major version a module path's /vN suffix (N >= 2)
// names, or 0 when it has none.
func pathMajor(modulePath string) int {
	i := strings.LastIndex(modulePath, "/v")
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(modulePath[i+2:])
	if err != nil || n < 2 || strconv.Itoa(n) != modulePath[i+2:] {
		return 0
	}
	return n
}
