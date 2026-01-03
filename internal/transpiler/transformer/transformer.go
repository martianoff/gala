package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"

	"github.com/antlr4-go/antlr/v4"
)

type scope struct {
	vals   map[string]bool
	parent *scope
}

type galaASTTransformer struct {
	currentScope   *scope
	immutFields    map[string]bool
	needsStdImport bool
}

func (t *galaASTTransformer) pushScope() {
	t.currentScope = &scope{
		vals:   make(map[string]bool),
		parent: t.currentScope,
	}
}

func (t *galaASTTransformer) popScope() {
	if t.currentScope != nil {
		t.currentScope = t.currentScope.parent
	}
}

func (t *galaASTTransformer) addVal(name string) {
	if t.currentScope != nil {
		t.currentScope.vals[name] = true
	}
}

func (t *galaASTTransformer) addVar(name string) {
	if t.currentScope != nil {
		t.currentScope.vals[name] = false
	}
}

func (t *galaASTTransformer) isVal(name string) bool {
	s := t.currentScope
	for s != nil {
		if isImmutable, ok := s.vals[name]; ok {
			return isImmutable
		}
		s = s.parent
	}
	return false
}

// NewGalaASTTransformer creates a new instance of ASTTransformer for GALA.
func NewGalaASTTransformer() transpiler.ASTTransformer {
	return &galaASTTransformer{
		immutFields: make(map[string]bool),
	}
}

func (t *galaASTTransformer) Transform(tree antlr.Tree) (*token.FileSet, *ast.File, error) {
	t.currentScope = nil
	t.needsStdImport = false
	t.immutFields = make(map[string]bool)
	t.pushScope() // Global scope
	defer t.popScope()

	fset := token.NewFileSet()
	sourceFile, ok := tree.(*grammar.SourceFileContext)
	if !ok {
		return nil, nil, fmt.Errorf("expected *grammar.SourceFileContext, got %T", tree)
	}

	pkgName := sourceFile.PackageClause().(*grammar.PackageClauseContext).Identifier().GetText()
	file := &ast.File{
		Name: ast.NewIdent(pkgName),
	}

	// Imports
	for _, importCtx := range sourceFile.AllImportDeclaration() {
		decl, err := t.transformImportDeclaration(importCtx.(*grammar.ImportDeclarationContext))
		if err != nil {
			return nil, nil, err
		}
		file.Decls = append(file.Decls, decl)
	}

	for _, topDeclCtx := range sourceFile.AllTopLevelDeclaration() {
		decl, err := t.transformTopLevelDeclaration(topDeclCtx)
		if err != nil {
			return nil, nil, err
		}
		if decl != nil {
			file.Decls = append(file.Decls, decl)
		}
	}

	if t.needsStdImport {
		// Add import at the beginning
		importDecl := &ast.GenDecl{
			Tok: token.IMPORT,
			Specs: []ast.Spec{
				&ast.ImportSpec{
					Path: &ast.BasicLit{
						Kind:  token.STRING,
						Value: "\"martianoff/gala/std\"",
					},
				},
			},
		}
		file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
	}

	return fset, file, nil
}

func (t *galaASTTransformer) transformTopLevelDeclaration(ctx grammar.ITopLevelDeclarationContext) (ast.Decl, error) {
	if valCtx := ctx.ValDeclaration(); valCtx != nil {
		return t.transformValDeclaration(valCtx.(*grammar.ValDeclarationContext))
	}
	if varCtx := ctx.VarDeclaration(); varCtx != nil {
		return t.transformVarDeclaration(varCtx.(*grammar.VarDeclarationContext))
	}
	if funcCtx := ctx.FunctionDeclaration(); funcCtx != nil {
		return t.transformFunctionDeclaration(funcCtx.(*grammar.FunctionDeclarationContext))
	}
	if typeCtx := ctx.TypeDeclaration(); typeCtx != nil {
		return t.transformTypeDeclaration(typeCtx.(*grammar.TypeDeclarationContext))
	}
	return nil, nil
}

