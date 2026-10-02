package analyzer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The Go-export scan backs type existence for a package whose Go type info is
// not loaded, so it has to see every form an exported type is declared in.
func TestGoExportedTypeScan(t *testing.T) {
	src := "package dep\n\n" +
		"type Plain struct{}\n" +
		"type Alias = Plain\n" +
		"type Box[T any] struct{ v T }\n" +
		"type unexported int\n" +
		"type (\n" +
		"\tGrouped int\n" +
		"\tPair[A, B any] struct {\n" +
		"\t\tField A\n" +
		"\t}\n" +
		"\thidden int\n" +
		")\n"
	assert.Equal(t, []string{"Plain", "Alias", "Box", "Grouped", "Pair"}, exportedGoTypeNames(src))
}
