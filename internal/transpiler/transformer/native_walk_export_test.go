package transformer

import "martianoff/gala/internal/transpiler"

// OnNativeDecl sets tr's onNativeDecl hook for TestNativeWalkAgrees.
func OnNativeDecl(tr transpiler.ASTTransformer, f func()) {
	tr.(*galaASTTransformer).onNativeDecl = f
}
