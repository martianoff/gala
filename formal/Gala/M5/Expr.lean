/-
  # M5, step 2a: call-free expressions

  docs/FORMAL_SEMANTICS.MD, "M5". The reference lowering of galaspec's call-free
  expressions into GoCore for whole programs (`Gala.GoCore.Full`), and the relations the
  forward simulation is stated with.

  * **Kinds.** The lowering is type-directed, as the transformer is: it is given each
    local's kind (`int`, `bool` or `string`, what the analyzer infers) and accepts an
    operator only on operands of the kinds Go type-checks it on. A program Go rejects,
    such as `x == "a"` with `x` an `int`, has no lowering, so the claim never speaks of
    a galaspec answer (here `false`) Go would not give.
  * **Values** (`VRel`). An `int`, `bool` or `string` of galaspec is GoCore's value of
    Go's type `int`, `bool` or `string`, or an untyped constant of that kind: a literal
    is an untyped constant in Go until an operator or a declaration gives it a type, and
    a comparison's result is an untyped boolean (Go spec, "Constants", "Comparison
    operators"). An untyped `int` is in int64's range, as every galaspec `int` is.
  * **Locals** (`LocRel`). Every local the lowering is told of is bound in galaspec and in
    GoCore, to related values of its kind; GoCore's is typed, since a declaration gives
    an untyped constant its default type.
  * **The lowering** (`lowerE`) is what the transformer emits: the Go literal (a negative
    one as `-` of its magnitude, as Go has no negative literals), `true` and `false` as
    Go's predeclared names, the same operators. It is partial: `none` outside this step.
  * **Constant expressions.** galaspec folds them before a run (`Gala/Const.lean`), so
    the expressions a run evaluates have none: `NoConstOp`.
  * **Evaluation order.** A call-free expression has no effects other than its result
    or a panic, and its parts run left to right in GoCore as in galaspec. Which of two
    failing operands panics is the only thing the order decides; Go fixes the order only
    among calls, and gc evaluates such operands left to right (the GoCore fixtures
    `panic_order_divide` and `panic_order_shift` pin it). The claim is for GoCore's
    order; M5's later steps take the order as a parameter.
-/
import Gala.M5.SpecPure
import Gala.GoCore.Eval

namespace Gala.M5

open Gala.GoCore.Full (Ty IntKind UKind)

/-- The kinds of value this step lowers. -/
inductive Kind
  | int | bool | str
  deriving DecidableEq, Repr

/-- A galaspec value's kind. -/
def valKind : Spec.Val → Option Kind
  | .int _ => some .int
  | .bool _ => some .bool
  | .str _ => some .str
  | _ => none

/-- Go's `int`. -/
abbrev intTy : Ty := .int .int

/-- **Values.** -/
inductive VRel : Spec.Val → GoCore.Full.Val → Prop
  | int (n : Int) : VRel (.int n) (.int intTy n)
  | intU (n : Int) : IntKind.int.represents n = true → VRel (.int n) (.int (.untyped .int) n)
  | bool (b : Bool) : VRel (.bool b) (.bool .bool b)
  | boolU (b : Bool) : VRel (.bool b) (.bool (.untyped .bool) b)
  | str (s : String) : VRel (.str s) (.str .string s)
  | strU (s : String) : VRel (.str s) (.str (.untyped .string) s)

/-- An untyped integer constant. -/
def UInt : GoCore.Full.Val → Prop
  | .int (.untyped .int) _ => True
  | _ => False

/-- A binding's value, through galaspec's heap for a `var`. -/
def bval (heap : Array Spec.Val) (b : Spec.Binding) : Option Spec.Val :=
  match b.val with
  | .ref n => heap[n]?
  | v => some v

/-- **Locals.** Each local the lowering is told of, with its kind, is bound in galaspec
    and in GoCore to related values of that kind; GoCore's is typed. -/
def LocRel (κ : String → Option Kind) (c : Spec.Core) (cells : Array GoCore.Full.Val)
    (env : GoCore.Full.Env) : Prop :=
  ∀ x k, κ x = some k → ∃ b cell v w, Spec.lookup c.locals x = some b ∧ bval c.heap b = some v ∧
    env.lookup x = some cell ∧ cells[cell]? = some w ∧ VRel v w ∧ valKind v = some k ∧ ¬ UInt w ∧
    (∀ t b, w ≠ .bool (.untyped t) b) ∧ (∀ t s, w ≠ .str (.untyped t) s)

