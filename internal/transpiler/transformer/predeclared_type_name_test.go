package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestPredeclaredTypeName pins GALA-E0106: a type, or a type parameter, may
// not take the name of a Go predeclared type, in any declaration form. GALA
// reads such a name as Go's type in a type position, so the program failed
// later and elsewhere (`error does not implement error`, a Go build error).
// Values, fields and functions may still take those names (#1051).
func TestPredeclaredTypeName(t *testing.T) {
	rejected := []struct {
		name, src, want string
	}{
		{"block struct", "type error struct {\n    Msg string\n}\n\nfunc main() = Println(1)", "main.gala:3:5 type error is named like a Go predeclared type"},
		{"shorthand struct", "struct string(S int)\n\nfunc main() = Println(1)", "main.gala:3:7 type string is named like"},
		{"sealed type", "sealed type int {\n    case A()\n    case B()\n}\n\nfunc main() = Println(1)", "type int is named like"},
		{"sealed case", "sealed type Num {\n    case byte(V int)\n    case Other()\n}\n\nfunc main() = Println(1)", "main.gala:4:9 type byte is named like"},
		{"alias", "type any int\n\nfunc main() = Println(1)", "type any is named like"},
		{"interface", "type comparable interface {\n    Less() bool\n}\n\nfunc main() = Println(1)", "type comparable is named like"},
		{"opaque type", "opaque type rune int32\n\nfunc main() = Println(1)", "type rune is named like"},
		{"local type", "func main() {\n    type float64 struct {\n        X int\n    }\n    Println(1)\n}", "main.gala:4:9 type float64 is named like"},
		{"generic struct", "struct uint8[T any](V T)\n\nfunc main() = Println(1)", "type uint8 is named like"},
		{"type parameter of a function", "func show[int any](x int) int = x\n\nfunc main() = Println(show(1))", "main.gala:3:10 type int is named like"},
		{"type parameter of a struct", "struct Box[string any](V string)\n\nfunc main() = Println(1)", "type string is named like"},
		{"type parameter of a method", "struct Box(V int)\n\nfunc (b Box) Map[error any](f func(int) error) error = f(b.V)\n\nfunc main() = Println(1)", "type error is named like"},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			_, err := transpileBareVariant(t, "package main\n\n"+tc.src)
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodePredeclaredTypeName))
			require.Contains(t, err.Error(), tc.want)
			require.Contains(t, err.Error(), "rename the type")
		})
	}

	t.Run("accepts other names spelled like a predeclared type", func(t *testing.T) {
		_, err := transpileBareVariant(t, `package main

struct Error(string string, int int)

type Int int

val error = Error("a", 1)

func main() = Println(error.string)
`)
		require.NoError(t, err)
	})
}
