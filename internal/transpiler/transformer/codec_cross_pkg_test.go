package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// codecCrossPkgFixture is a module whose billing package declares structs
// that another package encodes: one with exported fields, one with unexported
// fields (which only billing can read or construct), one nesting an
// unexported struct type, and one with a field that has no encoding — and
// whose clock package declares structs with fields typed by its own aliases.
func codecCrossPkgFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/codecx\n\ngala dev\n")
	write("billing/billing.gala", `package billing

import . "martianoff/gala/collection_immutable"

struct Email(V string)

struct Secret(v string, n int)

struct entry(Key string)

struct Ledger(Owner Email, entries Array[entry])

struct Hook(Name string, Run func() int)

struct Box[T any](Item T)

func NewSecret(v string) Secret = Secret(v, 3)

func (s Secret) Validate() Try[Secret] = Success(s)

func (l Ledger) Validate() Try[Ledger] = Success(l)

func NewLedger(owner Email) Ledger = Ledger(owner, ArrayOf(entry("k")))
`)
	write("clock/clock.gala", `package clock

import . "martianoff/gala/collection_immutable"

type Millis int64

struct Zone(Name string)

type Where Zone

type Laps Array[Millis]

struct Stamp(at Millis, Zone Where, laps Laps)

func NewStamp(ms int64) Stamp = Stamp(Millis(ms), Zone("utc"), ArrayOf(Millis(1)))

func NoLaps() Laps = EmptyArray[Millis]()

func (s Stamp) Validate() Try[Stamp] = Success(s)
`)
	return root
}

// TestCodecImportedStruct: a struct declared in another package is encoded
// through the StructMeta its own package emits, referenced by its qualified
// name. The consumer used to emit its own _StructMeta_Email naming the type
// unqualified, which does not compile, and could never read or construct the
// unexported fields of a struct like Secret.
func TestCodecImportedStruct(t *testing.T) {
	root := codecCrossPkgFixture(t)
	tests := []struct {
		name        string
		src         string
		contains    []string
		notContains []string
	}{
		{
			name: "nested field of an imported struct",
			src: `package main

import (
    . "martianoff/gala/json"
    "example.com/codecx/billing"
)

struct User(Name string, Mail billing.Email, Sec billing.Secret)

func main() {
    Println(Codec[User](SnakeCase()).Encode(User("x", billing.Email("a@b"), billing.NewSecret("k"))).Get())
}`,
			contains: []string{
				"type _StructMeta_User" + metaSuffix("main.gala") + " struct",
				"billing.StructMeta_Email{}.EncodeFields(w, t.Mail.Get()",
				"billing.StructMeta_Secret{}.EncodeFields(w, t.Sec.Get()",
				"_Mail = billing.StructMeta_Email{}.DecodeFields(r",
				"var _Sec billing.Secret",
			},
			notContains: []string{"_StructMeta_Email", "_StructMeta_Secret", "type StructMeta_"},
		},
		{
			name: "imported struct as the codec root",
			src: `package main

import (
    . "martianoff/gala/json"
    "example.com/codecx/billing"
)

func main() {
    Println(Codec[billing.Secret](SnakeCase()).Encode(billing.NewSecret("k")).Get())
}`,
			contains:    []string{"billing.StructMeta_Secret{}"},
			notContains: []string{"_StructMeta_", "type StructMeta_"},
		},
		{
			name: "imported struct inside collections and Option",
			src: `package main

import (
    . "martianoff/gala/json"
    . "martianoff/gala/collection_immutable"
    "example.com/codecx/billing"
)

struct Batch(
    All Array[billing.Email],
    Recent List[billing.Secret],
    First Option[billing.Email],
    ByName HashMap[string, billing.Secret],
    Book billing.Ledger,
)

func main() {
    Println(Codec[Batch](SnakeCase()).Encode(Batch(ArrayOf[billing.Email](), EmptyList[billing.Secret](), None[billing.Email](), EmptyHashMap[string, billing.Secret](), billing.NewLedger(billing.Email("o")))).Get())
}`,
			contains: []string{
				"billing.StructMeta_Email{}.EncodeFields",
				"billing.StructMeta_Secret{}.EncodeFields",
				"billing.StructMeta_Ledger{}.EncodeFields",
				"Array[billing.Email]",
				"HashMap[string, billing.Secret]",
			},
			notContains: []string{"_StructMeta_Email", "_StructMeta_Secret", "_StructMeta_Ledger", "StructMeta_entry"},
		},
		{
			name: "aliased import",
			src: `package main

import (
    . "martianoff/gala/json"
    b "example.com/codecx/billing"
)

struct User(Mail b.Email)

func main() {
    Println(Codec[User](SnakeCase()).Encode(User(b.Email("a@b"))).Get())
}`,
			contains:    []string{"b.StructMeta_Email{}.EncodeFields"},
			notContains: []string{"billing.StructMeta_Email"},
		},
		{
			name: "dot import",
			src: `package main

import (
    . "martianoff/gala/json"
    . "example.com/codecx/billing"
)

struct User(Mail Email)

func main() {
    Println(Codec[User](SnakeCase()).Encode(User(Email("a@b"))).Get())
}`,
			contains:    []string{"StructMeta_Email{}.EncodeFields", "type _StructMeta_User" + metaSuffix("main.gala") + " struct"},
			notContains: []string{"_StructMeta_Email", "billing.StructMeta_Email"},
		},
		{
			name: "a local struct named like an imported one",
			src: `package main

import (
    . "martianoff/gala/json"
    "example.com/codecx/billing"
)

struct Email(Local int)

struct User(Mine Email, Theirs billing.Email)

func main() {
    Println(Codec[User](SnakeCase()).Encode(User(Email(1), billing.Email("a@b"))).Get())
}`,
			contains: []string{
				"type _StructMeta_Email" + metaSuffix("main.gala") + " struct",
				"_StructMeta_Email" + metaSuffix("main.gala") + "{}.EncodeFields(w, t.Mine.Get()",
				"billing.StructMeta_Email{}.EncodeFields(w, t.Theirs.Get()",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileCrossPkg(t, root, tc.src)
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want, "generated:\n%s", out)
			}
			for _, bad := range tc.notContains {
				assert.NotContains(t, out, bad, "generated:\n%s", out)
			}
		})
	}
}

