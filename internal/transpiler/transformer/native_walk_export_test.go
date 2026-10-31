package transformer

import "martianoff/gala/internal/transpiler"

// OnNativeDecl sets tr's onNativeDecl hook for TestNativeWalkAgrees.
func OnNativeDecl(tr transpiler.ASTTransformer, f func()) {
	tr.(*galaASTTransformer).onNativeDecl = f
}

// OnNativeStmt sets tr's onNativeStmt hook for TestNativeWalkAgrees.
func OnNativeStmt(tr transpiler.ASTTransformer, f func(index int)) {
	tr.(*galaASTTransformer).onNativeStmt = f
}
