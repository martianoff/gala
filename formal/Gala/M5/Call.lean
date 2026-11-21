import Gala.M5.Stmt
namespace Gala.M5
open Gala.GoCore.Full

theorem nameOwner_obj (c : Ctx) (env : Env) (f : String) (p : PkgCtx) (o : Obj)
    (hpk : c.pkgs.get? env.pkg = some p) (hobj : p.objs.get? f = some o) :
    nameOwner c env f = some env.pkg := by
  unfold nameOwner
  rw [hpk]
  simp only [Std.HashMap.contains_eq_isSome_getElem?, ← Std.HashMap.get?_eq_getElem?, hobj, Option.isSome_some, ite_true]

/-- The declared function a call names, as GoCore's value. -/
def declFn (P f : String) : Val := .func_ (.func_ [] [] false) (some (.decl P f []))

section
variable (c : Ctx) (env : Env) (f : String) (p : PkgCtx) (fi : Nat) (sig : Sig) (body : List Stmt)
  (hpk : c.pkgs.get? env.pkg = some p) (hl : env.lookup f = none) (ht : env.tenv = [])
  (hobj : p.objs.get? f = some (.func fi [] sig (some body)))
include hpk hl ht hobj

theorem typeOf_fn (n : Nat) (g : St) : (typeOf? c (n + 1) env (.ident f)).run.run g = (.ok none, g) := by
  have h2 := hobj; have h1 := hpk
  simp only [Std.HashMap.get?_eq_getElem?] at h1 h2
  rw [typeOf?.eq_2]
  simp [hl, ht, nameOwner_obj c env f p _ hpk hobj, h1, h2]
  rfl

omit ht in
theorem declaredFunc_fn (n : Nat) (g : St) :
    (declaredFunc? c (n + 1) env (.ident f)).run.run g = (.ok (some (declFn env.pkg f)), g) := by
  have h2 := hobj; have h1 := hpk
  simp only [Std.HashMap.get?_eq_getElem?] at h1 h2
  unfold declaredFunc?
  simp [hl, nameOwner_obj c env f p _ hpk hobj, h1, h2]
  rfl

theorem evalCall_fn (n : Nat) (args : List Expr) (g : St) :
    (evalCall c (n + 2) env (.ident f) args false).run.run g =
      (do callValue c (n + 1) (declFn env.pkg f) (← evalArgs c (n + 1) env args) false).run.run g := by
  rw [evalCall.eq_2]
  simp only [grun_bind, typeOf_fn c env f p fi sig body hpk hl ht hobj]
  simp only [hl, nameOwner_obj c env f p _ hpk hobj, Option.isNone_some, Bool.and_false,
    Bool.false_eq_true, ite_false]
  rw [grun_bind, declaredFunc_fn c env f p fi sig body hpk hl hobj]
  simp only [grun_bind]
end

theorem callValue_fn (c : Ctx) (P f : String) (p : PkgCtx) (fi : Nat) (sig : Sig) (body : List Stmt)
    (hpk : c.pkgs.get? P = some p) (hobj : p.objs.get? f = some (.func fi [] sig (some body)))
    (hstd : P ≠ stdPath) (n : Nat) (vs : List Val) (g : St) :
    (callValue c (n + 1) (declFn P f) vs false).run.run g =
      (callBody c n { pkg := P, file := fi, tenv := [] } sig (some body) none vs false).run.run g := by
  have h1 := hpk; have h2 := hobj
  simp only [Std.HashMap.get?_eq_getElem?] at h1 h2
  unfold callValue
  simp [declFn, hstd, pkgCtx, h1, h2, bindTParams, fieldListNames]

/-- Go's name of a kind's type, and the type. -/
def kindName : Kind → String
  | .int => "int" | .bool => "bool" | .str => "string"
def kindTy : Kind → Ty
  | .int => intTy | .bool => .bool | .str => .string

theorem evalType_kind (c : Ctx) (n : Nat) (env : Env) (k : Kind) (g : St)
    (hl : env.lookup (kindName k) = none) (ht : env.tenv = [])
    (ho : nameOwner c env (kindName k) = none) :
    (evalType c (n + 2) env (.ident (kindName k))).run.run g = (.ok (kindTy k), g) := by
  unfold evalType
  simp only [grun_bind]
  rw [typeOf?.eq_2]
  cases k <;> simp only [kindName] at hl ho ⊢ <;>
    simp [kindTy, hl, ht, ho, universeType?, IntKind.ofName?] <;> rfl

theorem convert_kind (c : Ctx) (n : Nat) (v : Spec.Val) (w : Val) (k : Kind) (g : St)
    (hv : VRel v w) (hk : valKind v = some k) :
    (convert c (n + 2) (kindTy k) w).run.run g = (.ok (defaultVal w), g) := by
  cases hv <;> simp only [valKind, Option.some.injEq] at hk <;> subst hk
  · simp [convert, kindTy, Val.materializeTyped, Val.ty, defaultVal]; rfl
  · rename_i x hx; simpa [kindTy, defaultVal, UKind.default] using convert_uint c n x g hx
  · simp [convert, kindTy, Val.materializeTyped, Val.ty, defaultVal]; rfl
  · simpa [kindTy, defaultVal] using convert_ubool c n _ g
  · simp [convert, kindTy, Val.materializeTyped, Val.ty, defaultVal]; rfl
  · simpa [kindTy, defaultVal] using convert_ustr c n _ g

/-! ### A function's body, called -/

/-- A `for` loop each of whose iterations ends normally as `step` says: the loop's result
    is the fold of `step` over the list. -/
theorem forIn_steps {A : Type} (F : A → Env → GM (ForInStep Env)) (step : A → Env × St → Env × St)
    (P : A → Prop) (hF : ∀ a env g, P a → (F a env).run.run g = (.ok (.yield (step a (env, g)).1), (step a (env, g)).2)) :
    ∀ (l : List A) (env : Env) (g : St), (∀ a ∈ l, P a) →
      (forIn l env F).run.run g = (.ok (l.foldl (fun s a => step a s) (env, g)).1, (l.foldl (fun s a => step a s) (env, g)).2)
  | [], env, g, _ => rfl
  | a :: l, env, g, hP => by
    rw [List.forIn_cons, grun_bind, hF a env g (hP a (by simp))]
    exact forIn_steps F step P hF l _ _ (fun b hb => hP b (by simp [hb]))

/-- The type a parameter's type expression names, for the three kinds. -/
def teTy : Expr → Ty
  | .ident "int" => intTy
  | .ident "bool" => .bool
  | _ => .string

theorem teTy_kind (k : Kind) : teTy (.ident (kindName k)) = kindTy k := by cases k <;> rfl

/-- One parameter bound: a new cell holding the argument with its type. -/
def paramStep : (String × Expr) × Val → Env × St → Env × St
  | ((x, te), v), (env, g) =>
    (env.bind x g.cells.size, { g with cells := g.cells.push (defaultVal v), types := g.types.push (teTy te) })

/-- The signature the lowering gives a function: one parameter per name, one result. -/
def lowerSigOf (xks : List (String × Kind)) (ρ : Kind) : Sig :=
  .mk (xks.map fun xk => .mk [xk.1] (.ident (kindName xk.2))) [.mk [] (.ident (kindName ρ))] false

/-- The environment a function's body is called in, before its parameters. -/
def calleeEnv (P : String) (fi : Nat) : Env := { pkg := P, file := fi, tenv := [] }

/-- The parameters of a called function, bound: GoCore's environment and state. -/
def paramsBound (xks : List (String × Kind)) (vs : List Val) (env : Env) (g : St) : Env × St :=
  ((xks.map fun xk => (xk.1, Expr.ident (kindName xk.2))).zip vs).foldl (fun s a => paramStep a s) (env, g)

theorem flatMap_params (xks : List (String × Kind)) :
    (xks.map fun xk => Field.mk [xk.1] (.ident (kindName xk.2))).flatMap
      (fun | .mk ns t => if ns.isEmpty then [("_", t)] else ns.map (·, t)) =
    xks.map fun xk => (xk.1, Expr.ident (kindName xk.2)) := by
  induction xks with
  | nil => rfl
  | cons a l ih =>
    rw [List.map_cons, List.flatMap_cons, ih]
    simp

/-- An argument of a parameter's kind. -/
def ArgRel (xk : String × Kind) (w : Val) : Prop := ∃ v, VRel v w ∧ valKind v = some xk.2

/-- Arguments of the parameters' kinds, one each. -/
def ArgsRel : List (String × Kind) → List Val → Prop
  | [], [] => True
  | xk :: xks, w :: ws => ArgRel xk w ∧ ArgsRel xks ws
  | _, _ => False

theorem argsRel_length : ∀ {xks : List (String × Kind)} {vs : List Val}, ArgsRel xks vs → xks.length = vs.length
  | [], [], _ => rfl
  | _ :: _, _ :: _, h => by simp [argsRel_length h.2]

theorem zip_args : ∀ {xks : List (String × Kind)} {vs : List Val}, ArgsRel xks vs →
    ∀ a ∈ (xks.map fun xk => (xk.1, Expr.ident (kindName xk.2))).zip vs,
      ∃ k v, a.1.2 = .ident (kindName k) ∧ VRel v a.2 ∧ valKind v = some k
  | [], [], _, a, ha => by simp at ha
  | xk :: xks, w :: ws, ⟨⟨v, hv, hk⟩, h⟩, a, ha => by
    simp only [List.map_cons, List.zip_cons_cons, List.mem_cons] at ha
    rcases ha with rfl | ha
    · exact ⟨xk.2, v, rfl, hv, hk⟩
    · exact zip_args h a ha

theorem flatMap_single {α β : Type} (f : α → β) (l : List α) : l.flatMap (fun a => [f a]) = l.map f := by
  induction l with
  | nil => rfl
  | cons a l ih => simp [ih]

theorem gget_run (g : St) : (get : GM St).run.run g = (.ok g, g) := rfl

theorem gmodify_run (f : St → St) (g : St) : (modify f : GM Unit).run.run g = (.ok (), f g) := rfl

theorem emptyStr_isEmpty : ("" : String).isEmpty = true := rfl

theorem gtryCatch {α : Type} (x : GM α) (h : Stop → GM α) (g : St) :
    (tryCatch x h).run.run g = match x.run.run g with
      | (.ok a, t) => (.ok a, t)
      | (.error e, t) => (h e).run.run t := by
  show (ExceptT.tryCatch x h).run.run g = _
  simp only [ExceptT.tryCatch, ExceptT.run_mk, StateT.run_bind]
  rcases hx : x.run.run g with ⟨r, t⟩
  have h2 : StateT.run x g = (r, t) := hx
  rw [h2]; cases r <;> rfl

theorem paramStep_defers (a : (String × Expr) × Val) (s : Env × St) : (paramStep a s).2.defers = s.2.defers := by
  rcases a with ⟨⟨x, te⟩, v⟩; rfl

theorem foldl_paramStep_defers : ∀ (l : List ((String × Expr) × Val)) (s : Env × St),
    (l.foldl (fun s a => paramStep a s) s).2.defers = s.2.defers
  | [], _ => rfl
  | a :: l, s => by rw [List.foldl_cons, foldl_paramStep_defers l, paramStep_defers]

theorem paramsBound_defers (xks : List (String × Kind)) (vs : List Val) (env : Env) (g : St) :
    (paramsBound xks vs env g).2.defers = g.defers := foldl_paramStep_defers _ _

theorem gtick_succ (g : St) (k : Nat) (hk : g.steps = k + 1) :
    (tick : GM Unit).run.run g = (.ok (), { g with steps := k }) := by
  obtain ⟨cells, types, cl, out, steps, pr, gl, de, re, ma⟩ := g
  simp only at hk; subst hk; rfl

/-- `allocAs`, run. -/
theorem allocAs_run (t : Ty) (v : Val) (g : St) :
    (allocAs t v).run.run g = (.ok g.cells.size, { g with cells := g.cells.push v, types := g.types.push t }) := rfl

/-- One parameter bound, as `callBody`'s loop does it. -/
theorem param_iter (c : Ctx) (n : Nat) (P : String) (fi : Nat)
    (hnames : ∀ k, nameOwner c (calleeEnv P fi) (kindName k) = none) (x : String) (k : Kind) (v : Spec.Val)
    (w : Val) (hv : VRel v w) (hk : valKind v = some k) (env : Env) (g : St) :
    (do let t ← evalType c (n + 2) (calleeEnv P fi) (.ident (kindName k))
        let w' ← convert c (n + 2) t w
        let cell ← allocAs t w'
        pure (ForInStep.yield (env.bind x cell)) : GM (ForInStep Env)).run.run g =
      (.ok (.yield (paramStep ((x, .ident (kindName k)), w) (env, g)).1),
        (paramStep ((x, .ident (kindName k)), w) (env, g)).2) := by
  have he := evalType_kind c n (calleeEnv P fi) k g (by cases k <;> rfl) rfl (hnames k)
  simp only [grun_bind, he, convert_kind c n v w k g hv hk, allocAs_run, paramStep, teTy_kind]
  rfl

/-- **A lowered function's body, called.** GoCore's `callBody` on the lowered signature,
    arguments of the parameters' kinds: one step taken, the parameters bound to new
    cells holding the arguments with their types, a list of deferred calls of its own;
    when the body returns a value of the result's kind with that list still empty, the
    call's result is that value with the result's type, and the list is gone. -/
