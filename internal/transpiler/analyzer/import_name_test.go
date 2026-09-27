package analyzer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFileImportNames: an alias is the only name an aliased import binds;
// an unaliased one answers to every name its path may bind.
func TestFileImportNames(t *testing.T) {
	cases := []struct {
		imp       fileImport
		wantName  string
		wantNames []string
	}{
		{fileImport{Path: "strings"}, "strings", []string{"strings"}},
		{fileImport{Path: "gopkg.in/yaml.v3"}, "yaml", []string{"yaml", "yaml.v3"}},
		{fileImport{Path: "k8s.io/api/core/v1"}, "core", []string{"core", "v1"}},
		{fileImport{Path: "k8s.io/api/core/v1", Alias: "corev1"}, "corev1", []string{"corev1"}},
	}
	for _, tc := range cases {
		t.Run(tc.imp.Path+"/"+tc.imp.Alias, func(t *testing.T) {
			assert.Equal(t, tc.wantName, tc.imp.LocalName())
			assert.Equal(t, tc.wantNames, tc.imp.LocalNames())
		})
	}
}