func (t *galaASTTransformer) transformDeclaration(ctx grammar.IDeclarationContext) (ast.Decl, ast.Stmt, error) {
	if valCtx := ctx.ValDeclaration(); valCtx != nil {
		decl, err := t.transformValDeclaration(valCtx.(*grammar.ValDeclarationContext))
		return decl, nil, err
	}
	if varCtx := ctx.VarDeclaration(); varCtx != nil {
		decl, err := t.transformVarDeclaration(varCtx.(*grammar.VarDeclarationContext))
		return decl, nil, err
	}
	if funcCtx := ctx.FunctionDeclaration(); funcCtx != nil {
		decl, err := t.transformFunctionDeclaration(funcCtx.(*grammar.FunctionDeclarationContext))
		return decl, nil, err
	}
	if typeCtx := ctx.TypeDeclaration(); typeCtx != nil {
		decl, err := t.transformTypeDeclaration(typeCtx.(*grammar.TypeDeclarationContext))
		return decl, nil, err
	}
	if importCtx := ctx.ImportDeclaration(); importCtx != nil {
		decl, err := t.transformImportDeclaration(importCtx.(*grammar.ImportDeclarationContext))
		return decl, nil, err
	}
	if ifCtx := ctx.IfStatement(); ifCtx != nil {
		stmt, err := t.transformIfStatement(ifCtx.(*grammar.IfStatementContext))
		return nil, stmt, err
	}
	if forCtx := ctx.ForStatement(); forCtx != nil {
		// TODO: implement
		return nil, nil, fmt.Errorf("for statement not implemented yet")
	}
	if exprCtx := ctx.ExpressionStatement(); exprCtx != nil {
		stmt, err := t.transformExpressionStatement(exprCtx.(*grammar.ExpressionStatementContext))
		return nil, stmt, err
	}
	return nil, nil, nil
}

func (t *galaASTTransformer) transformExpressionStatement(ctx *grammar.ExpressionStatementContext) (ast.Stmt, error) {
	expr, err := t.transformExpression(ctx.Expression())
	if err != nil {
		return nil, err
	}
	return &ast.ExprStmt{X: expr}, nil
}

func (t *galaASTTransformer) transformValDeclaration(ctx *grammar.ValDeclarationContext) (ast.Decl, error) {
	name := ctx.Identifier().GetText()
	expr, err := t.transformExpression(ctx.Expression())
	if err != nil {
		return nil, err
	}

	t.needsStdImport = true
	t.addVal(name)

	// Wrap value: std.NewImmutable(expr)
	wrappedValue := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   ast.NewIdent("std"),
			Sel: ast.NewIdent("NewImmutable"),
		},
		Args: []ast.Expr{expr},
	}

	spec := &ast.ValueSpec{
		Names:  []*ast.Ident{ast.NewIdent(name)},
		Values: []ast.Expr{wrappedValue},
	}

	if ctx.Type_() != nil {
		typeExpr, err := t.transformType(ctx.Type_())
		if err != nil {
			return nil, err
		}
		// Change type to std.Immutable[typeExpr]
		spec.Type = &ast.IndexExpr{
			X: &ast.SelectorExpr{
				X:   ast.NewIdent("std"),
				Sel: ast.NewIdent("Immutable"),
			},
			Index: typeExpr,
		}
	}

	return &ast.GenDecl{
		Tok:   token.VAR,
		Specs: []ast.Spec{spec},
	}, nil
}

func (t *galaASTTransformer) transformVarDeclaration(ctx *grammar.VarDeclarationContext) (ast.Decl, error) {
	name := ctx.Identifier().GetText()
	t.addVar(name)
	spec := &ast.ValueSpec{
		Names: []*ast.Ident{ast.NewIdent(name)},
	}
	if ctx.Expression() != nil {
		expr, err := t.transformExpression(ctx.Expression())
		if err != nil {
			return nil, err
		}
		spec.Values = []ast.Expr{expr}
	}
	if ctx.Type_() != nil {
		typeExpr, err := t.transformType(ctx.Type_())
		if err != nil {
			return nil, err
		}
		spec.Type = typeExpr
	}

	return &ast.GenDecl{
		Tok:   token.VAR,
		Specs: []ast.Spec{spec},
	}, nil
}

