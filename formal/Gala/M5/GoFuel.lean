/-
  # M5 step 3d: GoCore's fuel adequacy

  GoCore's M1 (`Gala/Fuel.lean` is galaspec's). `simAll`: every function of the
  evaluator's mutual block (`Gala/GoCore/Eval.lean`), started with `k` more steps and at
  any larger depth, ends a run that does not run out of either the same way, in the same
  state, with `k` more steps left (`GSim`). `runProgramAt_adequate` and
  `runProgram_adequate` lift it to whole programs: a run that does not end in `fuel` ends
  the same way, with the same output, under any larger budget. `syncAll`: every state a
  run reaches from one with as many cell types as cells has as many, which is `Inv3.sync`
  for `runProgram`'s states (they start empty).

  What it rests on, each a lemma or a step of the tactics below:
  * `tick` and `forLoop`'s bound on its iterations (`loopM`, the steps left plus one) are
    the only readers of `steps` (`sim_tick`, `sim_loopM_steps`); everything else reads
    the state through its other fields (`sim_get` with `shift_cells`, ...) and updates
    them in ways that commute with adding steps (`sim_modify`).
  * Every handler of a `Stop` passes `fuel` on (`sim_tryCatch`'s third hypothesis,
    discharged for each `tryCatch` of the evaluator and of `runProgramAt`).
  * `recover` compares the number of open deferred-call frames with the one recorded,
    not the evaluator's depth, so the state does not depend on the depth.

  The proof is one induction on the depth with a field per function (`SimAll`,
  `SyncAll`, written out from the evaluator's signatures). Each step unfolds a function at
  `n + 1` on both sides and relates the two bodies part by part. The do-notation binds the
  rest of a block after an early `return` as a join point (`have __do_jp := ...`);
  unfolding those copies each into every branch that jumps to it, which grows
  exponentially in GoCore's larger functions, so `gsim_let` keeps them bound and relates
  each pair once. A function added to the mutual block needs its field in both bundles,
  its two step lemmas (`<f>_step`, `<f>_sync`) and its entries in `ih_step`,
  `simAll` and `syncAll`; a helper outside the block, its `sim_` and `sync_` lemma.
-/
import Gala.GoCore.Run
import Gala.M5.Expr
import Lean

namespace Gala.M5
open Gala.GoCore.Full

/-- `s` with `k` more steps left. Reducible, so that a decidability instance that still
    reads a field of `shift k s` unifies with the one reading `s`'s. -/
@[reducible] def shift (k : Nat) (s : St) : St := { s with steps := s.steps + k }

/-- A result other than running out of fuel. -/
def GNotFuel : Except Stop α → Prop
  | .error .fuel => False
  | _ => True

/-- `y` does what `x` does, given `k` more steps: wherever `x` does not run out of fuel,
    `y` started with `k` more steps ends with the same value or `Stop` and the same state,
    with `k` more steps left. -/
structure GSim (k : Nat) (x y : GM α) : Prop where
  prop : ∀ s, GNotFuel (x.run.run s).1 →
    y.run.run (shift k s) = ((x.run.run s).1, shift k (x.run.run s).2)

/-! ## `GSim` of the building blocks of `GM` -/

theorem notFuel_error {e : Stop} (h : GNotFuel (Except.error e : Except Stop α)) :
    GNotFuel (Except.error e : Except Stop β) := by
  cases e <;> first | trivial | exact h

/-- A run that always runs out of fuel is related to anything. -/
theorem sim_of_fuel {k : Nat} {x y : GM α} (h : ∀ s, ¬ GNotFuel (x.run.run s).1) : GSim k x y :=
  ⟨fun s hs => absurd hs (h s)⟩

theorem sim_bind {k : Nat} {x y : GM α} {f g : α → GM β} (hx : GSim k x y)
    (hf : ∀ a, GSim k (f a) (g a)) : GSim k (x >>= f) (y >>= g) := by
  refine ⟨fun s h => ?_⟩
  rw [grun_bind] at h ⊢
  rw [grun_bind]
  rcases hxs : x.run.run s with ⟨r, t⟩
  rw [hxs] at h
  cases r with
  | ok a =>
    rw [hx.prop s (by rw [hxs]; trivial), hxs]
    exact (hf a).prop t h
  | error e =>
    have h' : GNotFuel (Except.error e : Except Stop β) := by simpa using h
    rw [hx.prop s (by rw [hxs]; exact notFuel_error h'), hxs]

theorem sim_pure {k : Nat} (a : α) : GSim k (pure a : GM α) (pure a) := ⟨fun _ _ => rfl⟩

theorem sim_throw {k : Nat} (e : Stop) : GSim k (throw e : GM α) (throw e) := ⟨fun _ _ => rfl⟩

/-- Reading the state: the second run reads it with `k` more steps. What follows a `get`
    in GoCore reads its fields, and every field but `steps` is the same in both runs
    (`shift_cells`, ...); only `tick` and `forLoop` read `steps`. -/
theorem sim_get {k : Nat} {f g : St → GM β} (h : ∀ s, GSim k (f s) (g (shift k s))) :
    GSim k (get >>= f) (get >>= g) := ⟨fun s hs => (h s).prop s hs⟩

/-- Updating the state other than its steps: the update commutes with `shift`. -/
theorem sim_modify {k : Nat} {f : St → St} (h : ∀ s, f (shift k s) = shift k (f s)) :
    GSim k (modify f : GM Unit) (modify f) := ⟨fun s _ => by
  show (Except.ok (), f (shift k s)) = _
  rw [h]; rfl⟩

/-- `tick`, which reads and spends a step: with `k` more it spends one too and keeps `k`
    more, unless the smaller budget had none left. -/
theorem sim_tick {k : Nat} : GSim k tick tick := by
  refine ⟨fun s h => ?_⟩
  rcases s with ⟨c, ty, cl, o, n, p, g, d, r, m, l⟩
  cases n with
  | zero => exact False.elim h
  | succ n =>
    show tick.run.run ⟨c, ty, cl, o, n + 1 + k, p, g, d, r, m, l⟩ = _
    rw [Nat.add_right_comm]
    rfl

theorem sim_map {k : Nat} {x y : GM α} (f : α → β) (hx : GSim k x y) :
    GSim k (f <$> x) (f <$> y) := by
  rw [← bind_pure_comp, ← bind_pure_comp]
  exact sim_bind hx fun a => sim_pure _

theorem sim_seqRight {k : Nat} {x y : GM α} {x' y' : GM β} (hx : GSim k x y) (hy : GSim k x' y') :
    GSim k (x *> x') (y *> y') := by
  rw [seqRight_eq_bind, seqRight_eq_bind]
  exact sim_bind hx fun _ => hy

theorem sim_forIn {k : Nat} {β : Type} (xs : List γ) (b : β) {f g : γ → β → GM (ForInStep β)}
    (hf : ∀ a b, GSim k (f a b) (g a b)) : GSim k (forIn xs b f) (forIn xs b g) := by
  induction xs generalizing b with
  | nil => exact sim_pure _
  | cons a as ih =>
    simp only [List.forIn_cons]
    refine sim_bind (hf a b) fun r => ?_
    cases r with
    | done b => exact sim_pure _
    | yield b => exact ih b

theorem sim_forIn_array {k : Nat} {β : Type} (xs : Array γ) (b : β) {f g : γ → β → GM (ForInStep β)}
    (hf : ∀ a b, GSim k (f a b) (g a b)) : GSim k (forIn xs b f) (forIn xs b g) := by
  rw [← Array.forIn_toList, ← Array.forIn_toList]
  exact sim_forIn _ b hf

theorem sim_forIn_range {k : Nat} {β : Type} (r : Std.Legacy.Range) (b : β) {f g : Nat → β → GM (ForInStep β)}
    (hf : ∀ a b, GSim k (f a b) (g a b)) : GSim k (forIn r b f) (forIn r b g) := by
  rw [Std.Legacy.Range.forIn_eq_forIn_range', Std.Legacy.Range.forIn_eq_forIn_range']
  exact sim_forIn _ b hf

theorem sim_mapM {k : Nat} (xs : List γ) {f g : γ → GM β}
    (hf : ∀ a, GSim k (f a) (g a)) : GSim k (xs.mapM f) (xs.mapM g) := by
  induction xs with
  | nil => exact sim_pure _
  | cons a as ih =>
    simp only [List.mapM_cons]
    exact sim_bind (hf a) fun _ => sim_bind ih fun _ => sim_pure _

theorem sim_optionMapM {k : Nat} (o : Option γ) {f g : γ → GM β}
    (hf : ∀ a, GSim k (f a) (g a)) : GSim k (o.mapM f) (o.mapM g) := by
  cases o with
  | none => exact sim_pure _
  | some a => exact sim_bind (hf a) fun _ => sim_pure _

theorem sim_filterMapM {k : Nat} (xs : List γ) {f g : γ → GM (Option β)}
    (hf : ∀ a, GSim k (f a) (g a)) : GSim k (xs.filterMapM f) (xs.filterMapM g) := by
  induction xs with
  | nil => exact sim_pure _
  | cons a as ih =>
    simp only [List.filterMapM_cons]
    refine sim_bind (hf a) fun r => ?_
    cases r with
    | none => exact ih
    | some b => exact sim_bind ih fun _ => sim_pure _

theorem sim_flatMapM {k : Nat} (xs : List γ) {f g : γ → GM (List β)}
    (hf : ∀ a, GSim k (f a) (g a)) : GSim k (xs.flatMapM f) (xs.flatMapM g) := by
  induction xs with
  | nil => exact sim_pure _
  | cons a as ih =>
    simp only [List.flatMapM_cons]
    exact sim_bind (hf a) fun _ => sim_bind ih fun _ => sim_pure _

/-- An `if`, on the same condition on both sides; its decision procedures may be written
    differently (one may still read a field of `shift k s`), and decide the same. -/
theorem sim_ite {k : Nat} (c : Prop) {d1 d2 : Decidable c} {a b a' b' : GM α}
    (h1 : c → GSim k a a') (h2 : ¬ c → GSim k b b') :
    GSim k (@ite _ c d1 a b) (@ite _ c d2 a' b') := by
  cases d1 with
  | isTrue h => cases d2 with
    | isTrue _ => exact h1 h
    | isFalse h' => exact absurd h h'
  | isFalse h => cases d2 with
    | isTrue h' => exact absurd h' h
    | isFalse _ => exact h2 h

theorem sim_dite {k : Nat} (c : Prop) {d1 d2 : Decidable c} {a a' : c → GM α}
    {b b' : ¬ c → GM α} (h1 : ∀ h, GSim k (a h) (a' h)) (h2 : ∀ h, GSim k (b h) (b' h)) :
    GSim k (@dite _ c d1 a b) (@dite _ c d2 a' b') := by
  cases d1 with
  | isTrue h => cases d2 with
    | isTrue _ => exact h1 h
    | isFalse h' => exact absurd h h'
  | isFalse h => cases d2 with
    | isTrue h' => exact absurd h' h
    | isFalse _ => exact h2 h

theorem run_tryCatch_eq (x : GM α) (h : Stop → GM α) (s : St) :
    (tryCatch x h).run.run s = match x.run.run s with
      | (.ok a, t) => (.ok a, t)
      | (.error e, t) => (h e).run.run t := by
  show (ExceptT.tryCatch x h).run.run s = _
  simp only [ExceptT.tryCatch, ExceptT.run_mk, StateT.run_bind]
  rcases hx : x.run.run s with ⟨r, t⟩
  erw [hx]
  cases r <;> rfl

/-- Catching a `Stop` keeps `GSim` if the handler passes `fuel` on: a handler that
    recovered from `fuel` would turn "not enough fuel" into a result. -/
theorem sim_tryCatch {k : Nat} {x y : GM α} {h h' : Stop → GM α} (hx : GSim k x y)
    (hh : ∀ e, GSim k (h e) (h' e))
    (hfuel : ∀ s, ¬ GNotFuel ((h .fuel).run.run s).1) :
    GSim k (tryCatch x h) (tryCatch y h') := by
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

/-- `loopM`, whose bound is the steps left plus one in `forLoop`: with `k` more steps the
    bound is `k` larger, and since each iteration ticks, the smaller bound is never what
    ends a loop that does not run out of fuel. -/
theorem sim_loopM {k : Nat} {b b' : Nat} (hb : b ≤ b') (env : Env) {body body' : Env → GM (Option Env)}
    (hbody : ∀ env, GSim k (body env) (body' env)) :
    GSim k (loopM b env body) (loopM b' env body') := by
  induction b generalizing b' env with
  | zero => exact sim_of_fuel fun s => by rw [loopM]; exact id
  | succ b ih =>
    obtain ⟨b', rfl⟩ : ∃ b'', b' = b'' + 1 := ⟨b' - 1, by omega⟩
    rw [loopM, loopM]
    refine sim_bind (hbody env) fun r => ?_
    split
    · exact ih (by omega) _
    · exact sim_pure _

/-- `forLoop`'s bound, the steps left plus one, read in both runs. -/
theorem sim_loopM_steps {k : Nat} (s : St) (env : Env) {body body' : Env → GM (Option Env)}
    (hbody : ∀ env, GSim k (body env) (body' env)) :
    GSim k (loopM (s.steps + 1) env body) (loopM (s.steps + k + 1) env body') :=
  sim_loopM (by omega) env hbody

theorem shift_steps : (shift k s).steps = s.steps + k := rfl
theorem shift_cells : (shift k s).cells = s.cells := rfl
theorem shift_types : (shift k s).types = s.types := rfl
theorem shift_closures : (shift k s).closures = s.closures := rfl
theorem shift_out : (shift k s).out = s.out := rfl
theorem shift_printing : (shift k s).printing = s.printing := rfl
theorem shift_globals : (shift k s).globals = s.globals := rfl
theorem shift_defers : (shift k s).defers = s.defers := rfl
theorem shift_recoverable : (shift k s).recoverable = s.recoverable := rfl
theorem shift_maps : (shift k s).maps = s.maps := rfl
theorem shift_localTypes : (shift k s).localTypes = s.localTypes := rfl
theorem shift_localType? : (shift k s).localType? p n a = s.localType? p n a := rfl


/-! ## Local definitions

The do-notation names the rest of a block after an early `return` (`__do_jp`) and binds
pure values with `have`. Unfolding them copies each join point into every branch that
jumps to it, which for GoCore's larger functions grows exponentially. `gsim_let` keeps
them bound instead: a value bound the same on both sides becomes a variable; two join
points, which differ only in their depth, are related once (`∀ r, GSim k (j r) (j' r)`)
and that fact closes every jump (`gsim_hyp`). -/

open Lean Meta Elab Tactic in
/-- Is `t`, after its arguments, a `GM` type? -/
partial def isGMFun (t : Lean.Expr) : MetaM Bool :=
  forallTelescope t fun _ r => do
    let r ← whnfR r
    pure (r.isAppOf ``ExceptT)

open Lean Meta Elab Tactic in
/-- `∀ xs, body` for a join point's parameters `xs`; when its one parameter is
    `rangeLoop`'s iterations, `∀ x, ItemOK x → body`: the jumps pass a computed
    iteration function, which the body calls. -/
def jpForall (xs : Array Lean.Expr) (body : Lean.Expr) : MetaM Lean.Expr := do
  if xs.size == 1 then
    let e := mkApp (mkConst `Gala.M5.ItemOK) xs[0]!
    let ok ← try (do Meta.check e; pure true) catch _ => pure false
    if ok then
      return ← mkForallFVars xs (Lean.mkForall `hitem .default e body)
  mkForallFVars xs body

open Lean Meta Elab Tactic in
/-- `rel a b` for a goal `R ... a b` whose last two arguments are related: the relation
    applied to its other arguments. -/
def relParts (g : Lean.Expr) : Option (Lean.Expr × Lean.Expr × Lean.Expr) :=
  let args := g.getAppArgs
  if args.size < 2 then none
  else some (mkAppN g.getAppFn (args.extract 0 (args.size - 2)), args[args.size - 2]!, args[args.size - 1]!)

open Lean Meta Elab Tactic in
/-- `GSim k (have x := v; b) (have x := v'; b')`: see above. -/
elab "gsim_let" : tactic => do
  let g ← getMainGoal
  g.withContext do
  let ty ← instantiateMVars (← g.getType)
  let ty := ty.consumeMData
  unless ty.isAppOf ``GSim do throwError "gsim_let: not GSim"
  let some (rel, x, y) := relParts ty | throwError "gsim_let"
  let x := x.consumeMData
  let y := y.consumeMData
  match x, y with
  | .letE n t v b _, .letE _ t' v' b' _ =>
    unless ← isDefEq t t' do throwError "gsim_let: types"
    if !(← isGMFun t) then
      unless v == v' do throwError "gsim_let: differing non-GM values"
      -- the same value: a variable
      let newTy := Lean.Expr.forallE n t (mkApp2 rel b b') .default
      let ng ← mkFreshExprSyntheticOpaqueMVar newTy
      g.assign (mkApp ng v)
      replaceMainGoal [ng.mvarId!]
    else if v == v' && !t.isForall then
      -- the same action on both sides: substituted
      let ng ← mkFreshExprSyntheticOpaqueMVar (mkApp2 rel (b.instantiate1 v) (b'.instantiate1 v'))
      g.assign ng
      replaceMainGoal [ng.mvarId!]
    else
      -- two join points: related once
      let relOf (j j' : Lean.Expr) : MetaM Lean.Expr :=
        forallTelescope t fun xs _ => do
          jpForall xs (← mkAppM ``GSim #[ty.getAppArgs[1]!, (mkAppN j xs).headBeta, (mkAppN j' xs).headBeta])
      let h1Ty ← relOf v v'
      let h1 ← mkFreshExprSyntheticOpaqueMVar h1Ty
      let h2Ty ← withLocalDeclD n t fun j => withLocalDeclD n t fun j' => do
        let hT ← relOf j j'
        withLocalDeclD `hjp hT fun h => do
          mkForallFVars #[j, j', h] (mkApp2 rel (b.instantiate1 j) (b'.instantiate1 j'))
      let h2 ← mkFreshExprSyntheticOpaqueMVar h2Ty
      g.assign (mkApp3 h2 v v' h1)
      replaceMainGoal [h1.mvarId!, h2.mvarId!]
  | _, _ => throwError "gsim_let: no have"

open Lean Meta Elab Tactic in
/-- The goal relates two `if`s (`ite`, or with `dite`). Their decision procedures may
    differ in a field read through `shift k s`, which only default transparency sees
    through, so `sim_ite` is applied at default transparency, after this check. -/
elab "is_ite" : tactic => do
  let ty ← instantiateMVars (← (← getMainGoal).getType)
  let some (_, x, _) := relParts ty.consumeMData | throwError "is_ite"
  let f := x.consumeMData.getAppFn
  unless f.isConstOf ``ite || f.isConstOf ``dite do throwError "is_ite: no if"

open Lean Meta Elab Tactic in
/-- A jump to a join point, or a call of a function the context relates. -/
elab "gsim_hyp" : tactic => do
  let g ← getMainGoal
  g.withContext do
  let ty ← instantiateMVars (← g.getType)
  let some (_, x, _) := relParts ty.consumeMData | throwError "gsim_hyp"
  unless x.getAppFn.isFVar do throwError "gsim_hyp: not a variable"
  -- newest first: the join point's fact is among the last hypotheses
  for d in ((← getLCtx).foldl (fun acc d => acc.push d) #[]).reverse do
    if d.isImplementationDetail then continue
    let s ← saveState
    try
      let gs ← withReducible (g.apply d.toExpr)
      -- a jump passing an iteration function leaves its `ItemOK`
      if ← gs.allM fun m => do
          pure ((← instantiateMVars (← m.getType)).isAppOf `Gala.M5.ItemOK) then
        replaceMainGoal gs
        return
      restoreState s
    catch _ => restoreState s
  throwError "gsim_hyp: none"


set_option hygiene false in
/-- One step of relating two runs of the same code (`GSim`): a building block above, a
    or a jump to a join point (`gsim_hyp`). `gsim_auto` tries it first, then a helper
    (`gsim_help`), the induction hypothesis (`ih_step`), `rangeLoop`'s iterations
    (`item_step`), and last `dsimp` and `split` (an `if` or `match`, the same on both sides). -/
macro "gsim_base" : tactic => `(tactic| (first
  | gsim_let
  | with_reducible exact sim_pure _
  | with_reducible exact sim_throw _
  | with_reducible exact sim_tick
  | exact fun _ h => h
  | (with_reducible apply sim_modify); intro _; rfl
  | (with_reducible apply sim_get); intro _; try dsimp only [shift_cells, shift_types,
      shift_closures, shift_out, shift_printing, shift_globals, shift_defers,
      shift_recoverable, shift_maps, shift_localTypes, shift_localType?, shift_steps]
  | with_reducible apply sim_loopM_steps
  | (is_ite; first | apply sim_ite | apply sim_dite)
  | with_reducible apply sim_tryCatch
  | with_reducible intro _
  | with_reducible apply sim_bind_item  -- `rangeLoop`'s iterations, below
  | with_reducible apply sim_bind
  | with_reducible apply sim_map
  | with_reducible apply sim_seqRight
  | with_reducible apply sim_forIn
  | with_reducible apply sim_forIn_range
  | with_reducible apply sim_forIn_array
  | with_reducible apply sim_mapM
  | with_reducible apply sim_filterMapM
  | apply sim_optionMapM
  | with_reducible apply sim_flatMapM
  | gsim_hyp))

/-! ## Helpers outside the mutual block -/

set_option hygiene false in
macro "gsim_help" : tactic => `(tactic| (first
  | with_reducible exact sim_stuck _
  | with_reducible exact sim_unsupported _
  | with_reducible apply sim_runtimePanic
  | with_reducible apply sim_nilDeref
  | with_reducible apply sim_pkgCtx
  | with_reducible apply sim_bindTParams
  | with_reducible apply sim_stdFormatted?
  | with_reducible apply sim_single
  | with_reducible apply sim_asBool
  | with_reducible apply sim_asInt
  | with_reducible apply sim_asStr
  | with_reducible apply sim_solved?
  | with_reducible apply sim_sliceElems
  | with_reducible apply sim_spreadArgs
  | with_reducible apply sim_indexOutOfRange
  | with_reducible apply sim_sliceBounds
  | with_reducible apply sim_intOp
  | with_reducible apply sim_shiftOp
  | with_reducible apply sim_cmpOp
  | with_reducible apply sim_emit
  | with_reducible apply sim_allocAs
  | with_reducible apply sim_alloc
  | with_reducible apply sim_cellType
  | with_reducible apply sim_load
  | with_reducible apply sim_loadCell
  | with_reducible apply sim_storeCell
  | with_reducible apply sim_store
  | with_reducible apply sim_newMap
  | with_reducible apply sim_getMap
  | with_reducible apply sim_modifyMap
  | with_reducible apply sim_mapKeyOf
  | with_reducible apply sim_mapLookup
  | with_reducible apply sim_mapStore
  | with_reducible apply sim_constTo
  | with_reducible apply sim_inferTArgs))

macro "hauto" : tactic => `(tactic| repeat' (first | gsim_base | gsim_help | dsimp -zeta only | split))

theorem sim_stuck {k : Nat} (m : String) : GSim k (stuck m : GM α) (stuck m) := ⟨fun _ _ => rfl⟩
theorem sim_unsupported {k : Nat} (m : String) : GSim k (unsupported m : GM α) (unsupported m) :=
  ⟨fun _ _ => rfl⟩
theorem sim_runtimePanic {k : Nat} (m : String) : GSim k (runtimePanic m : GM α) (runtimePanic m) := by
  unfold runtimePanic; hauto
theorem sim_nilDeref {k : Nat} : GSim k (nilDeref : GM α) nilDeref := by unfold nilDeref; hauto
theorem sim_pkgCtx {k : Nat} (c : Ctx) (p : String) : GSim k (pkgCtx c p) (pkgCtx c p) := by
  unfold pkgCtx; hauto
theorem sim_bindTParams {k : Nat} (ps : List Field) (as : List Ty) : GSim k (bindTParams ps as) (bindTParams ps as) := by
  unfold bindTParams; hauto
theorem sim_single {k : Nat} (vs : List Val) : GSim k (Val.single vs) (Val.single vs) := by
  unfold Val.single; hauto
theorem sim_asBool {k : Nat} (v : Val) : GSim k (asBool v) (asBool v) := by unfold asBool; hauto
theorem sim_asInt {k : Nat} (v : Val) : GSim k (asInt v) (asInt v) := by unfold asInt; hauto
theorem sim_asStr {k : Nat} (v : Val) : GSim k (asStr v) (asStr v) := by unfold asStr; hauto
theorem sim_solved? {k : Nat} (e : List (String × Ty)) (x : String) :
    GSim k (solved? e x) (solved? e x) := by unfold solved?; hauto
theorem sim_emit {k : Nat} (t : String) : GSim k (emit t) (emit t) := by unfold emit; hauto
theorem sim_allocAs {k : Nat} (t : Ty) (v : Val) : GSim k (allocAs t v) (allocAs t v) := by
  unfold allocAs; hauto
theorem sim_alloc {k : Nat} (v : Val) : GSim k (alloc v) (alloc v) := by unfold alloc; hauto
theorem sim_cellType {k : Nat} (i : Nat) : GSim k (cellType i) (cellType i) := by
  unfold cellType; hauto
theorem sim_load {k : Nat} (l : Loc) : GSim k (load l) (load l) := by unfold load; hauto
theorem sim_loadCell {k : Nat} (i : Nat) : GSim k (loadCell i) (loadCell i) := by
  unfold loadCell; hauto
theorem sim_storeCell {k : Nat} (i : Nat) (v : Val) : GSim k (storeCell i v) (storeCell i v) := by
  unfold storeCell; hauto
theorem sim_store {k : Nat} (l : Loc) (v : Val) : GSim k (store l v) (store l v) := by
  unfold store; hauto
theorem sim_sliceElems {k : Nat} (v : Val) : GSim k (sliceElems v) (sliceElems v) := by
  unfold sliceElems; hauto
theorem sim_spreadArgs {k : Nat} (b : Bool) (vs : List Val) :
    GSim k (spreadArgs b vs) (spreadArgs b vs) := by unfold spreadArgs; hauto
theorem sim_indexOutOfRange {k : Nat} (i : Int) (n : Nat) :
    GSim k (indexOutOfRange i n : GM α) (indexOutOfRange i n) := by unfold indexOutOfRange; hauto
theorem sim_sliceBounds {k : Nat} (a b c : Option Int) (l p : Nat) (w : String) :
    GSim k (sliceBounds a b c l p w) (sliceBounds a b c l p w) := by unfold sliceBounds; hauto
theorem sim_intOp {k : Nat} (op : String) (t : Option IntKind) (x y : Int) :
    GSim k (intOp op t x y) (intOp op t x y) := by unfold intOp; hauto
theorem sim_shiftOp {k : Nat} (op : String) (t : Option IntKind) (x y : Int) :
    GSim k (shiftOp op t x y) (shiftOp op t x y) := by unfold shiftOp; hauto
theorem sim_cmpOp {k : Nat} (op : String) (o : Ordering) : GSim k (cmpOp op o) (cmpOp op o) := by
  unfold cmpOp; hauto
theorem sim_newMap {k : Nat} : GSim k newMap newMap := by unfold newMap; hauto
theorem sim_getMap {k : Nat} (i : Nat) : GSim k (getMap i) (getMap i) := by unfold getMap; hauto
theorem sim_modifyMap {k : Nat} (i : Nat) (f) : GSim k (modifyMap i f) (modifyMap i f) := by
  unfold modifyMap; hauto
theorem sim_mapKeyOf {k : Nat} (v : Val) : GSim k (mapKeyOf v) (mapKeyOf v) := by
  unfold mapKeyOf; hauto
theorem sim_mapLookup {k : Nat} (r : Option Nat) (m : MapKey) :
    GSim k (mapLookup r m) (mapLookup r m) := by unfold mapLookup; hauto
theorem sim_mapStore {k : Nat} (r : Option Nat) (key : Val) (m : MapKey) (v : Val) :
    GSim k (mapStore r key m v) (mapStore r key m v) := by unfold mapStore; hauto
theorem sim_constTo {k : Nat} (t u : Ty) (x : Int) (d : Nat) :
    GSim k (constTo t u x d) (constTo t u x d) := by unfold constTo; hauto
theorem sim_inferTArgs {k : Nat} (defs noCore tps sig ex vs sp) :
    GSim k (inferTArgs defs noCore tps sig ex vs sp) (inferTArgs defs noCore tps sig ex vs sp) := by
  unfold inferTArgs; hauto
theorem sim_stdFormatted? {k : Nat} (c : Ctx) (v : Val) :
    GSim k (stdFormatted? c v) (stdFormatted? c v) := by unfold stdFormatted?; hauto


/-! ## Every cell has a type: `Sync`

`allocAs` pushes a cell and its type together; `storeCell` and `store` replace a cell's
value in place; nothing else writes `cells` or `types`. So every state a run reaches
from one with as many types as cells has as many (`syncAll`), on success and on any
`Stop` alike. -/

/-- As many cell types as cells. -/
def Sync (s : St) : Prop := s.cells.size = s.types.size

/-- `x` keeps `P` of the state, whatever it ends in. -/
def Pres (P : St → Prop) (x : GM α) : Prop := ∀ s, P s → P (x.run.run s).2

open Lean Meta Elab Tactic in
/-- `Pres P (have x := v; b)`: a join point's fact once, other values a variable. -/
elab "pres_let" : tactic => do
  let g ← getMainGoal
  g.withContext do
  let ty ← instantiateMVars (← g.getType)
  let ty := ty.consumeMData
  unless ty.isAppOf ``Pres do throwError "pres_let: not Pres"
  let args := ty.getAppArgs
  let rel := mkAppN ty.getAppFn (args.extract 0 (args.size - 1))
  match (args[args.size - 1]!).consumeMData with
  | .letE n t v b _ =>
    if ← isGMFun t then
      let relOf (j : Lean.Expr) : MetaM Lean.Expr :=
        forallTelescope t fun xs _ => do
          jpForall xs (← mkAppM ``Pres #[args[1]!, (mkAppN j xs).headBeta])
      let h1 ← mkFreshExprSyntheticOpaqueMVar (← relOf v)
      let h2Ty ← withLocalDeclD n t fun j => do
        withLocalDeclD `hjp (← relOf j) fun h => do
          mkForallFVars #[j, h] (mkApp rel (b.instantiate1 j))
      let h2 ← mkFreshExprSyntheticOpaqueMVar h2Ty
      g.assign (mkApp2 h2 v h1)
      replaceMainGoal [h1.mvarId!, h2.mvarId!]
    else
      let ng ← mkFreshExprSyntheticOpaqueMVar (Lean.Expr.forallE n t (mkApp rel b) .default)
      g.assign (mkApp ng v)
      replaceMainGoal [ng.mvarId!]
  | _ => throwError "pres_let: no have"

open Lean Meta Elab Tactic in
/-- A jump to a join point the context keeps `Pres` of. -/
elab "pres_hyp" : tactic => do
  let g ← getMainGoal
  g.withContext do
  let ty ← instantiateMVars (← g.getType)
  let args := ty.consumeMData.getAppArgs
  unless ty.consumeMData.isAppOf ``Pres do throwError "pres_hyp"
  unless (args[args.size - 1]!).getAppFn.isFVar do throwError "pres_hyp: not a variable"
  for d in ((← getLCtx).foldl (fun acc d => acc.push d) #[]).reverse do
    if d.isImplementationDetail then continue
    let s ← saveState
    try
      let gs ← withReducible (g.apply d.toExpr)
      if ← gs.allM fun m => do
          pure ((← instantiateMVars (← m.getType)).isAppOf `Gala.M5.ItemOK) then
        replaceMainGoal gs
        return
      restoreState s
    catch _ => restoreState s
  throwError "pres_hyp: none"


theorem pres_bind {P : St → Prop} {x : GM α} {f : α → GM β} (hx : Pres P x)
    (hf : ∀ a, Pres P (f a)) : Pres P (x >>= f) := by
  intro s h
  rw [grun_bind]
  rcases hxs : x.run.run s with ⟨r, t⟩
  have ht : P t := by have := hx s h; rw [hxs] at this; exact this
  cases r with
  | ok a => exact hf a t ht
  | error e => exact ht

theorem pres_pure {P : St → Prop} (a : α) : Pres P (pure a : GM α) := fun _ h => h
theorem pres_throw {P : St → Prop} (e : Stop) : Pres P (throw e : GM α) := fun _ h => h
theorem pres_get {P : St → Prop} {f : St → GM β} (h : ∀ s, Pres P (f s)) :
    Pres P (get >>= f) := fun s hs => h s s hs
theorem pres_modify {P : St → Prop} {f : St → St} (h : ∀ s, P s → P (f s)) :
    Pres P (modify f : GM Unit) := fun s hs => h s hs

theorem pres_map {P : St → Prop} {x : GM α} (f : α → β) (hx : Pres P x) : Pres P (f <$> x) := by
  rw [← bind_pure_comp]; exact pres_bind hx fun _ => pres_pure _

theorem pres_seqRight {P : St → Prop} {x : GM α} {y : GM β} (hx : Pres P x) (hy : Pres P y) :
    Pres P (x *> y) := by
  rw [seqRight_eq_bind]; exact pres_bind hx fun _ => hy

theorem pres_ite {P : St → Prop} (c : Prop) [Decidable c] {a b : GM α}
    (h1 : c → Pres P a) (h2 : ¬ c → Pres P b) : Pres P (if c then a else b) := by
  cases ‹Decidable c› with
  | isTrue h => exact h1 h
  | isFalse h => exact h2 h

theorem pres_dite {P : St → Prop} (c : Prop) [Decidable c] {a : c → GM α} {b : ¬ c → GM α}
    (h1 : ∀ h, Pres P (a h)) (h2 : ∀ h, Pres P (b h)) : Pres P (if h : c then a h else b h) := by
  cases ‹Decidable c› with
  | isTrue h => exact h1 h
  | isFalse h => exact h2 h

theorem pres_forIn {P : St → Prop} {β : Type} (xs : List γ) (b : β) {f : γ → β → GM (ForInStep β)}
    (hf : ∀ a b, Pres P (f a b)) : Pres P (forIn xs b f) := by
  induction xs generalizing b with
  | nil => exact pres_pure _
  | cons a as ih =>
    simp only [List.forIn_cons]
    refine pres_bind (hf a b) fun r => ?_
    cases r with
    | done b => exact pres_pure _
    | yield b => exact ih b

theorem pres_forIn_array {P : St → Prop} {β : Type} (xs : Array γ) (b : β)
    {f : γ → β → GM (ForInStep β)} (hf : ∀ a b, Pres P (f a b)) : Pres P (forIn xs b f) := by
  rw [← Array.forIn_toList]
  exact pres_forIn _ b hf

theorem pres_forIn_range {P : St → Prop} {β : Type} (r : Std.Legacy.Range) (b : β)
    {f : Nat → β → GM (ForInStep β)} (hf : ∀ a b, Pres P (f a b)) : Pres P (forIn r b f) := by
  rw [Std.Legacy.Range.forIn_eq_forIn_range']
  exact pres_forIn _ b hf

theorem pres_mapM {P : St → Prop} (xs : List γ) {f : γ → GM β} (hf : ∀ a, Pres P (f a)) :
    Pres P (xs.mapM f) := by
  induction xs with
  | nil => exact pres_pure _
  | cons a as ih =>
    simp only [List.mapM_cons]
    exact pres_bind (hf a) fun _ => pres_bind ih fun _ => pres_pure _

theorem pres_optionMapM {P : St → Prop} (o : Option γ) {f : γ → GM β}
    (hf : ∀ a, Pres P (f a)) : Pres P (o.mapM f) := by
  cases o with
  | none => exact pres_pure _
  | some a => exact pres_bind (hf a) fun _ => pres_pure _

theorem pres_filterMapM {P : St → Prop} (xs : List γ) {f : γ → GM (Option β)}
    (hf : ∀ a, Pres P (f a)) : Pres P (xs.filterMapM f) := by
  induction xs with
  | nil => exact pres_pure _
  | cons a as ih =>
    simp only [List.filterMapM_cons]
    refine pres_bind (hf a) fun r => ?_
    cases r with
    | none => exact ih
    | some b => exact pres_bind ih fun _ => pres_pure _

theorem pres_flatMapM {P : St → Prop} (xs : List γ) {f : γ → GM (List β)}
    (hf : ∀ a, Pres P (f a)) : Pres P (xs.flatMapM f) := by
  induction xs with
  | nil => exact pres_pure _
  | cons a as ih =>
    simp only [List.flatMapM_cons]
    exact pres_bind (hf a) fun _ => pres_bind ih fun _ => pres_pure _

theorem pres_tryCatch {P : St → Prop} {x : GM α} {h : Stop → GM α} (hx : Pres P x)
    (hh : ∀ e, Pres P (h e)) : Pres P (tryCatch x h) := by
  intro s hs
  rw [run_tryCatch_eq]
  rcases hxs : x.run.run s with ⟨r, t⟩
  have ht : P t := by have := hx s hs; rw [hxs] at this; exact this
  cases r with
  | ok a => exact ht
  | error e => exact hh e t ht

theorem pres_loopM {P : St → Prop} (b : Nat) (env : Env) {body : Env → GM (Option Env)}
    (hbody : ∀ env, Pres P (body env)) : Pres P (loopM b env body) := by
  induction b generalizing env with
  | zero => rw [loopM]; exact pres_throw _
  | succ b ih =>
    rw [loopM]
    refine pres_bind (hbody env) fun r => ?_
    split
    · exact ih _
    · exact pres_pure _

theorem sync_allocAs (t : Ty) (v : Val) : Pres Sync (allocAs t v) := by
  intro s hs
  simp only [Sync] at hs ⊢
  show (s.cells.push v).size = (s.types.push t).size
  simp [hs]

theorem sync_storeCell (i : Nat) (v : Val) : Pres Sync (storeCell i v) := by
  intro s hs
  simp only [Sync] at hs ⊢
  show (s.cells.set! i v).size = s.types.size
  simp [hs]

set_option hygiene false in
macro "pres_base" : tactic => `(tactic| (first
  | pres_let
  | with_reducible exact pres_pure _
  | with_reducible exact pres_throw _
  | (with_reducible apply pres_modify); intro _ h; exact h
  | (with_reducible apply pres_get); intro _
  | with_reducible apply pres_ite
  | with_reducible apply pres_dite
  | with_reducible apply pres_tryCatch
  | with_reducible apply pres_loopM
  | with_reducible intro _
  | with_reducible apply pres_bind_item  -- `rangeLoop`'s iterations, below
  | with_reducible apply pres_bind
  | with_reducible apply pres_map
  | with_reducible apply pres_seqRight
  | with_reducible apply pres_forIn
  | with_reducible apply pres_forIn_range
  | with_reducible apply pres_forIn_array
  | with_reducible apply pres_mapM
  | with_reducible apply pres_filterMapM
  | apply pres_optionMapM
  | with_reducible apply pres_flatMapM
  | pres_hyp))

set_option hygiene false in
macro "pres_help" : tactic => `(tactic| (first
  | with_reducible exact sync_allocAs _ _
  | with_reducible exact sync_storeCell _ _
  | with_reducible apply sync_store
  | with_reducible apply sync_tick
  | with_reducible apply sync_stuck
  | with_reducible apply sync_unsupported
  | with_reducible apply sync_runtimePanic
  | with_reducible apply sync_nilDeref
  | with_reducible apply sync_pkgCtx
  | with_reducible apply sync_bindTParams
  | with_reducible apply sync_stdFormatted?
  | with_reducible apply sync_single
  | with_reducible apply sync_asBool
  | with_reducible apply sync_asInt
  | with_reducible apply sync_asStr
  | with_reducible apply sync_solved?
  | with_reducible apply sync_sliceElems
  | with_reducible apply sync_spreadArgs
  | with_reducible apply sync_indexOutOfRange
  | with_reducible apply sync_sliceBounds
  | with_reducible apply sync_intOp
  | with_reducible apply sync_shiftOp
  | with_reducible apply sync_cmpOp
  | with_reducible apply sync_emit
  | with_reducible apply sync_alloc
  | with_reducible apply sync_cellType
  | with_reducible apply sync_load
  | with_reducible apply sync_loadCell
  | with_reducible apply sync_newMap
  | with_reducible apply sync_getMap
  | with_reducible apply sync_modifyMap
  | with_reducible apply sync_mapKeyOf
  | with_reducible apply sync_mapLookup
  | with_reducible apply sync_mapStore
  | with_reducible apply sync_constTo
  | with_reducible apply sync_inferTArgs))

macro "pauto" : tactic => `(tactic| repeat' (first | pres_base | pres_help | dsimp -zeta only | split))

theorem sync_tick : Pres Sync tick := by unfold tick; pauto
theorem sync_stuck (m : String) : Pres Sync (stuck m : GM α) := fun _ h => h
theorem sync_unsupported (m : String) : Pres Sync (unsupported m : GM α) := fun _ h => h
theorem sync_runtimePanic (m : String) : Pres Sync (runtimePanic m : GM α) := by
  unfold runtimePanic; pauto
theorem sync_nilDeref : Pres Sync (nilDeref : GM α) := by unfold nilDeref; pauto
theorem sync_pkgCtx (c : Ctx) (p : String) : Pres Sync (pkgCtx c p) := by unfold pkgCtx; pauto
theorem sync_bindTParams (ps : List Field) (as : List Ty) : Pres Sync (bindTParams ps as) := by
  unfold bindTParams; pauto
theorem sync_single (vs : List Val) : Pres Sync (Val.single vs) := by unfold Val.single; pauto
theorem sync_asBool (v : Val) : Pres Sync (asBool v) := by unfold asBool; pauto
theorem sync_asInt (v : Val) : Pres Sync (asInt v) := by unfold asInt; pauto
theorem sync_asStr (v : Val) : Pres Sync (asStr v) := by unfold asStr; pauto
theorem sync_solved? (e : List (String × Ty)) (x : String) : Pres Sync (solved? e x) := by
  unfold solved?; pauto
theorem sync_emit (t : String) : Pres Sync (emit t) := by unfold emit; pauto
theorem sync_alloc (v : Val) : Pres Sync (alloc v) := by unfold alloc; pauto
theorem sync_cellType (i : Nat) : Pres Sync (cellType i) := by unfold cellType; pauto
theorem sync_load (l : Loc) : Pres Sync (load l) := by unfold load; pauto
theorem sync_loadCell (i : Nat) : Pres Sync (loadCell i) := by unfold loadCell; pauto
theorem sync_sliceElems (v : Val) : Pres Sync (sliceElems v) := by unfold sliceElems; pauto
theorem sync_spreadArgs (b : Bool) (vs : List Val) : Pres Sync (spreadArgs b vs) := by
  unfold spreadArgs; pauto
theorem sync_indexOutOfRange (i : Int) (n : Nat) : Pres Sync (indexOutOfRange i n : GM α) := by
  unfold indexOutOfRange; pauto
theorem sync_sliceBounds (a b c : Option Int) (l p : Nat) (w : String) :
    Pres Sync (sliceBounds a b c l p w) := by unfold sliceBounds; pauto
theorem sync_intOp (op : String) (t : Option IntKind) (x y : Int) : Pres Sync (intOp op t x y) := by
  unfold intOp; pauto
theorem sync_shiftOp (op : String) (t : Option IntKind) (x y : Int) :
    Pres Sync (shiftOp op t x y) := by unfold shiftOp; pauto
theorem sync_cmpOp (op : String) (o : Ordering) : Pres Sync (cmpOp op o) := by unfold cmpOp; pauto
theorem sync_newMap : Pres Sync newMap := by unfold newMap; pauto
theorem sync_getMap (i : Nat) : Pres Sync (getMap i) := by unfold getMap; pauto
theorem sync_modifyMap (i : Nat) (f) : Pres Sync (modifyMap i f) := by unfold modifyMap; pauto
theorem sync_mapKeyOf (v : Val) : Pres Sync (mapKeyOf v) := by unfold mapKeyOf; pauto
theorem sync_mapLookup (r : Option Nat) (m : MapKey) : Pres Sync (mapLookup r m) := by
  unfold mapLookup; pauto
theorem sync_mapStore (r : Option Nat) (key : Val) (m : MapKey) (v : Val) :
    Pres Sync (mapStore r key m v) := by unfold mapStore; pauto
theorem sync_constTo (t u : Ty) (x : Int) (d : Nat) : Pres Sync (constTo t u x d) := by
  unfold constTo; pauto
theorem sync_inferTArgs (defs noCore tps sig ex vs sp) :
    Pres Sync (inferTArgs defs noCore tps sig ex vs sp) := by unfold inferTArgs; pauto
theorem sync_stdFormatted? (c : Ctx) (v : Val) : Pres Sync (stdFormatted? c v) := by
  unfold stdFormatted?; pauto

theorem sync_store (l : Loc) (w : Val) : Pres Sync (store l w) := by
  unfold store
  refine pres_get fun s => ?_
  pres_let
  · intro _ s hs
    simp only [Sync] at hs ⊢
    show (s.cells.modify l.cell _).size = s.types.size
    simp [hs]
  · pauto


/-! ## `rangeLoop`'s iterations

`rangeLoop` first computes the number of iterations and a function giving each one's key
and value, `item`, then calls `item i` in each iteration. The do-notation passes the pair
to the rest of the loop as a join point's argument, so relating the calls needs a fact
about it: every `item` the computation passes loads or builds its values and does
nothing else (`ItemOK`). The join point is related under that hypothesis (`jpForall`),
and each jump proves it of the function it passes. -/

/-- An iteration's key and value: computed with no effect but reading the state, so the
    same with more steps, and keeping every cell typed. -/
def ItemOK (a : Nat × (Nat → GM (Val × Val))) : Prop :=
  ∀ i, (∀ k, GSim k (a.2 i) (a.2 i)) ∧ Pres Sync (a.2 i)

/-- Every value `x` returns satisfies `P`. -/
def Post (P : α → Prop) (x : GM α) : Prop := ∀ s a t, x.run.run s = (.ok a, t) → P a

theorem post_bind {P : β → Prop} {x : GM α} {f : α → GM β} (hf : ∀ a, Post P (f a)) :
    Post P (x >>= f) := by
  intro s a t h
  rw [grun_bind] at h
  rcases hxs : x.run.run s with ⟨r, u⟩
  rw [hxs] at h
  cases r with
  | ok b => exact hf b u a t h
  | error e => cases h

theorem post_pure {P : α → Prop} {a : α} (h : P a) : Post P (pure a : GM α) := by
  intro s b t hb
  cases hb
  exact h

theorem post_throw {P : α → Prop} (e : Stop) : Post P (throw e : GM α) := by
  intro s b t hb; cases hb

theorem post_stuck {P : α → Prop} (m : String) : Post P (stuck m : GM α) := post_throw _
theorem post_unsupported {P : α → Prop} (m : String) : Post P (unsupported m : GM α) := post_throw _

theorem post_ite {P : α → Prop} (c : Prop) [Decidable c] {a b : GM α}
    (h1 : c → Post P a) (h2 : ¬ c → Post P b) : Post P (if c then a else b) := by
  cases ‹Decidable c› with
  | isTrue h => exact h1 h
  | isFalse h => exact h2 h

/-- A computed iteration function passed on: `ItemOK` of every one the computation returns. -/
theorem sim_bind_item {k : Nat} {x y : GM (Nat × (Nat → GM (Val × Val)))}
    {f g : Nat × (Nat → GM (Val × Val)) → GM β} (hx : GSim k x y) (hp : Post ItemOK x)
    (hf : ∀ a, ItemOK a → GSim k (f a) (g a)) : GSim k (x >>= f) (y >>= g) := by
  refine ⟨fun s h => ?_⟩
  rw [grun_bind] at h ⊢
  rw [grun_bind]
  rcases hxs : x.run.run s with ⟨r, t⟩
  rw [hxs] at h
  cases r with
  | ok a =>
    rw [hx.prop s (by rw [hxs]; trivial), hxs]
    exact (hf a (hp s a t hxs)).prop t h
  | error e =>
    have h' : GNotFuel (Except.error e : Except Stop β) := by simpa using h
    rw [hx.prop s (by rw [hxs]; exact notFuel_error h'), hxs]

theorem pres_bind_item {x : GM (Nat × (Nat → GM (Val × Val)))}
    {f : Nat × (Nat → GM (Val × Val)) → GM β} (hx : Pres Sync x) (hp : Post ItemOK x)
    (hf : ∀ a, ItemOK a → Pres Sync (f a)) : Pres Sync (x >>= f) := by
  intro s hs
  rw [grun_bind]
  rcases hxs : x.run.run s with ⟨r, t⟩
  have ht : Sync t := by have := hx s hs; rw [hxs] at this; exact this
  cases r with
  | ok a => exact hf a (hp s a t hxs) t ht
  | error e => exact ht


theorem itemOK_sim {n : Nat} {it : Nat → GM (Val × Val)} (h : ItemOK (n, it)) {k i : Nat} :
    GSim k (it i) (it i) := (h i).1 k

theorem itemOK_pres {n : Nat} {it : Nat → GM (Val × Val)} (h : ItemOK (n, it)) {i : Nat} :
    Pres Sync (it i) := (h i).2

theorem itemOK_sim2 {a : Nat × (Nat → GM (Val × Val))} (h : ItemOK a) {k i : Nat} :
    GSim k (a.2 i) (a.2 i) := (h i).1 k

theorem itemOK_pres2 {a : Nat × (Nat → GM (Val × Val))} (h : ItemOK a) {i : Nat} :
    Pres Sync (a.2 i) := (h i).2

set_option hygiene false in
/-- Relating `rangeLoop`'s calls of its iteration function, and proving `ItemOK` of the
    functions it computes. -/
macro "item_step" : tactic => `(tactic| (first
  | with_reducible exact post_throw _
  | with_reducible exact post_stuck _
  | with_reducible exact post_unsupported _
  | (show Post _ _; dsimp only)
  | with_reducible apply post_bind
  | with_reducible apply post_ite
  | with_reducible apply post_pure
  | (show ItemOK _; assumption)
  | ((show ItemOK _; refine fun _ => ⟨fun _ => ?_, ?_⟩) <;> first | (hauto; done) | (pauto; done))
  | exact itemOK_sim (by assumption)
  | exact itemOK_pres (by assumption)
  | exact itemOK_sim2 (by assumption)
  | exact itemOK_pres2 (by assumption)))
/-- Every function of the evaluator's mutual block does the same at depth `n'` as at
    depth `n`, given `k` more steps (`GSim`). -/
structure SimAll (c : Ctx) (k n n' : Nat) : Prop where
  typeOf? : ∀ (a0 : Env) (a1 : Expr), GSim k (typeOf? c n a0 a1) (typeOf? c n' a0 a1)
  evalType : ∀ (a0 : Env) (a1 : Expr), GSim k (evalType c n a0 a1) (evalType c n' a0 a1)
  underlying : ∀ (a0 : Ty), GSim k (underlying c n a0) (underlying c n' a0)
  zero : ∀ (a0 : Ty), GSim k (zero c n a0) (zero c n' a0)
  convert : ∀ (a0 : Ty) (a1 : Val), GSim k (convert c n a0 a1) (convert c n' a0 a1)
  conversion : ∀ (a0 : Ty) (a1 : Val), GSim k (conversion c n a0 a1) (conversion c n' a0 a1)
  evalExpr : ∀ (a0 : Env) (a1 : Expr), GSim k (evalExpr c n a0 a1) (evalExpr c n' a0 a1)
  evalName : ∀ (a0 : Env) (a1 : String) (a2 : String), GSim k (evalName c n a0 a1 a2) (evalName c n' a0 a1 a2)
  funcVal : ∀ (a0 : String) (a1 : String) (a2 : List Ty), GSim k (funcVal c n a0 a1 a2) (funcVal c n' a0 a1 a2)
  funcValue? : ∀ (a0 : Env) (a1 : Expr), GSim k (funcValue? c n a0 a1) (funcValue? c n' a0 a1)
  composite : ∀ (a0 : Env) (a1 : Ty) (a2 : List Expr), GSim k (composite c n a0 a1 a2) (composite c n' a0 a1 a2)
  newArray : ∀ (a0 : Ty) (a1 : Array Val) (a2 : Nat), GSim k (newArray c n a0 a1 a2) (newArray c n' a0 a1 a2)
  stringBytes : ∀ (a0 : Ty) (a1 : Ty) (a2 : String), GSim k (stringBytes c n a0 a1 a2) (stringBytes c n' a0 a1 a2)
  elementKeys : ∀ (a0 : Env) (a1 : List Expr), GSim k (elementKeys c n a0 a1) (elementKeys c n' a0 a1)
  elements : ∀ (a0 : Env) (a1 : Ty) (a2 : List Expr), GSim k (elements c n a0 a1 a2) (elements c n' a0 a1 a2)
  staticElement : ∀ (a0 : Env) (a1 : Ty) (a2 : Expr), GSim k (staticElement c n a0 a1 a2) (staticElement c n' a0 a1 a2)
  literalElement : ∀ (a0 : Env) (a1 : Ty) (a2 : Expr), GSim k (literalElement c n a0 a1 a2) (literalElement c n' a0 a1 a2)
  constantOperand : ∀ (a0 : Env) (a1 : Expr), GSim k (constantOperand c n a0 a1) (constantOperand c n' a0 a1)
  element : ∀ (a0 : Val) (a1 : Option Loc) (a2 : Val), GSim k (element c n a0 a1 a2) (element c n' a0 a1 a2)
  mapKey : ∀ (a0 : Ty) (a1 : Val), GSim k (mapKey c n a0 a1) (mapKey c n' a0 a1)
  mapIndex : ∀ (a0 : Option Nat) (a1 : MapKey) (a2 : Ty), GSim k (mapIndex c n a0 a1 a2) (mapIndex c n' a0 a1 a2)
  sliceOf : ∀ (a0 : Env) (a1 : Expr) (a2 : Option Expr) (a3 : Option Expr) (a4 : Option Expr), GSim k (sliceOf c n a0 a1 a2 a3 a4) (sliceOf c n' a0 a1 a2 a3 a4)
  selector : ∀ (a0 : Env) (a1 : Expr) (a2 : String), GSim k (selector c n a0 a1 a2) (selector c n' a0 a1 a2)
  boundMethod : ∀ (a0 : String) (a1 : String) (a2 : String) (a3 : List Ty) (a4 : Val), GSim k (boundMethod c n a0 a1 a2 a3 a4) (boundMethod c n' a0 a1 a2 a3 a4)
  field? : ∀ (a0 : Val) (a1 : String), GSim k (field? c n a0 a1) (field? c n' a0 a1)
  operand : ∀ (a0 : Env) (a1 : Expr), GSim k (operand c n a0 a1) (operand c n' a0 a1)
  selectOn : ∀ (a0 : Val) (a1 : Option Loc) (a2 : String), GSim k (selectOn c n a0 a1 a2) (selectOn c n' a0 a1 a2)
  evalAddr : ∀ (a0 : Env) (a1 : Expr), GSim k (evalAddr c n a0 a1) (evalAddr c n' a0 a1)
  evalPlace : ∀ (a0 : Env) (a1 : Expr), GSim k (evalPlace c n a0 a1) (evalPlace c n' a0 a1)
  elemLoc : ∀ (a0 : Val) (a1 : Option Loc) (a2 : Val), GSim k (elemLoc c n a0 a1 a2) (elemLoc c n' a0 a1 a2)
  loadPlace : ∀ (a0 : Place), GSim k (loadPlace c n a0) (loadPlace c n' a0)
  storePlace : ∀ (a0 : Place) (a1 : Val), GSim k (storePlace c n a0 a1) (storePlace c n' a0 a1)
  assertType : ∀ (a0 : Val) (a1 : Ty), GSim k (assertType c n a0 a1) (assertType c n' a0 a1)
  methodOfType : ∀ (a0 : Ty) (a1 : String) (a2 : Ty), GSim k (methodOfType c n a0 a1 a2) (methodOfType c n' a0 a1 a2)
  unary : ∀ (a0 : String) (a1 : Val), GSim k (unary c n a0 a1) (unary c n' a0 a1)
  binary : ∀ (a0 : String) (a1 : Val) (a2 : Val), GSim k (binary c n a0 a1 a2) (binary c n' a0 a1 a2)
  equal : ∀ (a0 : Val) (a1 : Val), GSim k (equal c n a0 a1) (equal c n' a0 a1)
  equalAll : ∀ (a0 : List Val) (a1 : List Val), GSim k (equalAll c n a0 a1) (equalAll c n' a0 a1)
  evalCall : ∀ (a0 : Env) (a1 : Expr) (a2 : List Expr) (a3 : Bool), GSim k (evalCall c n a0 a1 a2 a3) (evalCall c n' a0 a1 a2 a3)
  appendValues : ∀ (a0 : Ty) (a1 : Ty) (a2 : Val) (a3 : List Val) (a4 : Bool), GSim k (appendValues c n a0 a1 a2 a3 a4) (appendValues c n' a0 a1 a2 a3 a4)
  layout : ∀ (a0 : Ty), GSim k (layout c n a0) (layout c n' a0)
  genericArgs : ∀ (a0 : Env) (a1 : Val) (a2 : List Expr) (a3 : Bool), GSim k (genericArgs c n a0 a1 a2 a3) (genericArgs c n' a0 a1 a2 a3)
  declaredFunc? : ∀ (a0 : Env) (a1 : Expr), GSim k (declaredFunc? c n a0 a1) (declaredFunc? c n' a0 a1)
  deferCall : ∀ (a0 : Env) (a1 : Expr) (a2 : List Expr) (a3 : Bool), GSim k (deferCall c n a0 a1 a2 a3) (deferCall c n' a0 a1 a2 a3)
  evalArgs : ∀ (a0 : Env) (a1 : List Expr), GSim k (evalArgs c n a0 a1) (evalArgs c n' a0 a1)
  builtin : ∀ (a0 : Env) (a1 : String) (a2 : List Expr) (a3 : Bool), GSim k (builtin c n a0 a1 a2 a3) (builtin c n' a0 a1 a2 a3)
  panicText : ∀ (a0 : Val), GSim k (panicText c n a0) (panicText c n' a0)
  callMethod? : ∀ (a0 : Val) (a1 : String), GSim k (callMethod? c n a0 a1) (callMethod? c n' a0 a1)
  callValue : ∀ (a0 : Val) (a1 : List Val) (a2 : Bool), GSim k (callValue c n a0 a1 a2) (callValue c n' a0 a1 a2)
  callMethod : ∀ (a0 : String) (a1 : List Ty) (a2 : Method) (a3 : Val) (a4 : List Val) (a5 : Bool), GSim k (callMethod c n a0 a1 a2 a3 a4 a5) (callMethod c n' a0 a1 a2 a3 a4 a5)
  callBody : ∀ (a0 : Env) (a1 : Sig) (a2 : Option (List Stmt)) (a3 : Option (String × Val)) (a4 : List Val) (a5 : Bool), GSim k (callBody c n a0 a1 a2 a3 a4 a5) (callBody c n' a0 a1 a2 a3 a4 a5)
  execStmts : ∀ (a0 : Env) (a1 : List Stmt), GSim k (execStmts c n a0 a1) (execStmts c n' a0 a1)
  execBlock : ∀ (a0 : Env) (a1 : List Stmt), GSim k (execBlock c n a0 a1) (execBlock c n' a0 a1)
  execStmt : ∀ (a0 : Env) (a1 : Stmt), GSim k (execStmt c n a0 a1) (execStmt c n' a0 a1)
  declValue : ∀ (a0 : Option Ty) (a1 : Option Val), GSim k (declValue c n a0 a1) (declValue c n' a0 a1)
  execInit : ∀ (a0 : Env) (a1 : Option Stmt), GSim k (execInit c n a0 a1) (execInit c n' a0 a1)
  switchClauses : ∀ (a0 : Env) (a1 : Val) (a2 : List Clause), GSim k (switchClauses c n a0 a1 a2) (switchClauses c n' a0 a1 a2)
  typeClauses : ∀ (a0 : Env) (a1 : Option String) (a2 : Val) (a3 : List Clause), GSim k (typeClauses c n a0 a1 a2 a3) (typeClauses c n' a0 a1 a2 a3)
  forLoop : ∀ (a0 : Env) (a1 : Option Expr) (a2 : Option Stmt) (a3 : List Stmt), GSim k (forLoop c n a0 a1 a2 a3) (forLoop c n' a0 a1 a2 a3)
  rangeLoop : ∀ (a0 : Env) (a1 : Option Expr) (a2 : Option Expr) (a3 : String) (a4 : Expr) (a5 : List Stmt), GSim k (rangeLoop c n a0 a1 a2 a3 a4 a5) (rangeLoop c n' a0 a1 a2 a3 a4 a5)
  assign : ∀ (a0 : Env) (a1 : String) (a2 : List Expr) (a3 : List Expr), GSim k (assign c n a0 a1 a2 a3) (assign c n' a0 a1 a2 a3)
  rhsValues : ∀ (a0 : Env) (a1 : Nat) (a2 : List Expr), GSim k (rhsValues c n a0 a1 a2) (rhsValues c n' a0 a1 a2)
  errorsNew : ∀ (a0 : String), GSim k (errorsNew c n a0) (errorsNew c n' a0)
  fmtCall : ∀ (a0 : String) (a1 : List Val), GSim k (fmtCall c n a0 a1) (fmtCall c n' a0 a1)
  sprint : ∀ (a0 : List Val), GSim k (sprint c n a0) (sprint c n' a0)
  sprintf : ∀ (a0 : List Char) (a1 : List Val), GSim k (sprintf c n a0 a1) (sprintf c n' a0 a1)
  fmtSpec : ∀ (a0 : Val) (a1 : Char) (a2 : FmtSpec), GSim k (fmtSpec c n a0 a1 a2) (fmtSpec c n' a0 a1 a2)
  fmtArg : ∀ (a0 : Val) (a1 : Char), GSim k (fmtArg c n a0 a1) (fmtArg c n' a0 a1)
  fmtValue : ∀ (a0 : Val) (a1 : Char) (a2 : Nat) (a3 : Bool), GSim k (fmtValue c n a0 a1 a2 a3) (fmtValue c n' a0 a1 a2 a3)
  libCall : ∀ (a0 : String) (a1 : List Val), GSim k (libCall c n a0 a1) (libCall c n' a0 a1)
  formatField : ∀ (a0 : Val), GSim k (formatField c n a0) (formatField c n' a0)

set_option hygiene false in
/-- A recursive call, of either bundle: the induction hypothesis's field. -/
macro "ih_step" : tactic => `(tactic| (first
  | with_reducible apply ih.typeOf?
  | with_reducible apply ih.evalType
  | with_reducible apply ih.underlying
  | with_reducible apply ih.zero
  | with_reducible apply ih.convert
  | with_reducible apply ih.conversion
  | with_reducible apply ih.evalExpr
  | with_reducible apply ih.evalName
  | with_reducible apply ih.funcVal
  | with_reducible apply ih.funcValue?
  | with_reducible apply ih.composite
  | with_reducible apply ih.newArray
  | with_reducible apply ih.stringBytes
  | with_reducible apply ih.elementKeys
  | with_reducible apply ih.elements
  | with_reducible apply ih.staticElement
  | with_reducible apply ih.literalElement
  | with_reducible apply ih.constantOperand
  | with_reducible apply ih.element
  | with_reducible apply ih.mapKey
  | with_reducible apply ih.mapIndex
  | with_reducible apply ih.sliceOf
  | with_reducible apply ih.selector
  | with_reducible apply ih.boundMethod
  | with_reducible apply ih.field?
  | with_reducible apply ih.operand
  | with_reducible apply ih.selectOn
  | with_reducible apply ih.evalAddr
  | with_reducible apply ih.evalPlace
  | with_reducible apply ih.elemLoc
  | with_reducible apply ih.loadPlace
  | with_reducible apply ih.storePlace
  | with_reducible apply ih.assertType
  | with_reducible apply ih.methodOfType
  | with_reducible apply ih.unary
  | with_reducible apply ih.binary
  | with_reducible apply ih.equal
  | with_reducible apply ih.equalAll
  | with_reducible apply ih.evalCall
  | with_reducible apply ih.appendValues
  | with_reducible apply ih.layout
  | with_reducible apply ih.genericArgs
  | with_reducible apply ih.declaredFunc?
  | with_reducible apply ih.deferCall
  | with_reducible apply ih.evalArgs
  | with_reducible apply ih.builtin
  | with_reducible apply ih.panicText
  | with_reducible apply ih.callMethod?
  | with_reducible apply ih.callValue
  | with_reducible apply ih.callMethod
  | with_reducible apply ih.callBody
  | with_reducible apply ih.execStmts
  | with_reducible apply ih.execBlock
  | with_reducible apply ih.execStmt
  | with_reducible apply ih.declValue
  | with_reducible apply ih.execInit
  | with_reducible apply ih.switchClauses
  | with_reducible apply ih.typeClauses
  | with_reducible apply ih.forLoop
  | with_reducible apply ih.rangeLoop
  | with_reducible apply ih.assign
  | with_reducible apply ih.rhsValues
  | with_reducible apply ih.errorsNew
  | with_reducible apply ih.fmtCall
  | with_reducible apply ih.sprint
  | with_reducible apply ih.sprintf
  | with_reducible apply ih.fmtSpec
  | with_reducible apply ih.fmtArg
  | with_reducible apply ih.fmtValue
  | with_reducible apply ih.libCall
  | with_reducible apply ih.formatField
  ))

macro "gsim_auto" : tactic => `(tactic| repeat' (first | gsim_base | gsim_help | ih_step | item_step | dsimp -zeta only | split))

theorem typeOfQ_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Expr) :
    GSim k (typeOf? c (n + 1) a0 a1) (typeOf? c (n' + 1) a0 a1) := by
  rw [typeOf?.eq_def, typeOf?.eq_def]; dsimp -zeta only; gsim_auto

theorem evalType_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Expr) :
    GSim k (evalType c (n + 1) a0 a1) (evalType c (n' + 1) a0 a1) := by
  rw [evalType.eq_def, evalType.eq_def]; dsimp -zeta only; gsim_auto

theorem underlying_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Ty) :
    GSim k (underlying c (n + 1) a0) (underlying c (n' + 1) a0) := by
  rw [underlying.eq_def, underlying.eq_def]; dsimp -zeta only; gsim_auto

theorem zero_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Ty) :
    GSim k (zero c (n + 1) a0) (zero c (n' + 1) a0) := by
  rw [zero.eq_def, zero.eq_def]; dsimp -zeta only; gsim_auto

theorem convert_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Ty) (a1 : Val) :
    GSim k (convert c (n + 1) a0 a1) (convert c (n' + 1) a0 a1) := by
  rw [convert.eq_def, convert.eq_def]; dsimp -zeta only; gsim_auto

theorem conversion_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Ty) (a1 : Val) :
    GSim k (conversion c (n + 1) a0 a1) (conversion c (n' + 1) a0 a1) := by
  rw [conversion.eq_def, conversion.eq_def]; dsimp -zeta only; gsim_auto

theorem evalExpr_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Expr) :
    GSim k (evalExpr c (n + 1) a0 a1) (evalExpr c (n' + 1) a0 a1) := by
  rw [evalExpr.eq_def, evalExpr.eq_def]; dsimp -zeta only; gsim_auto

theorem evalName_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : String) (a2 : String) :
    GSim k (evalName c (n + 1) a0 a1 a2) (evalName c (n' + 1) a0 a1 a2) := by
  rw [evalName.eq_def, evalName.eq_def]; dsimp -zeta only; gsim_auto

theorem funcVal_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : String) (a1 : String) (a2 : List Ty) :
    GSim k (funcVal c (n + 1) a0 a1 a2) (funcVal c (n' + 1) a0 a1 a2) := by
  rw [funcVal.eq_def, funcVal.eq_def]; dsimp -zeta only; gsim_auto

theorem funcValueQ_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Expr) :
    GSim k (funcValue? c (n + 1) a0 a1) (funcValue? c (n' + 1) a0 a1) := by
  rw [funcValue?.eq_def, funcValue?.eq_def]; dsimp -zeta only; gsim_auto

theorem composite_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Ty) (a2 : List Expr) :
    GSim k (composite c (n + 1) a0 a1 a2) (composite c (n' + 1) a0 a1 a2) := by
  rw [composite.eq_def, composite.eq_def]; dsimp -zeta only; gsim_auto

theorem newArray_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Ty) (a1 : Array Val) (a2 : Nat) :
    GSim k (newArray c (n + 1) a0 a1 a2) (newArray c (n' + 1) a0 a1 a2) := by
  rw [newArray.eq_def, newArray.eq_def]; dsimp -zeta only; gsim_auto

theorem stringBytes_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Ty) (a1 : Ty) (a2 : String) :
    GSim k (stringBytes c (n + 1) a0 a1 a2) (stringBytes c (n' + 1) a0 a1 a2) := by
  rw [stringBytes.eq_def, stringBytes.eq_def]; dsimp -zeta only; gsim_auto

theorem elementKeys_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : List Expr) :
    GSim k (elementKeys c (n + 1) a0 a1) (elementKeys c (n' + 1) a0 a1) := by
  rw [elementKeys.eq_def, elementKeys.eq_def]; dsimp -zeta only; gsim_auto

theorem elements_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Ty) (a2 : List Expr) :
    GSim k (elements c (n + 1) a0 a1 a2) (elements c (n' + 1) a0 a1 a2) := by
  rw [elements.eq_def, elements.eq_def]; dsimp -zeta only; gsim_auto

theorem staticElement_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Ty) (a2 : Expr) :
    GSim k (staticElement c (n + 1) a0 a1 a2) (staticElement c (n' + 1) a0 a1 a2) := by
  rw [staticElement.eq_def, staticElement.eq_def]; dsimp -zeta only; gsim_auto

theorem literalElement_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Ty) (a2 : Expr) :
    GSim k (literalElement c (n + 1) a0 a1 a2) (literalElement c (n' + 1) a0 a1 a2) := by
  rw [literalElement.eq_def, literalElement.eq_def]; dsimp -zeta only; gsim_auto

theorem constantOperand_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Expr) :
    GSim k (constantOperand c (n + 1) a0 a1) (constantOperand c (n' + 1) a0 a1) := by
  rw [constantOperand.eq_def, constantOperand.eq_def]; dsimp -zeta only; gsim_auto

theorem element_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Val) (a1 : Option Loc) (a2 : Val) :
    GSim k (element c (n + 1) a0 a1 a2) (element c (n' + 1) a0 a1 a2) := by
  rw [element.eq_def, element.eq_def]; dsimp -zeta only; gsim_auto

theorem mapKey_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Ty) (a1 : Val) :
    GSim k (mapKey c (n + 1) a0 a1) (mapKey c (n' + 1) a0 a1) := by
  rw [mapKey.eq_def, mapKey.eq_def]; dsimp -zeta only; gsim_auto

theorem mapIndex_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Option Nat) (a1 : MapKey) (a2 : Ty) :
    GSim k (mapIndex c (n + 1) a0 a1 a2) (mapIndex c (n' + 1) a0 a1 a2) := by
  rw [mapIndex.eq_def, mapIndex.eq_def]; dsimp -zeta only; gsim_auto

set_option maxHeartbeats 1000000 in
theorem sliceOf_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Expr) (a2 : Option Expr) (a3 : Option Expr) (a4 : Option Expr) :
    GSim k (sliceOf c (n + 1) a0 a1 a2 a3 a4) (sliceOf c (n' + 1) a0 a1 a2 a3 a4) := by
  rw [sliceOf.eq_def, sliceOf.eq_def]; dsimp -zeta only; gsim_auto

theorem selector_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Expr) (a2 : String) :
    GSim k (selector c (n + 1) a0 a1 a2) (selector c (n' + 1) a0 a1 a2) := by
  rw [selector.eq_def, selector.eq_def]; dsimp -zeta only; gsim_auto

theorem boundMethod_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : String) (a1 : String) (a2 : String) (a3 : List Ty) (a4 : Val) :
    GSim k (boundMethod c (n + 1) a0 a1 a2 a3 a4) (boundMethod c (n' + 1) a0 a1 a2 a3 a4) := by
  rw [boundMethod.eq_def, boundMethod.eq_def]; dsimp -zeta only; gsim_auto

theorem fieldQ_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Val) (a1 : String) :
    GSim k (field? c (n + 1) a0 a1) (field? c (n' + 1) a0 a1) := by
  rw [field?.eq_def, field?.eq_def]; dsimp -zeta only; gsim_auto

theorem operand_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Expr) :
    GSim k (operand c (n + 1) a0 a1) (operand c (n' + 1) a0 a1) := by
  rw [operand.eq_def, operand.eq_def]; dsimp -zeta only; gsim_auto

theorem selectOn_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Val) (a1 : Option Loc) (a2 : String) :
    GSim k (selectOn c (n + 1) a0 a1 a2) (selectOn c (n' + 1) a0 a1 a2) := by
  rw [selectOn.eq_def, selectOn.eq_def]; dsimp -zeta only; gsim_auto

theorem evalAddr_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Expr) :
    GSim k (evalAddr c (n + 1) a0 a1) (evalAddr c (n' + 1) a0 a1) := by
  rw [evalAddr.eq_def, evalAddr.eq_def]; dsimp -zeta only; gsim_auto

theorem evalPlace_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Expr) :
    GSim k (evalPlace c (n + 1) a0 a1) (evalPlace c (n' + 1) a0 a1) := by
  rw [evalPlace.eq_def, evalPlace.eq_def]; dsimp -zeta only; gsim_auto

theorem elemLoc_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Val) (a1 : Option Loc) (a2 : Val) :
    GSim k (elemLoc c (n + 1) a0 a1 a2) (elemLoc c (n' + 1) a0 a1 a2) := by
  rw [elemLoc.eq_def, elemLoc.eq_def]; dsimp -zeta only; gsim_auto

theorem loadPlace_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Place) :
    GSim k (loadPlace c (n + 1) a0) (loadPlace c (n' + 1) a0) := by
  cases a0 <;> (rw [loadPlace.eq_def, loadPlace.eq_def]; dsimp -zeta only; gsim_auto)

theorem storePlace_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Place) (a1 : Val) :
    GSim k (storePlace c (n + 1) a0 a1) (storePlace c (n' + 1) a0 a1) := by
  cases a0 <;> (rw [storePlace.eq_def, storePlace.eq_def]; dsimp -zeta only; gsim_auto)

theorem assertType_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Val) (a1 : Ty) :
    GSim k (assertType c (n + 1) a0 a1) (assertType c (n' + 1) a0 a1) := by
  rw [assertType.eq_def, assertType.eq_def]; dsimp -zeta only; gsim_auto

theorem methodOfType_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Ty) (a1 : String) (a2 : Ty) :
    GSim k (methodOfType c (n + 1) a0 a1 a2) (methodOfType c (n' + 1) a0 a1 a2) := by
  rw [methodOfType.eq_def, methodOfType.eq_def]; dsimp -zeta only; gsim_auto

theorem unary_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : String) (a1 : Val) :
    GSim k (unary c (n + 1) a0 a1) (unary c (n' + 1) a0 a1) := by
  rw [unary.eq_def, unary.eq_def]; dsimp -zeta only; gsim_auto

theorem binary_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : String) (a1 : Val) (a2 : Val) :
    GSim k (binary c (n + 1) a0 a1 a2) (binary c (n' + 1) a0 a1 a2) := by
  rw [binary.eq_def, binary.eq_def]; dsimp -zeta only; gsim_auto

theorem equal_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Val) (a1 : Val) :
    GSim k (equal c (n + 1) a0 a1) (equal c (n' + 1) a0 a1) := by
  rw [equal.eq_def, equal.eq_def]; dsimp -zeta only; gsim_auto

theorem equalAll_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : List Val) (a1 : List Val) :
    GSim k (equalAll c (n + 1) a0 a1) (equalAll c (n' + 1) a0 a1) := by
  rw [equalAll.eq_def, equalAll.eq_def]; dsimp -zeta only; gsim_auto

theorem evalCall_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Expr) (a2 : List Expr) (a3 : Bool) :
    GSim k (evalCall c (n + 1) a0 a1 a2 a3) (evalCall c (n' + 1) a0 a1 a2 a3) := by
  rw [evalCall.eq_def, evalCall.eq_def]; dsimp -zeta only; gsim_auto

theorem appendValues_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Ty) (a1 : Ty) (a2 : Val) (a3 : List Val) (a4 : Bool) :
    GSim k (appendValues c (n + 1) a0 a1 a2 a3 a4) (appendValues c (n' + 1) a0 a1 a2 a3 a4) := by
  rw [appendValues.eq_def, appendValues.eq_def]; dsimp -zeta only; gsim_auto

theorem layout_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Ty) :
    GSim k (layout c (n + 1) a0) (layout c (n' + 1) a0) := by
  rw [layout.eq_def, layout.eq_def]; dsimp -zeta only; gsim_auto

theorem genericArgs_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Val) (a2 : List Expr) (a3 : Bool) :
    GSim k (genericArgs c (n + 1) a0 a1 a2 a3) (genericArgs c (n' + 1) a0 a1 a2 a3) := by
  rw [genericArgs.eq_def, genericArgs.eq_def]; dsimp -zeta only; gsim_auto

theorem declaredFuncQ_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Expr) :
    GSim k (declaredFunc? c (n + 1) a0 a1) (declaredFunc? c (n' + 1) a0 a1) := by
  rw [declaredFunc?.eq_def, declaredFunc?.eq_def]; dsimp -zeta only; gsim_auto

theorem deferCall_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Expr) (a2 : List Expr) (a3 : Bool) :
    GSim k (deferCall c (n + 1) a0 a1 a2 a3) (deferCall c (n' + 1) a0 a1 a2 a3) := by
  rw [deferCall.eq_def, deferCall.eq_def]; dsimp -zeta only; gsim_auto

theorem evalArgs_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : List Expr) :
    GSim k (evalArgs c (n + 1) a0 a1) (evalArgs c (n' + 1) a0 a1) := by
  rw [evalArgs.eq_def, evalArgs.eq_def]; dsimp -zeta only; gsim_auto

theorem builtin_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : String) (a2 : List Expr) (a3 : Bool) :
    GSim k (builtin c (n + 1) a0 a1 a2 a3) (builtin c (n' + 1) a0 a1 a2 a3) := by
  rw [builtin.eq_def, builtin.eq_def]; dsimp -zeta only; gsim_auto

theorem panicText_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Val) :
    GSim k (panicText c (n + 1) a0) (panicText c (n' + 1) a0) := by
  rw [panicText.eq_def, panicText.eq_def]; dsimp -zeta only; gsim_auto

theorem callMethodQ_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Val) (a1 : String) :
    GSim k (callMethod? c (n + 1) a0 a1) (callMethod? c (n' + 1) a0 a1) := by
  rw [callMethod?.eq_def, callMethod?.eq_def]; dsimp -zeta only; gsim_auto

theorem callValue_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Val) (a1 : List Val) (a2 : Bool) :
    GSim k (callValue c (n + 1) a0 a1 a2) (callValue c (n' + 1) a0 a1 a2) := by
  rw [callValue.eq_def, callValue.eq_def]; dsimp -zeta only; gsim_auto

theorem callMethod_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : String) (a1 : List Ty) (a2 : Method) (a3 : Val) (a4 : List Val) (a5 : Bool) :
    GSim k (callMethod c (n + 1) a0 a1 a2 a3 a4 a5) (callMethod c (n' + 1) a0 a1 a2 a3 a4 a5) := by
  rw [callMethod.eq_def, callMethod.eq_def]; dsimp -zeta only; gsim_auto

theorem callBody_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Sig) (a2 : Option (List Stmt)) (a3 : Option (String × Val)) (a4 : List Val) (a5 : Bool) :
    GSim k (callBody c (n + 1) a0 a1 a2 a3 a4 a5) (callBody c (n' + 1) a0 a1 a2 a3 a4 a5) := by
  cases a1 <;> (rw [callBody.eq_def, callBody.eq_def]; dsimp -zeta only; gsim_auto)

theorem execStmts_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : List Stmt) :
    GSim k (execStmts c (n + 1) a0 a1) (execStmts c (n' + 1) a0 a1) := by
  rw [execStmts.eq_def, execStmts.eq_def]; dsimp -zeta only; gsim_auto

theorem execBlock_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : List Stmt) :
    GSim k (execBlock c (n + 1) a0 a1) (execBlock c (n' + 1) a0 a1) := by
  rw [execBlock.eq_def, execBlock.eq_def]; dsimp -zeta only; gsim_auto

theorem execStmt_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Stmt) :
    GSim k (execStmt c (n + 1) a0 a1) (execStmt c (n' + 1) a0 a1) := by
  rw [execStmt.eq_def, execStmt.eq_def]; dsimp -zeta only; gsim_auto

theorem declValue_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Option Ty) (a1 : Option Val) :
    GSim k (declValue c (n + 1) a0 a1) (declValue c (n' + 1) a0 a1) := by
  rw [declValue.eq_def, declValue.eq_def]; dsimp -zeta only; gsim_auto

theorem execInit_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Option Stmt) :
    GSim k (execInit c (n + 1) a0 a1) (execInit c (n' + 1) a0 a1) := by
  rw [execInit.eq_def, execInit.eq_def]; dsimp -zeta only; gsim_auto

theorem switchClauses_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Val) (a2 : List Clause) :
    GSim k (switchClauses c (n + 1) a0 a1 a2) (switchClauses c (n' + 1) a0 a1 a2) := by
  rw [switchClauses.eq_def, switchClauses.eq_def]; dsimp -zeta only; gsim_auto

theorem typeClauses_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Option String) (a2 : Val) (a3 : List Clause) :
    GSim k (typeClauses c (n + 1) a0 a1 a2 a3) (typeClauses c (n' + 1) a0 a1 a2 a3) := by
  rw [typeClauses.eq_def, typeClauses.eq_def]; dsimp -zeta only; gsim_auto

theorem forLoop_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Option Expr) (a2 : Option Stmt) (a3 : List Stmt) :
    GSim k (forLoop c (n + 1) a0 a1 a2 a3) (forLoop c (n' + 1) a0 a1 a2 a3) := by
  rw [forLoop.eq_def, forLoop.eq_def]; dsimp -zeta only; gsim_auto

theorem rangeLoop_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Option Expr) (a2 : Option Expr) (a3 : String) (a4 : Expr) (a5 : List Stmt) :
    GSim k (rangeLoop c (n + 1) a0 a1 a2 a3 a4 a5) (rangeLoop c (n' + 1) a0 a1 a2 a3 a4 a5) := by
  rw [rangeLoop.eq_def, rangeLoop.eq_def]; dsimp -zeta only; gsim_auto

theorem assign_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : String) (a2 : List Expr) (a3 : List Expr) :
    GSim k (assign c (n + 1) a0 a1 a2 a3) (assign c (n' + 1) a0 a1 a2 a3) := by
  rw [assign.eq_def, assign.eq_def]; dsimp -zeta only; gsim_auto

theorem rhsValues_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Env) (a1 : Nat) (a2 : List Expr) :
    GSim k (rhsValues c (n + 1) a0 a1 a2) (rhsValues c (n' + 1) a0 a1 a2) := by
  rw [rhsValues.eq_def, rhsValues.eq_def]; dsimp -zeta only; gsim_auto

theorem errorsNew_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : String) :
    GSim k (errorsNew c (n + 1) a0) (errorsNew c (n' + 1) a0) := by
  rw [errorsNew.eq_def, errorsNew.eq_def]; dsimp -zeta only; gsim_auto

theorem fmtCall_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : String) (a1 : List Val) :
    GSim k (fmtCall c (n + 1) a0 a1) (fmtCall c (n' + 1) a0 a1) := by
  rw [fmtCall.eq_def, fmtCall.eq_def]; dsimp -zeta only; gsim_auto

theorem sprint_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : List Val) :
    GSim k (sprint c (n + 1) a0) (sprint c (n' + 1) a0) := by
  rw [sprint.eq_def, sprint.eq_def]; dsimp -zeta only; gsim_auto

theorem sprintf_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : List Char) (a1 : List Val) :
    GSim k (sprintf c (n + 1) a0 a1) (sprintf c (n' + 1) a0 a1) := by
  rw [sprintf.eq_def, sprintf.eq_def]; dsimp -zeta only; gsim_auto

theorem fmtSpec_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Val) (a1 : Char) (a2 : FmtSpec) :
    GSim k (fmtSpec c (n + 1) a0 a1 a2) (fmtSpec c (n' + 1) a0 a1 a2) := by
  rw [fmtSpec.eq_def, fmtSpec.eq_def]; dsimp -zeta only; gsim_auto

theorem fmtArg_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Val) (a1 : Char) :
    GSim k (fmtArg c (n + 1) a0 a1) (fmtArg c (n' + 1) a0 a1) := by
  rw [fmtArg.eq_def, fmtArg.eq_def]; dsimp -zeta only; gsim_auto

theorem fmtValue_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Val) (a1 : Char) (a2 : Nat) (a3 : Bool) :
    GSim k (fmtValue c (n + 1) a0 a1 a2 a3) (fmtValue c (n' + 1) a0 a1 a2 a3) := by
  rw [fmtValue.eq_def, fmtValue.eq_def]; dsimp -zeta only; gsim_auto

theorem libCall_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : String) (a1 : List Val) :
    GSim k (libCall c (n + 1) a0 a1) (libCall c (n' + 1) a0 a1) := by
  rw [libCall.eq_def, libCall.eq_def]; dsimp -zeta only; gsim_auto

theorem formatField_step {c : Ctx} {k n n' : Nat} (ih : SimAll c k n n') (a0 : Val) :
    GSim k (formatField c (n + 1) a0) (formatField c (n' + 1) a0) := by
  rw [formatField.eq_def, formatField.eq_def]; dsimp -zeta only; gsim_auto

theorem simAll (c : Ctx) (k : Nat) : ∀ n n', n ≤ n' → SimAll c k n n'
  | 0, _, _ => {
    typeOf? := fun _ _ => sim_of_fuel fun _ => by rw [typeOf?.eq_def]; exact id,
    evalType := fun _ _ => sim_of_fuel fun _ => by rw [evalType.eq_def]; exact id,
    underlying := fun _ => sim_of_fuel fun _ => by rw [underlying.eq_def]; exact id,
    zero := fun _ => sim_of_fuel fun _ => by rw [zero.eq_def]; exact id,
    convert := fun _ _ => sim_of_fuel fun _ => by rw [convert.eq_def]; exact id,
    conversion := fun _ _ => sim_of_fuel fun _ => by rw [conversion.eq_def]; exact id,
    evalExpr := fun _ _ => sim_of_fuel fun _ => by rw [evalExpr.eq_def]; exact id,
    evalName := fun _ _ _ => sim_of_fuel fun _ => by rw [evalName.eq_def]; exact id,
    funcVal := fun _ _ _ => sim_of_fuel fun _ => by rw [funcVal.eq_def]; exact id,
    funcValue? := fun _ _ => sim_of_fuel fun _ => by rw [funcValue?.eq_def]; exact id,
    composite := fun _ _ _ => sim_of_fuel fun _ => by rw [composite.eq_def]; exact id,
    newArray := fun _ _ _ => sim_of_fuel fun _ => by rw [newArray.eq_def]; exact id,
    stringBytes := fun _ _ _ => sim_of_fuel fun _ => by rw [stringBytes.eq_def]; exact id,
    elementKeys := fun _ _ => sim_of_fuel fun _ => by rw [elementKeys.eq_def]; exact id,
    elements := fun _ _ _ => sim_of_fuel fun _ => by rw [elements.eq_def]; exact id,
    staticElement := fun _ _ _ => sim_of_fuel fun _ => by rw [staticElement.eq_def]; exact id,
    literalElement := fun _ _ _ => sim_of_fuel fun _ => by rw [literalElement.eq_def]; exact id,
    constantOperand := fun _ _ => sim_of_fuel fun _ => by rw [constantOperand.eq_def]; exact id,
    element := fun _ _ _ => sim_of_fuel fun _ => by rw [element.eq_def]; exact id,
    mapKey := fun _ _ => sim_of_fuel fun _ => by rw [mapKey.eq_def]; exact id,
    mapIndex := fun _ _ _ => sim_of_fuel fun _ => by rw [mapIndex.eq_def]; exact id,
    sliceOf := fun _ _ _ _ _ => sim_of_fuel fun _ => by rw [sliceOf.eq_def]; exact id,
    selector := fun _ _ _ => sim_of_fuel fun _ => by rw [selector.eq_def]; exact id,
    boundMethod := fun _ _ _ _ _ => sim_of_fuel fun _ => by rw [boundMethod.eq_def]; exact id,
    field? := fun _ _ => sim_of_fuel fun _ => by rw [field?.eq_def]; exact id,
    operand := fun _ _ => sim_of_fuel fun _ => by rw [operand.eq_def]; exact id,
    selectOn := fun _ _ _ => sim_of_fuel fun _ => by rw [selectOn.eq_def]; exact id,
    evalAddr := fun _ _ => sim_of_fuel fun _ => by rw [evalAddr.eq_def]; exact id,
    evalPlace := fun _ _ => sim_of_fuel fun _ => by rw [evalPlace.eq_def]; exact id,
    elemLoc := fun _ _ _ => sim_of_fuel fun _ => by rw [elemLoc.eq_def]; exact id,
    loadPlace := fun _ => sim_of_fuel fun _ => by rw [loadPlace.eq_def]; exact id,
    storePlace := fun _ _ => sim_of_fuel fun _ => by rw [storePlace.eq_def]; exact id,
    assertType := fun _ _ => sim_of_fuel fun _ => by rw [assertType.eq_def]; exact id,
    methodOfType := fun _ _ _ => sim_of_fuel fun _ => by rw [methodOfType.eq_def]; exact id,
    unary := fun _ _ => sim_of_fuel fun _ => by rw [unary.eq_def]; exact id,
    binary := fun _ _ _ => sim_of_fuel fun _ => by rw [binary.eq_def]; exact id,
    equal := fun _ _ => sim_of_fuel fun _ => by rw [equal.eq_def]; exact id,
    equalAll := fun _ _ => sim_of_fuel fun _ => by rw [equalAll.eq_def]; exact id,
    evalCall := fun _ _ _ _ => sim_of_fuel fun _ => by rw [evalCall.eq_def]; exact id,
    appendValues := fun _ _ _ _ _ => sim_of_fuel fun _ => by rw [appendValues.eq_def]; exact id,
    layout := fun _ => sim_of_fuel fun _ => by rw [layout.eq_def]; exact id,
    genericArgs := fun _ _ _ _ => sim_of_fuel fun _ => by rw [genericArgs.eq_def]; exact id,
    declaredFunc? := fun _ _ => sim_of_fuel fun _ => by rw [declaredFunc?.eq_def]; exact id,
    deferCall := fun _ _ _ _ => sim_of_fuel fun _ => by rw [deferCall.eq_def]; exact id,
    evalArgs := fun _ _ => sim_of_fuel fun _ => by rw [evalArgs.eq_def]; exact id,
    builtin := fun _ _ _ _ => sim_of_fuel fun _ => by rw [builtin.eq_def]; exact id,
    panicText := fun _ => sim_of_fuel fun _ => by rw [panicText.eq_def]; exact id,
    callMethod? := fun _ _ => sim_of_fuel fun _ => by rw [callMethod?.eq_def]; exact id,
    callValue := fun _ _ _ => sim_of_fuel fun _ => by rw [callValue.eq_def]; exact id,
    callMethod := fun _ _ _ _ _ _ => sim_of_fuel fun _ => by rw [callMethod.eq_def]; exact id,
    callBody := fun _ _ _ _ _ _ => sim_of_fuel fun _ => by rw [callBody.eq_def]; exact id,
    execStmts := fun _ _ => sim_of_fuel fun _ => by rw [execStmts.eq_def]; exact id,
    execBlock := fun _ _ => sim_of_fuel fun _ => by rw [execBlock.eq_def]; exact id,
    execStmt := fun _ _ => sim_of_fuel fun _ => by rw [execStmt.eq_def]; exact id,
    declValue := fun _ _ => sim_of_fuel fun _ => by rw [declValue.eq_def]; exact id,
    execInit := fun _ _ => sim_of_fuel fun _ => by rw [execInit.eq_def]; exact id,
    switchClauses := fun _ _ _ => sim_of_fuel fun _ => by rw [switchClauses.eq_def]; exact id,
    typeClauses := fun _ _ _ _ => sim_of_fuel fun _ => by rw [typeClauses.eq_def]; exact id,
    forLoop := fun _ _ _ _ => sim_of_fuel fun _ => by rw [forLoop.eq_def]; exact id,
    rangeLoop := fun _ _ _ _ _ _ => sim_of_fuel fun _ => by rw [rangeLoop.eq_def]; exact id,
    assign := fun _ _ _ _ => sim_of_fuel fun _ => by rw [assign.eq_def]; exact id,
    rhsValues := fun _ _ _ => sim_of_fuel fun _ => by rw [rhsValues.eq_def]; exact id,
    errorsNew := fun _ => sim_of_fuel fun _ => by rw [errorsNew.eq_def]; exact id,
    fmtCall := fun _ _ => sim_of_fuel fun _ => by rw [fmtCall.eq_def]; exact id,
    sprint := fun _ => sim_of_fuel fun _ => by rw [sprint.eq_def]; exact id,
    sprintf := fun _ _ => sim_of_fuel fun _ => by rw [sprintf.eq_def]; exact id,
    fmtSpec := fun _ _ _ => sim_of_fuel fun _ => by rw [fmtSpec.eq_def]; exact id,
    fmtArg := fun _ _ => sim_of_fuel fun _ => by rw [fmtArg.eq_def]; exact id,
    fmtValue := fun _ _ _ _ => sim_of_fuel fun _ => by rw [fmtValue.eq_def]; exact id,
    libCall := fun _ _ => sim_of_fuel fun _ => by rw [libCall.eq_def]; exact id,
    formatField := fun _ => sim_of_fuel fun _ => by rw [formatField.eq_def]; exact id
  }
  | _ + 1, 0, h => absurd h (by omega)
  | n + 1, n' + 1, h =>
    have ih := simAll c k n n' (by omega)
    {
    typeOf? := typeOfQ_step ih,
    evalType := evalType_step ih,
    underlying := underlying_step ih,
    zero := zero_step ih,
    convert := convert_step ih,
    conversion := conversion_step ih,
    evalExpr := evalExpr_step ih,
    evalName := evalName_step ih,
    funcVal := funcVal_step ih,
    funcValue? := funcValueQ_step ih,
    composite := composite_step ih,
    newArray := newArray_step ih,
    stringBytes := stringBytes_step ih,
    elementKeys := elementKeys_step ih,
    elements := elements_step ih,
    staticElement := staticElement_step ih,
    literalElement := literalElement_step ih,
    constantOperand := constantOperand_step ih,
    element := element_step ih,
    mapKey := mapKey_step ih,
    mapIndex := mapIndex_step ih,
    sliceOf := sliceOf_step ih,
    selector := selector_step ih,
    boundMethod := boundMethod_step ih,
    field? := fieldQ_step ih,
    operand := operand_step ih,
    selectOn := selectOn_step ih,
    evalAddr := evalAddr_step ih,
    evalPlace := evalPlace_step ih,
    elemLoc := elemLoc_step ih,
    loadPlace := loadPlace_step ih,
    storePlace := storePlace_step ih,
    assertType := assertType_step ih,
    methodOfType := methodOfType_step ih,
    unary := unary_step ih,
    binary := binary_step ih,
    equal := equal_step ih,
    equalAll := equalAll_step ih,
    evalCall := evalCall_step ih,
    appendValues := appendValues_step ih,
    layout := layout_step ih,
    genericArgs := genericArgs_step ih,
    declaredFunc? := declaredFuncQ_step ih,
    deferCall := deferCall_step ih,
    evalArgs := evalArgs_step ih,
    builtin := builtin_step ih,
    panicText := panicText_step ih,
    callMethod? := callMethodQ_step ih,
    callValue := callValue_step ih,
    callMethod := callMethod_step ih,
    callBody := callBody_step ih,
    execStmts := execStmts_step ih,
    execBlock := execBlock_step ih,
    execStmt := execStmt_step ih,
    declValue := declValue_step ih,
    execInit := execInit_step ih,
    switchClauses := switchClauses_step ih,
    typeClauses := typeClauses_step ih,
    forLoop := forLoop_step ih,
    rangeLoop := rangeLoop_step ih,
    assign := assign_step ih,
    rhsValues := rhsValues_step ih,
    errorsNew := errorsNew_step ih,
    fmtCall := fmtCall_step ih,
    sprint := sprint_step ih,
    sprintf := sprintf_step ih,
    fmtSpec := fmtSpec_step ih,
    fmtArg := fmtArg_step ih,
    fmtValue := fmtValue_step ih,
    libCall := libCall_step ih,
    formatField := formatField_step ih
  }

/-- Every function of the evaluator's mutual block keeps `Sync` at depth `n`. -/
structure SyncAll (c : Ctx) (n : Nat) : Prop where
  typeOf? : ∀ (a0 : Env) (a1 : Expr), Pres Sync (typeOf? c n a0 a1)
  evalType : ∀ (a0 : Env) (a1 : Expr), Pres Sync (evalType c n a0 a1)
  underlying : ∀ (a0 : Ty), Pres Sync (underlying c n a0)
  zero : ∀ (a0 : Ty), Pres Sync (zero c n a0)
  convert : ∀ (a0 : Ty) (a1 : Val), Pres Sync (convert c n a0 a1)
  conversion : ∀ (a0 : Ty) (a1 : Val), Pres Sync (conversion c n a0 a1)
  evalExpr : ∀ (a0 : Env) (a1 : Expr), Pres Sync (evalExpr c n a0 a1)
  evalName : ∀ (a0 : Env) (a1 : String) (a2 : String), Pres Sync (evalName c n a0 a1 a2)
  funcVal : ∀ (a0 : String) (a1 : String) (a2 : List Ty), Pres Sync (funcVal c n a0 a1 a2)
  funcValue? : ∀ (a0 : Env) (a1 : Expr), Pres Sync (funcValue? c n a0 a1)
  composite : ∀ (a0 : Env) (a1 : Ty) (a2 : List Expr), Pres Sync (composite c n a0 a1 a2)
  newArray : ∀ (a0 : Ty) (a1 : Array Val) (a2 : Nat), Pres Sync (newArray c n a0 a1 a2)
  stringBytes : ∀ (a0 : Ty) (a1 : Ty) (a2 : String), Pres Sync (stringBytes c n a0 a1 a2)
  elementKeys : ∀ (a0 : Env) (a1 : List Expr), Pres Sync (elementKeys c n a0 a1)
  elements : ∀ (a0 : Env) (a1 : Ty) (a2 : List Expr), Pres Sync (elements c n a0 a1 a2)
  staticElement : ∀ (a0 : Env) (a1 : Ty) (a2 : Expr), Pres Sync (staticElement c n a0 a1 a2)
  literalElement : ∀ (a0 : Env) (a1 : Ty) (a2 : Expr), Pres Sync (literalElement c n a0 a1 a2)
  constantOperand : ∀ (a0 : Env) (a1 : Expr), Pres Sync (constantOperand c n a0 a1)
  element : ∀ (a0 : Val) (a1 : Option Loc) (a2 : Val), Pres Sync (element c n a0 a1 a2)
  mapKey : ∀ (a0 : Ty) (a1 : Val), Pres Sync (mapKey c n a0 a1)
  mapIndex : ∀ (a0 : Option Nat) (a1 : MapKey) (a2 : Ty), Pres Sync (mapIndex c n a0 a1 a2)
  sliceOf : ∀ (a0 : Env) (a1 : Expr) (a2 : Option Expr) (a3 : Option Expr) (a4 : Option Expr), Pres Sync (sliceOf c n a0 a1 a2 a3 a4)
  selector : ∀ (a0 : Env) (a1 : Expr) (a2 : String), Pres Sync (selector c n a0 a1 a2)
  boundMethod : ∀ (a0 : String) (a1 : String) (a2 : String) (a3 : List Ty) (a4 : Val), Pres Sync (boundMethod c n a0 a1 a2 a3 a4)
  field? : ∀ (a0 : Val) (a1 : String), Pres Sync (field? c n a0 a1)
  operand : ∀ (a0 : Env) (a1 : Expr), Pres Sync (operand c n a0 a1)
  selectOn : ∀ (a0 : Val) (a1 : Option Loc) (a2 : String), Pres Sync (selectOn c n a0 a1 a2)
  evalAddr : ∀ (a0 : Env) (a1 : Expr), Pres Sync (evalAddr c n a0 a1)
  evalPlace : ∀ (a0 : Env) (a1 : Expr), Pres Sync (evalPlace c n a0 a1)
  elemLoc : ∀ (a0 : Val) (a1 : Option Loc) (a2 : Val), Pres Sync (elemLoc c n a0 a1 a2)
  loadPlace : ∀ (a0 : Place), Pres Sync (loadPlace c n a0)
  storePlace : ∀ (a0 : Place) (a1 : Val), Pres Sync (storePlace c n a0 a1)
  assertType : ∀ (a0 : Val) (a1 : Ty), Pres Sync (assertType c n a0 a1)
  methodOfType : ∀ (a0 : Ty) (a1 : String) (a2 : Ty), Pres Sync (methodOfType c n a0 a1 a2)
  unary : ∀ (a0 : String) (a1 : Val), Pres Sync (unary c n a0 a1)
  binary : ∀ (a0 : String) (a1 : Val) (a2 : Val), Pres Sync (binary c n a0 a1 a2)
  equal : ∀ (a0 : Val) (a1 : Val), Pres Sync (equal c n a0 a1)
  equalAll : ∀ (a0 : List Val) (a1 : List Val), Pres Sync (equalAll c n a0 a1)
  evalCall : ∀ (a0 : Env) (a1 : Expr) (a2 : List Expr) (a3 : Bool), Pres Sync (evalCall c n a0 a1 a2 a3)
  appendValues : ∀ (a0 : Ty) (a1 : Ty) (a2 : Val) (a3 : List Val) (a4 : Bool), Pres Sync (appendValues c n a0 a1 a2 a3 a4)
  layout : ∀ (a0 : Ty), Pres Sync (layout c n a0)
  genericArgs : ∀ (a0 : Env) (a1 : Val) (a2 : List Expr) (a3 : Bool), Pres Sync (genericArgs c n a0 a1 a2 a3)
  declaredFunc? : ∀ (a0 : Env) (a1 : Expr), Pres Sync (declaredFunc? c n a0 a1)
  deferCall : ∀ (a0 : Env) (a1 : Expr) (a2 : List Expr) (a3 : Bool), Pres Sync (deferCall c n a0 a1 a2 a3)
  evalArgs : ∀ (a0 : Env) (a1 : List Expr), Pres Sync (evalArgs c n a0 a1)
  builtin : ∀ (a0 : Env) (a1 : String) (a2 : List Expr) (a3 : Bool), Pres Sync (builtin c n a0 a1 a2 a3)
  panicText : ∀ (a0 : Val), Pres Sync (panicText c n a0)
  callMethod? : ∀ (a0 : Val) (a1 : String), Pres Sync (callMethod? c n a0 a1)
  callValue : ∀ (a0 : Val) (a1 : List Val) (a2 : Bool), Pres Sync (callValue c n a0 a1 a2)
  callMethod : ∀ (a0 : String) (a1 : List Ty) (a2 : Method) (a3 : Val) (a4 : List Val) (a5 : Bool), Pres Sync (callMethod c n a0 a1 a2 a3 a4 a5)
  callBody : ∀ (a0 : Env) (a1 : Sig) (a2 : Option (List Stmt)) (a3 : Option (String × Val)) (a4 : List Val) (a5 : Bool), Pres Sync (callBody c n a0 a1 a2 a3 a4 a5)
  execStmts : ∀ (a0 : Env) (a1 : List Stmt), Pres Sync (execStmts c n a0 a1)
  execBlock : ∀ (a0 : Env) (a1 : List Stmt), Pres Sync (execBlock c n a0 a1)
  execStmt : ∀ (a0 : Env) (a1 : Stmt), Pres Sync (execStmt c n a0 a1)
  declValue : ∀ (a0 : Option Ty) (a1 : Option Val), Pres Sync (declValue c n a0 a1)
  execInit : ∀ (a0 : Env) (a1 : Option Stmt), Pres Sync (execInit c n a0 a1)
  switchClauses : ∀ (a0 : Env) (a1 : Val) (a2 : List Clause), Pres Sync (switchClauses c n a0 a1 a2)
  typeClauses : ∀ (a0 : Env) (a1 : Option String) (a2 : Val) (a3 : List Clause), Pres Sync (typeClauses c n a0 a1 a2 a3)
  forLoop : ∀ (a0 : Env) (a1 : Option Expr) (a2 : Option Stmt) (a3 : List Stmt), Pres Sync (forLoop c n a0 a1 a2 a3)
  rangeLoop : ∀ (a0 : Env) (a1 : Option Expr) (a2 : Option Expr) (a3 : String) (a4 : Expr) (a5 : List Stmt), Pres Sync (rangeLoop c n a0 a1 a2 a3 a4 a5)
  assign : ∀ (a0 : Env) (a1 : String) (a2 : List Expr) (a3 : List Expr), Pres Sync (assign c n a0 a1 a2 a3)
  rhsValues : ∀ (a0 : Env) (a1 : Nat) (a2 : List Expr), Pres Sync (rhsValues c n a0 a1 a2)
  errorsNew : ∀ (a0 : String), Pres Sync (errorsNew c n a0)
  fmtCall : ∀ (a0 : String) (a1 : List Val), Pres Sync (fmtCall c n a0 a1)
  sprint : ∀ (a0 : List Val), Pres Sync (sprint c n a0)
  sprintf : ∀ (a0 : List Char) (a1 : List Val), Pres Sync (sprintf c n a0 a1)
  fmtSpec : ∀ (a0 : Val) (a1 : Char) (a2 : FmtSpec), Pres Sync (fmtSpec c n a0 a1 a2)
  fmtArg : ∀ (a0 : Val) (a1 : Char), Pres Sync (fmtArg c n a0 a1)
  fmtValue : ∀ (a0 : Val) (a1 : Char) (a2 : Nat) (a3 : Bool), Pres Sync (fmtValue c n a0 a1 a2 a3)
  libCall : ∀ (a0 : String) (a1 : List Val), Pres Sync (libCall c n a0 a1)
  formatField : ∀ (a0 : Val), Pres Sync (formatField c n a0)

macro "pres_auto" : tactic => `(tactic| repeat' (first | pres_base | pres_help | ih_step | item_step | dsimp -zeta only | split))

theorem typeOfQ_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Expr) :
    Pres Sync (typeOf? c (n + 1) a0 a1) := by
  rw [typeOf?.eq_def]; dsimp -zeta only; pres_auto

theorem evalType_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Expr) :
    Pres Sync (evalType c (n + 1) a0 a1) := by
  rw [evalType.eq_def]; dsimp -zeta only; pres_auto

theorem underlying_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Ty) :
    Pres Sync (underlying c (n + 1) a0) := by
  rw [underlying.eq_def]; dsimp -zeta only; pres_auto

theorem zero_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Ty) :
    Pres Sync (zero c (n + 1) a0) := by
  rw [zero.eq_def]; dsimp -zeta only; pres_auto

theorem convert_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Ty) (a1 : Val) :
    Pres Sync (convert c (n + 1) a0 a1) := by
  rw [convert.eq_def]; dsimp -zeta only; pres_auto

theorem conversion_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Ty) (a1 : Val) :
    Pres Sync (conversion c (n + 1) a0 a1) := by
  rw [conversion.eq_def]; dsimp -zeta only; pres_auto

theorem evalExpr_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Expr) :
    Pres Sync (evalExpr c (n + 1) a0 a1) := by
  rw [evalExpr.eq_def]; dsimp -zeta only; pres_auto

theorem evalName_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : String) (a2 : String) :
    Pres Sync (evalName c (n + 1) a0 a1 a2) := by
  rw [evalName.eq_def]; dsimp -zeta only; pres_auto

theorem funcVal_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : String) (a1 : String) (a2 : List Ty) :
    Pres Sync (funcVal c (n + 1) a0 a1 a2) := by
  rw [funcVal.eq_def]; dsimp -zeta only; pres_auto

theorem funcValueQ_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Expr) :
    Pres Sync (funcValue? c (n + 1) a0 a1) := by
  rw [funcValue?.eq_def]; dsimp -zeta only; pres_auto

theorem composite_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Ty) (a2 : List Expr) :
    Pres Sync (composite c (n + 1) a0 a1 a2) := by
  rw [composite.eq_def]; dsimp -zeta only; pres_auto

theorem newArray_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Ty) (a1 : Array Val) (a2 : Nat) :
    Pres Sync (newArray c (n + 1) a0 a1 a2) := by
  rw [newArray.eq_def]; dsimp -zeta only; pres_auto

theorem stringBytes_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Ty) (a1 : Ty) (a2 : String) :
    Pres Sync (stringBytes c (n + 1) a0 a1 a2) := by
  rw [stringBytes.eq_def]; dsimp -zeta only; pres_auto

theorem elementKeys_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : List Expr) :
    Pres Sync (elementKeys c (n + 1) a0 a1) := by
  rw [elementKeys.eq_def]; dsimp -zeta only; pres_auto

theorem elements_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Ty) (a2 : List Expr) :
    Pres Sync (elements c (n + 1) a0 a1 a2) := by
  rw [elements.eq_def]; dsimp -zeta only; pres_auto

theorem staticElement_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Ty) (a2 : Expr) :
    Pres Sync (staticElement c (n + 1) a0 a1 a2) := by
  rw [staticElement.eq_def]; dsimp -zeta only; pres_auto

theorem literalElement_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Ty) (a2 : Expr) :
    Pres Sync (literalElement c (n + 1) a0 a1 a2) := by
  rw [literalElement.eq_def]; dsimp -zeta only; pres_auto

theorem constantOperand_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Expr) :
    Pres Sync (constantOperand c (n + 1) a0 a1) := by
  rw [constantOperand.eq_def]; dsimp -zeta only; pres_auto

theorem element_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Val) (a1 : Option Loc) (a2 : Val) :
    Pres Sync (element c (n + 1) a0 a1 a2) := by
  rw [element.eq_def]; dsimp -zeta only; pres_auto

theorem mapKey_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Ty) (a1 : Val) :
    Pres Sync (mapKey c (n + 1) a0 a1) := by
  rw [mapKey.eq_def]; dsimp -zeta only; pres_auto

theorem mapIndex_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Option Nat) (a1 : MapKey) (a2 : Ty) :
    Pres Sync (mapIndex c (n + 1) a0 a1 a2) := by
  rw [mapIndex.eq_def]; dsimp -zeta only; pres_auto

set_option maxHeartbeats 1000000 in
theorem sliceOf_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Expr) (a2 : Option Expr) (a3 : Option Expr) (a4 : Option Expr) :
    Pres Sync (sliceOf c (n + 1) a0 a1 a2 a3 a4) := by
  rw [sliceOf.eq_def]; dsimp -zeta only; pres_auto

theorem selector_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Expr) (a2 : String) :
    Pres Sync (selector c (n + 1) a0 a1 a2) := by
  rw [selector.eq_def]; dsimp -zeta only; pres_auto

theorem boundMethod_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : String) (a1 : String) (a2 : String) (a3 : List Ty) (a4 : Val) :
    Pres Sync (boundMethod c (n + 1) a0 a1 a2 a3 a4) := by
  rw [boundMethod.eq_def]; dsimp -zeta only; pres_auto

theorem fieldQ_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Val) (a1 : String) :
    Pres Sync (field? c (n + 1) a0 a1) := by
  rw [field?.eq_def]; dsimp -zeta only; pres_auto

theorem operand_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Expr) :
    Pres Sync (operand c (n + 1) a0 a1) := by
  rw [operand.eq_def]; dsimp -zeta only; pres_auto

theorem selectOn_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Val) (a1 : Option Loc) (a2 : String) :
    Pres Sync (selectOn c (n + 1) a0 a1 a2) := by
  rw [selectOn.eq_def]; dsimp -zeta only; pres_auto

theorem evalAddr_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Expr) :
    Pres Sync (evalAddr c (n + 1) a0 a1) := by
  rw [evalAddr.eq_def]; dsimp -zeta only; pres_auto

theorem evalPlace_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Expr) :
    Pres Sync (evalPlace c (n + 1) a0 a1) := by
  rw [evalPlace.eq_def]; dsimp -zeta only; pres_auto

theorem elemLoc_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Val) (a1 : Option Loc) (a2 : Val) :
    Pres Sync (elemLoc c (n + 1) a0 a1 a2) := by
  rw [elemLoc.eq_def]; dsimp -zeta only; pres_auto

theorem loadPlace_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Place) :
    Pres Sync (loadPlace c (n + 1) a0) := by
  cases a0 <;> (rw [loadPlace.eq_def]; dsimp -zeta only; pres_auto)

theorem storePlace_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Place) (a1 : Val) :
    Pres Sync (storePlace c (n + 1) a0 a1) := by
  cases a0 <;> (rw [storePlace.eq_def]; dsimp -zeta only; pres_auto)

theorem assertType_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Val) (a1 : Ty) :
    Pres Sync (assertType c (n + 1) a0 a1) := by
  rw [assertType.eq_def]; dsimp -zeta only; pres_auto

theorem methodOfType_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Ty) (a1 : String) (a2 : Ty) :
    Pres Sync (methodOfType c (n + 1) a0 a1 a2) := by
  rw [methodOfType.eq_def]; dsimp -zeta only; pres_auto

theorem unary_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : String) (a1 : Val) :
    Pres Sync (unary c (n + 1) a0 a1) := by
  rw [unary.eq_def]; dsimp -zeta only; pres_auto

theorem binary_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : String) (a1 : Val) (a2 : Val) :
    Pres Sync (binary c (n + 1) a0 a1 a2) := by
  rw [binary.eq_def]; dsimp -zeta only; pres_auto

theorem equal_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Val) (a1 : Val) :
    Pres Sync (equal c (n + 1) a0 a1) := by
  rw [equal.eq_def]; dsimp -zeta only; pres_auto

theorem equalAll_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : List Val) (a1 : List Val) :
    Pres Sync (equalAll c (n + 1) a0 a1) := by
  rw [equalAll.eq_def]; dsimp -zeta only; pres_auto

theorem evalCall_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Expr) (a2 : List Expr) (a3 : Bool) :
    Pres Sync (evalCall c (n + 1) a0 a1 a2 a3) := by
  rw [evalCall.eq_def]; dsimp -zeta only; pres_auto

theorem appendValues_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Ty) (a1 : Ty) (a2 : Val) (a3 : List Val) (a4 : Bool) :
    Pres Sync (appendValues c (n + 1) a0 a1 a2 a3 a4) := by
  rw [appendValues.eq_def]; dsimp -zeta only; pres_auto

theorem layout_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Ty) :
    Pres Sync (layout c (n + 1) a0) := by
  rw [layout.eq_def]; dsimp -zeta only; pres_auto

theorem genericArgs_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Val) (a2 : List Expr) (a3 : Bool) :
    Pres Sync (genericArgs c (n + 1) a0 a1 a2 a3) := by
  rw [genericArgs.eq_def]; dsimp -zeta only; pres_auto

theorem declaredFuncQ_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Expr) :
    Pres Sync (declaredFunc? c (n + 1) a0 a1) := by
  rw [declaredFunc?.eq_def]; dsimp -zeta only; pres_auto

theorem deferCall_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Expr) (a2 : List Expr) (a3 : Bool) :
    Pres Sync (deferCall c (n + 1) a0 a1 a2 a3) := by
  rw [deferCall.eq_def]; dsimp -zeta only; pres_auto

theorem evalArgs_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : List Expr) :
    Pres Sync (evalArgs c (n + 1) a0 a1) := by
  rw [evalArgs.eq_def]; dsimp -zeta only; pres_auto

theorem builtin_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : String) (a2 : List Expr) (a3 : Bool) :
    Pres Sync (builtin c (n + 1) a0 a1 a2 a3) := by
  rw [builtin.eq_def]; dsimp -zeta only; pres_auto

theorem panicText_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Val) :
    Pres Sync (panicText c (n + 1) a0) := by
  rw [panicText.eq_def]; dsimp -zeta only; pres_auto

theorem callMethodQ_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Val) (a1 : String) :
    Pres Sync (callMethod? c (n + 1) a0 a1) := by
  rw [callMethod?.eq_def]; dsimp -zeta only; pres_auto

theorem callValue_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Val) (a1 : List Val) (a2 : Bool) :
    Pres Sync (callValue c (n + 1) a0 a1 a2) := by
  rw [callValue.eq_def]; dsimp -zeta only; pres_auto

theorem callMethod_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : String) (a1 : List Ty) (a2 : Method) (a3 : Val) (a4 : List Val) (a5 : Bool) :
    Pres Sync (callMethod c (n + 1) a0 a1 a2 a3 a4 a5) := by
  rw [callMethod.eq_def]; dsimp -zeta only; pres_auto

theorem callBody_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Sig) (a2 : Option (List Stmt)) (a3 : Option (String × Val)) (a4 : List Val) (a5 : Bool) :
    Pres Sync (callBody c (n + 1) a0 a1 a2 a3 a4 a5) := by
  cases a1 <;> (rw [callBody.eq_def]; dsimp -zeta only; pres_auto)

theorem execStmts_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : List Stmt) :
    Pres Sync (execStmts c (n + 1) a0 a1) := by
  rw [execStmts.eq_def]; dsimp -zeta only; pres_auto

theorem execBlock_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : List Stmt) :
    Pres Sync (execBlock c (n + 1) a0 a1) := by
  rw [execBlock.eq_def]; dsimp -zeta only; pres_auto

theorem execStmt_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Stmt) :
    Pres Sync (execStmt c (n + 1) a0 a1) := by
  rw [execStmt.eq_def]; dsimp -zeta only; pres_auto

theorem declValue_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Option Ty) (a1 : Option Val) :
    Pres Sync (declValue c (n + 1) a0 a1) := by
  rw [declValue.eq_def]; dsimp -zeta only; pres_auto

theorem execInit_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Option Stmt) :
    Pres Sync (execInit c (n + 1) a0 a1) := by
  rw [execInit.eq_def]; dsimp -zeta only; pres_auto

theorem switchClauses_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Val) (a2 : List Clause) :
    Pres Sync (switchClauses c (n + 1) a0 a1 a2) := by
  rw [switchClauses.eq_def]; dsimp -zeta only; pres_auto

theorem typeClauses_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Option String) (a2 : Val) (a3 : List Clause) :
    Pres Sync (typeClauses c (n + 1) a0 a1 a2 a3) := by
  rw [typeClauses.eq_def]; dsimp -zeta only; pres_auto

theorem forLoop_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Option Expr) (a2 : Option Stmt) (a3 : List Stmt) :
    Pres Sync (forLoop c (n + 1) a0 a1 a2 a3) := by
  rw [forLoop.eq_def]; dsimp -zeta only; pres_auto

theorem rangeLoop_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Option Expr) (a2 : Option Expr) (a3 : String) (a4 : Expr) (a5 : List Stmt) :
    Pres Sync (rangeLoop c (n + 1) a0 a1 a2 a3 a4 a5) := by
  rw [rangeLoop.eq_def]; dsimp -zeta only; pres_auto

theorem assign_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : String) (a2 : List Expr) (a3 : List Expr) :
    Pres Sync (assign c (n + 1) a0 a1 a2 a3) := by
  rw [assign.eq_def]; dsimp -zeta only; pres_auto

theorem rhsValues_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Env) (a1 : Nat) (a2 : List Expr) :
    Pres Sync (rhsValues c (n + 1) a0 a1 a2) := by
  rw [rhsValues.eq_def]; dsimp -zeta only; pres_auto

theorem errorsNew_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : String) :
    Pres Sync (errorsNew c (n + 1) a0) := by
  rw [errorsNew.eq_def]; dsimp -zeta only; pres_auto

theorem fmtCall_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : String) (a1 : List Val) :
    Pres Sync (fmtCall c (n + 1) a0 a1) := by
  rw [fmtCall.eq_def]; dsimp -zeta only; pres_auto

theorem sprint_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : List Val) :
    Pres Sync (sprint c (n + 1) a0) := by
  rw [sprint.eq_def]; dsimp -zeta only; pres_auto

theorem sprintf_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : List Char) (a1 : List Val) :
    Pres Sync (sprintf c (n + 1) a0 a1) := by
  rw [sprintf.eq_def]; dsimp -zeta only; pres_auto

theorem fmtSpec_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Val) (a1 : Char) (a2 : FmtSpec) :
    Pres Sync (fmtSpec c (n + 1) a0 a1 a2) := by
  rw [fmtSpec.eq_def]; dsimp -zeta only; pres_auto

theorem fmtArg_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Val) (a1 : Char) :
    Pres Sync (fmtArg c (n + 1) a0 a1) := by
  rw [fmtArg.eq_def]; dsimp -zeta only; pres_auto

theorem fmtValue_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Val) (a1 : Char) (a2 : Nat) (a3 : Bool) :
    Pres Sync (fmtValue c (n + 1) a0 a1 a2 a3) := by
  rw [fmtValue.eq_def]; dsimp -zeta only; pres_auto

theorem libCall_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : String) (a1 : List Val) :
    Pres Sync (libCall c (n + 1) a0 a1) := by
  rw [libCall.eq_def]; dsimp -zeta only; pres_auto

theorem formatField_sync {c : Ctx} {n : Nat} (ih : SyncAll c n) (a0 : Val) :
    Pres Sync (formatField c (n + 1) a0) := by
  rw [formatField.eq_def]; dsimp -zeta only; pres_auto

theorem syncAll (c : Ctx) : ∀ n, SyncAll c n
  | 0 => {
    typeOf? := fun _ _ => by rw [typeOf?.eq_def]; exact pres_throw _,
    evalType := fun _ _ => by rw [evalType.eq_def]; exact pres_throw _,
    underlying := fun _ => by rw [underlying.eq_def]; exact pres_throw _,
    zero := fun _ => by rw [zero.eq_def]; exact pres_throw _,
    convert := fun _ _ => by rw [convert.eq_def]; exact pres_throw _,
    conversion := fun _ _ => by rw [conversion.eq_def]; exact pres_throw _,
    evalExpr := fun _ _ => by rw [evalExpr.eq_def]; exact pres_throw _,
    evalName := fun _ _ _ => by rw [evalName.eq_def]; exact pres_throw _,
    funcVal := fun _ _ _ => by rw [funcVal.eq_def]; exact pres_throw _,
    funcValue? := fun _ _ => by rw [funcValue?.eq_def]; exact pres_throw _,
    composite := fun _ _ _ => by rw [composite.eq_def]; exact pres_throw _,
    newArray := fun _ _ _ => by rw [newArray.eq_def]; exact pres_throw _,
    stringBytes := fun _ _ _ => by rw [stringBytes.eq_def]; exact pres_throw _,
    elementKeys := fun _ _ => by rw [elementKeys.eq_def]; exact pres_throw _,
    elements := fun _ _ _ => by rw [elements.eq_def]; exact pres_throw _,
    staticElement := fun _ _ _ => by rw [staticElement.eq_def]; exact pres_throw _,
    literalElement := fun _ _ _ => by rw [literalElement.eq_def]; exact pres_throw _,
    constantOperand := fun _ _ => by rw [constantOperand.eq_def]; exact pres_throw _,
    element := fun _ _ _ => by rw [element.eq_def]; exact pres_throw _,
    mapKey := fun _ _ => by rw [mapKey.eq_def]; exact pres_throw _,
    mapIndex := fun _ _ _ => by rw [mapIndex.eq_def]; exact pres_throw _,
    sliceOf := fun _ _ _ _ _ => by rw [sliceOf.eq_def]; exact pres_throw _,
    selector := fun _ _ _ => by rw [selector.eq_def]; exact pres_throw _,
    boundMethod := fun _ _ _ _ _ => by rw [boundMethod.eq_def]; exact pres_throw _,
    field? := fun _ _ => by rw [field?.eq_def]; exact pres_throw _,
    operand := fun _ _ => by rw [operand.eq_def]; exact pres_throw _,
    selectOn := fun _ _ _ => by rw [selectOn.eq_def]; exact pres_throw _,
    evalAddr := fun _ _ => by rw [evalAddr.eq_def]; exact pres_throw _,
    evalPlace := fun _ _ => by rw [evalPlace.eq_def]; exact pres_throw _,
    elemLoc := fun _ _ _ => by rw [elemLoc.eq_def]; exact pres_throw _,
    loadPlace := fun _ => by rw [loadPlace.eq_def]; exact pres_throw _,
    storePlace := fun _ _ => by rw [storePlace.eq_def]; exact pres_throw _,
    assertType := fun _ _ => by rw [assertType.eq_def]; exact pres_throw _,
    methodOfType := fun _ _ _ => by rw [methodOfType.eq_def]; exact pres_throw _,
    unary := fun _ _ => by rw [unary.eq_def]; exact pres_throw _,
    binary := fun _ _ _ => by rw [binary.eq_def]; exact pres_throw _,
    equal := fun _ _ => by rw [equal.eq_def]; exact pres_throw _,
    equalAll := fun _ _ => by rw [equalAll.eq_def]; exact pres_throw _,
    evalCall := fun _ _ _ _ => by rw [evalCall.eq_def]; exact pres_throw _,
    appendValues := fun _ _ _ _ _ => by rw [appendValues.eq_def]; exact pres_throw _,
    layout := fun _ => by rw [layout.eq_def]; exact pres_throw _,
    genericArgs := fun _ _ _ _ => by rw [genericArgs.eq_def]; exact pres_throw _,
    declaredFunc? := fun _ _ => by rw [declaredFunc?.eq_def]; exact pres_throw _,
    deferCall := fun _ _ _ _ => by rw [deferCall.eq_def]; exact pres_throw _,
    evalArgs := fun _ _ => by rw [evalArgs.eq_def]; exact pres_throw _,
    builtin := fun _ _ _ _ => by rw [builtin.eq_def]; exact pres_throw _,
    panicText := fun _ => by rw [panicText.eq_def]; exact pres_throw _,
    callMethod? := fun _ _ => by rw [callMethod?.eq_def]; exact pres_throw _,
    callValue := fun _ _ _ => by rw [callValue.eq_def]; exact pres_throw _,
    callMethod := fun _ _ _ _ _ _ => by rw [callMethod.eq_def]; exact pres_throw _,
    callBody := fun _ _ _ _ _ _ => by rw [callBody.eq_def]; exact pres_throw _,
    execStmts := fun _ _ => by rw [execStmts.eq_def]; exact pres_throw _,
    execBlock := fun _ _ => by rw [execBlock.eq_def]; exact pres_throw _,
    execStmt := fun _ _ => by rw [execStmt.eq_def]; exact pres_throw _,
    declValue := fun _ _ => by rw [declValue.eq_def]; exact pres_throw _,
    execInit := fun _ _ => by rw [execInit.eq_def]; exact pres_throw _,
    switchClauses := fun _ _ _ => by rw [switchClauses.eq_def]; exact pres_throw _,
    typeClauses := fun _ _ _ _ => by rw [typeClauses.eq_def]; exact pres_throw _,
    forLoop := fun _ _ _ _ => by rw [forLoop.eq_def]; exact pres_throw _,
    rangeLoop := fun _ _ _ _ _ _ => by rw [rangeLoop.eq_def]; exact pres_throw _,
    assign := fun _ _ _ _ => by rw [assign.eq_def]; exact pres_throw _,
    rhsValues := fun _ _ _ => by rw [rhsValues.eq_def]; exact pres_throw _,
    errorsNew := fun _ => by rw [errorsNew.eq_def]; exact pres_throw _,
    fmtCall := fun _ _ => by rw [fmtCall.eq_def]; exact pres_throw _,
    sprint := fun _ => by rw [sprint.eq_def]; exact pres_throw _,
    sprintf := fun _ _ => by rw [sprintf.eq_def]; exact pres_throw _,
    fmtSpec := fun _ _ _ => by rw [fmtSpec.eq_def]; exact pres_throw _,
    fmtArg := fun _ _ => by rw [fmtArg.eq_def]; exact pres_throw _,
    fmtValue := fun _ _ _ _ => by rw [fmtValue.eq_def]; exact pres_throw _,
    libCall := fun _ _ => by rw [libCall.eq_def]; exact pres_throw _,
    formatField := fun _ => by rw [formatField.eq_def]; exact pres_throw _
  }
  | n + 1 =>
    have ih := syncAll c n
    {
    typeOf? := typeOfQ_sync ih,
    evalType := evalType_sync ih,
    underlying := underlying_sync ih,
    zero := zero_sync ih,
    convert := convert_sync ih,
    conversion := conversion_sync ih,
    evalExpr := evalExpr_sync ih,
    evalName := evalName_sync ih,
    funcVal := funcVal_sync ih,
    funcValue? := funcValueQ_sync ih,
    composite := composite_sync ih,
    newArray := newArray_sync ih,
    stringBytes := stringBytes_sync ih,
    elementKeys := elementKeys_sync ih,
    elements := elements_sync ih,
    staticElement := staticElement_sync ih,
    literalElement := literalElement_sync ih,
    constantOperand := constantOperand_sync ih,
    element := element_sync ih,
    mapKey := mapKey_sync ih,
    mapIndex := mapIndex_sync ih,
    sliceOf := sliceOf_sync ih,
    selector := selector_sync ih,
    boundMethod := boundMethod_sync ih,
    field? := fieldQ_sync ih,
    operand := operand_sync ih,
    selectOn := selectOn_sync ih,
    evalAddr := evalAddr_sync ih,
    evalPlace := evalPlace_sync ih,
    elemLoc := elemLoc_sync ih,
    loadPlace := loadPlace_sync ih,
    storePlace := storePlace_sync ih,
    assertType := assertType_sync ih,
    methodOfType := methodOfType_sync ih,
    unary := unary_sync ih,
    binary := binary_sync ih,
    equal := equal_sync ih,
    equalAll := equalAll_sync ih,
    evalCall := evalCall_sync ih,
    appendValues := appendValues_sync ih,
    layout := layout_sync ih,
    genericArgs := genericArgs_sync ih,
    declaredFunc? := declaredFuncQ_sync ih,
    deferCall := deferCall_sync ih,
    evalArgs := evalArgs_sync ih,
    builtin := builtin_sync ih,
    panicText := panicText_sync ih,
    callMethod? := callMethodQ_sync ih,
    callValue := callValue_sync ih,
    callMethod := callMethod_sync ih,
    callBody := callBody_sync ih,
    execStmts := execStmts_sync ih,
    execBlock := execBlock_sync ih,
    execStmt := execStmt_sync ih,
    declValue := declValue_sync ih,
    execInit := execInit_sync ih,
    switchClauses := switchClauses_sync ih,
    typeClauses := typeClauses_sync ih,
    forLoop := forLoop_sync ih,
    rangeLoop := rangeLoop_sync ih,
    assign := assign_sync ih,
    rhsValues := rhsValues_sync ih,
    errorsNew := errorsNew_sync ih,
    fmtCall := fmtCall_sync ih,
    sprint := sprint_sync ih,
    sprintf := sprintf_sync ih,
    fmtSpec := fmtSpec_sync ih,
    fmtArg := fmtArg_sync ih,
    fmtValue := fmtValue_sync ih,
    libCall := libCall_sync ih,
    formatField := formatField_sync ih
  }

/-! ## Programs -/

theorem initPkg_sim (c : Ctx) (k : Nat) {d d' : Nat} (h : d ≤ d') (path : String) :
    GSim k (initPkg c d path) (initPkg c d' path) := by
  have ih := simAll c k d d' h
  unfold initPkg; gsim_auto

/-- What `runProgramAt` runs: package initialization, then `main`, a panic's text made
    by `panicText`. Its own copy, which `runProgramAt_eq` checks against the definition. -/
def runGo (c : Ctx) (depth : Nat) : GM (Option String) :=
  tryCatch (do
      for path in pkgOrder c do
        initPkg c depth path
      let mainFn ← funcVal c depth c.main "main" []
      let _ ← callValue c depth mainFn [] false
      pure none) fun
    | .panic v runtime => do
      let text ← if runtime then asStr v else panicText c depth v
      pure (some text)
    | e => throw e

/-- How `runProgramAt` reads the end of a run. -/
def finish : Except Stop (Option String) × St → Outcome × String
  | (r, s) =>
    let o := match r with
      | .ok none => .ok
      | .ok (some text) => .panic text
      | .error (.panic ..) => .unsupported "panic-while-printing-a-panic"
      | .error .fuel => .fuel
      | .error (.unsupported what) => .unsupported what
      | .error (.stuck msg) => .stuck msg
      | .error (.ret _) => .stuck "return outside a function"
      | .error .brk | .error .cont => .stuck "break or continue outside a loop"
    (o, s.out)

/-- The state a run starts in, with `fuel` steps. -/
def initSt (fuel : Nat) : St := { cells := #[], closures := #[], out := "", steps := fuel }

theorem runProgramAt_eq (ord : Order) (prog : Program) (d f : Nat) :
    runProgramAt ord prog d f = finish ((runGo (buildCtx (ord prog)) d).run.run (initSt f)) := rfl

/-- Package initialization keeps `Sync`: it allocates each package-level variable's cell
    with its type (`allocAs`) and calls functions `syncAll` covers. -/
theorem initPkg_sync (c : Ctx) (d : Nat) (path : String) : Pres Sync (initPkg c d path) := by
  have ih := syncAll c d
  unfold initPkg; pres_auto

/-- A whole run keeps `Sync`: package initialization, `main`, and a panic's text. -/
theorem runGo_sync (c : Ctx) (d : Nat) : Pres Sync (runGo c d) := by
  have ih := syncAll c d
  have ihP := fun p => initPkg_sync c d p
  unfold runGo
  refine pres_tryCatch ?_ (fun e => ?_)
  · refine pres_bind (pres_forIn _ _ fun p _ => pres_bind (ihP p) fun _ => pres_pure _) fun _ => ?_
    pres_auto
  · pres_auto

/-- **`Inv3.sync` for whole programs.** A program's run starts with no cells and no cell
    types, and every part of it keeps their numbers equal (`runGo_sync`, from `syncAll`
    and `initPkg_sync`), so the state it ends in, at any depth and fuel, has as many cell
    types as cells, as has every state a function of the run starts from. -/
theorem runProgramAt_sync (ord : Order) (prog : Program) (d f : Nat) :
    Sync ((runGo (buildCtx (ord prog)) d).run.run (initSt f)).2 :=
  runGo_sync _ d _ rfl

theorem runGo_sim (c : Ctx) (k : Nat) {d d' : Nat} (h : d ≤ d') :
    GSim k (runGo c d) (runGo c d') := by
  have ih := simAll c k d d' h
  have ihP := fun p => initPkg_sim c k h p
  unfold runGo
  refine sim_tryCatch ?_ (fun e => ?_) (fun _ hs => hs)
  · refine sim_bind (sim_forIn _ _ fun p _ => sim_bind (ihP p) fun _ => sim_pure _) fun _ => ?_
    gsim_auto
  · gsim_auto

/-- **GoCore fuel adequacy.** A run of a program that does not run out of fuel ends the
    same way and prints the same output with any more depth and any more steps: `ok`, a
    panic, `unsupported` and `stuck` are what the program does under GoCore, not what the
    budget allowed. A run that ends in `fuel` says nothing. Lean's kernel does not
    evaluate a whole program's run (`buildCtx`'s hash maps), so the hypothesis is
    discharged for concrete programs by step 5's whole-program instances; the
    per-function results it rests on are pinned on `sw(1, 2, 2)` (`sw_gocore_more`). -/
theorem runProgramAt_adequate (ord : Order) (prog : Program) {d d' f f' : Nat}
    (hd : d ≤ d') (hf : f ≤ f') (h : (runProgramAt ord prog d f).1 ≠ .fuel) :
    runProgramAt ord prog d' f' = runProgramAt ord prog d f := by
  rw [runProgramAt_eq] at h ⊢
  rw [runProgramAt_eq]
  have hs : initSt f' = shift (f' - f) (initSt f) := by
    simp only [initSt, shift]; congr; omega
  rw [hs]
  rcases hr : (runGo (buildCtx (ord prog)) d).run.run (initSt f) with ⟨r, t⟩
  rw [hr] at h
  have hnf : GNotFuel r := by
    cases r with
    | ok => trivial
    | error e => cases e <;> first | trivial | exact h rfl
  rw [(runGo_sim (buildCtx (ord prog)) (f' - f) hd).prop _ (by rw [hr]; exact hnf), hr]
  rfl

/-- `runProgram`, whose depth is the fuel capped at `maxDepth`: more fuel ends a run
    that did not run out of it the same way. -/
theorem runProgram_adequate (prog : Program) {f f' : Nat} (hf : f ≤ f')
    (h : (runProgram prog f).1 ≠ .fuel) : runProgram prog f' = runProgram prog f :=
  runProgramAt_adequate .gc prog (by omega) hf h

end Gala.M5
