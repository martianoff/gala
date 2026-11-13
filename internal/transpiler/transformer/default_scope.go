package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"hash/fnv"
	"path/filepath"
	"slices"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// The scope of a default value
//
// A default is evaluated in the callee's scope, left to right, with the
// earlier parameters bound: `func scale(a int, b int = a * 2)` makes
// `scale(1)` scale(1, 2). The caller's locals are never visible to it, and a
// package-level name it reads is the callee package's even where the caller
// shadows it.
//
// The default is still lowered at the call site. While it is, the scope
// lookups skip the use site's scopes (skipUseSite), the earlier parameters
// are bound as vals of their declared types, and the receiver as in the
// method body. bindDefaultNames then rewrites the lowered Go:
//
//   - a parameter, or the receiver, becomes the value the call passes for it:
//     a copy when reading the value again is the same as reading it once
//     (a literal, a val), or else a temporary the call evaluates the argument
//     into once, in its place (see call_order.go);
//   - a package-level name a local of the use site shadows is read through a
//     package-level alias the file declares for it, since Go has no other way
//     to reach a shadowed package-level name.

// defaultParam is an earlier parameter a default may read: its name and
// declared type, and the argument the call passes for it.
type defaultParam struct {
	name string
	typ  transpiler.Type
	arg  ast.Expr
}

// defaultParams returns, for the default of parameter i of a signature with
// the parameters names of types, the earlier parameters it may read, args
// being what the call passes for each parameter so far (nil, or shorter, where
// unknown), and the names it may not read: parameter i and those after it.
func defaultParams(names []string, types []transpiler.Type, i int, args []ast.Expr) (params []defaultParam, later []string) {
	for j, name := range names {
		if name == "" || name == "_" {
			continue
		}
		if j >= i {
			later = append(later, name)
			continue
		}
		if j >= len(args) || args[j] == nil || j >= len(types) {
			continue
		}
		params = append(params, defaultParam{name: name, typ: types[j], arg: args[j]})
	}
	return params, later
}

// laterParamError is GALA-E0094 for a default whose source reads one of later,
// its own parameter or a later one, at that name. lowered is the default as
// lowered, nil when lowering it failed: a name it binds itself (a lambda
// parameter) is no parameter, and when lowering failed, what it failed on may
// be the later parameter.
func laterParamError(def exprSrc, later []string, lowered ast.Expr) error {
	if len(later) == 0 {
		return nil
	}
	if lowered != nil {
		reads := false
		substituteFree(lowered, func(name string) ast.Expr {
			reads = reads || slices.Contains(later, name)
			return nil
		})
		if !reads {
			return nil
		}
	}
	id := firstReference(def, func(id srcNode) bool {
		for _, name := range later {
			if id.text() == name {
				return true
			}
		}
		return false
	})
	if !id.ok() {
		return nil
	}
	line, col := id.start()
	name := id.text()
	if name == later[0] {
		return galaerr.NewCodedSemanticError(galaerr.CodeDefaultReadsLaterParam, line, col,
			fmt.Sprintf("the default value of parameter %s reads %s itself", name, name),
			"a default may read only the parameters before its own; give it a value that does not need the parameter")
	}
	return galaerr.NewCodedSemanticError(galaerr.CodeDefaultReadsLaterParam, line, col,
		fmt.Sprintf("the default value of parameter %s reads parameter %s, which is not bound until after it", later[0], name),
		fmt.Sprintf("a default may read only the parameters before its own; declare %s before %s, or pass %s explicitly", name, later[0], later[0]))
}

// bindDefaultNames rewrites expr, the lowered default src, for its use site,
// whose scope is useScope (see above).
func (t *galaASTTransformer) bindDefaultNames(expr ast.Expr, src defaultSource, useScope *scope, useLine, useCol int) (ast.Expr, error) {
	bound := map[string]ast.Expr{}
	for _, p := range src.params {
		bound[p.name] = p.arg
	}
	if src.recv != "" {
		bound[src.recv] = src.recvExpr
	}
	var shadowErr error
	values := map[string]ast.Expr{}
	expr = substituteFree(expr, func(name string) ast.Expr {
		if arg, ok := bound[name]; ok {
			if v, done := values[name]; done {
				return v
			}
			// The argument is the use site's: judged in its scope.
			saved, savedScope := t.loweringDefault, t.currentScope
			t.loweringDefault, t.currentScope = nil, useScope
			v := arg
			// A receiver that makes no call is read where it is: a
			// pointer-receiver method must see the variable itself.
			if name != src.recv || t.makesEffectfulCall(arg) {
				v = t.boundArgValue(arg)
			}
			t.loweringDefault, t.currentScope = saved, savedScope
			values[name] = v
			return v
		}
		if !shadowedAt(useScope, name) {
			return nil
		}
		alias, err := t.packageNameAlias(name, src)
		if err != nil {
			if shadowErr == nil {
				shadowErr = galaerr.NewSemanticErrorAt(useLine, useCol, err.Error())
			}
			return nil
		}
		return alias
	})
	return expr, shadowErr
}

