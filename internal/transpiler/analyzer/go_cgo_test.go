package analyzer

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The analyzer reads Go packages with the cgo setting the go command would
// build with, not go/build's platform default, which enables cgo even when no
// C compiler is installed.
func TestResolveCgoEnabled(t *testing.T) {
	found := func(string) (string, error) { return "/usr/bin/cc", nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }

	cases := []struct {
		name     string
		env      map[string]string
		ccSet    bool
		lookPath func(string) (string, error)
		platform bool
		want     bool
	}{
		{"CGO_ENABLED=0 wins over a C compiler on PATH", map[string]string{"CGO_ENABLED": "0"}, false, found, true, false},
		{"CGO_ENABLED=1 wins over a missing C compiler", map[string]string{"CGO_ENABLED": "1"}, false, missing, true, true},
		{"unset: default C compiler on PATH", nil, false, found, true, true},
		{"unset: no C compiler on PATH", nil, false, missing, true, false},
		{"unset: CC set, so the compiler is not looked for", nil, true, missing, true, true},
		{"unset: platform without cgo", nil, false, found, false, false},
		{"an unrecognised value counts as unset", map[string]string{"CGO_ENABLED": "yes"}, false, missing, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goenv := func(k string) string { return tc.env[k] }
			assert.Equal(t, tc.want, resolveCgoEnabled(goenv, tc.ccSet, tc.lookPath, tc.platform))
		})
	}
}

// A go env file is KEY=VALUE lines; `go env -w CGO_ENABLED=0` writes one.
func TestParseGoEnvFile(t *testing.T) {
	vals := map[string]string{"GOPROXY": "from-go.env"}
	parseGoEnvFile("# comment\r\nCGO_ENABLED=0\r\n\r\nGOPROXY=direct\nnot a setting\n", vals)
	assert.Equal(t, map[string]string{"CGO_ENABLED": "0", "GOPROXY": "direct"}, vals)
}
