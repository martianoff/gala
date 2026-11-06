package analyzer

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/transpiler"
)

// declCollectors are the collections of a file's declarations (analyze's
// sections 1 to 2), by name: types, then sealed and opaque types, methods and
// functions.
var declCollectors = map[string]typeCollector{
	"native": func(a *galaAnalyzer, p *galaparser.Parsed, fd *fileDecls) error {
		if err := typeCollectors["native"](a, p, fd); err != nil {
			return err
		}
		nf, err := nativeFileOf(p.CleanNative())
		if err != nil {
			return err
		}
		return a.collectFileFuncsNative(nf, fd)
	},
}

// methodNamesOf is the sorted names of meta's methods.
func methodNamesOf(meta *transpiler.TypeMetadata) []string {
	return slices.Sorted(maps.Keys(meta.Methods))
}

// TestRecollectedDeclarationDropsStaleMethods: a type whose declaration
// changes between two collections of its file, as in an editor session or
// watch mode, gets the entry a first collection of the new
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
		{
			// Large moves to ID, which is collected first: Other's reset
			// must not drop the companion ID has just made for it.
			name:        "case_moves_to_an_earlier_type",
			first:       "package box\n\nsealed type ID {\n    case Small(N int)\n}\n\nsealed type Other {\n    case Large(N int)\n}\n",
			again:       "package box\n\nsealed type ID {\n    case Small(N int)\n    case Large(N int)\n}\n\nsealed type Other {\n    case Big()\n}\n",
			wantMethods: []string{"Describe", "isLarge", "isSmall"},
		},
		{
			name:        "cases_swap_types",
			first:       "package box\n\nsealed type ID {\n    case Small(N int)\n}\n\nsealed type Other {\n    case Large(N int)\n}\n",
			again:       "package box\n\nsealed type ID {\n    case Large(N int)\n}\n\nsealed type Other {\n    case Small(N int)\n}\n",
			wantMethods: []string{"Describe", "isLarge"},
		},
	}
	for _, c := range cases {
		for name, collect := range declCollectors {
			t.Run(c.name+"/"+name, func(t *testing.T) {
				got := recollectDeclaration(t, collect, c.first, c.again).Types["box.ID"]
				if names := methodNamesOf(got); !reflect.DeepEqual(names, c.wantMethods) {
					t.Errorf("methods = %v, want %v", names, c.wantMethods)
				}
			})
		}
	}
}

// recollectDeclaration collects first, then again, as the analyzed file
// box.gala with collect, and returns the result after failing tb unless it is
// the one a first collection of again gives: every entry and companion, so a
// companion of a case the file no longer declares is caught too.
//
// Between the collections a new Analyze call starts (the declarations seen
// are its own), and another file declares Describe on box.ID — unless ID was
// an interface, on which no file declares a method. The two exceptions to
// "a first collection of again" are that method, which survives, and the
// DefinedIn an interface does not record (keepInterfaceDefinedIn).
func recollectDeclaration(tb testing.TB, collect typeCollector, first, again string) *transpiler.RichAST {
	tb.Helper()
	other := recollectDescribe("other.gala")
	addsOther := false
	var firstDefinedIn map[string]string
	a := newCollectionAnalyzer(tb, galaparser.NewAntlrGalaParser(), nil)
	fd := collectTwice(tb, a, collect, first, again, func(fd *fileDecls) {
		firstDefinedIn = definedInOf(fd.richAST.Types)
		fd.decls = newPackageDecls(fd.filePath)
		if id := fd.richAST.Types["box.ID"]; id != nil && !id.IsInterface {
			id.Methods[other.Name] = other
			addsOther = true
		}
	})
	fresh := newCollectionAnalyzer(tb, galaparser.NewAntlrGalaParser(), nil)
	want := collectOnce(tb, fresh, collect, fd.filePath, again).richAST
	if addsOther {
		want.Types["box.ID"].Methods[other.Name] = other
	}
	keepInterfaceDefinedIn(want.Types, firstDefinedIn)
	requireSameTypes(tb, "analyzed file", fd.richAST, want, first, again)
	return fd.richAST
}

// recollectDescribe is the method Describe that the file definedIn declares
// on box.ID.
func recollectDescribe(definedIn string) *transpiler.MethodMetadata {
	return &transpiler.MethodMetadata{Name: "Describe", Package: "box", DefinedIn: definedIn}
}

// keepInterfaceDefinedIn applies the documented exception to want: an
// interface declaration does not record DefinedIn, so an entry that is an
// interface keeps the one its first collection recorded.
func keepInterfaceDefinedIn(want map[string]*transpiler.TypeMetadata, firstDefinedIn map[string]string) {
	for key, meta := range want {
		if meta.IsInterface {
			if d, ok := firstDefinedIn[key]; ok {
				meta.DefinedIn = d
			}
		}
	}
}