func (t *galaASTTransformer) transformFunctionDeclaration(ctx *grammar.FunctionDeclarationContext) (ast.Decl, error) {
	t.pushScope()
	defer t.popScope()
	name := ctx.Identifier().GetText()

	// Signature
	sigCtx := ctx.Signature().(*grammar.SignatureContext)
	paramsCtx := sigCtx.Parameters().(*grammar.ParametersContext)

	fieldList := &ast.FieldList{}
	if paramsCtx.ParameterList() != nil {
		for _, pCtx := range paramsCtx.ParameterList().(*grammar.ParameterListContext).AllParameter() {
			field, err := t.transformParameter(pCtx.(*grammar.ParameterContext))
			if err != nil {
				return nil, err
			}
			fieldList.List = append(fieldList.List, field)
		}
	}

	var results *ast.FieldList
	if sigCtx.Type_() != nil {
		retType, err := t.transformType(sigCtx.Type_())
		if err != nil {
			return nil, err
		}
		results = &ast.FieldList{
			List: []*ast.Field{{Type: retType}},
		}
	}

	funcType := &ast.FuncType{
		Params:  fieldList,
		Results: results,
	}

	// Generics
	if ctx.TypeParameters() != nil {
		// Go AST for generics uses TypeParams field in FuncDecl
		tParams, err := t.transformTypeParameters(ctx.TypeParameters().(*grammar.TypeParametersContext))
		if err != nil {
			return nil, err
		}
		funcType.TypeParams = tParams
	}

	var body *ast.BlockStmt
	if ctx.Block() != nil {
		b, err := t.transformBlock(ctx.Block().(*grammar.BlockContext))
		if err != nil {
			return nil, err
		}
		body = b
	} else if ctx.Expression() != nil {
		// func f(x int) int = x * x  =>  { return x * x }
		expr, err := t.transformExpression(ctx.Expression())
		if err != nil {
			return nil, err
		}
		body = &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{Results: []ast.Expr{expr}},
			},
		}
	}

	return &ast.FuncDecl{
		Name: ast.NewIdent(name),
		Type: funcType,
		Body: body,
	}, nil
}

func (t *galaASTTransformer) transformTypeDeclaration(ctx *grammar.TypeDeclarationContext) (ast.Decl, error) {
	name := ctx.Identifier().GetText()
	var spec ast.Spec

	if ctx.StructType() != nil {
		structCtx := ctx.StructType().(*grammar.StructTypeContext)
		fields := &ast.FieldList{}
		for _, fCtx := range structCtx.AllStructField() {
			field, err := t.transformStructField(fCtx.(*grammar.StructFieldContext))
			if err != nil {
				return nil, err
			}
			fields.List = append(fields.List, field)
		}
		typeSpec := &ast.TypeSpec{
			Name: ast.NewIdent(name),
			Type: &ast.StructType{Fields: fields},
		}
		if ctx.TypeParameters() != nil {
			tParams, err := t.transformTypeParameters(ctx.TypeParameters().(*grammar.TypeParametersContext))
			if err != nil {
				return nil, err
			}
			typeSpec.TypeParams = tParams
		}
		spec = typeSpec
	} else if ctx.InterfaceType() != nil {
		// TODO: implement
		return nil, fmt.Errorf("interface type not implemented yet")
	} else if ctx.TypeAlias() != nil {
		// TODO: implement
		return nil, fmt.Errorf("type alias not implemented yet")
	}

	return &ast.GenDecl{
		Tok:   token.TYPE,
		Specs: []ast.Spec{spec},
	}, nil
}

func (t *galaASTTransformer) transformImportDeclaration(ctx *grammar.ImportDeclarationContext) (ast.Decl, error) {
	// import "pkg"  or import ( "pkg1" "pkg2" )
	var specs []ast.Spec
	for _, s := range ctx.AllSTRING() {
		specs = append(specs, &ast.ImportSpec{
			Path: &ast.BasicLit{Kind: token.STRING, Value: s.GetText()},
		})
	}
	return &ast.GenDecl{
		Tok:   token.IMPORT,
		Specs: specs,
	}, nil
}

