// Package coreexport writes GALA source as Core JSON: the surface syntax of
// the formal semantics' supported subset, with source positions, for the Lean
// reference interpreter to read (see docs/FORMAL_SEMANTICS.MD and SCHEMA.md).
//
// The exporter is a syntax dump. It resolves no names, fills in no default
// arguments, reorders no named arguments and desugars no match: those are the
// transpiler steps the spec checks, so the spec does each of them itself. A
// construct outside the subset becomes an Unsupported node in its place,
// never an error, so a coverage report can count it.
//
// It is a test-support library: no part of the gala CLI links it.
package coreexport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
)

// Source is a file to export: its path, as the document reports it, and its
// text.
type Source struct {
	Path string
	Text string
}

// LiteralTyper is the analyzer's type for an integer literal; false for none.
// It sees the literal alone, not its context, so it is the type an untyped
// constant takes by default (see SCHEMA.md, "Type hints").
type LiteralTyper func(native.LiteralNode) (string, bool)

// Options configure an export.
type Options struct {
	// LiteralType, when set, gives each integer literal a Hint.
	LiteralType LiteralTyper
}

// Export parses each source and exports the files as one document. A file
// with syntax errors is an error: the exporter dumps well-formed programs only.
func Export(srcs []Source, opts Options) (*Program, error) {
	p := &Program{Core: Version, Files: make([]*File, 0, len(srcs))}
	gp := parser.NewAntlrGalaParser()
	for _, src := range srcs {
		parsed := gp.ParseFile(src.Text)
		if err := parsed.Err(); err != nil {
			return nil, fmt.Errorf("coreexport: %s: %w", src.Path, err)
		}
		tree, err := parsed.CleanNative().Tree()
		if err != nil {
			return nil, fmt.Errorf("coreexport: %s: %w", src.Path, err)
		}
		p.Files = append(p.Files, ExportTree(src.Path, tree, opts))
	}
	return p, nil
}

// ExportTree exports one file's native tree.
func ExportTree(path string, tree *native.GalaTree, opts Options) *File {
	e := &exporter{tree: tree, opts: opts}
	return e.file(path, native.SourceFileNode{GalaNodeRef: tree.Root()})
}

// Marshal is p as indented JSON, the same bytes for the same document.
func Marshal(p *Program) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// exporter exports one file's tree.
type exporter struct {
	tree *native.GalaTree
	opts Options
}

// metaNode is a supported node, whose Meta the exporter fills in.
type metaNode interface {
	Node
	setMeta(Meta)
}

// at is n with its kind and the position of the tokens first..last.
func at[N metaNode](e *exporter, n N, first, last int) N {
	n.setMeta(Meta{Kind: n.Kind(), Pos: e.span(first, last)})
	return n
}

// of is n placed at node ref.
func of[N metaNode](e *exporter, n N, ref native.GalaNodeRef) N {
	return at(e, n, ref.StartIndex(), ref.StopIndex())
}

// unsupported is the construct what, in place of ref.
func (e *exporter) unsupported(what string, ref native.GalaNodeRef) Node {
	return &Unsupported{What: what, Pos: e.span(ref.StartIndex(), ref.StopIndex())}
}

// span is the position of tokens first..last; an empty span (last < first)
// sits at the start of token first.
func (e *exporter) span(first, last int) Pos {
	s := e.tree.Token(first)
	end := s.Start
	if last >= first {
		end = e.tree.Token(last).End
	}
	return Pos{Line: int(s.Line), Col: int(s.Column) + 1, Off: int(s.Start), End: int(end)}
}

// mapNodes is f of each of xs: an empty list (never null) for none.
func mapNodes[T any](xs []T, f func(T) Node) []Node {
	out := make([]Node, 0, len(xs))
	for _, x := range xs {
		out = append(out, f(x))
	}
	return out
}

// firstToken is the text of ref's first child token, "" for none.
func firstToken(ref native.GalaNodeRef) string {
	for i := range ref.ChildCount() {
		if c := ref.Child(i); c.IsToken() {
			return c.Terminal().GetText()
		}
	}
	return ""
}

