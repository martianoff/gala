package transformer

import (
	"go/ast"
	"reflect"
	"testing"

	"martianoff/gala/internal/transpiler"

	"github.com/stretchr/testify/require"
)

// TestEveryTransformerFieldIsClassified is the anti-erosion guard.
//
// scopedStateResidue can only detect a leak in a field it knows about. A new
// field added to galaASTTransformer without a classification would be silently
// exempt, and the leak detector would quietly cover less than it appears to.
// This test fails the moment that happens, forcing the author to decide
// whether the new field is scoped (restore it, list it in scopedStateFields,
// and add a residue check) or accumulated (list it in accumulatedStateFields).
func TestEveryTransformerFieldIsClassified(t *testing.T) {
	classified := make(map[string]string, len(scopedStateFields)+len(accumulatedStateFields))
	for _, name := range scopedStateFields {
		classified[name] = "scoped"
	}
	for _, name := range accumulatedStateFields {
		if kind, dup := classified[name]; dup {
			t.Fatalf("field %q is classified twice (%s and accumulated)", name, kind)
		}
		classified[name] = "accumulated"
	}

	structType := reflect.TypeOf(galaASTTransformer{})
	actual := make(map[string]bool, structType.NumField())
	var unclassified []string
	for i := 0; i < structType.NumField(); i++ {
		name := structType.Field(i).Name
		actual[name] = true
		if _, ok := classified[name]; !ok {
			unclassified = append(unclassified, name)
		}
	}

	require.Empty(t, unclassified,
		"new galaASTTransformer field(s) %v are neither scoped nor accumulated. "+
			"Scoped state must be restored on every exit path (including error returns) "+
			"and listed in scopedStateFields with a check in scopedStateResidue; "+
			"results and configuration go in accumulatedStateFields. See scoped_state.go.",
		unclassified)

	// The reverse direction: a classified name that no longer exists means a
	// field was renamed or removed and the classification was not updated, so
	// the residue check silently stopped covering it.
	var stale []string
	for name := range classified {
		if !actual[name] {
			stale = append(stale, name)
		}
	}
	require.Empty(t, stale, "classified field(s) %v no longer exist on galaASTTransformer", stale)
}

// setScopedFieldNonZero puts a recognisably non-zero value into each scoped
// field. Written as an explicit table rather than by reflection so that
// renaming a field breaks the build here instead of silently skipping it.
var setScopedFieldNonZero = map[string]func(*galaASTTransformer){
	"currentScope":             func(t *galaASTTransformer) { t.pushScope() },
	"currentFuncReturnType":    func(t *galaASTTransformer) { t.currentFuncReturnType = transpiler.BasicType{Name: "int"} },
	"currentMatchSubjectType":  func(t *galaASTTransformer) { t.currentMatchSubjectType = transpiler.BasicType{Name: "int"} },
	"expectedIfExprType":       func(t *galaASTTransformer) { t.expectedIfExprType = ast.NewIdent("int") },
	"expectedLambdaParamTypes": func(t *galaASTTransformer) { t.expectedLambdaParamTypes = []transpiler.Type{transpiler.BasicType{Name: "int"}} },
	"expectedLambdaRetType":    func(t *galaASTTransformer) { t.expectedLambdaRetType = ast.NewIdent("int") },
	"expectedArgTypes":         func(t *galaASTTransformer) { t.expectedArgTypes.push(transpiler.BasicType{Name: "int"}) },
	"matchInStatementPos":      func(t *galaASTTransformer) { t.matchInStatementPos = true },
	"blockLastStmtIsValue":     func(t *galaASTTransformer) { t.blockLastStmtIsValue = true },
	"pendingMatchStmtBlock":    func(t *galaASTTransformer) { t.pendingMatchStmtBlock = &ast.BlockStmt{} },
}

// TestScopedStateResidueCoversEveryScopedField asserts the hand-written
// residue checks stay in step with scopedStateFields: setting a scoped field
// must make it show up in the residue. A field listed without a matching check
// would otherwise be reported clean no matter what it held.
func TestScopedStateResidueCoversEveryScopedField(t *testing.T) {
	require.Len(t, setScopedFieldNonZero, len(scopedStateFields),
		"every scoped field needs an entry in setScopedFieldNonZero")

	for _, name := range scopedStateFields {
		t.Run(name, func(t *testing.T) {
			set, ok := setScopedFieldNonZero[name]
			require.True(t, ok, "no setter for scoped field %q", name)

			tr, ok := NewGalaASTTransformer().(*galaASTTransformer)
			require.True(t, ok)
			require.Empty(t, tr.scopedStateResidue(), "a fresh transformer must have no residue")

			set(tr)
			require.Contains(t, tr.scopedStateResidue(), name,
				"scopedStateFields lists %q but scopedStateResidue has no check for it", name)
		})
	}
}
