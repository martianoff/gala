/-
  # Surface syntax of GALA subset S1

  The abstract syntax galaspec reads. It mirrors Core JSON v1
  (`internal/formal/coreexport/SCHEMA.md`) one kind to one constructor, with three
  simplifications:

  * source positions are dropped: the semantics never depends on them;
  * literals are decoded: an `int` node carries its value, a `string` node its contents;
  * statements and expressions share one type, `Expr`, as they do in the semantics: a
    statement is an expression whose value is unit. A `block`'s statements, a `case`'s
    body and a function's body are all `Expr`.

  Nothing is resolved here: a `name` is just a string, exactly as the exporter wrote it.
  `Gala/Scope.lean` and `Gala/Eval.lean` decide what it refers to.

  An `unsupported` node of Core JSON stays an `unsupported` node here, at the same place,
  so execution reports it only when it reaches it.
-/
namespace Gala.Spec

/-- A type as written: a plain name (`int`, `Point`), or an unsupported form of type. -/
inductive Ty
  | name (n : String)
  | unsupported (kind : String)
  deriving Repr, BEq, Inhabited

/-- A field of a struct or a sealed variant. -/
structure Field where
  name : String
  type : Option Ty
  mutable : Bool
  deriving Repr, Inhabited

/-- One `case` of a sealed type. -/
structure Variant where
  name : String
  isDefault : Bool
  fields : List Field
  deriving Repr, Inhabited

/-- A literal. Integers are mathematical here; `Gala/Eval.lean` checks they fit `int`. -/
inductive Lit
  | int (v : Int)
  | bool (b : Bool)
  | str (s : String)
  deriving Repr, BEq, Inhabited

/-- A pattern (SCHEMA.md, `pat-*`). -/
inductive Pat
  /-- `_` -/
  | wild
  /-- A bare name: a binding, a zero-field variant, or a stable identifier. -/
  | name (n : String)
  /-- A literal pattern, `case 1`, `case -1`, `case "a"`, `case true`. -/
  | lit (l : Lit)
  /-- `(p1, ..., pn)`, n ≥ 2. -/
  | tuple (ps : List Pat)
  /-- `C(p1, ..., pn)`: a variant or struct extractor, named by `callee`. -/
  | extract (callee : String) (args : List Pat)
  /-- A pattern outside S1. -/
  | unsupported (kind : String)
  deriving Repr, Inhabited

/-- How a declaration binds: `val` (immutable) or `var` (mutable). -/
inductive Mut | val | var
  deriving Repr, BEq, DecidableEq, Inhabited

/-- Expressions and statements. -/
inductive Expr
  | lit (l : Lit)
  | name (n : String)
  /-- `op e`, `op` one of `-`, `+`, `!`, `^`. -/
  | unary (op : String) (e : Expr)
  /-- `l op r`. -/
  | binary (op : String) (l r : Expr)
  /-- `callee(args)`: a function call, a struct or variant construction, or printing. -/
  | call (callee : Expr) (args : List Expr)
  /-- `target.name`. -/
  | select (target : Expr) (name : String)
  /-- `(e)`. -/
  | paren (e : Expr)
  /-- `(e1, ..., en)`, n ≥ 2. -/
  | tuple (es : List Expr)
  /-- The expression `if (c) a else b`. -/
  | ifExpr (c a b : Expr)
  /-- `subject match { cases }`; each case is pattern, optional guard, body. -/
  | mtch (subject : Expr) (cases : List (Pat × Option Expr × Expr))
  /-- `{ stmts }`. -/
  | block (stmts : List Expr)
  /-- The statement `if c { ... } else ...`; the else part is a block, an `if`, or none. -/
  | ifStmt (c : Expr) (thenB : Expr) (elseB : Option Expr)
  /-- `return` or `return v`. -/
  | ret (v : Option Expr)
  /-- `targets op values`, `op` one of `=`, `+=`, `-=`, `*=`, `/=`. -/
  | assign (op : String) (targets : List Expr) (values : List Expr)
  /-- `target++` or `target--`; `op` is `++` or `--`. -/
  | incdec (op : String) (target : Expr)
  /-- `val`/`var` names (or `val (x, y) = ...` when `destructure`), optional type,
      initializers. -/
  | decl (m : Mut) (names : List String) (destructure : Bool) (type : Option Ty)
      (values : List Expr)
  /-- A construct outside S1. -/
  | unsupported (kind : String)
  deriving Repr, Inhabited

/-- A function parameter. -/
structure Param where
  name : String
  type : Option Ty
  deriving Repr, Inhabited

/-- A function declaration. -/
structure Func where
  name : String
  params : List Param
  result : Option Ty
  /-- The body: a `block`, or the expression of the `= expr` form. -/
  body : Expr
  deriving Repr, Inhabited

/-- A top-level declaration. -/
inductive Decl
  | func (f : Func)
  | struct_ (name : String) (fields : List Field)
  | sealed (name : String) (variants : List Variant)
  /-- A package-level `val` or `var`. -/
  | global (m : Mut) (names : List String) (destructure : Bool) (type : Option Ty)
      (values : List Expr)
  /-- A top-level declaration outside S1 (an `import`, a method, ...). -/
  | unsupported (kind : String)
  deriving Repr, Inhabited

/-- One source file. -/
structure File where
  path : String
  package : String
  decls : List Decl
  deriving Repr, Inhabited

/-- A Core JSON document. -/
structure Program where
  files : List File
  deriving Repr, Inhabited

end Gala.Spec