// lastToken is the text of ref's last child token, "" for none.
func lastToken(ref native.GalaNodeRef) string {
	for i := ref.ChildCount() - 1; i >= 0; i-- {
		if c := ref.Child(i); c.IsToken() {
			return c.Terminal().GetText()
		}
	}
	return ""
}

// --- Declarations ---

func (e *exporter) file(path string, sf native.SourceFileNode) *File {
	imports := mapNodes(sf.AllImportDeclaration(), func(d native.ImportDeclarationNode) Node {
		return e.unsupported(UnsupportedImport, d.GalaNodeRef)
	})
	return of(e, &File{Path: path, Package: sf.PackageClause().Identifier().GetText(),
		Decls: append(imports, mapNodes(sf.AllTopLevelDeclaration(), e.topLevel)...)}, sf.GalaNodeRef)
}

func (e *exporter) topLevel(d native.TopLevelDeclarationNode) Node {
	switch {
	case d.EmbedDeclaration().Ok():
		return e.unsupported(UnsupportedEmbed, d.GalaNodeRef)
	case d.ValDeclaration().Ok():
		return e.val(d.ValDeclaration())
	case d.VarDeclaration().Ok():
		return e.varDecl(d.VarDeclaration())
	case d.FunctionDeclaration().Ok():
		return e.function(d.FunctionDeclaration())
	case d.TypeDeclaration().Ok():
		return e.typeDecl(d.TypeDeclaration())
	case d.StructShorthandDeclaration().Ok():
		return e.structShorthand(d.StructShorthandDeclaration())
	case d.SealedTypeDeclaration().Ok():
		return e.sealed(d.SealedTypeDeclaration())
	default: // opaque type, the only alternative left
		return e.unsupported(UnsupportedOpaqueType, d.GalaNodeRef)
	}
}

// binding is the names a val or var binds and whether it destructures.
func binding(tp native.TuplePatternNode, ids native.IdentifierListNode) ([]string, bool) {
	if tp.Ok() {
		ids = tp.IdentifierList()
	}
	names := []string{}
	for _, id := range ids.AllIdentifier() {
		names = append(names, id.GetText())
	}
	return names, tp.Ok()
}

func (e *exporter) val(v native.ValDeclarationNode) Node {
	names, destructure := binding(v.TuplePattern(), v.IdentifierList())
	return of(e, &Val{Names: names, Destructure: destructure, Type: e.optType(v.Type_()),
		Values: e.exprs(v.ExpressionList())}, v.GalaNodeRef)
}

func (e *exporter) varDecl(v native.VarDeclarationNode) Node {
	names, destructure := binding(v.TuplePattern(), v.IdentifierList())
	return of(e, &Var{Names: names, Destructure: destructure, Type: e.optType(v.Type_()),
		Values: e.exprs(v.ExpressionList())}, v.GalaNodeRef)
}

func (e *exporter) function(f native.FunctionDeclarationNode) Node {
	switch {
	case f.Receiver().Ok():
		return e.unsupported(UnsupportedMethod, f.GalaNodeRef)
	case f.TypeParameters().Ok():
		return e.unsupported(UnsupportedGenericDecl, f.GalaNodeRef)
	}
	sig := f.Signature()
	fn := of(e, &Func{Name: f.Identifier().GetText(),
		Params: mapNodes(sig.Parameters().ParameterList().AllParameter(), e.param)}, f.GalaNodeRef)
	switch {
	case sig.GoResults().Ok():
		fn.Result = e.unsupported(UnsupportedGoResults, sig.GoResults().GalaNodeRef)
	case sig.Type_().Ok():
		fn.Result = e.typ(sig.Type_())
	}
	if f.Block().Ok() {
		fn.Body = e.block(f.Block())
	} else {
		fn.Body = e.expr(f.Expression().GalaNodeRef)
	}
	return fn
}

// unsupportedParam is what makes p, a function parameter or a shorthand
// struct field, unsupported; nil when nothing does. A `val` or `var` mark is
// the caller's to check: it is unsupported on a parameter only.
func (e *exporter) unsupportedParam(p native.ParameterNode) Node {
	switch {
	case p.ELLIPSIS().Ok():
		return e.unsupported(UnsupportedVariadicParam, p.GalaNodeRef)
	case !p.Identifier().Ok():
		return e.unsupported(UnsupportedUnnamedParam, p.GalaNodeRef)
	case p.ParamDefault().Ok():
		return e.unsupported(UnsupportedParamDefault, p.GalaNodeRef)
	}
	return nil
}

