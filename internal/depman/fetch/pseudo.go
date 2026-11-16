package fetch

import (
	"time"

	"golang.org/x/mod/module"
)

// pseudoVersion returns the pseudo-version Go gives commit rev, made at
// commitTime, of a module with no tagged version: vN.0.0-yyyymmddhhmmss-rev12,
// where vN is the major version the module path's suffix (/v2, gopkg.in's
// .v3) names, v0 without one.
func pseudoVersion(modulePath string, commitTime time.Time, rev string) string {
	major := ""
	if _, pathMajor, ok := module.SplitPathVersion(modulePath); ok {
		major = module.PathMajorPrefix(pathMajor)
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	return module.PseudoVersion(major, "", commitTime, rev)
}
