package transformer

import "martianoff/gala/internal/transpiler"

// OnNativeDecl sets tr's onNativeDecl hook for TestNativeWalkAgrees.
func OnNativeDecl(tr transpiler.ASTTransformer, f func()) {
	tr.(*galaASTTransformer).onNativeDecl = f
}

// OnNativeStmt sets tr's onNativeStmt hook for TestNativeWalkAgrees.
func OnNativeStmt(tr transpiler.ASTTransformer, f func(index, line int)) {
	tr.(*galaASTTransformer).onNativeStmt = f
}

// OnNativeType sets tr's onNativeType hook for TestNativeTypeCases.
func OnNativeType(tr transpiler.ASTTransformer, f func(line int)) {
	tr.(*galaASTTransformer).onNativeType = f
}
