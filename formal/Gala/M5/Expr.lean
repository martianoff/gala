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
    or a panic. Its run-time panics are an integer division by zero and a negative shift
    count, each with one text. The claim is proved for GoCore's order, left to right,
    which is galaspec's; where two failing operands panic differently (a division and a
    shift), the first in that order decides on both sides. The GoCore fixtures
    `panic_order_divide` and `panic_order_shift` pin gc 1.25.5 to the same order on the
    shapes they test; that gc's order agrees in general (with its `&&`/`||` temporaries)
    is carried to step 3 (`Gala/M5/StmtOrder.lean`).
  * **Shifts.** A shift's left operand is a typed `int`: the lowering rejects a literal
    there, as Go gives an untyped constant shifted by a variable count the type its
    context would give it, which this lowering does not track. The count is a typed `int`
    or an untyped constant; a negative constant count, which Go does not compile, is not
    lowered either. A galaspec `int` related to Go's is in int64's range (`VRel`).
-/
import Gala.M5.SpecPure
import Gala.M5.Bits
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
  | int (n : Int) : IntKind.int.represents n = true → VRel (.int n) (.int intTy n)
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
    declaration of the package shadows them; and `env` is not a constant declaration's
    (no `iota`), where a shift of an untyped constant is a constant one. -/
def Predeclared (c : GoCore.Full.Ctx) (env : GoCore.Full.Env) : Prop :=
  env.iota = none ∧ ∀ x ∈ ["true", "false"], env.lookup x = none ∧
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
  | "&", .int, .int | "|", .int, .int | "^", .int, .int | "&^", .int, .int => some .int
  | "<<", .int, .int | ">>", .int, .int => some .int
  | "+", .str, .str => some .str
  | "<", .str, .str | "<=", .str, .str | ">", .str, .str | ">=", .str, .str => some .bool
  | _, _, _ => none

def unaryKind (op : String) (k : Kind) : Option Kind :=
  match op, k with
  | "-", .int | "+", .int | "^", .int => some .int
  | "!", .bool => some .bool
  | _, _ => none

/-- A literal, through parentheses: the only expressions whose GoCore value may be an
    untyped integer. -/
def isLitE : Spec.Expr → Bool
  | .lit _ => true
  | .paren e => isLitE e
  | _ => false

/-- A shift. -/
def isShift (op : String) : Bool := op == "<<" || op == ">>"

/-- A negative integer literal, through parentheses: a shift count Go rejects. -/
def negLitE : Spec.Expr → Bool
  | .lit (.int v) => decide (v < 0)
  | .paren e => negLitE e
  | _ => false

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
    -- A shift of an untyped constant takes the type its context would give the constant
    -- (Go spec, "Operators"), which this lowering does not track; a negative constant
    -- count does not compile.
    if isShift op && (isLitE l || negLitE r) then none else pure (k, .binary op ll lr)
  | _ => none

/-- No operator node is a constant expression: what `foldConstants` leaves. -/
def NoConstOp : Spec.Expr → Prop
  | .paren e => NoConstOp e
  | .unary op e => Spec.constEval (.unary op e) = none ∧ NoConstOp e
  | .binary op l r => Spec.constEval (.binary op l r) = none ∧ NoConstOp l ∧ NoConstOp r
  | _ => True

