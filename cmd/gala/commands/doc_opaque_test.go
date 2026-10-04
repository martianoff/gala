package commands

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
)

// richWithOpaque is a package declaring two opaque types: one with a method of
// its own, one over bool (which gets Hash but no Compare).
func richWithOpaque() *transpiler.RichAST {
	return &transpiler.RichAST{
		PackageName: "ids",
		Types: map[string]*transpiler.TypeMetadata{
			"ids.Millis": {
				Name:       "Millis",
				Package:    "ids",
				Doc:        "Millis is a duration in milliseconds.",
				IsOpaque:   true,
				Underlying: transpiler.BasicType{Name: "int64"},
				Methods: map[string]*transpiler.MethodMetadata{
					"Seconds": {Name: "Seconds", ReturnType: transpiler.BasicType{Name: "float64"}},
				},
			},
			"ids.Enabled": {
				Name:       "Enabled",
				Package:    "ids",
				IsOpaque:   true,
				Underlying: transpiler.BasicType{Name: "bool"},
			},
		},
	}
}

func TestDocDescribesOpaqueTypes(t *testing.T) {
	pkg := collectPackageDoc("ids", richWithOpaque())
	require.Len(t, pkg.Types, 2)

	enabled, millis := pkg.Types[0], pkg.Types[1]
	assert.True(t, millis.Opaque)
	assert.Equal(t, "int64", millis.Underlying)
	var names []string
	for _, m := range millis.Methods {
		names = append(names, m.Name)
		assert.Equal(t, m.Name != "Seconds", m.Synthesized, "method %s", m.Name)
	}
	assert.Equal(t, []string{"Compare", "Hash", "Seconds"}, names)
	assert.Equal(t, []docField{{Name: "other", Type: "Millis"}}, millis.Methods[0].Params)

	assert.Equal(t, "bool", enabled.Underlying)
	require.Len(t, enabled.Methods, 1)
	assert.Equal(t, "Hash", enabled.Methods[0].Name)

	data, err := json.Marshal(millis)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"opaque":true,"underlying":"int64"`)
	assert.Contains(t, string(data), `"synthesized":true`)
}

func TestRenderPackageDocShowsOpaqueTypes(t *testing.T) {
	var buf bytes.Buffer
	renderPackageDoc(&buf, collectPackageDoc("ids", richWithOpaque()))
	got := buf.String()

	for _, want := range []string{
		"opaque type Millis int64\n    Millis is a duration in milliseconds.\n",
		"    Compare(other Millis) int  (synthesized)\n",
		"    Hash() uint32  (synthesized)\n",
		"    Seconds() float64\n",
		"opaque type Enabled bool\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing %q\n--- got ---\n%s", want, got)
		}
	}
}
