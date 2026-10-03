package transformer_test

import (
	"testing"

	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

// TestUnresolvedInventoryCountsValuesNotMethodNames pins what the
// unresolved-type inventory counts for a method call.
//
// A method call is typed as a call: `opt.Map(f)` has a type, and the method
// name `opt.Map` on its own is not a value anyone uses. The inventory used to
// count the method name anyway, from two places that ask about it on the way
// to the call — the Immutable-field check, which asked a std type's method for
// its field type, and the Hindley-Milner bridge, which converts a call's
// callee — and those entries were most of the corpus inventory.
//
// What must still count is anything value-shaped: a method taken as a value
// (`val get = b.Get`), on a user type or a std one, and a callee that is
// itself a call.
func TestUnresolvedInventoryCountsValuesNotMethodNames(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// wantNone asserts the inventory is empty; otherwise wantContains
		// lists entries that must be present. Entries that only cascade from
		// them (a call through an untyped method value) are not pinned.
		wantNone     bool
		wantContains []string
	}{
		{
			name: "method chain on a std type",
			src: `package main

func main() {
    Println(Some(1).Map((x) => x + 1).GetOrElse(0))
}`,
			wantNone: true,
		},
		{
			name: "user Get on a generic receiver, inside generic code",
			src: `package main

struct Box[T any](Value T)

func (b *Box[T]) Get() T = b.Value

struct Slot[T any](Value T)

func (s *Slot[T]) Get() Option[T] = Some(s.Value)

func viaPointer[T any](b *Box[T], fallback T) T {
    val v = b.Get()
    return Some(v).GetOrElse(fallback)
}

func viaPointerOption[T any](s *Slot[T]) string =
    s.Get().Map((v) => s"slot holds $v").GetOrElse("empty slot")

func main() {
    var b = Box(41)
    Println(viaPointer(&b, 0))
    var s = Slot(true)
    Println(viaPointerOption(&s))
}`,
			wantNone: true,
		},
		{
			name: "method chain on a Go type inside Try",
			src: `package main

import "net/url"

func queryValueSize(raw string) int =
    Try(url.ParseQuery(raw)).Map((q) => q.Get("a").Size()).GetOrElse(-1)

func main() {
    Println(queryValueSize("a=hello"))
}`,
			wantNone: true,
		},
		{
			name: "user method taken as a value",
			src: `package main

struct Box[T any](Value T)

func (b *Box[T]) Get() T = b.Value

func viaMethodValue[T any](b *Box[T]) T {
    val get = b.Get
    return get()
}

func main() {
    var b = Box(41)
    Println(viaMethodValue(&b))
}`,
			// A method value has the method's signature, with the receiver's
			// type arguments substituted, so nothing is left unresolved.
			wantNone: true,
		},
		{
			name: "std method taken as a value",
			src: `package main

func orElse[T any](o Option[T], fallback T) T {
    val get = o.GetOrElse
    return get(fallback)
}

func main() {
    Println(orElse(Some(1), 0))
}`,
			wantNone: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GALA_WARN_TYPES", "1")
			trans, tr := newTranspilerWithTransformer()
			_, err := trans.Transpile(tc.src, "main.gala")
			require.NoError(t, err)

			var got []string
			for _, u := range transformer.UnresolvedTypes(tr) {
				got = append(got, u.Expr)
			}
			if tc.wantNone {
				require.Empty(t, got)
				return
			}
			for _, want := range tc.wantContains {
				require.Contains(t, got, want)
			}
		})
	}
}