func (e *exporter) param(p native.ParameterNode) Node {
	if p.VAL().Ok() || p.VAR().Ok() {
		return e.unsupported(UnsupportedParamModifier, p.GalaNodeRef)
	}
	if bad := e.unsupportedParam(p); bad != nil {
		return bad
	}
	return of(e, &Param{Name: p.Identifier().GetText(), Type: e.optType(p.Type_())}, p.GalaNodeRef)
}

func (e *exporter) typeDecl(t native.TypeDeclarationNode) Node {
	switch {
	case t.TypeParameters().Ok():
		return e.unsupported(UnsupportedGenericDecl, t.GalaNodeRef)
	case t.InterfaceType().Ok():
		return e.unsupported(UnsupportedInterface, t.GalaNodeRef)
	case t.TypeAlias().Ok():
		return e.unsupported(UnsupportedTypeAlias, t.GalaNodeRef)
	}
	fields := mapNodes(t.StructType().AllStructField(), func(f native.StructFieldNode) Node {
		return of(e, &Field{Name: f.Identifier().GetText(), Type: e.typ(f.Type_()), Mutable: f.VAR().Ok()},
			f.GalaNodeRef)
	})
	return of(e, &Struct{Name: t.Identifier().GetText(), Fields: fields}, t.GalaNodeRef)
}

func (e *exporter) structShorthand(d native.StructShorthandDeclarationNode) Node {
	if d.TypeParameters().Ok() {
		return e.unsupported(UnsupportedGenericDecl, d.GalaNodeRef)
	}
	return of(e, &Struct{Name: d.Identifier().GetText(), Shorthand: true,
		Fields: mapNodes(d.Parameters().ParameterList().AllParameter(), e.shorthandField)}, d.GalaNodeRef)
}

func (e *exporter) shorthandField(p native.ParameterNode) Node {
	if bad := e.unsupportedParam(p); bad != nil {
		return bad
	}
	return of(e, &Field{Name: p.Identifier().GetText(), Type: e.optType(p.Type_()), Mutable: p.VAR().Ok()},
		p.GalaNodeRef)
}

func (e *exporter) sealed(d native.SealedTypeDeclarationNode) Node {
	if d.TypeParameters().Ok() {
		return e.unsupported(UnsupportedGenericDecl, d.GalaNodeRef)
	}
	return of(e, &Sealed{Name: d.Identifier().GetText(), Variants: mapNodes(d.AllSealedCase(), e.variant)},
		d.GalaNodeRef)
}

func (e *exporter) variant(c native.SealedCaseNode) Node {
	fields := mapNodes(c.SealedCaseFieldList().AllSealedCaseField(), func(f native.SealedCaseFieldNode) Node {
		if f.ParamDefault().Ok() {
			return e.unsupported(UnsupportedParamDefault, f.GalaNodeRef)
		}
		return of(e, &Field{Name: f.Identifier().GetText(), Type: e.typ(f.Type_())}, f.GalaNodeRef)
	})
	return of(e, &Variant{Name: c.Identifier().GetText(), Default: c.DEFAULT().Ok(), Fields: fields},
		c.GalaNodeRef)
}

// --- Types ---

// optType is t, or nil (JSON null) when there is none.
func (e *exporter) optType(t native.TypeNode) Node {
	if !t.Ok() {
		return nil
	}
	return e.typ(t)
}

func (e *exporter) typ(t native.TypeNode) Node {
	if q := t.QualifiedIdentifier(); q.Ok() {
		switch {
		case t.TypeArguments().Ok():
			return e.unsupported(UnsupportedGenericType, t.GalaNodeRef)
		case q.Identifier(1).Ok():
			return e.unsupported(UnsupportedQualifiedType, t.GalaNodeRef)
		}
		return of(e, &TypeName{Name: q.GetText()}, t.GalaNodeRef)
	}
	if t.FUNC().Ok() {
		return e.unsupported(UnsupportedFuncType, t.GalaNodeRef)
	}
	switch firstToken(t.GalaNodeRef) {
	case "[":
		return e.unsupported(UnsupportedSliceType, t.GalaNodeRef)
	case "map":
		return e.unsupported(UnsupportedMapType, t.GalaNodeRef)
	case "*": // the newline form of '*' too: its text is the same
		return e.unsupported(UnsupportedPointerType, t.GalaNodeRef)
	default: // a form of type added to the grammar later
		return e.unsupported(UnsupportedOtherType, t.GalaNodeRef)
	}
}

