package transformer_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNoTypeErasureInGeneratedGo is the hard backstop behind the
// unresolved-type inventory.
//
// The inventory is a ratchet: it counts where inference gives up, and giving up
// is not the same as getting it wrong, because a fallback usually rescues it.
// This test asserts the property that actually matters — that no fallback
// reached the output. GALA emits concrete Go, so an `any` in a type position is
// either something the transpiler could not work out or something the source
// asked for, and only the second is allowed.
//
// The two tests fail on different things, deliberately. The inventory can rise
// without this failing, when the fallbacks hold. This can fail without the
// inventory moving, when a resolved-but-wrong type widens.
//
// A source is taken to have asked for `any` when the word appears in it. That
// is coarse — it exempts a file rather than a declaration — but it needs no
// second parser, and the exempt set stays small enough that a file joining it
// is visible in review.
//
// Note for anyone extending this: examples/*.out is the expected *stdout* of
// the example program, not its generated Go. There is no checked-in corpus of
// generated Go to diff against, so the transpile has to happen here.
func TestNoTypeErasureInGeneratedGo(t *testing.T) {
	files := exampleCorpus(t)

	type erasure struct {
		file string
		spot string
	}
	var found []erasure
	var exempt []string
	var skipped int

	for _, f := range files {
		if f.Err != nil {
			// Intentional negative cases, and examples needing sibling files
			// this harness does not stage. Counted, so a harness that stops
			// covering the corpus cannot look like an improvement.
			skipped++
			continue
		}

		base := strings.TrimSuffix(f.Name, ".gala")
		if sourceRequestsAny(f.Source) {
			exempt = append(exempt, base)
			continue
		}

		for _, spot := range findErasedTypes(t, base, f.Output) {
			found = append(found, erasure{base, spot})
		}
	}

	checked := len(files) - skipped - len(exempt)
	t.Logf("corpus: %d files, %d checked, %d skipped, %d exempt (source names `any`)",
		len(files), checked, skipped, len(exempt))

	// Ceilings on the two ways coverage erodes, kept separate because they
	// mean different things. Examples silently ceasing to transpile is the
	// more serious of the two and would otherwise hide behind slack in the
	// exemption count. Both sit a little above their current values (7 and
	// 57), so ordinary additions do not trip them.
	const maxSkipped, maxExempt = 15, 70
	require.LessOrEqual(t, skipped, maxSkipped,
		"%d examples failed to transpile in this harness (ceiling %d). Each one is "+
			"a file this guard no longer checks; find out why before raising this.",
		skipped, maxSkipped)
	require.LessOrEqual(t, len(exempt), maxExempt,
		"%d examples are exempt because their source names `any` (ceiling %d). "+
			"The exemption is whole-file, so each one hides every widened value in "+
			"that file.",
		len(exempt), maxExempt)

	if len(found) > 0 {
		sort.Slice(found, func(i, j int) bool {
			if found[i].file != found[j].file {
				return found[i].file < found[j].file
			}
			return found[i].spot < found[j].spot
		})
		var b strings.Builder
		for _, e := range found {
			fmt.Fprintf(&b, "  %s.gala: %s\n", e.file, e.spot)
		}
		t.Fatalf("generated Go contains %d erased type(s) whose GALA source never names `any`:\n%s"+
			"GALA generates concrete Go, so an `any` here is a type the transpiler could not "+
			"determine. Resolve the type, or fail with a coded error — do not widen.",
			len(found), b.String())
	}
}

// sourceRequestsAny reports whether the GALA source names `any` itself, in
// which case `any` in the output is what was asked for rather than a fallback.
// Comments are stripped so a note mentioning the word does not grant an
// exemption.
func sourceRequestsAny(src string) bool {
	for _, line := range strings.Split(src, "\n") {
		code := line
		if i := strings.Index(code, "//"); i >= 0 {
			code = code[:i]
		}
		for _, f := range strings.FieldsFunc(code, func(r rune) bool {
			return !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
		}) {
			if f == "any" {
				return true
			}
		}
	}
	return false
}

// findErasedTypes returns the positions in generated Go where a type is `any`
// or a bare `interface{}`.
//
// It parses rather than greps, so `any` inside a string literal, in an
// identifier such as `anyOf`, or in a comment is not mistaken for a type. Only
// positions where a type is expected are reported.
func findErasedTypes(t *testing.T, name, src string) []string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name+".out", src, 0)
	require.NoError(t, err, "golden %s.out does not parse as Go; that is a bug in its own right", name)

	var out []string
	note := func(context string, pos token.Pos) {
		out = append(out, fmt.Sprintf("%s at line %d", context, fset.Position(pos).Line))
	}

	isErased := func(e ast.Expr) bool {
		switch v := e.(type) {
		case *ast.Ident:
			return v.Name == "any"
		case *ast.InterfaceType:
			return v.Methods == nil || len(v.Methods.List) == 0
		}
		return false
	}

	fieldLabel := func(f *ast.Field) string {
		names := make([]string, 0, len(f.Names))
		for _, id := range f.Names {
			names = append(names, id.Name)
		}
		if len(names) == 0 {
			return "<unnamed>"
		}
		return strings.Join(names, ", ")
	}

	// Value positions only: a declared variable, a struct field, a map key or
	// element, a slice element. `var first any` in a sequence pattern is the
	// shape this is for — an element type that could not be worked out,
	// widened rather than reported.
	//
	// Function signatures are deliberately not covered, because `any` is
	// correct and idiomatic in two shapes that dominate any raw count of the
	// word. A type-parameter constraint — `type Box[T any]`, `func f[T any]`
	// — is how an unconstrained GALA type parameter lowers, and flagging it
	// would flag every generic in the corpus. The extractor protocol,
	// `func (s Foo) Unapply(v any)`, takes the match subject and type-asserts
	// it; a concrete type there would defeat the purpose.
	//
	// So this is narrower than "no erasure anywhere": a widened parameter or
	// result type passes. Covering those means excluding those two shapes
	// precisely — TypeParams field lists, and FuncDecls named Unapply — which
	// is worth doing, but is a bigger guard than this one and belongs with
	// whatever first needs it.
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.ValueSpec:
			if v.Type != nil && isErased(v.Type) {
				names := make([]string, 0, len(v.Names))
				for _, id := range v.Names {
					names = append(names, id.Name)
				}
				note("var "+strings.Join(names, ", "), v.Pos())
			}
		case *ast.StructType:
			if v.Fields == nil {
				return true
			}
			for _, f := range v.Fields.List {
				if isErased(f.Type) {
					note("struct field "+fieldLabel(f), f.Pos())
				}
			}
		case *ast.MapType:
			if isErased(v.Key) || isErased(v.Value) {
				note("map type", v.Pos())
			}
		case *ast.ArrayType:
			// Catches `[]any` / `[N]any` wherever it appears, including inside
			// a declaration whose outer type is not itself `any`.
			if isErased(v.Elt) {
				note("slice/array element", v.Pos())
			}
		}
		return true
	})
	return out
}