func (t *galaASTTransformer) transformParameter(ctx *grammar.ParameterContext) (*ast.Field, error) {
	name := ctx.Identifier().GetText()
	field := &ast.Field{
		Names: []*ast.Ident{ast.NewIdent(name)},
	}

	isVal := ctx.VAL() != nil
	if isVal {
		t.addVal(name)
	} else {
		t.addVar(name)
	}

	if ctx.Type_() != nil {
		typ, err := t.transformType(ctx.Type_())
		if err != nil {
			return nil, err
		}
		if isVal {
			t.needsStdImport = true
			field.Type = &ast.IndexExpr{
				X: &ast.SelectorExpr{
					X:   ast.NewIdent("std"),
					Sel: ast.NewIdent("Immutable"),
				},
				Index: typ,
			}
		} else {
			field.Type = typ
		}
	}
	return field, nil
}

func (t *galaASTTransformer) transformStructField(ctx *grammar.StructFieldContext) (*ast.Field, error) {
	name := ctx.Identifier().GetText()
	typ, err := t.transformType(ctx.Type_())
	if err != nil {
		return nil, err
	}

	isVal := ctx.VAL() != nil
	field := &ast.Field{
		Names: []*ast.Ident{ast.NewIdent(name)},
	}

	if isVal {
		t.needsStdImport = true
		t.immutFields[name] = true
		field.Type = &ast.IndexExpr{
			X: &ast.SelectorExpr{
				X:   ast.NewIdent("std"),
				Sel: ast.NewIdent("Immutable"),
			},
			Index: typ,
		}
	} else {
		field.Type = typ
	}

	if ctx.STRING() != nil {
		field.Tag = &ast.BasicLit{Kind: token.STRING, Value: ctx.STRING().GetText()}
	}
	return field, nil
}

func (t *galaASTTransformer) transformTypeParameters(ctx *grammar.TypeParametersContext) (*ast.FieldList, error) {
	list := &ast.FieldList{}
	for _, tpCtx := range ctx.TypeParameterList().(*grammar.TypeParameterListContext).AllTypeParameter() {
		tp := tpCtx.(*grammar.TypeParameterContext)
		list.List = append(list.List, &ast.Field{
			Names: []*ast.Ident{ast.NewIdent(tp.Identifier(0).GetText())},
			Type:  ast.NewIdent(tp.Identifier(1).GetText()),
		})
	}
	return list, nil
}

func (t *galaASTTransformer) transformBlock(ctx *grammar.BlockContext) (*ast.BlockStmt, error) {
	t.pushScope()
	defer t.popScope()
	block := &ast.BlockStmt{}
	for _, stmtCtx := range ctx.AllStatement() {
		stmt, err := t.transformStatement(stmtCtx.(*grammar.StatementContext))
		if err != nil {
			return nil, err
		}
		block.List = append(block.List, stmt)
	}
	return block, nil
}

func (t *galaASTTransformer) transformStatement(ctx *grammar.StatementContext) (ast.Stmt, error) {
	if declCtx := ctx.Declaration(); declCtx != nil {
		decl, stmt, err := t.transformDeclaration(declCtx)
		if err != nil {
			return nil, err
		}
		if stmt != nil {
			return stmt, nil
		}
		if decl != nil {
			return &ast.DeclStmt{Decl: decl}, nil
		}
		return nil, nil
	}
	if retCtx := ctx.ReturnStatement(); retCtx != nil {
		var results []ast.Expr
		if retCtx.Expression() != nil {
			expr, err := t.transformExpression(retCtx.Expression())
			if err != nil {
				return nil, err
			}
			results = append(results, expr)
		}
		return &ast.ReturnStmt{Results: results}, nil
	}
	return nil, nil
}

