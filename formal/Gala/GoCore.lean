/-
  # GoCore: the subset of Go the transpiler emits (first fragment)

  A semantics of the Go constructs the transpiler emits for `match`
  (docs/FORMAL_SEMANTICS.MD, "M3: the match lowering against galaspec"). It is not a
  semantics of Go: it covers only the constructs below, and that real Go behaves as it
  says is an assumption, tested from M4 on by running GoCore next to `go run`. Later
  milestones extend it (M4, M5): calls, more operators, package-level initialization and
  `fmt`'s printing.

  This file does not import galaspec: GoCore is defined on its own terms, so a theorem
  relating the two is a statement about two independent definitions.

  Each construct cites the section of the Go language specification
  (https://go.dev/ref/spec) it models. The modelling choices, all deliberate:

  * **Variables.** A variable is a user name or a compiler temporary (`GVar.tmp`). The
    transformer names its temporaries `_tmp_<n>` and `_subj`, choosing each so that no
    name in scope has it; GoCore puts temporaries in a namespace of their own, so a
    temporary can never hide a user name or be hidden by one.
  * **Scopes.** Local variables are a stack, newest first. A block, an `if` branch and a
    function literal's body drop the variables declared in them when they end normally;
    an assignment to an outer variable updates it in place, so it survives (Go spec,
    "Blocks", "Declarations and scope"). Variables have no addresses: nothing in this
    fragment takes one, and closures that share a variable are S2 (M7).
  * **Values.** `int` is Go's `int` on a 64-bit target. A tuple is the value of a
    `std.TupleN` struct (always `tuple`, never `struct_`), a struct the value of a Go
    struct, by field position. `opt` is the `std.Option` a generated `Unapply` returns;
    a program's own `Option` values (`Some(x)`, `case Some(v)`) are outside S1 and this
    fragment. A sealed
    value is its type, its active case and that case's fields: this abstracts the struct
    the transpiler generates, which holds every case's fields and a tag naming the
    active case. M4 must either use the flat layout or prove that `Unapply` on it reads
    the tag and only the active case's fields. `Immutable` wrappers and their `Get()`
    are identities here.
  * **Pattern primitives.** `C{}.Unapply(x)` of a generated sealed case, `IsDefined()`,
    `Value.Get()` of an `Option`, and a field read (`t.V1.Get()`, `p.X`) are primitives.
    They are pure and total: on a value of a shape Go's type checker rules out, they give
    a placeholder (`zero`, `false` or `None`) instead of stopping. A user-defined
    `Unapply`, which runs arbitrary code, is not one of them.
  * **Zero values.** `Value.Get()` of a `None` is Go's zero value of the `Option`'s
    element type. GoCore has no types yet, so every such zero value is one placeholder,
    `zero` (Go spec, "The zero value"). Two `zero`s do not compare equal (`geq`), while
    two of Go's zero values of one type do, so this is sound only where no comparison
    involving `zero` decides anything. In the `match` lowering, a test that reads a
    `Value.Get()` result is an `&&` operand after its `IsDefined()`, so it runs only on a
    `Some`; M3's correctness proof for the lowering has to show this.
  * **Evaluation order.** Operands are evaluated left to right. The Go spec fixes that
    order only among calls (and method calls and receives), not between a call and a
    variable read in one expression. The fragment's operands are variables, constants and
    pattern primitives, which cannot change a variable, so the order is unobservable here;
    an expression mixing a call with a read of a variable the call assigns is outside
    what this fragment models faithfully.
  * **Function values** are not modelled: a value GoCore cannot represent is `opaque`.
  * **Panics** carry the class the differential harness gives Go's panic message; the
    message itself is not compared. After a panic only the output means anything: the
    scopes a panic leaves are not dropped, as no code runs after it.
  * **Holes.** `ext i` stands for code this fragment does not model yet (a call, `fmt`'s
    printing, ...). Evaluation takes what each hole does as a parameter, `X`, an arbitrary
    computation on the state: it may print, panic, assign variables. A theorem about code
    with holes holds for every `X` its hypotheses allow.
  * **No fuel.** The fragment has no loops and no calls of named functions, so evaluation
    is structurally recursive and always ends. Fuel comes with calls (M5).
-/
namespace Gala.GoCore

/-- A variable: one the program names, or a compiler temporary. -/
inductive GVar
  | user (x : String)
  | tmp (n : Nat)
  deriving DecidableEq, Repr, Inhabited


/-- A GoCore value. -/
inductive GVal
  | int (n : Int)
  | bool (b : Bool)
  | str (s : String)
  /-- Go's empty struct `struct{}{}`: the value of a statement or of a call without a
      result. -/
  | unit
  /-- A `std.TupleN` value, its fields `V1, ..., Vn` in order. -/
  | tuple (vs : List GVal)
  /-- A value of struct type `ty`, its fields in declaration order. -/
  | struct_ (ty : String) (fs : List GVal)
  /-- A value of sealed type `ty` whose active case is `c`, with that case's fields. -/
  | sealed (ty : String) (c : String) (fs : List GVal)
  /-- A `std.Option` value. -/
  | opt (o : Option GVal)
  /-- The placeholder for a typed zero value (see the module doc). -/
  | zero
  /-- A value GoCore does not model (a function value). -/
  | opaque
  deriving Repr, Inhabited

/-- The class of a panic, as the differential harness reads it from Go's message. -/
inductive PanicClass
  /-- No `match` arm matched. -/
  | match_
  /-- Integer division by zero. -/
  | divide
  | index
  | other
  deriving Repr, DecidableEq

/-- Why execution stopped before its end: a panic, a `return` on its way to its function,
    or something no program Go compiles does (`stuck`). -/
inductive GStop
  | panic (c : PanicClass) (msg : String)
  | ret (v : GVal)
  | stuck (msg : String)
  deriving Repr

/-- A binary operator whose operands are both evaluated, left to right. -/
inductive BinOp
  /-- `a == b` (Go spec, "Comparison operators"), and for sealed values the generated
      `a.Equal(b)`. -/
  | eq
  /-- `a + b` on `int`, wrapping (Go spec, "Arithmetic operators", "Integer overflow"). -/
  | add
  /-- `a > b` on `int` (Go spec, "Comparison operators"). -/
  | gt
  deriving Repr, DecidableEq

/-- A pattern primitive: pure and total, on the value of its one operand. -/
inductive Prim
  /-- `C{}.Unapply(e)` of case `C` of a sealed type, which has `n` fields: a `bool` when
      `n = 0`, an `Option` of the field when `n = 1`, an `Option` of a `std.TupleN` of
      them otherwise. -/
  | unapply (c : String) (n : Nat)
  /-- `e.IsDefined()` of an `Option`. -/
  | isDefined
  /-- `e.Value.Get()` of an `Option`. -/
  | get
  /-- Field `i` (from 0) of a tuple (`e.V{i+1}.Get()`) or a struct (Go spec, "Selectors"). -/
  | field (i : Nat)
  deriving Repr, DecidableEq

mutual
/-- GoCore expressions. -/
inductive GExpr
  /-- A constant (Go spec, "Constants"). -/
  | lit (v : GVal)
  /-- A variable (Go spec, "Operands"). -/
  | var (x : GVar)
  /-- `a op b`. -/
  | bin (op : BinOp) (a b : GExpr)
  /-- `a && b`: `b` is evaluated only when `a` is true (Go spec, "Logical operators"). -/
  | and (a b : GExpr)
  /-- A pattern primitive applied to `e`. -/
  | prim (p : Prim) (e : GExpr)
  /-- `func() T { body }()`: a function literal called at once (Go spec, "Function
      literals", "Calls"). Its value is the one its `return` gives. -/
  | funcLit (body : List GStmt)
  /-- A hole: code this fragment does not model (see the module doc). -/
  | ext (i : Nat)
/-- GoCore statements. -/
inductive GStmt
  /-- `x := e` (Go spec, "Short variable declarations"). -/
  | define (x : GVar) (e : GExpr)
  /-- `var x T`, with `T`'s zero value `z` (Go spec, "Variable declarations"). -/
  | varDecl (x : GVar) (z : GVal)
  /-- `x = e` (Go spec, "Assignment statements"). -/
  | assign (x : GVar) (e : GExpr)
  /-- `if c { t } else { f }` (Go spec, "If statements"). -/
  | ifElse (c : GExpr) (t f : List GStmt)
  /-- `{ ss }` (Go spec, "Blocks"). -/
  | block (ss : List GStmt)
  /-- `return e` (Go spec, "Return statements"). -/
  | ret (e : GExpr)
  /-- `panic(msg)` (Go spec, "Handling panics"), with the class of its message. -/
  | panic (c : PanicClass) (msg : String)
  /-- An expression statement, its value discarded; also `_ = e` (Go spec, "Expression
      statements", "Assignment statements"). -/
  | expr (e : GExpr)
end

instance : Inhabited GExpr := ⟨.lit .unit⟩

/-- The state: the local variables (newest first), the package-level ones, and the output
    so far. -/
structure GSt where
  locals : List (GVar × GVal)
  globals : List (String × GVal)
  out : String

/-- Execution: state that survives a stop, so output printed before a panic stays. -/
abbrev GM := ExceptT GStop (StateM GSt)

/-! ## Values -/

/-- Reduce to Go's `int` on a 64-bit target, wrapping around. -/
def wrap64 (x : Int) : Int := x.bmod Int64.size

/-- Go's `==` (Go spec, "Comparison operators"): `int`, `bool` and `string` by value;
    structs (a `std.TupleN` and the empty struct included) field by field, and only
    with a struct of the same type. A sealed value is compared by the `Equal` the
    transpiler generates: the same type, the same active case and equal fields. Go does
    not compile `==` on function values or a comparison of different types, so those
    are `false` here. -/
def geq : GVal → GVal → Bool
  | .int a, .int b => a == b
  | .bool a, .bool b => a == b
  | .str a, .str b => a == b
  | .unit, .unit => true
  | .tuple as, .tuple bs => geqs as bs
  | .struct_ t as, .struct_ u bs => t == u && geqs as bs
  | .sealed t c as, .sealed u d bs => t == u && c == d && geqs as bs
  | .opt none, .opt none => true
  | .opt (some a), .opt (some b) => geq a b
  | _, _ => false
where
  /-- Fields pairwise, both lists of the same length. -/
  geqs : List GVal → List GVal → Bool
    | [], [] => true
    | a :: as, b :: bs => geq a b && geqs as bs
    | _, _ => false

/-- A pattern primitive on a value. On a value of a shape Go's types rule out it gives a
    placeholder (see the module doc). -/
def primVal : Prim → GVal → GVal
  -- `C{}.Unapply(v)` of a generated sealed case with `n` fields.
  | .unapply c 0, .sealed _ c' _ => .bool (c' == c)
  | .unapply _ 0, _ => .bool false
  | .unapply c 1, .sealed _ c' fs => .opt (if c' == c then some (fs.headD .zero) else none)
  | .unapply c (_ + 2), .sealed _ c' fs => .opt (if c' == c then some (.tuple fs) else none)
  | .unapply _ _, _ => .opt none
  | .isDefined, .opt o => .bool o.isSome
  | .isDefined, _ => .bool false
  -- `Value.Get()` of a `None` is the zero value.
  | .get, .opt (some v) => v
  | .get, _ => .zero
  | .field i, .tuple vs | .field i, .struct_ _ vs => vs.getD i .zero
  | .field _, _ => .zero

