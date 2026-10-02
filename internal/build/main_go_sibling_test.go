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
// exports (#616), resource.Using over a Go constructor (#618), and a literal
// match, a stable identifier and a codec field over a Go named scalar.
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
`,
		"main.gala": `package main

import (
    "example.com/mainsib/collide"
    "martianoff/gala/go_interop"
    "martianoff/gala/json"
    "martianoff/gala/resource"
)

struct Event(Name string, At Millis)

func size(r Response) int = r.Extra

func main() {
    val b = Bag{Items: go_interop.SliceOf("a", "b"), Text: "héllo"}
    Println(s"${b.Items.Size()} ${b.Text.Size()} ${b.Text.ByteSize()}")

    val g = NewGreeter("world")
    g.Rename("there")
    Println(g.Hello())

    Println(resource.Using(OpenRes("res"), (r) => r.Name().Size()))

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
		// The val is not renamed: the pointer method runs on a copy.
		"hello world",
		"3",
		"2 3",
		"two",
		"stable two",
		`{"name":"e","at":5}`,
	}, "\n"), strings.TrimSpace(strings.ReplaceAll(out, "\r\n", "\n")))
}