func (t *galaASTTransformer) transformExpression(ctx grammar.IExpressionContext) (ast.Expr, error) {
	if ctx == nil {
		return nil, nil
	}

	// expression: primary
	if p := ctx.Primary(); p != nil {
		return t.transformPrimary(p.(*grammar.PrimaryContext))
	}

	// expression: lambdaExpression
	if l := ctx.LambdaExpression(); l != nil {
		return t.transformLambda(l.(*grammar.LambdaExpressionContext))
	}

	// expression: ifExpression
	if i := ctx.IfExpression(); i != nil {
		return t.transformIfExpression(i.(*grammar.IfExpressionContext))
	}

	// expression: expression 'match' '{' caseClause+ '}'
	// We check if it's a match by checking the number of children and existence of MATCH token
	if ctx.GetChildCount() >= 4 {
		for i := 0; i < ctx.GetChildCount(); i++ {
			if ctx.GetChild(i).(antlr.ParseTree).GetText() == "match" {
				return t.transformMatchExpression(ctx)
			}
		}
	}

	// Handle recursive expression patterns
	// Since there are no labels, we check the number of children and the tokens
	childCount := ctx.GetChildCount()
	if childCount == 2 {
		child1 := ctx.GetChild(0)
		child2 := ctx.GetChild(1)

		if _, ok := child1.(*grammar.UnaryOpContext); ok {
			expr, err := t.transformExpression(child2.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			opText := child1.(antlr.ParseTree).GetText()
			if opText == "*" {
				return &ast.StarExpr{X: expr}, nil
			}
			return &ast.UnaryExpr{
				Op: t.getUnaryToken(opText),
				X:  expr,
			}, nil
		}
	}

	if childCount == 3 {
		child1 := ctx.GetChild(0)
		child2 := ctx.GetChild(1)
		child3 := ctx.GetChild(2)

		c2Text := child2.(antlr.ParseTree).GetText()

		if c2Text == "." {
			// expression '.' identifier
			x, err := t.transformExpression(child1.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			selName := child3.(antlr.ParseTree).GetText()
			selExpr := &ast.SelectorExpr{
				X:   x,
				Sel: ast.NewIdent(selName),
			}
			if t.immutFields[selName] {
				return &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   selExpr,
						Sel: ast.NewIdent("Get"),
					},
				}, nil
			}
			return selExpr, nil
		}

		if c2Text == "(" && child3.(antlr.ParseTree).GetText() == ")" {
			// expression '(' ')'
			x, err := t.transformExpression(child1.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			return &ast.CallExpr{Fun: x}, nil
		}

		// expression binaryOp expression
		// Note: child2 might be the binaryOp rule or a terminal.
		// In our grammar, binaryOp is a rule.
		if _, ok := child2.(*grammar.BinaryOpContext); ok {
			left, err := t.transformExpression(child1.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			right, err := t.transformExpression(child3.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			return &ast.BinaryExpr{
				X:  left,
				Op: t.getBinaryToken(c2Text),
				Y:  right,
			}, nil
		}
	}

	if childCount == 4 {
		child1 := ctx.GetChild(0)
		child2 := ctx.GetChild(1)
		// child3 is expressionList
		child4 := ctx.GetChild(3)

		c2Text := child2.(antlr.ParseTree).GetText()
		c4Text := child4.(antlr.ParseTree).GetText()

		if c2Text == "(" && c4Text == ")" {
			// expression '(' expressionList ')'
			x, err := t.transformExpression(child1.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			args, err := t.transformExpressionList(ctx.GetChild(2).(*grammar.ExpressionListContext))
			if err != nil {
				return nil, err
			}
			return &ast.CallExpr{Fun: x, Args: args}, nil
		}

		if c2Text == "[" && c4Text == "]" {
			// expression '[' expressionList ']'
			x, err := t.transformExpression(child1.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			indices, err := t.transformExpressionList(ctx.GetChild(2).(*grammar.ExpressionListContext))
			if err != nil {
				return nil, err
			}
			if len(indices) == 1 {
				return &ast.IndexExpr{X: x, Index: indices[0]}, nil
			} else {
				return &ast.IndexListExpr{X: x, Indices: indices}, nil
			}
		}
	}

	return nil, fmt.Errorf("expression transformation not fully implemented for %T: %s", ctx, ctx.GetText())
}

func (t *galaASTTransformer) transformExpressionList(ctx *grammar.ExpressionListContext) ([]ast.Expr, error) {
	var exprs []ast.Expr
	for _, eCtx := range ctx.AllExpression() {
		e, err := t.transformExpression(eCtx)
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, e)
	}
	return exprs, nil
}

func (t *galaASTTransformer) getBinaryToken(op string) token.Token {
	switch op {
	case "||":
		return token.LOR
	case "&&":
		return token.LAND
	case "==":
		return token.EQL
	case "!=":
		return token.NEQ
	case "<":
		return token.LSS
	case "<=":
		return token.LEQ
	case ">":
		return token.GTR
	case ">=":
		return token.GEQ
	case "+":
		return token.ADD
	case "-":
		return token.SUB
	case "|":
		return token.OR
	case "^":
		return token.XOR
	case "*":
		return token.MUL
	case "/":
		return token.QUO
	case "%":
		return token.REM
	case "<<":
		return token.SHL
	case ">>":
		return token.SHR
	case "&":
		return token.AND
	case "&^":
		return token.AND_NOT
	default:
		return token.ILLEGAL
	}
}

func (t *galaASTTransformer) getUnaryToken(op string) token.Token {
	switch op {
	case "+":
		return token.ADD
	case "-":
		return token.SUB
	case "!":
		return token.NOT
	case "^":
		return token.XOR
	case "&":
		return token.AND
	default:
		return token.ILLEGAL
	}
}

func (t *galaASTTransformer) transformPrimary(ctx *grammar.PrimaryContext) (ast.Expr, error) {
	if ctx.Identifier() != nil {
		name := ctx.Identifier().GetText()
		ident := ast.NewIdent(name)
		if t.isVal(name) {
			return &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X:   ident,
					Sel: ast.NewIdent("Get"),
				},
			}, nil
		}
		return ident, nil
	}
	if ctx.Literal() != nil {
		return t.transformLiteral(ctx.Literal().(*grammar.LiteralContext))
	}
	if ctx.Expression() != nil {
		return t.transformExpression(ctx.Expression())
	}
	return nil, nil
}

func (t *galaASTTransformer) transformLiteral(ctx *grammar.LiteralContext) (ast.Expr, error) {
	if ctx.INT_LIT() != nil {
		return &ast.BasicLit{Kind: token.INT, Value: ctx.INT_LIT().GetText()}, nil
	}
	if ctx.FLOAT_LIT() != nil {
		return &ast.BasicLit{Kind: token.FLOAT, Value: ctx.FLOAT_LIT().GetText()}, nil
	}
	if ctx.STRING() != nil {
		return &ast.BasicLit{Kind: token.STRING, Value: ctx.STRING().GetText()}, nil
	}
	if ctx.GetText() == "true" || ctx.GetText() == "false" {
		return ast.NewIdent(ctx.GetText()), nil
	}
	if ctx.GetText() == "nil" {
		return ast.NewIdent("nil"), nil
	}
	return nil, nil
}

func (t *galaASTTransformer) transformType(ctx grammar.ITypeContext) (ast.Expr, error) {
	if ctx == nil {
		return nil, nil
	}
	// Simplified type handling
	if ctx.Identifier() != nil {
		ident := ast.NewIdent(ctx.Identifier().GetText())
		if ctx.TypeArguments() != nil {
			// Generic type: T[A, B] -> *ast.IndexExpr or *ast.IndexListExpr
			args := ctx.TypeArguments().(*grammar.TypeArgumentsContext).TypeList().(*grammar.TypeListContext).AllType_()
			var argExprs []ast.Expr
			for _, arg := range args {
				ae, err := t.transformType(arg)
				if err != nil {
					return nil, err
				}
				argExprs = append(argExprs, ae)
			}
			if len(argExprs) == 1 {
				return &ast.IndexExpr{X: ident, Index: argExprs[0]}, nil
			} else {
				return &ast.IndexListExpr{X: ident, Indices: argExprs}, nil
			}
		}
		return ident, nil
	}
	if ctx.GetChildCount() > 0 && ctx.GetChild(0).(antlr.ParseTree).GetText() == "*" {
		typ, err := t.transformType(ctx.GetChild(1).(grammar.ITypeContext))
		if err != nil {
			return nil, err
		}
		return &ast.StarExpr{X: typ}, nil
	}
	return ast.NewIdent(ctx.GetText()), nil
}

func (t *galaASTTransformer) transformLambda(ctx *grammar.LambdaExpressionContext) (ast.Expr, error) {
	t.pushScope()
	defer t.popScope()
	paramsCtx := ctx.Parameters().(*grammar.ParametersContext)
	fieldList := &ast.FieldList{}
	if paramsCtx.ParameterList() != nil {
		for _, pCtx := range paramsCtx.ParameterList().(*grammar.ParameterListContext).AllParameter() {
			field, err := t.transformParameter(pCtx.(*grammar.ParameterContext))
			if err != nil {
				return nil, err
			}
			fieldList.List = append(fieldList.List, field)
		}
	}

	var body *ast.BlockStmt
	if ctx.Block() != nil {
		b, err := t.transformBlock(ctx.Block().(*grammar.BlockContext))
		if err != nil {
			return nil, err
		}
		body = b
	} else if ctx.Expression() != nil {
		expr, err := t.transformExpression(ctx.Expression())
		if err != nil {
			return nil, err
		}
		body = &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{Results: []ast.Expr{expr}},
			},
		}
	}

	return &ast.FuncLit{
		Type: &ast.FuncType{
			Params: fieldList,
		},
		Body: body,
	}, nil
}

