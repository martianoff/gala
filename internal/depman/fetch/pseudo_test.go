package fetch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The pseudo-version of an untagged module carries the major version its path
// names, and the commit time in UTC.
func TestPseudoVersion(t *testing.T) {
	when := time.Date(2022, 6, 3, 3, 37, 15, 0, time.FixedZone("CEST", 2*60*60))
	const rev = "3360bf22bea4ffffffffffffffffffffffffffff"
	tests := []struct {
		path, want string
	}{
		{"github.com/hut8/goq", "v0.0.0-20220603013715-3360bf22bea4"},
		{"example.com/m/v2", "v2.0.0-20220603013715-3360bf22bea4"},
		{"gopkg.in/yaml.v3", "v3.0.0-20220603013715-3360bf22bea4"},
		{"example.com/vendor", "v0.0.0-20220603013715-3360bf22bea4"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, pseudoVersion(tt.path, when, rev), tt.path)
	}
}