// --- Statements ---

func (e *exporter) block(b native.BlockNode) Node {
	return of(e, &Block{Stmts: mapNodes(b.AllStatement(), e.statement)}, b.GalaNodeRef)
}

func (e *exporter) statement(s native.StatementNode) Node {
	if r := s.ReturnStatement(); r.Ok() {
		ret := of(e, &Return{}, r.GalaNodeRef)
		if r.Expression().Ok() {
			ret.Value = e.expr(r.Expression().GalaNodeRef)
		}
		return ret
	}
	d := s.Declaration()
	switch {
	case d.ValDeclaration().Ok():
		return e.val(d.ValDeclaration())
	case d.VarDeclaration().Ok():
		return e.varDecl(d.VarDeclaration())
	case d.BindDeclaration().Ok():
		return e.unsupported(UnsupportedBind, d.GalaNodeRef)
	case d.AlsoDeclaration().Ok():
		return e.unsupported(UnsupportedAlso, d.GalaNodeRef)
	case d.UseDeclaration().Ok():
		return e.unsupported(UnsupportedUse, d.GalaNodeRef)
	case d.FunctionDeclaration().Ok():
		return e.function(d.FunctionDeclaration())
	case d.TypeDeclaration().Ok():
		return e.typeDecl(d.TypeDeclaration())
	case d.ImportDeclaration().Ok():
		return e.unsupported(UnsupportedImport, d.GalaNodeRef)
	case d.IfStatement().Ok():
		return e.ifStatement(d.IfStatement())
	case d.ForStatement().Ok():
		return e.unsupported(UnsupportedFor, d.GalaNodeRef)
	default:
		return e.simple(d.SimpleStatement())
	}
}

func (e *exporter) ifStatement(s native.IfStatementNode) Node {
	if s.SimpleStatement().Ok() {
		return e.unsupported(UnsupportedIfInit, s.GalaNodeRef)
	}
	out := of(e, &If{Cond: e.expr(s.Expression().GalaNodeRef), Then: e.block(s.Block(0))}, s.GalaNodeRef)
	switch {
	case s.Block(1).Ok():
		out.Else = e.block(s.Block(1))
	case s.IfStatement().Ok():
		out.Else = e.ifStatement(s.IfStatement())
	}
	return out
}

func (e *exporter) simple(s native.SimpleStatementNode) Node {
	switch {
	case s.IncDecStmt().Ok():
		st := s.IncDecStmt()
		return of(e, &IncDec{Op: lastToken(st.GalaNodeRef), Target: e.expr(st.Expression().GalaNodeRef)},
			st.GalaNodeRef)
	case s.Assignment().Ok():
		a := s.Assignment()
		return of(e, &Assign{Op: firstToken(a.GalaNodeRef), Targets: e.exprs(a.ExpressionList(0)),
			Values: e.exprs(a.ExpressionList(1))}, a.GalaNodeRef)
	case s.ShortVarDecl().Ok():
		return e.unsupported(UnsupportedShortVarDecl, s.GalaNodeRef)
	default:
		return e.expr(s.Expression().GalaNodeRef)
	}
}

// --- Expressions ---

// exprs is the expressions of l, none when there is no list.
func (e *exporter) exprs(l native.ExpressionListNode) []Node {
	return mapNodes(l.AllExpression(), e.expression)
}

// expression is e.expr of x's node.
func (e *exporter) expression(x native.ExpressionNode) Node {
	return e.expr(x.GalaNodeRef)
}

