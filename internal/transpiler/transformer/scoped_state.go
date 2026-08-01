package transformer

import (
	"martianoff/gala/internal/transpiler"
)

// Scoped state vs. accumulated state
//
// The transformer carries two kinds of field. Telling them apart is what
// makes leak detection possible, so every field is classified below and a
// test fails the build if a new field is added without a classification.
//
//   - Scoped state is an expectation or a mode that applies to a bounded
//     region of the traversal: the expected type of the lambda about to be
//     lowered, the subject type of the enclosing match, whether the current
//     match sits in statement position. Whoever sets it owns restoring it.
//     Once Transform returns, every scoped field must be back at its zero
//     value — on the success path and the error path alike.
//
//   - Accumulated state is a result or a configuration: collected imports,
//     discovered type metadata, the expression-type cache, LSP hints. It is
//     expected to be non-zero when Transform returns; that is the point of it.
//
// A scoped field still set after Transform means some save/restore pair
// leaked. These are written several ways across the package — defer-restore,
// manual restore, per-branch re-set, consume-and-clear — and the manual forms
// skip their restore on early error returns. scopedStateResidue turns that
// into a test failure rather than a mis-inferred type somewhere downstream.
//
// expectedArgTypes is already leak-proof by construction: a LIFO stack whose
// push returns an idempotent unwind function (see expected_arg_stack.go).
// That is the pattern the rest should converge on. It is checked here anyway,
// since an unbalanced stack is the same bug in a different shape.

// scopedStateChecks pairs each scoped field with the test for "still set".
//
// One table rather than a list plus a parallel if-chain, so the names and the
// checks cannot drift apart. The tests are written out rather than derived by
// reflection because the zero test differs per field: expectedArgTypes is a
// struct wrapping a slice whose backing array is retained across pops, so it
// is empty at len 0 rather than at its zero value.
func (t *galaASTTransformer) scopedStateChecks() []struct {
	name  string
	isSet bool
} {
	return []struct {
		name  string
		isSet bool
	}{
		{"currentScope", t.currentScope != nil},
		{"currentFuncReturnType", t.currentFuncReturnType != nil},
		{"currentMatchSubjectType", t.currentMatchSubjectType != nil},
		{"expectedIfExprType", t.expectedIfExprType != nil},
		{"expectedLambdaParamTypes", t.expectedLambdaParamTypes != nil},
		{"expectedLambdaRetType", t.expectedLambdaRetType != nil},
		{"expectedArgTypes", len(t.expectedArgTypes.stack) != 0},
		{"matchInStatementPos", t.matchInStatementPos},
		{"blockLastStmtIsValue", t.blockLastStmtIsValue},
		{"pendingMatchStmtBlock", t.pendingMatchStmtBlock != nil},
	}
}

// scopedStateFieldNames lists the fields that must be zero once Transform
// returns, derived from the same table that checks them.
func scopedStateFieldNames() []string {
	var zero galaASTTransformer
	checks := zero.scopedStateChecks()
	names := make([]string, 0, len(checks))
	for _, c := range checks {
		names = append(names, c.name)
	}
	return names
}

// accumulatedStateFields lists fields that are results or configuration and
// are therefore exempt from the residue check.
var accumulatedStateFields = []string{
	"packageName",
	"immutFields",
	"structImmutFields",
	"needsStdImport",
	"needsFmtImport",
	"needsUtf8Import",
	"needsEmbedImport",
	"activeTypeParams",
	"structFields",
	"structFieldTypes",
	"genericMethods",
	"functions",
	"galaPkgPaths",
	"typeMetas",
	"companionObjects",
	"importManager",
	"tempVarCount",
	"inferer",
	"typeAliases",
	"goTypeInfo",
	"filePath",
	"sourceLines",
	"richAST",
	"traceTypeResolution",
	"typeTraces",
	"exprTypeCache",
	"warnTypeInference",
	"inferenceWarnings",
	"unresolvedTypes",
	"unresolvedSeen",
	"diagPackageNames",
	"structMetas",
	"instanceInterfaceNames",
	"synthesizedReturns",
	"lspVarTypes",
	"lspCurrentFunc",
	"lspLambdaParamHints",
	"lastLine",
	"lastCol",
}

// scopedStateResidue reports the scoped fields that are still set. It is the
// leak detector: an empty result means every save/restore pair in the
// traversal balanced.
func (t *galaASTTransformer) scopedStateResidue() []string {
	var residue []string
	for _, c := range t.scopedStateChecks() {
		if c.isSet {
			residue = append(residue, c.name)
		}
	}
	return residue
}

// ScopedStateResidue reports scoped transformer state left behind by a
// completed transform, by field name. An empty slice means no leak.
//
// It is exported so the full-pipeline tests can assert the invariant: those
// live in the external transformer_test package, because the analyzer imports
// the transformer and an in-package test could not link against it.
//
// Returns nil for a transformer this package did not construct.
func ScopedStateResidue(a transpiler.ASTTransformer) []string {
	t, ok := a.(*galaASTTransformer)
	if !ok {
		return nil
	}
	return t.scopedStateResidue()
}
