package transformer_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPanicAudit asserts that no production-code .go file in the transformer
// package contains a naked `panic(...)` call. Every panic must be either
// (a) wrapped in `galaerr.NewCodedSemanticError(...)` so end users get a
// stable GALA-Exxxx code and an actionable hint, or (b) accompanied by an
// `// invariant: ...` comment on the preceding line declaring why this
// branch is unreachable from any valid GALA program.
//
// This is the codified version of audit-item B10. New panics that don't
// satisfy one of those two conditions should fail this test, forcing the
// author to either route through galaerr or document the invariant.
func TestPanicAudit(t *testing.T) {
	dir := findTransformerSourceDir(t)
	if dir == "" {
		t.Skip("cannot locate transformer source directory (sandbox without runfile data deps); skipping")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read transformer dir: %v", err)
	}

	type violation struct {
		File string
		Line int
		Form string
	}
	var violations []violation

	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		commentByPrevLine := buildPrecedingCommentMap(fset, file)

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok || id.Name != "panic" {
				return true
			}
			pos := fset.Position(call.Pos())
			if isCodedPanic(call) {
				return true
			}
			if hasInvariantComment(commentByPrevLine, pos.Line) {
				return true
			}
			form := exprSummary(call)
			violations = append(violations, violation{File: name, Line: pos.Line, Form: form})
			return true
		})
	}

	if len(violations) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("naked panic(...) calls in production transformer files:\n")
	for _, v := range violations {
		b.WriteString("  ")
		b.WriteString(v.File)
		b.WriteString(":")
		b.WriteString(itoa(v.Line))
		b.WriteString("  panic(")
		b.WriteString(v.Form)
		b.WriteString(")\n")
	}
	b.WriteString("\nFix each by either:\n")
	b.WriteString("  (a) panic(galaerr.NewCodedSemanticError(<code>, line, col, msg, hint))  for user-facing failures, or\n")
	b.WriteString("  (b) prefix with an `// invariant: <name>` comment on the immediately-preceding line for unreachable branches.\n")
	t.Fatal(b.String())
}

// isCodedPanic reports whether call is `panic(galaerr.NewCodedSemanticError(...))`
// or another galaerr.NewSemanticErrorXxx constructor — the safe wrappers that
// produce a structured, locatable error visible to GALA end users.
func isCodedPanic(call *ast.CallExpr) bool {
	if len(call.Args) != 1 {
		return false
	}
	inner, ok := call.Args[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := inner.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkg.Name == "galaerr"
}

// hasInvariantComment reports whether the line immediately above `line`
// carries a comment beginning with "invariant:".
func hasInvariantComment(byPrevLine map[int]string, line int) bool {
	c, ok := byPrevLine[line]
	if !ok {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(c, "//")), "invariant:")
}

// buildPrecedingCommentMap returns a map from a source line N to the text of
// the // comment that ends on line N-1 (if any). Used by hasInvariantComment.
func buildPrecedingCommentMap(fset *token.FileSet, file *ast.File) map[int]string {
	out := map[int]string{}
	for _, group := range file.Comments {
		last := group.List[len(group.List)-1]
		endLine := fset.Position(last.End()).Line
		out[endLine+1] = last.Text
	}
	return out
}

func exprSummary(call *ast.CallExpr) string {
	if len(call.Args) == 0 {
		return ""
	}
	if lit, ok := call.Args[0].(*ast.BasicLit); ok {
		return lit.Value
	}
	if id, ok := call.Args[0].(*ast.Ident); ok {
		return id.Name
	}
	return "<expr>"
}

// itoa is a tiny helper to avoid pulling in strconv just for line numbers.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		buf[n] = '-'
	}
	return string(buf[n:])
}

// findTransformerSourceDir locates the transformer package's source directory
// so the audit can read its .go files. Returns "" if the directory cannot be
// located (e.g. running in a Bazel sandbox without source-file data deps).
func findTransformerSourceDir(t *testing.T) string {
	t.Helper()

	// runtime.Caller gives us this test file's path at compile time; under
	// Bazel that path lives in the sandboxed exec root and the sibling .go
	// files are co-located, so this works even when source files are not
	// declared as data deps.
	if _, file, _, ok := runtime.Caller(0); ok {
		dir := filepath.Dir(file)
		if hasTransformerSources(dir) {
			return dir
		}
	}

	// Fallback: walk up from cwd looking for the package.
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, "internal", "transpiler", "transformer")
		if hasTransformerSources(candidate) {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func hasTransformerSources(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "transformer.go"))
	return err == nil && !info.IsDir()
}