// expr exports an expression from any of the expression rules.
func (e *exporter) expr(ref native.GalaNodeRef) Node {
	switch ref.Kind() {
	case native.GalaKIND_expression:
		return e.expr(native.ExpressionNode{GalaNodeRef: ref}.OrExpr().GalaNodeRef)
	case native.GalaKIND_unaryExpr:
		return e.unary(native.UnaryExprNode{GalaNodeRef: ref})
	case native.GalaKIND_postfixExpr:
		return e.postfix(native.PostfixExprNode{GalaNodeRef: ref})
	default: // orExpr .. multiplicativeExpr: operands with operators between
		return e.chain(ref)
	}
}

// chain folds `a op b op c` left to right into nested Binary nodes.
func (e *exporter) chain(ref native.GalaNodeRef) Node {
	var left Node
	first, op := -1, ""
	for i := range ref.ChildCount() {
		c := ref.Child(i)
		if c.IsToken() {
			op = c.Terminal().GetText()
			continue
		}
		operand := c.Node()
		right := e.expr(operand)
		if left == nil {
			left, first = right, operand.StartIndex()
			continue
		}
		left = at(e, &Binary{Op: op, Left: left, Right: right}, first, operand.StopIndex())
	}
	return left
}

func (e *exporter) unary(u native.UnaryExprNode) Node {
	if !u.UnaryOp().Ok() {
		return e.expr(u.PostfixExpr().GalaNodeRef)
	}
	switch op := u.UnaryOp().GetText(); op {
	case "-", "+", "!", "^":
		return of(e, &Unary{Op: op, Operand: e.expr(u.UnaryExpr().GalaNodeRef)}, u.GalaNodeRef)
	case "*":
		return e.unsupported(UnsupportedDeref, u.GalaNodeRef)
	case "&":
		return e.unsupported(UnsupportedAddressOf, u.GalaNodeRef)
	default: // "<-"
		return e.unsupported(UnsupportedReceive, u.GalaNodeRef)
	}
}

func (e *exporter) postfix(p native.PostfixExprNode) Node {
	first := p.StartIndex()
	cur := e.primaryExpr(p.PrimaryExpr())
	for _, s := range p.AllPostfixSuffix() {
		switch {
		case s.Identifier().Ok():
			cur = at(e, &Select{Target: cur, Name: s.Identifier().GetText()}, first, s.StopIndex())
		case s.ExpressionList().Ok():
			cur = &Unsupported{What: UnsupportedIndex, Pos: e.span(first, s.StopIndex())}
		default:
			cur = at(e, &Call{Callee: cur, Args: mapNodes(s.ArgumentList().AllArgument(), e.arg)},
				first, s.StopIndex())
		}
	}
	if !p.MATCH().Ok() {
		return cur
	}
	return of(e, &Match{Subject: cur, Cases: mapNodes(p.AllCaseClause(), e.caseClause)}, p.GalaNodeRef)
}

// unsupportedArg is what makes a, a call argument or an extractor's
// sub-pattern, unsupported; nil when nothing does, and a is a pattern.
func (e *exporter) unsupportedArg(a native.ArgumentNode) Node {
	switch {
	case a.Identifier().Ok():
		return e.unsupported(UnsupportedNamedArg, a.GalaNodeRef)
	case a.LambdaExpression().Ok():
		return e.unsupported(UnsupportedLambda, a.GalaNodeRef)
	}
	return nil
}

func (e *exporter) arg(a native.ArgumentNode) Node {
	if bad := e.unsupportedArg(a); bad != nil {
		return bad
	}
	p := a.Pattern()
	if ep, ok := p.AsExpressionPattern(); ok {
		return e.expr(ep.Expression().GalaNodeRef)
	}
	if _, ok := p.AsRestPattern(); ok {
		return e.unsupported(UnsupportedSpreadArg, a.GalaNodeRef)
	}
	return e.unsupported(UnsupportedTypedPattern, a.GalaNodeRef)
}

func (e *exporter) primaryExpr(p native.PrimaryExprNode) Node {
	switch {
	case p.Primary().Ok():
		return e.primary(p.Primary())
	case p.IfExpression().Ok():
		return e.ifExpr(p.IfExpression())
	case p.LambdaExpression().Ok():
		return e.unsupported(UnsupportedLambda, p.GalaNodeRef)
	default:
		return e.unsupported(UnsupportedPartialFunction, p.GalaNodeRef)
	}
}

