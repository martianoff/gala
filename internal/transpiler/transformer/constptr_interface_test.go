package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const constPtrIfacePrelude = `package main

import "errors"

struct Counter(var N int)

func (c *Counter) Notify(s string) {
    c.N = c.N + 1
}

func (c *Counter) Error() string = "counter"

struct Plain(N int)

func (p Plain) Notify(s string) {
    Println("plain")
}

type Notifier interface {
    Notify(s string)
}

func send(n Notifier) {
    n.Notify("x")
}

func show(a any) {
    Println(a != nil)
}

func useErrors() bool = errors.Unwrap(nil) == nil
`

// TestConstPtrInInterfaceSlotIsRejected: `&x` of an immutable binding is a
// read-only ConstPtr, which implements no interface whose methods are declared
// on the value's type. Filling such an interface slot with it reached Go,
// which reported "std.ConstPtr[Counter] does not implement Notifier (missing
// method Notify)" about a wrapper the source never wrote. It is GALA-E0070.
func TestConstPtrInInterfaceSlotIsRejected(t *testing.T) {
	trans := newTypeAsCtorTranspiler()
	cases := []struct {
		name, body, wantMsg, wantHint string
	}{
		{
			name: "val passed to a GALA interface parameter",
			body: `func main() {
    val c = Counter(0)
    send(&c)
}`,
			wantMsg:  "cannot use &c (ConstPtr[Counter]) as Notifier",
			wantHint: "declare it `var c` to pass a *Counter",
		},
		{
			name: "val passed to a Go interface parameter",
			body: `func main() {
    val c = Counter(0)
    Println(errors.Unwrap(&c) == nil)
}`,
			wantMsg:  "as error",
			wantHint: "declare it `var c`",
		},
		{
			name: "typed val declaration",
			body: `func main() {
    val c = Counter(0)
    val n Notifier = &c
    send(n)
}`,
			wantMsg:  "as Notifier",
			wantHint: "declare it `var c`",
		},
		{
			name: "address of a parameter returned as the interface",
			body: `func mk(c Counter) Notifier = &c

func main() {
    send(mk(Counter(0)))
}`,
			wantMsg:  "as Notifier",
			wantHint: "declare it `var c`",
		},
		{
			// A receiver cannot be declared `var`; the hint must not say so.
			name: "address of a value receiver",
			body: `func (c Counter) asNotifier() Notifier = &c

func main() {
    send(Counter(0).asNotifier())
}`,
			wantMsg:  "as Notifier",
			wantHint: "declare the receiver as a pointer (c *Counter)",
		},
		{
			name: "val holding a ConstPtr",
			body: `func main() {
    val c = Counter(0)
    val p = &c
    send(p)
}`,
			wantMsg:  "cannot use p (ConstPtr[Counter]) as Notifier",
			wantHint: "pass the address of a `var` (a *Counter)",
		},
		{
			name: "value receivers: the value itself implements it",
			body: `func main() {
    val p = Plain(1)
    send(&p)
}`,
			wantMsg:  "cannot use &p (ConstPtr[Plain]) as Notifier",
			wantHint: "Plain implements it with value receivers: pass p itself",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(constPtrIfacePrelude+"\n"+tc.body+"\n", "main.gala")
			require.Error(t, err)
			msg := err.Error()
			assert.Contains(t, msg, string(galaerr.CodeConstPtrNotInterface))
			assert.Contains(t, msg, tc.wantMsg)
			assert.Contains(t, msg, tc.wantHint)
		})
	}
}

// TestConstPtrOutsideInterfaceSlotsStillWorks is the false-positive guard: an
// `any` slot holds a ConstPtr, a ConstPtr parameter takes one, and `&x` of a
// `var` is a *T that implements the interface.
func TestConstPtrOutsideInterfaceSlotsStillWorks(t *testing.T) {
	trans := newTypeAsCtorTranspiler()
	for name, body := range map[string]string{
		"any parameter": `func main() {
    val c = Counter(0)
    show(&c)
}`,
		"ConstPtr parameter": `func n(p ConstPtr[Counter]) int = p.N

func main() {
    val c = Counter(3)
    Println(n(&c))
}`,
		"address of a var": `func main() {
    var c = Counter(0)
    send(&c)
    Println(c.N)
}`,
		"value-receiver value passed as is": `func main() {
    val p = Plain(1)
    send(p)
}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := trans.Transpile(constPtrIfacePrelude+"\n"+body+"\n", "main.gala")
			require.NoError(t, err)
		})
	}
}
