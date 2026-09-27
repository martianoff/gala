package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPointerReceiverMethodOnNonAddressableReceiver pins how a call to a
// pointer-receiver method is emitted when Go cannot address the receiver. A
// val reads through Get(), which returns a copy, so `u.Get().String()` is
// rejected by Go ("cannot call pointer method String on url.URL"). The
// receiver is handed over as std.AddrOfCopy(...) instead: the method runs on a
// copy and the val stays unchanged. An addressable receiver (a var, a pointer)
// and a value-receiver method must be emitted exactly as before.
func TestPointerReceiverMethodOnNonAddressableReceiver(t *testing.T) {
	trans := newDefaultsTranspiler()

	tests := []struct {
		name           string
		body           string
		mustContain    []string
		mustNotContain []string
	}{
		{
			name:        "Go pointer method on a val",
			body:        `val u = url.URL(Scheme = "https", Host = "example.com")` + "\n    Println(u.String())",
			mustContain: []string{"std.AddrOfCopy(u.Get()).String()"},
		},
		{
			name:        "Go pointer method on a composite literal",
			body:        `Println(url.URL{Scheme: "ftp", Host: "y"}.String())`,
			mustContain: []string{"std.AddrOfCopy(url.URL{"},
		},
		{
			name:        "Go pointer method on a field reached through a val",
			body:        `val w = Wrapper(link = url.URL(Scheme = "http", Host = "x"), label = "w")` + "\n    Println(w.link.String())",
			mustContain: []string{"std.AddrOfCopy(w.Get().link.Get()).String()"},
		},
		{
			name:        "GALA pointer method on a val",
			body:        `val c = Counter(n = 1)` + "\n    Println(c.Bump())",
			mustContain: []string{"std.AddrOfCopy(c.Get()).Bump()"},
		},
		{
			name:        "GALA pointer method on a function result",
			body:        `Println(makeCounter().Bump())`,
			mustContain: []string{"std.AddrOfCopy(makeCounter()).Bump()"},
		},
		{
			name:           "var receiver is addressable",
			body:           `var v = url.URL(Scheme = "https", Host = "example.com")` + "\n    Println(v.String())",
			mustContain:    []string{"v.String()"},
			mustNotContain: []string{"AddrOfCopy"},
		},
		{
			name:           "pointer val needs no copy",
			body:           `val p = &Counter(n = 1)` + "\n    Println(p.Bump())",
			mustNotContain: []string{"AddrOfCopy"},
		},
		{
			name:           "value-receiver method is unchanged",
			body:           `val c = Counter(n = 1)` + "\n    Println(c.Peek())",
			mustContain:    []string{"c.Get().Peek()"},
			mustNotContain: []string{"AddrOfCopy"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := `package main

import (
    "net/url"
)

type Counter struct {
    var n int
}

func (c *Counter) Bump() int {
    c.n = c.n + 1
    return c.n
}

func (c Counter) Peek() int = c.n

func makeCounter() Counter = Counter(n = 5)

type Wrapper struct {
    link url.URL
    label string
}

func main() {
    ` + tc.body + `
}`
			out, err := trans.Transpile(input, "pointer_receiver_val_test.gala")
			require.NoError(t, err)
			for _, s := range tc.mustContain {
				assert.Contains(t, out, s)
			}
			for _, s := range tc.mustNotContain {
				assert.NotContains(t, out, s)
			}
		})
	}
}
