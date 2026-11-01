package analyzer

import (
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// declCollectors are the two twins of the collection of a file's
// declarations (analyze's sections 1 to 2), by tree: types, then sealed and
// opaque types, methods and functions.
var declCollectors = map[string]typeCollector{
	"antlr": func(a *galaAnalyzer, p *galaparser.Parsed, fd *fileDecls) error {
		if err := typeCollectors["antlr"](a, p, fd); err != nil {
			return err
		}
		return a.collectFileFuncs(p.Tree.(*grammar.SourceFileContext), fd)
	},
	"native": func(a *galaAnalyzer, p *galaparser.Parsed, fd *fileDecls) error {
		if err := typeCollectors["native"](a, p, fd); err != nil {
			return err
		}
		return a.collectFileFuncsNative(nativeFileOf(p.CleanNative()), fd)
	},
}

// methodNamesOf is the sorted names of meta's methods.
func methodNamesOf(meta *transpiler.TypeMetadata) []string {
	return slices.Sorted(maps.Keys(meta.Methods))
}

// TestRecollectedDeclarationDropsStaleMethods: a type whose declaration
// changes between two collections of its file, as in an editor session or
// watch mode, gets from both twins the entry a first collection of the new
// file gives. No method of its old form survives: an interface's method
// specs, a sealed type's case predicates, a method the file has since
// deleted. A method another file declares on the type survives.
func TestRecollectedDeclarationDropsStaleMethods(t *testing.T) {
	parseWithGalop(t)
	cases := []struct {
		name, first, again string
		// wantMethods is the methods the re-collected entry has, the
		// other file's included.
		wantMethods []string
	}{
		{
			name:        "interface_to_opaque",
			first:       "package box\n\ntype ID interface {\n    Raw() int64\n}\n",
			again:       "package box\n\nopaque type ID int64\n",
			wantMethods: nil,
		},
		{
			name:        "sealed_to_struct",
			first:       "package box\n\nsealed type ID {\n    case Small(N int)\n    case Large(N int)\n}\n",
			again:       "package box\n\nstruct ID(N int)\n",
			wantMethods: []string{"Describe"},
		},
		{
			name:        "sealed_loses_a_case",
			first:       "package box\n\nsealed type ID {\n    case Small(N int)\n    case Large(N int)\n}\n",
			again:       "package box\n\nsealed type ID {\n    case Small(N int)\n}\n",
			wantMethods: []string{"Describe", "isSmall"},
		},
		{
			name:        "struct_method_deleted",
			first:       "package box\n\nstruct ID(N int)\n\nfunc (i ID) Old() int {\n    return 1\n}\n\nfunc (i ID) Keep() int {\n    return 2\n}\n",
			again:       "package box\n\nstruct ID(N int)\n\nfunc (i ID) Keep() int {\n    return 2\n}\n",
			wantMethods: []string{"Describe", "Keep"},
		},
		{
			name:        "opaque_method_deleted",
			first:       "package box\n\nopaque type ID int64\n\nfunc (i ID) Old() int {\n    return 1\n}\n",
			again:       "package box\n\nopaque type ID int64\n",
			wantMethods: []string{"Describe"},
		},
		{
			name:        "struct_to_sealed",
			first:       "package box\n\nstruct ID(N int)\n\nfunc (i ID) Old() int {\n    return 1\n}\n",
			again:       "package box\n\nsealed type ID {\n    case Small(N int)\n}\n",
			wantMethods: []string{"Describe", "isSmall"},
		},
		{
			// The old case's companion goes: no Apply or Unapply is left on
			// the opaque type now named like it.
			name:        "case_name_reused_by_opaque",
			first:       "package box\n\nsealed type ID {\n    case Small(N int)\n}\n",
			again:       "package box\n\nsealed type ID {\n    case Big(N int)\n}\n\nopaque type Small int64\n",
			wantMethods: []string{"Describe", "isBig"},
		},
	}
	// A method another file declares on ID, recorded between the collections.
	declared := &transpiler.MethodMetadata{Name: "Describe", Package: "box", DefinedIn: "other.gala"}
	for _, c := range cases {
		for name, collect := range declCollectors {
			t.Run(c.name+"/"+name, func(t *testing.T) {
				fromInterface := c.name == "interface_to_opaque"
				a := newCollectionAnalyzer(t, galaparser.NewAntlrGalaParser(), nil)
				fd := collectTwice(t, a, collect, c.first, c.again, func(fd *fileDecls) {
					// A new Analyze call: the declarations seen are its own.
					fd.decls = newPackageDecls(fd.filePath)
					if !fromInterface {
						fd.richAST.Types["box.ID"].Methods[declared.Name] = declared
					}
				})
				got := fd.richAST.Types["box.ID"]
				if names := methodNamesOf(got); !reflect.DeepEqual(names, c.wantMethods) {
					t.Errorf("methods = %v, want %v", names, c.wantMethods)
				}
				// Every entry, so a companion of a case the file no longer
				// declares is caught too.
				fresh := newCollectionAnalyzer(t, galaparser.NewAntlrGalaParser(), nil)
				want := collectOnce(t, fresh, collect, fd.filePath, c.again).richAST.Types
				if !fromInterface {
					want["box.ID"].Methods[declared.Name] = declared
				}
				if !reflect.DeepEqual(fd.richAST.Types, want) {
					t.Errorf("re-collected entries differ from a first collection at %s", firstDiff(reflect.ValueOf(fd.richAST.Types), reflect.ValueOf(want), "", map[visit]bool{}))
				}
			})
		}
	}
}

// TestRecollectedSiblingDeclaration: a sibling's type that an earlier
// collection recorded, with its fields and the sibling's own methods, is
// collected again from the sibling as it now reads, rather than kept: the
// entry a fresh extraction gives, but for the method the analyzed file
// declares on the type, which is kept.
func TestRecollectedSiblingDeclaration(t *testing.T) {
	cases := []struct {
		name, first, again string
		wantMethods        []string
	}{
		{
			name:        "struct_to_interface",
			first:       "package box\n\nstruct ID(N int)\n\nfunc (i ID) Old() int {\n    return 1\n}\n",
			again:       "package box\n\ntype ID interface {\n    Raw() int64\n}\n",
			wantMethods: []string{"Describe", "Raw"},
		},
		{
			// The analyzed file's method stays, the old method specs go.
			name:        "interface_to_struct",
			first:       "package box\n\ntype ID interface {\n    Raw() int64\n}\n",
			again:       "package box\n\nstruct ID(N int)\n",
			wantMethods: []string{"Describe"},
		},
		{
			name:        "method_deleted",
			first:       "package box\n\ntype ID struct {\n    N int\n}\n\nfunc (i ID) Old() int {\n    return 1\n}\n\nfunc (i ID) Keep() int {\n    return 2\n}\n",
			again:       "package box\n\ntype ID struct {\n    N int\n}\n\nfunc (i ID) Keep() int {\n    return 2\n}\n",
			wantMethods: []string{"Describe", "Keep"},
		},
		{
			name:        "sealed_to_struct",
			first:       "package box\n\nsealed type ID {\n    case Small(N int)\n    case Large(N int)\n}\n",
			again:       "package box\n\nstruct ID(N int)\n",
			wantMethods: []string{"Describe"},
		},
		{
			name:        "sealed_loses_a_case",
			first:       "package box\n\nsealed type ID {\n    case Small(N int)\n    case Large(N int)\n}\n",
			again:       "package box\n\nsealed type ID {\n    case Small(N int)\n}\n",
			wantMethods: []string{"Describe", "isSmall"},
		},
		{
			name:        "opaque_changes_underlying",
			first:       "package box\n\nopaque type ID int64\n\nfunc (i ID) Old() int {\n    return 1\n}\n",
			again:       "package box\n\nopaque type ID string\n",
			wantMethods: []string{"Describe"},
		},
	}
	for _, c := range cases {
		for _, onNative := range []bool{false, true} {
			name := c.name + "/antlr"
			if onNative {
				name = c.name + "/native"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				mainFile := filepath.Join(dir, "main.gala")
				sibFile := filepath.Join(dir, "id.gala")
				// The method the analyzed file declares on ID, which its
				// collection records before the siblings' types.
				declared := func() *transpiler.MethodMetadata {
					return &transpiler.MethodMetadata{Name: "Describe", Package: "box", DefinedIn: mainFile}
				}
				extract := func(a *galaAnalyzer, richAST *transpiler.RichAST, src string) {
					t.Helper()
					parsed := parseClean(t, src)
					sf := parsed.Tree.(*grammar.SourceFileContext)
					var nf nativeFile
					if onNative {
						if nf = nativeFileOf(parsed.CleanNative()); !nf.ok() {
							t.Fatal("the sibling has no native tree")
						}
					}
					if err := a.extractSiblingFullMetadata(sf, nf, parsed.Docs, fileQualifiers{}, "box", richAST, sibFile, newPackageDecls(mainFile)); err != nil {
						t.Fatalf("sibling extraction: %v", err)
					}
				}
				// addDeclared records the analyzed file's method as its
				// collection does: on the entry, or on a placeholder for it.
				addDeclared := func(richAST *transpiler.RichAST) {
					if meta := richAST.Types["box.ID"]; meta != nil {
						meta.Methods["Describe"] = declared()
						return
					}
					richAST.Types["box.ID"] = &transpiler.TypeMetadata{
						Name: "ID", Package: "box",
						Methods: map[string]*transpiler.MethodMetadata{"Describe": declared()},
						Fields:  make(map[string]transpiler.Type),
					}
				}

				a := newCollectionAnalyzer(t, galaparser.NewAntlrGalaParser(), nil)
				richAST := newCollectedFile(a, "box", mainFile, nil).richAST
				extract(a, richAST, c.first)
				firstDefinedIn := richAST.Types["box.ID"].DefinedIn
				addDeclared(richAST)
				extract(a, richAST, c.again)
				got := richAST.Types["box.ID"]
				if names := methodNamesOf(got); !reflect.DeepEqual(names, c.wantMethods) {
					t.Errorf("methods = %v, want %v", names, c.wantMethods)
				}

				fresh := newCollectionAnalyzer(t, galaparser.NewAntlrGalaParser(), nil)
				freshAST := newCollectedFile(fresh, "box", mainFile, nil).richAST
				addDeclared(freshAST)
				extract(fresh, freshAST, c.again)
				if gotKeys, wantKeys := slices.Sorted(maps.Keys(richAST.Types)), slices.Sorted(maps.Keys(freshAST.Types)); !reflect.DeepEqual(gotKeys, wantKeys) {
					t.Errorf("types = %v, want %v", gotKeys, wantKeys)
				}
				want := freshAST.Types["box.ID"]
				// An interface declaration does not record DefinedIn, so the
				// one recorded before stays.
				if want.IsInterface {
					want.DefinedIn = firstDefinedIn
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("re-collected entry differs from a first collection at %s", firstDiff(reflect.ValueOf(got), reflect.ValueOf(want), "", map[visit]bool{}))
				}
			})
		}
	}
}
