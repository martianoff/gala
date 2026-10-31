package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"sort"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// Unknown method on a known GALA type
//
// A method call whose receiver type is known but which that type does not
// declare used to be emitted verbatim and left to `go build`. The message that
// came back described the GENERATED expression, not the source:
//
//	xs.Get().Filter(func(x int) bool {…}).Sum undefined
//	  (type collection_immutable.Array[int] has no field or method Sum)
//
// The `.Get()` in that expression is the Immutable[T] auto-unwrap the
// transpiler inserts; the user wrote `xs.Filter(...).Sum()`. Reporting in GALA
// keeps the expression the user's own, and lets the diagnostic suggest a method
// that actually exists.

// synthesizedMethodNames are methods the TRANSFORMER generates onto a type
// rather than the user declaring them in GALA source. They are therefore absent
// from TypeMetadata.Methods, and a check that only consulted that map would
// reject every one of them.
//
// Kept as a single declaration for the same reason ForbiddenBuiltinNames is:
// the generating sites and the checking site must not be able to drift apart.
// Each entry names where it is synthesized.
var synthesizedMethodNames = func() map[string]bool {
	names := map[string]bool{
		"Unapply": true, // sealed.go — case companion extractor
		"Apply":   true, // sealed.go — companion constructor
		// codec.go / codec_typed.go — generated for JSON/YAML codecs
		"NumFields":    true,
		"FieldName":    true,
		"EncodeFields": true,
		"DecodeFields": true,
		"FieldIsEmpty": true,
	}
	// methods.go, sealed.go — generated on every struct and sealed type
	for _, n := range transpiler.StructGeneratedMethods {
		names[n] = true
	}
	for _, n := range transpiler.SealedGeneratedMethods {
		names[n] = true
	}
	return names
}()

// isSynthesizedMethodName reports whether name is generated rather than
// declared. The `is<Variant>` predicates are matched by shape because the
// variant half is the user's own name (sealed.go emits `is`+variant per case).
func isSynthesizedMethodName(name string) bool {
	if synthesizedMethodNames[name] {
		return true
	}
	if strings.HasPrefix(name, "is") && len(name) > 2 {
		r := []rune(name)[2]
		return r >= 'A' && r <= 'Z'
	}
	return false
}

// checkUnknownMethod reports a method the receiver's GALA type does not
// declare, for a call that carries an argument list. It only locates the
// position; unknownMethodError holds the guards that decide whether the
// judgement can be made at all.
func (t *galaASTTransformer) checkUnknownMethod(
	argListCtx *grammar.ArgumentListContext,
	typeMeta *transpiler.TypeMetadata,
	method string,
	recvType transpiler.Type,
) error {
	// Cheap reject first: this branch is reached for every method call whose
	// metadata was not found, which includes every Go-interop receiver, so the
	// parse-tree walk below should not run in the common non-error case.
	if typeMeta == nil {
		return nil
	}
	line, col, exact := callAnchorPos(argListCtx)
	return t.unknownMethodError(typeMeta, method, recvType, line, col, exact)
}

// checkUnknownMethodZeroArg is the same check for `recv.Method()` with an EMPTY
// argument list. Those never reach the call dispatcher: applyCallSuffix handles
// a nil argument list on its own and ends at a verbatim `CallExpr{Fun: base}`,
// so without this the check would miss exactly the calls most likely to be
// wrong — `xs.Sum()`, `p.Norm()`, every accessor-shaped typo.
// recvType and lookupBaseName are resolved by the caller, which already needed
// them for its generic-method check; recomputing here would repeat an inference
// pass that is not cached when manual resolution falls short.
func (t *galaASTTransformer) checkUnknownMethodZeroArg(
	base ast.Expr,
	suffix *grammar.PostfixSuffixContext,
	recvType transpiler.Type,
	lookupBaseName string,
) error {
	sel, ok := base.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	// A package-qualified call (`time.Now()`) has a package, not a value, on
	// the left; it is not a method call and has no receiver type to judge.
	if id, isIdent := sel.X.(*ast.Ident); isIdent && t.importManager.IsPackage(id.Name) {
		return nil
	}

	typeMeta := t.getTypeMeta(lookupBaseName)
	if typeMeta == nil {
		return nil
	}

	line, col, exact := callAnchorPos(suffix)
	return t.unknownMethodError(typeMeta, sel.Sel.Name, recvType, line, col, exact)
}