// TestCodecImportedStructWithoutEncoding: an imported struct whose fields have
// no encoding is still a GALA-E0050 at the codec that asks for it, naming the
// field, even though its StructMeta lives in the declaring package.
func TestCodecImportedStructWithoutEncoding(t *testing.T) {
	root := codecCrossPkgFixture(t)
	for _, tc := range []struct{ name, src, want string }{
		{
			name: "nested",
			src: `package main

import (
    . "martianoff/gala/json"
    "example.com/codecx/billing"
)

struct Plan(Hook billing.Hook)

func main() {
    Println(Codec[Plan](SnakeCase()).Encode(Plan(billing.Hook("h", () => 1))).Get())
}`,
			want: "cannot generate a codec for Plan: field billing.Hook.Run has type func() int",
		},
		{
			name: "root",
			src: `package main

import (
    . "martianoff/gala/json"
    "example.com/codecx/billing"
)

func main() {
    Println(Codec[billing.Hook](SnakeCase()).Encode(billing.Hook("h", () => 1)).Get())
}`,
			want: "cannot generate a codec for billing.Hook: field billing.Hook.Run has type func() int",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, tc.src)
			require.Error(t, err)
			assert.Contains(t, err.Error(), string(galaerr.CodeUnsupportedCodecField))
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestCodecLibraryEmitsStructMetas: a library file emits an exported
// StructMeta for every struct it declares that the codec can describe —
// including unexported fields and unexported struct types, which no other
// package could describe — and nothing for one that has no encoding or is
// generic.
func TestCodecLibraryEmitsStructMetas(t *testing.T) {
	root := codecCrossPkgFixture(t)
	src, err := os.ReadFile(filepath.Join(root, "billing", "billing.gala"))
	require.NoError(t, err)
	out, err := transpileCrossPkgFile(t, root, string(src), filepath.Join(root, "billing", "billing.gala"))
	require.NoError(t, err)
	for _, want := range []string{
		"type StructMeta_Email struct",
		"type StructMeta_Secret struct",
		"type StructMeta_entry struct",
		"type StructMeta_Ledger struct",
		"func (_ StructMeta_Secret) EncodeFields(w std.FieldEncoder, t Secret,",
		"w.WriteString(t.v.Get())",
		"return Secret{v: std.NewImmutable(_v), n: std.NewImmutable(_n)}",
		"StructMeta_entry{}.EncodeFields(w, __elem",
	} {
		assert.Contains(t, out, want, "generated:\n%s", out)
	}
	for _, bad := range []string{"StructMeta_Hook", "StructMeta_Box", "_StructMeta_"} {
		assert.NotContains(t, out, bad, "generated:\n%s", out)
	}
}

// transpileCrossPkgFile transpiles src as the file at path inside the module
// at root.
func transpileCrossPkgFile(t *testing.T, root, src, path string) (string, error) {
	t.Helper()
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, append([]string{root}, getStdSearchPath()...), root)
	return newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator()).
		Transpile(src, path)
}

