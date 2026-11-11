/-
  # Lowering model: sealed types and `match`

  A small GALA core with sealed-type constructors and `match`, and a small Go-like core
  with the shape of the Go code the transpiler emits for them: a tagged struct, a chain of
  `Unapply` calls tried in order, and a panic when no arm matches.

  This is a *lowering model* (docs/FORMAL_SEMANTICS.MD, "Optional: lowering models"). It
  is not a semantics of Go: the target language models only the Go constructs the match
  lowering uses.

  Main results:
  * `compile_correct` and `program_correct`: the scoped lowering preserves meaning,
    for values and for errors. "No arm matched" in GALA becomes exactly a Go panic.
  * `leaky_not_correct`: a lowering that declares an arm's pattern variables before the
    arm's `if` (so they stay visible in later arms, issue #996) does not preserve meaning.

  Reflects docs/GALA.MD, "Sealed Types (Algebraic Data Types)" and "Match Expression".

  Simplifications, all deliberate:
  * variables are natural numbers; there are no types, and ill-typed programs end in
    `Err.typeErr`;
  * patterns are a constructor tag with variable binders only: no nesting, literals,
    wildcards or guards;
  * a value is an integer or a constructed sealed value.
-/
namespace Gala.MatchLowering

/-- How an evaluation can fail. `noMatch` is a GALA match with no matching arm; `panic`
    is a Go panic. The other two only arise for ill-formed programs. -/
inductive Err | noMatch | typeErr | unbound | panic
  deriving DecidableEq, Repr

/-- The result of an evaluation: a value, or an error. Errors are values, not crashes. -/
inductive Res (α : Type) | ok (v : α) | err (e : Err)

/-- Look a variable up in an environment. The first binding wins, so a later binder
    shadows an earlier one. -/
def look {β : Type} : List (Nat × β) → Nat → Option β
  | [], _ => none
  | (k, v) :: r, x => if k = x then some v else look r x

/-! ## Source: a GALA core -/

mutual
/-- GALA expressions. -/
inductive SExpr
  /-- An integer literal. -/
  | lit (n : Int)
  /-- A variable reference. -/
  | var (x : Nat)
  /-- `a + b`. -/
  | add (a b : SExpr)
  /-- `-a`. -/
  | neg (a : SExpr)
  /-- A sealed-type constructor call, `Circle(e1, ..., en)`. Variants are identified by
      their tag. -/
  | ctor (tag : Nat) (args : SArgs)
  /-- `scrut match { arms }`. -/
  | mtch (scrut : SExpr) (arms : SArms)
/-- Constructor arguments. -/
inductive SArgs
  | nil
  | cons (e : SExpr) (rest : SArgs)
/-- Match arms, in source order. Each is `case Tag(x1, ..., xn) => body`. -/
inductive SArms
  | nil
  | cons (tag : Nat) (xs : List Nat) (body : SExpr) (rest : SArms)
end

/-- GALA values: integers and constructed sealed values (variant tag plus fields). -/
inductive SVal
  | int (n : Int)
  | con (tag : Nat) (fs : List SVal)

/-- A GALA environment: variables to values. -/
abbrev SEnv := List (Nat × SVal)

/-- Bind the pattern variables `x_i` to the fields `i` of a matched value, starting at
    field `i`. Well-typed programs have matching arity; a missing field reads as 0. -/
def sbind (fs : List SVal) : SEnv → List Nat → Nat → SEnv
  | ρ, [], _ => ρ
  | ρ, x :: xs, i => sbind fs ((x, fs.getD i (.int 0)) :: ρ) xs (i + 1)

mutual
/-- The meaning of a GALA expression: big-step, left to right. -/
def seval (ρ : SEnv) : SExpr → Res SVal
  | .lit n => .ok (.int n)
  | .var x => match look ρ x with
    | some v => .ok v
    | none => .err .unbound
  | .add a b => match seval ρ a with
    | .err e => .err e
    | .ok (.int m) => match seval ρ b with
      | .err e => .err e
      | .ok (.int n) => .ok (.int (m + n))
      | .ok _ => .err .typeErr
    | .ok _ => .err .typeErr
  | .neg a => match seval ρ a with
    | .err e => .err e
    | .ok (.int m) => .ok (.int (-m))
    | .ok _ => .err .typeErr
  | .ctor t args => match sevalArgs ρ args with
    | .err e => .err e
    | .ok vs => .ok (.con t vs)
  | .mtch s arms => match seval ρ s with
    | .err e => .err e
    | .ok v => sevalArms ρ v arms
