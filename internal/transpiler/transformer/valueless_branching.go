package transformer

import (
	"fmt"
	"go/ast"

	"martianoff/gala/galaerr"
)

// A match or if-expression none of whose branches has a typed value, and
// whose slot gives it no type, lowers to a function literal with no result,
// called at once. That is right wherever its value is not used: a statement,
// the tail of a lambda or function body that then returns nothing, or an arm
// of such a construct. Anywhere else Go rejects the call with "(no value)
// used as value", far from the GALA source.
//
// Whether the value is used is known only once the enclosing code is lowered,
// so each such call is recorded with the construct's position, and
// checkValuelessBranching reports the first one the finished file does not
// run as a statement.

// valuelessSite is the source position and kind of a recorded call.
type valuelessSite struct {
	line, col int
	kind      string // "match" or "if-expression"
}

// recordValueless records call, the lowering of the construct of kind at
// line:col, as having no value.
func (t *galaASTTransformer) recordValueless(call *ast.CallExpr, kind string, line, col int) {
	if t.valuelessSites == nil {
		t.valuelessSites = make(map[*ast.CallExpr]valuelessSite)
	}
	t.valuelessSites[call] = valuelessSite{line: line, col: col, kind: kind}
}

// checkValuelessBranching reports GALA-E0068 for the first recorded call in
// file that is not an expression statement. The file is walked rather than the
// map: a lowering built for an alternative that was then thrown away is in the
// map but not in the file.
func (t *galaASTTransformer) checkValuelessBranching(file *ast.File) error {
	if len(t.valuelessSites) == 0 {
		return nil
	}
	var bad *ast.CallExpr
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		if bad != nil {
			return false
		}
		switch x := n.(type) {
		case *ast.ExprStmt:
			// A recorded call run as a statement is fine; what it holds may
			// not be.
			if call, ok := x.X.(*ast.CallExpr); ok {
				if _, recorded := t.valuelessSites[call]; recorded {
					ast.Inspect(call.Fun, visit)
					for _, arg := range call.Args {
						ast.Inspect(arg, visit)
					}
					return false
				}
			}
		case *ast.CallExpr:
			if _, recorded := t.valuelessSites[x]; recorded {
				bad = x
				return false
			}
		}
		return true
	}
	ast.Inspect(file, visit)
	if bad == nil {
		return nil
	}
	site := t.valuelessSites[bad]
	branch := "arm"
	if site.kind != "match" {
		branch = "branch"
	}
	return galaerr.NewCodedSemanticError(
		galaerr.CodeUntypedBranchingValue,
		site.line, site.col,
		fmt.Sprintf("cannot infer the type of this %s: its value is used, but no %s has a typed value", site.kind, branch),
		fmt.Sprintf("end each %s in the value the %s stands for (e.g. `Some(1)`, or `None[int]()` with its type spelled out), or use the %s as a statement on its own line", branch, site.kind, site.kind))
}