// unknownMethodError applies the guards and builds the diagnostic. Shared by
// the argument-carrying and zero-argument call paths so the two cannot drift.
//
// A false positive here rejects a program that is actually correct, so the
// check is deliberately conservative. It says nothing unless the receiver's
// type is a known GALA type (which alone excludes every Go-interop receiver)
// spelled as an instance of it (see receiverNamesAnInstance), the name is
// neither transformer-synthesized nor a function-typed field, and the method
// set is trustworthy — see the empty-method-set note below.
func (t *galaASTTransformer) unknownMethodError(
	typeMeta *transpiler.TypeMetadata,
	method string,
	recvType transpiler.Type,
	line, col int,
	exact bool,
) error {
	if typeMeta == nil || isSynthesizedMethodName(method) || t.isSynthesizedOpaqueMethod(typeMeta, method) {
		return nil
	}
	// An empty method set is ambiguous: it means either "this type genuinely
	// has no methods" or "this type's metadata was loaded without them". For a
	// type declared in the package being compiled the first reading is certain,
	// so it can still be judged; for a foreign type the check stands down
	// rather than risk reporting every call on it.
	if len(typeMeta.Methods) == 0 && typeMeta.Package != t.packageName {
		return nil
	}
	if _, declared := typeMeta.Methods[method]; declared {
		return nil
	}
	// A hand-written .go file of the type's package may declare methods on it
	// too: they are part of its method set as much as the GALA ones.
	goMethods := t.goMethodsOnGalaType(typeMeta)
	if _, declared := goMethods[method]; declared {
		return nil
	}
	if !t.receiverNamesAnInstance(typeMeta, recvType) {
		return nil
	}
	// A field holding a function is called the same way a method is; the field
	// set is consulted so `cfg.OnEvent()` is not mistaken for a missing method.
	// A field of a type that is not callable is the other mistake: `p.N()`
	// for an int field N (GALA-E0054). It stays behind the guards above: a
	// receiver here is only a selector, and one resolved to the wrong type —
	// a Go `fs.FileInfo` taken for a GALA struct of that name — must not be
	// judged by that type's fields.
	if fieldType, isField := typeMeta.Fields[method]; isField {
		if t.nonCallableType(fieldType, typeMeta.TypeParams) {
			return fieldNotCallableError(typeMeta, method, fieldType, line, col, exact)
		}
		return nil
	}

	msg := fmt.Sprintf("%s has no method %s", typeMeta.Name, method)
	err := galaerr.NewCodedSemanticError(
		galaerr.CodeUnknownMethod, line, col, msg,
		unknownMethodHint(typeMeta, goMethods, method),
	)
	if exact {
		err = err.WithSpan(col + len([]rune(method)))
	}
	return err
}

// checkMethodVisibility rejects a call of a method another package's GALA type
// has but does not export (GALA-E0083), for every method call: with or without
// arguments, generic or not, with defaults filled or not. It runs where the
// dispatcher first resolves the receiver, before any of those paths returns,
// because visibility is a property of the method found, not of failing to
// find one. anchor is the call's argument list, or its `()`.
//
// Visibility follows Go: a lower-case method — including the `is<Variant>`
// predicates every sealed type gets — is private to its package. The call used
// to be emitted verbatim and Go rejected the generated expression ("cannot
// refer to unexported method"); a generic one, lowered to the free function
// `Type_method`, even compiled. Types not declared in GALA source (no
// DefinedIn: Go-interop types) are left to Go. Only the receiver's base type
// is consulted: visibility does not depend on its type arguments.
func (t *galaASTTransformer) checkMethodVisibility(
	receiver ast.Expr,
	method string,
	lookupBaseName string,
	anchor antlr.ParserRuleContext,
) error {
	// Runs for every method call, so the exported (common) case returns before
	// any metadata lookup.
	if receiver == nil || method == "" || lookupBaseName == "" || token.IsExported(method) {
		return nil
	}
	// A package-qualified call (`shapes.helper()`) has no receiver value; a
	// lower-case function of another package is not this check's concern.
	if id, isIdent := receiver.(*ast.Ident); isIdent && t.importManager.IsPackage(id.Name) {
		return nil
	}
	typeMeta := t.getTypeMeta(lookupBaseName)
	if typeMeta == nil || typeMeta.DefinedIn == "" || visibleFrom(method, typeMeta, t.packageName) {
		return nil
	}
	if _, declared := typeMeta.Methods[method]; !declared {
		if _, declared := t.goMethodsOnGalaType(typeMeta)[method]; !declared {
			return nil
		}
	}
	line, col, exact := callAnchorPos(anchor)
	pkg := transpiler.PackageDisplayName(typeMeta.Package)
	hint := fmt.Sprintf("call an exported method (only the capitalized methods of %s are visible outside package %s)", typeMeta.Name, pkg)
	if pattern, ok := sealedPredicatePattern(typeMeta, method); ok {
		hint = fmt.Sprintf("match the variant instead (is<Variant> predicates are private to the sealed type's package): `case %s.%s => true`, `case _ => false`", pkg, pattern)
	}
	err := galaerr.NewCodedSemanticError(
		galaerr.CodeUnexportedMethod, line, col,
		fmt.Sprintf("%s is an unexported method of %s.%s, so only package %s can call it", method, pkg, typeMeta.Name, pkg),
		hint,
	)
	if exact {
		err = err.WithSpan(col + len([]rune(method)))
	}
	return err
}

