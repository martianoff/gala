/-
  # T4: default arguments

  docs/FORMAL_SEMANTICS.MD, "M2": a default value is evaluated in the callee's scope, with
  the earlier parameters bound. `bindArgs` (Gala/Eval.lean) evaluates the arguments a call
  writes, in the caller's scope, and then hands their values to `bindDefaults`, which fills
  in every parameter in order, a left-out one by its default. Two theorems say what that
  default sees:

  * `bindDefaults_callerLocals`: the defaults phase does not read the caller's locals.
    Run with any other locals, it gives the same values or the same `Stop`, and leaves the
    same output, heap, package-level variables and steps; when it succeeds, the caller's
    locals are back as they were.
  * `defaultFrame_lookup` (Gala/Theorems.lean): the frame a default is evaluated in binds
    exactly the parameters before it, each name to the latest such parameter's value,
    immutable.

  `bindDefaults_callerLocals` was false before galaspec resolved a by-name default's bare
  name in the default's frame: `byNameOk` read the caller's locals, so whether a call
  ended `unsupported` could depend on a local of the caller's.

  Not claimed here: that the arguments a call writes are evaluated before the defaults.
  That is `bindArgs`'s definition, and an ordering statement belongs to T3.

  The proof needs no fact about `eval`: `runIn` replaces the locals with the frame before it
  evaluates anything, so both runs evaluate the default from the same state.
-/
import Gala.Fuel

namespace Gala.Spec

/-- A state with its locals cleared: everything else a run can observe. -/
def eraseL (s : St) : St := { s with locals := [] }

/-- `x` does not read the locals it starts with. From two states that differ only in their
    locals, it gives the same value or `Stop` and two states that differ only in their
    locals; when it succeeds, each run's locals are back as they started. -/
def Oblivious (x : M α) : Prop :=
  ∀ s s' : St, eraseL s = eraseL s' →
    (x.run.run s).1 = (x.run.run s').1 ∧ eraseL (x.run.run s).2 = eraseL (x.run.run s').2 ∧
      ∀ v, (x.run.run s).1 = .ok v →
        (x.run.run s).2.locals = s.locals ∧ (x.run.run s').2.locals = s'.locals

/-- A state without its locals. -/
def readPart (c : Core) : Core := { c with locals := [] }

/-- An `H` step that only reads the state, and not its locals. -/
def ReadsNoLocals (x : H α) : Prop :=
  ∀ c c' : Core, readPart c = readPart c' → ∃ r, x.run.run c = (r, c) ∧ x.run.run c' = (r, c')

