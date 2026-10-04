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

// branchingSite is the source position and kind ("match" or "if-expression")
// of a recorded call.
type branchingSite struct {
	loopControlSite
	kind string
}

// recordBranchingCall records call, the lowering of the construct of kind at
// line:col whose value is used.
func (t *galaASTTransformer) recordBranchingCall(call *ast.CallExpr, kind string, line, col int) {
	if t.branchingCalls == nil {
		t.branchingCalls = make(map[*ast.CallExpr]branchingSite)
	}
	t.branchingCalls[call] = branchingSite{loopControlSite{line, col}, kind}
}

// recordUserReturn records ret as lowered from a source `return` at line:col.
func (t *galaASTTransformer) recordUserReturn(ret *ast.ReturnStmt, line, col int) {
	if t.userReturns == nil {
		t.userReturns = make(map[*ast.ReturnStmt]loopControlSite)
	}
	t.userReturns[ret] = loopControlSite{line, col}
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
			if site, ok := t.branchingCalls[x]; ok && isVoidIIFE(x) {
				if _, stmt := stack[holderIndex(stack, len(stack)-1)].(*ast.ExprStmt); !stmt {
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
// the innermost function literal around it is a recorded call's (stack[j] the
// literal, stack[j-1] the call, holderIndex what holds the call), and that
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
		h := holderIndex(stack, j-1)
		switch stack[h].(type) {
		case *ast.ReturnStmt:
			i = h
			continue
		case *ast.ExprStmt:
			if h >= 2 && isTrailingStmtOfFuncBody(stack[h-2], stack[h-1], stack[h]) {
				i = h
				continue
			}
		}
		return site, true
	}
}

// holderIndex is the index in stack of the node that holds stack[i], looking
// through parentheses around it.
func holderIndex(stack []ast.Node, i int) int {
	h := i - 1
	for h > 0 {
		if _, paren := stack[h].(*ast.ParenExpr); !paren {
			break
		}
		h--
	}
	return h
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

// branchNoun names a branch of a construct of kind: an arm of a match, a
// branch of an if-expression.
func branchNoun(kind string) string {
	if kind == "match" {
		return "arm"
	}
	return "branch"
}

// untypedBranchingError is GALA-E0068 for the construct of kind at line:col
// whose value is used though no branch of it has a typed value.
func untypedBranchingError(kind string, line, col int) error {
	return galaerr.NewCodedSemanticError(
		galaerr.CodeUntypedBranchingValue,
		line, col,
		fmt.Sprintf("cannot infer the type of this %s: its value is used, but no %s has a typed value", kind, branchNoun(kind)),
		fmt.Sprintf("end each %s in the value the %s stands for (e.g. `Some(1)`, or `None[int]()` with its type spelled out), or use the %s as a statement on its own line", branchNoun(kind), kind, kind))
}

// leavingBranchingError is GALA-E0068 for the construct of kind at line:col
// that initializes a declaration though every branch of it leaves with a
// `break`, `continue` or a `return` with no value: the declaration is never
// reached, and nothing gives it a type.
func leavingBranchingError(kind string, line, col int) error {
	return galaerr.NewCodedSemanticError(
		galaerr.CodeUntypedBranchingValue,
		line, col,
		fmt.Sprintf("this %s has no value: every %s leaves with `return`, `break` or `continue`", kind, branchNoun(kind)),
		fmt.Sprintf("use the %s as a statement on its own line; nothing after it in the block runs", kind))
}

// unknownBranchTypeError is GALA-E0068 for an if-expression at line:col whose
// branches have values, but of no known type (`if (c) nil else nil`): unlike
// a construct with no value, a declared type gives it one.
func unknownBranchTypeError(line, col int) error {
	return galaerr.NewCodedSemanticError(
		galaerr.CodeUntypedBranchingValue,
		line, col,
		"cannot infer the type of this if-expression: no branch has a known type",
		"declare the type its value fills (e.g. `val x Option[int] = if (...) ...`) or give a branch a typed value (e.g. `None[int]()`)")
}
