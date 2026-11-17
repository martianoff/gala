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
    or a panic, and the only run-time panic this step lowers is an integer division by
    zero, whose class and text are the same wherever it happens. So the order its parts
    run in is unobservable here, and the claim holds for any order Go allows, though it is
    proved for GoCore's, left to right. When shifts are lowered, two failing operands can
    panic differently; the GoCore fixtures `panic_order_divide` and `panic_order_shift`
    pin gc 1.25.5's choice there (left to right) for that step.
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

/-- How a galaspec panic and a GoCore panic agree in this step, whose only run-time panic
    is an integer division by zero: galaspec's is of class `divide`, and GoCore's is the
    runtime error with the same text. -/
def PanicRel : Spec.PanicClass → String → GoCore.Full.Val → Prop
  | c, m, .str .string t => c = .divide ∧ t = m
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
    (h1 : op ≠ "&&") (h2 : op ≠ "||") :
    evalExpr c (n + 1) env (.binary op x y) =
      (do let a ← evalExpr c n env x; let b ← evalExpr c n env y; binary c n op a b) := by
  simp [evalExpr]

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

/-- An untyped operand next to a typed one of the same kind is converted first (Go spec,
    "Operators"), so the operator then runs on two typed values. -/
theorem binary_uint_left (cx : Ctx) (n : Nat) (op : String) (x y : Int) (g : St)
    (hr : IntKind.int.represents x = true) (hs : (op == "<<" || op == ">>") = false) :
    (binary cx (n + 3) op (.int (.untyped .int) x) (.int intTy y)).run.run g =
      (binary cx (n + 3) op (.int intTy x) (.int intTy y)).run.run g := by
  simp only [binary, Val.isFloat, Bool.or_false, hs]
  simp [convert_uint _ _ _ _ hr, Val.ty, grun_bind]

theorem binary_uint_right (cx : Ctx) (n : Nat) (op : String) (x y : Int) (g : St)
    (hr : IntKind.int.represents y = true) (hs : (op == "<<" || op == ">>") = false) :
    (binary cx (n + 3) op (.int intTy x) (.int (.untyped .int) y)).run.run g =
      (binary cx (n + 3) op (.int intTy x) (.int intTy y)).run.run g := by
  simp only [binary, Val.isFloat, Bool.or_false, hs]
  simp [convert_uint _ _ _ _ hr, Val.ty, grun_bind]

theorem binary_ubool_left (cx : Ctx) (n : Nat) (op : String) (x y : Bool) (g : St)
    (hs : (op == "<<" || op == ">>") = false) :
    (binary cx (n + 3) op (.bool (.untyped .bool) x) (.bool .bool y)).run.run g =
      (binary cx (n + 3) op (.bool .bool x) (.bool .bool y)).run.run g := by
  simp only [binary, Val.isFloat, Bool.or_false, hs]
  simp [convert_ubool, Val.ty, grun_bind]

theorem binary_ubool_right (cx : Ctx) (n : Nat) (op : String) (x y : Bool) (g : St)
    (hs : (op == "<<" || op == ">>") = false) :
    (binary cx (n + 3) op (.bool .bool x) (.bool (.untyped .bool) y)).run.run g =
      (binary cx (n + 3) op (.bool .bool x) (.bool .bool y)).run.run g := by
  simp only [binary, Val.isFloat, Bool.or_false, hs]
  simp [convert_ubool, Val.ty, grun_bind]

theorem binary_ustr_left (cx : Ctx) (n : Nat) (op : String) (x y : String) (g : St)
    (hs : (op == "<<" || op == ">>") = false) :
    (binary cx (n + 3) op (.str (.untyped .string) x) (.str .string y)).run.run g =
      (binary cx (n + 3) op (.str .string x) (.str .string y)).run.run g := by
  simp only [binary, Val.isFloat, Bool.or_false, hs]
  simp [convert_ustr, Val.ty, grun_bind]

theorem binary_ustr_right (cx : Ctx) (n : Nat) (op : String) (x y : String) (g : St)
    (hs : (op == "<<" || op == ">>") = false) :
    (binary cx (n + 3) op (.str .string x) (.str (.untyped .string) y)).run.run g =
      (binary cx (n + 3) op (.str .string x) (.str .string y)).run.run g := by
  simp only [binary, Val.isFloat, Bool.or_false, hs]
  simp [convert_ustr, Val.ty, grun_bind]

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
    (k : Kind) (hk : binaryKind op .int .int = some k) :
    RRel k g ((Spec.binop op (.int x) (.int y)).run.run c).1
      ((binary cx (n + 3) op (.int intTy x) (.int intTy y)).run.run g) := by
  have hop : op = "==" ∨ op = "!=" ∨ op = "<" ∨ op = "<=" ∨ op = ">" ∨ op = ">=" ∨
      op = "+" ∨ op = "-" ∨ op = "*" ∨ op = "/" ∨ op = "%" := by
    unfold binaryKind at hk; split at hk <;> simp_all
  rcases hop with rfl | rfl | rfl | rfl | rfl | rfl | rfl | rfl | rfl | rfl | rfl <;>
    simp only [binaryKind, ite_true, Option.some.injEq] at hk <;> subst hk
  all_goals simp [Spec.binop, binary, grun_bind, underlying_int, intOp, cmpOp, Val.isFloat, Val.ty,
    equal, Val.dyn, Ty.isUntyped, intTy_beq, runtimePanic, RRel, Spec.valEq, cmp_lt, cmp_gt,
    cmp_le, cmp_ge, wrap_eq]
  all_goals first
    | exact ⟨_, rfl, .boolU _, rfl, by simp [UInt]⟩
    | exact ⟨_, rfl, .int _, rfl, by simp [UInt]⟩
    | (by_cases hy : y = 0
       · simp only [hy, ite_true]
         exact ⟨_, rfl, rfl, rfl⟩
       · simp only [hy, ite_false]
         exact ⟨_, rfl, .int _, rfl, by simp [UInt]⟩)

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
  all_goals simp [Spec.binop, binary, Val.isFloat, Val.ty, equal, Val.dyn, Ty.isUntyped,
    RRel, Spec.valEq]
  all_goals first
    | exact ⟨_, rfl, .boolU _, rfl, by simp [UInt]⟩
    | (simp [show (Ty.bool == Ty.bool) = true from rfl]; exact ⟨_, rfl, .boolU _, rfl, by simp [UInt]⟩)

