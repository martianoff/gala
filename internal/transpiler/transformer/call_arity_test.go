package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// A call with the wrong number of arguments is GALA-E0082, in GALA's terms.
// Before, a GALA function or method called that way was emitted as written and
// `go build` reported it against the generated call ("not enough arguments in
// call to fs.WriteFileString have (string, string) want (string, string,
// int)"), and a struct called with one argument too many was GALA-E0043, "is a
// type, not a constructor". The count respects defaults, variadic parameters
// and named arguments, and calls of Go functions keep Go's own check.
func TestCallArgumentCount(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/arity\n\ngala dev\n")
	write("lib/lib.gala", `package lib

func Join(a string, b string) string = a + b

func Tag(a string, b string = "x") string = a + b

struct Pt(X int, Y int)
`)

	const libImport = "import \"example.com/arity/lib\"\n\n"
	cases := []struct {
		name string
		src  string
		code galaerr.ErrorCode // "" means the call is accepted
		msg  []string          // substrings of the error
	}{
		{
			name: "struct constructor with one argument too many",
			src: `struct Decision(Id int, State int, Rule int)

func main() { Println(Decision(1, 2, 3, 4)) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{"too many arguments in call to Decision: expected at most 3, got 4", "remove 1 extra argument; Decision has 3 fields: Id, State, Rule"},
		},
		{
			name: "struct constructor with defaults and too many arguments",
			src: `struct Decision(Id int, State int = 2)

func main() { Println(Decision(1, 2, 3)) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{"too many arguments in call to Decision: expected at most 2, got 3"},
		},
		{
			name: "struct constructor with too few arguments stays GALA-E0045",
			src: `struct Decision(Id int, State int, Rule int)

func main() { Println(Decision(1, 2)) }`,
			code: galaerr.CodeMissingStructField,
			msg:  []string{`missing required field "Rule"`},
		},
		{
			name: "cross-package struct constructor with too many arguments",
			src:  libImport + `func main() { Println(lib.Pt(1, 2, 3)) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{"too many arguments in call to lib.Pt: expected at most 2, got 3", "lib.Pt has 2 fields: X, Y"},
		},
		{
			name: "function with too many arguments",
			src: `func add(a int, b int) int = a + b

func main() { Println(add(1, 2, 3)) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{"too many arguments in call to add: expected 2, got 3", "add takes (a, b)"},
		},
		{
			name: "function with too few arguments",
			src: `func add(a int, b int) int = a + b

func main() { Println(add(1)) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{`missing required argument "b" (parameter 2) in call to add: expected 2, got 1`},
		},
		{
			name: "function with no arguments at all",
			src: `func add(a int, b int) int = a + b

func main() { Println(add()) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{`missing required argument "a" (parameter 1) in call to add: expected 2, got 0`},
		},
		{
			name: "function with a default and too many arguments",
			src: `func add(a int, b int = 2) int = a + b

func main() { Println(add(1, 2, 3)) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{"too many arguments in call to add: expected 1 to 2, got 3", "add takes (a, b = ...)"},
		},
		{
			name: "generic function with too many arguments",
			src: `func id[T any](x T) T = x

func main() { Println(id(1, 2)) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{"too many arguments in call to id: expected 1, got 2"},
		},
		{
			name: "variadic function missing its fixed parameter",
			src: `func sum(first int, rest ...int) int = first

func main() { Println(sum()) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{`missing required argument "first" (parameter 1) in call to sum: expected at least 1, got 0`, "sum takes (first, rest...)"},
		},
		{
			name: "named arguments with too many in total",
			src: `func add(a int, b int = 2) int = a + b

func main() { Println(add(1, 2, 3, b = 4)) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{"too many arguments in call to add"},
		},
		{
			name: "named arguments leaving out a required parameter",
			src: `func add(a int, b int = 2) int = a + b

func main() { Println(add(b = 3)) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{`missing required argument "a" (parameter 1) in call to add`},
		},
		{
			name: "cross-package function with too few arguments",
			src:  libImport + `func main() { Println(lib.Join("a")) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{`missing required argument "b" (parameter 2) in call to Join: expected 2, got 1`},
		},
		{
			name: "cross-package function with too many arguments",
			src:  libImport + `func main() { Println(lib.Tag("a", "b", "c")) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{"too many arguments in call to Tag: expected 1 to 2, got 3"},
		},
		{
			name: "stdlib function with too few arguments",
			src: `import "martianoff/gala/fs"

func main() { fs.WriteFileString("out.txt", "x").Get() }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{`missing required argument "mode" (parameter 3) in call to WriteFileString: expected 3, got 2`, "WriteFileString takes (path, contents, mode)"},
		},
		{
			name: "method with too many arguments",
			src: `struct Counter(N int)

func (c Counter) Plus(x int) int = c.N + x

func main() { Println(Counter(1).Plus(1, 2)) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{"too many arguments in call to Plus: expected 1, got 2"},
		},
		{
			name: "method with no arguments",
			src: `struct Counter(N int)

func (c Counter) Plus(x int) int = c.N + x

func main() { Println(Counter(1).Plus()) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{`missing required argument "x" (parameter 1) in call to Plus: expected 1, got 0`},
		},
		{
			name: "generic method with too many arguments",
			src: `struct Box[T any](V T)

func (b Box[T]) Pick[U any](u U, keep bool) U = u

func main() { Println(Box(1).Pick("a", true, 3)) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{"too many arguments in call to Pick: expected 2, got 3"},
		},

		{
			name: "variadic callee with named arguments and too many positional ones",
			src: `func f(a int, rest ...int) int = a

func main() { Println(f(1, 2, 3, rest = 4)) }`,
			code: galaerr.CodeArgumentCountMismatch,
			msg:  []string{"too many arguments in call to f: expected at most 2 with named arguments, got 4"},
		},

		// Calls that are accepted.
		{
			name: "generic variadic method called with no variadic arguments",
			src: `struct Box[T any](V T)

func (b Box[T]) Count[U any](keep bool, us ...U) int = 0

func main() { Println(Box(1).Count[string](true)); Println(Box(1).Count[string](keep = true)) }`,
		},
		{
			name: "named arguments leaving out a variadic parameter",
			src: `struct Counter(N int)

func (c Counter) Plus(x int, xs ...int) int = c.N + x

func f(a int, rest ...int) int = a

func main() { Println(f(a = 1)); Println(Counter(1).Plus(x = 1)) }`,
		},
		{
			name: "defaults fill what the call leaves out",
			src: `func add(a int, b int = 2, c int = 3) int = a + b + c

func main() { Println(add(1)); Println(add(1, 5)); Println(add(1, c = 0)) }`,
		},
		{
			name: "variadic parameters take any number of arguments",
			src: `func sum(xs ...int) int = 0

func pre(first int, rest ...int) int = first

func main() { Println(sum()); Println(sum(1, 2, 3)); Println(pre(1)); Println(pre(1, 2, 3)) }`,
		},
		{
			name: "variadic method called with no variadic arguments",
			src: `struct Counter(N int)

func (c Counter) Plus(xs ...int) int = c.N

func main() { Println(Counter(1).Plus()); Println(Counter(1).Plus(1, 2)) }`,
		},
		{
			name: "struct constructed with defaults omitted",
			src: `struct Decision(Id int, State int = 2)

func main() { Println(Decision(1)); Println(Decision(1, 3)) }`,
		},
		{
			name: "go functions, variadic ones included",
			src: `import (
    "fmt"
    "strings"
)

func main() { Println(strings.Repeat("a", 2)); Println(fmt.Sprint()); Println(fmt.Sprint(1, 2)) }`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, "package main\n\n"+tc.src)
			if tc.code == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), string(tc.code))
			for _, want := range tc.msg {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}
