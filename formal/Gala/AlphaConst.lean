/-
  # Constant folding and renaming

  `run` folds a program's constant expressions before it runs it (`Gala/Const.lean`), so
  T2 (`run_alpha`, Gala/AlphaProg.lean) needs the folded programs of a program and its
  renaming to be related as the programs are. They are: a constant expression has no
  names, so related expressions are constant together and have the same value
  (`constEval_alpha`), and folding keeps every relation of Gala/Alpha.lean (`foldE_alpha`
  and the theorems after it).
-/
import Gala.AlphaEval

namespace Gala.Spec

variable {pkg : List String} {pm : String → Ren}

/-- Related expressions are constant expressions together, with the same value. -/
theorem constEval_alpha : ∀ {Γ : Ren} {e e' : Expr}, AlphaE pkg pm Γ e e' → constEval e = constEval e'
  | _, _, _, .lit _ => rfl
  | _, _, _, .unary _ h => by simp only [constEval, constEval_alpha h]
  | _, _, _, .binary _ hl hr => by simp only [constEval, constEval_alpha hl, constEval_alpha hr]
  | _, _, _, .paren h => by simp only [constEval, constEval_alpha h]
  | _, _, _, .name _ | _, _, _, .unsupported _ | _, _, _, .callFree .. | _, _, _, .call ..
  | _, _, _, .select .. | _, _, _, .tuple _ | _, _, _, .interp _ | _, _, _, .ifExpr ..
  | _, _, _, .ifStmt .. | _, _, _, .mtch .. | _, _, _, .block _ | _, _, _, .ret _
  | _, _, _, .assign .. | _, _, _, .incdec .. | _, _, _, .lambda .. => by simp [constEval]

/-- What a constant expression is replaced by is related to itself, in any scope. -/
theorem constExpr_alpha (Γ : Ren) (r : Exact.Res CVal) : AlphaE pkg pm Γ (constExpr r) (constExpr r) := by
  unfold constExpr
  split
  · split
    · exact .lit _
    · exact .unsupported _
  all_goals first | exact .lit _ | exact .unsupported _

/-- Folding makes a name only of a name. -/
theorem foldE_name {c : Expr} {f : String} (h : foldE c = .name f) : c = .name f := by
  cases c with
  | name x => simpa [foldE] using h
  | unary op e | paren e =>
    simp only [foldE, foldOp] at h; split at h
    · unfold constExpr at h; repeat' split at h
      all_goals cases h
    · cases h
  | binary op l r =>
    simp only [foldE, foldOp] at h; split at h
    · unfold constExpr at h; repeat' split at h
      all_goals cases h
    · cases h
  | lit _ | unsupported _ => simp [foldE] at h
  | _ => simp [foldE] at h

/-- Folding keeps an argument named or positional. -/
theorem isNamed_foldE (e : Expr) : isNamed (foldE e) = isNamed e := by
  cases e with
  | unary op e | paren e =>
    simp only [foldE, foldOp]; split
    · unfold constExpr; repeat' split
      all_goals rfl
    · rfl
  | binary op l r =>
    simp only [foldE, foldOp]; split
    · unfold constExpr; repeat' split
      all_goals rfl
    · rfl
  | _ => simp [foldE, isNamed]

/-- An operator node folded: its value if it is a constant expression, else the node with
    folded operands; related on both sides when the operands are. -/
private theorem foldOp_alpha {Γ : Ren} {e e' : Expr} {r r' : Unit → Expr}
    (hc : constEval e = constEval e') (hr : AlphaE pkg pm Γ (r ()) (r' ())) :
    AlphaE pkg pm Γ (foldOp e r) (foldOp e' r') := by
  unfold foldOp; rw [hc]
  cases constEval e' with
  | some v => exact constExpr_alpha Γ v
  | none => exact hr

