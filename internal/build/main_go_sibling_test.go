package build

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuild_MainPackageGoSibling builds a `package main` program whose package
// also holds a hand-written .go file. The declarations in that file are part of
// the program, and the GALA code must be typed against them exactly as a
// library's would be: `.Size()` on a field of a Go struct (#613), a
// pointer-receiver method through a val (#614), a type name an import also
// exports (#616), resource.Using over a Go constructor, with and without a
// partial type-argument list (#618), a literal match, a stable identifier and
// a codec field over a Go named scalar; methods declared in Go on a struct
// declared in GALA (#615), including on a generic one, where the receiver's
// type arguments are substituted into the method's signature; and a struct
// declared in Go built with named arguments, in `package main` and in a
// library package.
func TestBuild_MainPackageGoSibling(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	const moduleName = "example.com/mainsib"
	projectDir := t.TempDir()
	for name, content := range map[string]string{
		"gala.mod": "module " + moduleName + "\n\ngala 0.0.0\n",
		"go.mod":   "module " + moduleName + "\n\ngo 1.22\n",
		// An import that exports a type of the same name as a local one.
		"collide/collide.go": "package collide\n\ntype Response struct{ Status int }\n",
		// A library package whose GALA code builds a struct of its own
		// hand-written .go file with named arguments.
		"shelf/shelf.go": "package shelf\n\ntype Slot struct {\n\tLabel string\n\tScale func(int) int\n\tcount int\n}\n\nfunc (s Slot) Count() int { return s.count }\n",
		"shelf/shelf.gala": `package shelf

func Make(label string) Slot = Slot(Label = label, Scale = (n) => n * 10, count = 2)
`,
		"sibling.go": `package main

type Bag struct {
	Items []string
	Text  string
}

type Greeter struct{ Name string }

func (g Greeter) Hello() string    { return "hello " + g.Name }
func (g *Greeter) Rename(s string) { g.Name = s }

func NewGreeter(n string) Greeter { return Greeter{Name: n} }

type Res struct{ name string }

func (r Res) Close() error  { return nil }
func (r Res) Name() string { return r.name }

func OpenRes(p string) Res { return Res{name: p} }

type Response struct {
	Status int
	Extra  int
}

type Status int

const Two Status = 2

func Current() Status { return Two }

type Millis int64

func (r Repo) Save() string { return "saved " + r.Name }
func (r *Repo) Touch()      {}

func (b Box[T]) Get() T                     { return b.V }
func (b Box[U]) Map(f func(U) U) Box[U]     { return Box[U]{V: f(b.V)} }
func (b Box[T]) With(s string) Pair[T, string] { return Pair[T, string]{A: b.V, B: s} }
func (p *Pair[A, B]) Swap() Pair[B, A]      { return Pair[B, A]{A: p.B, B: p.A} }
`,
		"main.gala": `package main

import (
    "example.com/mainsib/collide"
    "example.com/mainsib/shelf"
    "martianoff/gala/go_interop"
    "martianoff/gala/json"
    "martianoff/gala/resource"
)

struct Event(Name string, At Millis)

// Declared here; its methods are declared in sibling.go.
struct Repo(var Name string)

// Generic, with methods declared in sibling.go.
struct Box[T any](var V T)

struct Pair[A any, B any](var A A, var B B)

func size(r Response) int = r.Extra

func main() {
    val b = Bag{Items: go_interop.SliceOf("a", "b"), Text: "héllo"}
    Println(s"${b.Items.Size()} ${b.Text.Size()} ${b.Text.ByteSize()}")
    val named = Bag(Items = go_interop.SliceOf("c"), Text = "named")
    Println(s"${named.Items.Size()} ${named.Text}")
    val slot = shelf.Make("s")
    Println(s"${slot.Label} ${slot.Scale(4)} ${slot.Count()}")

    val g = NewGreeter("world")
    g.Rename("there")
    Println(g.Hello())

    Println(resource.Using(OpenRes("res"), (r) => r.Name().Size()))
    Println(resource.Using[Res](OpenRes("res2"), (r) => r.Name().Size()))

    Println(s"${size(Response{Status: 1, Extra: 2})} ${collide.Response(Status = 3).Status}")

    val word = Current() match {
        case 1 => "one"
        case 2 => "two"
        case _ => "other"
    }
    Println(word)
    Println(Current() match {
        case Two => "stable two"
        case _ => "other"
    })

    Println(json.Codec[Event](json.SnakeCase()).Encode(Event("e", Millis(5))).GetOrElse("failed"))

    val repo = Repo("x")
    repo.Touch()
    Println(repo.Save())

    // Some(...) spells its argument's type, so each result must be typed.
    val box = Box(20)
    val doubled = box.Map((n) => n * 2)
    Println(Some(doubled.Get()).GetOrElse(0) + 2)
    val pair = box.With("w")
    val swapped = pair.Swap()
    Println(s"${Some(swapped.A).GetOrElse("")} ${Some(swapped.B).GetOrElse(0) + 1}")
}
`,
	} {
		writeFixtureFile(t, filepath.Join(projectDir, filepath.FromSlash(name)), content)
	}

	isolateUserState(t)
	setEnvForTest(t, "GOCACHE", filepath.Join(t.TempDir(), "gocache"))
	alignGorootWithPathGo(t)
	chdirForTest(t, projectDir)

	b, err := NewBuilder(projectDir, "test", false)
	require.NoError(t, err)
	binPath, buildErr := b.Build("")
	if buildErr != nil {
		if isToolchainEnvError(buildErr.Error()) {
			t.Skipf("skipping end-to-end check: Go toolchain unavailable/mismatched in this environment: %v", buildErr)
		}
		t.Fatalf("gala build failed: %v", buildErr)
	}
	out, runErr := runBuiltBinary(binPath)
	require.NoError(t, runErr, "built binary failed to run; output:\n%s", out)
	assert.Equal(t, strings.Join([]string{
		"2 5 6",
		"1 named",
		"s 40 2",
		// The val is not renamed: the pointer method runs on a copy.
		"hello world",
		"3",
		"4",
		"2 3",
		"two",
		"stable two",
		`{"name":"e","at":5}`,
		"saved x",
		"42",
		"w 21",
	}, "\n"), strings.TrimSpace(strings.ReplaceAll(out, "\r\n", "\n")))
}