theorem eraseL_setLocals {s s' : St} (h : eraseL s = eraseL s') (l : Env) :
    ({ s with locals := l } : St) = { s' with locals := l } := by
  obtain ⟨⟨_, _, _, _⟩, _⟩ := s
  obtain ⟨⟨_, _, _, _⟩, _⟩ := s'
  simp only [eraseL, St.mk.injEq, Core.mk.injEq] at h ⊢
  simp_all

/-! ## `Oblivious` of the building blocks of `M` -/

theorem obl_pure (a : α) : Oblivious (pure a : M α) := by
  intro s s' h
  exact ⟨rfl, h, fun _ _ => ⟨rfl, rfl⟩⟩

theorem obl_bind {x : M α} {f : α → M β} (hx : Oblivious x) (hf : ∀ a, Oblivious (f a)) :
    Oblivious (x >>= f) := by
  intro s s' h
  simp only [run_bind_eq]
  obtain ⟨h1, h2, h3⟩ := hx s s' h
  rcases hxs : x.run.run s with ⟨r, t⟩
  rcases hxs' : x.run.run s' with ⟨r', t'⟩
  rw [hxs, hxs'] at h1 h2 h3
  simp only at h1 h2 h3
  subst h1
  cases r with
  | error e => exact ⟨rfl, h2, fun _ hv => nomatch hv⟩
  | ok a =>
    obtain ⟨hl, hl'⟩ := h3 a rfl
    obtain ⟨g1, g2, g3⟩ := hf a t t' h2
    refine ⟨g1, g2, fun v hv => ?_⟩
    obtain ⟨k1, k2⟩ := g3 v hv
    exact ⟨k1.trans hl, k2.trans hl'⟩

theorem run_liftM (x : H α) (s : St) : (liftM x : M α).run.run s =
    ((x.run.run s.toCore).1, { toCore := (x.run.run s.toCore).2, steps := s.steps }) := rfl

theorem obl_liftM {x : H α} (hx : ReadsNoLocals x) : Oblivious (liftM x : M α) := by
  intro s s' h
  obtain ⟨r, hr, hr'⟩ := hx s.toCore s'.toCore (congrArg St.toCore (eraseL_setLocals h []))
  rw [run_liftM, run_liftM, hr, hr']
  exact ⟨rfl, h, fun _ _ => ⟨rfl, rfl⟩⟩

theorem readsNoLocals_throw (e : Stop) : ReadsNoLocals (throw e : H α) :=
  fun _ _ _ => ⟨.error e, rfl, rfl⟩

theorem obl_forIn {β : Type} (xs : List γ) (b : β) {f : γ → β → M (ForInStep β)}
    (hf : ∀ a b, Oblivious (f a b)) : Oblivious (forIn xs b f) := by
  induction xs generalizing b with
  | nil => exact obl_pure _
  | cons a as ih =>
    simp only [List.forIn_cons]
    refine obl_bind (hf a b) fun r => ?_
    cases r with
    | done b => exact obl_pure _
    | yield b => exact ih b

theorem run_getCore (c : Core) : getCore.run.run c = (.ok c, c) := rfl

theorem run_modifyCore (f : Core → Core) (c : Core) :
    (modifyCore f).run.run c = (.ok (), f c) := rfl

theorem runIn_run (ds : Decls) (d : Nat) (fr : Env) (b : Expr) (s : St) :
    (runIn ds d fr b).run.run s =
      match (tryCatch (eval ds d b) (fun e => match e with
          | .ret v => pure v
          | e => throw e) : M Val).run.run { s with locals := fr } with
      | (.ok a, t) => (.ok a, { t with locals := s.locals })
      | (.error e, t) => (.error e, t) := by
  rw [runIn.eq_1]
  simp only [run_bind_eq, run_liftM, run_getCore, run_modifyCore]
  generalize (tryCatch (eval ds d b) (fun e => match e with
          | .ret v => pure v
          | e => throw e) : M Val).run.run { s with locals := fr } = p
  obtain ⟨r, t⟩ := p
  cases r <;> rfl

/-- `runIn` sets the locals to the frame before it evaluates anything, so both runs evaluate
    the body from the same state, and it puts the saved locals back when the body ends
    normally. (After a `Stop` other than `return` it does not put them back, which no run
    can observe today: nothing catches such a `Stop`.) -/
theorem obl_runIn (ds : Decls) (d : Nat) (fr : Env) (b : Expr) : Oblivious (runIn ds d fr b) := by
  intro s s' h
  rw [runIn_run, runIn_run, eraseL_setLocals h fr]
  generalize (tryCatch (eval ds d b) (fun e => match e with
          | .ret v => pure v
          | e => throw e) : M Val).run.run { s' with locals := fr } = p
  obtain ⟨r, t⟩ := p
  cases r with
  | error e => exact ⟨rfl, rfl, fun _ hv => nomatch hv⟩
  | ok a =>
    exact ⟨rfl, rfl, fun _ _ => ⟨rfl, rfl⟩⟩

/-! ## The by-name check reads no locals

`byNameOk` resolves a name in the locals it is given (for a default, its frame), and reads
only the package-level variables and the heap of the state. -/

theorem run_get_bind (f : Core → H α) (c : Core) :
    ((get : H Core) >>= f).run.run c = (f c).run.run c := rfl

theorem deref_run (v : Val) (c : Core) :
    (deref v).run.run c = (((deref v).run.run (readPart c)).1, c) := by
  cases v <;> try rfl
  unfold deref
  rw [run_get_bind, run_get_bind]
  rw [show (readPart c).heap = c.heap from rfl]
  split <;> rfl

theorem byNameOk_run (ds : Decls) (fr : Env) (ty : Option Ty) (e : Expr) (c : Core) :
    (byNameOk ds fr ty e).run.run c = (((byNameOk ds fr ty e).run.run (readPart c)).1, c) := by
  unfold byNameOk
  rw [run_get_bind, run_get_bind]
  rw [show (readPart c).globals = c.globals from rfl]
  cases e <;> split <;> (try rfl) <;> (try dsimp only) <;> (generalize resolve ds fr c.globals _ = r) <;>
    cases r <;> (try rfl) <;> rw [run_bind_eq, run_bind_eq, deref_run] <;>
    rcases (deref _).run.run (readPart c) with ⟨r, t⟩ <;> cases r <;> rfl

theorem readsNoLocals_byNameOk (ds : Decls) (fr : Env) (ty : Option Ty) (e : Expr) :
    ReadsNoLocals (byNameOk ds fr ty e) := by
  intro c c' h
  exact ⟨_, byNameOk_run ds fr ty e c, by rw [byNameOk_run, h]⟩

/-! ## The defaults phase -/

/-- One step of showing a computation `Oblivious`, by the lemmas above. -/
macro "obl_step" : tactic => `(tactic| (first
  | dsimp only
  | with_reducible exact obl_pure _
  | exact obl_liftM (readsNoLocals_throw _)
  | exact obl_liftM (readsNoLocals_byNameOk _ _ _ _)
  | with_reducible apply obl_runIn
  | with_reducible intro _
  | with_reducible apply obl_bind
  | with_reducible apply obl_forIn
  | split))

theorem obl_bindDefaults (ds : Decls) (d : Nat) (names : List String) (tys : List (Option Ty))
    (dflts : List (Option Expr)) (fieldScope : Bool) (given : List (Nat × Val)) :
    Oblivious (bindDefaults ds d names tys dflts fieldScope given) := by
  rw [bindDefaults.eq_1]
  repeat' obl_step

/-- **T4, default arguments.** The defaults phase of a call (`bindDefaults`: each
    parameter the call leaves out takes its default, evaluated in the callee's scope) does
    not depend on the caller's locals. Run with any other locals `l`, it gives the same
    values or the same `Stop` and leaves the same output, heap, package-level variables
    and steps. When it succeeds, the caller's locals are back as they were. This holds for
    a function's defaults (each evaluated in `defaultFrame` of the earlier parameters,
    `defaultFrame_lookup`) and a field's (evaluated with no locals at all).

    It failed before galaspec resolved a by-name default's bare name in the default's
    frame: `byNameOk` read the caller's locals, so whether such a call ended `unsupported`
    could depend on a local of the caller's. That the arguments a call writes are
    evaluated first, in the caller's scope, is `bindArgs`'s definition, not part of this
    theorem. -/
theorem bindDefaults_callerLocals (ds : Decls) (d : Nat) (names : List String)
    (tys : List (Option Ty)) (dflts : List (Option Expr)) (fieldScope : Bool)
    (given : List (Nat × Val)) (s : St) (l : Env) :
    let a := (bindDefaults ds d names tys dflts fieldScope given).run.run s
    let b := (bindDefaults ds d names tys dflts fieldScope given).run.run { s with locals := l }
    a.1 = b.1 ∧ { a.2 with locals := [] } = { b.2 with locals := [] } ∧
      ∀ v, a.1 = .ok v → a.2.locals = s.locals ∧ b.2.locals = l :=
  obl_bindDefaults ds d names tys dflts fieldScope given s { s with locals := l } rfl

end Gala.Spec