/-- `true` and `false` mean Go's predeclared constants where `env` is: no variable and no
    declaration of the package shadows them. -/
def Predeclared (c : GoCore.Full.Ctx) (env : GoCore.Full.Env) : Prop :=
  ∀ x ∈ ["true", "false"], env.lookup x = none ∧
    ∃ p, c.pkgs.get? env.pkg = some p ∧ p.objs.get? x = none ∧ GoCore.Full.nameOwner c env x = none

def lowerLit : Spec.Lit → Kind × GoCore.Full.Expr
  | .int v => (.int, if v < 0 then .unary "-" (.intLit (-v)) else .intLit v)
  | .bool b => (.bool, .ident (if b then "true" else "false"))
  | .str s => (.str, .strLit s)

/-- The kind of `op` on operands of kinds `kl` and `kr`, where Go type-checks it and this
    step lowers it. -/
def binaryKind (op : String) (kl kr : Kind) : Option Kind :=
  match op, kl, kr with
  | "&&", .bool, .bool | "||", .bool, .bool => some .bool
  | "==", _, _ | "!=", _, _ => if kl = kr then some .bool else none
  | "<", .int, .int | "<=", .int, .int | ">", .int, .int | ">=", .int, .int => some .bool
  | "+", .int, .int | "-", .int, .int | "*", .int, .int | "/", .int, .int | "%", .int, .int =>
    some .int
  | "+", .str, .str => some .str
  | _, _, _ => none

def unaryKind (op : String) (k : Kind) : Option Kind :=
  match op, k with
  | "-", .int | "+", .int | "^", .int => some .int
  | "!", .bool => some .bool
  | _, _ => none

