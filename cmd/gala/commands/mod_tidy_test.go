package commands

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/mod"
)

// A module whose go.mod is maintained by hand already requires its Go
// dependencies. Tidy must not require them a second time, must bring a
// requirement to the version gala.mod names, and running it again must not
// change the file.
func TestRenderBazelGoMod(t *testing.T) {
	uuid := mod.Require{Path: "github.com/google/uuid", Version: "v1.6.0", Go: true}
	yaml := mod.Require{Path: "gopkg.in/yaml.v3", Version: "v3.0.1", Go: true}
	const header = "module github.com/example/acp\n\ngo 1.25.5\n\n"

	tests := []struct {
		name        string
		existing    string
		deps        []mod.Require
		wantManaged []string // requirements in the managed section; nil = no section
		wantUpdated []string
		wantAbsent  []string
	}{
		{
			name:     "single-line require of the same module",
			existing: header + "require github.com/google/uuid v1.6.0\n",
			deps:     []mod.Require{uuid},
		},
		{
			name:     "require block with an indirect comment",
			existing: header + "require (\n\tgithub.com/google/uuid v1.6.0 // indirect\n)\n",
			deps:     []mod.Require{uuid},
		},
		{
			name:     "require block opener with a comment",
			existing: header + "require ( // direct\n\tgithub.com/google/uuid v1.6.0\n)\n",
			deps:     []mod.Require{uuid},
		},
		{
			name:     "require block opener without a space",
			existing: header + "require(\n\tgithub.com/google/uuid v1.6.0\n)\n",
			deps:     []mod.Require{uuid},
		},
		{
			name:     "CRLF line endings",
			existing: strings.ReplaceAll(header+"require github.com/google/uuid v1.6.0\n", "\n", "\r\n"),
			deps:     []mod.Require{uuid},
		},
		{
			name:        "CRLF line endings with a managed section",
			existing:    strings.ReplaceAll(header+"require github.com/google/uuid v1.6.0\n", "\n", "\r\n"),
			deps:        []mod.Require{uuid, yaml},
			wantManaged: []string{"gopkg.in/yaml.v3 v3.0.1"},
		},
		{
			name:        "a hand-written requirement at another version takes gala.mod's",
			existing:    header + "require github.com/google/uuid v1.5.0 // indirect\n",
			deps:        []mod.Require{uuid},
			wantUpdated: []string{"github.com/google/uuid v1.5.0 -> v1.6.0"},
			wantAbsent:  []string{"v1.5.0"},
		},
		{
			name:        "only the modules not already required are managed",
			existing:    header + "require github.com/google/uuid v1.6.0\n",
			deps:        []mod.Require{uuid, yaml},
			wantManaged: []string{"gopkg.in/yaml.v3 v3.0.1"},
		},
		{
			name: "a section an earlier tidy wrote is replaced",
			existing: header + managedGoModStart + "\nrequire (\n\tgithub.com/google/uuid v1.5.0\n)\n" +
				managedGoModEnd + "\n",
			deps:        []mod.Require{uuid},
			wantManaged: []string{"github.com/google/uuid v1.6.0"},
			wantAbsent:  []string{"v1.5.0"},
		},
		{
			name: "a legacy section is replaced",
			existing: header + "// GALA dependencies below. DO NOT EDIT.\nrequire (\n\tgithub.com/google/uuid v1.5.0\n)\n" +
				"// End GALA dependencies.\n",
			deps:        []mod.Require{uuid},
			wantManaged: []string{"github.com/google/uuid v1.6.0"},
			wantAbsent:  []string{"v1.5.0", "// GALA dependencies below."},
		},
		{
			name:        "no go.mod yet",
			existing:    "",
			deps:        []mod.Require{uuid},
			wantManaged: []string{"github.com/google/uuid v1.6.0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first, updated := renderBazelGoMod(tt.existing, "github.com/example/acp", tt.deps)
			assert.Equal(t, tt.wantUpdated, updated)
			second, again := renderBazelGoMod(first, "github.com/example/acp", tt.deps)
			assert.Equal(t, first, second, "tidy must be idempotent")
			assert.Empty(t, again)

			if strings.Contains(tt.existing, "\r\n") {
				assert.NotContains(t, strings.ReplaceAll(first, "\r\n", ""), "\n",
					"line endings must stay CRLF:\n%q", first)
			}
			for _, dep := range tt.deps {
				n := strings.Count(first, dep.Path+" "+dep.Version)
				assert.Equal(t, 1, n, "%s required %d times in:\n%s", dep.Path, n, first)
			}
			for _, absent := range tt.wantAbsent {
				assert.NotContains(t, first, absent)
			}
			assert.LessOrEqual(t, strings.Count(first, managedGoModStart), 1, first)

			idx := strings.Index(first, managedGoModStart)
			if tt.wantManaged == nil {
				assert.Equal(t, -1, idx, "no managed section expected in:\n%s", first)
				return
			}
			require.NotEqual(t, -1, idx, "managed section expected in:\n%s", first)
			for _, want := range tt.wantManaged {
				assert.Contains(t, first[idx:], want)
			}
		})
	}
}

// `go mod download -json` reports a module once per requirement. go.sum keeps
// the lines it already had and lists each line once.
func TestGoSumFromDownload(t *testing.T) {
	uuid := `{"Path":"github.com/google/uuid","Version":"v1.6.0",` +
		`"Sum":"h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=",` +
		`"GoModSum":"h1:TIyPZe4MgqvfeYDBFedMoGGpEw/LqOeaOT+nhxU+yHo="}`
	const (
		uuidZip  = "github.com/google/uuid v1.6.0 h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0="
		uuidMod  = "github.com/google/uuid v1.6.0/go.mod h1:TIyPZe4MgqvfeYDBFedMoGGpEw/LqOeaOT+nhxU+yHo="
		graphMod = "example.com/graph v1.0.0/go.mod h1:abc="
	)

	tests := []struct {
		name     string
		existing string
		download string
		want     string
	}{
		{
			name:     "a module reported twice",
			download: uuid + "\n" + uuid + "\n",
			want:     uuidZip + "\n" + uuidMod + "\n",
		},
		{
			name:     "lines already in go.sum",
			existing: uuidZip + "\n" + uuidMod + "\n",
			download: uuid + "\n",
			want:     uuidZip + "\n" + uuidMod + "\n",
		},
		{
			name:     "go.mod hashes of the rest of the graph are kept",
			existing: graphMod + "\r\n",
			download: uuid + "\n",
			want:     graphMod + "\n" + uuidZip + "\n" + uuidMod + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			downloaded, err := goSumLinesFromDownload([]byte(tt.download))
			require.NoError(t, err)
			got := mergeGoSum(tt.existing, downloaded)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, got, mergeGoSum(got, downloaded), "tidy must be idempotent")
		})
	}
}

// Output that is not a stream of module records is an error, not a go.sum
// written from whatever came before it.
func TestGoSumFromDownloadMalformed(t *testing.T) {
	_, err := goSumLinesFromDownload([]byte(`{"Path":"a","Version":"v1.0.0"} {oops`))
	require.Error(t, err)
}
