package transformer_test

import (
	"testing"

	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `<-ch` is Go's channel receive. It lowered to token.ILLEGAL, emitting
// `ILLEGALtime.After(...)`; it is token.ARROW, and the received value has the
// channel's element type. A channel's type keeps its direction and its
// element's import path, so both can be spelled in generated Go.
func TestChannelReceive(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name: "receive infers the element type",
			input: `package main

import "time"

func main() {
    val fired = <-time.After(time.Millisecond)
    val later = Some(fired)
    Println(later.Get().Year())
}`,
			want: []string{"<-time.After(time.Millisecond)", "std.Some[time.Time]{}.Apply(fired)"},
		},
		{
			name: "element of a package the file does not import",
			input: `package main

import "martianoff/gala/go_interop"

func main() {
    val tick = <-go_interop.GoAfter(1000)
    val later = Some(tick)
    Println(later.Get().Year())
}`,
			want: []string{`import "time"`, "std.Some[time.Time]{}.Apply(tick)"},
		},
		{
			name: "a receive-only channel keeps its direction",
			input: `package main

import "time"

func main() {
    val pending = Some(time.After(time.Millisecond))
    Println((<-pending.Get()).Year())
}`,
			want: []string{"std.Some[<-chan time.Time]{}.Apply(time.After(time.Millisecond))", "(<-pending.Get()).Year()"},
		},
		{
			name: "range over a channel binds each received value",
			input: `package main

import "time"

func main() {
    for t := range time.Tick(time.Millisecond) {
        val later = Some(t)
        Println(later.Get().Year())
    }
}`,
			want: []string{"std.Some[time.Time]{}.Apply(t)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trans, tr := newTranspilerWithTransformer()
			out, err := trans.Transpile(tt.input, "main.gala")
			require.NoError(t, err)
			assert.NotContains(t, out, "ILLEGAL")
			for _, w := range tt.want {
				assert.Contains(t, out, w)
			}
			assert.Empty(t, transformer.UnresolvedTypes(tr))
		})
	}
}
