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

// Meta is the kind and position every supported node starts with.
type Meta struct {
	Kind string `json:"kind"`
	Pos  Pos    `json:"pos"`
}

func (m *Meta) setMeta(v Meta) { *m = v }

// Unsupported stands for a Go construct outside GoCore, in place of the node
// it would be. What names the construct (see UnsupportedKinds()).
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
	Name       string   `json:"name"`
	Recv       *Field   `json:"recv"`
	TypeParams []*Field `json:"typeParams"`
	Params     []*Field `json:"params"`
	Results    []*Field `json:"results"`
	Variadic   bool     `json:"variadic"`
	Body       Node     `json:"body"`
}

// TypeDecl is `type Name[params] Type`, or an alias `type Name = Type`.
type TypeDecl struct {
	Meta
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

// Field is a parameter, result, receiver, type parameter or struct field:
// its names (none for an unnamed parameter or an embedded field) and type.
type Field struct {
	Names []string `json:"names"`
	Type  Node     `json:"type"`
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

// FuncType is `func(params) results`.
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

// IntLit is an integer literal; Value is its value in decimal, read from
// the literal's text (any base, `_` separators), arbitrarily large.
type IntLit struct {
	Meta
	Value string `json:"value"`
}

// StringLit is a string literal, interpreted or raw; Value is its value,
// escapes interpreted.
type StringLit struct {
	Meta
	Value string `json:"value"`
}

// CharLit is a rune literal; Value is the code point.
type CharLit struct {
	Meta
	Value int32 `json:"value"`
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

// DeclStmt is a `var`, `const` or `type` declaration inside a function: one
// ValueDecl, or a TypeDecl for each spec of a grouped `type ( ... )`.
type DeclStmt struct {
	Meta
	Decls []Node `json:"decls"`
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

// CaseClause is `case list: body`, or `default:` when List is empty and
// Default is set.
type CaseClause struct {
	List    []Node `json:"list"`
	Default bool   `json:"default"`
	Body    []Node `json:"body"`
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

// nodeTypes maps each supported kind to its Go type, for schema_test.go and
// for Kind().
var nodeTypes = map[string]Node{
	"file":           &File{},
	"func-decl":      &FuncDecl{},
	"type-decl":      &TypeDecl{},
	"value-decl":     &ValueDecl{},
	"struct-type":    &StructType{},
	"interface-type": &InterfaceType{},
	"func-type":      &FuncType{},
	"ident":          &Ident{},
	"int-lit":        &IntLit{},
	"string-lit":     &StringLit{},
	"char-lit":       &CharLit{},
	"composite-lit":  &CompositeLit{},
	"key-value":      &KeyValue{},
	"func-lit":       &FuncLit{},
	"paren":          &Paren{},
	"selector":       &Selector{},
	"index":          &Index{},
	"type-assert":    &TypeAssert{},
	"call":           &Call{},
	"star":           &Star{},
	"unary":          &Unary{},
	"binary":         &Binary{},
	"block":          &Block{},
	"decl-stmt":      &DeclStmt{},
	"expr-stmt":      &ExprStmt{},
	"inc-dec":        &IncDec{},
	"assign":         &Assign{},
	"return":         &Return{},
	"branch":         &Branch{},
	"if":             &If{},
	"switch":         &Switch{},
	"for":            &For{},
	"range":          &Range{},
	"empty":          &Empty{},
}

func (n *File) Kind() string          { return n.Meta.Kind }
func (n *FuncDecl) Kind() string      { return n.Meta.Kind }
func (n *TypeDecl) Kind() string      { return n.Meta.Kind }
func (n *ValueDecl) Kind() string     { return n.Meta.Kind }
func (n *StructType) Kind() string    { return n.Meta.Kind }
func (n *InterfaceType) Kind() string { return n.Meta.Kind }
func (n *FuncType) Kind() string      { return n.Meta.Kind }
func (n *Ident) Kind() string         { return n.Meta.Kind }
func (n *IntLit) Kind() string        { return n.Meta.Kind }
func (n *StringLit) Kind() string     { return n.Meta.Kind }
func (n *CharLit) Kind() string       { return n.Meta.Kind }
func (n *CompositeLit) Kind() string  { return n.Meta.Kind }
func (n *KeyValue) Kind() string      { return n.Meta.Kind }
func (n *FuncLit) Kind() string       { return n.Meta.Kind }
func (n *Paren) Kind() string         { return n.Meta.Kind }
func (n *Selector) Kind() string      { return n.Meta.Kind }
func (n *Index) Kind() string         { return n.Meta.Kind }
func (n *TypeAssert) Kind() string    { return n.Meta.Kind }
func (n *Call) Kind() string          { return n.Meta.Kind }
func (n *Star) Kind() string          { return n.Meta.Kind }
func (n *Unary) Kind() string         { return n.Meta.Kind }
func (n *Binary) Kind() string        { return n.Meta.Kind }
func (n *Block) Kind() string         { return n.Meta.Kind }
func (n *DeclStmt) Kind() string      { return n.Meta.Kind }
func (n *ExprStmt) Kind() string      { return n.Meta.Kind }
func (n *IncDec) Kind() string        { return n.Meta.Kind }
func (n *Assign) Kind() string        { return n.Meta.Kind }
func (n *Return) Kind() string        { return n.Meta.Kind }
func (n *Branch) Kind() string        { return n.Meta.Kind }
func (n *If) Kind() string            { return n.Meta.Kind }
func (n *Switch) Kind() string        { return n.Meta.Kind }
func (n *For) Kind() string           { return n.Meta.Kind }
func (n *Range) Kind() string         { return n.Meta.Kind }
func (n *Empty) Kind() string         { return n.Meta.Kind }

// unsupportedKinds names every construct the exporter reports as
// Unsupported, for schema_test.go.
var unsupportedKinds = []string{
	"float-literal",
	"imag-literal",
	"slice-expr",
	"array-type",
	"map-type",
	"chan-type",
	"ellipsis",
	"labeled-stmt",
	"labeled-branch",
	"goto",
	"fallthrough",
	"send",
	"go",
	"defer",
	"select",
	"type-switch",
	"receive",
	"bad-node",
}

// Kinds is every node kind, sorted.
func Kinds() []string {
	out := make([]string, 0, len(nodeTypes))
	for k := range nodeTypes {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// UnsupportedKinds is every construct name an Unsupported node may carry.
func UnsupportedKinds() []string {
	return append([]string(nil), unsupportedKinds...)
}