func (t *galaASTTransformer) transformIfExpression(ctx *grammar.IfExpressionContext) (ast.Expr, error) {
	// 'if' '(' cond ')' thenExpr 'else' elseExpr
	cond, err := t.transformExpression(ctx.Expression(0))
	if err != nil {
		return nil, err
	}
	thenExpr, err := t.transformExpression(ctx.Expression(1))
	if err != nil {
		return nil, err
	}
	elseExpr, err := t.transformExpression(ctx.Expression(2))
	if err != nil {
		return nil, err
	}

	// Transpile to IIFE: func() any { if cond { return thenExpr }; return elseExpr }()
	return &ast.CallExpr{
		Fun: &ast.FuncLit{
			Type: &ast.FuncType{
				Params: &ast.FieldList{},
				Results: &ast.FieldList{
					List: []*ast.Field{{Type: ast.NewIdent("any")}},
				},
			},
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.IfStmt{
						Cond: cond,
						Body: &ast.BlockStmt{
							List: []ast.Stmt{
								&ast.ReturnStmt{Results: []ast.Expr{thenExpr}},
							},
						},
					},
					&ast.ReturnStmt{Results: []ast.Expr{elseExpr}},
				},
			},
		},
	}, nil
}

func (t *galaASTTransformer) transformIfStatement(ctx *grammar.IfStatementContext) (ast.Stmt, error) {
	cond, err := t.transformExpression(ctx.Expression())
	if err != nil {
		return nil, err
	}
	body, err := t.transformBlock(ctx.Block(0).(*grammar.BlockContext))
	if err != nil {
		return nil, err
	}
	stmt := &ast.IfStmt{
		Cond: cond,
		Body: body,
	}

	if ctx.SimpleStatement() != nil {
		init, err := t.transformSimpleStatement(ctx.SimpleStatement().(*grammar.SimpleStatementContext))
		if err != nil {
			return nil, err
		}
		stmt.Init = init
	}

	if ctx.ELSE() != nil {
		if ctx.Block(1) != nil {
			elseBody, err := t.transformBlock(ctx.Block(1).(*grammar.BlockContext))
			if err != nil {
				return nil, err
			}
			stmt.Else = elseBody
		} else if ctx.IfStatement() != nil {
			elseIf, err := t.transformIfStatement(ctx.IfStatement().(*grammar.IfStatementContext))
			if err != nil {
				return nil, err
			}
			stmt.Else = elseIf
		}
	}

	return stmt, nil
}