// TestBuild_GoSiblingMethodsBesideSameNamedGoType builds a program importing
// a GALA package named like a Go package (GALA `fs`, Go `io/fs`) whose Go
// sibling declares methods on a GALA type named like one of the Go package's
// types (FileInfo). Both types are filed under "fs.FileInfo". The record of
// the sibling's methods used to replace io/fs's type, so the program's other
// file, which uses io/fs's FileInfo, was told io/fs does not provide it. Each
// type keeps its own methods, including Size, which both declare with
// different results.
func TestBuild_GoSiblingMethodsBesideSameNamedGoType(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	const moduleName = "example.com/fscollide"
	projectDir := t.TempDir()
	for name, content := range map[string]string{
		"gala.mod": "module " + moduleName + "\n\ngala 0.0.0\n",
		"go.mod":   "module " + moduleName + "\n\ngo 1.22\n",
		"fs/sibling.go": `package fs

import "strings"

func (f FileInfo) Upper() string { return strings.ToUpper(f.Label) }

func (f FileInfo) Size() string { return "size of " + f.Label }
`,
		"fs/fs.gala": `package fs

struct FileInfo(var Label string)

func Shout(f FileInfo) string = f.Upper()
`,
		"main.gala": `package main

import (
    "os"
    "example.com/fscollide/fs"
)

func main() {
    Println(describe(os.Stat("gala.mod").Get()))
    Println(fs.Shout(fs.FileInfo("x")))
    Println(Some(fs.FileInfo("y").Size()).GetOrElse("") + "!")
}
`,
		"describe.gala": `package main

import "io/fs"

func describe(info fs.FileInfo) string {
    val dir = Some(info.IsDir())
    val size = Some(info.Size())
    s"${info.Name()} dir=${dir.GetOrElse(true)} sized=${size.GetOrElse(0) > 0}"
}
`,
	} {
		writeFixtureFile(t, filepath.Join(projectDir, filepath.FromSlash(name)), content)
	}

	isolateUserState(t)
	setEnvForTest(t, "GOCACHE", filepath.Join(t.TempDir(), "gocache"))
	alignGorootWithPathGo(t)
	chdirForTest(t, projectDir)

	b, err := NewBuilder(projectDir, "test", false)
	require.NoError(t, err)
	binPath, buildErr := b.Build("")
	if buildErr != nil {
		if isToolchainEnvError(buildErr.Error()) {
			t.Skipf("skipping end-to-end check: Go toolchain unavailable/mismatched in this environment: %v", buildErr)
		}
		t.Fatalf("gala build failed: %v", buildErr)
	}
	out, runErr := runBuiltBinary(binPath)
	require.NoError(t, runErr, "built binary failed to run; output:\n%s", out)
	assert.Equal(t, strings.Join([]string{
		"gala.mod dir=false sized=true",
		"X",
		"size of y!",
	}, "\n"), strings.TrimSpace(strings.ReplaceAll(out, "\r\n", "\n")))
}

