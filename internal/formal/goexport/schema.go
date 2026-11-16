package goexport

import "slices"

// This file is the GoCore JSON schema as Go types. SCHEMA.md documents the
// same node kinds and fields; schema_test.go checks that the two agree.

// Version is the schema version, the "gocore" field of every document. A
// consumer rejects a version it does not know.
const Version = 1

// Program is a GoCore JSON document: the packages of one Go program, the
// main package and every package it imports that the exporter was given.
type Program struct {
	GoCore   int        `json:"gocore"`
	Packages []*Package `json:"packages"`
}

// Package is one Go package: its import path and its files.
type Package struct {
	Path  string  `json:"path"`
	Name  string  `json:"name"`
	Files []*File `json:"files"`
}

// Pos is where a node starts in its file: the line and column (both from 1,
// the column in bytes) that go/token reports.
type Pos struct {
	Line int `json:"line"`
	Col  int `json:"col"`
}

// Node is a node of GoCore JSON: one of the kinds in nodeTypes, or an
// Unsupported construct.
type Node interface {
	// Kind is the node's "kind" field ("" for Unsupported).
	Kind() string
}

// Meta is the kind and position every supported node starts with. The
// exporter fills it from the node's Kind (see at), so a node's kind is its
// type's.
type Meta struct {
	Kind string `json:"kind"`
	Pos  Pos    `json:"pos"`
}

func (m *Meta) setMeta(v Meta) { *m = v }

// Unsupported stands for a Go construct outside GoCore, in place of the node
// it would be. What names the construct: one of the Unsupported* constants.
type Unsupported struct {
	What string `json:"unsupported"`
	Pos  Pos    `json:"pos"`
}

func (*Unsupported) Kind() string { return "" }

// --- Files and declarations ---

// File is one Go source file: its name, its imports and its declarations
// other than imports, in source order.
type File struct {
	Meta
	Name    string    `json:"name"`
	Imports []*Import `json:"imports"`
	Decls   []Node    `json:"decls"`
}

// Import is `import name "path"`; Name is "" when the import has none, "."
// for a dot import and "_" for a blank one.
type Import struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// FuncDecl is a function or method declaration. Recv is nil for a function;
// Body is nil for a declaration without a body.
type FuncDecl struct {
	Meta
	Name       string    `json:"name"`
	Recv       *Field    `json:"recv"`
	TypeParams []*Field  `json:"typeParams"`
	Type       *FuncType `json:"type"`
	Body       Node      `json:"body"`
}

// TypeDecl is one `type` declaration: its specs in source order.
type TypeDecl struct {
	Meta
	Specs []*TypeSpec `json:"specs"`
}

// TypeSpec is `Name[params] Type`, or an alias `Name = Type`.
type TypeSpec struct {
	Name       string   `json:"name"`
	TypeParams []*Field `json:"typeParams"`
	Alias      bool     `json:"alias"`
	Type       Node     `json:"type"`
}

// ValueDecl is one `var` or `const` declaration: its specs in source order.
// A const spec's index in Specs is its iota.
type ValueDecl struct {
	Meta
	Const bool         `json:"const"`
	Specs []*ValueSpec `json:"specs"`
}

// ValueSpec is `names [Type] [= values]`. In a const declaration a spec with
// neither a type nor values repeats the previous one's (Go spec, "Constant
// declarations"); the exporter leaves that to the consumer.
type ValueSpec struct {
	Names  []string `json:"names"`
	Type   Node     `json:"type"`
	Values []Node   `json:"values"`
}

// Field is a parameter, result, receiver, type parameter, struct field or
// interface element: its names (none for an unnamed parameter or an
// embedded field), its type and, for a struct field, its tag's value ("" for
// none).
type Field struct {
	Names []string `json:"names"`
	Type  Node     `json:"type"`
	Tag   string   `json:"tag"`
}

// --- Types ---

// StructType is `struct { fields }`.
type StructType struct {
	Meta
	Fields []*Field `json:"fields"`
}

// InterfaceType is `interface { ... }`: its methods (Field with one name and
// a FuncType) and embedded types (Field with no name).
type InterfaceType struct {
	Meta
	Elems []*Field `json:"elems"`
}

// FuncType is `func(params) results`; Variadic is set when the last
// parameter is `...T`, whose Field has type T.
type FuncType struct {
	Meta
	Params   []*Field `json:"params"`
	Results  []*Field `json:"results"`
	Variadic bool     `json:"variadic"`
}

// --- Expressions (a type name is an Ident, Selector, Index or Star) ---

// Ident is a name, whatever it refers to.
type Ident struct {
	Meta
	Name string `json:"name"`
}

// IntLit is an integer literal; Value is its value in decimal, arbitrarily
// large.
type IntLit struct {
	Meta
	Value string `json:"value"`
}