func (t *galaASTTransformer) transformSimpleStatement(ctx *grammar.SimpleStatementContext) (ast.Stmt, error) {
	if ctx.Expression() != nil {
		expr, err := t.transformExpression(ctx.Expression())
		if err != nil {
			return nil, err
		}
		return &ast.ExprStmt{X: expr}, nil
	}
	// TODO: assignment, shortVarDecl
	return nil, nil
}

func (t *galaASTTransformer) transformMatchExpression(ctx grammar.IExpressionContext) (ast.Expr, error) {
	// expression 'match' '{' caseClause+ '}'
	// Use children because it's not a distinct context type
	exprCtx := ctx.GetChild(0).(grammar.IExpressionContext)
	expr, err := t.transformExpression(exprCtx)
	if err != nil {
		return nil, err
	}

	paramName := "obj"
	if primary := exprCtx.Primary(); primary != nil {
		if p, ok := primary.(*grammar.PrimaryContext); ok && p.Identifier() != nil {
			paramName = p.Identifier().GetText()
		}
	}

	t.pushScope()
	defer t.popScope()
	t.addVar(paramName)

	var clauses []ast.Stmt
	var defaultBody []ast.Stmt
	foundDefault := false

	// case clauses start from child 3 (0: expr, 1: match, 2: {, 3: case...)
	for i := 3; i < ctx.GetChildCount()-1; i++ {
		ccCtx, ok := ctx.GetChild(i).(*grammar.CaseClauseContext)
		if !ok {
			continue
		}

		// Check if it's a default case
		patExprCtx := ccCtx.Expression(0)
		if patExprCtx.GetText() == "_" {
			if foundDefault {
				return nil, fmt.Errorf("multiple default cases in match")
			}
			foundDefault = true

			// Transform the body of default case
			if ccCtx.Block() != nil {
				b, err := t.transformBlock(ccCtx.Block().(*grammar.BlockContext))
				if err != nil {
					return nil, err
				}
				defaultBody = b.List
			} else if len(ccCtx.AllExpression()) > 1 {
				expr, err := t.transformExpression(ccCtx.Expression(1))
				if err != nil {
					return nil, err
				}
				defaultBody = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{expr}}}
			}
			continue
		}

		clause, err := t.transformCaseClause(ccCtx, paramName)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, clause)
	}

	if !foundDefault {
		return nil, fmt.Errorf("match expression must have a default case (case _ => ...)")
	}

	t.needsStdImport = true
	// Transpile to IIFE: func(obj any) any { ... }(expr)
	return &ast.CallExpr{
		Fun: &ast.FuncLit{
			Type: &ast.FuncType{
				Params: &ast.FieldList{
					List: []*ast.Field{
						{
							Names: []*ast.Ident{ast.NewIdent(paramName)},
							Type:  ast.NewIdent("any"),
						},
					},
				},
				Results: &ast.FieldList{
					List: []*ast.Field{{Type: ast.NewIdent("any")}},
				},
			},
			Body: &ast.BlockStmt{
				List: append([]ast.Stmt{
					&ast.SwitchStmt{
						Body: &ast.BlockStmt{
							List: clauses,
						},
					},
				}, defaultBody...),
			},
		},
		Args: []ast.Expr{expr},
	}, nil
}