/-- Evaluate constructor arguments left to right. -/
def sevalArgs (ρ : SEnv) : SArgs → Res (List SVal)
  | .nil => .ok []
  | .cons e r => match seval ρ e with
    | .err e => .err e
    | .ok v => match sevalArgs ρ r with
      | .err e => .err e
      | .ok vs => .ok (v :: vs)
/-- Match a value against the arms. Arms are tried in source order, and a pattern's
    variables are in scope in its own arm's body only. No matching arm is `noMatch`. -/
def sevalArms (ρ : SEnv) (v : SVal) : SArms → Res SVal
  | .nil => .err .noMatch
  | .cons t xs b r => match v with
    | .con t' fs => if t' = t then seval (sbind fs ρ xs 0) b else sevalArms ρ v r
    | .int _ => .err .typeErr
end

/-! ## Target: the Go constructs the match lowering emits -/

mutual
/-- Go expressions, limited to what the lowering produces. -/
inductive TExpr
  /-- An integer literal. -/
  | lit (n : Int)
  /-- A variable reference. -/
  | var (x : Nat)
  /-- `a + b`. -/
  | add (a b : TExpr)
  /-- `-a`. -/
  | neg (a : TExpr)
  /-- The parent struct of a sealed type: `Shape{fields..., _variant: tag}`. -/
  | mk (tag : Nat) (fs : TArgs)
  /-- `Tag{}.Unapply(e)`: an `Option` of the fields, defined when `e` has that tag. -/
  | unapply (tag : Nat) (e : TExpr)
  /-- `u.IsDefined()`. -/
  | isDef (e : TExpr)
  /-- Field `i` of an `Unapply` result; the zero value when it is `None`. -/
  | get (e : TExpr) (i : Nat)
  /-- `x := e` followed by `body`. -/
  | letIn (x : Nat) (e body : TExpr)
  /-- `if c { t } else { f }`. -/
  | ite (c t f : TExpr)
  /-- `panic(...)`. -/
  | panic
/-- Struct field values. -/
inductive TArgs
  | nil
  | cons (e : TExpr) (rest : TArgs)
end

/-- Go values: integers, booleans, sealed parent structs and `Option`s of field lists. -/
inductive TVal
  | int (n : Int)
  | bool (b : Bool)
  | strct (tag : Nat) (fs : List TVal)
  | opt (o : Option (List TVal))

/-- A Go environment: variables to values. -/
abbrev TEnv := List (Nat × TVal)

mutual
/-- The meaning of the target expressions. -/
def teval (τ : TEnv) : TExpr → Res TVal
  | .lit n => .ok (.int n)
  | .var x => match look τ x with
    | some v => .ok v
    | none => .err .unbound
  | .add a b => match teval τ a with
    | .err e => .err e
    | .ok (.int m) => match teval τ b with
      | .err e => .err e
      | .ok (.int n) => .ok (.int (m + n))
      | .ok _ => .err .typeErr
    | .ok _ => .err .typeErr
  | .neg a => match teval τ a with
    | .err e => .err e
    | .ok (.int m) => .ok (.int (-m))
    | .ok _ => .err .typeErr
  | .mk t fs => match tevalArgs τ fs with
    | .err e => .err e
    | .ok vs => .ok (.strct t vs)
  | .unapply t e => match teval τ e with
    | .err e => .err e
    | .ok (.strct t' fs) => .ok (.opt (if t' = t then some fs else none))
    | .ok _ => .err .typeErr
  | .isDef e => match teval τ e with
    | .err e => .err e
    | .ok (.opt o) => .ok (.bool o.isSome)
    | .ok _ => .err .typeErr
  | .get e i => match teval τ e with
    | .err e => .err e
    | .ok (.opt o) => .ok ((o.getD []).getD i (.int 0))
    | .ok _ => .err .typeErr
  | .letIn x e b => match teval τ e with
    | .err e => .err e
    | .ok v => teval ((x, v) :: τ) b
  | .ite c t f => match teval τ c with
    | .err e => .err e
    | .ok (.bool true) => teval τ t
    | .ok (.bool false) => teval τ f
    | .ok _ => .err .typeErr
  | .panic => .err .panic