/-- A binary operator on its operands' values. -/
def binVal : BinOp → GVal → GVal → Except GStop GVal
  | .eq, a, b => .ok (.bool (geq a b))
  | .add, .int x, .int y => .ok (.int (wrap64 (x + y)))
  | .gt, .int x, .int y => .ok (.bool (decide (x > y)))
  | _, _, _ => .error (.stuck "operator on operands of the wrong type")

/-! ## Variables -/

/-- A variable's value: the newest local of that name, else a package-level one. -/
def lookupVar (s : GSt) (x : GVar) : Option GVal :=
  match s.locals.lookup x, x with
  | some v, _ => some v
  | none, .user n => s.globals.lookup n
  | none, .tmp _ => none

/-- Set the newest binding of `x` in an association list, in its place; `none` if there
    is none. -/
def update {α : Type} [DecidableEq α] : List (α × GVal) → α → GVal → Option (List (α × GVal))
  | [], _, _ => none
  | (k, w) :: r, x, v =>
    if k = x then some ((k, v) :: r) else (update r x v).map ((k, w) :: ·)

/-- Assign a variable: the newest local of that name, else a package-level one; `none`
    if there is neither. -/
def updateVar (s : GSt) (x : GVar) (v : GVal) : Option GSt :=
  match update s.locals x v, x with
  | some l, _ => some { s with locals := l }
  | none, .user n => (update s.globals n v).map fun g => { s with globals := g }
  | none, .tmp _ => none

