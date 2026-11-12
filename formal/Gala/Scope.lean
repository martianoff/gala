/-
  # Name resolution for GALA subset S1

  How a name in S1 finds what it refers to. Reflects docs/GALA.MD §2 "Variable
  Declarations", §3 "Functions" and "Parameters", §6 "Match Expression" and "Stable
  Identifiers".

  * **Lexical scoping.** Locals live in a stack of bindings, newest first. A block's
    declarations are popped when the block ends; assignments to outer variables survive
    (`exitScope`).
  * **Shadowing.** Lookup takes the newest binding of a name (`lookup`), so an inner
    `val x` or a `case x` hides an outer `x` for the rest of its scope, and only there.
  * **Function binders.** A call of a top-level function starts a fresh local stack
    holding only the parameters: its body sees its parameters, its own locals and the
    package-level declarations, never its caller's locals.
  * **Closures** (S2; docs/FORMAL_SEMANTICS.MD, "Decisions the spec forces"). A lambda
    captures the locals in scope where it is written, by reference: a local `var` lives
    in a cell (`Val.ref`, `Gala/Eval.lean`), which the lambda shares with its scope, so
    each sees the other's assignments. A call of a lambda sees the captured locals and
    its parameters, never its caller's.
  * **Case binders.** A pattern's variables are pushed for its own guard and body, and
    popped before the next arm is tried (`Gala/Eval.lean`, `evalCases`).
  * **Resolution order** for a name used as an expression (`resolve`): a local, then a
    package-level `val`/`var`, then a function, a struct, a sealed variant, and the
    builtins `Println` and `Print`.

  This file is pure data and functions, so the theorems about scoping
  (`Gala/Theorems.lean`) are stated over it directly.
-/
import Gala.Syntax

namespace Gala.Spec

/-- A runtime value of S1. -/
inductive Val
  /-- An `int`, always within Go's int64 range. -/
  | int (n : Int)
  | bool (b : Bool)
  | str (s : String)
  /-- The value of a statement, or of a call to a function without a result. -/
  | unit
  /-- `(v1, ..., vn)`. -/
  | tuple (vs : List Val)
  /-- A struct value: its type and its fields, in declaration order. -/
  | struct_ (ty : String) (fs : List Val)
  /-- A sealed value: its type, its active case, and that case's fields in order. -/
  | sealed (ty : String) (variant : String) (fs : List Val)
  /-- A lambda's value (S2): its parameters, body and the locals it closes over. A
      captured `var` is a `ref`, so the closure shares it with its scope. -/
  | closure (params : List Param) (result : Option Ty) (body : Expr) (env : List (String × Val))
  /-- A top-level function used as a value. -/
  | fn (f : Func)
  /-- The cell a local `var` lives in (`St.heap`); never the value of an expression. -/
  | ref (n : Nat)
  deriving Repr, Inhabited

/-- A binding: a value and whether it may be assigned (`var`). -/
structure Binding where
  val : Val
  mutable : Bool
  deriving Repr, Inhabited

/-- A stack of bindings, newest first. -/
abbrev Env := List (String × Binding)

/-- The newest binding of `x`. -/
def lookup : Env → String → Option Binding
  | [], _ => none
  | (k, b) :: r, x => if k = x then some b else lookup r x

/-- Push a binding. The name `_` binds nothing (docs/GALA.MD, "Discarding a value"). -/
def bind (ρ : Env) (x : String) (b : Binding) : Env :=
  if x = "_" then ρ else (x, b) :: ρ

/-- Set the newest binding of `x` to `v`; `none` if `x` is unbound. The stack keeps its
    shape, so `exitScope` still finds the outer bindings where they were. -/
def update : Env → String → Val → Option Env
  | [], _, _ => none
  | (k, b) :: r, x, v =>
    if k = x then some ((k, { b with val := v }) :: r)
    else (update r x v).map ((k, b) :: ·)

/-- Leave a scope that was entered when the stack had `depth` bindings: drop the
    bindings made inside it, keep every update made to the ones outside it. -/
def exitScope (ρ : Env) (depth : Nat) : Env := ρ.drop (ρ.length - depth)

/-- The package-level declarations a name can refer to. Built once from the program. -/
structure Decls where
  funcs : List (String × Func)
  structs : List (String × List Field)
  /-- The structs declared in the shorthand form `struct Name(...)`. -/
  shorthands : List String
  /-- Each sealed type with its cases, in declaration order. -/
  sealeds : List (String × List Variant)
  /-- The kinds of the top-level declarations outside S1, in source order. -/
  unsupported : List String
  deriving Inhabited

/-- What a name used as an expression refers to. -/
inductive Ref
  /-- A local or package-level variable. -/
  | var (b : Binding)
  | func (f : Func)
  | struct_ (name : String) (fields : List Field)
  /-- A sealed case: its type and the case. -/
  | variant (ty : String) (v : Variant)
  /-- `Println` (`true`) or `Print` (`false`). -/
  | print (newline : Bool)
  | unbound

/-- The sealed type and case a case name denotes. Case names are unique in a package,
    since each generates a Go declaration of that name. -/
def Decls.variant? (ds : Decls) (name : String) : Option (String × Variant) :=
  ds.sealeds.findSome? fun (ty, vs) => (vs.find? (·.name == name)).map (ty, ·)

/-- The fields of case `c` of sealed type `ty`; `[]` if there is no such case. -/
def Decls.caseFields (ds : Decls) (ty c : String) : List Field :=
  (((ds.sealeds.lookup ty).bind fun vs => vs.find? (·.name == c)).map (·.fields)).getD []

/-- A variable: the newest local binding, else the package-level one. -/
def lookupVar (locals globals : Env) (x : String) : Option Binding :=
  lookup locals x <|> lookup globals x

/-- Resolve a name used as an expression, in the order of the module doc. -/
def resolve (ds : Decls) (locals globals : Env) (x : String) : Ref :=
  match lookupVar locals globals x with
  | some b => .var b
  | none =>
  match ds.funcs.lookup x with
  | some f => .func f
  | none =>
  match ds.structs.lookup x with
  | some fs => .struct_ x fs
  | none =>
  match ds.variant? x with
  | some (ty, v) => .variant ty v
  | none =>
  if x == "Println" then .print true
  else if x == "Print" then .print false
  else .unbound

/-- Is a name capitalized? Only a capitalized name can be a stable identifier. -/
def capitalized (x : String) : Bool :=
  match x.toList with
  | c :: _ => c.isUpper
  | [] => false

/-- What a bare name in a pattern means (docs/GALA.MD, "Stable Identifiers"), given the
    subject's sealed type and case set when it has one:
    1. a case of the subject's own sealed type: a test for that case;
    2. otherwise, a capitalized name of a value in scope: compared with `==`;
    3. otherwise, a new binding, whatever its case. -/
inductive PatName
  | variant (v : Variant)
  | stable (b : Binding)
  | binder

/-- Classify a bare pattern name. `subjectCases` is the case list of the subject's
    sealed type, or `[]` when the subject is not a sealed value. -/
def classifyPatName (subjectCases : List Variant) (locals globals : Env) (x : String) :
    PatName :=
  match subjectCases.find? (·.name == x) with
  | some v => .variant v
  | none =>
    match capitalized x, lookupVar locals globals x with
    | true, some b => .stable b
    | _, _ => .binder

end Gala.Spec