// TestCodecImportedStructWithAliasedFields: a struct whose fields name its
// own package's aliases (`type Millis int64`, an alias of a struct, an alias
// of a collection) is encoded from another package. The consumer checks the
// fields of the StructMeta it references; it used to look those alias names
// up in its own package, find nothing, and report GALA-E0050 for a struct its
// declaring package had described without complaint.
func TestCodecImportedStructWithAliasedFields(t *testing.T) {
	root := codecCrossPkgFixture(t)
	lib, err := os.ReadFile(filepath.Join(root, "clock", "clock.gala"))
	require.NoError(t, err)
	libOut, err := transpileCrossPkgFile(t, root, string(lib), filepath.Join(root, "clock", "clock.gala"))
	require.NoError(t, err)
	assert.Contains(t, libOut, "type StructMeta_Stamp struct", "generated:\n%s", libOut)

	out, err := transpileCrossPkg(t, root, `package main

import (
    . "martianoff/gala/json"
    "example.com/codecx/clock"
)

type Millis bool

struct Event(Name string, At clock.Stamp, Took clock.Millis, Laps clock.Laps, Where clock.Where, Mine Millis)

func main() {
    val s = clock.NewStamp(5)
    Println(Codec[Event](SnakeCase()).Encode(Event("e", s, clock.Millis(2), clock.NoLaps(), clock.Zone("z"), true)).Get())
    Println(Codec[clock.Stamp](SnakeCase()).Encode(clock.NewStamp(6)).Get())
}`)
	require.NoError(t, err)
	for _, want := range []string{
		"clock.StructMeta_Stamp{}.EncodeFields(w, t.At.Get()",
		"clock.StructMeta_Zone{}.EncodeFields(w, t.Where.Get()",
		"w.WriteInt64(t.Took.Get())",
		"w.WriteBool(t.Mine.Get())",
	} {
		assert.Contains(t, out, want, "generated:\n%s", out)
	}
	assert.NotContains(t, out, "_StructMeta_Stamp", "generated:\n%s", out)
}

// TestCodecLibraryStructWithGoStructField: a library struct whose field is a
// struct of a plain Go package. The analyzer describes that Go struct with
// the same metadata as a GALA one, but no Go package declares a StructMeta,
// so the library must not reference one. It used to emit StructMeta_Conf
// calling box.StructMeta_Box, which does not compile; a codec asking for a
// struct with such a field is GALA-E0050 instead.
func TestCodecLibraryStructWithGoStructField(t *testing.T) {
	files := map[string]string{
		"go.mod":     "module example.com/gostructfield\n\ngo 1.25\n",
		"gala.mod":   "module example.com/gostructfield\n",
		"box/box.go": "package box\n\ntype Box struct{ Size int }\n",
		"lib/main.gala": `package lib

import "example.com/gostructfield/box"

struct Conf(Name string, B box.Box)

struct Plain(Name string)
`,
	}
	out, err := transpileInModule(t, files, "lib/main.gala")
	require.NoError(t, err)
	assert.NotContains(t, out, "StructMeta_Box", "generated:\n%s", out)
	assert.NotContains(t, out, "StructMeta_Conf", "generated:\n%s", out)
	assert.Contains(t, out, "type StructMeta_Plain struct", "generated:\n%s", out)

	files["main.gala"] = `package main

import (
    . "martianoff/gala/json"
    "example.com/gostructfield/box"
)

struct Wrap(B box.Box)

func main() {
    Println(Codec[Wrap](SnakeCase()).Encode(Wrap(box.Box(Size = 1))).Get())
}
`
	_, err = transpileInModule(t, files, "main.gala")
	require.Error(t, err)
	assert.Contains(t, err.Error(), string(galaerr.CodeUnsupportedCodecField))
}