/-- Evaluate struct field values left to right. -/
def tevalArgs (τ : TEnv) : TArgs → Res (List TVal)
  | .nil => .ok []
  | .cons e r => match teval τ e with
    | .err e => .err e
    | .ok v => match tevalArgs τ r with
      | .err e => .err e
      | .ok vs => .ok (v :: vs)
end

/-! ## The scoped lowering -/

/-- Naming: GALA variable `x` becomes Go variable `2x+2`. Odd names are reserved for
    compiler temporaries, so a temporary can never capture a user variable. -/
def sv (x : Nat) : Nat := 2 * x + 2
/-- The temporary that holds the match subject. -/
def O : Nat := 1
/-- The temporary that holds the current arm's `Unapply` result. -/
def U : Nat := 3

/-- Bind the pattern variables to the fields of the `Unapply` result in `U`, then run `b`. -/
def bindFields : List Nat → Nat → TExpr → TExpr
  | [], _, b => b
  | x :: xs, i, b => .letIn (sv x) (.get (.var U) i) (bindFields xs (i + 1) b)

mutual
/-- Lower a GALA expression. `match` evaluates its subject once into `O`. -/
def compile : SExpr → TExpr
  | .lit n => .lit n
  | .var x => .var (sv x)
  | .add a b => .add (compile a) (compile b)
  | .neg a => .neg (compile a)
  | .ctor t args => .mk t (compileArgs args)
  | .mtch s arms => .letIn O (compile s) (compileArms arms)
/-- Lower constructor arguments. -/
def compileArgs : SArgs → TArgs
  | .nil => .nil
  | .cons e r => .cons (compile e) (compileArgs r)
/-- Scoped lowering of the arms: an arm's pattern variables are bound inside its `then`
    branch only, so later arms never see them. No arm left is a panic. -/
def compileArms : SArms → TExpr
  | .nil => .panic
  | .cons t xs b r =>
    .letIn U (.unapply t (.var O))
      (.ite (.isDef (.var U)) (bindFields xs 0 (compile b)) (compileArms r))
end

/-! ## Correctness of the scoped lowering -/

mutual
/-- How a GALA value is represented in Go. -/
def enc : SVal → TVal
  | .int n => .int n
  | .con t fs => .strct t (encL fs)
/-- `enc` on each element of a list. -/
def encL : List SVal → List TVal
  | [] => []
  | v :: vs => enc v :: encL vs
end

/-- How a GALA error shows up in Go: "no arm matched" is a panic, the rest are unchanged. -/
def encErr : Err → Err
  | .noMatch => .panic
  | e => e

/-- `enc` lifted to results. -/
def encR : Res SVal → Res TVal
  | .ok v => .ok (enc v)
  | .err e => .err (encErr e)

/-- `encL` lifted to results. -/
def encRL : Res (List SVal) → Res (List TVal)
  | .ok vs => .ok (encL vs)
  | .err e => .err (encErr e)

/-- A GALA environment and a Go environment agree: every GALA variable's Go name holds
    the encoding of its value, and unbound stays unbound. -/
def EnvRel (ρ : SEnv) (τ : TEnv) : Prop := ∀ x, look τ (sv x) = (look ρ x).map enc

theorem look_cons {β} (k x : Nat) (v : β) (r) :
    look ((k, v) :: r) x = if k = x then some v else look r x := rfl

/-- Binding a compiler temporary (an odd name) keeps the environments related. -/
theorem EnvRel.temp {ρ τ} (h : EnvRel ρ τ) (k : Nat) (hk : k % 2 = 1) (v : TVal) :
    EnvRel ρ ((k, v) :: τ) := by
  intro x
  rw [look_cons, ite_eq_right (by unfold sv; omega)]
  exact h x

