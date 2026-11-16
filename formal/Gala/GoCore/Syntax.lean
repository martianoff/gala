/-
  # GoCore: syntax of whole programs

  The syntax of the Go subset the transpiler emits, as the Go AST exporter writes it
  (`internal/formal/gocoreexport/SCHEMA.md`, GoCore JSON). It mirrors `go/ast`: names are not
  resolved, types are expressions, and a construct outside GoCore is an `unsupported`
  node of the category it stands in, which a run reports only if it reaches it.

  `Gala/GoCore.lean` is the fragment M3's `match` lowering targets, with abstract sealed
  values and pattern primitives. This is the full language M4 runs on the transformer's
  actual output (docs/FORMAL_SEMANTICS.MD, "M4: GoCore and the Go AST exporter"). Until M5
  retires the fragment, these are two GoCore semantics; M5's reference lowering targets
  this one.
-/
namespace Gala.GoCore.Full

mutual
/-- An expression, or a type written as one (Go spec, "Expressions", "Types"). -/
inductive Expr
  | ident (name : String)
  | intLit (value : Int)
  | strLit (value : String)
  | charLit (value : Int)
  /-- `T{elts}`; `none` for a type elided inside another composite literal. -/
  | composite (ty : Option Expr) (elts : List Expr)
  | keyValue (key value : Expr)
  | funcLit (sig : Sig) (body : List Stmt)
  | paren (x : Expr)
  | selector (x : Expr) (sel : String)
  /-- `x[indices]`: an index, or an instantiation with type arguments. -/
  | index (x : Expr) (indices : List Expr)
  /-- `x[low:high]`, or `x[low:high:max]`; each index may be omitted. -/
  | sliceExpr (x : Expr) (low high max : Option Expr)
  | typeAssert (x : Expr) (ty : Expr)
  | call (fn : Expr) (args : List Expr) (ellipsis : Bool)
  | star (x : Expr)
  | unary (op : String) (x : Expr)
  | binary (op : String) (x y : Expr)
  | structType (fields : List Field)
  | interfaceType (elems : List Field)
  | funcType (sig : Sig)
  /-- `[len]elt`, or the slice type `[]elt` when `len` is `none`. -/
  | arrayType (len : Option Expr) (elt : Expr)
  /-- The `...` length of `[...]T{...}`. -/
  | ellipsis
  | unsupported (what : String)
/-- A parameter, result, receiver, type parameter, struct field or interface element:
    its names (none when unnamed or embedded) and its type. -/
inductive Field
  | mk (names : List String) (ty : Expr)
/-- A signature: parameters, results, and whether the last parameter is `...T`. -/
inductive Sig
  | mk (params results : List Field) (variadic : Bool)
/-- A statement (Go spec, "Statements"). -/
inductive Stmt
  | block (stmts : List Stmt)
  | decl (d : Decl)
  | expr (x : Expr)
  | incDec (op : String) (x : Expr)
  /-- `lhs tok rhs`, `tok` one of `=`, `:=` and the `op=` forms. -/
  | assign (tok : String) (lhs rhs : List Expr)
  | ret (results : List Expr)
  /-- `break` or `continue`. -/
  | branch (tok : String)
  | if_ (init : Option Stmt) (cond : Expr) (body : List Stmt) (els : Option Stmt)
  | switch (init : Option Stmt) (tag : Option Expr) (clauses : List Clause)
  /-- `switch init; x := y.(type) { clauses }`, `bind` being `x` (`none` without one);
      a clause lists types, `nil` among them. -/
  | typeSwitch (init : Option Stmt) (bind : Option String) (x : Expr) (clauses : List Clause)
  | for_ (init : Option Stmt) (cond : Option Expr) (post : Option Stmt) (body : List Stmt)
  | range (key value : Option Expr) (tok : String) (x : Expr) (body : List Stmt)
  /-- `defer call`. -/
  | defer_ (call : Expr)
  | empty
  | unsupported (what : String)
/-- `case list: body`, or `default:`. -/
inductive Clause
  | mk (list : List Expr) (isDefault : Bool) (body : List Stmt)
/-- A declaration (Go spec, "Declarations and scope"). -/
inductive Decl
  /-- A function or method; `body` is `none` for one without a body. -/
  | func (name : String) (recv : Option Field) (typeParams : List Field) (sig : Sig)
      (body : Option (List Stmt))
  | type (specs : List TypeSpec)
  /-- A `var` or `const` declaration; a const spec's index is its `iota`. -/
  | value (isConst : Bool) (specs : List Spec)
  | unsupported (what : String)
/-- `name[typeParams] type`, or an alias `name = type`. -/
inductive TypeSpec
  | mk (name : String) (typeParams : List Field) (alias : Bool) (ty : Expr)
/-- `names [type] [= values]`. -/
inductive Spec
  | mk (names : List String) (ty : Option Expr) (values : List Expr)
end

instance : Inhabited Expr := ⟨.unsupported ""⟩
instance : Inhabited Stmt := ⟨.empty⟩

/-- An import: its name (`""` for none, `.` or `_`) and its path. -/
structure Import where
  name : String
  path : String

/-- A source file. -/
structure File where
  name : String
  imports : List Import
  decls : List Decl

/-- A package: its import path, its name and its files. -/
structure Package where
  path : String
  name : String
  /-- Only some of the package's files are given (Go's standard library's runnable files):
      a name it lacks is in a file left out. -/
  isPartial : Bool := false
  files : List File

/-- A program: the main package and the packages it imports. -/
structure Program where
  packages : List Package

end Gala.GoCore.Full
