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
		{"too many type arguments on a concrete subject", `package main

struct User(Name string)
struct Order(Total int)

opaque type Id[T any] int64

func f(id Id[User]) string = id match {
    case Id[User, Order](n) => s"user $n"
    case _ => "?"
}

func main() {
    Println(f(Id[User](1)))
}`, "Id takes 1 type argument(s), got 2"},
		{"a named sub-pattern", `package main

opaque type UserID int64

func f(id UserID) string = id match {
    case UserID(id = n) => s"user $n"
    case _ => "?"
}

func main() {
    Println(f(UserID(1)))
}`, "the sub-pattern of UserID(...) is a single pattern for its underlying value"},
		{"a stable identifier of another opaque type", `package main

opaque type UserID int64
opaque type OrderID int64

val FirstOrder OrderID = 1

func f(id UserID) string = id match {
    case UserID(FirstOrder) => "first"
    case _ => "?"
}

func main() {
    Println(f(UserID(1)))
}`, "FirstOrder has type OrderID, but the value inside UserID(...) has type int64"},
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

// TestOpaqueTypePatternSubjectKinds covers subjects that are neither the
// opaque type nor an interface: a type parameter is asserted through `any`
// (its value may be of any type), and an alias of the opaque type is the
// opaque type.
func TestOpaqueTypePatternSubjectKinds(t *testing.T) {
	out := transpileOpaque(t, `package main

opaque type UserID int64

type Uid UserID

func show[T any](v T) string = v match {
    case UserID(n) => s"user $n"
    case _ => "?"
}

func viaAlias(u Uid) string = u match {
    case UserID(0) => "nobody"
    case UserID(n) => s"user $n"
    case _ => "?"
}

func main() {
    Println(show(UserID(1)), show(int64(1)), viaAlias(UserID(2)))
}`)
	assert.Regexp(t, `any\(\w+\)\.\(UserID\)`, out)
	assert.Contains(t, out, "int64(obj) == 0")
}
