package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpaqueTypePatternRejections covers the pattern shapes GALA reports
// itself rather than leaving to a Go error on the generated code.
func TestOpaqueTypePatternRejections(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	for _, tc := range []struct{ name, src, want string }{
		{"an interface the opaque type does not implement", `package main

opaque type UserID int64

func f(e error) string = e match {
    case UserID(n) => s"user $n"
    case _ => "?"
}

func main() {}`, "UserID does not implement error (missing Error)"},
		{"a stable identifier of the opaque type inside its own pattern", `package main

opaque type Level int

val Debug Level = 0

func f(l Level) string = l match {
    case Level(Debug) => "debug"
    case _ => "?"
}

func main() {
    Println(f(Debug))
}`, "Debug is already a Level: match it with `case Debug`"},
		{"type arguments on a non-generic opaque type", `package main

opaque type UserID int64

func f(id UserID) string = id match {
    case UserID[string](n) => s"user $n"
    case _ => "?"
}

func main() {
    Println(f(UserID(1)))
}`, "UserID takes 0 type argument(s), got 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.src, "opaque_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestOpaqueTypePatternOpenInstantiation covers a phantom pattern on a
// subject whose type argument is a type parameter: the instantiation it names
// is checked at run time.
func TestOpaqueTypePatternOpenInstantiation(t *testing.T) {
	out := transpileOpaque(t, `package main

struct User(Name string)
struct Order(Total int)

opaque type Id[T any] int64

func kind[T any](id Id[T]) string = id match {
    case Id[User](n) => s"user $n"
    case _ => "other"
}

func main() {
    Println(kind(Id[User](1)), kind(Id[Order](2)))
}`)
	assert.Contains(t, out, "any(obj).(Id[User])")
}

// TestOpaqueTypePatternLowercaseNameBinds covers a lowercase name inside the
// opaque-type pattern: it binds the underlying value even when a val of the
// opaque type shares it, as any lowercase pattern name does.
func TestOpaqueTypePatternLowercaseNameBinds(t *testing.T) {
	out := transpileOpaque(t, `package main

opaque type Level int

val debug Level = 0

func f(l Level) string = l match {
    case Level(debug) => s"level $debug"
    case _ => "?"
}

func main() {
    Println(f(debug))
}`)
	assert.Contains(t, out, "int(obj)")
}