// StringLit is a string literal, interpreted or raw; Value is its value. A
// literal whose value is not valid UTF-8 is unsupported, since JSON could
// not carry it exactly.
type StringLit struct {
	Meta
	Value string `json:"value"`
}

// CharLit is a rune literal; Value is its value (a code point, or a byte
// for `'\xff'`).
type CharLit struct {
	Meta
	Value int64 `json:"value"`
}

// CompositeLit is `Type{elts}`. Type is nil for an elided type inside
// another composite literal.
type CompositeLit struct {
	Meta
	Type Node   `json:"type"`
	Elts []Node `json:"elts"`
}

// KeyValue is `key: value` in a composite literal.
type KeyValue struct {
	Meta
	Key   Node `json:"key"`
	Value Node `json:"value"`
}

// FuncLit is `func(params) results { body }`.
type FuncLit struct {
	Meta
	Type *FuncType `json:"type"`
	Body Node      `json:"body"`
}

// Paren is `(x)`.
type Paren struct {
	Meta
	X Node `json:"x"`
}

// Selector is `x.sel`: a field, a method, or a name of an imported package.
type Selector struct {
	Meta
	X   Node   `json:"x"`
	Sel string `json:"sel"`
}

// Index is `x[indices]`: an index expression, or the instantiation of a
// generic function or type with one or more type arguments.
type Index struct {
	Meta
	X       Node   `json:"x"`
	Indices []Node `json:"indices"`
}

// TypeAssert is `x.(Type)`.
type TypeAssert struct {
	Meta
	X    Node `json:"x"`
	Type Node `json:"type"`
}

// Call is `fun(args)`, `fun(args...)` when Ellipsis is set. A conversion
// `T(x)` and a builtin call are calls too.
type Call struct {
	Meta
	Fun      Node   `json:"fun"`
	Args     []Node `json:"args"`
	Ellipsis bool   `json:"ellipsis"`
}

// Star is `*x`: a pointer type or an indirection.
type Star struct {
	Meta
	X Node `json:"x"`
}

// Unary is `op x`, Op one of `- + ! ^ &`.
type Unary struct {
	Meta
	Op string `json:"op"`
	X  Node   `json:"x"`
}

// Binary is `x op y`.
type Binary struct {
	Meta
	Op string `json:"op"`
	X  Node   `json:"x"`
	Y  Node   `json:"y"`
}

// --- Statements ---

// Block is `{ stmts }`.
type Block struct {
	Meta
	Stmts []Node `json:"stmts"`
}

// DeclStmt is a `var`, `const` or `type` declaration inside a function.
type DeclStmt struct {
	Meta
	Decl Node `json:"decl"`
}

// ExprStmt is an expression used as a statement.
type ExprStmt struct {
	Meta
	X Node `json:"x"`
}

// IncDec is `x++` or `x--`.
type IncDec struct {
	Meta
	Op string `json:"op"`
	X  Node   `json:"x"`
}

// Assign is `lhs tok rhs`, Tok one of `=`, `:=` and the `op=` forms.
type Assign struct {
	Meta
	Tok string `json:"tok"`
	Lhs []Node `json:"lhs"`
	Rhs []Node `json:"rhs"`
}

// Return is `return results`.
type Return struct {
	Meta
	Results []Node `json:"results"`
}

// Branch is `break` or `continue`, without a label.
type Branch struct {
	Meta
	Tok string `json:"tok"`
}

// If is `if init; cond { body } else ...`; Init and Else may be nil, Else is
// a Block or an If.
type If struct {
	Meta
	Init Node `json:"init"`
	Cond Node `json:"cond"`
	Body Node `json:"body"`
	Else Node `json:"else"`
}

// Switch is `switch init; tag { clauses }`; Init and Tag may be nil.
type Switch struct {
	Meta
	Init    Node          `json:"init"`
	Tag     Node          `json:"tag"`
	Clauses []*CaseClause `json:"clauses"`
}

// CaseClause is `case list: body`, or `default:` when List is empty.
type CaseClause struct {
	List []Node `json:"list"`
	Body []Node `json:"body"`
}

// For is `for init; cond; post { body }`; each of the three may be nil.
type For struct {
	Meta
	Init Node `json:"init"`
	Cond Node `json:"cond"`
	Post Node `json:"post"`
	Body Node `json:"body"`
}

// Range is `for key, value tok range x { body }`; Key and Value may be nil,
// Tok is `:=`, `=` or "" (no variables).
type Range struct {
	Meta
	Key   Node   `json:"key"`
	Value Node   `json:"value"`
	Tok   string `json:"tok"`
	X     Node   `json:"x"`
	Body  Node   `json:"body"`
}

// Empty is the empty statement.
type Empty struct {
	Meta
}

