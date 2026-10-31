package transformer_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// A bare name written as a value that the implicitly imported std package
// provides as a type and an explicitly dot-imported package provides as a
// function or package-level value is ambiguous: neither reading outranks the
// other, so the use is rejected with GALA-E0087 instead of silently picking
// std's type. The rows cover a call as a statement, a call with an argument,
// a function used as a value, a call with explicit type arguments and a
// dot-imported val. A qualified spelling of either side, a type position, a
// local binding and a named (not dot) import stay unambiguous.
func TestDotImportPreludeAmbiguity(t *testing.T) {
	searchRoot := writeGalaModule(t, map[string]string{
		"util/util.gala": `package util

func Void() = Println("util")

func NoSuchElementError(msg string) string = "E:" + msg

func Ordered(n int) int = n * 10

func Hashable[T any](x T) T = x
`,
		"consts/consts.gala": "package consts\n\nval Ordered = 3\n",
	})
	trans := newDocGuardTranspilerWithPaths(searchRoot)
	mainPath := filepath.Join(searchRoot, "main", "main.gala")

	const dotUtil = "import . \"martianoff/gala/util\"\n\n"
	tests := []struct {
		name string
		src  string
		// wantErr is the ambiguous name; "" means the source compiles.
		wantErr string
		// want holds substrings of the generated Go, or of the error.
		want []string
	}{
		{
			name:    "zero-argument call as a statement",
			src:     dotUtil + "func main() {\n    Void()\n}",
			wantErr: "Void",
		},
		{
			name:    "call with an argument",
			src:     dotUtil + "func main() {\n    Println(NoSuchElementError(\"x\"))\n}",
			wantErr: "NoSuchElementError",
		},
		{
			name: "function used as a value",
			src: dotUtil + "func apply(f func(int) int, n int) int = f(n)\n\n" +
				"func main() {\n    Println(apply(Ordered, 2))\n}",
			wantErr: "Ordered",
		},
		{
			name:    "call with explicit type arguments",
			src:     dotUtil + "func main() {\n    Println(Hashable[int](5))\n}",
			wantErr: "Hashable",
		},
		{
			name:    "dot-imported package-level val",
			src:     "import . \"martianoff/gala/consts\"\n\nfunc main() {\n    Println(Ordered + 1)\n}",
			wantErr: "Ordered",
		},
		{
			name: "package qualifier picks the function",
			src: "import \"martianoff/gala/util\"\n\n" +
				"func main() {\n    util.Void()\n    Println(util.Ordered(4))\n}",
			want: []string{"util.Void()", "util.Ordered(4)"},
		},
		{
			name: "std qualifier alone compiles",
			src: "import (\n    \"martianoff/gala/std\"\n    . \"martianoff/gala/util\"\n)\n\n" +
				"func main() {\n    Println(std.NoSuchElementError(\"x\").Error())\n}",
			want: []string{"std.NoSuchElementError"},
		},
		{
			name: "a type position keeps naming std's type",
			src: dotUtil + "func run() Try[Void] = FromError(nil)\n\n" +
				"func describe(e NoSuchElementError) string = e.Error()\n\n" +
				"func main() {\n    Println(run().IsSuccess())\n}",
			want: []string{"func run() std.Try[std.Void]", "func describe(e std.NoSuchElementError) string"},
		},
		{
			name: "a local binding shadows both",
			src:  dotUtil + "func main() {\n    val Ordered = 7\n    Println(Ordered)\n}",
			want: []string{"Println(Ordered)"},
		},
		{
			name: "a type argument of an instantiation names std's type",
			src: dotUtil + "func main() {\n    val e = None[NoSuchElementError]()\n" +
				"    val v = None[Void]()\n    Println(e.IsEmpty(), v.IsEmpty())\n}",
			want: []string{"std.None[std.NoSuchElementError]", "std.None[std.Void]"},
		},
		{
			name: "a constructor pattern names std's type",
			src: dotUtil + "func describe(e error) string = e match {\n" +
				"    case NoSuchElementError(m) => m\n    case _ => \"other\"\n}\n\n" +
				"func main() {\n    Println(describe(NoSuchElement(\"x\")))\n}",
			want: []string{"std.NoSuchElementError"},
		},
		{
			name: "the hint names the qualifier std is imported under",
			src: "import (\n    s \"martianoff/gala/std\"\n    . \"martianoff/gala/util\"\n)\n\n" +
				"func main() {\n    Println(s.NoSuchElementError(\"x\").Error())\n    Void()\n}",
			wantErr: "Void",
			want:    []string{"for the type, write `s.Void`"},
		},
		{
			name: "dot-imported val as a stable-identifier pattern",
			src: "import . \"martianoff/gala/consts\"\n\n" +
				"func main() {\n    Println(3 match {\n        case Ordered => \"three\"\n        case _ => \"other\"\n    })\n}",
			wantErr: "Ordered",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\n" + tc.src + "\n"
			got, err := trans.Transpile(src, mainPath)
			if tc.wantErr == "" {
				require.NoError(t, err)
				for _, w := range tc.want {
					assert.Contains(t, got, w)
				}
				return
			}
			require.Error(t, err, "generated:\n%s", got)
			assert.Contains(t, err.Error(), string(galaerr.CodeDotImportPreludeAmbiguity))
			assert.Contains(t, err.Error(), `"`+tc.wantErr+`"`)
			for _, w := range tc.want {
				assert.Contains(t, err.Error(), w)
			}
		})
	}
}
