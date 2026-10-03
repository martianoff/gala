package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `opaque type UserID int64` lowers to the Go defined type `type UserID int64`
// with a synthesized Hash, and Compare for an ordered underlying type. Every
// output also goes through the package's Go type-check oracle.

func transpileOpaque(t *testing.T, src string) string {
	t.Helper()
	out, err := newAliasExpectedTranspiler().Transpile(src, "opaque_test.gala")
	require.NoError(t, err)
	return out
}

// TestOpaqueTypeDeclaration covers the lowering of each scalar kind: the
// defined type, and the std helper its Hash and Compare delegate to.
func TestOpaqueTypeDeclaration(t *testing.T) {
	cases := []struct {
		decl   string
		want   []string
		absent []string
	}{
		{"opaque type UserID int64", []string{
			"type UserID int64\n",
			"func (s UserID) Hash() uint32 {\n\treturn std.HashInt(int64(s))",
			"func (s UserID) Compare(other UserID) int {\n\treturn std.CompareInt(int64(s), int64(other))",
		}, nil},
		{"opaque type Port uint16", []string{
			"type Port uint16\n",
			"std.HashUint(uint64(s))",
			"std.CompareUint(uint64(s), uint64(other))",
		}, nil},
		{"opaque type Ratio float64", []string{
			"std.HashUint(uint64(float64(s)))",
			"std.CompareFloat(float64(s), float64(other))",
		}, nil},
		{"opaque type Email string", []string{
			"type Email string\n",
			"std.HashString(string(s))",
			"std.CompareString(string(s), string(other))",
		}, nil},
		{"opaque type Flag bool", []string{
			"type Flag bool\n",
			"std.HashBool(bool(s))",
		}, []string{"func (s Flag) Compare("}},
		{"opaque type Letter rune", []string{"type Letter rune\n", "std.HashInt(int64(s))"}, nil},
		{"type Raw int32\nopaque type Code Raw", []string{"type Code Raw\n", "std.HashInt(int64(s))"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.decl, func(t *testing.T) {
			out := transpileOpaque(t, "package main\n\n"+tc.decl+"\n\nfunc main() {}\n")
			for _, w := range tc.want {
				assert.Contains(t, out, w)
			}
			for _, a := range tc.absent {
				assert.NotContains(t, out, a)
			}
		})
	}
}

// TestOpaqueTypeGoNamedUnderlying covers an opaque type over a Go named scalar:
// it is declared over time.Duration, inherits none of its methods, and hashes
// through Duration's own underlying int64.
func TestOpaqueTypeGoNamedUnderlying(t *testing.T) {
	out := transpileOpaque(t, `package main

import "time"

opaque type Timeout time.Duration

func main() {
    Println(int64(Timeout(5)))
}`)
	assert.Contains(t, out, "type Timeout time.Duration\n")
	assert.Contains(t, out, "std.HashInt(int64(s))")
}

// TestOpaqueTypeUserMethodsSuppressSynthesized covers a Hash or Compare the
// user declares: the synthesized one is skipped instead of colliding.
func TestOpaqueTypeUserMethodsSuppressSynthesized(t *testing.T) {
	out := transpileOpaque(t, `package main

opaque type UserID int64

func (u UserID) Hash() uint32 = uint32(u)

func (u UserID) Compare(other UserID) int = int(other) - int(u)

func main() {}`)
	assert.Equal(t, 1, strings.Count(out, ") Hash() uint32"), "only the user's Hash")
	assert.Equal(t, 1, strings.Count(out, ") Compare(other UserID) int"), "only the user's Compare")
	assert.Contains(t, out, "func (u UserID) Hash() uint32")
	assert.NotContains(t, out, "func (s UserID)")
}

// TestOpaqueTypeGoSiblingMethodsSuppressSynthesized covers a Hash declared on
// the opaque type by a hand-written .go file of the package: the generated
// one would be a duplicate method.
func TestOpaqueTypeGoSiblingMethodsSuppressSynthesized(t *testing.T) {
	files, galaFile := samePackageModule(".",
		"package main\n\nfunc (u UserID) Hash() uint32 { return uint32(u) }\n",
		"package main\n\nopaque type UserID int64\n\nfunc main() {\n    Println(UserID(3).Hash())\n}\n")
	out, err := transpileInModule(t, files, galaFile)
	require.NoError(t, err)
	assert.NotContains(t, out, ") Hash() uint32", "Hash is declared in Go; generating it too would not compile")
	assert.Contains(t, out, "func (s UserID) Compare(other UserID) int")
}

// TestOpaqueTypeConversionsAndOperators covers construction and unwrapping
// as Go conversions, operators on the opaque type itself, untyped constants
// mixing with it, methods, and the synthesized methods being callable.
func TestOpaqueTypeConversionsAndOperators(t *testing.T) {
	out := transpileOpaque(t, `package main

opaque type Millis int64

func (m Millis) Seconds() float64 = float64(m) / 1000.0

func later(m Millis) Millis = m + 500

func main() {
    val m = Millis(1500)
    val back = int64(m)
    val zero Millis = 0
    val late = later(m) > Millis(1000)
    Println(back, zero, late, m.Seconds(), m == 1500, m.Compare(zero), m.Hash() > 0, later(42))
}`)
	assert.Contains(t, out, "std.NewImmutable(Millis(1500))")
	assert.Contains(t, out, "std.NewImmutable(int64(m.Get()))")
	assert.Contains(t, out, "return m + 500")
}

// TestOpaqueTypeNotAnAlias covers the distinctness that separates an opaque
// type from an alias: it is a Go defined type, not `type X = Y`, and a value
// of it is inferred as the opaque type.
func TestOpaqueTypeNotAnAlias(t *testing.T) {
	out := transpileOpaque(t, `package main

import . "martianoff/gala/collection_immutable"

opaque type UserID int64

func ids() Array[UserID] = ArrayOf(UserID(1), UserID(2))

func main() {
    val first = ids().Head()
    Println(first)
}`)
	assert.NotContains(t, out, "type UserID = int64")
	assert.Contains(t, out, "ArrayOf(UserID(1), UserID(2))")
}

// TestOpaqueTypeCodecFields covers an opaque type in each codec position: a
// struct field, a collection element, an Option and a HashMap key and value.
// It encodes as its underlying scalar and decodes through a conversion.
func TestOpaqueTypeCodecFields(t *testing.T) {
	out := transpileOpaque(t, `package main

import (
    . "martianoff/gala/collection_immutable"
    "martianoff/gala/json"
)

opaque type UserID int64
opaque type Email string
opaque type Active bool

struct User(Id UserID, Mail Option[Email], On Active, Friends Array[UserID], ByMail HashMap[Email, UserID])

func main() {
    Println(json.Codec[User](json.SnakeCase()).Encode(User(UserID(1), None(), Active(true), EmptyArray(), EmptyHashMap())))
}`)
	assert.Contains(t, out, "int64(t.Id.Get())")
	assert.Contains(t, out, "UserID(")
	assert.Contains(t, out, "!bool(t.On.Get())", "a named bool's emptiness test yields a plain bool")
}