// definedInOf is the DefinedIn of each entry of types.
func definedInOf(types map[string]*transpiler.TypeMetadata) map[string]string {
	out := make(map[string]string, len(types))
	for key, meta := range types {
		out[key] = meta.DefinedIn
	}
	return out
}

// requireSameTypes fails tb when got's entries or companions differ from
// want's, at the first place they do.
func requireSameTypes(tb testing.TB, what string, got, want *transpiler.RichAST, first, again string) {
	tb.Helper()
	gotKeys, wantKeys := slices.Sorted(maps.Keys(got.Types)), slices.Sorted(maps.Keys(want.Types))
	diff := ""
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		diff = fmt.Sprintf("types %v, want %v", gotKeys, wantKeys)
	} else if d := firstDiff(reflect.ValueOf(got.Types), reflect.ValueOf(want.Types), "Types", map[visit]bool{}); d != "" {
		diff = d
	} else if d := firstDiff(reflect.ValueOf(got.CompanionObjects), reflect.ValueOf(want.CompanionObjects), "CompanionObjects", map[visit]bool{}); d != "" {
		diff = d
	}
	if diff != "" {
		tb.Fatalf("%s: re-collected entries differ from a first collection at %s\nfirst:\n%s\nagain:\n%s", what, diff, first, again)
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
		t.Run(c.name, func(t *testing.T) {
			got := recollectSibling(t, c.first, c.again).Types["box.ID"]
			if names := methodNamesOf(got); !reflect.DeepEqual(names, c.wantMethods) {
				t.Errorf("methods = %v, want %v", names, c.wantMethods)
			}
		})
	}
}

// The analyzed file and its sibling in recollectSibling. Neither is read or
// written: the sources are parsed from memory and the paths only compared,
// so they need no directory.
var (
	recollectMainFile = filepath.Join(os.TempDir(), "gala-recollect", "main.gala")
	recollectSibFile  = filepath.Join(os.TempDir(), "gala-recollect", "id.gala")
)

// recollectSibling extracts first, then again, as a sibling of the analyzed
// file, which declares Describe on box.ID, and returns the result after
// failing tb unless it is the one a first extraction of again gives, every
// entry and companion: but for the DefinedIn an interface does not record
// (keepInterfaceDefinedIn).
func recollectSibling(tb testing.TB, first, again string) *transpiler.RichAST {
	tb.Helper()
	extract := func(a *galaAnalyzer, richAST *transpiler.RichAST, src string) {
		tb.Helper()
		parsed := parseClean(tb, src)
		nf, err := nativeFileOf(parsed.CleanNative())
		if err != nil {
			tb.Fatalf("the sibling has no native tree: %v", err)
		}
		sib := pkgFile{recollectSibFile, &parsedFileEntry{parsed: parsed, nf: nf, pkgName: "box"}}
		if err := a.extractSiblingFullMetadata(sib, fileQualifiers{}, "box", richAST, newPackageDecls(recollectMainFile)); err != nil {
			tb.Fatalf("sibling extraction: %v\nsource:\n%s", err, src)
		}
	}
	// addDeclared records the analyzed file's method as its collection
	// does, before the siblings' types: on the entry, or on a placeholder
	// for it.
	addDeclared := func(richAST *transpiler.RichAST) {
		if meta := richAST.Types["box.ID"]; meta != nil {
			meta.Methods["Describe"] = recollectDescribe(recollectMainFile)
			return
		}
		richAST.Types["box.ID"] = &transpiler.TypeMetadata{
			Name: "ID", Package: "box",
			Methods: map[string]*transpiler.MethodMetadata{"Describe": recollectDescribe(recollectMainFile)},
			Fields:  make(map[string]transpiler.Type),
		}
	}
	a := newCollectionAnalyzer(tb, galaparser.NewAntlrGalaParser(), nil)
	richAST := newCollectedFile(a, "box", recollectMainFile, nil).richAST
	extract(a, richAST, first)
	firstDefinedIn := definedInOf(richAST.Types)
	addDeclared(richAST)
	extract(a, richAST, again)

	fresh := newCollectionAnalyzer(tb, galaparser.NewAntlrGalaParser(), nil)
	want := newCollectedFile(fresh, "box", recollectMainFile, nil).richAST
	addDeclared(want)
	extract(fresh, want, again)
	keepInterfaceDefinedIn(want.Types, firstDefinedIn)
	requireSameTypes(tb, "sibling", richAST, want, first, again)
	return richAST
}
