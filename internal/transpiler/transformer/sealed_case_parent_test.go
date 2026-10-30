package transformer

import (
	"testing"

	"martianoff/gala/internal/transpiler"
)

// keylessSealedFixture is a transformer of package pkg, a package with no
// qualified key, holding its sealed type Cmd with case MsgCmd, whose
// companion is filed under its bare name.
func keylessSealedFixture(pkg string) *galaASTTransformer {
	tr := NewGalaASTTransformer().(*galaASTTransformer)
	tr.packageName = pkg
	tr.importManager = NewImportManager()
	tr.richAST = &transpiler.RichAST{
		PackageName: pkg,
		Types: map[string]*transpiler.TypeMetadata{
			"Cmd": {Name: "Cmd", Package: pkg, IsSealed: true, SealedVariants: []transpiler.SealedVariant{{Name: "MsgCmd"}}},
		},
	}
	tr.companionObjects = map[string]*transpiler.CompanionObjectMetadata{
		"MsgCmd": {Name: "MsgCmd", Package: pkg, TargetType: "Cmd"},
	}
	return tr
}

// TestSealedCaseParentOfKeylessPackage: a case type of a main or test
// package may name its package (`main.MsgCmd`), while its companion is filed
// under its bare name. The case's parent is still found, as on master.
func TestSealedCaseParentOfKeylessPackage(t *testing.T) {
	for _, pkg := range []string{"main", "test"} {
		tr := keylessSealedFixture(pkg)
		for _, caseType := range []transpiler.Type{
			transpiler.NamedType{Package: pkg, Name: "MsgCmd"},
			transpiler.NamedType{Name: "MsgCmd"},
		} {
			if parent := tr.sealedCaseParent(caseType); parent == nil || parent.BaseName() != "Cmd" {
				t.Errorf("%s: the parent of %s is %v, want Cmd", pkg, caseType, parent)
			}
		}
	}
}

// TestSealedCaseParentOfNamedImportCase: a bare case type of a package the
// file imports by name (`dep.MsgCmd` met as `MsgCmd`) resolves by no
// precedence, so it takes the one package's case of that name, as master's
// suffix scan did; when two packages have one, it takes neither rather than
// guess.
func TestSealedCaseParentOfNamedImportCase(t *testing.T) {
	fixture := func(pkgs ...string) *galaASTTransformer {
		tr := NewGalaASTTransformer().(*galaASTTransformer)
		tr.packageName = "main"
		tr.importManager = NewImportManager()
		tr.richAST = &transpiler.RichAST{PackageName: "main", Types: map[string]*transpiler.TypeMetadata{}}
		tr.companionObjects = map[string]*transpiler.CompanionObjectMetadata{}
		for _, pkg := range pkgs {
			tr.richAST.Types[pkg+".Cmd"] = &transpiler.TypeMetadata{Name: "Cmd", Package: pkg, IsSealed: true, SealedVariants: []transpiler.SealedVariant{{Name: "MsgCmd"}}}
			tr.companionObjects[pkg+".MsgCmd"] = &transpiler.CompanionObjectMetadata{Name: "MsgCmd", Package: pkg, TargetType: "Cmd"}
		}
		return tr
	}
	if parent := fixture("dep").sealedCaseParent(transpiler.NamedType{Name: "MsgCmd"}); parent == nil || parent.BaseName() != "dep.Cmd" {
		t.Errorf("the parent of MsgCmd is %v, want dep.Cmd", parent)
	}
	for run := 0; run < 50; run++ {
		if parent := fixture("dep", "other").sealedCaseParent(transpiler.NamedType{Name: "MsgCmd"}); parent != nil {
			t.Fatalf("run %d: MsgCmd of two packages took %v for its parent", run, parent)
		}
	}
}

// TestSealedCaseParentBorrowsNoCompanion: a qualified case type of a named
// package whose companion is not filed has no parent here, even where a
// keyless package has a bare companion of the case's name. Master looked the
// case up by its bare name and took that companion's parent.
func TestSealedCaseParentBorrowsNoCompanion(t *testing.T) {
	for _, pkg := range []string{"main", "test"} {
		if parent := keylessSealedFixture(pkg).sealedCaseParent(transpiler.NamedType{Package: "other", Name: "MsgCmd"}); parent != nil {
			t.Errorf("%s: other.MsgCmd, which has no companion, took %v for its parent", pkg, parent)
		}
	}
}
