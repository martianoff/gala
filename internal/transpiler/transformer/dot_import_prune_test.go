package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A dot import the generated file does not use must be dropped — Go rejects
// it ("imported and not used") — and one it does use must stay. The rule is
// the same for every package: std, another GALA package, and a Go package.
func TestDotImportPruning(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		used bool
	}{
		// The generated `fmt.Println` selects a name std also exports; a
		// selected name is not a reference to the dot import.
		{"std unused", "martianoff/gala/std", `
func main() {
    Println(1)
}
`, false},
		{"std used by a type", "martianoff/gala/std", `
func get(o Option[int]) int = o.GetOrElse(2)
`, true},
		{"std used by a constructor", "martianoff/gala/std", `
func main() {
    Println(Some(1).GetOrElse(2))
}
`, true},
		{"std used by a method of a std type", "martianoff/gala/std", `
func main() {
    Println(Some(1).Map((x) => x + 1).GetOrElse(0))
}
`, true},
		{"std used only by generated code", "martianoff/gala/std", `
type Point struct {
    X int
}

func main() {
    val p = Point(X = 1)
    Println(p.X)
}
`, true},
		{"GALA package unused", "martianoff/gala/collection_immutable", `
func main() {
    Println(1)
}
`, false},
		{"GALA package used by a function", "martianoff/gala/collection_immutable", `
func main() {
    Println(ArrayOf(1, 2).Size())
}
`, true},
		{"GALA package used by a type", "martianoff/gala/collection_immutable", `
func size(a Array[int]) int = a.Size()
`, true},
		{"Go package unused", "strings", `
func main() {
    Println(1)
}
`, false},
		{"Go package used by a function", "strings", `
func main() {
    Println(ToUpper("a"))
}
`, true},
		{"Go package used by a type", "strings", `
func size(r Reader) int64 = r.Size()
`, true},
	}

	trans := newBindTranspiler()
	imp := newOracleImporter()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := "package main\n\nimport . \"" + tt.path + "\"\n" + tt.body
			got, err := trans.Transpile(input, "")
			require.NoError(t, err)

			dotImport := `. "` + tt.path + `"`
			if tt.used {
				assert.Contains(t, got, dotImport, "a used dot import must be kept")
			} else {
				assert.NotContains(t, got, dotImport, "an unused dot import must be dropped")
			}

			// The generated file must be valid Go, with no unused import:
			// soft errors count here.
			res := imp.TypeCheck("out.go", got)
			require.Empty(t, res.Skipped, "the generated file must be type-checked")
			assert.Empty(t, res.Errors, "generated Go:\n%s", got)
			assert.Zero(t, res.Soft, "generated Go has an unused import or variable:\n%s", got)
		})
	}
}
