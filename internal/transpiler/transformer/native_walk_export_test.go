package transformer

import "martianoff/gala/internal/transpiler"

// SetWalkHooks sets tr's walk test hooks (see galaASTTransformer.antlrWalkOnly
// and onNativeDecl) for TestNativeWalkAgrees.
func SetWalkHooks(tr transpiler.ASTTransformer, antlrOnly bool, onNativeDecl func()) {
	t := tr.(*galaASTTransformer)
	t.antlrWalkOnly, t.onNativeDecl = antlrOnly, onNativeDecl
}
