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

end SpecCall