// sealedPredicatePattern returns the case pattern (`B()`, `A(_)`) matching the
// variant whose `is<Variant>` predicate method names, when typeMeta is a sealed
// type that has that variant.
func sealedPredicatePattern(typeMeta *transpiler.TypeMetadata, method string) (string, bool) {
	if !typeMeta.IsSealed {
		return "", false
	}
	name, ok := strings.CutPrefix(method, "is")
	if !ok {
		return "", false
	}
	for i := range typeMeta.SealedVariants {
		if v := &typeMeta.SealedVariants[i]; v.Name == name {
			return name + "(" + repeatJoin("_", len(v.FieldNames)) + ")", true
		}
	}
	return "", false
}

// goMethodsOnGalaType returns the methods hand-written .go files of its package
// declare on the GALA type typeMeta describes (see GoKindMethodsOnly), or nil.
func (t *galaASTTransformer) goMethodsOnGalaType(typeMeta *transpiler.TypeMetadata) map[string]*transpiler.GoFuncSignature {
	if rec := t.galaTypeGoMethods(typeMeta); rec != nil {
		return rec.Methods
	}
	return nil
}

// galaTypeGoMethods returns the record of the methods hand-written .go files
// of its package declare on the GALA type typeMeta describes, or nil. It is
// keyed the way GALA metadata keys the type, apart from Go types: a Go type of
// the same key (io/fs's `fs.FileInfo` beside GALA's `fs.FileInfo`) is a
// different type.
func (t *galaASTTransformer) galaTypeGoMethods(typeMeta *transpiler.TypeMetadata) *transpiler.GoTypeData {
	return t.goTypeInfo.GetGalaTypeMethods(typeMeta.Package + "." + typeMeta.Name)
}

// siblingMethodSignature looks method up among those hand-written .go files
// declare on the GALA type of a value of type recv (or of an instantiation of
// it), for goMethodSignature, which has stripped a pointer and checked recv
// and Go type info. declared reports whether they declare it; the
// caller then must not consult a Go type of the same key, which is another
// type. sig is the signature as declared: a generic type's method still names
// the receiver's type parameters (see goMethodSignature, which instantiates
// it).
func (t *galaASTTransformer) siblingMethodSignature(recv transpiler.Type, method string) (sig *transpiler.GoFuncSignature, declared bool) {
	if len(t.goTypeInfo.GalaTypeMethods) == 0 {
		return nil, false
	}
	base := receiverBase(recv)
	if named, ok := base.(transpiler.NamedType); ok && t.isGoTyped(named) {
		return nil, false
	}
	meta := t.getTypeMeta(base.BaseName())
	if meta == nil {
		return nil, false
	}
	rec := t.galaTypeGoMethods(meta)
	if rec == nil {
		return nil, false
	}
	sig, declared = rec.Methods[method]
	return sig, declared
}

