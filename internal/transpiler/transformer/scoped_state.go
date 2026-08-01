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
// A scoped field that is still set after Transform means some save/restore
// pair leaked. Historically these were written four different ways in four
// files — defer-restore, manual restore, per-branch re-set, and
// consume-and-clear — and the manual forms silently skipped their restore on
// early error returns. scopedStateResidue turns that class of mistake into a
// test failure instead of a mis-inferred type somewhere downstream.
//
// The one field here that is already leak-proof by construction is
// expectedArgTypes: it is a LIFO stack whose push returns an idempotent
// unwind function (see expected_arg_stack.go). It is checked anyway, because
// an unbalanced stack is the same bug wearing a different shape.

// scopedStateFields lists fields that must be zero once Transform returns.
// Keep in sync with scopedStateResidue below.
var scopedStateFields = []string{
	"currentScope",
	"currentFuncReturnType",
	"currentMatchSubjectType",
	"expectedIfExprType",
	"expectedLambdaParamTypes",
	"expectedLambdaRetType",
	"expectedArgTypes",
	"matchInStatementPos",
	"blockLastStmtIsValue",
	"pendingMatchStmtBlock",
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
//
// The checks are written out rather than derived by reflection because the
// zero test differs per field — expectedArgTypes is a struct wrapping a slice
// whose backing array is retained across pops, so it is empty at len 0 rather
// than at its zero value.
func (t *galaASTTransformer) scopedStateResidue() []string {
	var residue []string
	add := func(name string) { residue = append(residue, name) }

	if t.currentScope != nil {
		add("currentScope")
	}
	if t.currentFuncReturnType != nil {
		add("currentFuncReturnType")
	}
	if t.currentMatchSubjectType != nil {
		add("currentMatchSubjectType")
	}
	if t.expectedIfExprType != nil {
		add("expectedIfExprType")
	}
	if t.expectedLambdaParamTypes != nil {
		add("expectedLambdaParamTypes")
	}
	if t.expectedLambdaRetType != nil {
		add("expectedLambdaRetType")
	}
	if len(t.expectedArgTypes.stack) != 0 {
		add("expectedArgTypes")
	}
	if t.matchInStatementPos {
		add("matchInStatementPos")
	}
	if t.blockLastStmtIsValue {
		add("blockLastStmtIsValue")
	}
	if t.pendingMatchStmtBlock != nil {
		add("pendingMatchStmtBlock")
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