func (e *exporter) primary(p native.PrimaryNode) Node {
	switch {
	case p.Identifier().Ok():
		return of(e, &Name{Name: p.Identifier().GetText()}, p.GalaNodeRef)
	case p.Literal().Ok():
		return e.literal(p.Literal())
	case p.CompositeLiteral().Ok():
		return e.unsupported(UnsupportedCompositeLiteral, p.GalaNodeRef)
	}
	elems, bad := e.parenthesised(p)
	switch {
	case bad != nil:
		return bad
	case len(elems) == 1:
		return of(e, &Paren{Expr: e.expression(elems[0])}, p.GalaNodeRef)
	}
	return of(e, &Tuple{Elems: mapNodes(elems, e.expression)}, p.GalaNodeRef)
}

// parenthesised is the expressions of p, a parenthesised list: one for
// `(a)`, more for a tuple. `()` and `(a,)` are unsupported instead.
func (e *exporter) parenthesised(p native.PrimaryNode) ([]native.ExpressionNode, Node) {
	l := p.TupleExpressionList()
	elems := l.AllExpression()
	switch {
	case len(elems) == 0:
		return nil, e.unsupported(UnsupportedUnit, p.GalaNodeRef)
	case len(elems) == 1 && l.ChildCount() > 1: // `(a,)`
		return nil, e.unsupported(UnsupportedOneTuple, p.GalaNodeRef)
	}
	return elems, nil
}

func (e *exporter) literal(l native.LiteralNode) Node {
	switch {
	case l.INT_LIT().Ok():
		n := of(e, &Int{Text: l.GetText()}, l.GalaNodeRef)
		if e.opts.LiteralType != nil {
			if typ, ok := e.opts.LiteralType(l); ok {
				n.Hint = &Hint{Type: typ}
			}
		}
		return n
	case l.STRING().Ok():
		text := l.GetText()
		value, err := strconv.Unquote(text)
		if err != nil {
			// The lexer accepts any backslash pair; Go's escapes are the
			// meaning a string has once transpiled, so one Go rejects has
			// none to export.
			return e.unsupported(UnsupportedStringEscape, l.GalaNodeRef)
		}
		return of(e, &String{Text: text, Value: value}, l.GalaNodeRef)
	case l.RAW_STRING().Ok():
		text := l.GetText()
		return of(e, &String{Text: text, Value: text[1 : len(text)-1], Raw: true}, l.GalaNodeRef)
	case l.FLOAT_LIT().Ok():
		return e.unsupported(UnsupportedFloatLiteral, l.GalaNodeRef)
	case l.CHAR_LIT().Ok():
		return e.unsupported(UnsupportedCharLiteral, l.GalaNodeRef)
	case l.INTERPOLATED_STRING().Ok():
		return e.unsupported(UnsupportedInterpolated, l.GalaNodeRef)
	case l.FORMAT_STRING().Ok():
		return e.unsupported(UnsupportedFormatString, l.GalaNodeRef)
	}
	switch l.GetText() {
	case "true":
		return of(e, &Bool{Value: true}, l.GalaNodeRef)
	case "false":
		return of(e, &Bool{Value: false}, l.GalaNodeRef)
	default:
		return e.unsupported(UnsupportedNil, l.GalaNodeRef)
	}
}

func (e *exporter) ifExpr(x native.IfExpressionNode) Node {
	return of(e, &IfExpr{Cond: e.expr(x.Expression().GalaNodeRef), Then: e.branch(x.IfExprBranch(0)),
		Else: e.branch(x.IfExprBranch(1))}, x.GalaNodeRef)
}

func (e *exporter) branch(b native.IfExprBranchNode) Node {
	if b.Block().Ok() {
		return e.block(b.Block())
	}
	return e.expr(b.Expression().GalaNodeRef)
}

func (e *exporter) caseClause(c native.CaseClauseNode) Node {
	out := of(e, &Case{Pattern: e.pattern(c.Pattern())}, c.GalaNodeRef)
	if g := c.GetGuard(); g.Ok() {
		out.Guard = e.expr(g.GalaNodeRef)
	}
	if b := c.GetBodyBlock(); b.Ok() {
		out.Body = e.block(b)
	} else {
		out.Body = e.simple(c.GetBodyStmt())
	}
	return out
}
