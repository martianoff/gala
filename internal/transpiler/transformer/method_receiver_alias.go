package transformer

import (
	"fmt"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// A method declared on a type alias
//
// `type X Y` lowers to Go's `type X = Y`, so a method written on X has Y as its
// receiver base type. Go permits a method only on a type its own package
// declares, and the declaration used to be emitted unchecked — so the rejection
// arrived from `go build`, against generated code, in Go's vocabulary:
//
//	cannot define new methods on non-local type DateTime
//	invalid receiver type Handler
//
// Both name a locality property of a file the author never wrote, for a type
// they declared in GALA, and both appear only after a clean transpile.

// recordMethodReceiver notes a method's receiver for checkMethodReceivers to
// validate once the whole file has been walked.
func (t *galaASTTransformer) recordMethodReceiver(recvCtx *grammar.ReceiverContext, recvTypeName string) {
	if recvTypeName == "" || recvCtx == nil {
		return
	}
	t.methodReceivers = append(t.methodReceivers, methodReceiver{ctx: recvCtx, typeName: recvTypeName})
}

// checkMethodReceivers validates every receiver recorded during the walk. It
// runs after the last declaration so that aliases declared below a method that
// names them are still in the table.
func (t *galaASTTransformer) checkMethodReceivers() error {
	for _, recv := range t.methodReceivers {
		if err := t.checkMethodReceiverAlias(recv.ctx, recv.typeName); err != nil {
			return err
		}
	}
	return nil
}

// methodReceiver is one method's receiver, held until the file is fully walked.
type methodReceiver struct {
	ctx      *grammar.ReceiverContext
	typeName string
}

// checkMethodReceiverAlias rejects a method whose receiver names a type alias
// that cannot carry one.
//
// `type X Y` lowers to Go's `type X = Y`, so the method's receiver base type is
// Y, and Go accepts a method only on a type its own package declares. The rule
// is stated positively — accept when the chain ends at a locally declared type
// — because the shapes that fail are open-ended: a built-in (`type Millis
// int64`), an imported type (`type Dur time.Duration`), and an unnamed
// composite (`type Handler func(int) int`, `type Bytes []byte`) all leave the
// receiver base type non-local, and Go rejects each with a different message
// against generated code.
func (t *galaASTTransformer) checkMethodReceiverAlias(recvCtx *grammar.ReceiverContext, recvTypeName string) error {
	if recvTypeName == "" {
		return nil
	}
	alias, isAlias := t.lookupTypeAlias(recvTypeName)
	if !isAlias || alias.IsNil() {
		return nil
	}

	// Aliases chain, and Go collapses the whole chain: `type A int64; type B A`
	// gives B the base type int64, not A. The rule applies to where the chain
	// ends, so the walk runs before the locality test.
	alias = t.followAliasChain(alias)
	reason, illegal := t.illegalReceiverTarget(alias)
	if !illegal {
		return nil
	}

	line, col := recvCtx.GetStart().GetLine(), recvCtx.GetStart().GetColumn()
	return galaerr.NewCodedSemanticError(
		galaerr.CodeMethodOnNonLocalAlias,
		line, col,
		fmt.Sprintf("cannot declare a method on %q: it resolves to %s", recvTypeName, reason),
		"a type alias is the same type as its target, so it takes no methods of its own — declare a struct that wraps the value, or write the method as a plain function",
	)
}

// illegalReceiverTarget reports the reason typ cannot serve as a method's
// receiver base type, or ok=false when it can.
//
// The cases mirror Go's own, each of which it reports differently:
//
//   - a built-in, an unnamed composite (slice, map, func) or a type from
//     another package — "cannot define new methods on non-local type X" /
//     "invalid receiver type X"
//   - an instantiated or generic type — "cannot define new methods on
//     instantiated type Pair[int]"
//
// A pointer target is unwrapped first: `type PP *Point` puts a method on
// Point, which Go allows when Point is local.
func (t *galaASTTransformer) illegalReceiverTarget(typ transpiler.Type) (string, bool) {
	for {
		ptr, isPtr := typ.(transpiler.PointerType)
		if !isPtr {
			break
		}
		typ = ptr.Elem
	}

	switch typ.(type) {
	case transpiler.GenericType:
		return fmt.Sprintf("%s, an instantiated type", typ.String()), true
	case transpiler.ArrayType, transpiler.MapType, transpiler.FuncType:
		return fmt.Sprintf("%s, which names no type of its own", typ.String()), true
	}

	if name := typ.BaseName(); name == "" || transpiler.IsPrimitiveType(name) {
		return fmt.Sprintf("the built-in type %s", typ.String()), true
	}
	if pkg := typ.GetPackage(); pkg != "" && pkg != t.packageName {
		return fmt.Sprintf("%s, declared in another package", typ.String()), true
	}
	return "", false
}