/-- Binding a GALA variable on both sides keeps the environments related. -/
theorem EnvRel.bind {ρ τ} (h : EnvRel ρ τ) (x : Nat) (v : SVal) :
    EnvRel ((x, v) :: ρ) ((sv x, enc v) :: τ) := by
  intro y
  rw [look_cons, look_cons]
  by_cases hxy : x = y
  · subst hxy; simp
  · rw [ite_eq_right (show ¬ sv x = sv y by unfold sv; omega), ite_eq_right hxy]; exact h y

theorem encL_getD (fs : List SVal) (i : Nat) :
    (encL fs).getD i (.int 0) = enc (fs.getD i (.int 0)) := by
  induction fs generalizing i with
  | nil => rfl
  | cons v vs ih => cases i with
    | zero => rfl
    | succ n => exact ih n

theorem look_U_sv (x : Nat) (v : TVal) (τ : TEnv) : look ((sv x, v) :: τ) U = look τ U := by
  rw [look_cons, ite_eq_right (by unfold sv U; omega)]

/-- Binding the fields in Go matches `sbind` in GALA. -/
theorem bindFields_correct (fs : List SVal) (b : SExpr) (b' : TExpr)
    (hb : ∀ ρ τ, EnvRel ρ τ → teval τ b' = encR (seval ρ b)) :
    ∀ xs i ρ τ, EnvRel ρ τ → look τ U = some (.opt (some (encL fs))) →
      teval τ (bindFields xs i b') = encR (seval (sbind fs ρ xs i) b)
  | [], _, ρ, τ, h, _ => by simp only [bindFields, sbind]; exact hb ρ τ h
  | x :: xs, i, ρ, τ, h, hu => by
    simp only [bindFields, sbind, teval, hu, Option.getD_some, encL_getD]
    exact bindFields_correct fs b b' hb xs (i + 1) _ _ (h.bind x _)
      (by rw [look_U_sv]; exact hu)

mutual
/-- **The scoped lowering is correct**: in related environments, the lowered Go
    expression evaluates to the encoding of the GALA expression's result. -/
theorem compile_correct : ∀ (e : SExpr) (ρ : SEnv) (τ : TEnv), EnvRel ρ τ →
    teval τ (compile e) = encR (seval ρ e)
  | .lit n, ρ, τ, h => by simp [compile, teval, seval, encR, enc]
  | .var x, ρ, τ, h => by
    simp only [compile, teval, seval, h x]
    cases look ρ x <;> simp [encR, encErr]
  | .add a b, ρ, τ, h => by
    simp only [compile, teval, seval, compile_correct a ρ τ h, compile_correct b ρ τ h]
    rcases seval ρ a with (m | _) | _ | e <;> rcases seval ρ b with (n | _) | _ | e' <;>
      simp [encR, enc, encErr]
  | .neg a, ρ, τ, h => by
    simp only [compile, teval, seval, compile_correct a ρ τ h]
    rcases seval ρ a with (m | _) | _ | e <;> simp [encR, enc, encErr]
  | .ctor t args, ρ, τ, h => by
    simp only [compile, teval, seval, compileArgs_correct args ρ τ h]
    cases sevalArgs ρ args <;> simp [encRL, encR, enc]
  | .mtch s arms, ρ, τ, h => by
    simp only [compile, teval, seval, compile_correct s ρ τ h]
    cases hs : seval ρ s with
    | err e => simp [encR]
    | ok v =>
      simp only [encR]
      exact compileArms_correct arms ρ _ v (h.temp O (by decide) _) rfl
/-- Constructor arguments lower correctly. -/
theorem compileArgs_correct : ∀ (as : SArgs) (ρ : SEnv) (τ : TEnv), EnvRel ρ τ →
    tevalArgs τ (compileArgs as) = encRL (sevalArgs ρ as)
  | .nil, ρ, τ, h => by simp [compileArgs, tevalArgs, sevalArgs, encRL, encL]
  | .cons e r, ρ, τ, h => by
    simp only [compileArgs, tevalArgs, sevalArgs, compile_correct e ρ τ h,
      compileArgs_correct r ρ τ h]
    cases seval ρ e <;> cases sevalArgs ρ r <;> simp [encR, encRL, encL]
/-- Match arms lower correctly, given that `O` holds the encoded subject. -/
theorem compileArms_correct : ∀ (arms : SArms) (ρ : SEnv) (τ : TEnv) (v : SVal),
    EnvRel ρ τ → look τ O = some (enc v) →
    teval τ (compileArms arms) = encR (sevalArms ρ v arms)
  | .nil, ρ, τ, v, h, ho => by simp [compileArms, teval, sevalArms, encR, encErr]
  | .cons t xs b r, ρ, τ, v, h, ho => by
    cases v with
    | int n => simp [compileArms, teval, sevalArms, ho, enc, encR, encErr]
    | con t' fs =>
      by_cases ht : t' = t
      · subst ht
        simp only [compileArms, teval, sevalArms, ho, enc, look_cons]
        exact bindFields_correct fs b (compile b)
          (fun ρ τ h => compile_correct b ρ τ h) xs 0 ρ _ (h.temp U (by decide) _)
          rfl
      · simp only [compileArms, teval, sevalArms, ho, enc, ite_eq_right ht, look_cons]
        exact compileArms_correct r ρ _ _ (h.temp U (by decide) _)
          (by rw [look_cons, ite_eq_right (show ¬ U = O by decide)]; exact ho)
end

/-- For a closed program, the Go output is the GALA meaning, with "no arm matched"
    turned into a panic. -/
theorem program_correct (e : SExpr) : teval [] (compile e) = encR (seval [] e) :=
  compile_correct e [] [] (fun _ => rfl)

/-! ## The leaky lowering (issue #996) -/

/-- Leaky lowering of the arms: an arm's pattern variables are declared *before* the
    arm's `if`, so they are still in scope in the arms after it and shadow outer
    variables of the same name. This is the shape reported in issue #996. -/
def compileArmsLeaky (body : SExpr → TExpr) : SArms → TExpr
  | .nil => .panic
  | .cons t xs b r =>
    .letIn U (.unapply t (.var O))
      (bindFields xs 0 (.ite (.isDef (.var U)) (body b) (compileArmsLeaky body r)))

/-- Leaky lowering of the outermost `match` only; enough for the counterexample. -/
def compileLeaky : SExpr → TExpr
  | .mtch s arms => .letIn O (compile s) (compileArmsLeaky compile arms)
  | e => compile e

/-
  The counterexample, in GALA:

    func area(s Shape, r int) int = s match {
        case Circle(r)    => r          // tag 0, binds r (variable 0)
        case Square(side) => side + r   // tag 1, binds side (variable 1); r is the parameter
    }
    area(Square(3), 7)

  GALA says 10. The leaky lowering says 3: the `Circle` arm failed, but its `r` was still
  bound (to the zero value) and shadows the parameter in the `Square` arm.
-/

/-- The pattern variable `r`, which shadows the parameter of the same name. -/
def r := 0
/-- The pattern variable `side`. -/
def side := 1
/-- The body of `area`, applied to `Square(3)`. -/
def areaBody : SExpr :=
  .mtch (.ctor 1 (.cons (.lit 3) .nil))
    (.cons 0 [r] (.var r)
      (.cons 1 [side] (.add (.var side) (.var r)) .nil))
/-- The parameter `r = 7`. -/
def paramEnv : SEnv := [(r, .int 7)]

theorem gala_says_10 : seval paramEnv areaBody = .ok (.int 10) := rfl
theorem scoped_go_says_10 : teval [(sv r, .int 7)] (compile areaBody) = .ok (.int 10) := rfl
theorem leaky_go_says_3 : teval [(sv r, .int 7)] (compileLeaky areaBody) = .ok (.int 3) := rfl

/-- **The leaky lowering is not correct**: there are related environments where it
    disagrees with GALA. -/
theorem leaky_not_correct :
    ¬ ∀ (e : SExpr) (ρ : SEnv) (τ : TEnv), EnvRel ρ τ →
      teval τ (compileLeaky e) = encR (seval ρ e) := by
  intro hall
  have h := hall areaBody paramEnv [(sv r, .int 7)] (by
    intro x; by_cases hx : x = 0
    · subst hx; rfl
    · unfold paramEnv; rw [look_cons, look_cons, ite_eq_right (show ¬ sv r = sv x by unfold sv r; omega),
        ite_eq_right (show ¬ r = x by unfold r; omega)]; rfl)
  rw [leaky_go_says_3, gala_says_10] at h
  simp [encR, enc] at h

end Gala.MatchLowering
