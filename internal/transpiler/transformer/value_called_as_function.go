package transformer

import (
	"fmt"
	"go/ast"
	"slices"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// A value called as a function
//
// `Red()` where Red is a `val` of a struct type, or `p.N()` where N is an int
// field, calls something that is not a function. The call used to be emitted
// as written and left to `go build`, which reported it against the GENERATED
// expression, including the `.Get()` unwrap the transpiler inserts for a val:
//
//	invalid operation: cannot call Red.Get() (value of struct type Color): Color is not a function
//
// The mistake is usually a stray pair of parentheses — reading a val the way
// one calls a zero-argument accessor, or remembering a val as the function that
// used to build it.

// nonCallableType reports whether a value of type typ is certainly not a
// function, so that calling it is an error GALA can report. It answers only
// when that is known: a predeclared type other than `any`, or a GALA struct or
// sealed type with no Apply method. A function type, a type parameter (of the
// enclosing declaration, or one of typeParams — those of the struct declaring
// a field of type typ), an interface, a pointer and a type GALA cannot see into
// are left to Go.
func (t *galaASTTransformer) nonCallableType(typ transpiler.Type, typeParams []string) bool {
	if transpiler.IsUnusable(typ) {
		return false
	}
	switch typ.(type) {
	case transpiler.BasicType, transpiler.NamedType, transpiler.GenericType:
	default:
		return false
	}
	if t.resolveTranspilerTypeAsFuncType(typ) != nil {
		return false // a function type, or an alias to one
	}
	name := typ.BaseName()
	if slices.Contains(typeParams, name) || t.isActiveTypeParam(name) {
		return false
	}
	// An alias to anything but a function type is judged by what it names.
	// One level only, so an alias cycle in malformed input cannot loop.
	if underlying, ok := t.typeAliases[name]; ok {
		name = underlying.BaseName()
	}
	if name != "any" && transpiler.IsPrimitiveType(name) {
		return true
	}
	// A type declared in this package can arrive as a BasicType carrying its
	// bare name, so all three kinds are looked up.
	meta := t.getTypeMeta(name)
	if meta == nil {
		return false
	}
	// A type with an Apply method is called through it
	// (tryTransformValWithApply).
	if _, hasApply := meta.Methods["Apply"]; hasApply {
		return false
	}
	// Only a struct or sealed type is known to be a data type. Metadata
	// without fields could be an interface, whose value may be anything.
	return meta.IsSealed || meta.IsShorthand || len(meta.FieldNames) > 0
}

// checkValueCalledAsFunction rejects a call whose callee is a val, var or
// parameter, or a val struct field, of a type that is not a function. node is
// the call's argument list, or its empty-call suffix; it locates the
// diagnostic and says whether the callee was written as a member, `x.name(`.
func (t *galaASTTransformer) checkValueCalledAsFunction(fun ast.Expr, node antlr.ParserRuleContext) error {
	if b, ok := t.bindingRef(fun); ok {
		if !t.nonCallableType(b.typ, nil) {
			return nil
		}
		line, col, exact := primaryStartOf(node)
		if !exact {
			line, col = node.GetStart().GetLine(), node.GetStart().GetColumn()
		}
		// A var and a parameter are both "a value": the binding does not say
		// which it is, and the distinction does not change the fix.
		kind := "value"
		if b.isVal {
			kind = "val"
		}
		name := b.String()
		return notCallableError(name, kind, name, name, b.typ, line, col, exact)
	}
	return t.checkValFieldCalledAsFunction(fun, node)
}

// checkValFieldCalledAsFunction is the check for a val struct field, `p.N()`:
// a field is a val unless declared `var`, so the callee arrives as the field's
// unwrap, `p.N.Get()`. The parse tree, not that shape, decides that a field
// was called: `p.N.Get()()`, a call of what the user's own `.Get()` returned,
// has the same shape but is preceded by a call suffix, not by `.N`. A `var`
// field stays a selector, and a call of it is a method call, which
// unknownMethodError checks.
func (t *galaASTTransformer) checkValFieldCalledAsFunction(fun ast.Expr, node antlr.ParserRuleContext) error {
	member := calledMemberToken(node)
	if member == nil {
		return nil
	}
	unwrap := immutableGetReceiver(fun)
	if unwrap == nil {
		return nil
	}
	sel, ok := unwrap.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != member.GetText() {
		return nil
	}
	if id, isIdent := sel.X.(*ast.Ident); isIdent && t.importManager.IsPackage(id.Name) {
		return nil // a package member, not a field
	}
	_, lookupBaseName := t.resolveReceiverTypeAndLookupKey(sel.X)
	meta := t.getTypeMeta(lookupBaseName)
	if meta == nil {
		return nil
	}
	fieldType, isField := meta.Fields[sel.Sel.Name]
	if !isField || !t.nonCallableType(fieldType, meta.TypeParams) {
		return nil
	}
	return fieldNotCallableError(meta, sel.Sel.Name, fieldType, member.GetLine(), member.GetColumn(), true)
}

// fieldNotCallableError is notCallableError for field of the struct meta.
func fieldNotCallableError(meta *transpiler.TypeMetadata, field string, typ transpiler.Type, line, col int, exact bool) error {
	return notCallableError(meta.Name+"."+field, "field", "."+field, field, typ, line, col, exact)
}

// notCallableError builds the diagnostic for calling subject (`Red`,
// `Color.N`), a kind ("val", "value", "field") of type typ. read is how the
// value is read without the call, and spanned is the source text starting at
// line:col that the caret covers when the position is exact.
func notCallableError(subject, kind, read, spanned string, typ transpiler.Type, line, col int, exact bool) error {
	msg := fmt.Sprintf("%s is a %s of type %s, not a function", subject, kind, displayType(typ))
	hint := fmt.Sprintf("remove the parentheses to read the %s: `%s`; only a value of "+
		"function type, or of a type with an Apply method, can be called", kind, read)
	err := galaerr.NewCodedSemanticError(galaerr.CodeValueCalledAsFunction, line, col, msg, hint)
	if exact {
		err = err.WithSpan(col + len([]rune(spanned)))
	}
	return err
}
