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
// lower to: `Bump(inout c Counter)`, `inout Inc() int`, a `func(inout
// Counter)` parameter, in text and --json.
func TestDocShowsInout(t *testing.T) {
	dir := t.TempDir()
	src := `package counter

struct Counter(var n int)

func Bump(inout c Counter, by int) {
    c.n = c.n + by
}

func Apply(inout c Counter, f func(inout Counter)) = f(&c)

func (inout c Counter) Inc() int {
    c.n = c.n + 1
    c.n
}

func (c Counter) Merge(inout into Counter) {
    into.n = into.n + c.n
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
	for _, want := range []string{
		"Bump(inout c counter.Counter, by int)",
		"Apply(inout c counter.Counter, f func(inout counter.Counter))",
		"inout Inc() int",
		"Merge(inout into counter.Counter)",
		"inout Step() int",
	} {
		assert.Contains(t, text, want)
	}
	assert.NotContains(t, text, "*counter.Counter")

	jsonOf := func(v any) string {
		data, err := json.Marshal(v)
		require.NoError(t, err)
		return string(data)
	}
	methods := map[string]string{}
	for _, typ := range pkg.Types {
		for _, m := range typ.Methods {
			methods[typ.Name+"."+m.Name] = jsonOf(m)
		}
	}
	require.Contains(t, methods, "Counter.Inc")
	require.Contains(t, methods, "Counter.Merge")
	require.Contains(t, methods, "Stepper.Step")
	assert.Contains(t, methods["Counter.Inc"], `"inoutReceiver":true`)
	assert.Contains(t, methods["Stepper.Step"], `"inoutReceiver":true`)
	assert.NotContains(t, methods["Counter.Merge"], `"inoutReceiver"`)
	assert.Contains(t, methods["Counter.Merge"], `{"name":"into","type":"counter.Counter","inout":true}`)

	functions := map[string]string{}
	for _, f := range pkg.Functions {
		functions[f.Name] = jsonOf(f)
	}
	require.Contains(t, functions, "Bump")
	assert.Contains(t, functions["Bump"], `{"name":"c","type":"counter.Counter","inout":true}`)
	assert.Contains(t, functions["Bump"], `{"name":"by","type":"int"}`)
}
