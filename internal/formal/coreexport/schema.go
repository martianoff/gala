package coreexport

import "slices"

// This file is the Core JSON schema as Go types. SCHEMA.md documents the same
// node kinds and fields; schema_test.go checks that the two agree.

// Version is the schema version, the "core" field of every document. A
// consumer rejects a version it does not know.
const Version = 1

// Program is a Core JSON document: one or more source files.
type Program struct {
	Core  int     `json:"core"`
	Files []*File `json:"files"`
}

// Pos is where a node is in its file: the line (from 1) and column (from 1,
// in code points) of its first token, and the byte offsets of the start of
// its first token and the end of its last one, so Off:End slices the node's
// source text out of the file.
type Pos struct {
	Line int `json:"line"`
	Col  int `json:"col"`
	Off  int `json:"off"`
	End  int `json:"end"`
}

// Node is a node of Core JSON: one of the kinds in nodeTypes, or an
// Unsupported construct.
type Node interface {
	// Kind is the node's "kind" field ("" for Unsupported).
	Kind() string
}

// Meta is the kind and position every supported node starts with.
type Meta struct {
	Kind string `json:"kind"`
	Pos  Pos    `json:"pos"`
}

func (m *Meta) setMeta(v Meta) { *m = v }

// Unsupported stands for a construct outside the subset, in place of the
// node it would be. What names the construct (see UnsupportedKinds()).
type Unsupported struct {
	What string `json:"unsupported"`
	Pos  Pos    `json:"pos"`
}

// Hint is a type the analyzer inferred for a node whose meaning depends on
// it. The spec checks it against its own rules; it is never the only source
// of a node's meaning.
type Hint struct {
	Type string `json:"type"`
}

// --- Declarations ---

// File is one source file: its package name and its declarations, imports
// first, in source order.
type File struct {
	Meta
	Path    string `json:"path"`
	Package string `json:"package"`
	Decls   []Node `json:"decls"`
}

// Val is `val names [type] = values`. Destructure is true for the tuple form
// `val (a, b) = ...`.
type Val struct {
	Meta
	Names       []string `json:"names"`
	Destructure bool     `json:"destructure"`
	Type        Node     `json:"type"`
	Values      []Node   `json:"values"`
}

// Var is `var names [type] [= values]`; Values is empty without `=`.
type Var struct {
	Meta
	Names       []string `json:"names"`
	Destructure bool     `json:"destructure"`
	Type        Node     `json:"type"`
	Values      []Node   `json:"values"`
}

// Func is a function declaration. Body is a block, or an expression for the
// `= expr` form. Result is null when the signature declares none.
type Func struct {
	Meta
	Name   string `json:"name"`
	Params []Node `json:"params"`
	Result Node   `json:"result"`
	Body   Node   `json:"body"`
}

// Param is a function parameter. Type is null when it is left out.
type Param struct {
	Meta
	Name string `json:"name"`
	Type Node   `json:"type"`
}

// Struct is `type Name struct { fields }`, or with Shorthand
// `struct Name(fields)`.
type Struct struct {
	Meta
	Name      string `json:"name"`
	Shorthand bool   `json:"shorthand"`
	Fields    []Node `json:"fields"`
}

// Field is a field of a struct or of a sealed variant. Mutable is true for a
// `var` field.
type Field struct {
	Meta
	Name    string `json:"name"`
	Type    Node   `json:"type"`
	Mutable bool   `json:"mutable"`
}

// Sealed is `sealed type Name { variants }`.
type Sealed struct {
	Meta
	Name     string `json:"name"`
	Variants []Node `json:"variants"`
}

// Variant is one `case` of a sealed type. Default is true for `default case`.
type Variant struct {
	Meta
	Name    string `json:"name"`
	Default bool   `json:"default"`
	Fields  []Node `json:"fields"`
}

// TypeName is a type written as a plain name, such as `int` or `Point`.
type TypeName struct {
	Meta
	Name string `json:"name"`
}

// --- Statements ---

// Block is `{ stmts }`. A statement is a declaration, a statement node or an
// expression.
type Block struct {
	Meta
	Stmts []Node `json:"stmts"`
}

// If is the statement `if cond { } [else ...]`. Else is null, a block, or
// another If for `else if`.
type If struct {
	Meta
	Cond Node `json:"cond"`
	Then Node `json:"then"`
	Else Node `json:"else"`
}

// Return is `return [value]`; Value is null without one.
type Return struct {
	Meta
	Value Node `json:"value"`
}

// Assign is `targets op values`, with op one of = += -= *= /=.
type Assign struct {
	Meta
	Op      string `json:"op"`
	Targets []Node `json:"targets"`
	Values  []Node `json:"values"`
}

// IncDec is `target++` or `target--`.
type IncDec struct {
	Meta
	Op     string `json:"op"`
	Target Node   `json:"target"`
}

// --- Expressions ---

// Int is an integer literal as written (decimal, 0x, 0b or 0o). Hint is the
// analyzer's type for it, when the exporter was given one.
type Int struct {
	Meta
	Text string `json:"text"`
	Hint *Hint  `json:"hint,omitempty"`
}

