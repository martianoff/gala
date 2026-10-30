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
