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

// TestOpaqueTypeDefaults covers untyped constant defaults for a parameter and
// a shorthand struct field of an opaque type: they fit it as they fit its
// underlying type.
func TestOpaqueTypeDefaults(t *testing.T) {
	out := transpileOpaque(t, `package main

opaque type Millis int64

struct Retry(Wait Millis = 250, Tries int = 3)

func sleepFor(ms Millis = 100) Millis = ms

func main() {
    Println(sleepFor(), Retry().Wait)
}`)
	assert.Contains(t, out, "sleepFor(100)")
}

// TestOpaqueTypeUntypedConstantFields covers untyped string, bool and numeric
// constants filling Immutable slots of opaque types — a constructor field, a
// Copy override and a typed val: NewImmutable is instantiated with the opaque
// type, since the constant's default type (string, bool, int) is not it.
func TestOpaqueTypeUntypedConstantFields(t *testing.T) {
	out := transpileOpaque(t, `package main

opaque type Email string
opaque type Active bool
opaque type UserID int64

struct User(Id UserID, Mail Email, On Active)

func main() {
    val u = User(0, "", false)
    val v = u.Copy(Mail = "a@b", On = true, Id = 7)
    val m Email = "c@d"
    Println(u, v, m)
}`)
	assert.Contains(t, out, `std.NewImmutable[Email]("")`)
	assert.Contains(t, out, `std.NewImmutable[Active](false)`)
	assert.Contains(t, out, `std.NewImmutable[Email]("a@b")`)
	assert.Contains(t, out, `std.NewImmutable[Active](true)`)
	assert.Contains(t, out, `std.NewImmutable[UserID](7)`)
}

// TestOpaqueTypeBoolOperators covers comparisons and logical operators filling
// a slot of an opaque type over bool: a comparison is an untyped bool and
// `&&` / `||` / `!` take their operands' type, so Go accepts them unconverted.
func TestOpaqueTypeBoolOperators(t *testing.T) {
	transpileOpaque(t, `package main

opaque type Enabled bool

struct Settings(On Enabled)

func both(a Enabled, b Enabled) Enabled = a && b

func main() {
    val x = 4
    val s = Settings(x > 3)
    val e Enabled = !both(Enabled(true), Enabled(false)) || s.On
    val ab = s.On && e
    val untyped Enabled = x > 1 && x < 9
    Println(e, both(ab, !ab), untyped)
}`)

	// A logical operator over plain bools is a bool, not an Enabled.
	_, err := newAliasExpectedTranspiler().Transpile(`package main

opaque type Enabled bool

func main() {
    val flag = 3 > 2
    val e Enabled = flag && flag
    Println(e)
}`, "opaque_test.gala")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0064")
}

// TestOpaqueTypeSynthesizedMethodTypes covers the result types inference
// gives the generated Hash, Compare and a struct's Equal.
func TestOpaqueTypeSynthesizedMethodTypes(t *testing.T) {
	out := transpileOpaque(t, `package main

opaque type UserID int64

struct Pair(A int, B int)

func pick(b bool) string {
    val h = if (b) UserID(1).Hash() else UserID(2).Hash()
    val c = if (b) UserID(1).Compare(UserID(2)) else UserID(2).Compare(UserID(1))
    val e = if (b) Pair(1, 2).Equal(Pair(1, 2)) else Pair(1, 2).Equal(Pair(2, 1))
    s"$h $c $e"
}

func main() {
    Println(pick(true))
}`)
	// An if-expression's IIFE spells the branches' inferred type.
	assert.Contains(t, out, "func() uint32 {")
	assert.Contains(t, out, "func() int {")
	assert.Contains(t, out, "func() bool {")
}

// TestOpaqueTypePhantomInstantiationsDiffer covers phantom type parameters:
// Id[User] and Id[Order] are different types, so a direct conversion between
// them is GALA-E0063 and passing one for the other is GALA-E0064.
func TestOpaqueTypePhantomInstantiationsDiffer(t *testing.T) {
	const decls = `package main

struct User(Name string)
struct Order(Total int)

opaque type Id[T any] int64

func orderKey(id Id[Order]) int64 = int64(id)
`
	trans := newAliasExpectedTranspiler()
	_, err := trans.Transpile(decls+`
func main() {
    val u = Id[User](7)
    Println(Id[Order](u))
}`, "opaque_test.gala")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0063")
	assert.Contains(t, err.Error(), "cannot convert Id[User] to Id[Order] directly")

	_, err = trans.Transpile(decls+`
func main() {
    val u = Id[User](7)
    Println(orderKey(u))
}`, "opaque_test.gala")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0064")

	// Nested type arguments differ too.
	_, err = trans.Transpile(`package main

import . "martianoff/gala/collection_immutable"

struct User(Name string)
struct Order(Total int)

opaque type Id[T any] int64

func main() {
    val u = Id[Array[User]](1)
    Println(Id[Array[Order]](u))
}`, "opaque_test.gala")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0063")

	// Composite and `any` tags are told apart as well.
	for _, conv := range []string{
		"Id[*Order](Id[*User](1))",
		"Id[Array[Order]](Id[Array[User]](1))",
		"Id[User](Id[any](1))",
	} {
		_, err = trans.Transpile(`package main

import . "martianoff/gala/collection_immutable"

struct User(Name string)
struct Order(Total int)

opaque type Id[T any] int64

func main() {
    Println(`+conv+`)
}`, "opaque_test.gala")
		require.Error(t, err, conv)
		assert.Contains(t, err.Error(), "GALA-E0063", conv)
	}

	// The same instantiation through an alias of the argument is the same type.
	transpileOpaque(t, decls+`
type Customer User

func main() {
    val c = Id[Customer](3)
    Println(orderKey(Id[Order](7)), Id[Order](int64(Id[User](8))), Id[User](c))
}`)
}