/-- **The lowering** of a call-free expression, with its kind; `none` outside this step. -/
def lowerE (κ : String → Option Kind) : Spec.Expr → Option (Kind × GoCore.Full.Expr)
  | .lit l => some (lowerLit l)
  | .name x => if x = "_" then none else (κ x).map (·, .ident x)
  | .paren e => (lowerE κ e).map fun (k, le) => (k, .paren le)
  | .unary op e => do
    let (k, le) ← lowerE κ e
    let k' ← unaryKind op k
    pure (k', .unary op le)
  | .binary op l r => do
    let (kl, ll) ← lowerE κ l
    let (kr, lr) ← lowerE κ r
    let k ← binaryKind op kl kr
    pure (k, .binary op ll lr)
  | _ => none

/-- No operator node is a constant expression: what `foldConstants` leaves. -/
def NoConstOp : Spec.Expr → Prop
  | .paren e => NoConstOp e
  | .unary op e => Spec.constEval (.unary op e) = none ∧ NoConstOp e
  | .binary op l r => Spec.constEval (.binary op l r) = none ∧ NoConstOp l ∧ NoConstOp r
  | _ => True

/-- How a galaspec panic and a GoCore panic agree: GoCore's is a runtime error whose text
    is galaspec's message. -/
def PanicRel : Spec.PanicClass → String → GoCore.Full.Val → Prop
  | c, m, .str _ t => t = m ∧ (c = .divide ∨ c = .other)
  | _, _, _ => False

/-- What GoCore's evaluation of `le` at every depth from some point on gives, against
    galaspec's result `r`: a related value of kind `k`, or the same panic; anything else
    galaspec gives (it ran out of fuel, or stopped `stuck` or `unsupported`) claims
    nothing. GoCore's state is left as it was. -/
def Matches (cx : GoCore.Full.Ctx) (env : GoCore.Full.Env) (g : GoCore.Full.St) (le : GoCore.Full.Expr)
    (k : Kind) (r : Except Spec.Stop Spec.Val) (typed : Prop) : Prop :=
  match r with
  | .ok v => ∃ m, ∀ n ≥ m, ∃ w, (GoCore.Full.evalExpr cx n env le).run.run g = (.ok w, g) ∧
      VRel v w ∧ valKind v = some k ∧ (typed → ¬ UInt w)
  | .error (.panic c msg) => ∃ m, ∀ n ≥ m, ∃ pv,
      (GoCore.Full.evalExpr cx n env le).run.run g = (.error (.panic pv true), g) ∧ PanicRel c msg pv
  | _ => True

/-! ## GoCore's evaluation of the lowered forms -/

section GoCoreRuns
open Gala.GoCore.Full

theorem grun_bind (x : GM α) (f : α → GM β) (g : St) :
    (x >>= f).run.run g = match x.run.run g with
      | (.ok a, t) => (f a).run.run t
      | (.error e, t) => (.error e, t) := by
  simp only [ExceptT.run_bind, StateT.run_bind]
  rcases hx : x.run.run g with ⟨r, t⟩
  cases r <;> rfl

theorem evalExpr_intLit (c : Ctx) (n : Nat) (env : Env) (v : Int) (g : St) :
    (evalExpr c (n + 1) env (.intLit v)).run.run g = (.ok (.int (.untyped .int) v), g) := by
  simp [evalExpr]; rfl

theorem evalExpr_strLit (c : Ctx) (n : Nat) (env : Env) (s : String) (g : St) :
    (evalExpr c (n + 1) env (.strLit s)).run.run g = (.ok (.str (.untyped .string) s), g) := by
  simp [evalExpr]; rfl

theorem evalExpr_paren (c : Ctx) (n : Nat) (env : Env) (x : Expr) :
    evalExpr c (n + 1) env (.paren x) = evalExpr c n env x := by
  simp [evalExpr]

theorem evalExpr_unary (c : Ctx) (n : Nat) (env : Env) (op : String) (x : Expr) (h : op ≠ "&") :
    evalExpr c (n + 1) env (.unary op x) = (do unary c n op (← evalExpr c n env x)) := by
  simp [evalExpr, h]

theorem evalExpr_binary (c : Ctx) (n : Nat) (env : Env) (op : String) (x y : Expr)
    (h1 : op ≠ "&&") (h2 : op ≠ "||") :
    evalExpr c (n + 1) env (.binary op x y) =
      (do let a ← evalExpr c n env x; let b ← evalExpr c n env y; binary c n op a b) := by
  simp [evalExpr, h1, h2]

theorem evalExpr_local (c : Ctx) (n : Nat) (env : Env) (x : String) (cell : Nat) (w : Val)
    (g : St) (hl : env.lookup x = some cell) (hw : g.cells[cell]? = some w)
    (hp : ∀ what, w ≠ .poison what) (hx : x ≠ "_") :
    (evalExpr c (n + 1) env (.ident x)).run.run g = (.ok w, g) := by
  simp [evalExpr, hl, loadCell, hx]
  show (match g.cells[cell]? with
    | some (.poison what) => (throw (Stop.unsupported what) : GM Val)
    | some v => pure v
    | none => stuck "no such variable").run.run g = _
  rw [hw]
  cases w <;> first | rfl | exact absurd rfl (hp _)

/-! ### Integers: galaspec's `wrap` is Go's `int` -/

theorem wrap_eq (x : Int) : Spec.wrap x = IntKind.int.wrap x := by
  simp [Spec.wrap, IntKind.wrap, IntKind.signed, IntKind.bits]

theorem minInt_eq : Spec.minInt = -9223372036854775808 := rfl
theorem maxInt_eq : Spec.maxInt = 9223372036854775807 := rfl

theorem represents_of_range {v : Int} (h1 : Spec.minInt ≤ v) (h2 : v ≤ Spec.maxInt) :
    IntKind.int.represents v = true := by
  rw [minInt_eq] at h1; rw [maxInt_eq] at h2
  simp only [IntKind.represents, IntKind.wrap, IntKind.signed, IntKind.bits, ite_true, beq_iff_eq]
  apply Int.bmod_eq_of_le <;> simp <;> omega

theorem underlying_int (c : Ctx) (n : Nat) (g : St) :
    (underlying c (n + 1) intTy).run.run g = (.ok intTy, g) := by
  simp [underlying]; rfl

/-! ### Unary operators -/

theorem unary_sim (cx : Ctx) (n : Nat) (op : String) (v : Spec.Val) (w : Val) (k k' : Kind)
    (g : St) (c : Spec.Core) (hv : VRel v w) (hk : valKind v = some k) (hu : unaryKind op k = some k')
    (hnu : ¬ UInt w) :
    ∃ v' w', ((Spec.unop op v).run.run c).1 = .ok v' ∧
      (unary cx (n + 2) op w).run.run g = (.ok w', g) ∧ VRel v' w' ∧ valKind v' = some k' ∧ ¬ UInt w' := by
  rcases hv with x | ⟨x, _⟩ | b | b | s | s <;> simp only [valKind, Option.some.injEq] at hk <;> subst hk
  · have hop : (op = "-" ∧ k' = .int) ∨ (op = "+" ∧ k' = .int) ∨ (op = "^" ∧ k' = .int) := by
      unfold unaryKind at hu; split at hu <;> simp_all
    rcases hop with ⟨rfl, rfl⟩ | ⟨rfl, rfl⟩ | ⟨rfl, rfl⟩
    · refine ⟨_, _, rfl, ?_, .int (Spec.wrap (-x)), rfl, by simp [UInt]⟩
      simp [unary, grun_bind, underlying_int, wrap_eq]; rfl
    · refine ⟨_, _, rfl, ?_, .int x, rfl, by simp [UInt]⟩
      simp [unary]; rfl
    · refine ⟨_, _, rfl, ?_, .int (Spec.wrap (-x - 1)), rfl, by simp [UInt]⟩
      simp [unary, grun_bind, underlying_int, wrap_eq, bitNot]; rfl
  · exact absurd trivial hnu
  all_goals first
    | (exfalso; unfold unaryKind at hu; split at hu <;> simp_all; done)
    | (have hop : op = "!" ∧ k' = .bool := by unfold unaryKind at hu; split at hu <;> simp_all
       obtain ⟨rfl, rfl⟩ := hop
       first
        | exact ⟨_, _, rfl, by simp [unary]; rfl, .bool _, rfl, by simp [UInt]⟩
        | exact ⟨_, _, rfl, by simp [unary]; rfl, .boolU _, rfl, by simp [UInt]⟩)

/-! ### Binary operators -/

theorem untyped_beq (k : UKind) (t : Ty) (h : ∀ k', t ≠ .untyped k') : (Ty.untyped k == t) = false := by
  cases t <;> first | rfl | exact absurd rfl (h _)

theorem underlying_bool (c : Ctx) (n : Nat) (g : St) :
    (underlying c (n + 1) .bool).run.run g = (.ok .bool, g) := by
  simp [underlying]; rfl

theorem underlying_string (c : Ctx) (n : Nat) (g : St) :
    (underlying c (n + 1) .string).run.run g = (.ok .string, g) := by
  simp [underlying]; rfl

/-- An untyped constant meeting an operand of Go's `int`, `bool` or `string` takes its
    type (Go spec, "Operators"). -/
theorem convert_uint (c : Ctx) (n : Nat) (x : Int) (g : St) (hr : IntKind.int.represents x = true) :
    (convert c (n + 2) intTy (.int (.untyped .int) x)).run.run g = (.ok (.int intTy x), g) := by
  simp [convert, grun_bind, underlying_int, hr, Val.ty, untyped_beq]; rfl

theorem convert_ubool (c : Ctx) (n : Nat) (b : Bool) (g : St) :
    (convert c (n + 2) .bool (.bool (.untyped .bool) b)).run.run g = (.ok (.bool .bool b), g) := by
  simp [convert, grun_bind, underlying_bool, Val.ty, untyped_beq]; rfl

theorem convert_ustr (c : Ctx) (n : Nat) (s : String) (g : St) :
    (convert c (n + 2) .string (.str (.untyped .string) s)).run.run g = (.ok (.str .string s), g) := by
  simp [convert, grun_bind, underlying_string, Val.ty, untyped_beq]; rfl

example (cx : Ctx) (n : Nat) (x y : Int) (g : St) (hr : IntKind.int.represents x = true) :
    (binary cx (n + 3) "+" (.int (.untyped .int) x) (.int intTy y)).run.run g =
      (.ok (.int intTy (IntKind.int.wrap (x + y))), g) := by
  simp [binary, grun_bind, underlying_int, intOp, Val.isFloat, convert_uint _ _ _ _ hr, Val.ty]
  rfl

end GoCoreRuns

end Gala.M5