// Bool is `true` or `false`.
type Bool struct {
	Meta
	Value bool `json:"value"`
}

// String is a string literal: Text as written, quotes included, and Value,
// its contents with escapes decoded. Raw is true for a backquoted literal.
type String struct {
	Meta
	Text  string `json:"text"`
	Value string `json:"value"`
	Raw   bool   `json:"raw"`
}

// Name is an identifier in an expression, unresolved.
type Name struct {
	Meta
	Name string `json:"name"`
}

// Unary is `op operand`, with op one of - + ! ^.
type Unary struct {
	Meta
	Op      string `json:"op"`
	Operand Node   `json:"operand"`
}

// Binary is `left op right`. Chains are nested left-associatively, as the
// grammar groups them: `a - b - c` is `(a - b) - c`.
type Binary struct {
	Meta
	Op    string `json:"op"`
	Left  Node   `json:"left"`
	Right Node   `json:"right"`
}

// Call is `callee(args)`, arguments positional and in source order.
type Call struct {
	Meta
	Callee Node   `json:"callee"`
	Args   []Node `json:"args"`
}

// Select is `target.name`, unresolved: a field, a method or a package member.
type Select struct {
	Meta
	Target Node   `json:"target"`
	Name   string `json:"name"`
}

// Paren is `(expr)`.
type Paren struct {
	Meta
	Expr Node `json:"expr"`
}

// Tuple is `(a, b, ...)` with two or more elements.
type Tuple struct {
	Meta
	Elems []Node `json:"elems"`
}

// IfExpr is the expression `if (cond) then else else`; each branch is a block
// or an expression.
type IfExpr struct {
	Meta
	Cond Node `json:"cond"`
	Then Node `json:"then"`
	Else Node `json:"else"`
}

// Match is `subject match { cases }`, arms in source order.
type Match struct {
	Meta
	Subject Node   `json:"subject"`
	Cases   []Node `json:"cases"`
}

// Case is `case pattern [if guard] => body`. Guard is null without one. Body
// is a block, an expression or a statement (an assignment, say).
type Case struct {
	Meta
	Pattern Node `json:"pattern"`
	Guard   Node `json:"guard"`
	Body    Node `json:"body"`
}

// --- Patterns ---

// PatWild is the pattern `_`.
type PatWild struct {
	Meta
}

// PatName is a bare name in a pattern, unresolved: it binds a variable or
// names a variant without fields, which the spec decides.
type PatName struct {
	Meta
	Name string `json:"name"`
}

// PatLit is a literal pattern: an int, bool or string literal, or a unary
// minus applied to an int literal.
type PatLit struct {
	Meta
	Value Node `json:"value"`
}

// PatTuple is the pattern `(p1, p2, ...)`.
type PatTuple struct {
	Meta
	Elems []Node `json:"elems"`
}

// PatExtract is `callee(p1, p2, ...)`: a variant or extractor applied to
// sub-patterns. Callee is a Name or a Select, unresolved.
type PatExtract struct {
	Meta
	Callee Node   `json:"callee"`
	Args   []Node `json:"args"`
}

func (*File) Kind() string        { return "file" }
func (*Val) Kind() string         { return "val" }
func (*Var) Kind() string         { return "var" }
func (*Func) Kind() string        { return "func" }
func (*Param) Kind() string       { return "param" }
func (*Struct) Kind() string      { return "struct" }
func (*Field) Kind() string       { return "field" }
func (*Sealed) Kind() string      { return "sealed" }
func (*Variant) Kind() string     { return "variant" }
func (*TypeName) Kind() string    { return "type-name" }
func (*Block) Kind() string       { return "block" }
func (*If) Kind() string          { return "if" }
func (*Return) Kind() string      { return "return" }
func (*Assign) Kind() string      { return "assign" }
func (*IncDec) Kind() string      { return "incdec" }
func (*Int) Kind() string         { return "int" }
func (*Bool) Kind() string        { return "bool" }
func (*String) Kind() string      { return "string" }
func (*Name) Kind() string        { return "name" }
func (*Unary) Kind() string       { return "unary" }
func (*Binary) Kind() string      { return "binary" }
func (*Call) Kind() string        { return "call" }
func (*Select) Kind() string      { return "select" }
func (*Paren) Kind() string       { return "paren" }
func (*Tuple) Kind() string       { return "tuple" }
func (*IfExpr) Kind() string      { return "if-expr" }
func (*Match) Kind() string       { return "match" }
func (*Case) Kind() string        { return "case" }
func (*PatWild) Kind() string     { return "pat-wild" }
func (*PatName) Kind() string     { return "pat-name" }
func (*PatLit) Kind() string      { return "pat-lit" }
func (*PatTuple) Kind() string    { return "pat-tuple" }
func (*PatExtract) Kind() string  { return "pat-extract" }
func (*Unsupported) Kind() string { return "" }