func (*File) Kind() string          { return "file" }
func (*FuncDecl) Kind() string      { return "func-decl" }
func (*TypeDecl) Kind() string      { return "type-decl" }
func (*ValueDecl) Kind() string     { return "value-decl" }
func (*StructType) Kind() string    { return "struct-type" }
func (*InterfaceType) Kind() string { return "interface-type" }
func (*FuncType) Kind() string      { return "func-type" }
func (*Ident) Kind() string         { return "ident" }
func (*IntLit) Kind() string        { return "int-lit" }
func (*StringLit) Kind() string     { return "string-lit" }
func (*CharLit) Kind() string       { return "char-lit" }
func (*CompositeLit) Kind() string  { return "composite-lit" }
func (*KeyValue) Kind() string      { return "key-value" }
func (*FuncLit) Kind() string       { return "func-lit" }
func (*Paren) Kind() string         { return "paren" }
func (*Selector) Kind() string      { return "selector" }
func (*Index) Kind() string         { return "index" }
func (*TypeAssert) Kind() string    { return "type-assert" }
func (*Call) Kind() string          { return "call" }
func (*Star) Kind() string          { return "star" }
func (*Unary) Kind() string         { return "unary" }
func (*Binary) Kind() string        { return "binary" }
func (*Block) Kind() string         { return "block" }
func (*DeclStmt) Kind() string      { return "decl-stmt" }
func (*ExprStmt) Kind() string      { return "expr-stmt" }
func (*IncDec) Kind() string        { return "inc-dec" }
func (*Assign) Kind() string        { return "assign" }
func (*Return) Kind() string        { return "return" }
func (*Branch) Kind() string        { return "branch" }
func (*If) Kind() string            { return "if" }
func (*Switch) Kind() string        { return "switch" }
func (*For) Kind() string           { return "for" }
func (*Range) Kind() string         { return "range" }
func (*Empty) Kind() string         { return "empty" }

// nodeTypes is one value of every supported node type, for schema_test.go
// and Kinds.
var nodeTypes = []Node{
	&File{}, &FuncDecl{}, &TypeDecl{}, &ValueDecl{}, &StructType{}, &InterfaceType{}, &FuncType{},
	&Ident{}, &IntLit{}, &StringLit{}, &CharLit{}, &CompositeLit{}, &KeyValue{}, &FuncLit{},
	&Paren{}, &Selector{}, &Index{}, &TypeAssert{}, &Call{}, &Star{}, &Unary{}, &Binary{},
	&Block{}, &DeclStmt{}, &ExprStmt{}, &IncDec{}, &Assign{}, &Return{}, &Branch{}, &If{},
	&Switch{}, &For{}, &Range{}, &Empty{},
}

// Kinds is every node kind, sorted.
func Kinds() []string {
	out := make([]string, 0, len(nodeTypes))
	for _, n := range nodeTypes {
		out = append(out, n.Kind())
	}
	slices.Sort(out)
	return out
}

// The constructs the exporter reports as Unsupported.
const (
	UnsupportedFloatLiteral  = "float-literal"
	UnsupportedImagLiteral   = "imag-literal"
	UnsupportedInvalidUTF8   = "invalid-utf8-string"
	UnsupportedSliceExpr     = "slice-expr"
	UnsupportedArrayType     = "array-type"
	UnsupportedMapType       = "map-type"
	UnsupportedChanType      = "chan-type"
	UnsupportedEllipsis      = "ellipsis"
	UnsupportedTilde         = "tilde"
	UnsupportedReceive       = "receive"
	UnsupportedLabeledStmt   = "labeled-stmt"
	UnsupportedLabeledBranch = "labeled-branch"
	UnsupportedGoto          = "goto"
	UnsupportedFallthrough   = "fallthrough"
	UnsupportedSend          = "send"
	UnsupportedGo            = "go"
	UnsupportedDefer         = "defer"
	UnsupportedSelect        = "select"
	UnsupportedTypeSwitch    = "type-switch"
	UnsupportedBadNode       = "bad-node"
)

var unsupportedKinds = []string{
	UnsupportedFloatLiteral, UnsupportedImagLiteral, UnsupportedInvalidUTF8, UnsupportedSliceExpr,
	UnsupportedArrayType, UnsupportedMapType, UnsupportedChanType, UnsupportedEllipsis,
	UnsupportedTilde, UnsupportedReceive, UnsupportedLabeledStmt, UnsupportedLabeledBranch,
	UnsupportedGoto, UnsupportedFallthrough, UnsupportedSend, UnsupportedGo, UnsupportedDefer,
	UnsupportedSelect, UnsupportedTypeSwitch, UnsupportedBadNode,
}

// UnsupportedKinds is every construct name an Unsupported node may carry.
func UnsupportedKinds() []string {
	return slices.Clone(unsupportedKinds)
}