// TestBuild_GoNamedFuncTypeSlots builds a program whose lambdas fill slots
// typed by non-generic Go named function types — one of the package's own .go
// file (Visitor) and imported ones (fs.WalkDirFunc, http.HandlerFunc) — as a
// GALA function's parameter, a val annotation, a result and a struct field.
// Each lambda takes the type's underlying signature.
func TestBuild_GoNamedFuncTypeSlots(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	const moduleName = "example.com/namedfunc"
	projectDir := t.TempDir()
	for name, content := range map[string]string{
		"gala.mod":   "module " + moduleName + "\n\ngala 0.0.0\n",
		"go.mod":     "module " + moduleName + "\n\ngo 1.22\n",
		"visitor.go": "package main\n\ntype Visitor func(int) bool\n",
		"main.gala": `package main

import (
    "io/fs"
    "net/http"
)

struct Walker(Fn fs.WalkDirFunc)

func check(v Visitor) bool = v(3)

func skipAll() fs.WalkDirFunc = (path, d, err) => fs.SkipAll

func main() {
    Println(check((n) => n > 2))
    val keep fs.WalkDirFunc = (path, d, err) => err
    Println(keep(".", nil, nil) == nil)
    val w = Walker(Fn = (path, d, err) => err)
    Println(w.Fn(".", nil, nil) == nil)
    Println(skipAll()(".", nil, nil) == fs.SkipAll)
    val h http.HandlerFunc = (rw, r) => rw.WriteHeader(204)
    Println(h != nil)
}
`,
	} {
		writeFixtureFile(t, filepath.Join(projectDir, filepath.FromSlash(name)), content)
	}

	isolateUserState(t)
	setEnvForTest(t, "GOCACHE", filepath.Join(t.TempDir(), "gocache"))
	alignGorootWithPathGo(t)
	chdirForTest(t, projectDir)

	b, err := NewBuilder(projectDir, "test", false)
	require.NoError(t, err)
	binPath, buildErr := b.Build("")
	if buildErr != nil {
		if isToolchainEnvError(buildErr.Error()) {
			t.Skipf("skipping end-to-end check: Go toolchain unavailable/mismatched in this environment: %v", buildErr)
		}
		t.Fatalf("gala build failed: %v", buildErr)
	}
	out, runErr := runBuiltBinary(binPath)
	require.NoError(t, runErr, "built binary failed to run; output:\n%s", out)
	assert.Equal(t, "true\ntrue\ntrue\ntrue\ntrue", strings.TrimSpace(strings.ReplaceAll(out, "\r\n", "\n")))
}