// TestOpaqueTypeImportedNameDoesNotShadowLocal covers a local declaration
// that shares its name with another package's opaque type, imported by name:
// a bare name is not in scope through a named import, so the local alias is
// what the name means and nothing about it is opaque. Diagnostics spell an
// imported opaque type with the import's alias.
func TestOpaqueTypeImportedNameDoesNotShadowLocal(t *testing.T) {
	files := map[string]string{
		"go.mod":       "module example.com/sibs\n\ngo 1.25\n",
		"gala.mod":     "module example.com/sibs\n",
		"lib/lib.gala": "package lib\n\nopaque type Cents int64\n\nfunc Charge(c Cents) int64 = int64(c)\n",
		"main.gala": "package main\n\nimport b \"example.com/sibs/lib\"\n\ntype Cents int64\n\n" +
			"opaque type Price Cents\n\n" +
			"func main() {\n    val n int64 = 5\n    val c Cents = n\n    Println(c, Price(1), b.Charge(b.Cents(2)))\n}\n",
	}
	_, err := transpileInModule(t, files, "main.gala")
	require.NoError(t, err)

	files["main.gala"] = "package main\n\nimport b \"example.com/sibs/lib\"\n\n" +
		"func main() {\n    val n int64 = 5\n    Println(b.Charge(n))\n}\n"
	_, err = transpileInModule(t, files, "main.gala")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0064")
	assert.Contains(t, err.Error(), "convert explicitly: b.Cents(n)")
}

// TestOpaqueTypeOverOwnGoNamedScalar covers an opaque type declared over a
// scalar named type of the package's own hand-written .go file.
func TestOpaqueTypeOverOwnGoNamedScalar(t *testing.T) {
	files, galaFile := samePackageModule(".",
		"package main\n\ntype RawID int64\n",
		"package main\n\nopaque type AccountID RawID\n\nfunc main() {\n    Println(AccountID(3).Hash())\n}\n")
	out, err := transpileInModule(t, files, galaFile)
	require.NoError(t, err)
	assert.Contains(t, out, "type AccountID RawID")
	assert.Contains(t, out, "std.HashInt(int64(s))")
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

// TestOpaqueTypePattern covers `case UserID(p)`: on a subject of the opaque
// type it converts to the underlying value and matches p against it; on an
// `any` subject it asserts the opaque type first. Nothing is generated on the
// type itself.
func TestOpaqueTypePattern(t *testing.T) {
	out := transpileOpaque(t, `package main

opaque type UserID int64

func describe(id UserID) string = id match {
    case UserID(0) => "nobody"
    case UserID(n) => s"user $n"
    case _ => "?"
}

func kind(v any) string = v match {
    case UserID(n) => s"id $n"
    case _ => "other"
}

func main() {
    Println(describe(UserID(3)), kind(UserID(4)), kind(int64(4)))
}`)
	assert.Contains(t, out, "int64(obj) == 0")
	assert.Contains(t, out, ".(UserID)")
	assert.NotContains(t, out, "Unapply", "no extractor is generated for an opaque type")
}

// TestOpaqueTypePhantomCodec covers a codec field of a phantom-typed opaque
// type and the root value of an opaque type: both are the bare scalar.
func TestOpaqueTypePhantomCodec(t *testing.T) {
	out := transpileOpaque(t, `package main

import "martianoff/gala/json"

struct User(Name string)

opaque type Id[T any] int64

struct Ref(Who Id[User])

func main() {
    Println(json.Codec[Ref](json.AsIs()).Encode(Ref(Id[User](1))), json.Value[Id[User]]().Encode(Id[User](2)))
}`)
	assert.Contains(t, out, "int64(t.Who.Get())")
	assert.Contains(t, out, "Id[User](")
}
