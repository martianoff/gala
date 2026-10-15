package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestInterfaceLiteral pins interface literals: `Repo(Find = ...)` builds a
// value of an interface from one function per method, lowered to a private
// helper struct of this file whose methods call those functions.
func TestInterfaceLiteral(t *testing.T) {
	const decls = `package main

import "fmt"

struct User(Id int, Name string)

type Repo interface {
    Find(id int) Option[User]
    Count() int
}

type Sink interface {
    Write(s string) (int, error)
}

type Logger interface {
    Log(prefix string, parts ...string)
}

type Box[T any] interface {
    Get() T
}

struct MemRepo(name string)
func (r MemRepo) Find(id int) Option[User] = Some(User(id, r.name))
`
	cases := []struct {
		name      string
		body      string
		contains  []string
		wantError string
	}{
		{
			name: "lambda parameter types come from the method",
			body: `func main() {
    val r = Repo(Find = (id) => Some(User(id, "Ada")), Count = () => 1)
    Println(r.Find(1), r.Count())
}`,
			contains: []string{
				"type _Repo_literal_main struct {",
				"_Count func() int",
				"_Find  func(int) std.Option[User]",
				"func (l _Repo_literal_main) Find(p0 int) std.Option[User] {\n\treturn l._Find(p0)\n}",
				"Repo(&_Repo_literal_main{_Count: func() int {",
				"_Find: func(id int) std.Option[User] {",
			},
		},
		{
			name: "a method value implements a method, in any argument order",
			body: `func main() {
    val mem = MemRepo("mem")
    val r = Repo(Count = () => 0, Find = mem.Find)
    Println(r.Find(1))
}`,
			contains: []string{"_Find: mem.Get().Find"},
		},
		{
			name: "the expected type gives a generic interface its type arguments",
			body: `func wrap[T any](v T) Box[T] = Box(Get = () => v)
func main() { Println(wrap(1).Get(), Box[string](Get = () => "s").Get()) }`,
			contains: []string{
				"type _Box_literal_main[T any] struct {",
				"func (l _Box_literal_main[T]) Get() T {",
				"Box[T](&_Box_literal_main[T]{_Get: func() T {",
				"Box[string](&_Box_literal_main[string]{",
			},
		},
		{
			name: "a method with a Go result list takes a lambda returning a Try",
			body: `func main() {
    val s = Sink(Write = (s) => Success(s.Size()))
    Println(s.Write("hello"))
}`,
			contains: []string{"_Write func(string) (int, error)", "func (l _Sink_literal_main) Write(p0 string) (int, error) {"},
		},
		{
			name: "a variadic method's function takes the slice",
			body: `func main() {
    val l = Logger(Log = (prefix, parts) => Println(prefix, parts.Size()))
    l.Log("n", "a", "b")
}`,
			contains: []string{"_Log func(string, []string)", "func (l _Logger_literal_main) Log(p0 string, p1 ...string) {\n\tl._Log(p0, p1)\n}"},
		},
		{
			name: "a Go interface",
			body: `func main() {
    val s = fmt.Stringer(String = () => "hi")
    Println(fmt.Sprint(s))
}`,
			contains: []string{"type _fmt_Stringer_literal_main struct {", "fmt.Stringer(&_fmt_Stringer_literal_main{_String: func() string {"},
		},
		{
			name: "a method left out",
			body: `func main() {
    val r = Repo(Find = (id) => None())
    Println(r)
}`,
			wantError: "interface literal of Repo leaves out method Count",
		},
		{
			name: "an argument naming no method",
			body: `func main() {
    val r = Repo(Find = (id) => None(), Count = () => 1, Size = () => 2)
    Println(r)
}`,
			wantError: "Repo has no method Size",
		},
		{
			name: "a positional argument",
			body: `func main() {
    val r = Repo((id) => None(), Count = () => 1)
    Println(r)
}`,
			wantError: "an argument of an interface literal of Repo names the method it implements",
		},
		{
			name: "a method given twice",
			body: `func main() {
    val r = Repo(Find = (id) => None(), Count = () => 1, Count = () => 2)
    Println(r)
}`,
			wantError: "method Count of Repo is given twice",
		},
		{
			name: "a generic interface without type arguments",
			body: `func main() {
    val b = Box(Get = () => 1)
    Println(b)
}`,
			wantError: "Box is generic, and the literal does not say its type arguments",
		},
		{
			name: "a generic method",
			body: `type Conv interface {
    Convert[U any](x U) U
}

func main() {
    val c = Conv(Convert = (x) => x)
    Println(c)
}`,
			wantError: "method Convert of Conv is generic, and a function cannot implement a generic method",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, decls+tc.body)
			if tc.wantError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), string(galaerr.CodeInvalidInterfaceLiteral))
				require.Contains(t, err.Error(), tc.wantError)
				return
			}
			require.NoError(t, err)
			for _, want := range tc.contains {
				require.Contains(t, out, want)
			}
		})
	}
}