/-- How a galaspec panic and a GoCore panic agree in this step, whose run-time panics are
    an integer division by zero (galaspec's class `divide`) and a negative shift count
    (class `other`): GoCore's is the runtime error with the same text. -/
def PanicRel : Spec.PanicClass → String → GoCore.Full.Val → Prop
  | c, m, .str .string t =>
    ((c = .divide ∧ m = "runtime error: integer divide by zero") ∨
      (c = .other ∧ m = "runtime error: negative shift amount")) ∧ t = m
  | _, _, _ => False


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
  simp [evalExpr]

theorem evalExpr_binary (c : Ctx) (n : Nat) (env : Env) (op : String) (x y : Expr)
    (h1 : op ≠ "&&") (h2 : op ≠ "||") (h3 : env.iota = none) :
    evalExpr c (n + 1) env (.binary op x y) =
      (do let a ← evalExpr c n env x; let b ← evalExpr c n env y; binary c n op a b) := by
  simp [evalExpr, h3]

theorem evalExpr_local (c : Ctx) (n : Nat) (env : Env) (x : String) (cell : Nat) (w : Val)
    (g : St) (hl : env.lookup x = some cell) (hw : g.cells[cell]? = some w)
    (hp : ∀ what, w ≠ .poison what) (hx : x ≠ "_") :
    (evalExpr c (n + 1) env (.ident x)).run.run g = (.ok w, g) := by
  simp [evalExpr, hl, loadCell]
  show (match g.cells[cell]? with
    | some (.poison what) => (throw (Stop.unsupported what) : GM Val)
    | some v => pure v
    | none => stuck "no such variable").run.run g = _
  rw [hw]
  cases w <;> first | rfl | exact absurd rfl (hp _)

/-! ### Integers: galaspec's `wrap` is Go's `int` -/

theorem wrap_eq (x : Int) : Spec.wrap x = IntKind.int.wrap x := by
  simp [Spec.wrap, IntKind.wrap, IntKind.signed, IntKind.bits]

theorem represents_kwrap (x : Int) : IntKind.int.represents (IntKind.int.wrap x) = true := by
  simp [IntKind.represents, IntKind.wrap, IntKind.signed, IntKind.bits, Int.bmod_bmod]

theorem represents_swrap (x : Int) : IntKind.int.represents (Spec.wrap x) = true := by
  rw [wrap_eq]; exact represents_kwrap x

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
  rcases hv with ⟨x, hx⟩ | ⟨x, _⟩ | b | b | s | s <;> simp only [valKind, Option.some.injEq] at hk <;> subst hk
  · have hop : (op = "-" ∧ k' = .int) ∨ (op = "+" ∧ k' = .int) ∨ (op = "^" ∧ k' = .int) := by
      unfold unaryKind at hu; split at hu <;> simp_all
    rcases hop with ⟨rfl, rfl⟩ | ⟨rfl, rfl⟩ | ⟨rfl, rfl⟩
    · refine ⟨_, _, rfl, ?_, .int _ (represents_swrap _), rfl, by simp [UInt]⟩
      simp [unary, grun_bind, underlying_int, wrap_eq]; rfl
    · refine ⟨_, _, rfl, ?_, .int x hx, rfl, by simp [UInt]⟩
      simp [unary]; rfl
    · refine ⟨_, _, rfl, ?_, .int _ (represents_swrap _), rfl, by simp [UInt]⟩
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
  simp [convert, grun_bind, underlying_int, hr, Val.ty, Val.materializeTyped, untyped_beq]; rfl

theorem convert_ubool (c : Ctx) (n : Nat) (b : Bool) (g : St) :
    (convert c (n + 2) .bool (.bool (.untyped .bool) b)).run.run g = (.ok (.bool .bool b), g) := by
  simp [convert, grun_bind, underlying_bool, Val.ty, Val.materializeTyped, untyped_beq]; rfl

theorem convert_ustr (c : Ctx) (n : Nat) (s : String) (g : St) :
    (convert c (n + 2) .string (.str (.untyped .string) s)).run.run g = (.ok (.str .string s), g) := by
  simp [convert, grun_bind, underlying_string, Val.ty, Val.materializeTyped, untyped_beq]; rfl

/-- An untyped operand next to a typed one of the same kind is converted first (Go spec,
    "Operators"), so the operator then runs on two typed values. -/
theorem binary_uint_left (cx : Ctx) (n : Nat) (op : String) (x y : Int) (g : St)
    (hr : IntKind.int.represents x = true) (hs : (op == "<<" || op == ">>") = false) :
    (binary cx (n + 3) op (.int (.untyped .int) x) (.int intTy y)).run.run g =
      (binary cx (n + 3) op (.int intTy x) (.int intTy y)).run.run g := by
  simp only [binary, hs]
  simp [convert_uint _ _ _ _ hr, Val.ty, Val.materializeTyped, grun_bind]

theorem binary_uint_right (cx : Ctx) (n : Nat) (op : String) (x y : Int) (g : St)
    (hr : IntKind.int.represents y = true) (hs : (op == "<<" || op == ">>") = false) :
    (binary cx (n + 3) op (.int intTy x) (.int (.untyped .int) y)).run.run g =
      (binary cx (n + 3) op (.int intTy x) (.int intTy y)).run.run g := by
  simp only [binary, hs]
  simp [convert_uint _ _ _ _ hr, Val.ty, Val.materializeTyped, grun_bind]

theorem binary_ubool_left (cx : Ctx) (n : Nat) (op : String) (x y : Bool) (g : St)
    (hs : (op == "<<" || op == ">>") = false) :
    (binary cx (n + 3) op (.bool (.untyped .bool) x) (.bool .bool y)).run.run g =
      (binary cx (n + 3) op (.bool .bool x) (.bool .bool y)).run.run g := by
  simp only [binary, hs]
  simp [convert_ubool, Val.ty, Val.materializeTyped, grun_bind]

theorem binary_ubool_right (cx : Ctx) (n : Nat) (op : String) (x y : Bool) (g : St)
    (hs : (op == "<<" || op == ">>") = false) :
    (binary cx (n + 3) op (.bool .bool x) (.bool (.untyped .bool) y)).run.run g =
      (binary cx (n + 3) op (.bool .bool x) (.bool .bool y)).run.run g := by
  simp only [binary, hs]
  simp [convert_ubool, Val.ty, Val.materializeTyped, grun_bind]

theorem binary_ustr_left (cx : Ctx) (n : Nat) (op : String) (x y : String) (g : St)
    (hs : (op == "<<" || op == ">>") = false) :
    (binary cx (n + 3) op (.str (.untyped .string) x) (.str .string y)).run.run g =
      (binary cx (n + 3) op (.str .string x) (.str .string y)).run.run g := by
  simp only [binary, hs]
  simp [convert_ustr, Val.ty, Val.materializeTyped, grun_bind]

theorem binary_ustr_right (cx : Ctx) (n : Nat) (op : String) (x y : String) (g : St)
    (hs : (op == "<<" || op == ">>") = false) :
    (binary cx (n + 3) op (.str .string x) (.str (.untyped .string) y)).run.run g =
      (binary cx (n + 3) op (.str .string x) (.str .string y)).run.run g := by
  simp only [binary, hs]
  simp [convert_ustr, Val.ty, Val.materializeTyped, grun_bind]

/-- A galaspec result and a GoCore run agree: a related value of kind `k`, or the same
    panic, with GoCore's state unchanged; any other galaspec result claims nothing. -/
def RRel (k : Kind) (g : St) : Except Spec.Stop Spec.Val → Except Stop Val × St → Prop
  | .ok v, r => ∃ w, r = (.ok w, g) ∧ VRel v w ∧ valKind v = some k ∧ ¬ UInt w
  | .error (.panic c m), r => ∃ pv, r = (.error (.panic pv true), g) ∧ PanicRel c m pv
  | _, _ => True

theorem cmp_lt (x y : Int) : (compare x y == .lt) = decide (x < y) := by
  by_cases h : x < y
  · simp [h, Int.compare_eq_lt.mpr h]
  · have : compare x y ≠ .lt := fun e => h (Int.compare_eq_lt.mp e)
    simp [h]; exact this

theorem cmp_gt (x y : Int) : (compare x y == .gt) = decide (x > y) := by
  by_cases h : x > y
  · simp [h, Int.compare_eq_gt.mpr h]
  · have : compare x y ≠ .gt := fun e => h (Int.compare_eq_gt.mp e)
    simp [h]; exact this

theorem cmp_le (x y : Int) : (compare x y != .gt) = decide (x ≤ y) := by
  have h := cmp_gt x y
  simp only [bne, h]
  by_cases hxy : x ≤ y
  · simp [hxy, Int.not_lt.mpr hxy]
  · simp [hxy, Int.not_le.mp hxy]

theorem cmp_ge (x y : Int) : (compare x y != .lt) = decide (x ≥ y) := by
  have h := cmp_lt x y
  simp only [bne, h]
  by_cases hxy : x ≥ y
  · simp [hxy, Int.not_lt.mpr hxy]
  · simp [hxy, Int.not_le.mp hxy]

theorem intTy_beq : (intTy == intTy) = true := rfl

/-- **Integer operators** on two typed `int`s, against galaspec's. -/
theorem binary_int_typed (cx : Ctx) (n : Nat) (op : String) (x y : Int) (g : St) (c : Spec.Core)
    (k : Kind) (hk : binaryKind op .int .int = some k) (hsh : isShift op = false) :
    RRel k g ((Spec.binop op (.int x) (.int y)).run.run c).1
      ((binary cx (n + 3) op (.int intTy x) (.int intTy y)).run.run g) := by
  have hop : op = "==" ∨ op = "!=" ∨ op = "<" ∨ op = "<=" ∨ op = ">" ∨ op = ">=" ∨
      op = "+" ∨ op = "-" ∨ op = "*" ∨ op = "/" ∨ op = "%" ∨ op = "&" ∨ op = "|" ∨ op = "^" ∨
      op = "&^" := by
    unfold binaryKind at hk; split at hk <;> simp_all [isShift]
  rcases hop with rfl | rfl | rfl | rfl | rfl | rfl | rfl | rfl | rfl | rfl | rfl | rfl | rfl | rfl | rfl <;>
    simp only [binaryKind, ite_true, Option.some.injEq] at hk <;> subst hk
  all_goals (first | rw [binop_and] | rw [binop_or] | rw [binop_xor] | rw [binop_andNot] | skip)
  all_goals simp [Spec.binop, binary, grun_bind, underlying_int, intOp, cmpOp, Val.ty, Val.materializeTyped,
    equal, Val.dyn, Ty.isUntyped, runtimePanic, RRel, Spec.valEq, cmp_lt, cmp_gt,
    cmp_le, cmp_ge, wrap_eq]
  all_goals first
    | exact ⟨_, rfl, .boolU _, rfl, by simp [UInt]⟩
    | exact ⟨_, rfl, .int _ (represents_kwrap _), rfl, by simp [UInt]⟩
    | (by_cases hy : y = 0
       · simp only [hy, ite_true]
         exact ⟨_, rfl, .inl ⟨rfl, rfl⟩, rfl⟩
       · simp only [hy, ite_false]
         exact ⟨_, rfl, .int _ (represents_kwrap _), rfl, by simp [UInt]⟩)

/-- `==` and `!=` on two booleans of one type: Go's `bool`, or two untyped ones (two
    comparisons' results). -/
theorem binary_bool_same (cx : Ctx) (n : Nat) (op : String) (t : Ty) (x y : Bool) (g : St)
    (c : Spec.Core) (k : Kind) (ht : t = .bool ∨ t = .untyped .bool)
    (hk : binaryKind op .bool .bool = some k) (h1 : op ≠ "&&") (h2 : op ≠ "||") :
    RRel k g ((Spec.binop op (.bool x) (.bool y)).run.run c).1
      ((binary cx (n + 3) op (.bool t x) (.bool t y)).run.run g) := by
  have hop : op = "==" ∨ op = "!=" := by
    unfold binaryKind at hk; split at hk <;> simp_all
  rcases hop with rfl | rfl <;> simp only [binaryKind, ite_true, Option.some.injEq] at hk <;> subst hk <;>
    rcases ht with rfl | rfl
  all_goals simp [Spec.binop, binary, Val.ty, Val.materializeTyped, equal, Val.dyn, Ty.isUntyped,
    RRel, Spec.valEq]
  all_goals first
    | exact ⟨_, rfl, .boolU _, rfl, by simp [UInt]⟩
    | (simp [show (Ty.bool == Ty.bool) = true from rfl]; exact ⟨_, rfl, .boolU _, rfl, by simp [UInt]⟩)

/-! String ordering: GoCore's `compare` on `String` is code-point order, which equals Go's
    byte order on UTF-8 (GoCore's fidelity, tested); galaspec's `<` is the same order. -/

theorem scmp_lt (x y : String) : (compare x y == .lt) = decide (x < y) := by
  show (compareOfLessAndEq x y == .lt) = _
  unfold compareOfLessAndEq
  by_cases h : x < y
  · simp [h]
  · by_cases he : x = y <;> simp [h, he]

theorem scmp_gt (x y : String) : (compare x y == .gt) = decide (y < x) := by
  show (compareOfLessAndEq x y == .gt) = _
  have := compareOfLessAndEq_eq_gt String.le_antisymm String.le_total String.not_le x y
  by_cases h : y < x
  · simp_all
  · simp only [h, decide_false, beq_eq_false_iff_ne, ne_eq]
    exact fun e => h (this.mp e)

theorem scmp_le (x y : String) : (compare x y != .gt) = decide (x ≤ y) := by
  have h := scmp_gt x y
  simp only [bne, h]
  by_cases hxy : y < x
  · simp [hxy]
  · simp [hxy]; exact String.not_lt.mp hxy

theorem scmp_ge (x y : String) : (compare x y != .lt) = decide (y ≤ x) := by
  have h := scmp_lt x y
  simp only [bne, h]
  by_cases hxy : x < y
  · simp [hxy]
  · simp [hxy]; exact String.not_lt.mp hxy

/-- `+`, `==`, `!=` and the orderings on two strings of one type: Go's `string`, or two
    untyped ones. -/
theorem binary_str_same (cx : Ctx) (n : Nat) (op : String) (t : Ty) (x y : String) (g : St)
    (c : Spec.Core) (k : Kind) (ht : t = .string ∨ t = .untyped .string)
    (hk : binaryKind op .str .str = some k) :
    RRel k g ((Spec.binop op (.str x) (.str y)).run.run c).1
      ((binary cx (n + 3) op (.str t x) (.str t y)).run.run g) := by
  have hop : op = "==" ∨ op = "!=" ∨ op = "+" ∨ op = "<" ∨ op = "<=" ∨ op = ">" ∨ op = ">=" := by
    unfold binaryKind at hk; split at hk <;> simp_all
  rcases hop with rfl | rfl | rfl | rfl | rfl | rfl | rfl <;>
    simp only [binaryKind, ite_true, Option.some.injEq] at hk <;> subst hk <;> rcases ht with rfl | rfl
  all_goals simp [Spec.binop, binary, Val.ty, Val.materializeTyped, equal, Val.dyn, Ty.isUntyped,
    RRel, Spec.valEq, cmpOp, scmp_lt, scmp_gt, scmp_le, scmp_ge]
  all_goals first
    | exact ⟨_, rfl, .boolU _, rfl, by simp [UInt]⟩
    | exact ⟨_, rfl, .str _, rfl, by simp [UInt]⟩
    | exact ⟨_, rfl, .strU _, rfl, by simp [UInt]⟩
    | (simp [show (Ty.string == Ty.string) = true from rfl]; exact ⟨_, rfl, .boolU _, rfl, by simp [UInt]⟩)

theorem evalExpr_and (c : Ctx) (n : Nat) (env : Env) (x y : Expr) :
    evalExpr c (n + 1) env (.binary "&&" x y) = (do
      let a ← evalExpr c n env x
      if ← asBool a then
        let b ← evalExpr c n env y
        pure (.bool (boolType a b) (← asBool b))
      else pure a) := by
  simp [evalExpr]

theorem evalExpr_or (c : Ctx) (n : Nat) (env : Env) (x y : Expr) :
    evalExpr c (n + 1) env (.binary "||" x y) = (do
      let a ← evalExpr c n env x
      if ← asBool a then pure a
      else
        let b ← evalExpr c n env y
        pure (.bool (boolType a b) (← asBool b))) := by
  simp [evalExpr]

/-- `true` and `false`, Go's predeclared constants, where nothing shadows them. -/
theorem evalExpr_predeclared (c : Ctx) (n : Nat) (env : Env) (b : Bool) (g : St)
    (hp : Predeclared c env) :
    (evalExpr c (n + 2) env (.ident (if b then "true" else "false"))).run.run g =
      (.ok (.bool (.untyped .bool) b), g) := by
  cases b
  · obtain ⟨hl, p, hpk, hobj, hown⟩ := hp.2 "false" (by simp)
    simp only [Std.HashMap.get?_eq_getElem?] at hpk hobj
    simp [evalExpr, hl, hown, evalName, pkgCtx, hpk, hobj]
    rfl
  · obtain ⟨hl, p, hpk, hobj, hown⟩ := hp.2 "true" (by simp)
    simp only [Std.HashMap.get?_eq_getElem?] at hpk hobj
    simp [evalExpr, hl, hown, evalName, pkgCtx, hpk, hobj]
    rfl

/-- A shift on `int`s takes `int`s. -/
theorem binaryKind_shift {op : String} {k₁ k₂ k : Kind} (h : binaryKind op k₁ k₂ = some k)
    (hs : isShift op = true) : k₁ = .int ∧ k₂ = .int ∧ k = .int := by
  simp only [isShift, Bool.or_eq_true, beq_iff_eq] at hs
  rcases hs with rfl | rfl <;> (unfold binaryKind at h; split at h <;> simp_all)

/-- An `int` of Go's is in int64's range (the converse of `represents_of_range`). -/
theorem represents_bounds {x : Int} (hx : IntKind.int.represents x = true) :
    -9223372036854775808 ≤ x ∧ x < 9223372036854775808 := by
  simp only [IntKind.represents, IntKind.wrap, IntKind.signed, IntKind.bits, ite_true,
    beq_iff_eq] at hx
  have h1 := Int.le_bmod (x := x) (m := 2 ^ 64) (by decide)
  have h2 := Int.bmod_lt (x := x) (m := 2 ^ 64) (by decide)
  rw [hx] at h1 h2
  constructor <;> omega

/-- An `int` in Go's range, divided by `2 ^ s` with `s ≥ 64`, is `-1` or `0` by its sign. -/
theorem ediv_big (x : Int) (hx : IntKind.int.represents x = true) (s : Nat) (hs : 64 ≤ s) :
    x / 2 ^ s = if x < 0 then -1 else 0 := by
  obtain ⟨h1, h2⟩ := represents_bounds hx
  have hp : 18446744073709551616 ≤ (2 : Int) ^ s := by
    have := Nat.pow_le_pow_right (n := 2) (by decide) hs
    have h : ((2 ^ 64 : Nat) : Int) ≤ ((2 ^ s : Nat) : Int) := by exact_mod_cast this
    simpa using h
  generalize (2 : Int) ^ s = B at *
  split
  · have := (Int.ediv_emod_unique (a := x) (b := B) (q := -1) (r := x + B) (by omega)).mpr
      ⟨by omega, by omega, by omega⟩
    exact this.1
  · exact Int.ediv_eq_zero_of_lt (by omega) (by omega)

theorem represents_ediv (x : Int) (hx : IntKind.int.represents x = true) (s : Nat) :
    IntKind.int.represents (x / 2 ^ s) = true := by
  obtain ⟨h1, h2⟩ := represents_bounds hx
  have hB1 : (1 : Int) ≤ 2 ^ s := by
    have h : ((1 : Nat) : Int) ≤ ((2 ^ s : Nat) : Int) := by exact_mod_cast Nat.one_le_two_pow
    simpa using h
  generalize (2 : Int) ^ s = B at *
  have hd := Int.mul_ediv_add_emod x B
  have hr0 : 0 ≤ x % B := Int.emod_nonneg x (by omega)
  have hrB : x % B < B := Int.emod_lt_of_pos x (by omega)
  generalize x / B = q at *
  generalize x % B = r at *
  apply represents_of_range <;> simp only [minInt_eq, maxInt_eq]
  · by_cases hq : 0 ≤ q
    · omega
    · have h := Int.mul_nonneg (by omega : (0 : Int) ≤ B - 1) (by omega : (0 : Int) ≤ -(q + 1))
      rw [Int.sub_mul, Int.one_mul, Int.mul_neg, Int.mul_add, Int.mul_one] at h
      omega
  · by_cases hq : 0 ≤ q
    · have h := Int.mul_nonneg (by omega : (0 : Int) ≤ B - 1) hq
      rw [Int.sub_mul, Int.one_mul] at h
      omega
    · omega

/-- **Shifts** of a typed `int` by an `int` count, typed or an untyped constant: galaspec's
    and GoCore's agree, a negative count panicking with the same text on both sides. -/
theorem binary_shift (cx : Ctx) (n : Nat) (op : String) (x y : Int) (w : Val) (g : St)
    (c : Spec.Core) (hop : isShift op = true) (hx : IntKind.int.represents x = true)
    (hy : VRel (.int y) w) :
    RRel .int g ((Spec.binop op (.int x) (.int y)).run.run c).1
      ((binary cx (n + 3) op (.int intTy x) w).run.run g) := by
  simp only [isShift, Bool.or_eq_true, beq_iff_eq] at hop
  have hbits : IntKind.int.bits = 64 := rfl
  rcases hop with rfl | rfl
  · cases hy <;>
      simp [Spec.binop, binary, grun_bind, asInt, underlying_int, shiftOp, Val.ty, runtimePanic, RRel] <;>
    · by_cases hneg : y < 0
      · simp only [hneg, ite_true]; exact ⟨_, rfl, .inr ⟨rfl, rfl⟩, rfl⟩
      · by_cases hbig : 64 ≤ y
        · have h64 : 64 ≤ y.toNat := by omega
          simp only [hneg, hbig, h64, ite_true, ite_false, shiftL, hbits]
          exact ⟨_, rfl, .int 0 (show IntKind.int.represents 0 = true from rfl), rfl, by simp [UInt]⟩
        · have h64 : ¬ 64 ≤ y.toNat := by omega
          simp only [hneg, hbig, h64, ite_false, shiftL, hbits, wrap_eq]
          exact ⟨_, rfl, .int _ (represents_kwrap _), rfl, by simp [UInt]⟩
  · cases hy <;>
      simp [Spec.binop, binary, grun_bind, asInt, underlying_int, shiftOp, Val.ty, runtimePanic, RRel] <;>
    · by_cases hneg : y < 0
      · simp only [hneg, ite_true]; exact ⟨_, rfl, .inr ⟨rfl, rfl⟩, rfl⟩
      · by_cases hbig : 64 ≤ y
        · have h64 : 64 ≤ y.toNat := by omega
          simp only [hneg, hbig, ite_true, ite_false, shiftR, ediv_big x hx _ h64]
          exact ⟨_, rfl, .int (if x < 0 then -1 else 0) (by split <;> rfl), rfl, by simp [UInt]⟩
        · simp only [hneg, hbig, ite_false, shiftR]
          exact ⟨_, rfl, .int _ (represents_ediv x hx _), rfl, by simp [UInt]⟩

/-- **Binary operators** other than `&&` and `||`, on related operands: galaspec's result
    and GoCore's agree. The two operands are not both untyped integers: an expression
    with no constant operator node never has two (`NoConstOp`); a shift's left operand is
    not one at all (the lowering rejects a literal there). -/
theorem binary_sim (cx : Ctx) (n : Nat) (op : String) (v₁ v₂ : Spec.Val) (w₁ w₂ : Val)
    (k₁ k₂ k : Kind) (g : St) (c : Spec.Core) (h1 : VRel v₁ w₁) (h2 : VRel v₂ w₂)
    (hk1 : valKind v₁ = some k₁) (hk2 : valKind v₂ = some k₂) (hb : binaryKind op k₁ k₂ = some k)
    (ha : op ≠ "&&") (ho : op ≠ "||") (hnu : ¬ (UInt w₁ ∧ UInt w₂))
    (hsl : isShift op = true → ¬ UInt w₁) :
    RRel k g ((Spec.binop op v₁ v₂).run.run c).1 ((binary cx (n + 3) op w₁ w₂).run.run g) := by
  cases hsh : isShift op
  case true =>
    obtain ⟨rfl, rfl, rfl⟩ := binaryKind_shift hb hsh
    have hnu1 := hsl hsh
    cases h1 with
    | int x hx =>
      cases h2 with
      | int y hy => exact binary_shift cx n op x y _ g c hsh hx (.int y hy)
      | intU y hy => exact binary_shift cx n op x y _ g c hsh hx (.intU y hy)
      | _ => simp [valKind] at hk2
    | intU x _ => exact absurd trivial hnu1
    | _ => simp [valKind] at hk1
  case false =>
  have hs : (op == "<<" || op == ">>") = false := hsh
  rcases h1 with ⟨x, hx⟩ | ⟨x, hx⟩ | x | x | x | x <;> rcases h2 with ⟨y, hy⟩ | ⟨y, hy⟩ | y | y | y | y <;>
    simp only [valKind, Option.some.injEq] at hk1 hk2 <;> subst hk1 hk2
  all_goals first
    | (exfalso; unfold binaryKind at hb; split at hb <;> simp_all; done)
    | (simp [UInt] at hnu; done)
    | exact binary_int_typed cx n op x y g c k hb hsh
    | (rw [binary_uint_left cx n op x y g hx hs]; exact binary_int_typed cx n op x y g c k hb hsh)
    | (rw [binary_uint_right cx n op x y g hy hs]; exact binary_int_typed cx n op x y g c k hb hsh)
    | exact binary_bool_same cx n op _ x y g c k (.inl rfl) hb ha ho
    | exact binary_bool_same cx n op _ x y g c k (.inr rfl) hb ha ho
    | (rw [binary_ubool_left cx n op x y g hs]; exact binary_bool_same cx n op _ x y g c k (.inl rfl) hb ha ho)
    | (rw [binary_ubool_right cx n op x y g hs]; exact binary_bool_same cx n op _ x y g c k (.inl rfl) hb ha ho)
    | exact binary_str_same cx n op _ x y g c k (.inl rfl) hb
    | exact binary_str_same cx n op _ x y g c k (.inr rfl) hb
    | (rw [binary_ustr_left cx n op x y g hs]; exact binary_str_same cx n op _ x y g c k (.inl rfl) hb)
    | (rw [binary_ustr_right cx n op x y g hs]; exact binary_str_same cx n op _ x y g c k (.inl rfl) hb)
end GoCoreRuns

/-! ## Literal trees are constant expressions -/

def cvKind : Spec.CVal → Kind
  | .int _ => .int
  | .bool _ => .bool
  | .str _ => .str

theorem lit_const (κ : String → Option Kind) : ∀ (x : Spec.Expr) (k : Kind) (lx : GoCore.Full.Expr),
    lowerE κ x = some (k, lx) → isLitE x = true →
    ∃ r, Spec.constEval x = some r ∧ ∀ cv, r = .val cv → cvKind cv = k
  | .lit (.int v), k, lx, h, _ => by
    simp only [lowerE, lowerLit, Option.some.injEq, Prod.mk.injEq] at h
    obtain ⟨rfl, -⟩ := h
    refine ⟨_, rfl, fun cv hcv => ?_⟩
    revert hcv; unfold Gala.Exact.bounded; split <;> intro hcv <;> cases hcv <;> rfl
  | .lit (.bool b), k, lx, h, _ => by
    simp only [lowerE, lowerLit, Option.some.injEq, Prod.mk.injEq] at h
    obtain ⟨rfl, -⟩ := h
    exact ⟨_, rfl, fun cv hcv => by cases hcv; rfl⟩
  | .lit (.str s), k, lx, h, _ => by
    simp only [lowerE, lowerLit, Option.some.injEq, Prod.mk.injEq] at h
    obtain ⟨rfl, -⟩ := h
    exact ⟨_, rfl, fun cv hcv => by cases hcv; rfl⟩
  | .paren x, k, lx, h, hl => by
    simp only [lowerE, Option.map_eq_some_iff] at h
    obtain ⟨⟨k', lx'⟩, h', he⟩ := h
    simp only [Prod.mk.injEq] at he
    obtain ⟨r, hr, hk⟩ := lit_const κ x k' lx' h' hl
    exact ⟨r, by simp [Spec.constEval, hr], fun cv h => he.1 ▸ hk cv h⟩

theorem unaryC_some {op : String} {k k' : Kind} {cv : Spec.CVal} (hu : unaryKind op k = some k')
    (hk : cvKind cv = k) : (Spec.unaryC op cv).isSome := by
  subst hk
  cases cv <;> (unfold unaryKind at hu; split at hu <;> simp_all [Spec.unaryC, cvKind])

theorem binaryC_some {op : String} {kl kr k : Kind} {l r : Spec.CVal}
    (hb : binaryKind op kl kr = some k) (hl : cvKind l = kl) (hr : cvKind r = kr) :
    (Spec.binaryC op l r).isSome := by
  subst hl hr
  cases l <;> cases r <;> (unfold binaryKind at hb; split at hb <;>
    simp_all [Spec.binaryC, cvKind, Gala.Exact.intOp]) <;> (try split) <;> simp_all

/-! ## The simulation of call-free expressions -/

section Sim
open Gala.GoCore.Full

/-- galaspec's result and GoCore's run agree, as `RRel`, except that an untyped integer is
    allowed when `u` (the expression is a literal). -/
def ERel (k : Kind) (g : St) (u : Bool) : Except Spec.Stop Spec.Val → Except Stop Val × St → Prop
  | .ok v, r => ∃ w, r = (.ok w, g) ∧ VRel v w ∧ valKind v = some k ∧ (u = false → ¬ UInt w)
  | .error (.panic c m), r => ∃ pv, r = (.error (.panic pv true), g) ∧ PanicRel c m pv
  | _, _ => True

/-- How deep GoCore's evaluation of an expression's lowering must be allowed to go. -/
def gdepth : Spec.Expr → Nat
  | .lit (.int _) => 3
  | .lit _ => 2
  | .name _ => 1
  | .paren e => gdepth e + 1
  | .unary _ e => gdepth e + 3
  | .binary _ l r => max (gdepth l) (gdepth r) + 4
  | _ => 0

theorem deref_bval (b : Spec.Binding) (c : Spec.Core) (v : Spec.Val) (h : bval c.heap b = some v) :
    (Spec.deref b.val).run.run c = (.ok v, c) := by
  unfold bval at h
  unfold Spec.deref
  split at h
  · rename_i n hn; simp only [hn]; rw [Spec.run_get_bind]; simp [h]; rfl
  · cases h; rename_i hn; cases hv : b.val <;> simp_all <;> rfl

theorem vrel_bool {v : Spec.Val} {w : Val} (h : VRel v w) (hk : valKind v = some .bool) :
    ∃ b t, v = .bool b ∧ w = .bool t b ∧ (t = .bool ∨ t = .untyped .bool) := by
  cases h <;> simp_all [valKind]

theorem vrel_of_bool (b : Bool) (t : Ty) (ht : t = .bool ∨ t = .untyped .bool) :
    VRel (.bool b) (.bool t b) := by
  rcases ht with rfl | rfl
  · exact .bool b
  · exact .boolU b

theorem boolType_cases (t t' : Ty) (b b' : Bool) (ht : t = .bool ∨ t = .untyped .bool)
    (ht' : t' = .bool ∨ t' = .untyped .bool) :
    boolType (.bool t b) (.bool t' b') = .bool ∨ boolType (.bool t b) (.bool t' b') = .untyped .bool := by
  rcases ht with rfl | rfl <;> rcases ht' with rfl | rfl <;> simp [boolType, Val.ty]

theorem gasBool (t : Ty) (b : Bool) (g : St) : (asBool (.bool t b)).run.run g = (.ok b, g) := rfl

/-- **GoCore's side of step 2a.** A call-free expression the lowering accepts, with no
    constant operator node, in related states: what galaspec's step-free evaluation gives,
    GoCore's evaluation gives too, at every depth from `gdepth` on. -/
theorem pure_sim (ds : Spec.Decls) (cx : Ctx) (κ : String → Option Kind) :
    ∀ (e : Spec.Expr) (k : Kind) (le : GoCore.Full.Expr), lowerE κ e = some (k, le) → NoConstOp e →
    ∀ (c : Spec.Core) (g : St) (env : Env), LocRel κ c g.cells env → Predeclared cx env →
    ∀ N ≥ gdepth e, ERel k g (isLitE e) ((pureEval ds e).run.run c).1 ((evalExpr cx N env le).run.run g)
  | .lit (.int v), k, le, hl, _, c, g, env, _, _, N, hN => by
    simp only [lowerE, lowerLit, Option.some.injEq, Prod.mk.injEq] at hl
    obtain ⟨rfl, rfl⟩ := hl
    simp only [pureEval, Spec.intLit]
    split
    · rename_i hr
      obtain ⟨n, rfl⟩ : ∃ n, N = n + 3 := ⟨N - 3, by simp [gdepth] at hN; omega⟩
      refine ⟨.int (.untyped .int) v, ?_, .intU v (represents_of_range hr.1 hr.2), rfl, by simp [isLitE]⟩
      split
      · rw [evalExpr_unary _ _ _ _ _ (by decide), grun_bind, evalExpr_intLit]
        simp [unary]; rfl
      · rw [evalExpr_intLit]
    · exact trivial
  | .lit (.bool b), k, le, hl, _, c, g, env, _, hp, N, hN => by
    simp only [lowerE, lowerLit, Option.some.injEq, Prod.mk.injEq] at hl
    obtain ⟨rfl, rfl⟩ := hl
    obtain ⟨n, rfl⟩ : ∃ n, N = n + 2 := ⟨N - 2, by simp [gdepth] at hN; omega⟩
    exact ⟨_, evalExpr_predeclared cx n env b g hp, .boolU b, rfl, by simp [isLitE]⟩
  | .lit (.str s), k, le, hl, _, c, g, env, _, _, N, hN => by
    simp only [lowerE, lowerLit, Option.some.injEq, Prod.mk.injEq] at hl
    obtain ⟨rfl, rfl⟩ := hl
    obtain ⟨n, rfl⟩ : ∃ n, N = n + 2 := ⟨N - 2, by simp [gdepth] at hN; omega⟩
    exact ⟨_, evalExpr_strLit cx (n + 1) env s g, .strU s, rfl, by simp [isLitE]⟩
  | .name x, k, le, hl, _, c, g, env, hloc, _, N, hN => by
    simp only [lowerE] at hl
    split at hl
    · cases hl
    rename_i hx
    simp only [Option.map_eq_some_iff, Prod.mk.injEq] at hl
    obtain ⟨k', hk, rfl, rfl⟩ := hl
    obtain ⟨b, cell, v, w, hb, hv, hcell, hw, hvw, hkv, hnu, -, -⟩ := hloc x k' hk
    obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by simp [gdepth] at hN; omega⟩
    have hp : ∀ what, w ≠ .poison what := by intro what h; subst h; cases hvw
    have hs : ((pureEval ds (.name x)).run.run c).1 = .ok v := by
      unfold pureEval Spec.getCore
      rw [Spec.run_get_bind]
      show ((match Spec.lookup c.locals x with
        | some b => Spec.deref b.val
        | none => (throw (Spec.Stop.stuck "not a local") : Spec.H Spec.Val)).run.run c).1 = _
      rw [hb, deref_bval b c v hv]
    rw [hs]
    exact ⟨w, evalExpr_local cx n env x cell w g hcell hw hp hx, hvw, hkv, fun _ => hnu⟩
  | .paren x, k, le, hl, hnc, c, g, env, hloc, hp, N, hN => by
    simp only [lowerE, Option.map_eq_some_iff] at hl
    obtain ⟨⟨k', lx⟩, hlx, he⟩ := hl
    simp only [Prod.mk.injEq] at he
    obtain ⟨rfl, rfl⟩ := he
    obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by simp [gdepth] at hN; omega⟩
    rw [evalExpr_paren]
    exact pure_sim ds cx κ x _ lx hlx hnc c g env hloc hp n (by simp [gdepth] at hN; omega)
  | .unary op x, k, le, hl, hnc, c, g, env, hloc, hp, N, hN => by
    simp only [lowerE, Option.bind_eq_bind, Option.bind_eq_some_iff] at hl
    obtain ⟨⟨kx, lx⟩, hlx, k', hu, he⟩ := hl
    simp only [Option.pure_def, Option.some.injEq, Prod.mk.injEq] at he
    obtain ⟨rfl, rfl⟩ := he
    obtain ⟨hc, hncx⟩ := hnc
    have hnl : isLitE x = false := by
      cases hlit : isLitE x
      · rfl
      · exfalso
        obtain ⟨r, hr, hk⟩ := lit_const κ x kx lx hlx hlit
        have hs : (Spec.constEval (.unary op x)).isSome := by
          cases r with
          | val cv => simpa [Spec.constEval, hr] using unaryC_some hu (hk cv rfl)
          | err e => simp [Spec.constEval, hr]
        rw [hc] at hs; simp at hs
    have hspec : pureEval ds (.unary op x) = (do Spec.unop op (← pureEval ds x)) :=
      pureEval_unary ds op x fun n h => by rw [h.2] at hnl; simp [isLitE] at hnl
    have hamp : op ≠ "&" := by intro h; subst h; simp [unaryKind] at hu
    obtain ⟨n, rfl⟩ : ∃ n, N = n + 3 := ⟨N - 3, by simp [gdepth] at hN; omega⟩
    have ih := pure_sim ds cx κ x kx lx hlx hncx c g env hloc hp (n + 2)
      (by simp [gdepth] at hN; omega)
    rw [evalExpr_unary _ _ _ _ _ hamp, grun_bind, hspec, Spec.run_bind_eq]
    rcases hx : (pureEval ds x).run.run c with ⟨rx, c'⟩
    rw [hx] at ih
    cases rx with
    | error e =>
      cases e <;> try exact trivial
      obtain ⟨pv, h, hpv⟩ := ih
      rw [h]; exact ⟨pv, rfl, hpv⟩
    | ok v =>
      obtain ⟨w, h, hvw, hkv, hnu⟩ := ih
      rw [h]
      obtain ⟨v', w', h1, h2, hvw', hk', hnu'⟩ := unary_sim cx n op v w kx k' g c' hvw hkv hu (hnu hnl)
      simp only
      rw [show ((Spec.unop op v).run.run c').1 = .ok v' from h1, h2]
      exact ⟨w', rfl, hvw', hk', fun _ => hnu'⟩
  | .binary op l r, k, le, hl, hnc, c, g, env, hloc, hp, N, hN => by
    simp only [lowerE, Option.bind_eq_bind, Option.bind_eq_some_iff] at hl
    obtain ⟨⟨kl, ll⟩, hll, ⟨kr, lr⟩, hlr, k', hb, he⟩ := hl
    split at he
    · cases he
    rename_i hnsh
    simp only [Option.pure_def, Option.some.injEq, Prod.mk.injEq] at he
    obtain ⟨rfl, rfl⟩ := he
    obtain ⟨hc, hncl, hncr⟩ := hnc
    obtain ⟨n, rfl⟩ : ∃ n, N = n + 4 := ⟨N - 4, by simp [gdepth] at hN; omega⟩
    have ihl := pure_sim ds cx κ l kl ll hll hncl c g env hloc hp (n + 3)
      (by simp [gdepth] at hN; omega)
    have ihr := pure_sim ds cx κ r kr lr hlr hncr c g env hloc hp (n + 3)
      (by simp [gdepth] at hN; omega)
    by_cases hand : op = "&&"
    · subst hand
      have hkb : kl = .bool ∧ kr = .bool ∧ k' = .bool := by
        unfold binaryKind at hb; split at hb <;> simp_all
      obtain ⟨rfl, rfl, rfl⟩ := hkb
      rw [evalExpr_and]
      simp only [pureEval]
      rw [grun_bind, Spec.run_bind_eq]
      rcases hx : (pureEval ds l).run.run c with ⟨rl, c₁⟩
      have hc₁ : c₁ = c := by have := pureEval_readOnly ds l c; rw [hx] at this; exact this
      subst c₁
      rw [hx] at ihl
      cases rl with
      | error e =>
        cases e <;> try exact trivial
        obtain ⟨pv, h, hpv⟩ := ihl
        rw [h]; exact ⟨pv, rfl, hpv⟩
      | ok vl =>
        obtain ⟨wl, h, hvl, hkl, -⟩ := ihl
        obtain ⟨b, t, rfl, rfl, ht⟩ := vrel_bool hvl hkl
        rw [h]
        simp only
        rw [grun_bind, gasBool, Spec.run_bind_eq]
        cases b
        · exact ⟨_, rfl, vrel_of_bool false t ht, rfl, by simp [UInt]⟩
        · rw [show StateT.run (ExceptT.run (Spec.asBool (Spec.Val.bool true))) c = (.ok true, c) from rfl]
          simp only [ite_true]
          rw [Spec.run_bind_eq, grun_bind]
          rcases hy : (pureEval ds r).run.run c with ⟨rr, c₂⟩
          have hc₂ : c₂ = c := by have := pureEval_readOnly ds r c; rw [hy] at this; exact this
          subst c₂
          rw [hy] at ihr
          cases rr with
          | error e =>
            cases e <;> try exact trivial
            obtain ⟨pv, h, hpv⟩ := ihr
            rw [h]; exact ⟨pv, rfl, hpv⟩
          | ok vr =>
            obtain ⟨wr, h, hvr, hkr, -⟩ := ihr
            obtain ⟨b', t', rfl, rfl, ht'⟩ := vrel_bool hvr hkr
            rw [h]
            simp only
            rw [grun_bind, gasBool]
            exact ⟨_, rfl, vrel_of_bool b' _ (boolType_cases t t' true b' ht ht'), rfl, by simp [UInt]⟩
    by_cases hor : op = "||"
    · subst hor
      have hkb : kl = .bool ∧ kr = .bool ∧ k' = .bool := by
        unfold binaryKind at hb; split at hb <;> simp_all
      obtain ⟨rfl, rfl, rfl⟩ := hkb
      rw [evalExpr_or]
      simp only [pureEval]
      rw [grun_bind, Spec.run_bind_eq]
      rcases hx : (pureEval ds l).run.run c with ⟨rl, c₁⟩
      have hc₁ : c₁ = c := by have := pureEval_readOnly ds l c; rw [hx] at this; exact this
      subst c₁
      rw [hx] at ihl
      cases rl with
      | error e =>
        cases e <;> try exact trivial
        obtain ⟨pv, h, hpv⟩ := ihl
        rw [h]; exact ⟨pv, rfl, hpv⟩
      | ok vl =>
        obtain ⟨wl, h, hvl, hkl, -⟩ := ihl
        obtain ⟨b, t, rfl, rfl, ht⟩ := vrel_bool hvl hkl
        rw [h]
        simp only
        rw [grun_bind, gasBool, Spec.run_bind_eq]
        cases b
        · rw [show StateT.run (ExceptT.run (Spec.asBool (Spec.Val.bool false))) c = (.ok false, c) from rfl]
          simp only [Bool.false_eq_true, ite_false]
          rw [Spec.run_bind_eq, grun_bind]
          rcases hy : (pureEval ds r).run.run c with ⟨rr, c₂⟩
          have hc₂ : c₂ = c := by have := pureEval_readOnly ds r c; rw [hy] at this; exact this
          subst c₂
          rw [hy] at ihr
          cases rr with
          | error e =>
            cases e <;> try exact trivial
            obtain ⟨pv, h, hpv⟩ := ihr
            rw [h]; exact ⟨pv, rfl, hpv⟩
          | ok vr =>
            obtain ⟨wr, h, hvr, hkr, -⟩ := ihr
            obtain ⟨b', t', rfl, rfl, ht'⟩ := vrel_bool hvr hkr
            rw [h]
            simp only
            rw [grun_bind, gasBool]
            exact ⟨_, rfl, vrel_of_bool b' _ (boolType_cases t t' false b' ht ht'), rfl, by simp [UInt]⟩
        · exact ⟨_, rfl, vrel_of_bool true t ht, rfl, by simp [UInt]⟩
    -- Not both operands literals: that would be a constant expression.
    have hlits : ¬ (isLitE l = true ∧ isLitE r = true) := by
      rintro ⟨h1, h2⟩
      obtain ⟨r1, hr1, hk1⟩ := lit_const κ l kl ll hll h1
      obtain ⟨r2, hr2, hk2⟩ := lit_const κ r kr lr hlr h2
      have hs : (Spec.constEval (.binary op l r)).isSome := by
        cases r1 with
        | val cv1 =>
          cases r2 with
          | val cv2 => simpa [Spec.constEval, hr1, hr2] using binaryC_some hb (hk1 cv1 rfl) (hk2 cv2 rfl)
          | err e => simp [Spec.constEval, hr1, hr2]
        | err e => cases r2 <;> simp [Spec.constEval, hr1, hr2]
      rw [hc] at hs; simp at hs
    rw [evalExpr_binary _ _ _ _ _ _ hand hor hp.1, pureEval_binary ds op l r hand hor]
    rw [grun_bind, Spec.run_bind_eq]
    rcases hx : (pureEval ds l).run.run c with ⟨rl, c₁⟩
    have hc₁ : c₁ = c := by have := pureEval_readOnly ds l c; rw [hx] at this; exact this
    subst c₁
    rw [hx] at ihl
    cases rl with
    | error e =>
      cases e <;> try exact trivial
      obtain ⟨pv, h, hpv⟩ := ihl
      rw [h]; exact ⟨pv, rfl, hpv⟩
    | ok vl =>
      obtain ⟨wl, h, hvl, hkl, hnul⟩ := ihl
      rw [h]
      simp only
      rw [grun_bind, Spec.run_bind_eq]
      rcases hy : (pureEval ds r).run.run c with ⟨rr, c₂⟩
      have hc₂ : c₂ = c := by have := pureEval_readOnly ds r c; rw [hy] at this; exact this
      subst c₂
      rw [hy] at ihr
      cases rr with
      | error e =>
        cases e <;> try exact trivial
        obtain ⟨pv, h, hpv⟩ := ihr
        rw [h]; exact ⟨pv, rfl, hpv⟩
      | ok vr =>
        obtain ⟨wr, h, hvr, hkr, hnur⟩ := ihr
        rw [h]
        simp only
        have hnu : ¬ (UInt wl ∧ UInt wr) := by
          rintro ⟨h1, h2⟩
          cases hl : isLitE l
          · exact hnul hl h1
          · cases hr : isLitE r
            · exact hnur hr h2
            · exact hlits ⟨hl, hr⟩
        have hbs := binary_sim cx n op vl vr wl wr kl kr k' g c hvl hvr hkl hkr hb hand hor hnu
          (fun hs => hnul (by simp [hs] at hnsh; exact hnsh.1))
        revert hbs
        rcases ((Spec.binop op vl vr).run.run c) with ⟨rb, cb⟩
        intro hbs
        cases rb with
        | ok v =>
          obtain ⟨w, h, hvw, hk, hnu'⟩ := hbs
          exact ⟨w, h, hvw, hk, fun _ => hnu'⟩
        | error e => cases e <;> first | exact trivial | exact hbs

/-- A name the lowering accepts is a local galaspec binds. -/
theorem pure_of_lower (κ : String → Option Kind) (c : Spec.Core) (cells : Array Val) (env : Env)
    (hloc : LocRel κ c cells env) :
    ∀ (e : Spec.Expr) (k : Kind) (le : GoCore.Full.Expr), lowerE κ e = some (k, le) → Pure c.locals e
  | .lit _, _, _, _ => trivial
  | .name x, k, le, hl => by
    simp only [lowerE] at hl
    split at hl
    · cases hl
    simp only [Option.map_eq_some_iff, Prod.mk.injEq] at hl
    obtain ⟨k', hk, -, -⟩ := hl
    obtain ⟨b, -, -, -, hb, -⟩ := hloc x k' hk
    simp [Pure, hb]
  | .paren x, k, le, hl => by
    simp only [lowerE, Option.map_eq_some_iff] at hl
    obtain ⟨⟨k', lx⟩, hlx, -⟩ := hl
    exact pure_of_lower κ c cells env hloc x k' lx hlx
  | .unary op x, k, le, hl => by
    simp only [lowerE, Option.bind_eq_bind, Option.bind_eq_some_iff] at hl
    obtain ⟨⟨kx, lx⟩, hlx, -⟩ := hl
    exact pure_of_lower κ c cells env hloc x kx lx hlx
  | .binary op l r, k, le, hl => by
    simp only [lowerE, Option.bind_eq_bind, Option.bind_eq_some_iff] at hl
    obtain ⟨⟨kl, ll⟩, hll, ⟨kr, lr⟩, hlr, -⟩ := hl
    exact ⟨pure_of_lower κ c cells env hloc l kl ll hll, pure_of_lower κ c cells env hloc r kr lr hlr⟩

/-- **M5, step 2a: call-free expressions.** A call-free expression the reference lowering
    accepts, with no constant operator node (what `foldConstants` leaves), evaluated by
    galaspec from a state related to GoCore's: galaspec's state is unchanged apart from its
    steps, and, at every depth from `gdepth e` on, GoCore's evaluation of the lowering
    gives a related value of the expression's kind, or the same panic, leaving GoCore's
    state as it was. Nothing is claimed when galaspec runs out of fuel or stops `stuck`
    or `unsupported`.

    Covered: `int`, `bool` and `string` literals, locals, parentheses, unary `-`, `+`,
    `^`, `!`, and binary `+ - * / %` and the bitwise `& | ^ &^` on `int`s (galaspec's
    through `Int64`, GoCore's exact bits reduced to `int`; `Gala/M5/Bits.lean`),
    comparisons of `int`s and of strings (byte order in Go, code-point order in Lean: the
    same for UTF-8), `==` and `!=` of any one kind, `+` of strings, `&&` and `||`.
    Shifts `<< >>` of a typed `int` by an `int` count, a negative count panicking as in Go.
    Not covered yet: a shift of a literal or by a negative constant (the lowering has
    neither). Stated for GoCore's order of evaluation, left to right, which is gc's for
    operands that are not calls. -/
theorem expr_correct (ds : Spec.Decls) (cx : Ctx) (κ : String → Option Kind) (e : Spec.Expr)
    (k : Kind) (le : GoCore.Full.Expr) (hl : lowerE κ e = some (k, le)) (hnc : NoConstOp e)
    (s : Spec.St) (g : St) (env : Env) (hloc : LocRel κ s.toCore g.cells env)
    (hp : Predeclared cx env) (d : Nat) :
    ((Spec.eval ds d e).run.run s).2.toCore = s.toCore ∧
    ∀ N ≥ gdepth e, ERel k g (isLitE e) ((Spec.eval ds d e).run.run s).1
      ((evalExpr cx N env le).run.run g) := by
  obtain ⟨hr, hs⟩ := eval_pure ds e d s (pure_of_lower κ s.toCore g.cells env hloc e k le hl)
  refine ⟨hs, fun N hN => ?_⟩
  rcases hr with hr | hr
  · rw [hr]; exact trivial
  · rw [hr]; exact pure_sim ds cx κ e k le hl hnc s.toCore g env hloc hp N hN

/-! ### The hypotheses can be met

A main package with no declarations, a local `x : int` holding `5` on both sides, and
`x + 1`: every hypothesis of `expr_correct` holds, so it says something. -/

section Instance
open Std (HashMap)

def emptyPkg : PkgCtx := ⟨"main", "main", #[[]], {}, {}, #[], #[], {}, false⟩
def instCtx : Ctx := ⟨(∅ : HashMap String PkgCtx).insert "main" emptyPkg, "main"⟩
def instEnv : Env := { pkg := "main", file := 0, scopes := [[("x", 0)]] }
def instKinds : String → Option Kind := fun x => if x = "x" then some .int else none
def instCore : Spec.Core := { locals := [("x", ⟨.int 5, false⟩)], globals := [], out := "" }
def instExpr : Spec.Expr := .binary "+" (.name "x") (.lit (.int 1))

theorem inst_locRel : LocRel instKinds instCore #[.int intTy 5] instEnv := by
  intro x k hk
  unfold instKinds at hk
  split at hk
  · subst_vars
    simp only [Option.some.injEq] at hk; subst hk
    exact ⟨_, 0, .int 5, .int intTy 5, rfl, rfl, rfl, rfl, .int 5 rfl, rfl, by simp [UInt],
      by simp, by simp⟩
  · cases hk

theorem inst_predeclared : Predeclared instCtx instEnv := by
  refine ⟨rfl, ?_⟩
  intro x hx
  simp only [List.mem_cons, List.not_mem_nil, or_false] at hx
  refine ⟨?_, emptyPkg, by simp [instCtx, instEnv], by simp [emptyPkg], ?_⟩
  · rcases hx with rfl | rfl <;> rfl
  · rcases hx with rfl | rfl <;> simp [nameOwner, instCtx, instEnv, emptyPkg, exported] <;> decide

def instSt : St := { cells := #[.int intTy 5], closures := #[], out := "", steps := 0 }

/-- What the instance computes: with `x = 5`, `x + 1` is `6` in galaspec and in GoCore. -/
theorem inst_values (ds : Spec.Decls) :
    ((pureEval ds instExpr).run.run instCore).1 = .ok (.int 6) ∧
    (evalExpr instCtx 5 instEnv (.binary "+" (.ident "x") (.intLit 1))).run.run instSt =
      (.ok (.int intTy 6), instSt) := by
  exact ⟨rfl, rfl⟩

/-- The bitwise operators on the instance: with `x = 5`, `x & -3`, `x | -3`, `x ^ -3` and
    `x &^ -3` are `5`, `-3`, `-8` and `0` in galaspec (through `Int64`) and in GoCore
    (exact bits reduced to `int`), as in Go; the lowering accepts each, with `-3` the folded
    literal galaspec runs and Go's `-` of `3` in GoCore. -/
theorem inst_bitwise :
    (["&", "|", "^", "&^"].all fun op =>
      (lowerE instKinds (.binary op (.name "x") (.lit (.int (-3))))).isSome) = true ∧
    ((pureEval default (.binary "&" (.name "x") (.lit (.int (-3))))).run.run instCore).1 =
      .ok (.int 5) ∧
    ((pureEval default (.binary "^" (.name "x") (.lit (.int (-3))))).run.run instCore).1 =
      .ok (.int (-8)) ∧
    ((pureEval default (.binary "|" (.name "x") (.lit (.int (-3))))).run.run instCore).1 =
      .ok (.int (-3)) ∧
    ((pureEval default (.binary "&^" (.name "x") (.lit (.int (-3))))).run.run instCore).1 =
      .ok (.int 0) ∧
    (evalExpr instCtx 8 instEnv (.binary "&" (.ident "x") (.unary "-" (.intLit 3)))).run.run instSt =
      (.ok (.int intTy 5), instSt) ∧
    (evalExpr instCtx 8 instEnv (.binary "|" (.ident "x") (.unary "-" (.intLit 3)))).run.run instSt =
      (.ok (.int intTy (-3)), instSt) ∧
    (evalExpr instCtx 8 instEnv (.binary "^" (.ident "x") (.unary "-" (.intLit 3)))).run.run instSt =
      (.ok (.int intTy (-8)), instSt) ∧
    (evalExpr instCtx 8 instEnv (.binary "&^" (.ident "x") (.unary "-" (.intLit 3)))).run.run instSt =
      (.ok (.int intTy 0), instSt) := by
  exact ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩

/-- A local `s : string` holding `"é"` on both sides, for the string orderings. -/
def strEnv : Env := { pkg := "main", file := 0, scopes := [[("s", 0)]] }
def strCore : Spec.Core := { locals := [("s", ⟨.str "é", false⟩)], globals := [], out := "" }
def strSt : St := { cells := #[.str .string "é"], closures := #[], out := "", steps := 0 }
def strKinds : String → Option Kind := fun x => if x = "s" then some .str else none

/-- String ordering on the instance, `s = "é"`: `s < "z"` is false and `s > "z"` true (Go
    compares bytes, `é` is `C3 A9`; Lean code points, `é` is `U+E9`: the same order for
    UTF-8), `"a" <= s` is true, `s >= "é"` true, in galaspec and GoCore. The lowering
    accepts each. -/
theorem inst_strcmp :
    ((pureEval default (.binary "<" (.name "s") (.lit (.str "z")))).run.run strCore).1 = .ok (.bool false) ∧
    (evalExpr instCtx 10 strEnv (.binary "<" (.ident "s") (.strLit "z"))).run.run strSt =
      (.ok (.bool (.untyped .bool) false), strSt) ∧
    ((pureEval default (.binary ">" (.name "s") (.lit (.str "z")))).run.run strCore).1 = .ok (.bool true) ∧
    (evalExpr instCtx 10 strEnv (.binary ">" (.ident "s") (.strLit "z"))).run.run strSt =
      (.ok (.bool (.untyped .bool) true), strSt) ∧
    ((pureEval default (.binary "<=" (.lit (.str "a")) (.name "s"))).run.run strCore).1 = .ok (.bool true) ∧
    (evalExpr instCtx 10 strEnv (.binary "<=" (.strLit "a") (.ident "s"))).run.run strSt =
      (.ok (.bool (.untyped .bool) true), strSt) ∧
    ((pureEval default (.binary ">=" (.name "s") (.lit (.str "é")))).run.run strCore).1 = .ok (.bool true) ∧
    (evalExpr instCtx 10 strEnv (.binary ">=" (.ident "s") (.strLit "é"))).run.run strSt =
      (.ok (.bool (.untyped .bool) true), strSt) ∧
    (["<", "<=", ">", ">="].all fun op =>
      (lowerE strKinds (.binary op (.name "s") (.lit (.str "z")))).isSome) = true := by
  exact ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩

/-- Shifts on the instance, `x = 5`: by a typed count `x << x` is `160`; `-x >> 1` is `-3`
    (an arithmetic shift rounds down); a count of `64` or more gives `x << 64 = 0` and
    `-x >> 70 = -1`; a negative count `x << -x` panics with Go's text. galaspec and GoCore
    give each of these. The lowering accepts them and rejects `1 << x`, whose untyped left
    operand would take its type from the context, and `x << -1`, which Go does not compile. -/
theorem inst_shift :
    ((pureEval default (.binary "<<" (.name "x") (.name "x"))).run.run instCore).1 = .ok (.int 160) ∧
    (evalExpr instCtx 10 instEnv (.binary "<<" (.ident "x") (.ident "x"))).run.run instSt =
      (.ok (.int intTy 160), instSt) ∧
    ((pureEval default (.binary ">>" (.unary "-" (.name "x")) (.lit (.int 1)))).run.run instCore).1 =
      .ok (.int (-3)) ∧
    (evalExpr instCtx 10 instEnv (.binary ">>" (.unary "-" (.ident "x")) (.intLit 1))).run.run instSt =
      (.ok (.int intTy (-3)), instSt) ∧
    ((pureEval default (.binary "<<" (.name "x") (.lit (.int 64)))).run.run instCore).1 = .ok (.int 0) ∧
    (evalExpr instCtx 10 instEnv (.binary "<<" (.ident "x") (.intLit 64))).run.run instSt =
      (.ok (.int intTy 0), instSt) ∧
    ((pureEval default (.binary ">>" (.unary "-" (.name "x")) (.lit (.int 70)))).run.run instCore).1 =
      .ok (.int (-1)) ∧
    (evalExpr instCtx 10 instEnv (.binary ">>" (.unary "-" (.ident "x")) (.intLit 70))).run.run instSt =
      (.ok (.int intTy (-1)), instSt) ∧
    ((pureEval default (.binary "<<" (.name "x") (.unary "-" (.name "x")))).run.run instCore).1 =
      .error (.panic .other "runtime error: negative shift amount") ∧
    (evalExpr instCtx 10 instEnv (.binary "<<" (.ident "x") (.unary "-" (.ident "x")))).run.run instSt =
      (.error (.panic (.str .string "runtime error: negative shift amount") true), instSt) ∧
    (lowerE instKinds (.binary ">>" (.unary "-" (.name "x")) (.lit (.int 70)))).isSome ∧
    lowerE instKinds (.binary "<<" (.lit (.int 1)) (.name "x")) = none ∧
    lowerE instKinds (.binary "<<" (.name "x") (.lit (.int (-1)))) = none := by
  exact ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩

/-- `expr_correct` applied to `x + 1`, every hypothesis discharged: it is not vacuous. -/
theorem expr_correct_instance (ds : Spec.Decls) (steps d : Nat) :
    ((Spec.eval ds d instExpr).run.run { instCore with steps }).2.toCore = instCore ∧
    ∀ N ≥ gdepth instExpr, ERel .int instSt (isLitE instExpr)
      ((Spec.eval ds d instExpr).run.run { instCore with steps }).1
      ((evalExpr instCtx N instEnv (.binary "+" (.ident "x") (.intLit 1))).run.run instSt) :=
  expr_correct ds instCtx instKinds instExpr .int (.binary "+" (.ident "x") (.intLit 1)) rfl
    ⟨rfl, trivial, trivial⟩ { instCore with steps } instSt instEnv inst_locRel inst_predeclared d

end Instance

end Sim

end Gala.M5