func (t *galaASTTransformer) transformCaseClause(ctx *grammar.CaseClauseContext, paramName string) (ast.Stmt, error) {
	// 'case' expression '=>' (expression | block)
	patExpr, err := t.transformExpression(ctx.Expression(0))
	if err != nil {
		return nil, err
	}

	var body []ast.Stmt
	if ctx.Block() != nil {
		b, err := t.transformBlock(ctx.Block().(*grammar.BlockContext))
		if err != nil {
			return nil, err
		}
		body = b.List
	} else if len(ctx.AllExpression()) > 1 {
		expr, err := t.transformExpression(ctx.Expression(1))
		if err != nil {
			return nil, err
		}
		body = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{expr}}}
	}

	if ident, ok := patExpr.(*ast.Ident); ok && ident.Name == "_" {
		return &ast.CaseClause{
			List: nil,
			Body: body,
		}, nil
	}

	// Use std.UnapplyCheck(paramName, patExpr)
	check := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   ast.NewIdent("std"),
			Sel: ast.NewIdent("UnapplyCheck"),
		},
		Args: []ast.Expr{
			ast.NewIdent(paramName),
			patExpr,
		},
	}

	return &ast.CaseClause{
		List: []ast.Expr{check},
		Body: body,
	}, nil
}

var _ transpiler.ASTTransformer = (*galaASTTransformer)(nil)
