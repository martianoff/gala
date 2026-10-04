package transformer

import (
	"fmt"
	"go/ast"

	"martianoff/gala/galaerr"
)

// A match or if-expression whose value is used lowers to a function literal
// called at once. Two things about that call are known only once the code
// around it is lowered, so each one is recorded with the construct's source
// position and checkBranchingCalls looks at the finished file:
//
//   - With no typed branch and no slot type, the function has no result.
//     That is right wherever its value is not used: a statement, the tail of a
//     lambda or function body that then returns nothing, an arm of such a
//     construct. Anywhere else Go rejects the call with "(no value) used as
//     value", far from the GALA source: GALA-E0068.
//   - A `return` written in one of its arms leaves only the function literal.
//     That is the same as leaving the enclosing function when the call is
//     itself returned — `return x match {...}`, the tail of a function or
//     lambda with a result — but anywhere else the enclosing function would
//     go on running with the returned value as the match's: GALA-E0069. (A
//     construct a local `val` or `var` is initialized with is lowered as
//     statements instead, so its `return` does leave the function; see
//     hoisted_value.go.)

// branchingSite is the source position and kind of a recorded call.
type branchingSite struct {
	line, col int
	kind      string // "match" or "if-expression"
	valueless bool   // the function literal has no result
}

// sourcePos is the source position of a lowered `return`.
type sourcePos struct {
	line, col int
}

// recordBranchingCall records call, the lowering of the construct of kind at
// line:col whose value is used.
func (t *galaASTTransformer) recordBranchingCall(call *ast.CallExpr, kind string, valueless bool, line, col int) {
	if t.branchingCalls == nil {
		t.branchingCalls = make(map[*ast.CallExpr]branchingSite)
	}
	t.branchingCalls[call] = branchingSite{line: line, col: col, kind: kind, valueless: valueless}
}

// recordUserReturn records ret as lowered from a source `return` at line:col.
func (t *galaASTTransformer) recordUserReturn(ret *ast.ReturnStmt, line, col int) {
	if t.userReturns == nil {
		t.userReturns = make(map[*ast.ReturnStmt]sourcePos)
	}
	t.userReturns[ret] = sourcePos{line: line, col: col}
}

// checkBranchingCalls reports the first recorded call in file whose value is
// used though it has none (GALA-E0068), or the first source `return` that
// leaves only such a call (GALA-E0069). The file is walked rather than the
// maps: a lowering built for an alternative that was then thrown away is in a
// map but not in the file.
func (t *galaASTTransformer) checkBranchingCalls(file *ast.File) error {
	if len(t.branchingCalls) == 0 {
		return nil
	}
	var stack []ast.Node
	var found error
	ast.Inspect(file, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		switch x := n.(type) {
		case *ast.CallExpr:
			if site, ok := t.branchingCalls[x]; ok && site.valueless {
				if _, stmt := stack[len(stack)-2].(*ast.ExprStmt); !stmt {
					found = untypedBranchingError(site.kind, site.line, site.col)
				}
			}
		case *ast.ReturnStmt:
			if pos, ok := t.userReturns[x]; ok {
				if site, trapped := t.trappingCall(stack); trapped {
					found = galaerr.NewCodedSemanticError(
						galaerr.CodeReturnInBranchingValue,
						pos.line, pos.col,
						fmt.Sprintf("`return` inside %s whose value is used leaves only the %s, not the function", withArticle(site.kind), site.kind),
						fmt.Sprintf("initialize a `val` with the %s first (`val x = ...`) and use `x`: there a `return` in it leaves the function", site.kind))
				}
			}
		}
		return true
	})
	return found
}

// trappingCall reports the recorded call a `return` — the top of stack, the
// path from the file to it — leaves without leaving the enclosing function:
// the innermost function literal around it is a recorded call's, and that
// call's value is neither returned nor the trailing statement of a function
// body (where leaving it is leaving that function).
func (t *galaASTTransformer) trappingCall(stack []ast.Node) (branchingSite, bool) {
	i := len(stack) - 1
	for {
		j := i - 1
		for j >= 0 {
			if _, ok := stack[j].(*ast.FuncLit); ok {
				break
			}
			if _, ok := stack[j].(*ast.FuncDecl); ok {
				return branchingSite{}, false
			}
			j--
		}
		if j < 2 {
			return branchingSite{}, false
		}
		call, ok := stack[j-1].(*ast.CallExpr)
		if !ok || call.Fun != stack[j] {
			return branchingSite{}, false
		}
		site, ok := t.branchingCalls[call]
		if !ok {
			return branchingSite{}, false
		}
		switch stack[j-2].(type) {
		case *ast.ReturnStmt:
			i = j - 2
			continue
		case *ast.ExprStmt:
			if j >= 4 && isTrailingStmtOfFuncBody(stack[j-4], stack[j-3], stack[j-2]) {
				i = j - 2
				continue
			}
		}
		return site, true
	}
}

// isTrailingStmtOfFuncBody reports whether stmt is the last statement of
// block, the body of fn, a function literal or declaration.
func isTrailingStmtOfFuncBody(fn, block, stmt ast.Node) bool {
	b, ok := block.(*ast.BlockStmt)
	if !ok || len(b.List) == 0 || b.List[len(b.List)-1] != stmt {
		return false
	}
	switch f := fn.(type) {
	case *ast.FuncLit:
		return f.Body == b
	case *ast.FuncDecl:
		return f.Body == b
	}
	return false
}

// withArticle is kind, "match" or "if-expression", with its article.
func withArticle(kind string) string {
	if kind == "match" {
		return "a match"
	}
	return "an " + kind
}

// leavingBranchingError is GALA-E0068 for the construct of kind at line:col
// that initializes a declaration though every branch of it leaves with a
// `return`, `break` or `continue`: the declaration is never reached.
func leavingBranchingError(kind string, line, col int) error {
	branch := "arm"
	if kind != "match" {
		branch = "branch"
	}
	return galaerr.NewCodedSemanticError(
		galaerr.CodeUntypedBranchingValue,
		line, col,
		fmt.Sprintf("this %s has no value: every %s leaves with `return`, `break` or `continue`", kind, branch),
		fmt.Sprintf("use the %s as a statement on its own line; nothing after it in the block runs", kind))
}

// untypedBranchingError is GALA-E0068 for the construct of kind at line:col.
func untypedBranchingError(kind string, line, col int) error {
	branch := "arm"
	if kind != "match" {
		branch = "branch"
	}
	return galaerr.NewCodedSemanticError(
		galaerr.CodeUntypedBranchingValue,
		line, col,
		fmt.Sprintf("cannot infer the type of this %s: its value is used, but no %s has a typed value", kind, branch),
		fmt.Sprintf("end each %s in the value the %s stands for (e.g. `Some(1)`, or `None[int]()` with its type spelled out), or use the %s as a statement on its own line", branch, kind, kind))
}