mutual
theorem foldE_alpha : ∀ {Γ : Ren} {e e' : Expr}, AlphaE pkg pm Γ e e' →
    AlphaE pkg pm Γ (foldE e) (foldE e')
  | _, _, _, .lit l => .lit l
  | _, _, _, .name h => .name h
  | _, _, _, .unsupported k => .unsupported k
  | _, _, _, .unary op he => by
    simp only [foldE]; exact foldOp_alpha (constEval_alpha (.unary op he)) (.unary op (foldE_alpha he))
  | _, _, _, .binary op hl hr => by
    simp only [foldE]
    exact foldOp_alpha (constEval_alpha (.binary op hl hr)) (.binary op (foldE_alpha hl) (foldE_alpha hr))
  | _, _, _, .paren he => by
    simp only [foldE]; exact foldOp_alpha (constEval_alpha (.paren he)) (.paren (foldE_alpha he))
  | _, _, _, .callFree hf ha => by simp only [foldE]; exact .callFree hf (foldArgs_alpha ha)
  | _, _, _, .call hc hce ha => by
    simp only [foldE]
    exact .call (fun f hf => hc f (foldE_name hf)) (foldE_alpha hce) (foldArgs_alpha ha)
  | _, _, _, .select f h => by simp only [foldE]; exact .select f (foldE_alpha h)
  | _, _, _, .tuple h => by simp only [foldE]; exact .tuple (foldL_alpha h)
  | _, _, _, .interp h => by simp only [foldE]; exact .interp (foldL_alpha h)
  | _, _, _, .ifExpr hc ha hb => by
    simp only [foldE]; exact .ifExpr (foldE_alpha hc) (foldE_alpha ha) (foldE_alpha hb)
  | _, _, _, .ifStmt hc ha hb => by
    simp only [foldE]; exact .ifStmt (foldE_alpha hc) (foldE_alpha ha) (foldOpt_alpha hb)
  | _, _, _, .mtch hs hc => by simp only [foldE]; exact .mtch (foldE_alpha hs) (foldCases_alpha hc)
  | _, _, _, .block h => by simp only [foldE]; exact .block (foldBlock_alpha h)
  | _, _, _, .ret h => by simp only [foldE]; exact .ret (foldOpt_alpha h)
  | _, _, _, .assign op ht hv => by
    simp only [foldE]; exact .assign op (foldL_alpha ht) (foldL_alpha hv)
  | _, _, _, .incdec op h => by simp only [foldE]; exact .incdec op (foldE_alpha h)
  | _, _, _, .lambda r ht hn hb => by simp only [foldE]; exact .lambda r ht hn (foldE_alpha hb)
theorem foldL_alpha : ∀ {Γ : Ren} {es es' : List Expr}, AlphaL pkg pm Γ es es' →
    AlphaL pkg pm Γ (foldEs es) (foldEs es')
  | _, _, _, .nil => .nil
  | _, _, _, .cons h t => by simp only [foldEs]; exact .cons (foldE_alpha h) (foldL_alpha t)
theorem foldOpt_alpha : ∀ {Γ : Ren} {o o' : Option Expr}, OptR pkg pm Γ o o' →
    OptR pkg pm Γ (foldOE o) (foldOE o')
  | _, _, _, .none => .none
  | _, _, _, .some h => by simp only [foldOE]; exact .some (foldE_alpha h)
theorem foldArgs_alpha : ∀ {m Γ : Ren} {as as' : List Expr}, ArgsR pkg pm m Γ as as' →
    ArgsR pkg pm m Γ (foldEs as) (foldEs as')
  | _, _, _, _, .nil => .nil
  | _, _, _, _, .pos hn h t => by
    simp only [foldEs]
    exact .pos (by rw [isNamed_foldE]; exact hn) (foldE_alpha h) (foldArgs_alpha t)
  | _, _, _, _, .named hm h t => by
    simp only [foldEs, foldE]; exact .named hm (foldE_alpha h) (foldArgs_alpha t)
theorem foldBlock_alpha : ∀ {Γ : Ren} {ss ss' : List Expr}, BlockR pkg pm Γ ss ss' →
    BlockR pkg pm Γ (foldEs ss) (foldEs ss')
  | _, _, _, .nil => .nil
  | _, _, _, .decl m d ty hn hv t => by
    simp only [foldEs, foldE]; exact .decl m d ty hn (foldL_alpha hv) (foldBlock_alpha t)
  | _, _, _, .stmt h t => by simp only [foldEs]; exact .stmt (foldE_alpha h) (foldBlock_alpha t)
theorem foldCases_alpha : ∀ {Γ : Ren} {cs cs' : List (Pat × Option Expr × Expr)},
    CasesR pkg pm Γ cs cs' → CasesR pkg pm Γ (foldCases cs) (foldCases cs')
  | _, _, _, .nil => .nil
  | _, _, _, .cons hp hg hb t => by
    simp only [foldCases]
    exact .cons hp (fun Δ hΔ => foldOpt_alpha (hg Δ hΔ)) (fun Δ hΔ => foldE_alpha (hb Δ hΔ))
      (foldCases_alpha t)
end

/-! ## Declarations -/

/-- The declarations of a folded program (`collect_fold`). -/
def foldDecls (ds : Decls) : Decls :=
  { ds with funcs := ds.funcs.map (fun a => (a.1, foldFunc a.2)),
            structs := ds.structs.map (fun a => (a.1, a.2.map foldField)),
            sealeds := ds.sealeds.map (fun a => (a.1, a.2.map foldVariant)) }

/-- A package-level variable of a folded program. -/
def foldGlobal (g : GlobalDecl) : GlobalDecl := { g with values := foldEs g.values }

def foldAcc (a : Decls × List GlobalDecl) : Decls × List GlobalDecl :=
  (foldDecls a.1, a.2.map foldGlobal)

theorem collectDecl_fold (a : Decls × List GlobalDecl) (d : Decl) :
    collectDecl (foldAcc a) (foldDecl d) = foldAcc (collectDecl a d) := by
  rcases a with ⟨ds, gs⟩
  cases d with
  | func f => simp [collectDecl, foldDecl, foldAcc, foldDecls, foldFunc]
  | struct_ n sh fs => cases sh <;> simp [collectDecl, foldDecl, foldAcc, foldDecls]
  | sealed n vs => simp [collectDecl, foldDecl, foldAcc, foldDecls]
  | global m ns dz t vs => simp [collectDecl, foldDecl, foldAcc, foldGlobal]
  | unsupported k => simp [collectDecl, foldDecl, foldAcc, foldDecls]

private theorem foldl_collectDecl_fold (l : List Decl) (a : Decls × List GlobalDecl) :
    (l.map foldDecl).foldl collectDecl (foldAcc a) = foldAcc (l.foldl collectDecl a) := by
  rw [List.foldl_map]; exact List.foldl_hom foldAcc collectDecl_fold

private theorem foldl_files_fold :
    ∀ (fs : List File) (a : Decls × List GlobalDecl),
      (fs.map fun f => { f with decls := f.decls.map foldDecl }).foldl
          (fun acc file => file.decls.foldl collectDecl acc) (foldAcc a) =
        foldAcc (fs.foldl (fun acc file => file.decls.foldl collectDecl acc) a)
  | [], _ => rfl
  | f :: fs, a => by
    simp only [List.map_cons, List.foldl_cons, foldl_collectDecl_fold]
    exact foldl_files_fold fs _

/-- A folded program's declarations and package-level variables are its own, folded. -/
theorem collect_fold (p : Program) : collect (foldConstants p) = foldAcc (collect p) :=
  foldl_files_fold p.files ({ funcs := [], structs := [], shorthands := [], sealeds := [], unsupported := [] }, [])

private theorem lookup_map_snd {β γ : Type} (g : β → γ) (x : String) :
    ∀ l : List (String × β), (l.map (fun a => (a.1, g a.2))).lookup x = (l.lookup x).map g
  | [] => rfl
  | (k, v) :: l => by
    simp only [List.map_cons, List.lookup_cons]
    split <;> simp_all [lookup_map_snd g x l]

private theorem getD_map_foldOE (l : List (Option Expr)) (j : Nat) :
    (l.map foldOE).getD j none = foldOE (l.getD j none) := by
  simp only [List.getD_eq_getElem?_getD, List.getElem?_map]
  cases l[j]? <;> simp [foldOE]

private theorem variant?_fold (ds : Decls) (x : String) :
    ((foldDecls ds).variant? x).isSome = (ds.variant? x).isSome := by
  unfold Decls.variant? foldDecls
  induction ds.sealeds with
  | nil => rfl
  | cons a l ih =>
    simp only [List.map_cons, List.findSome?_cons]
    have hf : (a.2.map foldVariant).find? (·.name == x) =
        (a.2.find? (·.name == x)).map foldVariant := by
      rw [List.find?_map]; rfl
    rw [hf]
    cases a.2.find? (·.name == x) <;> simp [ih]

theorem funcR_fold {f f' : Func} (h : FuncR pkg pm f f') : FuncR pkg pm (foldFunc f) (foldFunc f') where
  name := h.name
  types := h.types
  names := h.names
  nodup := h.nodup
  map := h.map
  dlen := by simp [foldFunc, h.dlen]
  defaults := fun j => by
    simp only [foldFunc, getD_map_foldOE]; exact foldOpt_alpha (h.defaults j)
  result := h.result
  body := foldE_alpha h.body

theorem fieldR_fold {a b : Field} (h : FieldR pkg pm a b) : FieldR pkg pm (foldField a) (foldField b) :=
  ⟨by rw [h.1], foldOpt_alpha h.2⟩

/-- Folding keeps declarations related. -/
theorem declsR_fold {ds ds' : Decls} (h : DeclsR pkg pm ds ds') :
    DeclsR pkg pm (foldDecls ds) (foldDecls ds') where
  funcs := fall2_map (fun a b hab => ⟨hab.1, hab.2.1, funcR_fold hab.2.2⟩) h.funcs
  noFunc := fun f hf => h.noFunc f (by
    have := lookup_map_snd foldFunc f ds.funcs
    simp only [foldDecls] at hf
    rw [this] at hf
    cases hl : ds.funcs.lookup f <;> simp_all)
  structs := fall2_map (fun a b hab => ⟨hab.1, fall2_map (fun _ _ h => fieldR_fold h) hab.2⟩) h.structs
  shorthands := h.shorthands
  sealeds := fall2_map (fun a b hab => ⟨hab.1, fall2_map (fun v w hvw =>
      ⟨hvw.1, hvw.2.1, show Fall2 _ (v.fields.map foldField) (w.fields.map foldField) from
        fall2_map (fun _ _ h => fieldR_fold h) hvw.2.2⟩) hab.2⟩) h.sealeds
  unsupported := h.unsupported
  inPkg := fun x hx => h.inPkg x (by
    have h1 := lookup_map_snd foldFunc x ds.funcs
    have h2 := lookup_map_snd (List.map foldField) x ds.structs
    simp only [foldDecls] at hx
    rw [h1, h2] at hx
    rw [← variant?_fold]
    simpa [foldDecls, Option.isSome_map] using hx)

end Gala.Spec