theorem callBody_ret (c : Ctx) (n : Nat) (P : String) (fi : Nat) (xks : List (String × Kind))
    (ρ : Kind) (body : List Stmt) (vs : List Val) (g : St) (k : Nat) (hk : g.steps = k + 1)
    (hargs : ArgsRel xks vs) (hnames : ∀ k, nameOwner c (calleeEnv P fi) (kindName k) = none)
    (v : Spec.Val) (w : Val) (hw : VRel v w) (hwk : valKind v = some ρ) (g1 : St)
    (hb : (execStmts c (n + 2) (paramsBound xks vs (calleeEnv P fi).push { g with steps := k }).1 body).run.run
      { (paramsBound xks vs (calleeEnv P fi).push { g with steps := k }).2 with
        defers := [] :: g.defers } = (.error (.ret [w]), g1))
    (hd : g1.defers = [] :: g.defers) :
    (callBody c (n + 3) (calleeEnv P fi) (lowerSigOf xks ρ) (some body) none vs false).run.run g =
      (.ok [defaultVal w], { g1 with defers := g.defers }) := by
  rw [show n + 3 = (n + 2).succ from rfl, lowerSigOf, callBody.eq_2]
  simp only [grun_bind, gtick_succ g k hk, Bool.not_false, ite_true, Bool.false_and,
    Bool.false_eq_true, ite_false, run_pureM]
  simp only [List.flatMap_map, List.isEmpty_cons, Bool.false_eq_true, ite_false, List.map_cons,
    List.map_nil, flatMap_single, List.flatMap_cons, List.flatMap_nil, List.isEmpty_nil, ite_true,
    List.append_nil]
  simp only [List.length_map, argsRel_length hargs, bne_self_eq_false, Bool.false_eq_true, ite_false]
  rw [grun_bind, forIn_steps _ paramStep (fun a => ∃ k v, a.1.2 = .ident (kindName k) ∧ VRel v a.2 ∧ valKind v = some k)
    ?step _ _ _ (zip_args hargs)]
  case step =>
    rintro ⟨⟨x, te⟩, w⟩ env g' ⟨k', v', hte, hv', hk'⟩
    simp only at hte; subst hte
    exact param_iter c n P fi hnames x k' v' w hv' hk' env g'
  have hty : ∀ g', (evalType c (n + 2) (calleeEnv P fi) (.ident (kindName ρ))).run.run g' = (.ok (kindTy ρ), g') :=
    fun g' => evalType_kind c n (calleeEnv P fi) ρ g' (by cases ρ <;> rfl) rfl (hnames ρ)
  simp only [List.mapM_cons, List.mapM_nil, grun_bind, hty, run_pureM, List.zip_cons_cons, List.zip_nil_right,
    List.forIn_cons, List.forIn_nil, emptyStr_isEmpty, Bool.not_true, Bool.false_eq_true, ite_false,
    gtryCatch, gget_run, gmodify_run, foldl_paramStep_defers]
  simp only [paramsBound] at hb
  simp only [hb]
  have hcv : ∀ g', (convert c (n + 2) (kindTy ρ) w).run.run g' = (.ok (defaultVal w), g') :=
    fun g' => convert_kind c n v w ρ g' hw hwk
  simp only [run_pureM]
  simp [hcv, gget_run, gmodify_run, hd, grun_bind]
  rfl

/-- **... and when it panics**, with its list of deferred calls still empty: the call
    panics the same way, the list gone. -/
theorem callBody_panic (c : Ctx) (n : Nat) (P : String) (fi : Nat) (xks : List (String × Kind))
    (ρ : Kind) (body : List Stmt) (vs : List Val) (g : St) (k : Nat) (hk : g.steps = k + 1)
    (hargs : ArgsRel xks vs) (hnames : ∀ k, nameOwner c (calleeEnv P fi) (kindName k) = none)
    (pv : Val) (rt : Bool) (g1 : St)
    (hb : (execStmts c (n + 2) (paramsBound xks vs (calleeEnv P fi).push { g with steps := k }).1 body).run.run
      { (paramsBound xks vs (calleeEnv P fi).push { g with steps := k }).2 with
        defers := [] :: g.defers } = (.error (.panic pv rt), g1))
    (hd : g1.defers = [] :: g.defers) :
    (callBody c (n + 3) (calleeEnv P fi) (lowerSigOf xks ρ) (some body) none vs false).run.run g =
      (.error (.panic pv rt), { g1 with defers := g.defers }) := by
  rw [show n + 3 = (n + 2).succ from rfl, lowerSigOf, callBody.eq_2]
  simp only [grun_bind, gtick_succ g k hk, Bool.not_false, ite_true, Bool.false_and,
    Bool.false_eq_true, ite_false, run_pureM]
  simp only [List.flatMap_map, List.isEmpty_cons, Bool.false_eq_true, ite_false, List.map_cons,
    List.map_nil, flatMap_single, List.flatMap_cons, List.flatMap_nil, List.isEmpty_nil, ite_true,
    List.append_nil]
  simp only [List.length_map, argsRel_length hargs, bne_self_eq_false, Bool.false_eq_true, ite_false]
  rw [grun_bind, forIn_steps _ paramStep (fun a => ∃ k v, a.1.2 = .ident (kindName k) ∧ VRel v a.2 ∧ valKind v = some k)
    ?step _ _ _ (zip_args hargs)]
  case step =>
    rintro ⟨⟨x, te⟩, w⟩ env g' ⟨k', v', hte, hv', hk'⟩
    simp only at hte; subst hte
    exact param_iter c n P fi hnames x k' v' w hv' hk' env g'
  have hty : ∀ g', (evalType c (n + 2) (calleeEnv P fi) (.ident (kindName ρ))).run.run g' = (.ok (kindTy ρ), g') :=
    fun g' => evalType_kind c n (calleeEnv P fi) ρ g' (by cases ρ <;> rfl) rfl (hnames ρ)
  simp only [List.mapM_cons, List.mapM_nil, grun_bind, hty, run_pureM, List.zip_cons_cons, List.zip_nil_right,
    List.forIn_cons, List.forIn_nil, emptyStr_isEmpty, Bool.not_true, Bool.false_eq_true, ite_false,
    gtryCatch, gget_run, gmodify_run, foldl_paramStep_defers]
  simp only [paramsBound] at hb
  simp only [hb]
  simp only [run_pureM]
  simp [gget_run, gmodify_run, hd, grun_bind]
  rfl

/-! ### galaspec's call, unfolded -/

section SpecCall
open Spec (eval evalList funcArgs bindArgs bindDefaults callFunc runIn argSlots)

theorem ebind_run {σ ε α β : Type} (x : ExceptT ε (StateM σ) α) (f : α → ExceptT ε (StateM σ) β) (s : σ) :
    (x >>= f).run.run s = match x.run.run s with
      | (.ok a, t) => (f a).run.run t
      | (.error e, t) => (.error e, t) := by
  simp only [ExceptT.run_bind, StateT.run_bind]
  rcases hx : x.run.run s with ⟨r, t⟩
  cases r <;> rfl

/-- A `for` loop in a state-and-exception monad each of whose iterations changes no
    state and goes on: its result is the fold of `step`. -/
theorem forIn_fold {σ ε A B : Type} (F : A → B → ExceptT ε (StateM σ) (ForInStep B)) (step : A → B → B)
    (P : A → Prop) (hF : ∀ a b s, P a → (F a b).run.run s = (.ok (.yield (step a b)), s)) :
    ∀ (l : List A) (b : B) (s : σ), (∀ a ∈ l, P a) →
      (forIn l b F).run.run s = (.ok (l.foldl (fun b a => step a b) b), s)
  | [], _, _, _ => rfl
  | a :: l, b, s, hP => by
    rw [List.forIn_cons, ebind_run, hF a b s (hP a (by simp))]
    exact forIn_fold F step P hF l _ s (fun x hx => hP x (by simp [hx]))

theorem eraseDups_nodup {α : Type} [BEq α] [LawfulBEq α] :
    ∀ (n : Nat) (l : List α), l.length ≤ n → l.Nodup → l.eraseDups = l
  | _, [], _, _ => rfl
  | n + 1, a :: l, hl, hnd => by
    rw [List.eraseDups_cons]
    have hnd' := List.nodup_cons.mp hnd
    have hf : l.filter (fun b => !b == a) = l := by
      rw [List.filter_eq_self]
      intro b hb
      have : b ≠ a := fun h => hnd'.1 (h ▸ hb)
      simpa using this
    rw [hf, eraseDups_nodup n l (by simp at hl; omega) hnd'.2]

/-- A positional argument takes the next slot. -/
theorem argSlots_go_pos (names : List String) (a : Spec.Expr) (as : List Spec.Expr) (slots : List Nat)
    (ha : Spec.isNamed a = false) :
    argSlots.go names (a :: as) false slots = argSlots.go names as false (slots ++ [slots.length]) := by
  cases a <;> simp [Spec.isNamed] at ha <;> rfl

theorem argSlots_go_range (names : List String) :
    ∀ (args : List Spec.Expr) (m : Nat), (∀ a ∈ args, Spec.isNamed a = false) →
      m + args.length ≤ names.length →
      argSlots.go names args false (List.range m) = .ok (List.range (m + args.length))
  | [], m, _, hl => by
    simp only [argSlots.go, List.length_nil, Nat.add_zero]
    rw [eraseDups_nodup _ _ (Nat.le_refl _) List.nodup_range]
    have : (List.range m).any (· ≥ names.length) = false := by
      simp only [List.any_eq_false, List.mem_range, decide_eq_true_eq, Nat.not_le]
      intro x hx; simp at hl; omega
    simp [this]
  | a :: as, m, ha, hl => by
    rw [argSlots_go_pos names a as _ (ha a (by simp)), List.length_range, ← List.range_succ]
    rw [argSlots_go_range names as (m + 1) (fun b hb => ha b (by simp [hb])) (by simp at hl ⊢; omega)]
    simp [Nat.add_assoc, Nat.add_comm 1]

/-- **Positional arguments covering every parameter** take the slots `0, 1, ...`. -/
theorem argSlots_pos (names : List String) (args : List Spec.Expr) (ha : ∀ a ∈ args, Spec.isNamed a = false)
    (hl : args.length = names.length) : argSlots names args = .ok (List.range args.length) := by
  unfold argSlots
  simpa using argSlots_go_range names args 0 ha (by omega)

theorem lookup_range_zip {β : Type} : ∀ (l : List β) (m j : Nat),
    ((List.range' m l.length).zip l).lookup j = if m ≤ j then l[j - m]? else none
  | [], m, j => by simp
  | b :: l, m, j => by
    rw [List.length_cons, List.range'_succ, List.zip_cons_cons, List.lookup_cons, lookup_range_zip l (m + 1) j]
    by_cases h : j = m
    · subst h; simp
    · have : (j == m) = false := by simpa using h
      rw [this]
      by_cases h2 : m + 1 ≤ j
      · have : j - m = (j - (m + 1)) + 1 := by omega
        simp [h2, show m ≤ j by omega, this]
      · simp [h2, show ¬ m ≤ j by omega]

theorem foldl_append_map {α β : Type} (f : α → β) : ∀ (l : List α) (acc : List β),
    l.foldl (fun b a => b ++ [f a]) acc = acc ++ l.map f
  | [], acc => by simp
  | a :: l, acc => by simp [foldl_append_map f l]

theorem bindDefaults_given (ds : Spec.Decls) (d : Nat) (names : List String) (tys : List (Option Spec.Ty))
    (dflts : List (Option Spec.Expr)) (fs : Bool) (vs : List Spec.Val) (hl : vs.length = names.length)
    (s : Spec.St) :
    (bindDefaults ds d names tys dflts fs ((List.range vs.length).zip vs)).run.run s = (.ok vs, s) := by
  rw [Spec.bindDefaults.eq_def]
  simp only
  rw [ebind_run, forIn_fold _ (fun a acc => acc ++ [vs[a.2]!]) (fun a => a.2 < vs.length) ?step _ _ _
    (fun a ha => hl ▸ List.snd_lt_of_mem_zipIdx ha)]
  case step =>
    intro a acc s' ha
    have hlk : List.lookup a.2 ((List.range vs.length).zip vs) = some vs[a.2]! := by
      rw [List.range_eq_range', lookup_range_zip vs 0 a.2]
      simp [ha]
    simp only [hlk]
    rfl
  rw [foldl_append_map (fun a : String × Nat => vs[a.2]!)]
  have hm : names.zipIdx.map (fun a : String × Nat => vs[a.2]!) = vs := by
    rw [show (fun a : String × Nat => vs[a.2]!) = (fun j => vs[j]!) ∘ Prod.snd from rfl, ← List.map_map,
      List.zipIdx_map_snd, ← hl]
    apply List.ext_getElem
    · simp
    · intro i h1 h2; simp [List.getElem?_eq_getElem h2]
  rw [hm]
  rfl

theorem evalList_length (ds : Spec.Decls) (d : Nat) : ∀ (es : List Spec.Expr) (s : Spec.St) (vs : List Spec.Val) (t : Spec.St),
    (evalList ds d es).run.run s = (.ok vs, t) → vs.length = es.length
  | [], s, vs, t, h => by
    rw [Spec.evalList.eq_def] at h; simp only at h
    cases h; rfl
  | e :: es, s, vs, t, h => by
    rw [Spec.evalList.eq_def] at h
    simp only [Spec.run_bind_eq] at h
    rcases h1 : (eval ds d e).run.run s with ⟨r1, s1⟩
    rw [h1] at h
    cases r1 with
    | error => cases h
    | ok v =>
      simp only at h
      rcases h2 : (evalList ds d es).run.run s1 with ⟨r2, s2⟩
      rw [h2] at h
      cases r2 with
      | error => cases h
      | ok vs' =>
        simp only [run_pureM] at h
        cases h
        simp [evalList_length ds d es s1 vs' _ h2]

theorem map_unnamed (args : List Spec.Expr) (ha : ∀ a ∈ args, Spec.isNamed a = false) :
    args.map Spec.unnamed = args := by
  induction args with
  | nil => rfl
  | cons a as ih =>
    rw [List.map_cons, ih (fun b hb => ha b (by simp [hb]))]
    have := ha a (by simp)
    cases a <;> simp_all [Spec.isNamed, Spec.unnamed]

theorem byNameOk_named (ds : Spec.Decls) (l : Spec.Env) (t : String) (a : Spec.Expr) (c : Spec.Core) :
    (Spec.byNameOk ds l (some (.name t)) a).run.run c = (.ok true, c) := by
  unfold Spec.byNameOk
  rw [Spec.run_get_bind]
  cases a <;> rfl

/-- **Arguments by position, one per parameter**, of parameters of named types: galaspec
    binds them as it evaluates them, left to right. -/
theorem bindArgs_pos (ds : Spec.Decls) (d : Nat) (names : List String) (tys : List (Option Spec.Ty))
    (dflts : List (Option Spec.Expr)) (args : List Spec.Expr) (ha : ∀ a ∈ args, Spec.isNamed a = false)
    (hl : args.length = names.length) (htys : ∀ j < names.length, ∃ t, tys.getD j none = some (.name t)) (s : Spec.St) :
    (bindArgs ds d names tys dflts false args).run.run s = (evalList ds d args).run.run s := by
  rw [Spec.bindArgs.eq_def]
  simp only [argSlots_pos names args ha hl, Spec.run_bind_eq, run_pureM, Spec.run_liftM, Spec.run_getCore]
  rw [forIn_fold _ (fun _ _ => PUnit.unit) (fun a => ∃ t, tys.getD a.2 none = some (.name t)) ?step _ _ _
    (fun a ha => htys a.2 (by have := (List.of_mem_zip ha).2; simp at this; omega))]
  case step =>
    rintro ⟨a, j⟩ u s' ⟨t, ht⟩
    simp only [Spec.run_bind_eq, Spec.run_liftM, ht, byNameOk_named]
    rfl
  simp only [map_unnamed args ha]
  rcases he : (evalList ds d args).run.run _ with ⟨r, t⟩
  cases r with
  | error => rfl
  | ok vs =>
    have hvl := evalList_length ds d args _ vs t he
    simp only
    rw [← hvl, bindDefaults_given ds d names tys dflts false vs (by omega)]

theorem checkTy_kind (ds : Spec.Decls) (k : Kind) (c : Spec.Core) :
    (Spec.checkTy ds (.name (kindName k))).run.run c = (.ok (kindName k), c) := by
  cases k <;> rfl

/-- The frame galaspec calls a function in: its parameters bound to the arguments. -/
def specFrame (ps : List Spec.Param) (vs : List Spec.Val) : Spec.Env :=
  (ps.zip vs).foldl (fun ρ (p, v) => Spec.bind ρ p.name ⟨v, false⟩) []

/-- **A call of a function whose parameters and result are of the kinds**, on as many
    arguments as parameters: what its body gives, run in the frame of its parameters,
    unless that is `unit` (which a body of the result's kind never gives). -/
theorem callFunc_run (ds : Spec.Decls) (d : Nat) (F : Spec.Func) (vs : List Spec.Val) (s : Spec.St)
    (hp : ∀ p ∈ F.params, ∃ k, p.type = some (.name (kindName k))) (ρ : Kind)
    (hr : F.result = some (.name (kindName ρ))) (hl : vs.length = F.params.length)
    (r : Except Spec.Stop Spec.Val) (t : Spec.St)
    (hrun : (runIn ds d (specFrame F.params vs) F.body).run.run s = (r, t))
    (hu : r ≠ .ok .unit) :
    (callFunc ds d F vs).run.run s = (r, t) := by
  rw [Spec.callFunc.eq_def]
  simp only [hl, bne_self_eq_false, Bool.false_eq_true, ite_false]
  rw [Spec.run_bind_eq, forIn_fold _ (fun _ _ => PUnit.unit) (fun p : Spec.Param => ∃ k, p.type = some (.name (kindName k)))
    ?step _ _ _ hp]
  case step =>
    rintro p u s' ⟨k, hk⟩
    simp only [Spec.run_bind_eq, run_discard, Spec.run_liftM, hk, Spec.checkOptTy, checkTy_kind]
    rfl
  simp only [hr, Spec.run_bind_eq, run_discard, Spec.run_liftM, checkTy_kind]
  unfold specFrame at hrun
  simp only [hrun]
  rcases r with e | v
  · rfl
  · cases v <;> first | exact absurd rfl hu | rfl

/-- galaspec's call of a name that resolves to a function `F`: one step, then `F`'s
    arguments bound, then the call. -/
theorem eval_call_run (ds : Spec.Decls) (d k : Nat) (f : String) (args : List Spec.Expr) (F : Spec.Func)
    (s : Spec.St) (hs : s.steps = k + 1) (hres : Spec.resolve ds s.locals s.globals f = .func F) :
    (eval ds (d + 1) (.call (.name f) args)).run.run s =
      (do callFunc ds d F (← funcArgs ds d F args)).run.run { s with steps := k } := by
  rw [eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq, tick_succ s k hs]
  simp only [Spec.run_liftM, Spec.run_getCore]
  rw [show s.globals = ({ s with steps := k } : Spec.St).globals from rfl,
    show s.locals = ({ s with steps := k } : Spec.St).locals from rfl] at hres
  simp only [hres]
  rw [Spec.run_bind_eq]

theorem eval_ret_run (ds : Spec.Decls) (d k : Nat) (x : Spec.Expr) (s : Spec.St) (hs : s.steps = k + 1) :
    (eval ds (d + 1) (.ret (some x))).run.run s = match (eval ds d x).run.run { s with steps := k } with
      | (.ok v, t) => (.error (.ret v), t)
      | (.error e, t) => (.error e, t) := by
  rw [eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq, tick_succ s k hs]
  rcases (eval ds d x).run.run { s with steps := k } with ⟨r, t⟩
  cases r <;> rfl

end SpecCall

theorem execStmt_ret (c : Ctx) (n : Nat) (env : Env) (le : Expr) (g : St) :
    (execStmt c (n + 3) env (.ret [le])).run.run g = match (evalExpr c (n + 1) env le).run.run g with
      | (.ok w, g') => (.error (.ret w.results), g')
      | (.error e, g') => (.error e, g') := by
  rw [execStmt]
  simp only [evalArgs, grun_bind]
  rcases (evalExpr c (n + 1) env le).run.run g with ⟨r, g'⟩
  cases r <;> rfl

/-! ## The lowering of calls and `return` -/

/-- The kind of a type the lowering accepts: `int`, `bool` or `string`. -/
def tyKindS : Spec.Ty → Option Kind
  | .name "int" => some .int
  | .name "bool" => some .bool
  | .name "string" => some .str
  | _ => none

theorem tyKindS_some {t : Spec.Ty} {k : Kind} (h : tyKindS t = some k) : t = .name (kindName k) := by
  unfold tyKindS at h; split at h <;> cases h <;> rfl

/-- The functions the lowering calls: each one's parameters' kinds and its result's. -/
abbrev Sigs := String → Option (List Kind × Kind)

/-- A function's parameters and result, when each is of a kind. -/
def sigOf (F : Spec.Func) : Option (List (String × Kind) × Kind) := do
  let xks ← F.params.mapM fun p => do pure (p.name, ← tyKindS (← p.type))
  let ρ ← tyKindS (← F.result)
  pure (xks, ρ)

/-- A call somewhere in an operator tree. -/
def hasCall : Spec.Expr → Bool
  | .call .. => true
  | .paren e => hasCall e
  | .unary _ e => hasCall e
  | .binary _ l r => hasCall l || hasCall r
  | _ => false

/-- A call-free expression, at most `K` deep in GoCore: step 2a's lowering. -/
def lowerLeaf (K : Nat) (κ : String → Option Kind) (e : Spec.Expr) : Option (Kind × Expr) :=
  if gdepth e ≤ K then lowerX κ e else none

mutual
/-- **The lowering of an expression with calls**: a call-free part is step 2a's (at most `K`
    deep in GoCore); an operator over a call, the same operator; `f(args)` of a function the
    lowering calls (`sg`), not shadowed by a local, with its arguments by position, one for
    each parameter and of its kind, Go's call `f(args)`. No named argument and no default:
    a call that leaves a parameter out or names one is not lowered. -/
def lowerC (K : Nat) (sg : Sigs) (κ : String → Option Kind) : Spec.Expr → Option (Kind × Expr)
  | .paren e => if hasCall e then (lowerC K sg κ e).map fun (k, l) => (k, .paren l) else lowerLeaf K κ (.paren e)
  | .unary op e =>
    if hasCall e then do
      let (k, l) ← lowerC K sg κ e
      let k' ← unaryKind op k
      pure (k', .unary op l)
    else lowerLeaf K κ (.unary op e)
  | .binary op l r =>
    if hasCall l || hasCall r then do
      let (kl, ll) ← lowerC K sg κ l
      let (kr, lr) ← lowerC K sg κ r
      let k ← binaryKind op kl kr
      if isShift op && (isLitE l || negLitE r) then none else pure (k, .binary op ll lr)
    else lowerLeaf K κ (.binary op l r)
  | .call (.name f) args =>
    if f = "_" ∨ (κ f).isSome then none else do
      let (ks, k) ← sg f
      let las ← lowerArgs K sg κ ks args
      pure (k, .call (.ident f) las false)
  | .lit l => lowerLeaf K κ (.lit l)
  | .name x => lowerLeaf K κ (.name x)
  | _ => none
/-- A call's arguments, by position, each of its parameter's kind. -/
def lowerArgs (K : Nat) (sg : Sigs) (κ : String → Option Kind) : List Kind → List Spec.Expr → Option (List Expr)
  | [], [] => some []
  | k :: ks, a :: as =>
    if Spec.isNamed a then none else
    match lowerC K sg κ a with
    | some (k', l) => if k' = k then (lowerArgs K sg κ ks as).map (l :: ·) else none
    | none => none
  | _, _ => none
end

mutual
/-- **The lowering of a statement**, step 2b's shapes with calls in their expressions, and
    `return e` of the result's kind `ρ`. -/
def lowerS3 (K : Nat) (sg : Sigs) (ρ : Kind) (κ : String → Option Kind) : Spec.Expr →
    Option ((String → Option Kind) × Stmt)
  | .decl _ [x] false none [e] =>
    if x = "_" ∨ x = "true" ∨ x = "false" then none
    else (lowerC K sg κ e).map fun (k, le) => (upd κ x k, .decl (.value false [.mk [x] none [le]]))
  | .block ss => (lowerSs3 K sg ρ κ ss).map fun (_, l) => (κ, .block l)
  | .ifStmt c (.block a) els =>
    match lowerC K sg κ c, lowerSs3 K sg ρ κ a, lowerElse3 K sg ρ κ els with
    | some (.bool, lc), some (_, la), some le => some (κ, .if_ none lc la le)
    | _, _, _ => none
  | .ret (some e) =>
    match lowerC K sg κ e with
    | some (k, le) => if k = ρ then some (κ, .ret [le]) else none
    | none => none
  | _ => none
def lowerElse3 (K : Nat) (sg : Sigs) (ρ : Kind) (κ : String → Option Kind) :
    Option Spec.Expr → Option (Option Stmt)
  | none => some none
  | some (.block ss) => (lowerSs3 K sg ρ κ ss).map fun (_, l) => some (.block l)
  | some (.ifStmt c a els) => (lowerS3 K sg ρ κ (.ifStmt c a els)).map fun (_, l) => some l
  | some _ => none
def lowerSs3 (K : Nat) (sg : Sigs) (ρ : Kind) (κ : String → Option Kind) : List Spec.Expr →
    Option ((String → Option Kind) × List Stmt)
  | [] => some (κ, [])
  | s :: ss => do
    let (κ₁, l) ← lowerS3 K sg ρ κ s
    let (κ₂, ls) ← lowerSs3 K sg ρ κ₁ ss
    pure (κ₂, l :: ls)
end

/-- The last element of a function's block body: an expression of the result's kind, or
    `return` of one; Go's `return`. -/
def lowerLast (K : Nat) (sg : Sigs) (ρ : Kind) (κ : String → Option Kind) (e : Spec.Expr) : Option Expr :=
  match e with
  | .ret (some x) => match lowerC K sg κ x with
    | some (k, le) => if k = ρ then some le else none
    | none => none
  | x => match lowerC K sg κ x with
    | some (k, le) => if k = ρ then some le else none
    | none => none

/-- A block body: statements, then the last element, returned. -/
def lowerTail (K : Nat) (sg : Sigs) (ρ : Kind) (κ : String → Option Kind) : List Spec.Expr → Option (List Stmt)
  | [] => none
  | [e] => (lowerLast K sg ρ κ e).map fun le => [.ret [le]]
  | s :: s' :: ss => do
    let (κ₁, l) ← lowerS3 K sg ρ κ s
    let ls ← lowerTail K sg ρ κ₁ (s' :: ss)
    pure (l :: ls)

/-- The parameters' kinds, as the body sees them. -/
def paramKinds (xks : List (String × Kind)) : String → Option Kind :=
  xks.foldl (fun κ xk => upd κ xk.1 xk.2) fun _ => none

/-- Parameter names Go accepts and that bind: distinct, none `_`, `true` or `false`. -/
def ParamsOK (names : List String) : Prop := names.Nodup ∧ ∀ x ∈ names, x ≠ "_" ∧ x ≠ "true" ∧ x ≠ "false"

instance (names : List String) : Decidable (ParamsOK names) := by unfold ParamsOK; infer_instance

/-- **The lowering of a function's body**: parameters of distinct names (none `_`, `true` or
    `false`), each of a kind, and a result of one; the body `= e` is Go's `return e`, a
    block body its statements and then `return` of its last element. -/
def lowerBody (K : Nat) (sg : Sigs) (F : Spec.Func) : Option (List Stmt) :=
  match sigOf F with
  | none => none
  | some (xks, ρ) =>
    if ParamsOK (xks.map (·.1)) then
      match F.body with
      | .block ss => lowerTail K sg ρ (paramKinds xks) ss
      | e => (lowerLast K sg ρ (paramKinds xks) e).map fun le => [.ret [le]]
    else none

/-! ## The relations, with calls -/

/-- **GoCore's state after code ran**: cells (and their types) only added to; every other
    part (output, deferred calls, maps, ...) as it was, but the steps. -/
def GFrame (g g' : St) : Prop :=
  Pre g.cells g'.cells ∧ { g' with cells := g.cells, types := g.types, steps := g.steps } = g

/-- **Steps**: GoCore spends no more than galaspec. -/
def Spent (s s' : Spec.St) (g g' : St) : Prop := g.steps + s'.steps ≤ g'.steps + s.steps

/-- **galaspec's state after an expression**: the heap only added to; locals, package-level
    variables and output as they were. -/
def SFrame (c c' : Spec.Core) : Prop := Pre c.heap c'.heap ∧ { c' with heap := c.heap } = c

/-- **The state relation with calls**: step 2b's (`Inv`), and: the code runs in package `P`
    with no type parameters; every local galaspec binds is one the lowering knows of, and so
    is every GoCore local (so a name the lowering calls is no local); and no package-level
    variable of galaspec is named as a function the lowering calls. -/
structure Inv3 (cx : Ctx) (P : String) (sg : Sigs) (κ : String → Option Kind) (c : Spec.Core) (g : St)
    (env : Env) : Prop where
  inv : Inv cx κ c g env
  pkg : env.pkg = P
  tenv : env.tenv = []
  closed : ∀ x, κ x = none → Spec.lookup c.locals x = none ∧ env.lookup x = none
  globals : ∀ f, (sg f).isSome → Spec.lookup c.globals f = none

/-- galaspec's panic and GoCore's agree, with equal outputs; GoCore's state framed. -/
def PanOK (s : Spec.St) (g : St) (pc : Spec.PanicClass) (m : String) (s' : Spec.St) :
    Except Stop α × St → Prop
  | r => ∃ pv g', r = (.error (.panic pv true), g') ∧ PanicRel pc m pv ∧ s'.out = s.out ∧ GFrame g g'

/-- A value `v` of kind `ρ` returned: GoCore returns a related one. -/
def RetOK (ρ : Kind) (s : Spec.St) (g : St) (v : Spec.Val) (s' : Spec.St) : Except Stop α × St → Prop
  | r => ∃ w g', r = (.error (.ret [w]), g') ∧ VRel v w ∧ valKind v = some ρ ∧ Pre s.heap s'.heap ∧
      s'.globals = s.globals ∧ s'.out = s.out ∧ GFrame g g' ∧ Spent s s' g g'

/-- **An expression's results agree**: a related value of kind `k` (not an untyped integer
    unless `u`), with both states framed and GoCore spending no more steps; or the same panic.
    An expression never ends with a `return` (`False`: proved, not assumed). Nothing is
    claimed when galaspec runs out of fuel or stops `stuck` or `unsupported`. -/
def ERes3 (k : Kind) (u : Bool) (s : Spec.St) (g : St) :
    Except Spec.Stop Spec.Val × Spec.St → Except Stop Val × St → Prop
  | (.ok v, s'), r => ∃ w g', r = (.ok w, g') ∧ VRel v w ∧ valKind v = some k ∧ (u = false → ¬ UInt w) ∧
      SFrame s.toCore s'.toCore ∧ GFrame g g' ∧ Spent s s' g g'
  | (.error (.panic pc m), s'), r => PanOK s g pc m s' r
  | (.error (.ret _), _), _ => False
  | _, _ => True

/-- Arguments of kinds `ks`: related values, one each. -/
def KRel : List Kind → List Spec.Val → List Val → Prop
  | [], [], [] => True
  | k :: ks, v :: vs, w :: ws => VRel v w ∧ valKind v = some k ∧ KRel ks vs ws
  | _, _, _ => False

/-- A call's arguments' results agree. -/
def ARes3 (ks : List Kind) (s : Spec.St) (g : St) :
    Except Spec.Stop (List Spec.Val) × Spec.St → Except Stop (List Val) × St → Prop
  | (.ok vs, s'), r => ∃ ws g', r = (.ok ws, g') ∧ KRel ks vs ws ∧ SFrame s.toCore s'.toCore ∧
      GFrame g g' ∧ Spent s s' g g'
  | (.error (.panic pc m), s'), r => PanOK s g pc m s' r
  | (.error (.ret _), _), _ => False
  | _, _ => True

/-- **A statement list's results agree**: ending normally, related states with the kinds
    `κ'` the list declared (galaspec's locals only extended); a panic, the same panic; a
    `return` of a value of the result's kind `ρ`, the same `return`. -/
def ResL3 (cx : Ctx) (P : String) (sg : Sigs) (ρ : Kind) (κ' : String → Option Kind) (s : Spec.St) (g : St) :
    Except Spec.Stop Spec.Val × Spec.St → Except Stop Env × St → Prop
  | (.ok _, s'), r => ∃ env' g', r = (.ok env', g') ∧ Inv3 cx P sg κ' s'.toCore g' env' ∧
      Pre s.heap s'.heap ∧ s'.globals = s.globals ∧ (∃ pre, s'.locals = pre ++ s.locals) ∧
      GFrame g g' ∧ Spent s s' g g'
  | (.error (.panic pc m), s'), r => PanOK s g pc m s' r
  | (.error (.ret v), s'), r => RetOK ρ s g v s' r
  | _, _ => True

/-- The same for a block or an `if`, which leaves galaspec's locals as they were. -/
def ResU3 (ρ : Kind) (Q : α → Prop) (s : Spec.St) (g : St) :
    Except Spec.Stop Spec.Val × Spec.St → Except Stop α × St → Prop
  | (.ok _, s'), r => ∃ a g', r = (.ok a, g') ∧ Q a ∧ SFrame s.toCore s'.toCore ∧ GFrame g g' ∧ Spent s s' g g'
  | (.error (.panic pc m), s'), r => PanOK s g pc m s' r
  | (.error (.ret v), s'), r => RetOK ρ s g v s' r
  | _, _ => True

/-- **A function body's results agree**: the value galaspec's body gives or returns, of
    the result's kind, is the value GoCore's `return`s; or the same panic. -/
def ResB (ρ : Kind) (s : Spec.St) (g : St) : Except Spec.Stop Spec.Val × Spec.St → Except Stop Env × St → Prop
  | (.ok v, s'), r => RetOK ρ s g v s' r
  | (.error (.ret v), s'), r => RetOK ρ s g v s' r
  | (.error (.panic pc m), s'), r => PanOK s g pc m s' r
  | _, _ => True

/-- **The functions the lowering calls are GoCore's**: package `P` of `cx` (not the
    standard library) declares no `true`, `false`, `int`, `bool` or `string`, and declares
    each function `f` with `sg f = some sig` as the lowering of galaspec's `f`, of signature
    `sig`, with the lowered signature and body. -/
structure FunRel (K : Nat) (sg : Sigs) (ds : Spec.Decls) (cx : Ctx) (P : String) : Prop where
  notStd : P ≠ stdPath
  pkg : ∃ p, cx.pkgs.get? P = some p ∧ (∀ x ∈ ["true", "false", "int", "bool", "string"], p.objs.get? x = none) ∧
    ∀ f sig, sg f = some sig → ∃ F xks ρ lb fi, ds.funcs.lookup f = some F ∧ sigOf F = some (xks, ρ) ∧
      sig = (xks.map (·.2), ρ) ∧ lowerBody K sg F = some lb ∧
      p.objs.get? f = some (.func fi [] (lowerSigOf xks ρ) (some lb))

/-! ## galaspec's steps never grow -/

section Mono
open Spec (eval)

/-- A step that spends steps, never adds any. -/
def Mono (x : Spec.M α) : Prop := ∀ s, (x.run.run s).2.steps ≤ s.steps

theorem mono_pure (a : α) : Mono (pure a : Spec.M α) := fun _ => Nat.le_refl _
theorem mono_throw (e : Spec.Stop) : Mono (throw e : Spec.M α) := fun _ => Nat.le_refl _
theorem mono_liftM (x : Spec.H α) : Mono (liftM x : Spec.M α) := fun s => by rw [Spec.run_liftM]; exact Nat.le_refl _
theorem mono_tick : Mono Spec.tick := fun s => by
  rcases s with ⟨c, n⟩; cases n <;> simp [tick_run]
theorem mono_bind {x : Spec.M α} {f : α → Spec.M β} (hx : Mono x) (hf : ∀ a, Mono (f a)) :
    Mono (x >>= f) := fun s => by
  rw [Spec.run_bind_eq]
  rcases h : x.run.run s with ⟨r, t⟩
  have := hx s; rw [h] at this
  cases r with
  | error => exact this
  | ok a => exact Nat.le_trans (hf a t) this

/-- The shapes of step 2a's expressions. -/
def CFShape : Spec.Expr → Prop
  | .lit _ | .name _ => True
  | .paren e | .unary _ e => CFShape e
  | .binary _ l r => CFShape l ∧ CFShape r
  | _ => False

set_option hygiene false in
macro "mono_step" : tactic => `(tactic| (first
  | exact mono_pure _
  | exact mono_throw _
  | exact mono_liftM _
  | exact mono_tick
  | exact ihl
  | exact ihr
  | exact ih
  | (apply mono_bind <;> intros)
  | split))

theorem eval_mono (ds : Spec.Decls) : ∀ (e : Spec.Expr) (d : Nat), CFShape e → Mono (eval ds d e)
  | _, 0, _ => by rw [eval.eq_def]; exact mono_throw _
  | .lit l, d + 1, _ => by
    rw [eval.eq_def]
    refine mono_bind mono_tick fun _ => ?_
    cases l <;> first | exact mono_liftM _ | exact mono_pure _
  | .name x, d + 1, _ => by
    rw [eval.eq_def]
    refine mono_bind mono_tick fun _ => ?_
    dsimp only
    refine mono_bind (mono_liftM _) fun _ => ?_
    split <;> (try split) <;> first | exact mono_liftM _ | exact mono_pure _
  | .paren x, d + 1, h => by
    have ih := eval_mono ds x d h
    rw [eval.eq_def]; exact mono_bind mono_tick fun _ => ih
  | .unary op x, d + 1, h => by
    have ih := eval_mono ds x d h
    rw [eval.eq_def]
    refine mono_bind mono_tick fun _ => ?_
    dsimp only
    split
    all_goals first
      | exact mono_liftM _
      | exact mono_bind ih fun _ => mono_liftM _
      | (exfalso; simp_all; done)
      | (rename_i hh; obtain ⟨rfl, rfl⟩ := hh; exact mono_bind ih fun _ => mono_liftM _)
  | .binary op l r, d + 1, h => by
    have ihl := eval_mono ds l d h.1
    have ihr := eval_mono ds r d h.2
    rw [eval.eq_def]
    refine mono_bind mono_tick fun _ => ?_
    dsimp only
    split
    case h_11 =>
      rename_i hh; simp only [Spec.Expr.binary.injEq] at hh; obtain ⟨rfl, rfl, rfl⟩ := hh
      exact mono_bind ihl fun _ => mono_bind (mono_liftM _) fun b => by
        split
        · exact mono_bind ihr fun _ => mono_bind (mono_liftM _) fun _ => mono_pure _
        · exact mono_pure _
    case h_12 =>
      rename_i hh; simp only [Spec.Expr.binary.injEq] at hh; obtain ⟨rfl, rfl, rfl⟩ := hh
      exact mono_bind ihl fun _ => mono_bind (mono_liftM _) fun b => by
        split
        · exact mono_pure _
        · exact mono_bind ihr fun _ => mono_bind (mono_liftM _) fun _ => mono_pure _
    case h_13 =>
      rename_i hh; simp only [Spec.Expr.binary.injEq] at hh; obtain ⟨rfl, rfl, rfl⟩ := hh
      exact mono_bind ihl fun _ => mono_bind ihr fun _ => mono_liftM _
    all_goals (exfalso; simp_all)

end Mono

/-! ## A call-free expression never returns -/

section NoRet
open Spec (H)

/-- A step-free computation that never ends with a `return`. -/
def NoRet (x : H α) : Prop := ∀ c v, (x.run.run c).1 ≠ .error (.ret v)

theorem noRet_pure (a : α) : NoRet (pure a : H α) := fun _ _ h => nomatch h
theorem noRet_bind {x : H α} {f : α → H β} (hx : NoRet x) (hf : ∀ a, NoRet (f a)) : NoRet (x >>= f) := by
  intro c v
  rw [Spec.run_bind_eq]
  rcases h : x.run.run c with ⟨r, t⟩
  cases r with
  | error e => intro he; cases he; exact hx c v (by rw [h])
  | ok a => exact hf a t v

theorem noRet_unop (op : String) (v : Spec.Val) : NoRet (Spec.unop op v) := by
  intro c w; unfold Spec.unop; split <;> exact fun h => nomatch h
theorem noRet_intLit (n : Int) : NoRet (Spec.intLit n) := by
  intro c w; unfold Spec.intLit; split <;> exact fun h => nomatch h
theorem noRet_binop (op : String) (a b : Spec.Val) : NoRet (Spec.binop op a b) := by
  intro c w; unfold Spec.binop
  split <;> (try split) <;> (try split) <;> exact fun h => nomatch h
theorem noRet_asBool (v : Spec.Val) : NoRet (Spec.asBool v) := by
  intro c w; unfold Spec.asBool; split <;> exact fun h => nomatch h
theorem noRet_deref (v : Spec.Val) : NoRet (Spec.deref v) := by
  intro c w; unfold Spec.deref; split
  · rw [Spec.run_get_bind]; split <;> exact fun h => nomatch h
  · exact fun h => nomatch h

theorem pureEval_noRet (ds : Spec.Decls) : ∀ e, NoRet (pureEval ds e)
  | .lit (.int n) => noRet_intLit n
  | .lit (.bool _) | .lit (.str _) => noRet_pure _
  | .paren x => pureEval_noRet ds x
  | .name x => by
    intro c w
    unfold pureEval Spec.getCore
    rw [Spec.run_get_bind]
    split
    · exact noRet_deref _ c w
    · exact fun h => nomatch h
  | .unary op x => by
    by_cases hneg : ∃ n, op = "-" ∧ x = .lit (.int n)
    · obtain ⟨n, rfl, rfl⟩ := hneg; exact noRet_intLit _
    · rw [pureEval_unary ds op x (fun n h => hneg ⟨n, h⟩)]
      exact noRet_bind (pureEval_noRet ds x) fun _ => noRet_unop _ _
  | .binary op l r => by
    by_cases hand : op = "&&"
    · subst hand
      simp only [pureEval]
      exact noRet_bind (pureEval_noRet ds l) fun _ => noRet_bind (noRet_asBool _) fun b => by
        split
        · exact noRet_bind (pureEval_noRet ds r) fun _ => noRet_bind (noRet_asBool _) fun _ => noRet_pure _
        · exact noRet_pure _
    by_cases hor : op = "||"
    · subst hor
      simp only [pureEval]
      exact noRet_bind (pureEval_noRet ds l) fun _ => noRet_bind (noRet_asBool _) fun b => by
        split
        · exact noRet_pure _
        · exact noRet_bind (pureEval_noRet ds r) fun _ => noRet_bind (noRet_asBool _) fun _ => noRet_pure _
    rw [pureEval_binary ds op l r hand hor]
    exact noRet_bind (pureEval_noRet ds l) fun _ => noRet_bind (pureEval_noRet ds r) fun _ => noRet_binop _ _ _
  | .call .. | .select .. | .tuple _ | .ifExpr .. | .mtch .. | .block _ | .ifStmt .. | .ret _
  | .assign .. | .incdec .. | .decl .. | .interp _ | .lambda .. | .named .. | .unsupported _ => by
    intro c w; unfold pureEval; exact fun h => nomatch h

end NoRet

/-! ## Frames -/

section Frames
open Gala.GoCore.Full

theorem gframe_refl (g : St) : GFrame g g := ⟨pre_refl _, by cases g; rfl⟩

theorem gframe_trans {g₁ g₂ g₃ : St} (h₁ : GFrame g₁ g₂) (h₂ : GFrame g₂ g₃) : GFrame g₁ g₃ := by
  refine ⟨pre_trans h₁.1 h₂.1, ?_⟩
  have e₁ := h₁.2; have e₂ := h₂.2
  cases g₁; cases g₂; cases g₃
  simp only [St.mk.injEq] at e₁ e₂ ⊢
  obtain ⟨-, -, a1, a2, -, a3, a4, a5, a6, a7⟩ := e₁
  obtain ⟨-, -, b1, b2, -, b3, b4, b5, b6, b7⟩ := e₂
  subst_vars
  simp

theorem gframe_out {g g' : St} (h : GFrame g g') : g'.out = g.out := by
  have e := h.2; cases g; cases g'; simp only [St.mk.injEq] at e; exact e.2.2.2.1

theorem gframe_defers {g g' : St} (h : GFrame g g') : g'.defers = g.defers := by
  have e := h.2; cases g; cases g'; simp only [St.mk.injEq] at e; exact e.2.2.2.2.2.2.2.1

theorem spent_trans {s₁ s₂ s₃ : Spec.St} {g₁ g₂ g₃ : St} (h₁ : Spent s₁ s₂ g₁ g₂) (h₂ : Spent s₂ s₃ g₂ g₃) :
    Spent s₁ s₃ g₁ g₃ := by unfold Spent at *; omega

theorem spent_le {s s' : Spec.St} {g g' : St} (h : Spent s s' g g') (hle : s.steps ≤ g.steps) :
    s'.steps ≤ g'.steps := by unfold Spent at h; omega

theorem sframe_refl (c : Spec.Core) : SFrame c c := ⟨pre_refl _, by cases c; rfl⟩

theorem sframe_trans {c₁ c₂ c₃ : Spec.Core} (h₁ : SFrame c₁ c₂) (h₂ : SFrame c₂ c₃) : SFrame c₁ c₃ := by
  refine ⟨pre_trans h₁.1 h₂.1, ?_⟩
  have e₁ := h₁.2; have e₂ := h₂.2
  cases c₁; cases c₂; cases c₃
  simp only [Spec.Core.mk.injEq] at e₁ e₂ ⊢
  obtain ⟨a1, a2, a3, -⟩ := e₁
  obtain ⟨b1, b2, b3, -⟩ := e₂
  subst_vars
  simp

theorem sframe_parts {c c' : Spec.Core} (h : SFrame c c') :
    c'.locals = c.locals ∧ c'.globals = c.globals ∧ c'.out = c.out := by
  have e := h.2; cases c; cases c'; simp only [Spec.Core.mk.injEq] at e; exact ⟨e.1, e.2.1, e.2.2.1⟩

/-- Code that only added cells, on either side, keeps the state relation. -/
theorem inv3_frame {cx : Ctx} {P : String} {sg : Sigs} {κ : String → Option Kind} {c c' : Spec.Core}
    {g g' : St} {env : Env} (hi : Inv3 cx P sg κ c g env) (hs : SFrame c c') (hg : GFrame g g') :
    Inv3 cx P sg κ c' g' env := by
  obtain ⟨hl, hgl, ho⟩ := sframe_parts hs
  refine ⟨⟨locRel_mono hi.inv.loc hs.1 hl hg.1, ?_, hi.inv.pre⟩, hi.pkg, hi.tenv, ?_, ?_⟩
  · rw [ho, gframe_out hg]; exact hi.inv.out
  · intro x hx; rw [hl]; exact hi.closed x hx
  · intro f hf; rw [hgl]; exact hi.globals f hf

end Frames

/-! ## The call-free parts: step 2a -/

section Leaf
open Gala.GoCore.Full

theorem lowerE_shape (κ : String → Option Kind) : ∀ (e : Spec.Expr) (k : Kind) (le : Expr),
    lowerE κ e = some (k, le) → CFShape e
  | .lit _, _, _, _ | .name _, _, _, _ => trivial
  | .paren x, k, le, h => by
    simp only [lowerE, Option.map_eq_some_iff] at h
    obtain ⟨⟨k', l'⟩, h', -⟩ := h
    exact lowerE_shape κ x k' l' h'
  | .unary op x, k, le, h => by
    simp only [lowerE, Option.bind_eq_bind, Option.bind_eq_some_iff] at h
    obtain ⟨⟨k', l'⟩, h', -⟩ := h
    exact lowerE_shape κ x k' l' h'
  | .binary op l r, k, le, h => by
    simp only [lowerE, Option.bind_eq_bind, Option.bind_eq_some_iff] at h
    obtain ⟨⟨kl, ll⟩, hl, ⟨kr, lr⟩, hr, -⟩ := h
    exact ⟨lowerE_shape κ l kl ll hl, lowerE_shape κ r kr lr hr⟩

/-- **A call-free part**, step 2a's: as `expr_correct`, with both states framed, GoCore
    spending no step, and no `return`. -/
theorem leaf_sim (ds : Spec.Decls) (cx : Ctx) (P : String) (sg : Sigs) (K : Nat) (κ : String → Option Kind)
    (e : Spec.Expr) (k : Kind) (le : Expr) (hl : lowerLeaf K κ e = some (k, le)) (s : Spec.St) (g : St)
    (env : Env) (hi : Inv3 cx P sg κ s.toCore g env) (d N : Nat) (hN : K ≤ N) :
    ERes3 k (isLitE e) s g ((Spec.eval ds d e).run.run s) ((evalExpr cx N env le).run.run g) := by
  unfold lowerLeaf at hl
  split at hl
  · rename_i hK
    obtain ⟨hnc, hle⟩ := lowerX_some hl
    obtain ⟨hcore, hrel⟩ := expr_correct ds cx κ e k le hle hnc s g env hi.inv.loc hi.inv.pre d
    have hrel := hrel N (by omega)
    have hmono := eval_mono ds e d (lowerE_shape κ e k le hle) s
    have hpure := (eval_pure ds e d s (pure_of_lower κ s.toCore g.cells env hi.inv.loc e k le hle)).1
    rcases hr : (Spec.eval ds d e).run.run s with ⟨r, s'⟩
    rw [hr] at hrel hcore hmono hpure
    simp only at hcore hmono hpure
    cases r with
    | ok v =>
      obtain ⟨w, hg, hvw, hk, hu⟩ := hrel
      exact ⟨w, g, hg, hvw, hk, hu, by rw [hcore]; exact sframe_refl _, gframe_refl g,
        by unfold Spent; omega⟩
    | error er =>
      cases er with
      | panic pc m =>
        obtain ⟨pv, hg, hpv⟩ := hrel
        exact ⟨pv, g, hg, hpv, congrArg Spec.Core.out hcore, gframe_refl g⟩
      | ret v =>
        rcases hpure with h | h
        · cases h
        · exact pureEval_noRet ds e s.toCore v h.symm
      | _ => trivial
  · cases hl

end Leaf

/-! ## The simulation -/

section Sim
open Gala.GoCore.Full

/-- galaspec's state after its `tick`: what the program sees is the same, a step fewer. -/
structure Ticked (s₁ s : Spec.St) : Prop where
  core : s₁.toCore = s.toCore
  le : s₁.steps ≤ s.steps

theorem ticked_of (s : Spec.St) (k : Nat) (hs : s.steps = k + 1) : Ticked { s with steps := k } s :=
  ⟨rfl, by simp [hs]⟩

theorem panOK_tick {s₁ s : Spec.St} (ht : Ticked s₁ s) {g : St} {pc m s'} {r : Except Stop α × St}
    (h : PanOK s₁ g pc m s' r) : PanOK s g pc m s' r := by
  obtain ⟨pv, g', hr, hp, ho, hg⟩ := h
  exact ⟨pv, g', hr, hp, by rw [ho, show s₁.out = s.out from congrArg Spec.Core.out ht.core], hg⟩

theorem retOK_tick {s₁ s : Spec.St} (ht : Ticked s₁ s) {ρ g v s'} {r : Except Stop α × St}
    (h : RetOK ρ s₁ g v s' r) : RetOK ρ s g v s' r := by
  obtain ⟨w, g', hr, hv, hk, hh, hgl, ho, hg, hsp⟩ := h
  have hc := ht.core
  refine ⟨w, g', hr, hv, hk, ?_, ?_, ?_, hg, ?_⟩
  · rw [show s.heap = s₁.heap from (congrArg Spec.Core.heap hc).symm]; exact hh
  · rw [hgl, show s₁.globals = s.globals from congrArg Spec.Core.globals hc]
  · rw [ho, show s₁.out = s.out from congrArg Spec.Core.out hc]
  · unfold Spent at *; have := ht.le; omega

theorem eRes3_tick {s₁ s : Spec.St} (ht : Ticked s₁ s) {k u g} {sr : Except Spec.Stop Spec.Val × Spec.St}
    {r : Except Stop Val × St} (h : ERes3 k u s₁ g sr r) : ERes3 k u s g sr r := by
  rcases sr with ⟨_ | v, s'⟩
  · rename_i e
    cases e with
    | panic pc m => exact panOK_tick ht h
    | ret => exact h
    | _ => trivial
  · obtain ⟨w, g', hr, hv, hk, hu, hs, hg, hsp⟩ := h
    refine ⟨w, g', hr, hv, hk, hu, ht.core ▸ hs, hg, ?_⟩
    unfold Spent at *; have := ht.le; omega

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs)

/-- The expressions, at galaspec depth `d` and GoCore depth from `K * d` on. -/
def ExprSim (d : Nat) : Prop :=
  ∀ κ e k le, lowerC K sg κ e = some (k, le) → ∀ (s : Spec.St) (g : St) (env : Env),
    Inv3 cx P sg κ s.toCore g env → s.steps ≤ g.steps → ∀ N, K * d ≤ N →
      ERes3 k (isLitE e) s g ((Spec.eval ds d e).run.run s) ((evalExpr cx N env le).run.run g)

/-- What `lowerC` takes apart, for a part with a call. -/
theorem lowerC_paren {κ : String → Option Kind} {x : Spec.Expr} {k : Kind} {le : Expr}
    (h : lowerC K sg κ (.paren x) = some (k, le)) (hc : hasCall x = true) :
    ∃ l, lowerC K sg κ x = some (k, l) ∧ le = .paren l := by
  simp only [lowerC, hc, ite_true, Option.map_eq_some_iff, Prod.mk.injEq] at h
  obtain ⟨⟨k', l⟩, h, rfl, rfl⟩ := h
  exact ⟨l, h, rfl⟩

theorem lowerC_unary {κ : String → Option Kind} {op : String} {x : Spec.Expr} {k : Kind} {le : Expr}
    (h : lowerC K sg κ (.unary op x) = some (k, le)) (hc : hasCall x = true) :
    ∃ kx l, lowerC K sg κ x = some (kx, l) ∧ unaryKind op kx = some k ∧ le = .unary op l := by
  simp only [lowerC, hc, ite_true, Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def,
    Option.some.injEq, Prod.mk.injEq] at h
  obtain ⟨⟨kx, l⟩, hx, k', hk', rfl, rfl⟩ := h
  exact ⟨kx, l, hx, hk', rfl⟩

/-- A part with a call is never a literal, and its value never an untyped constant. -/
theorem isLitE_of_hasCall : ∀ (e : Spec.Expr), hasCall e = true → isLitE e = false
  | .paren x, h => isLitE_of_hasCall x h
  | .lit _, h => by simp [hasCall] at h
  | .name _, _ | .unary .., _ | .binary .., _ | .call .., _ | .select .., _ | .tuple _, _ | .ifExpr .., _
  | .mtch .., _ | .block _, _ | .ifStmt .., _ | .ret _, _ | .assign .., _ | .incdec .., _ | .decl .., _
  | .interp _, _ | .lambda .., _ | .named .., _ | .unsupported _, _ => rfl

theorem eval_paren_run (d k : Nat) (x : Spec.Expr) (s : Spec.St) (hs : s.steps = k + 1) :
    (Spec.eval ds (d + 1) (.paren x)).run.run s = (Spec.eval ds d x).run.run { s with steps := k } := by
  rw [Spec.eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq, tick_succ s k hs]

/-- galaspec's `unary` on an operand that is no literal. -/
theorem eval_unary_run (d k : Nat) (op : String) (x : Spec.Expr) (s : Spec.St) (hs : s.steps = k + 1)
    (hx : isLitE x = false) :
    (Spec.eval ds (d + 1) (.unary op x)).run.run s =
      (do Spec.unop op (← Spec.eval ds d x)).run.run { s with steps := k } := by
  rw [Spec.eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq, tick_succ s k hs]
  split
  all_goals first
    | (rename_i heq; obtain ⟨-, rfl⟩ := heq; simp [isLitE] at hx; done)
    | (rename_i heq; obtain ⟨rfl, rfl⟩ := heq; rw [Spec.run_bind_eq]; done)
    | (rename_i heq; simp at heq; done)

theorem paren_sim (d : Nat) (ih : ExprSim ds cx P K sg d) (hK : 1 ≤ K) (κ : String → Option Kind)
    (x : Spec.Expr) (k : Kind) (le : Expr) (hl : lowerC K sg κ (.paren x) = some (k, le))
    (hc : hasCall x = true) (s : Spec.St) (g : St) (env : Env) (hi : Inv3 cx P sg κ s.toCore g env)
    (hsg : s.steps ≤ g.steps) (N : Nat) (hN : K * (d + 1) ≤ N) :
    ERes3 k (isLitE (.paren x)) s g ((Spec.eval ds (d + 1) (.paren x)).run.run s)
      ((evalExpr cx N env le).run.run g) := by
  obtain ⟨l, hlx, rfl⟩ := lowerC_paren K sg hl hc
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by rw [Nat.mul_succ] at hN; omega⟩
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; trivial
  | succ k' =>
  rw [eval_paren_run ds d k' x s hs, evalExpr_paren]
  exact eRes3_tick (ticked_of s k' hs)
    (ih κ x k l hlx { s with steps := k' } g env hi (by show k' ≤ g.steps; omega) n (by rw [Nat.mul_succ] at hN; omega))

theorem unary_sim3 (d : Nat) (ih : ExprSim ds cx P K sg d) (hK : 3 ≤ K) (κ : String → Option Kind)
    (op : String) (x : Spec.Expr) (k : Kind) (le : Expr) (hl : lowerC K sg κ (.unary op x) = some (k, le))
    (hc : hasCall x = true) (s : Spec.St) (g : St) (env : Env) (hi : Inv3 cx P sg κ s.toCore g env)
    (hsg : s.steps ≤ g.steps) (N : Nat) (hN : K * (d + 1) ≤ N) :
    ERes3 k (isLitE (.unary op x)) s g ((Spec.eval ds (d + 1) (.unary op x)).run.run s)
      ((evalExpr cx N env le).run.run g) := by
  obtain ⟨kx, l, hlx, hku, rfl⟩ := lowerC_unary K sg hl hc
  have hop : op ≠ "&" := by intro h; subst h; unfold unaryKind at hku; split at hku <;> simp_all
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 3 := ⟨N - 3, by rw [Nat.mul_succ] at hN; omega⟩
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; trivial
  | succ k' =>
  rw [eval_unary_run ds d k' op x s hs (isLitE_of_hasCall _ hc), evalExpr_unary _ _ _ _ _ hop]
  have h := ih κ x kx l hlx { s with steps := k' } g env hi (by show k' ≤ g.steps; omega) (n + 2)
    (by rw [Nat.mul_succ] at hN; omega)
  rw [Spec.run_bind_eq, grun_bind]
  rcases hr : (Spec.eval ds d x).run.run { s with steps := k' } with ⟨r, s'⟩
  rw [hr] at h
  cases r with
  | error e =>
    cases e with
    | panic pc m =>
      obtain ⟨pv, g', hg, hp, ho, hgf⟩ := h
      rw [hg]
      exact ⟨pv, g', rfl, hp, by rw [ho], hgf⟩
    | ret => exact h.elim
    | _ => trivial
  | ok v =>
    obtain ⟨w, g', hg, hvw, hkv, hnu, hsf, hgf, hsp⟩ := h
    rw [hg]
    simp only
    obtain ⟨v', w', hv', hw', hvw', hk', hnu'⟩ := unary_sim cx n op v w kx k g' s'.toCore hvw hkv hku
      (hnu (isLitE_of_hasCall _ hc))
    rw [Spec.run_liftM, hv', hw']
    simp only
    have hst : (Spec.unop op v).run.run s'.toCore = (.ok v', s'.toCore) := by
      have := readOnly_unop op v s'.toCore
      rw [← hv']; exact Prod.ext rfl this
    rw [hst]
    exact ⟨w', g', rfl, hvw', hk', fun _ => hnu', hsf, hgf,
      by unfold Spent at *; simp at hsp ⊢; omega⟩

end Sim

section Sim2
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs)

theorem eval_binary_run (d k : Nat) (op : String) (l r : Spec.Expr) (s : Spec.St) (hs : s.steps = k + 1)
    (h1 : op ≠ "&&") (h2 : op ≠ "||") :
    (Spec.eval ds (d + 1) (.binary op l r)).run.run s =
      (do let a ← Spec.eval ds d l; let b ← Spec.eval ds d r; Spec.binop op a b).run.run { s with steps := k } := by
  rw [Spec.eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq, tick_succ s k hs]

theorem eval_and_run (d k : Nat) (l r : Spec.Expr) (s : Spec.St) (hs : s.steps = k + 1) :
    (Spec.eval ds (d + 1) (.binary "&&" l r)).run.run s =
      (do if ← Spec.asBool (← Spec.eval ds d l) then pure (.bool (← Spec.asBool (← Spec.eval ds d r)))
          else pure (.bool false) : Spec.M Spec.Val).run.run { s with steps := k } := by
  rw [Spec.eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq, tick_succ s k hs]

theorem eval_or_run (d k : Nat) (l r : Spec.Expr) (s : Spec.St) (hs : s.steps = k + 1) :
    (Spec.eval ds (d + 1) (.binary "||" l r)).run.run s =
      (do if ← Spec.asBool (← Spec.eval ds d l) then pure (.bool true)
          else pure (.bool (← Spec.asBool (← Spec.eval ds d r))) : Spec.M Spec.Val).run.run { s with steps := k } := by
  rw [Spec.eval.eq_def ds (d + 1) _]
  simp only [Spec.run_bind_eq, tick_succ s k hs]

end Sim2

section Sim3
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs)

theorem lowerC_binary {κ : String → Option Kind} {op : String} {l r : Spec.Expr} {k : Kind} {le : Expr}
    (h : lowerC K sg κ (.binary op l r) = some (k, le)) (hc : (hasCall l || hasCall r) = true) :
    ∃ kl ll kr lr, lowerC K sg κ l = some (kl, ll) ∧ lowerC K sg κ r = some (kr, lr) ∧
      binaryKind op kl kr = some k ∧ (isShift op = true → isLitE l = false) ∧ le = .binary op ll lr := by
  simp only [lowerC, hc, ite_true, Option.bind_eq_bind, Option.bind_eq_some_iff] at h
  obtain ⟨⟨kl, ll⟩, hl, ⟨kr, lr⟩, hr, k', hk', h⟩ := h
  split at h
  · cases h
  · rename_i hs
    simp only [Option.pure_def, Option.some.injEq, Prod.mk.injEq] at h
    obtain ⟨rfl, rfl⟩ := h
    refine ⟨kl, ll, kr, lr, hl, hr, hk', fun hsh => ?_, rfl⟩
    simp only [hsh, Bool.true_and, Bool.or_eq_true, not_or] at hs
    simpa using hs.1

/-- An operand with a call gives a value that is no untyped constant. -/
theorem nu_of_call {e : Spec.Expr} {u : Bool} (hu : u = isLitE e) {w : Val} (h : u = false → ¬ UInt w)
    (hc : hasCall e = true) : ¬ UInt w := h (hu ▸ isLitE_of_hasCall _ hc)

/-- **Binary operators over a call**, other than `&&` and `||`. -/
theorem binary_sim3 (d : Nat) (ih : ExprSim ds cx P K sg d) (hK : 4 ≤ K) (κ : String → Option Kind)
    (op : String) (l r : Spec.Expr) (k : Kind) (le : Expr) (hl : lowerC K sg κ (.binary op l r) = some (k, le))
    (hc : (hasCall l || hasCall r) = true) (hand : op ≠ "&&") (hor : op ≠ "||") (s : Spec.St) (g : St) (env : Env)
    (hi : Inv3 cx P sg κ s.toCore g env) (hsg : s.steps ≤ g.steps) (N : Nat) (hN : K * (d + 1) ≤ N) :
    ERes3 k (isLitE (.binary op l r)) s g ((Spec.eval ds (d + 1) (.binary op l r)).run.run s)
      ((evalExpr cx N env le).run.run g) := by
  obtain ⟨kl, ll, kr, lr, hll, hlr, hb, hsh, rfl⟩ := lowerC_binary K sg hl hc
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 4 := ⟨N - 4, by rw [Nat.mul_succ] at hN; omega⟩
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; trivial
  | succ k' =>
  have ht := ticked_of s k' hs
  rw [eval_binary_run ds d k' op l r s hs hand hor, evalExpr_binary _ _ _ _ _ _ hand hor hi.inv.pre.1]
  have hL := ih κ l kl ll hll { s with steps := k' } g env hi (by show k' ≤ g.steps; omega) (n + 3)
    (by rw [Nat.mul_succ] at hN; omega)
  rw [Spec.run_bind_eq, grun_bind]
  rcases hrl : (Spec.eval ds d l).run.run { s with steps := k' } with ⟨rl, sl⟩
  rw [hrl] at hL
  cases rl with
  | error e =>
    cases e with
    | panic pc m => obtain ⟨pv, g', hg, hp, ho, hgf⟩ := hL; rw [hg]; exact panOK_tick ht ⟨pv, g', rfl, hp, ho, hgf⟩
    | ret => exact hL.elim
    | _ => trivial
  | ok vl =>
  obtain ⟨wl, gl, hg, hvl, hkl, hul, hsfl, hgfl, hspl⟩ := hL
  rw [hg]
  simp only
  have hR := ih κ r kr lr hlr sl gl env (inv3_frame hi hsfl hgfl)
    (spent_le hspl (by show k' ≤ g.steps; omega)) (n + 3) (by rw [Nat.mul_succ] at hN; omega)
  rw [Spec.run_bind_eq, grun_bind]
  rcases hrr : (Spec.eval ds d r).run.run sl with ⟨rr, sr⟩
  rw [hrr] at hR
  cases rr with
  | error e =>
    cases e with
    | panic pc m =>
      obtain ⟨pv, g', hg, hp, ho, hgf⟩ := hR; rw [hg]
      refine panOK_tick ht ⟨pv, g', rfl, hp, ?_, gframe_trans hgfl hgf⟩
      rw [ho, (sframe_parts hsfl).2.2]
    | ret => exact hR.elim
    | _ => trivial
  | ok vr =>
  obtain ⟨wr, gr, hg, hvr, hkr, hur, hsfr, hgfr, hspr⟩ := hR
  rw [hg]
  simp only
  have hnu : ¬ (UInt wl ∧ UInt wr) := by
    rintro ⟨h1, h2⟩
    simp only [Bool.or_eq_true] at hc
    rcases hc with hc | hc
    · exact nu_of_call rfl hul hc h1
    · exact nu_of_call rfl hur hc h2
  have hbs := binary_sim cx n op vl vr wl wr kl kr k gr sr.toCore hvl hvr hkl hkr hb hand hor hnu
    (fun hsh' => hul (hsh hsh'))
  rw [Spec.run_liftM]
  have hro := readOnly_binop op vl vr sr.toCore
  rcases hbr : (Spec.binop op vl vr).run.run sr.toCore with ⟨rb, cb⟩
  rw [hbr] at hbs hro
  simp only at hro
  subst hro
  cases rb with
  | error e =>
    cases e with
    | panic pc m =>
      obtain ⟨pv, hg, hp⟩ := hbs
      rw [hg]
      refine panOK_tick ht ⟨pv, gr, rfl, hp, ?_, gframe_trans hgfl hgfr⟩
      show sr.out = _
      rw [(sframe_parts hsfr).2.2, (sframe_parts hsfl).2.2]
    | ret v => exact (noRet_binop op vl vr sr.toCore v (by rw [hbr])).elim
    | _ => trivial
  | ok v =>
    obtain ⟨w, hg, hvw, hk, hnuw⟩ := hbs
    rw [hg]
    exact eRes3_tick ht ⟨w, gr, rfl, hvw, hk, fun _ => hnuw, sframe_trans hsfl hsfr,
      gframe_trans hgfl hgfr, spent_trans hspl hspr⟩

end Sim3

section Sim4
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs)

theorem spec_asBool_run (b : Bool) (c : Spec.Core) : (Spec.asBool (.bool b)).run.run c = (.ok b, c) := rfl

/-- **`&&` and `||` over a call**: each operand evaluated as galaspec and Go short-circuit. -/
theorem andor_sim3 (d : Nat) (ih : ExprSim ds cx P K sg d) (hK : 4 ≤ K) (κ : String → Option Kind)
    (op : String) (hop : op = "&&" ∨ op = "||") (l r : Spec.Expr) (k : Kind) (le : Expr)
    (hl : lowerC K sg κ (.binary op l r) = some (k, le))
    (hc : (hasCall l || hasCall r) = true) (s : Spec.St) (g : St) (env : Env)
    (hi : Inv3 cx P sg κ s.toCore g env) (hsg : s.steps ≤ g.steps) (N : Nat) (hN : K * (d + 1) ≤ N) :
    ERes3 k (isLitE (.binary op l r)) s g ((Spec.eval ds (d + 1) (.binary op l r)).run.run s)
      ((evalExpr cx N env le).run.run g) := by
  obtain ⟨kl, ll, kr, lr, hll, hlr, hb, -, rfl⟩ := lowerC_binary K sg hl hc
  have hkb : kl = .bool ∧ kr = .bool ∧ k = .bool := by
    rcases hop with rfl | rfl <;> (unfold binaryKind at hb; split at hb <;> simp_all)
  obtain ⟨rfl, rfl, rfl⟩ := hkb
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 4 := ⟨N - 4, by rw [Nat.mul_succ] at hN; omega⟩
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; trivial
  | succ k' =>
  have ht := ticked_of s k' hs
  have hL := ih κ l .bool ll hll { s with steps := k' } g env hi (by show k' ≤ g.steps; omega) (n + 3)
    (by rw [Nat.mul_succ] at hN; omega)
  rcases hop with rfl | rfl
  · rw [eval_and_run ds d k' l r s hs, evalExpr_and]
    rw [Spec.run_bind_eq, grun_bind]
    rcases hrl : (Spec.eval ds d l).run.run { s with steps := k' } with ⟨rl, sl⟩
    rw [hrl] at hL
    cases rl with
    | error e =>
      cases e with
      | panic pc m => obtain ⟨pv, g', hg, hp, ho, hgf⟩ := hL; rw [hg]; exact panOK_tick ht ⟨pv, g', rfl, hp, ho, hgf⟩
      | ret => exact hL.elim
      | _ => trivial
    | ok vl =>
    obtain ⟨wl, gl, hg, hvl, hkl, hul, hsfl, hgfl, hspl⟩ := hL
    obtain ⟨b, t, rfl, rfl, htb⟩ := vrel_bool hvl hkl
    rw [hg]
    simp only
    rw [Spec.run_bind_eq, Spec.run_liftM, spec_asBool_run, grun_bind, gasBool]
    cases b with
    | false =>
      simp only [Bool.false_eq_true, ite_false, run_pureM]
      exact eRes3_tick ht ⟨_, gl, rfl, vrel_of_bool false t htb, rfl, fun _ => by simp [UInt], hsfl, hgfl, hspl⟩
    | true =>
      simp only [ite_true]
      have hR := ih κ r .bool lr hlr sl gl env (inv3_frame hi hsfl hgfl)
        (spent_le hspl (by show k' ≤ g.steps; omega)) (n + 3) (by rw [Nat.mul_succ] at hN; omega)
      rw [Spec.run_bind_eq, grun_bind]
      rcases hrr : (Spec.eval ds d r).run.run sl with ⟨rr, sr⟩
      rw [hrr] at hR
      cases rr with
      | error e =>
        cases e with
        | panic pc m =>
          obtain ⟨pv, g', hg, hp, ho, hgf⟩ := hR; rw [hg]
          refine panOK_tick ht ⟨pv, g', rfl, hp, ?_, gframe_trans hgfl hgf⟩
          rw [ho, (sframe_parts hsfl).2.2]
        | ret => exact hR.elim
        | _ => trivial
      | ok vr =>
      obtain ⟨wr, gr, hg, hvr, hkr, -, hsfr, hgfr, hspr⟩ := hR
      obtain ⟨b', t', rfl, rfl, htb'⟩ := vrel_bool hvr hkr
      rw [hg]
      simp only
      rw [Spec.run_bind_eq, Spec.run_liftM, spec_asBool_run, grun_bind, gasBool]
      exact eRes3_tick ht ⟨_, gr, rfl, vrel_of_bool b' _ (boolType_cases t t' true b' htb htb'), rfl,
        fun _ => by simp [UInt], sframe_trans hsfl hsfr, gframe_trans hgfl hgfr, spent_trans hspl hspr⟩
  · rw [eval_or_run ds d k' l r s hs, evalExpr_or]
    rw [Spec.run_bind_eq, grun_bind]
    rcases hrl : (Spec.eval ds d l).run.run { s with steps := k' } with ⟨rl, sl⟩
    rw [hrl] at hL
    cases rl with
    | error e =>
      cases e with
      | panic pc m => obtain ⟨pv, g', hg, hp, ho, hgf⟩ := hL; rw [hg]; exact panOK_tick ht ⟨pv, g', rfl, hp, ho, hgf⟩
      | ret => exact hL.elim
      | _ => trivial
    | ok vl =>
    obtain ⟨wl, gl, hg, hvl, hkl, hul, hsfl, hgfl, hspl⟩ := hL
    obtain ⟨b, t, rfl, rfl, htb⟩ := vrel_bool hvl hkl
    rw [hg]
    simp only
    rw [Spec.run_bind_eq, Spec.run_liftM, spec_asBool_run, grun_bind, gasBool]
    cases b with
    | true =>
      simp only [ite_true, run_pureM]
      exact eRes3_tick ht ⟨_, gl, rfl, vrel_of_bool true t htb, rfl, fun _ => by simp [UInt], hsfl, hgfl, hspl⟩
    | false =>
      simp only [Bool.false_eq_true, ite_false]
      have hR := ih κ r .bool lr hlr sl gl env (inv3_frame hi hsfl hgfl)
        (spent_le hspl (by show k' ≤ g.steps; omega)) (n + 3) (by rw [Nat.mul_succ] at hN; omega)
      rw [Spec.run_bind_eq, grun_bind]
      rcases hrr : (Spec.eval ds d r).run.run sl with ⟨rr, sr⟩
      rw [hrr] at hR
      cases rr with
      | error e =>
        cases e with
        | panic pc m =>
          obtain ⟨pv, g', hg, hp, ho, hgf⟩ := hR; rw [hg]
          refine panOK_tick ht ⟨pv, g', rfl, hp, ?_, gframe_trans hgfl hgf⟩
          rw [ho, (sframe_parts hsfl).2.2]
        | ret => exact hR.elim
        | _ => trivial
      | ok vr =>
      obtain ⟨wr, gr, hg, hvr, hkr, -, hsfr, hgfr, hspr⟩ := hR
      obtain ⟨b', t', rfl, rfl, htb'⟩ := vrel_bool hvr hkr
      rw [hg]
      simp only
      rw [Spec.run_bind_eq, Spec.run_liftM, spec_asBool_run, grun_bind, gasBool]
      exact eRes3_tick ht ⟨_, gr, rfl, vrel_of_bool b' _ (boolType_cases t t' false b' htb htb'), rfl,
        fun _ => by simp [UInt], sframe_trans hsfl hsfr, gframe_trans hgfl hgfr, spent_trans hspl hspr⟩

end Sim4

section Args
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs)

theorem lowerArgs_facts (κ : String → Option Kind) : ∀ (ks : List Kind) (args : List Spec.Expr) (las : List Expr),
    lowerArgs K sg κ ks args = some las →
      (∀ a ∈ args, Spec.isNamed a = false) ∧ args.length = ks.length
  | [], [], _, _ => ⟨by simp, rfl⟩
  | k :: ks, a :: as, las, h => by
    simp only [lowerArgs] at h
    split at h
    · cases h
    · rename_i hn
      split at h
      · rename_i k' l hl
        split at h
        · simp only [Option.map_eq_some_iff] at h
          obtain ⟨ls, hls, -⟩ := h
          obtain ⟨h1, h2⟩ := lowerArgs_facts κ ks as ls hls
          refine ⟨fun b hb => ?_, by simp [h2]⟩
          simp only [List.mem_cons] at hb
          rcases hb with rfl | hb
          · simpa using hn
          · exact h1 b hb
        · cases h
      · cases h
  | [], _ :: _, _, h | _ :: _, [], _, h => by simp [lowerArgs] at h

theorem evalList_cons_run (d : Nat) (e : Spec.Expr) (es : List Spec.Expr) (s : Spec.St) :
    (Spec.evalList ds d (e :: es)).run.run s = match (Spec.eval ds d e).run.run s with
      | (.ok v, t) => match (Spec.evalList ds d es).run.run t with
        | (.ok vs, t') => (.ok (v :: vs), t')
        | (.error er, t') => (.error er, t')
      | (.error er, t) => (.error er, t) := by
  rw [Spec.evalList.eq_def]
  simp only [Spec.run_bind_eq]
  rcases (Spec.eval ds d e).run.run s with ⟨r, t⟩
  cases r with
  | error => rfl
  | ok v =>
    simp only
    rcases (Spec.evalList ds d es).run.run t with ⟨r', t'⟩
    cases r' <;> rfl

theorem evalList_nil_run (d : Nat) (s : Spec.St) : (Spec.evalList ds d []).run.run s = (.ok [], s) := by
  rw [Spec.evalList.eq_def]; rfl

/-- **A call's arguments**, left to right on both sides. -/
theorem mapM_sim (d : Nat) (ih : ExprSim ds cx P K sg d) (κ : String → Option Kind) :
    ∀ (ks : List Kind) (args : List Spec.Expr) (las : List Expr), lowerArgs K sg κ ks args = some las →
    ∀ (s : Spec.St) (g : St) (env : Env), Inv3 cx P sg κ s.toCore g env → s.steps ≤ g.steps →
    ∀ M, K * d ≤ M → ARes3 ks s g ((Spec.evalList ds d args).run.run s) ((las.mapM (evalExpr cx M env)).run.run g)
  | [], [], las, h, s, g, env, _, _, M, _ => by
    simp only [lowerArgs, Option.some.injEq] at h; subst h
    rw [evalList_nil_run]
    exact ⟨[], g, rfl, trivial, sframe_refl _, gframe_refl g, by unfold Spent; omega⟩
  | k :: ks, a :: as, las, h, s, g, env, hi, hsg, M, hM => by
    simp only [lowerArgs] at h
    split at h
    · cases h
    split at h
    · rename_i k' l hl
      split at h
      · rename_i hk; subst hk
        simp only [Option.map_eq_some_iff] at h
        obtain ⟨ls, hls, rfl⟩ := h
        have hA := ih κ a k' l hl s g env hi hsg M hM
        rw [evalList_cons_run, List.mapM_cons, grun_bind]
        rcases hra : (Spec.eval ds d a).run.run s with ⟨ra, sa⟩
        rw [hra] at hA
        cases ra with
        | error e =>
          cases e with
          | panic pc m => obtain ⟨pv, g', hg, hp, ho, hgf⟩ := hA; rw [hg]; exact ⟨pv, g', rfl, hp, ho, hgf⟩
          | ret => exact hA.elim
          | _ => trivial
        | ok v =>
        obtain ⟨w, ga, hg, hvw, hk, -, hsfa, hgfa, hspa⟩ := hA
        rw [hg]
        simp only
        have hR := mapM_sim d ih κ ks as ls hls sa ga env (inv3_frame hi hsfa hgfa) (spent_le hspa hsg) M hM
        rw [grun_bind]
        rcases hrr : (Spec.evalList ds d as).run.run sa with ⟨rr, sr⟩
        rw [hrr] at hR
        cases rr with
        | error e =>
          cases e with
          | panic pc m =>
            obtain ⟨pv, g', hg, hp, ho, hgf⟩ := hR; rw [hg]
            exact ⟨pv, g', rfl, hp, by rw [ho, (sframe_parts hsfa).2.2], gframe_trans hgfa hgf⟩
          | ret => exact hR.elim
          | _ => trivial
        | ok vs =>
        obtain ⟨ws, gr, hg, hkr, hsfr, hgfr, hspr⟩ := hR
        rw [hg]
        exact ⟨w :: ws, gr, rfl, ⟨hvw, hk, hkr⟩, sframe_trans hsfa hsfr, gframe_trans hgfa hgfr,
          spent_trans hspa hspr⟩
      · cases h
    · cases h
  | [], _ :: _, _, h, _, _, _, _, _, _, _ | _ :: _, [], _, h, _, _, _, _, _, _, _ => by simp [lowerArgs] at h

theorem krel_vrel : ∀ {ks : List Kind} {vs : List Spec.Val} {w : Val} {ws : List Val},
    KRel ks vs (w :: ws) → ∃ v, VRel v w
  | _ :: _, _ :: _, _, _, h => ⟨_, h.1⟩
  | [], [], _, _, h | [], _ :: _, _, _, h | _ :: _, [], _, _, h => by simp [KRel] at h

/-- ... and GoCore's `evalArgs`, which passes a single call's results as they are: here a
    single argument is a value of a kind, whose results are itself. -/
theorem args_sim (d : Nat) (ih : ExprSim ds cx P K sg d) (κ : String → Option Kind) (ks : List Kind)
    (args : List Spec.Expr) (las : List Expr) (hl : lowerArgs K sg κ ks args = some las)
    (s : Spec.St) (g : St) (env : Env) (hi : Inv3 cx P sg κ s.toCore g env) (hsg : s.steps ≤ g.steps)
    (M : Nat) (hM : K * d ≤ M) :
    ARes3 ks s g ((Spec.evalList ds d args).run.run s) ((evalArgs cx (M + 1) env las).run.run g) := by
  have h := mapM_sim ds cx P K sg d ih κ ks args las hl s g env hi hsg M hM
  match las, h with
  | [a], h =>
    simp only [evalArgs]
    rw [List.mapM_cons, List.mapM_nil, grun_bind] at h
    rw [grun_bind]
    revert h
    rcases (evalExpr cx M env a).run.run g with ⟨r, g'⟩
    intro h
    cases r with
    | error e => simp only at h ⊢; exact h
    | ok w =>
      simp only [run_pureM, grun_bind] at h ⊢
      rcases sr : (Spec.evalList ds d args).run.run s with ⟨rs, s'⟩
      rw [sr] at h
      rcases rs with er | vs
      · cases er with
        | panic => obtain ⟨_, _, hg, _⟩ := h; cases hg
        | ret => exact h.elim
        | _ => trivial
      · obtain ⟨ws, g'', hg, hkr, rest⟩ := h
        cases hg
        obtain ⟨v, hv⟩ := krel_vrel hkr
        exact ⟨[w], g', by rw [results_of_vrel hv], hkr, rest⟩
  | [], h => simpa [evalArgs] using h
  | a :: b :: rest, h => simpa [evalArgs] using h

end Args

/-! ## The callee's frame -/

section Frame
open Gala.GoCore.Full

/-- The locals galaspec and GoCore bind, related, and nothing else bound. -/
def FrameRel (κ : String → Option Kind) (c : Spec.Core) (cells : Array Val) (env : Env) : Prop :=
  LocRel κ c cells env ∧ ∀ x, κ x = none → Spec.lookup c.locals x = none ∧ env.lookup x = none

theorem bval_plain (h : Array Spec.Val) (v : Spec.Val) (hv : ∀ n, v ≠ .ref n) : bval h ⟨v, false⟩ = some v := by
  unfold bval; cases v <;> simp_all

/-- One parameter bound on both sides. -/
theorem frameRel_bind {κ : String → Option Kind} {c : Spec.Core} {cells : Array Val} {env : Env}
    (h : FrameRel κ c cells env) (y : String) (hy : y ≠ "_") (v : Spec.Val) (w : Val) (k : Kind)
    (hv : VRel v w) (hk : valKind v = some k) :
    FrameRel (upd κ y k) { c with locals := Spec.bind c.locals y ⟨v, false⟩ } (cells.push (defaultVal w))
      (env.bind y cells.size) := by
  have hb : Spec.bind c.locals y ⟨v, false⟩ = (y, ⟨v, false⟩) :: c.locals := by simp [Spec.bind, hy]
  obtain ⟨hvd, hnu, hnb, hns⟩ := vrel_default hv
  refine ⟨fun x k' hk' => ?_, fun x hx => ?_⟩
  · unfold upd at hk'
    by_cases hxy : x = y
    · subst hxy
      simp only [ite_true, Option.some.injEq] at hk'
      subst hk'
      refine ⟨⟨v, false⟩, cells.size, v, defaultVal w, ?_, bval_plain _ v (vrel_not_ref hv), ?_, ?_, hvd, hk,
        hnu, hnb, hns⟩
      · show Spec.lookup (Spec.bind c.locals x ⟨v, false⟩) x = _
        rw [hb]; simp [Spec.lookup]
      · rw [Env.lookup_bind _ _ _ _ hy]; simp
      · simp
    · simp only [hxy, ite_false] at hk'
      obtain ⟨b', cell, v', w', hb', hv', hcell, hw', rest⟩ := h.1 x k' hk'
      refine ⟨b', cell, v', w', ?_, hv', ?_, pre_get (pre_push _ _) hw', rest⟩
      · show Spec.lookup (Spec.bind c.locals y ⟨v, false⟩) x = _
        rw [hb]; simp only [Spec.lookup, Ne.symm hxy, ite_false]; exact hb'
      · rw [Env.lookup_bind _ _ _ _ hy]; simp only [hxy, ite_false]; exact hcell
  · unfold upd at hx
    by_cases hxy : x = y
    · simp [hxy] at hx
    · simp only [hxy, ite_false] at hx
      obtain ⟨h1, h2⟩ := h.2 x hx
      refine ⟨?_, ?_⟩
      · show Spec.lookup (Spec.bind c.locals y ⟨v, false⟩) x = _
        rw [hb]; simp only [Spec.lookup, Ne.symm hxy, ite_false]; exact h1
      · rw [Env.lookup_bind _ _ _ _ hy]; simp only [hxy, ite_false]; exact h2

/-- **The parameters bound** on both sides, one by one: related, and nothing else bound. -/
theorem frameRel_params : ∀ (xks : List (String × Kind)) (ps : List Spec.Param) (vs : List Spec.Val)
    (ws : List Val) (κ : String → Option Kind) (c : Spec.Core) (env : Env) (g : St),
    ps.map (·.name) = xks.map (·.1) → KRel (xks.map (·.2)) vs ws → (∀ x ∈ xks.map (·.1), x ≠ "_") →
    FrameRel κ c g.cells env →
    FrameRel (xks.foldl (fun κ xk => upd κ xk.1 xk.2) κ)
      { c with locals := (ps.zip vs).foldl (fun ρ pv => Spec.bind ρ pv.1.name ⟨pv.2, false⟩) c.locals }
      (((xks.map fun xk => (xk.1, Expr.ident (kindName xk.2))).zip ws).foldl (fun s a => paramStep a s) (env, g)).2.cells
      (((xks.map fun xk => (xk.1, Expr.ident (kindName xk.2))).zip ws).foldl (fun s a => paramStep a s) (env, g)).1
  | [], [], [], [], κ, c, env, g, _, _, _, h => by simpa using h
  | (x, k) :: xks, p :: ps, v :: vs, w :: ws, κ, c, env, g, hn, hkr, hx, h => by
    simp only [List.map_cons, List.cons.injEq] at hn
    obtain ⟨hvw, hk, hkr⟩ := hkr
    simp only [List.map_cons, List.zip_cons_cons, List.foldl_cons]
    have hstep := frameRel_bind h x (hx x (by simp)) v w k hvw hk
    have := frameRel_params xks ps vs ws (upd κ x k) { c with locals := Spec.bind c.locals x ⟨v, false⟩ }
      (env.bind x g.cells.size) { g with cells := g.cells.push (defaultVal w), types := g.types.push (teTy (.ident (kindName k))) }
      hn.2 hkr (fun y hy => hx y (by simp [hy])) hstep
    rw [hn.1]
    exact this
  | [], _ :: _, _, _, _, _, _, _, hn, _, _, _ | _ :: _, [], _, _, _, _, _, _, hn, _, _, _ => by simp at hn
  | [], [], _ :: _, _, _, _, _, _, _, hk, _, _ | [], [], [], _ :: _, _, _, _, _, _, hk, _, _ => by simp [KRel] at hk
  | _ :: _, _ :: _, [], _, _, _, _, _, _, hk, _, _ | _ :: _, _ :: _, _ :: _, [], _, _, _, _, _, hk, _, _ => by
    simp [KRel] at hk

end Frame

/-! ## Helpers for a call -/

section CallHelpers
open Gala.GoCore.Full

/-- galaspec's `runIn`: the body run in the frame; a `return` is its value; the caller's
    locals back when it ends normally. -/
theorem runIn_res (ds : Spec.Decls) (d : Nat) (fr : Spec.Env) (body : Spec.Expr) (s : Spec.St) :
    (Spec.runIn ds d fr body).run.run s = match (Spec.eval ds d body).run.run { s with locals := fr } with
      | (.ok v, t) => (.ok v, { t with locals := s.locals })
      | (.error (.ret v), t) => (.ok v, { t with locals := s.locals })
      | (.error e, t) => (.error e, t) := by
  rw [Spec.runIn_run, Spec.run_tryCatch_eq]
  rcases (Spec.eval ds d body).run.run { s with locals := fr } with ⟨r, t⟩
  cases r with
  | ok => rfl
  | error e => cases e <;> rfl

theorem mapM_params : ∀ (ps : List Spec.Param) (xks : List (String × Kind)),
    ps.mapM (fun p => do pure (p.name, ← tyKindS (← p.type))) = some xks →
      ps.map (·.name) = xks.map (·.1) ∧ ps.map (·.type) = xks.map (fun xk => some (.name (kindName xk.2)))
  | [], xks, h => by simp at h; subst h; simp
  | p :: ps, xks, h => by
    simp only [List.mapM_cons, Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def,
      Option.some.injEq] at h
    obtain ⟨⟨x, k⟩, ⟨t, ht, k', hk', hxk⟩, xs, hxs, rfl⟩ := h
    simp only [Prod.mk.injEq] at hxk
    obtain ⟨rfl, rfl⟩ := hxk
    obtain ⟨h1, h2⟩ := mapM_params ps xs hxs
    simp [h1, h2, ht, tyKindS_some hk']

/-- What a function's lowered signature says of its galaspec declaration. -/
theorem sigOf_facts {F : Spec.Func} {xks : List (String × Kind)} {ρ : Kind} (h : sigOf F = some (xks, ρ)) :
    F.params.map (·.name) = xks.map (·.1) ∧
    F.params.map (·.type) = xks.map (fun xk => some (.name (kindName xk.2))) ∧
    F.result = some (.name (kindName ρ)) := by
  simp only [sigOf, Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def, Option.some.injEq] at h
  obtain ⟨xs, hxs, t, ht, ρ', hρ, hpair⟩ := h
  simp only [Prod.mk.injEq] at hpair
  obtain ⟨rfl, rfl⟩ := hpair
  obtain ⟨h1, h2⟩ := mapM_params F.params xs hxs
  exact ⟨h1, h2, by rw [ht, tyKindS_some hρ]⟩

theorem params_typed {F : Spec.Func} {xks : List (String × Kind)}
    (h : F.params.map (·.type) = xks.map (fun xk => some (.name (kindName xk.2)))) :
    ∀ p ∈ F.params, ∃ k, p.type = some (.name (kindName k)) := by
  intro p hp
  have : p.type ∈ xks.map (fun xk => some (Spec.Ty.name (kindName xk.2))) := h ▸ List.mem_map_of_mem hp
  simp only [List.mem_map] at this
  obtain ⟨xk, -, he⟩ := this
  exact ⟨xk.2, he.symm⟩

theorem paramStep_env (a : (String × Expr) × Val) (s : Env × St) :
    (paramStep a s).1.pkg = s.1.pkg ∧ (paramStep a s).1.tenv = s.1.tenv ∧ (paramStep a s).1.iota = s.1.iota := by
  rcases a with ⟨⟨x, te⟩, v⟩
  rcases s with ⟨env, g⟩
  simp only [paramStep, Env.bind]
  split
  · exact ⟨rfl, rfl, rfl⟩
  · split <;> exact ⟨rfl, rfl, rfl⟩

/-- Binding the parameters keeps the package, the type parameters and `iota`, and `true`
    and `false` unshadowed. -/
theorem params_env : ∀ (l : List ((String × Expr) × Val)) (s : Env × St) (cx : Ctx),
    (∀ a ∈ l, a.1.1 ≠ "_" ∧ a.1.1 ≠ "true" ∧ a.1.1 ≠ "false") → Predeclared cx s.1 →
    (l.foldl (fun s a => paramStep a s) s).1.pkg = s.1.pkg ∧ (l.foldl (fun s a => paramStep a s) s).1.tenv = s.1.tenv ∧
      Predeclared cx (l.foldl (fun s a => paramStep a s) s).1
  | [], _, _, _, hp => ⟨rfl, rfl, hp⟩
  | a :: l, s, cx, hn, hp => by
    rw [List.foldl_cons]
    obtain ⟨h1, h2, h3⟩ := hn a (by simp)
    have hp' : Predeclared cx (paramStep a s).1 := by
      rcases a with ⟨⟨x, te⟩, v⟩
      exact predeclared_bind hp x _ h1 h2 h3
    obtain ⟨e1, e2, -⟩ := paramStep_env a s
    obtain ⟨f1, f2, f3⟩ := params_env l (paramStep a s) cx (fun b hb => hn b (by simp [hb])) hp'
    exact ⟨f1.trans e1, f2.trans e2, f3⟩

/-- Binding the parameters only adds cells. -/
theorem params_st : ∀ (l : List ((String × Expr) × Val)) (s : Env × St),
    GFrame s.2 (l.foldl (fun s a => paramStep a s) s).2 ∧ (l.foldl (fun s a => paramStep a s) s).2.steps = s.2.steps
  | [], s => ⟨gframe_refl _, rfl⟩
  | a :: l, s => by
    rw [List.foldl_cons]
    have hstep : GFrame s.2 (paramStep a s).2 ∧ (paramStep a s).2.steps = s.2.steps := by
      rcases a with ⟨⟨x, te⟩, v⟩
      rcases s with ⟨env, g⟩
      exact ⟨⟨pre_push _ _, by cases g; rfl⟩, rfl⟩
    obtain ⟨h1, h2⟩ := params_st l (paramStep a s)
    exact ⟨gframe_trans hstep.1 h1, h2.trans hstep.2⟩

/-- The frame of a call: parameters bound, a list of deferred calls pushed, the body run, the
    list popped: only cells added. -/
theorem call_frame {g₀ pb g₁ : St} (D : List (Val × List Val × Bool)) (hpb : GFrame g₀ pb)
    (hb : GFrame { pb with defers := D :: g₀.defers } g₁) : GFrame g₀ { g₁ with defers := g₀.defers } := by
  refine ⟨pre_trans hpb.1 hb.1, ?_⟩
  have e₁ := hpb.2; have e₂ := hb.2
  cases g₀; cases pb; cases g₁
  simp only [St.mk.injEq] at e₁ e₂ ⊢
  obtain ⟨-, -, a1, a2, -, a3, a4, a5, a6, a7⟩ := e₁
  obtain ⟨-, -, b1, b2, -, b3, b4, -, b6, b7⟩ := e₂
  subst_vars
  simp

/-- A name the package does not declare, and not exported, is no package's. -/
theorem nameOwner_none (cx : Ctx) (env : Env) (x : String) (p : PkgCtx) (hpk : cx.pkgs.get? env.pkg = some p)
    (ho : p.objs.get? x = none) (hx : exported x = false) : nameOwner cx env x = none := by
  unfold nameOwner
  rw [hpk]
  have hc : p.objs.contains x = false := by
    simp only [Std.HashMap.contains_eq_isSome_getElem?, ← Std.HashMap.get?_eq_getElem?, ho, Option.isSome_none]
  simp only [hc, Bool.false_eq_true, ite_false, hx, Bool.not_false, ite_true]

/-- The environment a lowered function is called in, before its parameters, has `true`
    and `false` unshadowed. -/
theorem predeclared_callee (cx : Ctx) (P : String) (fi : Nat) (p : PkgCtx) (hpk : cx.pkgs.get? P = some p)
    (hobj : ∀ x ∈ ["true", "false", "int", "bool", "string"], p.objs.get? x = none) :
    Predeclared cx (calleeEnv P fi).push := by
  refine ⟨rfl, fun x hx => ?_⟩
  have ho : p.objs.get? x = none := hobj x (by simp at hx ⊢; rcases hx with rfl | rfl <;> simp)
  have hexp : exported x = false := by simp at hx; rcases hx with rfl | rfl <;> decide
  exact ⟨by simp [calleeEnv, Env.push, Env.lookup, List.findSome?], p, hpk, ho,
    nameOwner_none cx _ x p hpk ho hexp⟩

theorem nameOwner_kinds (cx : Ctx) (P : String) (fi : Nat) (p : PkgCtx) (hpk : cx.pkgs.get? P = some p)
    (hobj : ∀ x ∈ ["true", "false", "int", "bool", "string"], p.objs.get? x = none) (k : Kind) :
    nameOwner cx (calleeEnv P fi) (kindName k) = none :=
  nameOwner_none cx _ _ p hpk (hobj _ (by cases k <;> simp [kindName])) (by cases k <;> decide)

end CallHelpers

/-! ## A call -/

section CallSim
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs)

/-- The function bodies, at galaspec depth `d` and GoCore depth from `K * d + 3` on. -/
def BodySim (d : Nat) : Prop :=
  ∀ F xks ρ lb, sigOf F = some (xks, ρ) → lowerBody K sg F = some lb → ∀ (s : Spec.St) (g : St) (env : Env),
    Inv3 cx P sg (paramKinds xks) s.toCore g env → s.steps ≤ g.steps → ∀ N, K * d + 3 ≤ N →
      ResB ρ s g ((Spec.eval ds d F.body).run.run s) ((execStmts cx N env lb).run.run g)

theorem lowerC_call {κ : String → Option Kind} {f : String} {args : List Spec.Expr} {k : Kind} {le : Expr}
    (h : lowerC K sg κ (.call (.name f) args) = some (k, le)) :
    κ f = none ∧ ∃ ks las, sg f = some (ks, k) ∧ lowerArgs K sg κ ks args = some las ∧
      le = .call (.ident f) las false := by
  simp only [lowerC] at h
  split at h
  · cases h
  · rename_i hf
    have hκ : κ f = none := by cases h' : κ f <;> simp_all
    simp only [Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def, Option.some.injEq] at h
    obtain ⟨⟨ks, k'⟩, hsg, las, hlas, hp⟩ := h
    simp only [Prod.mk.injEq] at hp
    obtain ⟨rfl, rfl⟩ := hp
    exact ⟨hκ, ks, las, hsg, hlas, rfl⟩

theorem lowerBody_names {F : Spec.Func} {xks : List (String × Kind)} {ρ : Kind} {lb : List Stmt}
    (hs : sigOf F = some (xks, ρ)) (h : lowerBody K sg F = some lb) :
    ∀ x ∈ xks.map (·.1), x ≠ "_" ∧ x ≠ "true" ∧ x ≠ "false" := by
  simp only [lowerBody, hs] at h
  split at h
  · rename_i hc; exact hc.2
  · cases h

theorem argsRel_of_krel : ∀ {xks : List (String × Kind)} {vs : List Spec.Val} {ws : List Val},
    KRel (xks.map (·.2)) vs ws → ArgsRel xks ws
  | [], [], [], _ => trivial
  | _ :: _, _ :: _, _ :: _, ⟨hv, hk, h⟩ => ⟨⟨_, hv, hk⟩, argsRel_of_krel h⟩
  | [], _ :: _, _, h | [], [], _ :: _, h | _ :: _, [], _, h | _ :: _, _ :: _, [], h => by simp [KRel] at h

theorem krel_length : ∀ {ks : List Kind} {vs : List Spec.Val} {ws : List Val}, KRel ks vs ws →
    vs.length = ks.length
  | [], [], [], _ => rfl
  | _ :: _, _ :: _, _ :: _, ⟨_, _, h⟩ => by simp [krel_length h]
  | [], _ :: _, _, h | [], [], _ :: _, h | _ :: _, [], _, h | _ :: _, _ :: _, [], h => by simp [KRel] at h

theorem sframe_mk {c c' : Spec.Core} (hh : Pre c.heap c'.heap) (hl : c'.locals = c.locals)
    (hg : c'.globals = c.globals) (ho : c'.out = c.out) : SFrame c c' := by
  refine ⟨hh, ?_⟩
  cases c; cases c'; simp_all

theorem evalExpr_call (c : Ctx) (n : Nat) (env : Env) (fn : Expr) (args : List Expr) :
    evalExpr c (n + 1) env (.call fn args false) = (do Val.single (← evalCall c n env fn args false)) := by
  rw [evalExpr]

/-- **A call of a function the lowering calls**: galaspec's call against Go's. GoCore takes
    one step for it (in `callBody`, after the arguments); galaspec took one for the call
    node before the arguments, so GoCore's steps keep up. -/
theorem call_sim3 (d : Nat) (hF : FunRel K sg ds cx P) (hK : 7 ≤ K) (ihE : ExprSim ds cx P K sg d)
    (ihB : BodySim ds cx P K sg d) (κ : String → Option Kind) (f : String) (args : List Spec.Expr) (k : Kind)
    (le : Expr) (hl : lowerC K sg κ (.call (.name f) args) = some (k, le)) (s : Spec.St) (g : St) (env : Env)
    (hi : Inv3 cx P sg κ s.toCore g env) (hsg : s.steps ≤ g.steps) (N : Nat) (hN : K * (d + 1) ≤ N) :
    ERes3 k (isLitE (.call (.name f) args)) s g ((Spec.eval ds (d + 1) (.call (.name f) args)).run.run s)
      ((evalExpr cx N env le).run.run g) := by
  obtain ⟨hκ, ks, las, hsgf, hlas, rfl⟩ := lowerC_call K sg hl
  rw [show isLitE (.call (.name f) args) = false by simp [isLitE]]
  obtain ⟨p, hpk, hobjs, hfun⟩ := hF.pkg
  obtain ⟨F, xks, ρ, lb, fi, hFl, hsig, hks, hlb, hobj⟩ := hfun f _ hsgf
  simp only [Prod.mk.injEq] at hks
  obtain ⟨rfl, hkρ⟩ := hks
  subst k
  obtain ⟨hnames, htys, hres⟩ := sigOf_facts hsig
  obtain ⟨hargsU, hargsL⟩ := lowerArgs_facts K sg κ _ args las hlas
  obtain ⟨m, rfl⟩ : ∃ m, N = m + 6 := ⟨N - 6, by rw [Nat.mul_succ] at hN; omega⟩
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; trivial
  | succ k1 =>
  obtain ⟨hlocf, henvf⟩ := hi.closed f hκ
  have hglob := hi.globals f (by rw [hsgf]; rfl)
  have hresolve : Spec.resolve ds s.locals s.globals f = .func F := by
    simp [Spec.resolve, Spec.lookupVar, hlocf, hglob, hFl]
  rw [eval_call_run ds d k1 f args F s hs hresolve]
  have hpk' : cx.pkgs.get? env.pkg = some p := hi.pkg ▸ hpk
  rw [show m + 6 = (m + 5) + 1 from rfl, evalExpr_call, grun_bind,
    evalCall_fn cx env f p fi (lowerSigOf xks ρ) lb hpk' henvf hi.tenv hobj (m + 3), grun_bind]
  have hlenP : F.params.length = xks.length := by
    have := congrArg List.length hnames; simpa using this
  have hfa : ∀ s', (Spec.funcArgs ds d F args).run.run s' = (Spec.evalList ds d args).run.run s' := by
    intro s'
    rw [Spec.funcArgs.eq_def]
    refine bindArgs_pos ds d _ _ _ args hargsU (by simp [hargsL, hlenP]) (fun j hj => ?_) s'
    rw [htys]
    simp only [List.length_map] at hj
    exact ⟨kindName xks[j].2, by simp [List.getD_eq_getElem?_getD, hlenP ▸ hj]⟩
  rw [Spec.run_bind_eq, hfa]
  have hA := args_sim ds cx P K sg d ihE κ _ args las hlas { s with steps := k1 } g env hi
    (by show k1 ≤ g.steps; omega) (m + 3) (by rw [Nat.mul_succ] at hN; omega)
  rcases hra : (Spec.evalList ds d args).run.run { s with steps := k1 } with ⟨ra, sa⟩
  rw [hra] at hA
  have ht := ticked_of s k1 hs
  cases ra with
  | error e =>
    cases e with
    | panic pc msg =>
      obtain ⟨pv, g', hg, hp, ho, hgf⟩ := hA
      rw [hg]
      exact panOK_tick ht ⟨pv, g', rfl, hp, ho, hgf⟩
    | ret => exact hA.elim
    | _ => trivial
  | ok vs =>
  obtain ⟨ws, ga, hg, hkr, hsfa, hgfa, hspa⟩ := hA
  rw [hg]
  simp only
  rw [hi.pkg, callValue_fn cx P f p fi _ lb hpk hobj hF.notStd (m + 3) ws ga]
  obtain ⟨k2, hk2⟩ : ∃ k2, ga.steps = k2 + 1 := ⟨ga.steps - 1, by unfold Spent at hspa; simp at hspa; omega⟩
  have hia := inv3_frame hi hsfa hgfa
  -- The callee's frame.
  have hlenV : vs.length = F.params.length := by rw [hlenP, krel_length hkr, List.length_map]
  have hnm := lowerBody_names K sg hsig hlb
  have hfr := frameRel_params xks F.params vs ws (fun _ => none) { sa.toCore with locals := [] }
    (calleeEnv P fi).push { ga with steps := k2 } hnames hkr (fun x hx => (hnm x hx).1)
    ⟨fun _ _ h => (by cases h), fun x _ => ⟨rfl, by simp [calleeEnv, Env.push, Env.lookup, List.findSome?]⟩⟩
  have hzip : ∀ a ∈ (xks.map fun xk => (xk.1, Expr.ident (kindName xk.2))).zip ws,
      a.1.1 ≠ "_" ∧ a.1.1 ≠ "true" ∧ a.1.1 ≠ "false" := by
    intro a ha
    have := (List.of_mem_zip ha).1
    simp only [List.mem_map] at this
    obtain ⟨xk, hxk, he⟩ := this
    rw [← he]
    exact hnm xk.1 (List.mem_map_of_mem hxk)
  obtain ⟨hepkg, hetenv, hepre⟩ := params_env _ ((calleeEnv P fi).push, { ga with steps := k2 }) cx hzip
    (predeclared_callee cx P fi p hpk hobjs)
  obtain ⟨hpst, hpsteps⟩ := params_st ((xks.map fun xk => (xk.1, Expr.ident (kindName xk.2))).zip ws)
    ((calleeEnv P fi).push, { ga with steps := k2 })
  have hgac : GFrame ga { ga with steps := k2 } := ⟨pre_refl _, by cases ga; rfl⟩
  have hpst' := gframe_trans hgac hpst
  have hic : Inv3 cx P sg (paramKinds xks) { sa with locals := specFrame F.params vs }.toCore
      { (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).2 with defers := [] :: ga.defers }
      (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).1 := by
    refine ⟨⟨hfr.1, ?_, hepre⟩, hepkg, hetenv, hfr.2, ?_⟩
    · show sa.out = (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).2.out
      rw [paramsBound, gframe_out hpst']; exact hia.inv.out
    · intro x hx; exact hia.globals x hx
  have hB := ihB F xks ρ lb hsig hlb { sa with locals := specFrame F.params vs } _ _ hic
    (by show sa.steps ≤ _; simp only [paramsBound]; rw [hpsteps]; unfold Spent at hspa; simp at hspa ⊢; omega)
    (m + 2) (by rw [Nat.mul_succ] at hN; omega)
  have hnames' := nameOwner_kinds cx P fi p hpk hobjs
  have hstSteps : ((xks.map fun xk => (xk.1, Expr.ident (kindName xk.2))).zip ws).foldl
      (fun s a => paramStep a s) ((calleeEnv P fi).push, { ga with steps := k2 }) =
      paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 } := rfl
  rcases hrb : (Spec.eval ds d F.body).run.run { sa with locals := specFrame F.params vs } with ⟨rb, t⟩
  rw [hrb] at hB
  -- What galaspec's call gives, from its body's result.
  have hcall : ∀ r t', r ≠ .ok .unit → (Spec.runIn ds d (specFrame F.params vs) F.body).run.run sa = (r, t') →
      (Spec.callFunc ds d F vs).run.run sa = (r, t') := fun r t' hr hrun =>
    callFunc_run ds d F vs sa (params_typed htys) ρ hres hlenV r t' hrun hr
  -- Both sides return a value: the call's result.
  have hok : ∀ v, RetOK ρ { sa with locals := specFrame F.params vs }
      { (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).2 with defers := [] :: ga.defers } v t
      ((execStmts cx (m + 2) (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).1 lb).run.run
        { (paramsBound xks ws (calleeEnv P fi).push { ga with steps := k2 }).2 with defers := [] :: ga.defers }) →
      (Spec.runIn ds d (specFrame F.params vs) F.body).run.run sa = (.ok v, { t with locals := sa.locals }) →
      ERes3 ρ false s g ((Spec.callFunc ds d F vs).run.run sa)
        ((do Val.single (← callBody cx (m + 3) (calleeEnv P fi) (lowerSigOf xks ρ) (some lb) none ws false)).run.run ga) := by
    intro v hret hrun
    obtain ⟨w, g1, hgo, hvw, hkv, hheap, hglob', hout', hgf1, hsp1⟩ := hret
    have hv1 : v ≠ .unit := by intro h; subst h; cases hvw
    rw [hcall (.ok v) _ (fun h => hv1 (by cases h; rfl)) hrun]
    rw [grun_bind, callBody_ret cx m P fi xks ρ lb ws ga k2 hk2 (argsRel_of_krel hkr) hnames' v w hvw hkv g1
      hgo (by rw [gframe_defers hgf1])]
    obtain ⟨hvd, hnu, -, -⟩ := vrel_default hvw
    refine ⟨defaultVal w, { g1 with defers := ga.defers }, rfl, hvd, hkv, fun _ => hnu, ?_, ?_, ?_⟩
    · obtain ⟨hl1, hg1, ho1⟩ := sframe_parts hsfa
      exact sframe_mk (pre_trans hsfa.1 hheap) hl1 (hglob'.trans hg1) (hout'.trans ho1)
    · exact gframe_trans hgfa (call_frame [] hpst' hgf1)
    · unfold Spent at hspa hsp1 ⊢
      simp only [paramsBound] at hsp1
      rw [hpsteps] at hsp1
      simp only [hs] at hspa ⊢
      simp at hsp1 hspa ⊢
      omega
  cases rb with
  | ok v =>
    have h := hok v hB (by rw [runIn_res, hrb])
    rw [grun_bind] at h
    simp only [calleeEnv] at h
    exact h
  | error e =>
    cases e with
    | ret v =>
      have h := hok v hB (by rw [runIn_res, hrb])
      rw [grun_bind] at h
      simp only [calleeEnv] at h
      exact h
    | panic pc msg =>
      obtain ⟨pv, g1, hgo, hp, ho, hgf1⟩ := hB
      have hcb := callBody_panic cx m P fi xks ρ lb ws ga k2 hk2 (argsRel_of_krel hkr) hnames' pv true g1 hgo
          (by rw [gframe_defers hgf1])
      simp only [calleeEnv] at hcb
      rw [hcall (.error (.panic pc msg)) _ (fun h => nomatch h) (by rw [runIn_res, hrb]), hcb]
      refine panOK_tick ht ⟨pv, _, rfl, hp, ?_, gframe_trans hgfa (call_frame [] hpst' hgf1)⟩
      rw [ho]; exact (sframe_parts hsfa).2.2
    | fuel =>
      rw [hcall (.error .fuel) _ (fun h => nomatch h) (by rw [runIn_res, hrb])]
      trivial
    | stuck m' =>
      rw [hcall (.error (.stuck m')) _ (fun h => nomatch h) (by rw [runIn_res, hrb])]
      trivial
    | unsupported a b =>
      rw [hcall (.error (.unsupported a b)) _ (fun h => nomatch h) (by rw [runIn_res, hrb])]
      trivial

end CallSim

/-! ## Expressions, assembled -/

section ExprAll
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs)

theorem exprSim_zero : ExprSim ds cx P K sg 0 := by
  intro κ e k le _ s g env _ _ N _
  rw [eval_zero]; trivial

/-- **Expressions with calls, at depth `d + 1`**, from the parts at depth `d`. -/
theorem exprSim_succ (d : Nat) (hF : FunRel K sg ds cx P) (hK : 7 ≤ K) (ihE : ExprSim ds cx P K sg d)
    (ihB : BodySim ds cx P K sg d) : ExprSim ds cx P K sg (d + 1) := by
  intro κ e k le hl s g env hi hsg N hN
  have hKN : K ≤ N := by rw [Nat.mul_succ] at hN; omega
  match e, hl with
  | .lit l, hl => exact leaf_sim ds cx P sg K κ _ k le (by simpa [lowerC] using hl) s g env hi _ N hKN
  | .name x, hl => exact leaf_sim ds cx P sg K κ _ k le (by simpa [lowerC] using hl) s g env hi _ N hKN
  | .paren x, hl =>
    cases hc : hasCall x
    · exact leaf_sim ds cx P sg K κ _ k le (by simpa [lowerC, hc] using hl) s g env hi _ N hKN
    · exact paren_sim ds cx P K sg d ihE (by omega) κ x k le hl hc s g env hi hsg N hN
  | .unary op x, hl =>
    cases hc : hasCall x
    · exact leaf_sim ds cx P sg K κ _ k le (by simpa [lowerC, hc] using hl) s g env hi _ N hKN
    · exact unary_sim3 ds cx P K sg d ihE (by omega) κ op x k le hl hc s g env hi hsg N hN
  | .binary op l r, hl =>
    cases hc : (hasCall l || hasCall r)
    · have hl' : lowerLeaf K κ (.binary op l r) = some (k, le) := by
        simp only [lowerC] at hl; rw [hc] at hl; simpa using hl
      exact leaf_sim ds cx P sg K κ _ k le hl' s g env hi _ N hKN
    · by_cases hab : op = "&&" ∨ op = "||"
      · exact andor_sim3 ds cx P K sg d ihE (by omega) κ op hab l r k le hl hc s g env hi hsg N hN
      · simp only [not_or] at hab
        exact binary_sim3 ds cx P K sg d ihE (by omega) κ op l r k le hl hc hab.1 hab.2 s g env hi hsg N hN
  | .call (.name f) args, hl => exact call_sim3 ds cx P K sg d hF hK ihE ihB κ f args k le hl s g env hi hsg N hN
  | .call (.lit _) _, hl | .call (.paren _) _, hl | .call (.unary ..) _, hl | .call (.binary ..) _, hl
  | .call (.call ..) _, hl | .call (.select ..) _, hl | .call (.tuple _) _, hl | .call (.ifExpr ..) _, hl
  | .call (.mtch ..) _, hl | .call (.block _) _, hl | .call (.ifStmt ..) _, hl | .call (.ret _) _, hl
  | .call (.assign ..) _, hl | .call (.incdec ..) _, hl | .call (.decl ..) _, hl | .call (.interp _) _, hl
  | .call (.lambda ..) _, hl | .call (.named ..) _, hl | .call (.unsupported _) _, hl
  | .select .., hl | .tuple _, hl | .ifExpr .., hl | .mtch .., hl | .block _, hl | .ifStmt .., hl
  | .ret _, hl | .assign .., hl | .incdec .., hl | .decl .., hl | .interp _, hl | .lambda .., hl
  | .named .., hl | .unsupported _, hl => simp [lowerC] at hl

end ExprAll

/-! ## Statements with calls -/

section Stmts
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs)

/-- A statement, at galaspec depth `d` and GoCore depth from `K * d` on. -/
def StmtSim (d : Nat) : Prop :=
  ∀ ρ κ st κ' l, lowerS3 K sg ρ κ st = some (κ', l) → ∀ (s : Spec.St) (g : St) (env : Env),
    Inv3 cx P sg κ s.toCore g env → s.steps ≤ g.steps → ∀ N, K * d ≤ N →
      ResL3 cx P sg ρ κ' s g ((Spec.eval ds d st).run.run s) ((execStmt cx N env l).run.run g)

/-- A statement list, from `K * d + 1` on. -/
def ListSim3 (d : Nat) : Prop :=
  ∀ ρ κ ss κ' ls, lowerSs3 K sg ρ κ ss = some (κ', ls) → ∀ (s : Spec.St) (g : St) (env : Env),
    Inv3 cx P sg κ s.toCore g env → s.steps ≤ g.steps → ∀ N, K * d + 1 ≤ N →
      ResL3 cx P sg ρ κ' s g ((Spec.evalBlock ds d ss).run.run s) ((execStmts cx N env ls).run.run g)

/-- A block, from `K * (d - 1) + 2` on. -/
def BlockSim3 (d : Nat) : Prop :=
  ∀ ρ κ ss κ' ls, lowerSs3 K sg ρ κ ss = some (κ', ls) → ∀ (s : Spec.St) (g : St) (env : Env),
    Inv3 cx P sg κ s.toCore g env → s.steps ≤ g.steps → ∀ N, K * d + 2 ≤ N + K →
      ResU3 ρ (fun _ => True) s g ((Spec.eval ds d (.block ss)).run.run s) ((execBlock cx N env ls).run.run g)

/-- A function's block body, from `K * d + 3` on. -/
def TailSim (d : Nat) : Prop :=
  ∀ ρ κ ss ls, lowerTail K sg ρ κ ss = some ls → ∀ (s : Spec.St) (g : St) (env : Env),
    Inv3 cx P sg κ s.toCore g env → s.steps ≤ g.steps → ∀ N, K * d + 3 ≤ N →
      ResB ρ s g ((Spec.evalBlock ds d ss).run.run s) ((execStmts cx N env ls).run.run g)

theorem inv3_push {κ : String → Option Kind} {c : Spec.Core} {g : St} {env : Env}
    (h : Inv3 cx P sg κ c g env) : Inv3 cx P sg κ c g env.push :=
  ⟨inv_push h.inv, h.pkg, h.tenv, fun x hx => by rw [Env.lookup_push]; exact h.closed x hx, h.globals⟩

theorem retOK_trans {ρ : Kind} {s s₁ : Spec.St} {g g₁ : St} (hh : Pre s.heap s₁.heap) (hgl : s₁.globals = s.globals)
    (ho : s₁.out = s.out) (hg : GFrame g g₁) (hsp : Spent s s₁ g g₁) {v s' r}
    (h : RetOK ρ s₁ g₁ v s' r) : RetOK (α := α) ρ s g v s' r := by
  obtain ⟨w, g', hr, hv, hk, hh', hgl', ho', hg', hsp'⟩ := h
  exact ⟨w, g', hr, hv, hk, pre_trans hh hh', hgl'.trans hgl, ho'.trans ho, gframe_trans hg hg',
    spent_trans hsp hsp'⟩

theorem panOK_trans {s s₁ : Spec.St} {g g₁ : St} (ho : s₁.out = s.out) (hg : GFrame g g₁) {pc m s'} {r : Except Stop α × St}
    (h : PanOK s₁ g₁ pc m s' r) : PanOK s g pc m s' r := by
  obtain ⟨pv, g', hr, hp, ho', hg'⟩ := h
  exact ⟨pv, g', hr, hp, ho'.trans ho, gframe_trans hg hg'⟩

theorem blockSim_zero : BlockSim3 ds cx P K sg 0 := by
  intro ρ κ ss κ' ls _ s g env _ _ N _
  rw [eval_zero]; trivial

/-- **A block**, from the statement lists one level down. -/
theorem blockSim_succ (d : Nat) (ihL : ListSim3 ds cx P K sg d) : BlockSim3 ds cx P K sg (d + 1) := by
  intro ρ κ ss κ' ls hl s g env hi hsg N hN
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by rw [Nat.mul_succ] at hN; omega⟩
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; trivial
  | succ k =>
  have ht := ticked_of s k hs
  rw [eval_block_run, tick_succ s k hs]
  simp only
  rw [execBlock_run]
  have h := ihL ρ κ ss κ' ls hl { s with steps := k } g env.push (inv3_push cx P sg hi)
    (by show k ≤ g.steps; omega) n (by rw [Nat.mul_succ] at hN; omega)
  rcases hr : (Spec.evalBlock ds d ss).run.run { s with steps := k } with ⟨r, t⟩
  rw [hr] at h
  cases r with
  | error e =>
    cases e with
    | panic pc m => obtain ⟨pv, g', hg, hp, ho, hgf⟩ := h; rw [hg]; exact panOK_tick ht ⟨pv, g', rfl, hp, ho, hgf⟩
    | ret v =>
      obtain ⟨w, g', hg, rest⟩ := h; rw [hg]; exact retOK_tick ht ⟨w, g', rfl, rest⟩
    | _ => trivial
  | ok v =>
    obtain ⟨env', g', hg, hinv, hh, hgl, ⟨pre, hl'⟩, hgf, hsp⟩ := h
    rw [hg]
    refine ⟨(), g', rfl, trivial, sframe_mk hh ?_ hgl ?_, hgf, ?_⟩
    · show Spec.exitScope t.locals s.locals.length = s.locals
      rw [hl']; simp [Spec.exitScope]
    · show t.out = s.out
      rw [hinv.inv.out, gframe_out hgf]; exact hi.inv.out.symm
    · unfold Spent at hsp ⊢; simp at hsp ⊢; omega

/-- A list, from its statements. -/
theorem resL3_after {ρ : Kind} {κ' : String → Option Kind} {s t : Spec.St} {g g₁ : St}
    (hh : Pre s.heap t.heap) (hgl : t.globals = s.globals) (ho : t.out = s.out) (hg : GFrame g g₁)
    (hsp : Spent s t g g₁) {pre : List (String × Spec.Binding)} (hl : t.locals = pre ++ s.locals)
    {sr : Except Spec.Stop Spec.Val × Spec.St} {r : Except Stop Env × St}
    (h : ResL3 cx P sg ρ κ' t g₁ sr r) : ResL3 cx P sg ρ κ' s g sr r := by
  rcases sr with ⟨_ | v, s'⟩
  · rename_i e
    cases e with
    | panic => exact panOK_trans ho hg h
    | ret => exact retOK_trans hh hgl ho hg hsp h
    | _ => trivial
  · obtain ⟨env', g', hr, hinv, hh', hgl', ⟨pre', hl'⟩, hg', hsp'⟩ := h
    exact ⟨env', g', hr, hinv, pre_trans hh hh', hgl'.trans hgl, ⟨pre' ++ pre, by rw [hl', hl]; simp⟩,
      gframe_trans hg hg', spent_trans hsp hsp'⟩

theorem listSim_of (d : Nat) (ihS : StmtSim ds cx P K sg d) : ListSim3 ds cx P K sg d := by
  intro ρ κ ss
  induction ss generalizing κ with
  | nil =>
    intro κ' ls hl s g env hi hsg N hN
    simp only [lowerSs3, Option.some.injEq, Prod.mk.injEq] at hl
    obtain ⟨rfl, rfl⟩ := hl
    obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by omega⟩
    rw [evalBlock_nil, execStmts_nil]
    exact ⟨env, g, rfl, hi, pre_refl _, rfl, ⟨[], rfl⟩, gframe_refl g, by unfold Spent; omega⟩
  | cons st rest ihl =>
    intro κ' ls hl s g env hi hsg N hN
    simp only [lowerSs3, Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def,
      Option.some.injEq, Prod.mk.injEq] at hl
    obtain ⟨⟨κ₁, l⟩, hst, ⟨κ₂, ls'⟩, hrest, rfl, rfl⟩ := hl
    obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by omega⟩
    have hs := ihS ρ κ st κ₁ l hst s g env hi hsg n (by omega)
    rw [execStmts_cons]
    have hstep : ∀ t, (Spec.eval ds d st).run.run s = t → ∀ (r' : Except Stop Env × St),
        (execStmt cx n env l).run.run g = r' →
        ResL3 cx P sg ρ κ₂ s g (match t with
          | (.ok _, t') => (Spec.evalBlock ds d rest).run.run t'
          | (.error er, t') => (.error er, t'))
          (match r' with
          | (.ok env', g') => (execStmts cx (n + 1) env' ls').run.run g'
          | (.error e, g') => (.error e, g')) := by
      intro t ht r' hr'
      rw [ht] at hs; rw [hr'] at hs
      rcases t with ⟨rt, t⟩
      cases rt with
      | error e =>
        cases e with
        | panic => obtain ⟨pv, g', hg, hp, ho, hgf⟩ := hs; subst hg; exact ⟨pv, g', rfl, hp, ho, hgf⟩
        | ret v => obtain ⟨w, g', hg, rest'⟩ := hs; subst hg; exact ⟨w, g', rfl, rest'⟩
        | _ => trivial
      | ok v =>
        obtain ⟨env', g', hg, hinv, hh, hgl, ⟨pre, hl'⟩, hgf, hsp⟩ := hs
        subst hg
        have ho : t.out = s.out := by rw [hinv.inv.out, gframe_out hgf]; exact hi.inv.out.symm
        exact resL3_after cx P sg hh hgl ho hgf hsp hl'
          (ihl κ₁ κ₂ ls' hrest t g' env' hinv (spent_le hsp hsg) (n + 1) hN)
    cases rest with
    | nil =>
      simp only [lowerSs3, Option.some.injEq, Prod.mk.injEq] at hrest
      obtain ⟨rfl, rfl⟩ := hrest
      rw [evalBlock_one]
      have := hstep _ rfl _ rfl
      rcases hrt : (Spec.eval ds d st).run.run s with ⟨rt, t⟩
      rw [hrt] at this
      rcases hrg : (execStmt cx n env l).run.run g with ⟨rg, g'⟩
      rw [hrg] at this
      cases rt with
      | error e => cases rg <;> exact this
      | ok v =>
        cases rg with
        | error e =>
          simp only at this
          rw [evalBlock_nil] at this
          obtain ⟨_, _, h, _⟩ := this
          cases h
        | ok env' =>
          simp only at this
          rw [evalBlock_nil, execStmts_nil] at this
          exact this
    | cons st' rest' =>
      rw [evalBlock_cons]
      exact hstep _ rfl _ rfl

end Stmts

section Stmts2
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs)

theorem env_bind_parts (env : Env) (x : String) (c : Nat) :
    (env.bind x c).pkg = env.pkg ∧ (env.bind x c).tenv = env.tenv := by
  unfold Env.bind; split
  · exact ⟨rfl, rfl⟩
  · split <;> exact ⟨rfl, rfl⟩

/-- After `val x = e` or `var x = e`: the state relation with `x` of kind `k`. -/
theorem inv3_decl {κ : String → Option Kind} {t : Spec.St} {g : St} {env : Env}
    (hi : Inv3 cx P sg κ t.toCore g env) (m : Spec.Mut) (x : String) (hx_ : x ≠ "_") (hxt : x ≠ "true")
    (hxf : x ≠ "false") (v : Spec.Val) (w : Val) (k : Kind) (hvw : VRel v w) (hkv : valKind v = some k) :
    Inv3 cx P sg (upd κ x k) (declSt m x v t).toCore (declG w g) (env.bind x g.cells.size) := by
  obtain ⟨b, hloc, hbv, hout, hheap⟩ := declSt_facts m x v t hx_ (vrel_not_ref hvw)
  obtain ⟨hvd, hnu, hnb, hns⟩ := vrel_default hvw
  have hglob : (declSt m x v t).globals = t.globals := by cases m <;> rfl
  refine ⟨⟨?_, ?_, predeclared_bind hi.inv.pre x _ hx_ hxt hxf⟩, (env_bind_parts env x _).1.trans hi.pkg,
    (env_bind_parts env x _).2.trans hi.tenv, ?_, ?_⟩
  · intro y k' hk'
    unfold upd at hk'
    by_cases hyx : y = x
    · subst hyx
      simp only [ite_true, Option.some.injEq] at hk'
      subst hk'
      refine ⟨b, g.cells.size, v, defaultVal w, ?_, hbv, ?_, ?_, hvd, hkv, hnu, hnb, hns⟩
      · show Spec.lookup (declSt m y v t).locals y = some b
        rw [hloc]; simp [Spec.lookup]
      · rw [Env.lookup_bind _ _ _ _ hx_]; simp
      · simp [declG]
    · simp only [hyx, ite_false] at hk'
      obtain ⟨b', cell, v', w', hb', hv', hcell, hw', rest⟩ := hi.inv.loc y k' hk'
      refine ⟨b', cell, v', w', ?_, bval_mono hheap hv', ?_, pre_get (pre_push _ _) hw', rest⟩
      · show Spec.lookup (declSt m x v t).locals y = some b'
        rw [hloc]; simp only [Spec.lookup, Ne.symm hyx, ite_false]; exact hb'
      · rw [Env.lookup_bind _ _ _ _ hx_]; simp only [hyx, ite_false]; exact hcell
  · show (declSt m x v t).out = g.out
    rw [hout]; exact hi.inv.out
  · intro y hy
    unfold upd at hy
    by_cases hyx : y = x
    · simp [hyx] at hy
    · simp only [hyx, ite_false] at hy
      obtain ⟨h1, h2⟩ := hi.closed y hy
      refine ⟨?_, ?_⟩
      · show Spec.lookup (declSt m x v t).locals y = none
        rw [hloc]; simp only [Spec.lookup, Ne.symm hyx, ite_false]; exact h1
      · rw [Env.lookup_bind _ _ _ _ hx_]; simp only [hyx, ite_false]; exact h2
  · intro f hf
    show Spec.lookup (declSt m x v t).globals f = none
    rw [hglob]; exact hi.globals f hf

/-- **A declaration**, its expression one level down. -/
theorem decl_sim3 (d : Nat) (ihE : ExprSim ds cx P K sg d) (hK : 3 ≤ K) (ρ : Kind) (κ κ' : String → Option Kind)
    (m : Spec.Mut) (x : String) (e : Spec.Expr) (l : Stmt)
    (hl : lowerS3 K sg ρ κ (.decl m [x] false none [e]) = some (κ', l)) (s : Spec.St) (g : St) (env : Env)
    (hi : Inv3 cx P sg κ s.toCore g env) (hsg : s.steps ≤ g.steps) (N : Nat) (hN : K * (d + 1) ≤ N) :
    ResL3 cx P sg ρ κ' s g ((Spec.eval ds (d + 1) (.decl m [x] false none [e])).run.run s)
      ((execStmt cx N env l).run.run g) := by
  simp only [lowerS3] at hl
  split at hl
  · cases hl
  rename_i hxs
  simp only [not_or] at hxs
  obtain ⟨hx_, hxt, hxf⟩ := hxs
  simp only [Option.map_eq_some_iff, Prod.mk.injEq] at hl
  obtain ⟨⟨k, le⟩, hle, rfl, rfl⟩ := hl
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 3 := ⟨N - 3, by rw [Nat.mul_succ] at hN; omega⟩
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; trivial
  | succ k' =>
  have ht := ticked_of s k' hs
  rw [eval_decl_run ds d k' m x e s hs]
  have hE := ihE κ e k le hle { s with steps := k' } g env hi (by show k' ≤ g.steps; omega) (n + 1)
    (by rw [Nat.mul_succ] at hN; omega)
  rcases hr : (Spec.eval ds d e).run.run { s with steps := k' } with ⟨r, t⟩
  rw [hr] at hE
  cases r with
  | error er =>
    cases er with
    | panic pc msg =>
      obtain ⟨pv, g', hg, hp, ho, hgf⟩ := hE
      rw [execStmt_decl_err cx n env x le g g' _ hg]
      exact panOK_tick ht ⟨pv, g', rfl, hp, ho, hgf⟩
    | ret => exact hE.elim
    | _ => trivial
  | ok v =>
    obtain ⟨w, g', hg, hvw, hkv, -, hsf, hgf, hsp⟩ := hE
    rw [execStmt_decl_ok cx n env x le g g' w hg (results_of_vrel hvw)]
    have hit := inv3_frame hi hsf hgf
    obtain ⟨b, hloc, -, hout, hheap⟩ := declSt_facts m x v t hx_ (vrel_not_ref hvw)
    obtain ⟨hl1, hg1, -⟩ := sframe_parts hsf
    refine ⟨_, _, rfl, inv3_decl cx P sg hit m x hx_ hxt hxf v w k hvw hkv, pre_trans hsf.1 hheap, ?_,
      ⟨[(x, b)], by rw [hloc]; exact congrArg _ hl1⟩, gframe_trans hgf ⟨pre_push _ _, by cases g'; rfl⟩, ?_⟩
    · show (declSt m x v t).globals = s.globals
      cases m <;> exact hg1
    · have : (declSt m x v t).steps = t.steps := by cases m <;> rfl
      unfold Spent at hsp ⊢; simp only [declG] at *; rw [this]; simp at hsp ⊢; omega

/-- **`return e`**, its expression one level down. -/
theorem ret_sim3 (d : Nat) (ihE : ExprSim ds cx P K sg d) (hK : 3 ≤ K) (ρ : Kind) (κ κ' : String → Option Kind)
    (x : Spec.Expr) (l : Stmt) (hl : lowerS3 K sg ρ κ (.ret (some x)) = some (κ', l)) (s : Spec.St) (g : St)
    (env : Env) (hi : Inv3 cx P sg κ s.toCore g env) (hsg : s.steps ≤ g.steps) (N : Nat) (hN : K * (d + 1) ≤ N) :
    ResL3 cx P sg ρ κ' s g ((Spec.eval ds (d + 1) (.ret (some x))).run.run s)
      ((execStmt cx N env l).run.run g) := by
  simp only [lowerS3] at hl
  split at hl
  · rename_i k le hle
    split at hl
    · rename_i hk
      subst hk
      simp only [Option.some.injEq, Prod.mk.injEq] at hl
      obtain ⟨rfl, rfl⟩ := hl
      obtain ⟨n, rfl⟩ : ∃ n, N = n + 3 := ⟨N - 3, by rw [Nat.mul_succ] at hN; omega⟩
      cases hs : s.steps with
      | zero => rw [eval_fuel _ _ _ _ hs]; trivial
      | succ k' =>
      have ht := ticked_of s k' hs
      rw [eval_ret_run ds d k' x s hs, execStmt_ret]
      have hE := ihE κ x k le hle { s with steps := k' } g env hi (by show k' ≤ g.steps; omega) (n + 1)
        (by rw [Nat.mul_succ] at hN; omega)
      rcases hr : (Spec.eval ds d x).run.run { s with steps := k' } with ⟨r, t⟩
      rw [hr] at hE
      cases r with
      | error er =>
        cases er with
        | panic pc msg =>
          obtain ⟨pv, g', hg, hp, ho, hgf⟩ := hE
          rw [hg]; exact panOK_tick ht ⟨pv, g', rfl, hp, ho, hgf⟩
        | ret => exact hE.elim
        | _ => trivial
      | ok v =>
        obtain ⟨w, g', hg, hvw, hkv, -, hsf, hgf, hsp⟩ := hE
        rw [hg]
        simp only
        rw [results_of_vrel hvw]
        obtain ⟨hl1, hg1, ho1⟩ := sframe_parts hsf
        exact retOK_tick ht ⟨w, g', rfl, hvw, hkv, hsf.1, hg1, ho1, hgf, hsp⟩
    · cases hl
  · cases hl

end Stmts2

section Stmts3
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs)

/-- An `if`, at galaspec depth `d` and GoCore depth from `K * d` on: galaspec's locals are
    as they were after it. -/
def IfSim3 (d : Nat) : Prop :=
  ∀ ρ κ c a els κ' l, lowerS3 K sg ρ κ (.ifStmt c a els) = some (κ', l) → ∀ (s : Spec.St) (g : St) (env : Env),
    Inv3 cx P sg κ s.toCore g env → s.steps ≤ g.steps → ∀ N, K * d ≤ N →
      ResU3 ρ (· = env) s g ((Spec.eval ds d (.ifStmt c a els)).run.run s) ((execStmt cx N env l).run.run g)

theorem resU3_withEnv {ρ : Kind} {Q : α → Prop} {s : Spec.St} {g : St} {sr : Except Spec.Stop Spec.Val × Spec.St}
    {r : Except Stop α × St} (env : Env) (h : ResU3 ρ Q s g sr r) : ResU3 ρ (· = env) s g sr (withEnv env r) := by
  rcases sr with ⟨_ | v, s'⟩
  · rename_i e
    cases e with
    | panic => obtain ⟨pv, g', rfl, rest⟩ := h; exact ⟨pv, g', rfl, rest⟩
    | ret => obtain ⟨w, g', rfl, rest⟩ := h; exact ⟨w, g', rfl, rest⟩
    | _ => trivial
  · obtain ⟨a, g', rfl, -, rest⟩ := h
    exact ⟨env, g', rfl, rfl, rest⟩

theorem resU3_after {ρ : Kind} {Q : α → Prop} {s t : Spec.St} {g g₁ : St} (hsf : SFrame s.toCore t.toCore)
    (hg : GFrame g g₁) (hsp : Spent s t g g₁) {sr : Except Spec.Stop Spec.Val × Spec.St} {r : Except Stop α × St}
    (h : ResU3 ρ Q t g₁ sr r) : ResU3 ρ Q s g sr r := by
  obtain ⟨-, hgl, ho⟩ := sframe_parts hsf
  rcases sr with ⟨_ | v, s'⟩
  · rename_i e
    cases e with
    | panic => exact panOK_trans ho hg h
    | ret => exact retOK_trans hsf.1 hgl ho hg hsp h
    | _ => trivial
  · obtain ⟨a, g', hr, hq, hsf', hg', hsp'⟩ := h
    exact ⟨a, g', hr, hq, sframe_trans hsf hsf', gframe_trans hg hg', spent_trans hsp hsp'⟩

theorem resL3_of_resU3 {ρ : Kind} {κ : String → Option Kind} {s : Spec.St} {g : St} {env : Env}
    (hi : Inv3 cx P sg κ s.toCore g env) {sr : Except Spec.Stop Spec.Val × Spec.St} {r : Except Stop Env × St}
    (h : ResU3 ρ (· = env) s g sr r) : ResL3 cx P sg ρ κ s g sr r := by
  rcases sr with ⟨_ | v, s'⟩
  · rename_i e
    cases e <;> first | trivial | exact h
  · obtain ⟨a, g', hr, rfl, hsf, hg, hsp⟩ := h
    obtain ⟨hl, hgl, -⟩ := sframe_parts hsf
    exact ⟨a, g', hr, inv3_frame hi hsf hg, hsf.1, hgl, ⟨[], by simp [hl]⟩, hg, hsp⟩

theorem lowerS3_if {ρ : Kind} {κ κ' : String → Option Kind} {c a : Spec.Expr} {els : Option Spec.Expr} {l : Stmt}
    (h : lowerS3 K sg ρ κ (.ifStmt c a els) = some (κ', l)) :
    κ' = κ ∧ ∃ as lc κ₁ la le, a = .block as ∧ lowerC K sg κ c = some (.bool, lc) ∧
      lowerSs3 K sg ρ κ as = some (κ₁, la) ∧ lowerElse3 K sg ρ κ els = some le ∧ l = .if_ none lc la le := by
  cases a with
  | block as =>
    simp only [lowerS3] at h
    split at h
    · rename_i lc κ₁ la le hc ha he
      simp only [Option.some.injEq, Prod.mk.injEq] at h
      obtain ⟨rfl, rfl⟩ := h
      exact ⟨rfl, as, lc, κ₁, la, le, rfl, hc, ha, he, rfl⟩
    · cases h
  | _ => simp [lowerS3] at h

theorem ifSim_zero : IfSim3 ds cx P K sg 0 := by
  intro ρ κ c a els κ' l _ s g env _ _ N _
  rw [eval_zero]; trivial

/-- **An `if`**, its condition and branches one level down. -/
theorem ifSim_succ (d : Nat) (ihE : ExprSim ds cx P K sg d) (ihB : BlockSim3 ds cx P K sg d)
    (ihI : IfSim3 ds cx P K sg d) (hK : 3 ≤ K) : IfSim3 ds cx P K sg (d + 1) := by
  intro ρ κ c a els κ' l hl s g env hi hsg N hN
  obtain ⟨rfl, as, lc, κ₁, la, le, rfl, hlc, hla, hle, rfl⟩ := lowerS3_if K sg hl
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 2 := ⟨N - 2, by rw [Nat.mul_succ] at hN; omega⟩
  cases hs : s.steps with
  | zero => rw [eval_fuel _ _ _ _ hs]; trivial
  | succ k =>
  have ht := ticked_of s k hs
  have hip := inv3_push cx P sg hi
  have hE := ihE κ' c .bool lc hlc { s with steps := k } g env.push hip (by show k ≤ g.steps; omega) (n + 1)
    (by rw [Nat.mul_succ] at hN; omega)
  rcases hr : (Spec.eval ds d c).run.run { s with steps := k } with ⟨r, t⟩
  rw [hr] at hE
  cases r with
  | error er =>
    rw [eval_ifStmt_err ds d k c _ els s t er hs hr]
    cases er with
    | panic pc msg =>
      obtain ⟨pv, g', hg, hp, ho, hgf⟩ := hE
      rw [execStmt_if_err cx n env lc la le g g' _ hg]
      exact panOK_tick ht ⟨pv, g', rfl, hp, ho, hgf⟩
    | ret => exact hE.elim
    | _ => trivial
  | ok v =>
  obtain ⟨w, g', hg, hvw, hkv, -, hsf, hgf, hsp⟩ := hE
  obtain ⟨b, ty, rfl, rfl, -⟩ := vrel_bool hvw hkv
  have hsf' : SFrame s.toCore t.toCore := hsf
  have hsp' : Spent s t g g' := by unfold Spent at hsp ⊢; simp at hsp; omega
  have hit : Inv3 cx P sg κ' t.toCore g' env.push := inv3_frame hip hsf hgf
  have hstg : t.steps ≤ g'.steps := spent_le hsp (by show k ≤ g.steps; omega)
  cases els with
  | none =>
    simp only [lowerElse3, Option.some.injEq] at hle
    subst hle
    rw [eval_ifStmt_none ds d k c (.block as) b s t hs hr]
    cases b with
    | true =>
      rw [execStmt_if_true cx n env lc la none g g' ty hg]
      simp only [ite_true]
      have hb := ihB ρ κ' as κ₁ la hla t g' env.push hit hstg (n + 1) (by rw [Nat.mul_succ] at hN; omega)
      refine resU3_after hsf' hgf hsp' ?_
      rcases hrb : (Spec.eval ds d (.block as)).run.run t with ⟨rb, tb⟩
      rw [hrb] at hb
      have := resU3_withEnv env hb
      cases rb with
      | ok => exact this
      | error e => exact this
    | false =>
      rw [execStmt_if_false cx n env lc la none g g' ty hg]
      simp only [Bool.false_eq_true, ite_false]
      exact ⟨env, g', rfl, rfl, hsf', hgf, hsp'⟩
  | some e =>
    rw [eval_ifStmt_some ds d k c (.block as) e b s t hs hr]
    refine resU3_after hsf' hgf hsp' ?_
    cases b with
    | true =>
      rw [execStmt_if_true cx n env lc la _ g g' ty hg]
      exact resU3_withEnv env (ihB ρ κ' as κ₁ la hla t g' env.push hit hstg (n + 1)
        (by rw [Nat.mul_succ] at hN; omega))
    | false =>
      rw [execStmt_if_false cx n env lc la _ g g' ty hg]
      simp only [Bool.false_eq_true, ite_false]
      match e, hle with
      | .block bs, hle =>
        simp only [lowerElse3, Option.map_eq_some_iff] at hle
        obtain ⟨⟨κ₂, lb⟩, hlb, rfl⟩ := hle
        simp only
        rw [execStmt_block, withEnv_withEnv]
        exact resU3_withEnv env (ihB ρ κ' bs κ₂ lb hlb t g' env.push hit hstg n
          (by rw [Nat.mul_succ] at hN; omega))
      | .ifStmt c₂ a₂ els₂, hle =>
        simp only [lowerElse3, Option.map_eq_some_iff] at hle
        obtain ⟨⟨κ₂, l₂⟩, hl₂, rfl⟩ := hle
        simp only
        exact resU3_withEnv env (ihI ρ κ' c₂ a₂ els₂ κ₂ l₂ hl₂ t g' env.push hit hstg (n + 1)
          (by rw [Nat.mul_succ] at hN; omega))
      | .lit _, hle | .name _, hle | .unary .., hle | .binary .., hle | .call .., hle
      | .select .., hle | .paren _, hle | .tuple _, hle | .ifExpr .., hle | .mtch .., hle
      | .ret _, hle | .assign .., hle | .incdec .., hle | .decl .., hle | .interp _, hle
      | .lambda .., hle | .named .., hle | .unsupported _, hle => simp [lowerElse3] at hle

theorem stmtSim_zero : StmtSim ds cx P K sg 0 := by
  intro ρ κ st κ' l _ s g env _ _ N _
  rw [eval_zero]; trivial

/-- **A statement** at depth `d + 1`. -/
theorem stmtSim_succ (d : Nat) (ihE : ExprSim ds cx P K sg d) (ihB : BlockSim3 ds cx P K sg (d + 1))
    (ihI : IfSim3 ds cx P K sg (d + 1)) (hK : 3 ≤ K) : StmtSim ds cx P K sg (d + 1) := by
  intro ρ κ st κ' l hl s g env hi hsg N hN
  match st, hl with
  | .decl m [x] false none [e], hl => exact decl_sim3 ds cx P K sg d ihE hK ρ κ κ' m x e l hl s g env hi hsg N hN
  | .ret (some x), hl => exact ret_sim3 ds cx P K sg d ihE hK ρ κ κ' x l hl s g env hi hsg N hN
  | .block ss, hl =>
    simp only [lowerS3, Option.map_eq_some_iff, Prod.mk.injEq] at hl
    obtain ⟨⟨κ₁, lb⟩, hlb, rfl, rfl⟩ := hl
    obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by rw [Nat.mul_succ] at hN; omega⟩
    rw [execStmt_block]
    exact resL3_of_resU3 cx P sg hi (resU3_withEnv env
      (ihB ρ κ ss κ₁ lb hlb s g env hi hsg n (by rw [Nat.mul_succ] at hN ⊢; omega)))
  | .ifStmt c a els, hl =>
    have hκ : κ' = κ := (lowerS3_if K sg hl).1
    subst hκ
    exact resL3_of_resU3 cx P sg hi (ihI ρ κ' c a els _ l hl s g env hi hsg N hN)
  | .decl _ [] _ _ _, hl | .decl _ (_ :: _ :: _) _ _ _, hl | .decl _ [_] true _ _, hl
  | .decl _ [_] false (some _) _, hl | .decl _ [_] false none [], hl
  | .decl _ [_] false none (_ :: _ :: _), hl | .ret none, hl
  | .lit _, hl | .name _, hl | .unary .., hl | .binary .., hl | .call .., hl | .select .., hl
  | .paren _, hl | .tuple _, hl | .ifExpr .., hl | .mtch .., hl | .assign .., hl
  | .incdec .., hl | .interp _, hl | .lambda .., hl | .named .., hl | .unsupported _, hl =>
    simp [lowerS3] at hl

end Stmts3

section Bodies
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs)

theorem lowerLast_cases {ρ : Kind} {κ : String → Option Kind} {e : Spec.Expr} {le : Expr}
    (h : lowerLast K sg ρ κ e = some le) :
    (∃ x, e = .ret (some x) ∧ lowerC K sg κ x = some (ρ, le)) ∨ lowerC K sg κ e = some (ρ, le) := by
  unfold lowerLast at h
  split at h
  · rename_i x
    left; refine ⟨x, rfl, ?_⟩
    split at h
    · rename_i k l hl
      split at h
      · rename_i hk; subst hk; simp only [Option.some.injEq] at h; subst h; exact hl
      · cases h
    · cases h
  · right
    split at h
    · rename_i k l hl
      split at h
      · rename_i hk; subst hk; simp only [Option.some.injEq] at h; subst h; exact hl
      · cases h
    · cases h

theorem resB_after {ρ : Kind} {s t : Spec.St} {g g₁ : St} (hh : Pre s.heap t.heap) (hgl : t.globals = s.globals)
    (ho : t.out = s.out) (hg : GFrame g g₁) (hsp : Spent s t g g₁) {sr : Except Spec.Stop Spec.Val × Spec.St}
    {r : Except Stop Env × St} (h : ResB ρ t g₁ sr r) : ResB ρ s g sr r := by
  rcases sr with ⟨_ | v, s'⟩
  · rename_i e
    cases e with
    | panic => exact panOK_trans ho hg h
    | ret => exact retOK_trans hh hgl ho hg hsp h
    | _ => trivial
  · exact retOK_trans hh hgl ho hg hsp h

theorem execStmts_ret1 (n : Nat) (env : Env) (le : Expr) (g : St) :
    (execStmts cx (n + 4) env [.ret [le]]).run.run g = match (evalExpr cx (n + 1) env le).run.run g with
      | (.ok w, g') => (.error (.ret w.results), g')
      | (.error e, g') => (.error e, g') := by
  rw [execStmts_cons, execStmt_ret]
  rcases (evalExpr cx (n + 1) env le).run.run g with ⟨r, g'⟩
  cases r <;> rfl

/-- **The last element of a body**, returned. -/
theorem last_sim (d : Nat) (ihE : ExprSim ds cx P K sg d) (ihE' : ∀ d', d = d' + 1 → ExprSim ds cx P K sg d')
    (hK : 1 ≤ K) (ρ : Kind) (κ : String → Option Kind) (e : Spec.Expr) (le : Expr)
    (hl : lowerLast K sg ρ κ e = some le) (s : Spec.St) (g : St) (env : Env) (hi : Inv3 cx P sg κ s.toCore g env)
    (hsg : s.steps ≤ g.steps) (N : Nat) (hN : K * d + 3 ≤ N) :
    ResB ρ s g ((Spec.eval ds d e).run.run s) ((execStmts cx N env [.ret [le]]).run.run g) := by
  cases d with
  | zero => rw [eval_zero]; trivial
  | succ d' =>
  obtain ⟨n, rfl⟩ : ∃ n, N = n + 4 := ⟨N - 4, by rw [Nat.mul_succ] at hN; omega⟩
  rw [execStmts_ret1]
  rcases lowerLast_cases K sg hl with ⟨x, rfl, hx⟩ | hx
  ·     cases hs : s.steps with
    | zero => rw [eval_fuel _ _ _ _ hs]; trivial
    | succ k =>
    have ht := ticked_of s k hs
    rw [eval_ret_run ds d' k x s hs]
    have hE := ihE' d' rfl κ x ρ le hx { s with steps := k } g env hi (by show k ≤ g.steps; omega) (n + 1)
      (by rw [Nat.mul_succ] at hN; omega)
    rcases hr : (Spec.eval ds d' x).run.run { s with steps := k } with ⟨r, t⟩
    rw [hr] at hE
    cases r with
    | error er =>
      cases er with
      | panic => obtain ⟨pv, g', hg, hp, ho, hgf⟩ := hE; rw [hg]; exact panOK_tick ht ⟨pv, g', rfl, hp, ho, hgf⟩
      | ret => exact hE.elim
      | _ => trivial
    | ok v =>
      obtain ⟨w, g', hg, hvw, hkv, -, hsf, hgf, hsp⟩ := hE
      rw [hg]; simp only; rw [results_of_vrel hvw]
      obtain ⟨-, hg1, ho1⟩ := sframe_parts hsf
      exact retOK_tick ht ⟨w, g', rfl, hvw, hkv, hsf.1, hg1, ho1, hgf, hsp⟩
  · have hE := ihE κ e ρ le hx s g env hi hsg (n + 1) (by omega)
    rcases hr : (Spec.eval ds (d' + 1) e).run.run s with ⟨r, t⟩
    rw [hr] at hE
    cases r with
    | error er =>
      cases er with
      | panic => obtain ⟨pv, g', hg, hp, ho, hgf⟩ := hE; rw [hg]; exact ⟨pv, g', rfl, hp, ho, hgf⟩
      | ret => exact hE.elim
      | _ => trivial
    | ok v =>
      obtain ⟨w, g', hg, hvw, hkv, -, hsf, hgf, hsp⟩ := hE
      rw [hg]; simp only; rw [results_of_vrel hvw]
      obtain ⟨-, hg1, ho1⟩ := sframe_parts hsf
      exact ⟨w, g', rfl, hvw, hkv, hsf.1, hg1, ho1, hgf, hsp⟩

/-- **A block body**: its statements, then its last element returned. -/
theorem tailSim_of (d : Nat) (ihS : StmtSim ds cx P K sg d) (ihE : ExprSim ds cx P K sg d)
    (ihE' : ∀ d', d = d' + 1 → ExprSim ds cx P K sg d') (hK : 1 ≤ K) : TailSim ds cx P K sg d := by
  intro ρ κ ss
  induction ss generalizing κ with
  | nil => intro ls hl; simp [lowerTail] at hl
  | cons st rest ihl =>
    intro ls hl s g env hi hsg N hN
    cases rest with
    | nil =>
      simp only [lowerTail, Option.map_eq_some_iff] at hl
      obtain ⟨le, hle, rfl⟩ := hl
      rw [evalBlock_one]
      exact last_sim ds cx P K sg d ihE ihE' hK ρ κ st le hle s g env hi hsg N hN
    | cons st' rest' =>
      simp only [lowerTail, Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def,
        Option.some.injEq] at hl
      obtain ⟨⟨κ₁, l⟩, hst, ls', hrest, rfl⟩ := hl
      obtain ⟨n, rfl⟩ : ∃ n, N = n + 1 := ⟨N - 1, by omega⟩
      have hs := ihS ρ κ st κ₁ l hst s g env hi hsg n (by omega)
      rw [evalBlock_cons, execStmts_cons]
      rcases hrs : (Spec.eval ds d st).run.run s with ⟨rs, t⟩
      rw [hrs] at hs
      rcases hrg : (execStmt cx n env l).run.run g with ⟨rg, g'⟩
      rw [hrg] at hs
      cases rs with
      | error e =>
        cases e with
        | panic => obtain ⟨pv, g'', hg, hp, ho, hgf⟩ := hs; rw [hg]; exact ⟨pv, g'', rfl, hp, ho, hgf⟩
        | ret v => obtain ⟨w, g'', hg, rest⟩ := hs; rw [hg]; exact ⟨w, g'', rfl, rest⟩
        | fuel => cases rg <;> trivial
        | stuck => cases rg <;> trivial
        | unsupported => cases rg <;> trivial
      | ok v =>
        obtain ⟨env', g'', hg, hinv, hh, hgl, -, hgf, hsp⟩ := hs
        rw [hg]
        simp only
        have ho : t.out = s.out := by rw [hinv.inv.out, gframe_out hgf]; exact hi.inv.out.symm
        exact resB_after hh hgl ho hgf hsp
          (ihl κ₁ ls' hrest t g'' env' hinv (spent_le hsp hsg) (n + 1) hN)

/-- **A function's body**, from its tail one level down (a block) or its expression. -/
theorem bodySim_of (d : Nat) (ihE : ExprSim ds cx P K sg d) (ihE' : ∀ d', d = d' + 1 → ExprSim ds cx P K sg d')
    (ihT : ∀ d', d = d' + 1 → TailSim ds cx P K sg d') (hK : 1 ≤ K) : BodySim ds cx P K sg d := by
  intro F xks ρ lb hsig hlb s g env hi hsg N hN
  simp only [lowerBody, hsig] at hlb
  split at hlb
  · split at hlb
    · rename_i ss hbody
      rw [hbody]
      cases d with
      | zero => rw [eval_zero]; trivial
      | succ d' =>
      cases hs : s.steps with
      | zero => rw [eval_fuel _ _ _ _ hs]; trivial
      | succ k =>
      have ht := ticked_of s k hs
      rw [eval_block_run, tick_succ s k hs]
      simp only
      have h := ihT d' rfl ρ _ ss lb hlb { s with steps := k } g env hi (by show k ≤ g.steps; omega) N
        (by rw [Nat.mul_succ] at hN; omega)
      rcases hr : (Spec.evalBlock ds d' ss).run.run { s with steps := k } with ⟨r, t⟩
      rw [hr] at h
      cases r with
      | error e =>
        cases e with
        | panic => exact panOK_tick ht h
        | ret => exact retOK_tick ht h
        | _ => trivial
      | ok v =>
        obtain ⟨w, g', hg, hvw, hkv, hh, hgl, ho, hgf, hsp⟩ := h
        exact retOK_tick ht ⟨w, g', hg, hvw, hkv, hh, hgl, ho, hgf, hsp⟩
    · rename_i e hne
      simp only [Option.map_eq_some_iff] at hlb
      obtain ⟨le, hle, rfl⟩ := hlb
      exact last_sim ds cx P K sg d ihE ihE' hK ρ _ F.body le hle s g env hi hsg N hN
  · cases hlb

end Bodies

/-! ## The induction on galaspec's depth, and the theorems -/

section Main
open Gala.GoCore.Full

variable (ds : Spec.Decls) (cx : Ctx) (P : String) (K : Nat) (sg : Sigs)

/-- Every part, at every galaspec depth: induction on the depth. -/
theorem sim3_all (hF : FunRel K sg ds cx P) (hK : 7 ≤ K) : ∀ d,
    ExprSim ds cx P K sg d ∧ BodySim ds cx P K sg d ∧ StmtSim ds cx P K sg d ∧ ListSim3 ds cx P K sg d ∧
      BlockSim3 ds cx P K sg d ∧ IfSim3 ds cx P K sg d ∧ TailSim ds cx P K sg d
  | 0 => by
    have hE := exprSim_zero ds cx P K sg
    have hS := stmtSim_zero ds cx P K sg
    have hT := tailSim_of ds cx P K sg 0 hS hE (fun _ h => by cases h) (by omega)
    exact ⟨hE, bodySim_of ds cx P K sg 0 hE (fun _ h => by cases h) (fun _ h => by cases h) (by omega), hS,
      listSim_of ds cx P K sg 0 hS, blockSim_zero ds cx P K sg, ifSim_zero ds cx P K sg, hT⟩
  | d + 1 => by
    obtain ⟨hE, hB, hS, hL, hBl, hI, hT⟩ := sim3_all hF hK d
    have hE1 := exprSim_succ ds cx P K sg d hF hK hE hB
    have hBl1 := blockSim_succ ds cx P K sg d hL
    have hI1 := ifSim_succ ds cx P K sg d hE hBl hI (by omega)
    have hS1 := stmtSim_succ ds cx P K sg d hE hBl1 hI1 (by omega)
    have hE' : ∀ d', d + 1 = d' + 1 → ExprSim ds cx P K sg d' := fun d' h => by cases h; exact hE
    exact ⟨hE1, bodySim_of ds cx P K sg (d + 1) hE1 hE' (fun d' h => by cases h; exact hT) (by omega), hS1,
      listSim_of ds cx P K sg (d + 1) hS1, hBl1, hI1, tailSim_of ds cx P K sg (d + 1) hS1 hE1 hE' (by omega)⟩

/-- **M5, step 3a: direct calls and `return`.** An expression the reference lowering
    accepts (`lowerC`), calls of the program's functions in it included, evaluated by
    galaspec from a state related to GoCore's (`Inv3`) with no more steps than GoCore has:
    wherever galaspec's evaluation ends with a value, GoCore's evaluation of the lowering, at
    every depth from `K * d` on, ends with a related value of the expression's kind; both
    states only gain cells (and GoCore's otherwise unchanged: `GFrame`, `SFrame`); and GoCore
    spends no more steps than galaspec (`Spent`). Wherever galaspec panics, GoCore panics the
    same way, with equal outputs. An expression never ends with a `return`. Nothing is
    claimed when galaspec runs out of fuel or depth or stops `stuck` or `unsupported`.

    * **The functions**: `FunRel` says GoCore's package `P` declares every function the
      lowering calls (`sg`) as the lowering of galaspec's: parameters and result of kinds
      `int`, `bool`, `string`; positional arguments, one for each parameter (no named
      argument, no default); the body `= e` as `return e`, a block body as its statements
      and `return` of its last element; `return` statements inside.
    * **Depth.** A call's depth in GoCore is not bounded by the program's text (a recursive
      call goes as deep as the run), so the bound is `K * d` for galaspec's depth `d` and a
      `K` of the program: each galaspec level costs GoCore at most `K` levels when `K` is at
      least 7 (a call takes seven) and at least the depth of each call-free part (the
      lowering at `K` accepts only those; `lowerC_mono`: raising `K` loses nothing). The
      claim is for every GoCore depth from `K * d` on: GoCore never runs out of depth there.
    * **Steps.** GoCore takes one step per call, galaspec at least one per evaluation;
      with no more steps than GoCore to start with, galaspec's run that ends has paid for
      every step GoCore takes, so GoCore never runs out of steps where galaspec does not.
    * **Order.** Stated for GoCore's evaluator as it is, left to right; `Body3` and
      `gc_runs3` say when GoCore's model of gc's order runs a lowered body as written. -/
theorem calls_correct (hF : FunRel K sg ds cx P) (hK : 7 ≤ K) (κ : String → Option Kind) (e : Spec.Expr)
    (k : Kind) (le : Expr) (hl : lowerC K sg κ e = some (k, le)) (s : Spec.St) (g : St) (env : Env)
    (hi : Inv3 cx P sg κ s.toCore g env) (hsg : s.steps ≤ g.steps) (d : Nat) :
    ∀ N ≥ K * d, ERes3 k (isLitE e) s g ((Spec.eval ds d e).run.run s) ((evalExpr cx N env le).run.run g) :=
  fun N hN => (sim3_all ds cx P K sg hF hK d).1 κ e k le hl s g env hi hsg N hN

/-- **A function's body**, called: the value galaspec's body gives or returns is the value
    Go's body `return`s, or the same panic; as `calls_correct`. -/
theorem body_correct (hF : FunRel K sg ds cx P) (hK : 7 ≤ K) (F : Spec.Func) (xks : List (String × Kind))
    (ρ : Kind) (lb : List Stmt) (hs : sigOf F = some (xks, ρ)) (hl : lowerBody K sg F = some lb) (s : Spec.St)
    (g : St) (env : Env) (hi : Inv3 cx P sg (paramKinds xks) s.toCore g env) (hsg : s.steps ≤ g.steps) (d : Nat) :
    ∀ N ≥ K * d + 3, ResB ρ s g ((Spec.eval ds d F.body).run.run s) ((execStmts cx N env lb).run.run g) :=
  fun N hN => (sim3_all ds cx P K sg hF hK d).2.1 F xks ρ lb hs hl s g env hi hsg N hN

/-- **Statement lists with calls and `return`**: step 2b's claim (`stmts_correct`) with
    calls in the expressions, and a `return` of the result's kind `ρ` matched by Go's. -/
theorem stmts3_correct (hF : FunRel K sg ds cx P) (hK : 7 ≤ K) (ρ : Kind) (κ κ' : String → Option Kind)
    (ss : List Spec.Expr) (ls : List Stmt) (hl : lowerSs3 K sg ρ κ ss = some (κ', ls)) (s : Spec.St) (g : St)
    (env : Env) (hi : Inv3 cx P sg κ s.toCore g env) (hsg : s.steps ≤ g.steps) (d : Nat) :
    ∀ N ≥ K * d + 1, ResL3 cx P sg ρ κ' s g ((Spec.evalBlock ds d ss).run.run s) ((execStmts cx N env ls).run.run g) :=
  fun N hN => (sim3_all ds cx P K sg hF hK d).2.2.2.1 ρ κ ss κ' ls hl s g env hi hsg N hN

end Main

/-! ## `K` is no restriction: a lowering at `K` is the same at every larger `K` -/

section MonoK

variable {K K' : Nat} (sg : Sigs)

theorem lowerLeaf_mono (hKK : K ≤ K') {κ : String → Option Kind} {e : Spec.Expr} {r : Kind × GoCore.Full.Expr}
    (h : lowerLeaf K κ e = some r) : lowerLeaf K' κ e = some r := by
  unfold lowerLeaf at h ⊢
  split at h
  · have : gdepth e ≤ K' := by omega
    simp only [this, ↓reduceIte]; exact h
  · cases h

mutual
theorem lowerC_mono (hKK : K ≤ K') (κ : String → Option Kind) : ∀ (e : Spec.Expr) (r : Kind × GoCore.Full.Expr),
    lowerC K sg κ e = some r → lowerC K' sg κ e = some r
  | .lit l, r, h => lowerLeaf_mono hKK h
  | .name x, r, h => lowerLeaf_mono hKK h
  | .paren x, r, h => by
    simp only [lowerC] at h ⊢
    split at h
    · rename_i hc
      simp only [hc, ↓reduceIte]
      cases hx : lowerC K sg κ x with
      | none => rw [hx] at h; cases h
      | some p => rw [hx] at h; rw [lowerC_mono hKK κ x p hx]; exact h
    · rename_i hc
      simp only [hc]; exact lowerLeaf_mono hKK h
  | .unary op x, r, h => by
    simp only [lowerC] at h ⊢
    split at h
    · rename_i hc
      simp only [hc, ↓reduceIte]
      cases hx : lowerC K sg κ x with
      | none => rw [hx] at h; cases h
      | some p => rw [hx] at h; rw [lowerC_mono hKK κ x p hx]; exact h
    · rename_i hc
      simp only [hc]; exact lowerLeaf_mono hKK h
  | .binary op l r', r, h => by
    simp only [lowerC] at h ⊢
    split at h
    · rename_i hc
      simp only [hc, ↓reduceIte]
      cases hl : lowerC K sg κ l with
      | none => rw [hl] at h; cases h
      | some p =>
        rw [hl] at h; rw [lowerC_mono hKK κ l p hl]
        cases hr : lowerC K sg κ r' with
        | none => rw [hr] at h; simp at h
        | some q => rw [hr] at h; rw [lowerC_mono hKK κ r' q hr]; exact h
    · rename_i hc
      simp only [hc]; exact lowerLeaf_mono hKK h
  | .call (.name f) args, r, h => by
    simp only [lowerC] at h ⊢
    split at h
    · cases h
    · rename_i hc
      simp only [hc, ↓reduceIte]
      cases hs : sg f with
      | none => rw [hs] at h; cases h
      | some p =>
        rw [hs] at h
        dsimp only [bind, Option.bind] at h ⊢
        cases ha : lowerArgs K sg κ p.1 args with
        | none => rw [ha] at h; cases h
        | some las => rw [ha] at h; rw [lowerArgs_mono hKK κ p.1 args las ha]; exact h
  | .call (.lit _) _, _, h | .call (.paren _) _, _, h | .call (.unary ..) _, _, h | .call (.binary ..) _, _, h
  | .call (.call ..) _, _, h | .call (.select ..) _, _, h | .call (.tuple _) _, _, h | .call (.ifExpr ..) _, _, h
  | .call (.mtch ..) _, _, h | .call (.block _) _, _, h | .call (.ifStmt ..) _, _, h | .call (.ret _) _, _, h
  | .call (.assign ..) _, _, h | .call (.incdec ..) _, _, h | .call (.decl ..) _, _, h | .call (.interp _) _, _, h
  | .call (.lambda ..) _, _, h | .call (.named ..) _, _, h | .call (.unsupported _) _, _, h
  | .select .., _, h | .tuple _, _, h | .ifExpr .., _, h | .mtch .., _, h | .block _, _, h | .ifStmt .., _, h
  | .ret _, _, h | .assign .., _, h | .incdec .., _, h | .decl .., _, h | .interp _, _, h | .lambda .., _, h
  | .named .., _, h | .unsupported _, _, h => by simp [lowerC] at h
theorem lowerArgs_mono (hKK : K ≤ K') (κ : String → Option Kind) : ∀ (ks : List Kind) (args : List Spec.Expr) (las : List GoCore.Full.Expr),
    lowerArgs K sg κ ks args = some las → lowerArgs K' sg κ ks args = some las
  | [], [], las, h => h
  | k :: ks, a :: as, las, h => by
    simp only [lowerArgs] at h ⊢
    split at h
    · cases h
    · rename_i hn
      simp only [hn]
      cases ha : lowerC K sg κ a with
      | none => rw [ha] at h; cases h
      | some p =>
        rw [ha] at h; rw [lowerC_mono hKK κ a p ha]
        simp only at h ⊢
        split at h
        · rename_i hk
          simp only [hk, ↓reduceIte]
          cases hr : lowerArgs K sg κ ks as with
          | none => rw [hr] at h; cases h
          | some ls => rw [hr] at h; rw [lowerArgs_mono hKK κ ks as ls hr]; exact h
        · cases h
  | [], _ :: _, _, h | _ :: _, [], _, h => by simp [lowerArgs] at h
end

end MonoK

section MonoK2

variable {K K' : Nat} (sg : Sigs)

mutual
theorem lowerS3_mono (hKK : K ≤ K') (ρ : Kind) (κ : String → Option Kind) : ∀ (st : Spec.Expr)
    (r : (String → Option Kind) × GoCore.Full.Stmt), lowerS3 K sg ρ κ st = some r → lowerS3 K' sg ρ κ st = some r
  | .decl m [x] false none [e], r, h => by
    simp only [lowerS3] at h ⊢
    split at h
    · cases h
    · rename_i hc
      simp only [hc, ↓reduceIte]
      cases he : lowerC K sg κ e with
      | none => rw [he] at h; cases h
      | some p => rw [he] at h; rw [lowerC_mono sg hKK κ e p he]; exact h
  | .block ss, r, h => by
    simp only [lowerS3] at h ⊢
    cases hs : lowerSs3 K sg ρ κ ss with
    | none => rw [hs] at h; cases h
    | some p => rw [hs] at h; rw [lowerSs3_mono hKK ρ κ ss p hs]; exact h
  | .ifStmt c (.block a) els, r, h => by
    simp only [lowerS3] at h ⊢
    cases hc : lowerC K sg κ c with
    | none => rw [hc] at h; cases h
    | some pc =>
      cases ha : lowerSs3 K sg ρ κ a with
      | none => rw [hc, ha] at h; simp at h
      | some pa =>
        cases he : lowerElse3 K sg ρ κ els with
        | none => rw [hc, ha, he] at h; simp at h
        | some pe =>
          rw [hc, ha, he] at h
          rw [lowerC_mono sg hKK κ c pc hc, lowerSs3_mono hKK ρ κ a pa ha, lowerElse3_mono hKK ρ κ els pe he]
          exact h
  | .ret (some e), r, h => by
    simp only [lowerS3] at h ⊢
    cases he : lowerC K sg κ e with
    | none => rw [he] at h; cases h
    | some p => rw [he] at h; rw [lowerC_mono sg hKK κ e p he]; exact h
  | .ifStmt _ (.lit _) _, _, h | .ifStmt _ (.name _) _, _, h | .ifStmt _ (.paren _) _, _, h
  | .ifStmt _ (.unary ..) _, _, h | .ifStmt _ (.binary ..) _, _, h | .ifStmt _ (.call ..) _, _, h
  | .ifStmt _ (.select ..) _, _, h | .ifStmt _ (.tuple _) _, _, h | .ifStmt _ (.ifExpr ..) _, _, h
  | .ifStmt _ (.mtch ..) _, _, h | .ifStmt _ (.ifStmt ..) _, _, h | .ifStmt _ (.ret _) _, _, h
  | .ifStmt _ (.assign ..) _, _, h | .ifStmt _ (.incdec ..) _, _, h | .ifStmt _ (.decl ..) _, _, h
  | .ifStmt _ (.interp _) _, _, h | .ifStmt _ (.lambda ..) _, _, h | .ifStmt _ (.named ..) _, _, h
  | .ifStmt _ (.unsupported _) _, _, h
  | .decl _ [] _ _ _, _, h | .decl _ (_ :: _ :: _) _ _ _, _, h | .decl _ [_] true _ _, _, h
  | .decl _ [_] false (some _) _, _, h | .decl _ [_] false none [], _, h
  | .decl _ [_] false none (_ :: _ :: _), _, h | .ret none, _, h
  | .lit _, _, h | .name _, _, h | .unary .., _, h | .binary .., _, h | .call .., _, h | .select .., _, h
  | .paren _, _, h | .tuple _, _, h | .ifExpr .., _, h | .mtch .., _, h | .assign .., _, h
  | .incdec .., _, h | .interp _, _, h | .lambda .., _, h | .named .., _, h | .unsupported _, _, h => by
    simp [lowerS3] at h
theorem lowerElse3_mono (hKK : K ≤ K') (ρ : Kind) (κ : String → Option Kind) : ∀ (els : Option Spec.Expr)
    (r : Option GoCore.Full.Stmt), lowerElse3 K sg ρ κ els = some r → lowerElse3 K' sg ρ κ els = some r
  | none, r, h => h
  | some (.block ss), r, h => by
    simp only [lowerElse3] at h ⊢
    cases hs : lowerSs3 K sg ρ κ ss with
    | none => rw [hs] at h; cases h
    | some p => rw [hs] at h; rw [lowerSs3_mono hKK ρ κ ss p hs]; exact h
  | some (.ifStmt c a els), r, h => by
    simp only [lowerElse3] at h ⊢
    cases hs : lowerS3 K sg ρ κ (.ifStmt c a els) with
    | none => rw [hs] at h; cases h
    | some p => rw [hs] at h; rw [lowerS3_mono hKK ρ κ (.ifStmt c a els) p hs]; exact h
  | some (.lit _), _, h | some (.name _), _, h | some (.paren _), _, h | some (.unary ..), _, h
  | some (.binary ..), _, h | some (.call ..), _, h | some (.select ..), _, h | some (.tuple _), _, h
  | some (.ifExpr ..), _, h | some (.mtch ..), _, h | some (.ret _), _, h | some (.assign ..), _, h
  | some (.incdec ..), _, h | some (.decl ..), _, h | some (.interp _), _, h | some (.lambda ..), _, h
  | some (.named ..), _, h | some (.unsupported _), _, h => by simp [lowerElse3] at h
theorem lowerSs3_mono (hKK : K ≤ K') (ρ : Kind) (κ : String → Option Kind) : ∀ (ss : List Spec.Expr)
    (r : (String → Option Kind) × List GoCore.Full.Stmt), lowerSs3 K sg ρ κ ss = some r → lowerSs3 K' sg ρ κ ss = some r
  | [], r, h => h
  | st :: ss, r, h => by
    simp only [lowerSs3, Option.bind_eq_bind, Option.bind_eq_some_iff] at h ⊢
    obtain ⟨⟨κ₁, l⟩, h1, ⟨κ₂, ls⟩, h2, h3⟩ := h
    exact ⟨(κ₁, l), lowerS3_mono hKK ρ κ st _ h1, (κ₂, ls), lowerSs3_mono hKK ρ κ₁ ss _ h2, h3⟩
end

theorem lowerLast_mono (hKK : K ≤ K') (ρ : Kind) (κ : String → Option Kind) (e : Spec.Expr) (le : GoCore.Full.Expr)
    (h : lowerLast K sg ρ κ e = some le) : lowerLast K' sg ρ κ e = some le := by
  rcases lowerLast_cases K sg h with ⟨x, rfl, hx⟩ | hx
  · simp [lowerLast, lowerC_mono sg hKK κ x _ hx]
  · have := lowerC_mono sg hKK κ e _ hx
    unfold lowerLast
    split
    · rename_i x; simp [lowerC] at hx
    · simp [this]

theorem lowerTail_mono (hKK : K ≤ K') (ρ : Kind) : ∀ (κ : String → Option Kind) (ss : List Spec.Expr)
    (ls : List GoCore.Full.Stmt), lowerTail K sg ρ κ ss = some ls → lowerTail K' sg ρ κ ss = some ls
  | _, [], _, h => by simp [lowerTail] at h
  | κ, [e], ls, h => by
    simp only [lowerTail, Option.map_eq_some_iff] at h ⊢
    obtain ⟨le, hle, rfl⟩ := h
    exact ⟨le, lowerLast_mono sg hKK ρ κ e le hle, rfl⟩
  | κ, st :: st' :: ss, ls, h => by
    simp only [lowerTail, Option.bind_eq_bind, Option.bind_eq_some_iff] at h ⊢
    obtain ⟨⟨κ₁, l⟩, h1, ls', h2, h3⟩ := h
    exact ⟨(κ₁, l), lowerS3_mono sg hKK ρ κ st _ h1, ls', lowerTail_mono hKK ρ κ₁ (st' :: ss) ls' h2, h3⟩

/-- **The lowering of a body at `K` is the same at every larger `K`**: `K`, the depth GoCore
    is allowed per galaspec level, excludes no program, as a large enough one lowers it. -/
theorem lowerBody_mono (hKK : K ≤ K') (F : Spec.Func) (lb : List GoCore.Full.Stmt)
    (h : lowerBody K sg F = some lb) : lowerBody K' sg F = some lb := by
  unfold lowerBody at h ⊢
  split at h
  · cases h
  · rename_i xks ρ hs
    split at h
    · rename_i hp
      simp only [hp, ↓reduceIte]
      split at h
      · rename_i ss hb
        exact lowerTail_mono sg hKK ρ _ ss lb h
      · simp only [Option.map_eq_some_iff] at h ⊢
        obtain ⟨le, hle, rfl⟩ := h
        exact ⟨le, lowerLast_mono sg hKK ρ _ F.body le hle, rfl⟩
    · cases h

end MonoK2
