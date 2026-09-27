package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoCopyReceiverRejected pins GALA-E0053: a pointer-receiver method on a
// value Go cannot address runs on a copy (std.AddrOfCopy), which is wrong for
// a value that must not be copied — a copied Mutex locks nothing, a copied
// Builder loses its writes. Such calls are rejected; a value that is fine to
// copy (url.URL, time.Time) still gets the copy, and an addressable receiver
// is untouched.
func TestNoCopyReceiverRejected(t *testing.T) {
	trans := newDefaultsTranspiler()

	const decls = `
struct Guarded(mu sync.Mutex, n int)

func (g *Guarded) Bump() int = g.n + 1

type spinLock struct {
    var held bool
}

func (s *spinLock) Lock() {
    s.held = true
}

func (s *spinLock) Unlock() {
    s.held = false
}

type noCopy struct {
    var unused bool
}

func (n *noCopy) Touch() {
    n.unused = true
}

struct Marked(nc noCopy, n int)

func (m *Marked) Touch() int = m.n

struct Holder(g Guarded)

struct Box[T any](v T)

func (b *Box[T]) Touch() int = 1

type halfLock struct {
    var held bool
}

func (h halfLock) Lock() {
    Println(h.held)
}

func (h *halfLock) Unlock() {
    h.held = false
}

func makeWG() sync.WaitGroup = sync.WaitGroup{}
`

	rejected := []struct {
		name   string
		body   string
		reason string
	}{
		{"sync.Mutex val", "val m = sync.Mutex{}\n    m.Lock()", "sync.Mutex must not be copied"},
		{"sync.RWMutex val", "val m = sync.RWMutex{}\n    m.RLock()", "sync.RWMutex must not be copied"},
		{"sync.WaitGroup call result", "makeWG().Add(1)", "sync.WaitGroup must not be copied"},
		{"sync.Once val", "val o = sync.Once{}\n    o.Do(() => Println(1))", "sync.Once must not be copied"},
		{"sync.Cond literal", "sync.Cond{}.Signal()", "sync.Cond must not be copied"},
		{"sync.Map val", "val sm = sync.Map{}\n    sm.Store(1, 2)", "sync.Map must not be copied"},
		{"sync.Pool val", "val p = sync.Pool{}\n    Println(p.Get())", "sync.Pool must not be copied"},
		{"atomic.Int64 val", "val a = atomic.Int64{}\n    Println(a.Add(1))", "atomic.Int64 must not be copied"},
		{"atomic.Value val", "val a = atomic.Value{}\n    Println(a.Load())", "atomic.Value must not be copied"},
		{"strings.Builder val", "val sb = strings.Builder{}\n    sb.WriteString(\"x\")", "strings.Builder must not be copied"},
		{"bytes.Buffer val", "val b = bytes.Buffer{}\n    b.WriteString(\"x\")", "bytes.Buffer must not be copied"},
		{"GALA struct holding a Mutex", "val g = Guarded(sync.Mutex{}, 1)\n    Println(g.Bump())", "Guarded contains sync.Mutex"},
		{"GALA struct holding one, reached through a field", "val h = Holder(Guarded(sync.Mutex{}, 1))\n    Println(h.g.Bump())", "Guarded contains sync.Mutex"},
		{"GALA type with pointer Lock/Unlock", "val s = spinLock{}\n    s.Lock()", "spinLock must not be copied"},
		{"GALA type with value Lock and pointer Unlock", "val h = halfLock{}\n    h.Unlock()", "halfLock must not be copied"},
		{"generic GALA struct instantiated with a Mutex", "val b = Box[sync.Mutex](sync.Mutex{})\n    Println(b.Touch())", "Box contains sync.Mutex"},
		{"GALA struct with a noCopy marker", "val m = Marked(noCopy{}, 1)\n    Println(m.Touch())", "Marked contains noCopy"},
		{"Go struct with lock fields", "val s = http.Server{}\n    Println(s.Close())", "http.Server contains "},
	}
	for _, tc := range rejected {
		t.Run("rejected/"+tc.name, func(t *testing.T) {
			_, err := trans.Transpile(noCopySource(decls, tc.body), "nocopy_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0053")
			assert.Contains(t, err.Error(), tc.reason)
			assert.Contains(t, err.Error(), "declare it with `var`, or hold a pointer (`&T{...}`)")
		})
	}

	accepted := []struct {
		name        string
		body        string
		mustContain string
	}{
		{"url.URL val runs on a copy", "val u = url.URL(Scheme = \"https\", Host = \"x\")\n    Println(u.String())", "std.AddrOfCopy(u.Get()).String()"},
		{"time.Time val runs on a copy", "val tm = time.Now()\n    Println(tm.UnmarshalText(go_interop.ToBytes(\"x\")))", "std.AddrOfCopy(tm.Get()).UnmarshalText("},
		{"var Mutex is addressable", "var m = sync.Mutex{}\n    m.Lock()", "m.Lock()"},
		{"pointer to a Mutex", "val m = &sync.Mutex{}\n    m.Lock()", "m.Get().Lock()"},
		{"generic GALA struct of a copyable type", "val b = Box[int](1)\n    Println(b.Touch())", "std.AddrOfCopy(b.Get()).Touch()"},
		{"var Builder is addressable", "var sb = strings.Builder{}\n    sb.WriteString(\"x\")", "sb.WriteString(\"x\")"},
	}
	for _, tc := range accepted {
		t.Run("accepted/"+tc.name, func(t *testing.T) {
			out, err := trans.Transpile(noCopySource(decls, tc.body), "nocopy_test.gala")
			require.NoError(t, err)
			assert.Contains(t, out, tc.mustContain)
		})
	}
}

func noCopySource(decls, body string) string {
	return `package main

import (
    "bytes"
    "net/http"
    "net/url"
    "strings"
    "sync"
    "sync/atomic"
    "time"

    "martianoff/gala/go_interop"
)
` + decls + `
func main() {
    ` + body + `
}
`
}
