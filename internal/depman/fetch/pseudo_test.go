package fetch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePseudoVersion(t *testing.T) {
	tests := []struct {
		ver, timestamp, rev string
	}{
		{"v0.0.0-20220603013715-3360bf22bea4", "20220603013715", "3360bf22bea4"},
		{"v1.2.4-0.20220603013715-3360bf22bea4", "20220603013715", "3360bf22bea4"},
		{"v1.2.3-pre.0.20220603013715-3360bf22bea4", "20220603013715", "3360bf22bea4"},
		{"v1.2.3-rc.1.0.20220603013715-3360bf22bea4", "20220603013715", "3360bf22bea4"},
		{"v2.0.0-20220603013715-3360bf22bea4+incompatible", "20220603013715", "3360bf22bea4"},
	}
	for _, tt := range tests {
		t.Run(tt.ver, func(t *testing.T) {
			require.True(t, isPseudoVersion(tt.ver))
			timestamp, rev, err := parsePseudoVersion(tt.ver)
			require.NoError(t, err)
			assert.Equal(t, tt.timestamp, timestamp)
			assert.Equal(t, tt.rev, rev)
		})
	}
}

func TestIsPseudoVersion_RejectsVersions(t *testing.T) {
	for _, ver := range []string{"v1.2.3", "v1.2.3-beta.1", "latest", "main", "3360bf22bea4", "v0.0.0-2022060301371-3360bf22bea4"} {
		assert.False(t, isPseudoVersion(ver), ver)
	}
}

func TestPseudoVersion(t *testing.T) {
	when := time.Date(2022, 6, 3, 1, 37, 15, 0, time.UTC)
	const rev = "3360bf22bea4ffffffffffffffffffffffffffff"
	tests := []struct {
		path, want string
	}{
		{"github.com/hut8/goq", "v0.0.0-20220603013715-3360bf22bea4"},
		{"example.com/m/v2", "v2.0.0-20220603013715-3360bf22bea4"},
		{"example.com/m/v1", "v0.0.0-20220603013715-3360bf22bea4"},
		{"example.com/vendor", "v0.0.0-20220603013715-3360bf22bea4"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, pseudoVersion(tt.path, when, rev), tt.path)
	}
}
