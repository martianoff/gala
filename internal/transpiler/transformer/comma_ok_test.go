package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two names bind a Go map index or a channel receive as Go's comma-ok form,
// with `:=` as with `val`: the value takes the element type and the flag is a
// bool, so both feed later inference (`Some(v)` is a `Some[int]`).
func TestCommaOkBinding(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "short var decl from a map index",
			body: `
    val m = go_interop.MapEmpty[string, int]()
    v, ok := m["a"]
    Println(Some(v), ok)`,
			want: []string{`var v, ok = m["a"]`, "std.Some[int]{}.Apply(v)"},
		},
		{
			name: "val from a map index",
			body: `
    val m = go_interop.MapEmpty[string, int]()
    val v, ok = m["a"]
    Println(Some(v), ok)`,
			want: []string{`var v, ok = m["a"]`, "std.Some[int]{}.Apply(v)"},
		},
		{
			name: "var from a map index",
			body: `
    val m = go_interop.MapEmpty[string, int]()
    var v, ok = m["a"]
    Println(Some(v), ok)`,
			want: []string{`var v, ok = m["a"]`, "std.Some[int]{}.Apply(v)"},
		},
		{
			name: "short var decl from a channel receive",
			body: `
    v, ok := <-time.After(time.Millisecond)
    Println(Some(v), ok)`,
			want: []string{"var v, ok = <-time.After(time.Millisecond)", "std.Some[time.Time]{}.Apply(v)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "package main\n\nimport \"time\"\nimport \"martianoff/gala/go_interop\"\n\nfunc main() {\n    Println(time.Millisecond, go_interop.MapEmpty[string, int]())" + tt.body + "\n}\n"
			got, err := newBindTranspiler().Transpile(src, "")
			require.NoError(t, err)
			for _, want := range tt.want {
				assert.Contains(t, got, want)
			}
		})
	}
}

// Anything but a call or a two-name comma-ok form is one value, which
// several names cannot bind.
func TestCommaOkBindingMismatch(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "a literal is one value",
			body: `a, b := 5`,
			want: "assignment mismatch: 2 names but the right-hand side is a single value",
		},
		{
			name: "a map index binds at most two names",
			body: `a, b, c := m["a"]`,
			want: "assignment mismatch: 3 names but a map index or channel receive gives at most 2 values",
		},
		{
			name: "an array index is one value",
			body: `a, b := go_interop.SliceOf(1, 2)[0]`,
			want: "assignment mismatch: 2 names but the right-hand side is a single value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "package main\n\nimport \"martianoff/gala/go_interop\"\n\nfunc main() {\n    val m = go_interop.MapEmpty[string, int]()\n    " + tt.body + "\n    Println(m)\n}\n"
			_, err := newBindTranspiler().Transpile(src, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}