// nodeTypes is every supported node type, one value each.
var nodeTypes = []Node{
	&File{}, &Val{}, &Var{}, &Func{}, &Param{}, &Struct{}, &Field{}, &Sealed{}, &Variant{}, &TypeName{},
	&Block{}, &If{}, &Return{}, &Assign{}, &IncDec{},
	&Int{}, &Bool{}, &String{}, &Name{}, &Unary{}, &Binary{}, &Call{}, &Select{}, &Paren{}, &Tuple{},
	&IfExpr{}, &Match{}, &Case{},
	&PatWild{}, &PatName{}, &PatLit{}, &PatTuple{}, &PatExtract{},
}

// Kinds is the "kind" of every supported node type, in schema order.
func Kinds() []string {
	out := make([]string, len(nodeTypes))
	for i, n := range nodeTypes {
		out[i] = n.Kind()
	}
	return out
}

// The constructs the exporter reports as unsupported, by the name in their
// "unsupported" field.
const (
	UnsupportedImport           = "import"
	UnsupportedEmbed            = "embed"
	UnsupportedOpaqueType       = "opaque-type"
	UnsupportedDefinedType      = "defined-type"
	UnsupportedInterface        = "interface"
	UnsupportedGenericDecl      = "generic-decl"
	UnsupportedMethod           = "method"
	UnsupportedGoResults        = "go-results"
	UnsupportedParamModifier    = "param-modifier"
	UnsupportedParamDefault     = "param-default"
	UnsupportedVariadicParam    = "variadic-param"
	UnsupportedUnnamedParam     = "unnamed-param"
	UnsupportedBind             = "bind"
	UnsupportedAlso             = "also"
	UnsupportedUse              = "use"
	UnsupportedFor              = "for"
	UnsupportedIfInit           = "if-init"
	UnsupportedShortVarDecl     = "short-var-decl"
	UnsupportedLambda           = "lambda"
	UnsupportedPartialFunction  = "partial-function"
	UnsupportedCompositeLiteral = "composite-literal"
	UnsupportedIndex            = "index"
	UnsupportedNamedArg         = "named-arg"
	UnsupportedSpreadArg        = "spread-arg"
	UnsupportedUnit             = "unit"
	UnsupportedOneTuple         = "one-tuple"
	UnsupportedLeadingZero      = "leading-zero"
	UnsupportedFloatLiteral     = "float-literal"
	UnsupportedCharLiteral      = "char-literal"
	UnsupportedNil              = "nil"
	UnsupportedInterpolated     = "interpolated-string"
	UnsupportedFormatString     = "format-string"
	UnsupportedStringEscape     = "string-escape"
	UnsupportedGenericType      = "generic-type"
	UnsupportedQualifiedType    = "qualified-type"
	UnsupportedSliceType        = "slice-type"
	UnsupportedPointerType      = "pointer-type"
	UnsupportedMapType          = "map-type"
	UnsupportedFuncType         = "func-type"
	UnsupportedOtherType        = "other-type"
	UnsupportedDeref            = "deref"
	UnsupportedAddressOf        = "address-of"
	UnsupportedReceive          = "receive"
	UnsupportedTypedPattern     = "typed-pattern"
	UnsupportedRestPattern      = "rest-pattern"
	UnsupportedSelectPattern    = "select-pattern"
	UnsupportedExprPattern      = "expression-pattern"
	UnsupportedParenPattern     = "paren-pattern"
)

// unsupportedKinds is every name an Unsupported node can carry.
var unsupportedKinds = []string{
	UnsupportedImport, UnsupportedEmbed, UnsupportedOpaqueType, UnsupportedDefinedType,
	UnsupportedInterface, UnsupportedGenericDecl, UnsupportedMethod, UnsupportedGoResults,
	UnsupportedParamModifier, UnsupportedParamDefault, UnsupportedVariadicParam,
	UnsupportedUnnamedParam, UnsupportedBind, UnsupportedAlso, UnsupportedUse, UnsupportedFor,
	UnsupportedIfInit, UnsupportedShortVarDecl, UnsupportedLambda, UnsupportedPartialFunction,
	UnsupportedCompositeLiteral, UnsupportedIndex, UnsupportedNamedArg, UnsupportedSpreadArg,
	UnsupportedUnit, UnsupportedOneTuple, UnsupportedLeadingZero, UnsupportedFloatLiteral, UnsupportedCharLiteral,
	UnsupportedNil, UnsupportedInterpolated, UnsupportedFormatString, UnsupportedStringEscape, UnsupportedGenericType,
	UnsupportedQualifiedType, UnsupportedSliceType, UnsupportedPointerType, UnsupportedMapType,
	UnsupportedFuncType, UnsupportedOtherType, UnsupportedDeref, UnsupportedAddressOf, UnsupportedReceive,
	UnsupportedTypedPattern, UnsupportedRestPattern, UnsupportedSelectPattern,
	UnsupportedExprPattern, UnsupportedParenPattern,
}

// UnsupportedKinds is every name an Unsupported node can carry.
func UnsupportedKinds() []string {
	return slices.Clone(unsupportedKinds)
}