/-- `+`, `==` and `!=` on two strings of one type: Go's `string`, or two untyped ones. -/
theorem binary_str_same (cx : Ctx) (n : Nat) (op : String) (t : Ty) (x y : String) (g : St)
    (c : Spec.Core) (k : Kind) (ht : t = .string ∨ t = .untyped .string)
    (hk : binaryKind op .str .str = some k) :
    RRel k g ((Spec.binop op (.str x) (.str y)).run.run c).1
      ((binary cx (n + 3) op (.str t x) (.str t y)).run.run g) := by
  have hop : op = "==" ∨ op = "!=" ∨ op = "+" := by
    unfold binaryKind at hk; split at hk <;> simp_all
  rcases hop with rfl | rfl | rfl <;> simp only [binaryKind, ite_true, Option.some.injEq] at hk <;>
    subst hk <;> rcases ht with rfl | rfl
  all_goals simp [Spec.binop, binary, Val.isFloat, Val.ty, equal, Val.dyn, Ty.isUntyped,
    RRel, Spec.valEq]
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
  · obtain ⟨hl, p, hpk, hobj, hown⟩ := hp "false" (by simp)
    simp only [Std.HashMap.get?_eq_getElem?] at hpk hobj
    simp [evalExpr, hl, hown, evalName, pkgCtx, hpk, hobj]
    rfl
  · obtain ⟨hl, p, hpk, hobj, hown⟩ := hp "true" (by simp)
    simp only [Std.HashMap.get?_eq_getElem?] at hpk hobj
    simp [evalExpr, hl, hown, evalName, pkgCtx, hpk, hobj]
    rfl

theorem binaryKind_noshift {op : String} {k₁ k₂ k : Kind} (h : binaryKind op k₁ k₂ = some k) :
    (op == "<<" || op == ">>") = false := by
  unfold binaryKind at h
  split at h <;> simp_all

/-- **Binary operators** other than `&&` and `||`, on related operands: galaspec's result
    and GoCore's agree. The two operands are not both untyped integers: an expression
    with no constant operator node never has two (`NoConstOp`). -/
theorem binary_sim (cx : Ctx) (n : Nat) (op : String) (v₁ v₂ : Spec.Val) (w₁ w₂ : Val)
    (k₁ k₂ k : Kind) (g : St) (c : Spec.Core) (h1 : VRel v₁ w₁) (h2 : VRel v₂ w₂)
    (hk1 : valKind v₁ = some k₁) (hk2 : valKind v₂ = some k₂) (hb : binaryKind op k₁ k₂ = some k)
    (ha : op ≠ "&&") (ho : op ≠ "||") (hnu : ¬ (UInt w₁ ∧ UInt w₂)) :
    RRel k g ((Spec.binop op v₁ v₂).run.run c).1 ((binary cx (n + 3) op w₁ w₂).run.run g) := by
  have hs := binaryKind_noshift hb
  rcases h1 with x | ⟨x, hx⟩ | x | x | x | x <;> rcases h2 with y | ⟨y, hy⟩ | y | y | y | y <;>
    simp only [valKind, Option.some.injEq] at hk1 hk2 <;> subst hk1 hk2
  all_goals first
    | (exfalso; unfold binaryKind at hb; split at hb <;> simp_all; done)
    | (simp [UInt] at hnu; done)
    | exact binary_int_typed cx n op x y g c k hb
    | (rw [binary_uint_left cx n op x y g hx hs]; exact binary_int_typed cx n op x y g c k hb)
    | (rw [binary_uint_right cx n op x y g hy hs]; exact binary_int_typed cx n op x y g c k hb)
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

/-- A literal, through parentheses: the only expressions whose GoCore value may be an
    untyped integer. -/
def isLitE : Spec.Expr → Bool
  | .lit _ => true
  | .paren e => isLitE e
  | _ => false

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
    rw [evalExpr_binary _ _ _ _ _ _ hand hor, pureEval_binary ds op l r hand hor]
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
    `^`, `!`, and binary `+ - * / %`, comparisons of `int`s, `==` and `!=` of any one
    kind, `+` of strings, `&&` and `||`. Not covered yet: shifts, the bitwise operators
    and the ordering of strings (the lowering has none). Stated for GoCore's order of
    evaluation, left to right, which is gc's for operands that are not calls. -/
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
    exact ⟨_, 0, .int 5, .int intTy 5, rfl, rfl, rfl, rfl, .int 5, rfl, by simp [UInt],
      by simp, by simp⟩
  · cases hk

theorem inst_predeclared : Predeclared instCtx instEnv := by
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
