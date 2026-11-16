/-
  # T1: fuel adequacy

  `run p n` runs a program with `n` as both the bound on `eval`'s recursion depth and the
  number of steps it may take. `run_fuel_adequate` (docs/FORMAL_SEMANTICS.MD, "M1: fuel
  adequacy"): if the run with fuel `n` ends in anything but `fuel`, then the run with any
  fuel `m ≥ n` ends the same way and prints the same output. So `ok`, a `panic`,
  `unsupported` and `stuck` are each what the program does, not an artefact of the
  budget. Nothing is said about a run that ends in `fuel`: more fuel may end it any way.

  The proof relates two runs of the same code, one with more depth and `k` more steps
  (`Sim k`): the second gives the same value or the same `Stop`, and the same state apart
  from exactly `k` more steps left. Every function of `eval`'s mutual block keeps `Sim`
  when its depth grows (`eval_sim` to `runIn_sim`, one mutual induction with the
  definitions' own measures), then `initGlobals`, `runM` and `run`.

  What it rests on:
  * Code that cannot see the steps runs in `H` (Gala/Eval.lean) and is lifted into `M`,
    so by its type it does the same thing whatever the fuel (`sim_liftM`). The heap, the
    output and the variables are all there, so none of them needs a lemma of its own.
  * `tick` is the only code that reads or spends steps (`sim_tick`).
  * The one handler of a `Stop`, `runIn`'s for `return`, passes `fuel` on unchanged:
    `sim_tryCatch` needs that of its handler, since a handler that recovered from `fuel`
    would make a run's result depend on the budget.
-/
import Gala.Eval

namespace Gala.Spec

/-- `s` with `k` more steps left. -/
def shift (k : Nat) (s : St) : St := { s with steps := s.steps + k }

/-- A result other than running out of fuel. -/
def NotFuel : Except Stop α → Prop
  | .error .fuel => False
  | _ => True

/-- `y` does what `x` does, given `k` more steps: wherever `x` does not run out of fuel,
    `y` started with `k` more steps ends with the same value or `Stop` and the same state,
    with `k` more steps left. -/
structure Sim (k : Nat) (x y : M α) : Prop where
  prop : ∀ s, NotFuel (x.run.run s).1 →
    y.run.run (shift k s) = ((x.run.run s).1, shift k (x.run.run s).2)

/-! ## `Sim` of the building blocks of `M` -/

theorem run_bind_eq (x : M α) (f : α → M β) (s : St) :
    (x >>= f).run.run s = match x.run.run s with
      | (.ok a, t) => (f a).run.run t
      | (.error e, t) => (.error e, t) := by
  simp only [ExceptT.run_bind, StateT.run_bind]
  rcases hx : x.run.run s with ⟨r, t⟩
  cases r <;> rfl

theorem notFuel_error {e : Stop} (h : NotFuel (Except.error e : Except Stop α)) :
    NotFuel (Except.error e : Except Stop β) := by
  cases e <;> first | trivial | exact h

theorem sim_bind {k : Nat} {x y : M α} {f g : α → M β} (hx : Sim k x y)
    (hf : ∀ a, Sim k (f a) (g a)) : Sim k (x >>= f) (y >>= g) := by
  refine ⟨fun s h => ?_⟩
  rw [run_bind_eq] at h ⊢
  rw [run_bind_eq]
  rcases hxs : x.run.run s with ⟨r, t⟩
  rw [hxs] at h
  cases r with
  | ok a =>
    rw [hx.prop s (by rw [hxs]; trivial), hxs]
    exact (hf a).prop t h
  | error e =>
    have h' : NotFuel (Except.error e : Except Stop β) := by simpa using h
    rw [hx.prop s (by rw [hxs]; exact notFuel_error h'), hxs]

theorem sim_pure {k : Nat} (a : α) : Sim k (pure a : M α) (pure a) := ⟨fun _ _ => rfl⟩

theorem sim_throw {k : Nat} (e : Stop) : Sim k (throw e : M α) (throw e) := ⟨fun _ _ => rfl⟩

theorem sim_liftM {k : Nat} (x : H α) : Sim k (liftM x : M α) (liftM x) := ⟨fun _ _ => rfl⟩

theorem sim_tick {k : Nat} : Sim k tick tick := by
  refine ⟨fun ⟨c, n⟩ h => ?_⟩
  cases n with
  | zero => exact False.elim h
  | succ n =>
    show tick.run.run ⟨c, n + 1 + k⟩ = _
    rw [Nat.add_right_comm]
    rfl

theorem sim_map {k : Nat} {x y : M α} (f : α → β) (hx : Sim k x y) :
    Sim k (f <$> x) (f <$> y) := by
  rw [← bind_pure_comp, ← bind_pure_comp]
  exact sim_bind hx fun a => sim_pure _

theorem sim_discard {k : Nat} {x y : M α} (hx : Sim k x y) :
    Sim k (discard x) (discard y) := by
  simp only [discard, Functor.mapConst]
  exact sim_map _ hx

theorem sim_forIn {k : Nat} {β : Type} (xs : List γ) (b : β) {f g : γ → β → M (ForInStep β)}
    (hf : ∀ a b, Sim k (f a b) (g a b)) : Sim k (forIn xs b f) (forIn xs b g) := by
  induction xs generalizing b with
  | nil => exact sim_pure _
  | cons a as ih =>
    simp only [List.forIn_cons]
    refine sim_bind (hf a b) fun r => ?_
    cases r with
    | done b => exact sim_pure _
    | yield b => exact ih b

theorem run_tryCatch_eq (x : M α) (h : Stop → M α) (s : St) :
    (tryCatch x h).run.run s = match x.run.run s with
      | (.ok a, t) => (.ok a, t)
      | (.error e, t) => (h e).run.run t := by
  show (ExceptT.tryCatch x h).run.run s = _
  simp only [ExceptT.tryCatch, ExceptT.run_mk, StateT.run_bind]
  rcases hx : x.run.run s with ⟨r, t⟩
  erw [hx]
  cases r <;> rfl

/-- Catching a `Stop` keeps `Sim` if the handler passes `fuel` on: a handler that
    recovered from `fuel` would turn "not enough fuel" into a result. -/
theorem sim_tryCatch {k : Nat} {x y : M α} {h h' : Stop → M α} (hx : Sim k x y)
    (hh : ∀ e, Sim k (h e) (h' e))
    (hfuel : ∀ s, ¬ NotFuel ((h .fuel).run.run s).1) :
    Sim k (tryCatch x h) (tryCatch y h') := by
  refine ⟨fun s hs => ?_⟩
  rw [run_tryCatch_eq] at hs ⊢
  rw [run_tryCatch_eq]
  rcases hxs : x.run.run s with ⟨r, t⟩
  rw [hxs] at hs
  cases r with
  | ok a => rw [hx.prop s (by rw [hxs]; trivial), hxs]
  | error e =>
    cases e with
    | fuel => exact absurd hs (hfuel t)
    | _ => rw [hx.prop s (by rw [hxs]; trivial), hxs]; exact (hh _).prop t hs

/-! ## The mutual block of `eval`

Each function's `Sim` follows from its definition part by part: the lemmas above, and the
induction hypotheses `ihE`, `ihL`, ... (one per function the definition calls, at the
depth it calls it). `sim_step` takes one such step; `split` follows an `if` or `match`,
which is the same on both sides.

The hypotheses are named, and `sim_step` applies them by name, rather than through
`apply_assumption`: that also tries every hypothesis ending in `False` (a negated `if`
condition) and unfolds whole definitions while unifying, and does not finish. -/

set_option hygiene false in
/-- One step of relating two runs of the same code: `Sim` of a part, by the lemmas above
    or an induction hypothesis named `ihE`, `ihL`, ... in the context. -/
macro "sim_step" : tactic => `(tactic| (first
  | dsimp only
  | with_reducible exact sim_pure _
  | with_reducible exact sim_throw _
  | with_reducible exact sim_liftM _
  | with_reducible exact sim_tick
  | with_reducible apply ihE
  | with_reducible apply ihL
  | with_reducible apply ihB
  | with_reducible apply ihCV
  | with_reducible apply ihBl
  | with_reducible apply ihC
  | with_reducible apply ihD
  | with_reducible apply ihCF
  | with_reducible apply ihR
  | with_reducible apply ihFA
  | with_reducible apply ihFi
  | with_reducible apply ihG
  | with_reducible intro _
  | with_reducible apply sim_bind
  | with_reducible apply sim_discard
  | with_reducible apply sim_map
  | with_reducible apply sim_forIn
  | split))

/-- Relate two runs of the same code as far as `sim_step` goes. -/
macro "sim_auto" : tactic => `(tactic| repeat' sim_step)

mutual
/-- More depth and more steps: `eval` does the same, unless the smaller budget ran out.
    Depth `0` runs out at once; otherwise both sides spend a step and then evaluate the
    same expression's parts at depths `d ≤ d'`. -/
theorem eval_sim (ds : Decls) (k : Nat) :
    ∀ (d d' : Nat) (e : Expr), d ≤ d' → Sim k (eval ds d e) (eval ds d' e)
  | 0, _, e, _ => ⟨fun s h => by rw [eval.eq_1] at h; exact False.elim h⟩
  | _ + 1, 0, _, h => absurd h (by omega)
  | d + 1, d' + 1, e, h => by
    have h : d ≤ d' := by omega
    have ihE := fun e => eval_sim ds k d d' e h
    have ihL := fun es => evalList_sim ds k d d' es h
    have ihFA := fun f as => funcArgs_sim ds k d d' f as h
    have ihFi := fun fs as => fieldArgs_sim ds k d d' fs as h
    have ihCV := fun v as => callValue_sim ds k d d' v as h
    have ihBl := fun es => evalBlock_sim ds k d d' es h
    have ihC := fun v cs => evalCases_sim ds k d d' v cs h
    have ihD := fun ns b t vs => declare_sim ds k d d' ns b t vs h
    have ihCF := fun f vs => callFunc_sim ds k d d' f vs h
    rw [eval.eq_def ds (d + 1) e, eval.eq_def ds (d' + 1) e]
    refine sim_bind sim_tick fun _ => ?_
    split
    all_goals sim_auto
  termination_by d _ _ _ => (d, 0, 0)

theorem evalList_sim (ds : Decls) (k : Nat) :
    ∀ (d d' : Nat) (es : List Expr), d ≤ d' → Sim k (evalList ds d es) (evalList ds d' es)
  | d, d', [], h => by rw [evalList.eq_1, evalList.eq_1]; exact sim_pure _
  | d, d', e :: es, h => by
    have ihE := eval_sim ds k d d' e h
    have ihL := evalList_sim ds k d d' es h
    rw [evalList.eq_2, evalList.eq_2]
    sim_auto
  termination_by d _ es => (d, 1, es.length)

theorem bindArgs_sim (ds : Decls) (k : Nat) :
    ∀ (d d' : Nat) (ns : List String) (ts : List (Option Ty)) (dfs : List (Option Expr))
      (fs : Bool) (as : List Expr), d ≤ d' →
      Sim k (bindArgs ds d ns ts dfs fs as) (bindArgs ds d' ns ts dfs fs as)
  | d, d', ns, ts, dfs, fs, as, h => by
    have ihL := fun es => evalList_sim ds k d d' es h
    have ihR := fun fr b => runIn_sim ds k d d' fr b h
    rw [bindArgs.eq_1, bindArgs.eq_1]
    sim_auto
  termination_by d _ _ _ _ _ _ => (d, 2, 0)

theorem funcArgs_sim (ds : Decls) (k : Nat) :
    ∀ (d d' : Nat) (f : Func) (as : List Expr), d ≤ d' →
      Sim k (funcArgs ds d f as) (funcArgs ds d' f as)
  | d, d', f, as, h => by
    rw [funcArgs.eq_1, funcArgs.eq_1]
    exact bindArgs_sim ds k d d' _ _ _ _ _ h
  termination_by d _ _ _ => (d, 3, 0)

theorem fieldArgs_sim (ds : Decls) (k : Nat) :
    ∀ (d d' : Nat) (fs : List Field) (as : List Expr), d ≤ d' →
      Sim k (fieldArgs ds d fs as) (fieldArgs ds d' fs as)
  | d, d', fs, as, h => by
    rw [fieldArgs.eq_1, fieldArgs.eq_1]
    exact bindArgs_sim ds k d d' _ _ _ _ _ h
  termination_by d _ _ _ => (d, 3, 0)

theorem callValue_sim (ds : Decls) (k : Nat) :
    ∀ (d d' : Nat) (v : Val) (as : List Expr), d ≤ d' →
      Sim k (callValue ds d v as) (callValue ds d' v as)
  | d, d', v, as, h => by
    have ihB := fun ns ts dfs fs as => bindArgs_sim ds k d d' ns ts dfs fs as h
    have ihCF := fun f vs => callFunc_sim ds k d d' f vs h
    have ihR := fun fr b => runIn_sim ds k d d' fr b h
    rw [callValue.eq_def ds d v as, callValue.eq_def ds d' v as]
    sim_auto
  termination_by d _ _ _ => (d, 4, 0)

theorem evalBlock_sim (ds : Decls) (k : Nat) :
    ∀ (d d' : Nat) (es : List Expr), d ≤ d' → Sim k (evalBlock ds d es) (evalBlock ds d' es)
  | d, d', [], h => by rw [evalBlock.eq_1, evalBlock.eq_1]; exact sim_pure _
  | d, d', [e], h => by
    rw [evalBlock.eq_2, evalBlock.eq_2]; exact eval_sim ds k d d' e h
  | d, d', e :: e' :: es, h => by
    have ihE := eval_sim ds k d d' e h
    have ihBl := evalBlock_sim ds k d d' (e' :: es) h
    rw [evalBlock.eq_3 ds d e (e' :: es) (by simp), evalBlock.eq_3 ds d' e (e' :: es) (by simp)]
    sim_auto
  termination_by d _ es => (d, 1, es.length)

theorem evalCases_sim (ds : Decls) (k : Nat) :
    ∀ (d d' : Nat) (v : Val) (cs : List (Pat × Option Expr × Expr)), d ≤ d' →
      Sim k (evalCases ds d v cs) (evalCases ds d' v cs)
  | d, d', v, [], h => by rw [evalCases.eq_1, evalCases.eq_1]; exact sim_throw _
  | d, d', v, c :: cs, h => by
    have ihE := fun e => eval_sim ds k d d' e h
    have ihC := evalCases_sim ds k d d' v cs h
    rw [evalCases.eq_2, evalCases.eq_2]
    sim_auto
  termination_by d _ _ cs => (d, 1, cs.length)

theorem declare_sim (ds : Decls) (k : Nat) :
    ∀ (d d' : Nat) (ns : List String) (b : Bool) (t : Option Ty) (vs : List Expr), d ≤ d' →
      Sim k (declare ds d ns b t vs) (declare ds d' ns b t vs)
  | d, d', ns, b, t, vs, h => by
    have ihE := fun e => eval_sim ds k d d' e h
    have ihL := fun es => evalList_sim ds k d d' es h
    rw [declare.eq_def ds d ns b t vs, declare.eq_def ds d' ns b t vs]
    sim_auto
  termination_by d _ _ _ _ _ => (d, 2, 0)

theorem callFunc_sim (ds : Decls) (k : Nat) :
    ∀ (d d' : Nat) (f : Func) (vs : List Val), d ≤ d' →
      Sim k (callFunc ds d f vs) (callFunc ds d' f vs)
  | d, d', f, vs, h => by
    have ihR := fun fr b => runIn_sim ds k d d' fr b h
    rw [callFunc.eq_1, callFunc.eq_1]
    sim_auto
  termination_by d _ _ _ => (d, 2, 0)

theorem runIn_sim (ds : Decls) (k : Nat) :
    ∀ (d d' : Nat) (fr : Env) (b : Expr), d ≤ d' →
      Sim k (runIn ds d fr b) (runIn ds d' fr b)
  | d, d', fr, b, h => by
    have ihE := eval_sim ds k d d' b h
    rw [runIn.eq_1, runIn.eq_1]
    refine sim_bind (sim_liftM _) fun _ => ?_
    refine sim_bind (sim_liftM _) fun _ => ?_
    refine sim_bind (sim_tryCatch ihE (fun e => ?_) (fun s hs => hs)) fun _ => ?_
    · split <;> first | exact sim_pure _ | exact sim_throw _
    · sim_auto
  termination_by d _ _ _ => (d, 1, 0)
end

/-! ## Programs

`initGlobals` uses `reach` and `mentions` only to order the declarations; they take no
fuel, so both runs order them the same way. -/

theorem initGlobals_sim (ds : Decls) (k d d' : Nat) (gs : List GlobalDecl) (h : d ≤ d') :
    Sim k (initGlobals ds d gs) (initGlobals ds d' gs) := by
  have ihD := fun ns b t vs => declare_sim ds k d d' ns b t vs h
  unfold initGlobals
  sim_auto

theorem runM_sim (p : Program) (k n m : Nat) (h : n ≤ m) : Sim k (runM p n) (runM p m) := by
  have ihG := fun ds gs => initGlobals_sim ds k n m gs h
  have ihCF := fun ds f vs => callFunc_sim ds k n m f vs h
  unfold runM
  sim_auto

/-- **T1, fuel adequacy.** A run that does not run out of fuel ends the same way and
    prints the same output with any more fuel: `ok`, a `panic`, `unsupported` and `stuck`
    are what the program does, not what the budget allowed. A run that ends in `fuel`
    says nothing: this does not claim that more fuel would end it. -/
theorem run_fuel_adequate (p : Program) (n m : Nat) (h : n ≤ m) (hn : (run p n).1 ≠ .fuel) :
    run p m = run p n := by
  have hs : ({ locals := [], globals := [], out := "", steps := m } : St) =
      shift (m - n) { locals := [], globals := [], out := "", steps := n } := by
    simp only [shift]; congr; omega
  unfold run at hn ⊢
  rw [hs]
  rcases hr : (runM p n).run.run { locals := [], globals := [], out := "", steps := n } with ⟨r, t⟩
  rw [hr] at hn
  have hnf : NotFuel r := by
    cases r with
    | ok => trivial
    | error e => cases e <;> first | trivial | exact hn rfl
  rw [(runM_sim p (m - n) n m h).prop _ (by rw [hr]; exact hnf), hr]
  rfl

end Gala.Spec
