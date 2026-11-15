package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDocShowsInout pins that `gala doc` shows `inout` parameters and
// receivers as they are written, as hover does, not as the pointers they
// lower to: `Bump(inout c Counter)`, `inout Inc() int`, in text and --json.
func TestDocShowsInout(t *testing.T) {
	dir := t.TempDir()
	src := `package counter

struct Counter(var n int)

func Bump(inout c Counter, by int) {
    c.n = c.n + by
}

func (inout c Counter) Inc() int {
    c.n = c.n + 1
    c.n
}

type Stepper interface {
    inout Step() int
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "counter.gala"), []byte(src), 0o644))
	pkg, err := loadPackageDoc("counter", dir)
	require.NoError(t, err)

	var out bytes.Buffer
	renderPackageDoc(&out, pkg)
	text := out.String()
	assert.Contains(t, text, "Bump(inout c counter.Counter, by int)")
	assert.Contains(t, text, "inout Inc() int")
	assert.Contains(t, text, "inout Step() int")
	assert.NotContains(t, text, "*counter.Counter")

	require.Len(t, pkg.Functions, 1)
	data, err := json.Marshal(pkg.Functions[0])
	require.NoError(t, err)
	assert.Contains(t, string(data), `{"name":"c","type":"counter.Counter","inout":true}`)
	for _, typ := range pkg.Types {
		if typ.Name != "Counter" {
			continue
		}
		require.Len(t, typ.Methods, 1)
		data, err := json.Marshal(typ.Methods[0])
		require.NoError(t, err)
		assert.Contains(t, string(data), `"inoutReceiver":true`)
	}
}