// receiverNamesAnInstance reports whether the receiver's type is an instance
// of the type typeMeta describes: it carries one type argument per type
// parameter. Which types those are does not matter. A GALA type's method
// set does not depend on them, so `Node[T]` inside `func walk[T any]` has
// exactly the methods of `Node[int]` (#807).
//
// A receiver spelled with the wrong number of type arguments (`Res[int,
// string]` for `type Res[T any] Try[T]`) names no instance of the type, so its
// method set cannot be judged; Go reports the type itself. Nor is a receiver
// missing a type argument: one inference produced no type for, even nested.
func (t *galaASTTransformer) receiverNamesAnInstance(typeMeta *transpiler.TypeMetadata, recvType transpiler.Type) bool {
	if len(typeMeta.TypeParams) == 0 {
		return true
	}
	args := t.getReceiverTypeArgTypes(recvType)
	return len(args) == len(typeMeta.TypeParams) && !slices.ContainsFunc(args, transpiler.ContainsUnusable)
}

// unknownMethodHint suggests the nearest real method by edit distance, and
// otherwise lists a few of the methods the type does have. Naming a method that
// exists is the whole point: "Sum does not exist" leaves the caller to guess,
// while "did you mean Size" or a list containing FoldLeft ends the search.
func unknownMethodHint(typeMeta *transpiler.TypeMetadata, goMethods map[string]*transpiler.GoFuncSignature, method string) string {
	names := make([]string, 0, len(typeMeta.Methods)+len(goMethods))
	for name := range typeMeta.Methods {
		names = append(names, name)
	}
	for name := range goMethods {
		if _, dup := typeMeta.Methods[name]; !dup {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	if best := nearestName(method, names); best != "" {
		return fmt.Sprintf("did you mean `%s`?", best)
	}

	// A type in the package being compiled is judged even with an empty method
	// set (see unknownMethodError), so the list can genuinely be empty. Say so,
	// rather than rendering "Point declares: " with nothing after the colon.
	if len(names) == 0 {
		return fmt.Sprintf("%s declares no methods", typeMeta.Name)
	}

	const show = 8
	shown := names
	suffix := ""
	if len(shown) > show {
		shown, suffix = shown[:show], ", ..."
	}
	return fmt.Sprintf("%s declares: %s%s", typeMeta.Name, strings.Join(shown, ", "), suffix)
}

// nearestName returns the candidate within edit distance 2 of name, preferring
// the closest. Two is the usual typo budget: it catches a transposition, a
// wrong letter and a dropped one, without proposing an unrelated method. The
// same threshold suggestExtractorName uses, over the package's one distance
// primitive.
func nearestName(name string, candidates []string) string {
	best, bestDist := "", 3
	lower := strings.ToLower(name)
	for _, c := range candidates {
		d := levenshteinDistance(lower, strings.ToLower(c))
		if d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

// methodNameStartOf locates the `.name` suffix that precedes this argument
// list, so the caret lands on the method name rather than on the parenthesis.
func methodNameStartOf(node antlr.Tree) (line, col int, ok bool) {
	tok := calledMemberToken(node)
	if tok == nil {
		return 0, 0, false
	}
	return tok.GetLine(), tok.GetColumn(), true
}

// callAnchorPos is the position a call diagnostic reports: the called method's
// name when it can be located (exact), otherwise the start of anchor — the
// call's argument list or its `()`.
func callAnchorPos(anchor antlr.ParserRuleContext) (line, col int, exact bool) {
	if line, col, exact = methodNameStartOf(anchor); exact {
		return line, col, true
	}
	return anchor.GetStart().GetLine(), anchor.GetStart().GetColumn(), false
}

// calledMemberToken returns the identifier of the `.name` suffix that
// immediately precedes the call suffix holding node, or nil when the call is
// not of a member — `f(x)`, or `g()(x)`, whose preceding suffix is a call.
//
// `postfixExpr: primaryExpr postfixSuffix*` means `xs.Sum()` carries two
// suffixes — `.Sum` and `(...)` — and the argument list belongs to the second.
// The method name is the identifier of the one before it.
func calledMemberToken(node antlr.Tree) antlr.Token {
	// Walk up to the postfixSuffix that owns this argument list.
	var suffix *grammar.PostfixSuffixContext
	for n := node; n != nil; n = n.GetParent() {
		if s, isSuffix := n.(*grammar.PostfixSuffixContext); isSuffix {
			suffix = s
			break
		}
	}
	if suffix == nil {
		return nil
	}
	pe, isPE := suffix.GetParent().(*grammar.PostfixExprContext)
	if !isPE {
		return nil
	}
	all := pe.AllPostfixSuffix()
	for i, s := range all {
		if s != suffix || i == 0 {
			continue
		}
		if id := all[i-1].Identifier(); id != nil {
			return id.GetStart()
		}
		return nil
	}
	return nil
}
