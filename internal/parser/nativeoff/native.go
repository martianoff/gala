// Package native, in this directory, stands in for internal/parser/native in
// builds without galop's parser (--//internal/parser:antlr, or nix with
// useGalop = false). It declares what internal/parser names of the native
// tree: the types of its handle (parser.Native) and what its native-tree
// helpers (type_text_native.go) read. There is never a tree of them, so every
// node is no node.
package native

// GalaTree stands in for galop's native tree.
type GalaTree struct {
	Tokens []GalaToken
}

// Node is no node.
func (t *GalaTree) Node(int) GalaNodeRef { return GalaNodeRef{} }

// Text is "". Its receiver is nil (GalaNodeRef.Tree), so it reads nothing.
func (t *GalaTree) Text(int) string { return "" }

// GalaNodeRef stands in for a node of galop's native tree: always no node.
type GalaNodeRef struct{}

// Ok reports false: there are no nodes.
func (GalaNodeRef) Ok() bool { return false }

// Tree is nil.
func (GalaNodeRef) Tree() *GalaTree { return nil }

// Index is -1.
func (GalaNodeRef) Index() int { return -1 }

// Kind is the zero kind.
func (GalaNodeRef) Kind() GalaKind { return 0 }

// StartIndex and StopIndex are -1.
func (GalaNodeRef) StartIndex() int { return -1 }

func (GalaNodeRef) StopIndex() int { return -1 }

// Start is the zero token.
func (GalaNodeRef) Start() GalaToken { return GalaToken{} }

// GetText and SourceText are "".
func (GalaNodeRef) GetText() string { return "" }

func (GalaNodeRef) SourceText() string { return "" }

// ChildCount is 0.
func (GalaNodeRef) ChildCount() int { return 0 }

// Child is no child.
func (GalaNodeRef) Child(int) GalaChild { return GalaChild{} }

// GalaChild stands in for a child of a node: always no node.
type GalaChild struct{}

// Node is no node.
func (GalaChild) Node() GalaNodeRef { return GalaNodeRef{} }

// GalaKind stands in for the kind of a node. The kinds below are the ones
// internal/parser reads, with galop's numbers; no node has one.
type GalaKind uint16

const (
	GalaKIND_parameter GalaKind = 32
	GalaKIND_type      GalaKind = 78
)

// GalaToken stands in for a token of galop's native tree, with the fields
// internal/parser reads.
type GalaToken struct {
	Line, Column int32
}

// GalaTerminal stands in for a token child: always no token.
type GalaTerminal struct{}

// Ok reports false.
func (GalaTerminal) Ok() bool { return false }

// The rule handles below are the ones internal/parser reads, with the
// children it reads: each is no node, as is each child.

// TypeNode stands in for a node of rule type.
type TypeNode struct{ GalaNodeRef }

func (TypeNode) QualifiedIdentifier() QualifiedIdentifierNode { return QualifiedIdentifierNode{} }
func (TypeNode) TypeArguments() TypeArgumentsNode             { return TypeArgumentsNode{} }
func (TypeNode) Type_(int) TypeNode                           { return TypeNode{} }
func (TypeNode) FUNC() GalaTerminal                           { return GalaTerminal{} }
func (TypeNode) Signature() SignatureNode                     { return SignatureNode{} }

// TypeAliasNode stands in for a node of rule typeAlias.
type TypeAliasNode struct{ GalaNodeRef }

func (TypeAliasNode) Type_() TypeNode { return TypeNode{} }

// QualifiedIdentifierNode stands in for a node of rule qualifiedIdentifier.
type QualifiedIdentifierNode struct{ GalaNodeRef }

// IdentifierNode stands in for a node of rule identifier.
type IdentifierNode struct{ GalaNodeRef }

// TypeArgumentsNode stands in for a node of rule typeArguments.
type TypeArgumentsNode struct{ GalaNodeRef }

func (TypeArgumentsNode) TypeList() TypeListNode { return TypeListNode{} }

// TypeListNode stands in for a node of rule typeList.
type TypeListNode struct{ GalaNodeRef }

// SignatureNode stands in for a node of rule signature.
type SignatureNode struct{ GalaNodeRef }

func (SignatureNode) Parameters() ParametersNode { return ParametersNode{} }
func (SignatureNode) Type_() TypeNode            { return TypeNode{} }
func (SignatureNode) GoResults() GoResultsNode   { return GoResultsNode{} }

// GoResultsNode stands in for a node of rule goResults.
type GoResultsNode struct{ GalaNodeRef }

// ParametersNode stands in for a node of rule parameters.
type ParametersNode struct{ GalaNodeRef }

func (ParametersNode) ParameterList() ParameterListNode { return ParameterListNode{} }

// ParameterListNode stands in for a node of rule parameterList.
type ParameterListNode struct{ GalaNodeRef }

// ParameterNode stands in for a node of rule parameter.
type ParameterNode struct{ GalaNodeRef }

func (ParameterNode) ELLIPSIS() GalaTerminal     { return GalaTerminal{} }
func (ParameterNode) Type_() TypeNode            { return TypeNode{} }
func (ParameterNode) Identifier() IdentifierNode { return IdentifierNode{} }
