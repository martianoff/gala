package transformer

import (
	"testing"

	"martianoff/gala/internal/transpiler"
)

// The LSP's var-type channel answers two questions: what type a name has, and
// whether the name is a local at all. Dropping entries whose type did not
// resolve answered the second question "no" for exactly those bindings, so a
// local shadowing a documented package-level val was rendered as that val.
func TestUnresolvedLocalIsStillRecordedAsABinding(t *testing.T) {
	tr := NewGalaASTTransformer().(*galaASTTransformer)
	tr.lspVarTypes = map[string]transpiler.Type{}
	tr.lspCurrentFunc = "Run"

	tr.recordLSPVarType("label", transpiler.NilType{})
	typ, ok := tr.lspVarTypes["Run.label"]
	if !ok {
		t.Fatal("a local whose type did not resolve was not recorded as a binding")
	}
	// It must still read as "no type": every hint consumer skips the empty string.
	if typ.String() != "" {
		t.Errorf("unresolved binding rendered as %q, want the empty string", typ.String())
	}

	// A resolved type replaces it, and is never clobbered by a later unresolved
	// pass over the same name.
	tr.recordLSPVarType("label", transpiler.BasicType{Name: "int"})
	tr.recordLSPVarType("label", transpiler.NilType{})
	if got := tr.lspVarTypes["Run.label"].String(); got != "int" {
		t.Errorf("resolved type was lost: got %q, want int", got)
	}
}

const lspCollectionSource = `package main

func run() {
	val count = 1
	val label = "ok"
	val identity func(int) int = (value) => value
	Println(count, label, identity(1))
}
`

func lspCollectionRichAST(tb testing.TB) *transpiler.RichAST {
	tb.Helper()
	tree, _, err := transpiler.NewAntlrGalaParser().Parse(lspCollectionSource)
	if err != nil {
		tb.Fatal(err)
	}
	return &transpiler.RichAST{
		Tree:             tree,
		PackageName:      "main",
		Types:            make(map[string]*transpiler.TypeMetadata),
		Functions:        make(map[string]*transpiler.FunctionMetadata),
		Packages:         make(map[string]string),
		ImportAliases:    make(map[string]string),
		CompanionObjects: make(map[string]*transpiler.CompanionObjectMetadata),
		GoExports:        make(map[string][]string),
		TypeAliases:      make(map[string]transpiler.Type),
		PackageVals:      make(map[string]*transpiler.PackageValMetadata),
		FilePath:         "lsp_collection.gala",
		SourceContent:    lspCollectionSource,
	}
}

func TestTransformCollectsLSPMetadataOnlyForLSP(t *testing.T) {
	richAST := lspCollectionRichAST(t)
	tr := NewGalaASTTransformer().(*galaASTTransformer)

	if _, _, err := tr.Transform(richAST); err != nil {
		t.Fatal(err)
	}
	if tr.lspVarTypes != nil || tr.lspLambdaParamHints != nil {
		t.Fatal("normal transform retained LSP metadata")
	}

	result, err := tr.TransformForLSP(richAST)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.VarTypes["run.count"]; !ok {
		t.Fatalf("LSP variable metadata is missing run.count: %v", result.VarTypes)
	}
	if len(result.LambdaParamHints) != 1 {
		t.Fatalf("LSP lambda hints = %d, want 1", len(result.LambdaParamHints))
	}

	if _, _, err := tr.Transform(richAST); err != nil {
		t.Fatal(err)
	}
	if tr.lspVarTypes != nil || tr.lspLambdaParamHints != nil {
		t.Fatal("normal transform retained metadata after an LSP transform")
	}
}

func BenchmarkTransformNormalMetadata(b *testing.B) {
	richAST := lspCollectionRichAST(b)
	tr := NewGalaASTTransformer()
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := tr.Transform(richAST); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTransformLSPMetadata(b *testing.B) {
	richAST := lspCollectionRichAST(b)
	tr := NewGalaASTTransformer()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := tr.TransformForLSP(richAST); err != nil {
			b.Fatal(err)
		}
	}
}