/-! ## Execution -/

/-- Leave a scope entered when the stack had `n` variables: drop the ones declared in
    it, keep the updates of the others. -/
def truncate (s : GSt) (n : Nat) : GSt :=
  { s with locals := s.locals.drop (s.locals.length - n) }

/-- Run `m` in a scope of its own (a block, an `if` branch, a function literal's body):
    its variables are dropped when it ends normally. -/
def withScope (m : GM α) : GM α := do
  let n := (← get).locals.length
  let r ← m
  modify (truncate · n)
  pure r

def asBool : GVal → GM Bool
  | .bool b => pure b
  | _ => throw (.stuck "not a bool")


mutual
/-- Evaluate an expression; `X i` is what hole `i` does. -/
def geval (X : Nat → GM GVal) : GExpr → GM GVal
  | .lit v => pure v
  | .var x => do
    match lookupVar (← get) x with
    | some v => pure v
    | none => throw (.stuck "undeclared variable")
  | .bin op a b => do
    let va ← geval X a
    let vb ← geval X b
    MonadExcept.ofExcept (binVal op va vb)
  | .and a b => do
    if ← asBool (← geval X a) then pure (.bool (← asBool (← geval X b)))
    else pure (.bool false)
  | .prim p e => do pure (primVal p (← geval X e))
  | .funcLit body => do
    let r ← withScope (tryCatch (do gexecs X body; pure none) fun
      | .ret v => pure (some v)
      | e => throw e)
    match r with
    | some v => pure v
    | none => throw (.stuck "function literal ended without a return")
  | .ext i => X i
/-- Execute a statement. -/
def gexec (X : Nat → GM GVal) : GStmt → GM Unit
  | .define x e => do
    let v ← geval X e
    modify fun s => { s with locals := (x, v) :: s.locals }
  | .varDecl x z => modify fun s => { s with locals := (x, z) :: s.locals }
  | .assign x e => do
    let v ← geval X e
    match updateVar (← get) x v with
    | some s => set s
    | none => throw (.stuck "assignment to an undeclared variable")
  | .expr e => discard (geval X e)
  | .ifElse c t f => do
    if ← asBool (← geval X c) then withScope (gexecs X t) else withScope (gexecs X f)
  | .block ss => withScope (gexecs X ss)
  | .ret e => do throw (.ret (← geval X e))
  | .panic c msg => throw (.panic c msg)
/-- Execute statements in order. -/
def gexecs (X : Nat → GM GVal) : List GStmt → GM Unit
  | [] => pure ()
  | s :: ss => do gexec X s; gexecs X ss
end

/-! ## Checks

Small programs run while this file compiles: scoping, short-circuit `&&`, wrapping and
the pattern primitives. -/

/-- Run an expression from empty locals, with no holes. -/
def evalClosed (e : GExpr) : Except GStop GVal × GSt :=
  (geval (fun _ => throw (.stuck "hole")) e).run.run { locals := [], globals := [], out := "" }

private def isOk (r : Except GStop GVal × GSt) (p : GVal → Bool) : Bool :=
  match r.1 with
  | .ok v => p v
  | .error _ => false

-- A variable declared in an `if` branch is gone after it; the outer one is seen again,
-- and an assignment to it in the branch survives.
#guard isOk (evalClosed (.funcLit [
    .define (.user "x") (.lit (.int 1)),
    .ifElse (.lit (.bool true))
      [.assign (.user "x") (.lit (.int 2)), .define (.user "x") (.lit (.int 5))] [],
    .ret (.var (.user "x"))]))
  fun | .int 2 => true | _ => false

-- `false && e` does not evaluate `e` (here an undeclared variable).
#guard isOk (evalClosed (.and (.lit (.bool false)) (.var (.user "nope"))))
  fun | .bool false => true | _ => false

-- int64 wraps around.
#guard isOk (evalClosed (.bin .add (.lit (.int 9223372036854775807)) (.lit (.int 1))))
  fun | .int (-9223372036854775808) => true | _ => false

-- `Rect{}.Unapply` of a `Rect(1, 2)` is `Some((1, 2))`; of a `Circle(5)` it is `None`,
-- whose `Value.Get()` is the zero placeholder.
#guard isOk (evalClosed (.prim (.field 1) (.prim .get (.prim (.unapply "Rect" 2)
    (.lit (.sealed "Shape" "Rect" [.int 1, .int 2]))))))
  fun | .int 2 => true | _ => false
#guard isOk (evalClosed (.prim .get (.prim (.unapply "Rect" 2) (.lit (.sealed "Shape" "Circle" [.int 5])))))
  fun | .zero => true | _ => false

-- A function literal that ends without `return` is stuck.
#guard match (evalClosed (.funcLit [])).1 with
  | .error (.stuck _) => true
  | _ => false

end Gala.GoCore
