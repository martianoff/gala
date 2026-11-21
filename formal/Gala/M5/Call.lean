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