// shadowedAt reports whether a local of the scopes from s up to the package
// scope binds name.
func shadowedAt(s *scope, name string) bool {
	for ; s != nil && s.parent != nil; s = s.parent {
		if _, ok := s.vals[name]; ok {
			return true
		}
	}
	return false
}

// packageNameAlias returns the expression that reads the package-level name
// through an alias the file declares for it, where a local shadows it.
func (t *galaASTTransformer) packageNameAlias(name string, src defaultSource) (ast.Expr, error) {
	if t.importManager != nil && t.importManager.IsPackage(name) {
		return nil, fmt.Errorf("the default value of a parameter reads the package %s, which a local of this call site shadows; rename the local", name)
	}
	h := fnv.New32a()
	h.Write([]byte(filepath.Base(t.filePath)))
	alias := fmt.Sprintf("_gala_default_%s_%08x", name, h.Sum32())
	// The alias is bound in the package scope beside name, so the code around
	// the lowered default infers its type as it would name's.
	root := t.currentScope
	for root != nil && root.parent != nil {
		root = root.parent
	}
	// The reference to the package-level name, which keeps reaching it if
	// renameShadowingTopLevel renames it.
	ref := ast.NewIdent(name)
	t.markTopLevelRef(ref)
	var value ast.Expr
	deref := false
	switch {
	case t.isTopLevelBinding(name) && root != nil && root.vals[name]:
		// A val never changes: the alias holds its value.
		value = ref
		root.vals[alias], root.valTypes[alias] = true, root.valTypes[name]
		if root.boxedVals[name] {
			root.boxedVals[alias] = true // non-nil: it holds name
		}
	case t.isTopLevelBinding(name) && root != nil:
		// A var does: the alias points at it.
		value, deref = &ast.UnaryExpr{Op: token.AND, X: ref}, true
		root.vals[alias], root.valTypes[alias] = false, transpiler.PointerType{Elem: root.valTypes[name]}
	default:
		fm, ok := t.unshadowedFunctionByName(name)
		if !ok || len(fm.TypeParams) > 0 {
			return nil, fmt.Errorf("the default value of a parameter reads %s, which a local of this call site shadows; rename the local", name)
		}
		value = ref
	}
	if !t.defaultRefNames[alias] {
		if t.defaultRefNames == nil {
			t.defaultRefNames = map[string]bool{}
		}
		t.defaultRefNames[alias] = true
		t.defaultRefDecls = append(t.defaultRefDecls, &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{
			&ast.ValueSpec{Names: []*ast.Ident{ast.NewIdent(alias)}, Values: []ast.Expr{value}},
		}})
	}
	if deref {
		return &ast.ParenExpr{X: t.markDeref(&ast.StarExpr{X: ast.NewIdent(alias)})}, nil
	}
	return ast.NewIdent(alias), nil
}

// substituteFree replaces each free identifier of expr — a reference, not a
// member or a name a function literal of expr binds — by what with returns
// for its name, when that is not nil, in one pass: a replacement is not
// itself searched.
func substituteFree(expr ast.Expr, with func(name string) ast.Expr) ast.Expr {
	inserted := map[ast.Node]bool{}
	var walk func(n ast.Node, shadowed map[string]bool)
	holder := &ast.ParenExpr{X: expr}
	walk = func(n ast.Node, shadowed map[string]bool) {
		ast.Inspect(n, func(c ast.Node) bool {
			if c == nil || inserted[c] {
				return false
			}
			if fl, ok := c.(*ast.FuncLit); ok && c != n {
				inner := map[string]bool{}
				for k := range shadowed {
					inner[k] = true
				}
				for k := range boundNames(fl) {
					inner[k] = true
				}
				walk(fl, inner)
				return false
			}
			for _, slot := range referenceSlots(c) {
				id, ok := (*slot).(*ast.Ident)
				if !ok || shadowed[id.Name] {
					continue
				}
				if r := with(id.Name); r != nil {
					*slot = r
					inserted[r] = true
				}
			}
			return true
		})
	}
	walk(holder, map[string]bool{})
	return holder.X
}
