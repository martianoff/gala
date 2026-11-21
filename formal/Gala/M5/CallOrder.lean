/-
  # M5, step 3a: the orders a lowered body with calls allows

  docs/FORMAL_SEMANTICS.MD, "Evaluation order". Step 3a's theorems (`Gala/M5/Call.lean`) are
  about GoCore's evaluator as it is, left to right. GoCore's model of gc 1.25.5's order
  (`Order.gc`, the rewrite in `Gala/GoCore/Hoist.lean`) rewrites a statement only where its
  rewrite's trigger (`Hoist.triggers`) fires on the statement's events. `Body3` says no
  statement of a lowered body fires it, in every scope in which no local is one a call may
  change; `gc_runs3` shows `Order.gc` then runs the body as written.

  In plain words: no statement in which an operand that may panic (a division or modulo by
  something other than a non-zero literal, a shift by something other than a literal)
  precedes a call in the same operand list, a call's arguments being a list of their own
  (`f(x / z) + g()` is allowed: the division is in `f`'s arguments; `x / z + f(x)` and
  `g(x / z, f())` are not). The other half of the trigger, a read of a variable a call may
  change before the call, never fires in these bodies: they take no addresses and have no
  function literals, so no local is changeable (`chSs_noTrig`). Quantifying over scopes is
  conservative: the scope decides only whether `f(...)` is a call, a builtin or a
  conversion, and a conversion never fires the trigger.

  This is about GoCore's model of gc, not real gc: real gc copies every call into a
  temporary, which agrees with the model only where the model's trigger is right; that full
  hoisting agrees with left to right is carried to step 3d.
-/
import Gala.M5.Call
import Gala.M5.StmtOrder

namespace Gala.M5

open Gala.GoCore.Full
open Gala.GoCore.Full.Hoist

/-! ## The shapes of lowered expressions, calls included -/

mutual
/-- A lowered expression's shape in GoCore: literals, names, Go's operators of step 2a, and
    calls of a function by name. -/
def CC : Expr → Prop
  | .ident _ | .intLit _ | .strLit _ => True
  | .paren x => CC x
  | .unary op x => op ∈ ["-", "+", "^", "!"] ∧ CC x
  | .binary _ x y => CC x ∧ CC y
  | .call (.ident _) args false => CCs args
  | _ => False
def CCs : List Expr → Prop
  | [] => True
  | a :: as => CC a ∧ CCs as
end

theorem lowerE_CC (κ : String → Option Kind) : ∀ (e : Spec.Expr) (k : Kind) (le : Expr),
    lowerE κ e = some (k, le) → CC le
  | .lit (.int v), k, le, h => by
    simp only [lowerE, lowerLit, Option.some.injEq, Prod.mk.injEq] at h
    obtain ⟨-, rfl⟩ := h
    split <;> simp [CC]
  | .lit (.bool b), k, le, h => by
    simp only [lowerE, lowerLit, Option.some.injEq, Prod.mk.injEq] at h
    obtain ⟨-, rfl⟩ := h; simp [CC]
  | .lit (.str s), k, le, h => by
    simp only [lowerE, lowerLit, Option.some.injEq, Prod.mk.injEq] at h
    obtain ⟨-, rfl⟩ := h; simp [CC]
  | .name x, k, le, h => by
    simp only [lowerE] at h
    split at h
    · cases h
    simp only [Option.map_eq_some_iff, Prod.mk.injEq] at h
    obtain ⟨_, -, -, rfl⟩ := h; simp [CC]
  | .paren x, k, le, h => by
    simp only [lowerE, Option.map_eq_some_iff] at h
    obtain ⟨⟨k', l'⟩, h', he⟩ := h
    simp only [Prod.mk.injEq] at he; obtain ⟨-, rfl⟩ := he
    exact lowerE_CC κ x k' l' h'
  | .unary op x, k, le, h => by
    simp only [lowerE, Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def, Option.some.injEq,
      Prod.mk.injEq] at h
    obtain ⟨⟨k', l'⟩, h', k'', hk, -, rfl⟩ := h
    refine ⟨?_, lowerE_CC κ x k' l' h'⟩
    unfold unaryKind at hk; split at hk <;> simp_all
  | .binary op l r, k, le, h => by
    simp only [lowerE, Option.bind_eq_bind, Option.bind_eq_some_iff] at h
    obtain ⟨⟨kl, ll⟩, hl, ⟨kr, lr⟩, hr, k', -, h⟩ := h
    split at h
    · cases h
    simp only [Option.pure_def, Option.some.injEq, Prod.mk.injEq] at h
    obtain ⟨-, rfl⟩ := h
    exact ⟨lowerE_CC κ l kl ll hl, lowerE_CC κ r kr lr hr⟩
  | .call .., _, _, h | .select .., _, _, h | .tuple _, _, _, h | .ifExpr .., _, _, h
  | .mtch .., _, _, h | .block _, _, _, h | .ifStmt .., _, _, h | .ret _, _, _, h
  | .assign .., _, _, h | .incdec .., _, _, h | .decl .., _, _, h | .interp _, _, _, h
  | .lambda .., _, _, h | .named .., _, _, h | .unsupported _, _, _, h => by simp [lowerE] at h

theorem lowerLeaf_CC {K : Nat} {κ : String → Option Kind} {e : Spec.Expr} {k : Kind} {le : Expr}
    (h : lowerLeaf K κ e = some (k, le)) : CC le := by
  unfold lowerLeaf at h
  split at h
  · exact lowerE_CC κ e k le (lowerX_some h).2
  · cases h

mutual
theorem lowerC_CC (K : Nat) (sg : Sigs) (κ : String → Option Kind) : ∀ (e : Spec.Expr) (k : Kind) (le : Expr),
    lowerC K sg κ e = some (k, le) → CC le
  | .lit _, _, _, h | .name _, _, _, h => lowerLeaf_CC h
  | .paren x, k, le, h => by
    simp only [lowerC] at h
    split at h
    · simp only [Option.map_eq_some_iff, Prod.mk.injEq] at h
      obtain ⟨⟨k', l⟩, h, -, rfl⟩ := h
      exact lowerC_CC K sg κ x k' l h
    · exact lowerLeaf_CC h
  | .unary op x, k, le, h => by
    simp only [lowerC] at h
    split at h
    · simp only [Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def, Option.some.injEq,
        Prod.mk.injEq] at h
      obtain ⟨⟨k', l⟩, hx, k'', hk, -, rfl⟩ := h
      refine ⟨?_, lowerC_CC K sg κ x k' l hx⟩
      unfold unaryKind at hk; split at hk <;> simp_all
    · exact lowerLeaf_CC h
  | .binary op l r, k, le, h => by
    simp only [lowerC] at h
    split at h
    · simp only [Option.bind_eq_bind, Option.bind_eq_some_iff] at h
      obtain ⟨⟨kl, ll⟩, hl, ⟨kr, lr⟩, hr, k', -, h⟩ := h
      split at h
      · cases h
      simp only [Option.pure_def, Option.some.injEq, Prod.mk.injEq] at h
      obtain ⟨-, rfl⟩ := h
      exact ⟨lowerC_CC K sg κ l kl ll hl, lowerC_CC K sg κ r kr lr hr⟩
    · exact lowerLeaf_CC h
  | .call (.name f) args, k, le, h => by
    simp only [lowerC] at h
    split at h
    · cases h
    simp only [Option.bind_eq_bind, Option.bind_eq_some_iff, Option.pure_def, Option.some.injEq] at h
    obtain ⟨⟨ks, k'⟩, -, las, hlas, hp⟩ := h
    simp only [Prod.mk.injEq] at hp
    obtain ⟨-, rfl⟩ := hp
    exact lowerArgs_CC K sg κ ks args las hlas
  | .call (.lit _) _, _, _, h | .call (.paren _) _, _, _, h | .call (.unary ..) _, _, _, h
  | .call (.binary ..) _, _, _, h | .call (.call ..) _, _, _, h | .call (.select ..) _, _, _, h
  | .call (.tuple _) _, _, _, h | .call (.ifExpr ..) _, _, _, h | .call (.mtch ..) _, _, _, h
  | .call (.block _) _, _, _, h | .call (.ifStmt ..) _, _, _, h | .call (.ret _) _, _, _, h
  | .call (.assign ..) _, _, _, h | .call (.incdec ..) _, _, _, h | .call (.decl ..) _, _, _, h
  | .call (.interp _) _, _, _, h | .call (.lambda ..) _, _, _, h | .call (.named ..) _, _, _, h
  | .call (.unsupported _) _, _, _, h
  | .select .., _, _, h | .tuple _, _, _, h | .ifExpr .., _, _, h | .mtch .., _, _, h | .block _, _, _, h
  | .ifStmt .., _, _, h | .ret _, _, _, h | .assign .., _, _, h | .incdec .., _, _, h | .decl .., _, _, h
  | .interp _, _, _, h | .lambda .., _, _, h | .named .., _, _, h | .unsupported _, _, _, h => by
    simp [lowerC] at h
theorem lowerArgs_CC (K : Nat) (sg : Sigs) (κ : String → Option Kind) : ∀ (ks : List Kind) (args : List Spec.Expr)
    (las : List Expr), lowerArgs K sg κ ks args = some las → CCs las
  | [], [], las, h => by simp only [lowerArgs, Option.some.injEq] at h; subst h; trivial
  | k :: ks, a :: as, las, h => by
    simp only [lowerArgs] at h
    split at h
    · cases h
    split at h
    · rename_i k' l hl
      split at h
      · simp only [Option.map_eq_some_iff] at h
        obtain ⟨ls, hls, rfl⟩ := h
        exact ⟨lowerC_CC K sg κ a k' l hl, lowerArgs_CC K sg κ ks as ls hls⟩
      · cases h
    · cases h
  | [], _ :: _, _, h | _ :: _, [], _, h => by simp [lowerArgs] at h
end

/-! ## With hoisting off, the rewrite leaves them as they are -/

theorem idb {α β : Type} (a : α) (f : α → Id β) : (a >>= f : Id β) = f a := rfl

mutual
theorem hE_cc (s : Scope) (top : Bool) : ∀ (e : Expr), CC e → ∀ h, (hE s false top e).run h = (e, h)
  | .ident _, _, h | .intLit _, _, h | .strLit _, _, h => by simp [hE]; rfl
  | .paren x, hc, h => by
    simp only [hE]
    show (do let x' ← hE s false top x; pure (Expr.paren x')).run h = _
    simp only [StateT.run_bind, hE_cc s top x hc h]; rfl
  | .unary op x, ⟨_, hc⟩, h => by
    rw [hE]
    simp only [StateT.run_bind, hE_cc s false x hc h, Bool.false_and]
    rfl
  | .binary op x y, ⟨hx, hy⟩, h => by
    rw [hE]
    simp [hE_cc s false x hx h]
    show (fun p => (Expr.binary op x p.fst, p.snd)) <$> (hE s false false y).run h = _
    rw [hE_cc s false y hy h]; rfl
  | .call (.ident f) args false, hc, h => by
    rw [hE]
    split
    · simp only [StateT.run_bind, hEs_cc s args hc h, idb, Bool.false_and, Bool.false_eq_true, ite_false]
      rfl
    · simp only [StateT.run_bind, Bool.false_and, Bool.false_eq_true, ite_false]
      have hid : (hE s false false (.ident f)).run h = (.ident f, h) := by simp [hE]; rfl
      simp only [idb, StateT.run_pure, id_pure, hid, hArgs_cc s _ args hc h]
theorem hEs_cc (s : Scope) : ∀ (es : List Expr), CCs es → ∀ h, (hEs s false es).run h = (es, h)
  | [], _, h => rfl
  | e :: es, ⟨he, hes⟩, h => by
    simp only [hEs, StateT.run_bind, hE_cc s false e he h, idb, hEs_cc s es hes h]; rfl
theorem hArgs_cc (s : Scope) (iface : Bool) : ∀ (es : List Expr), CCs es → ∀ h, (hArgs s false iface es).run h = (es, h)
  | [], _, h => rfl
  | e :: es, ⟨he, hes⟩, h => by
    simp only [hArgs, StateT.run_bind, hE_cc s false e he h, Bool.false_and, Bool.false_eq_true, ite_false,
      idb, StateT.run_pure, id_pure, hArgs_cc s iface es hes h]

end


/-! ## Statements no trigger fires on -/

theorem blocked_off {evs : List Ev} (h : triggers evs = false) : blocked evs = none := by
  simp [blocked, h]

theorem hOperands_cc (s : Scope) (top : Bool) (le : Expr) (hc : CC le) (h : HState) :
    (hOperands s false top [le]).run h = ([le], h) := by
  simp only [hOperands, hEs, StateT.run_bind, List.isEmpty_nil, Bool.and_true]
  rw [hE_cc s top le hc h]
  rfl

theorem idbp {α β : Type} (x : Id α) (g : α → β) : (x >>= fun p => pure (g p) : Id β) = g x := rfl


/-- The value of a lowered declaration `var x = e`. -/
def declExpr? : Decl → Option (String × Expr)
  | .value false [.mk [x] none [e]] => some (x, e)
  | _ => none

theorem declExpr?_some {d : Decl} {x : String} {e : Expr} (h : declExpr? d = some (x, e)) :
    d = .value false [.mk [x] none [e]] := by
  unfold declExpr? at h; split at h
  · simp only [Option.some.injEq, Prod.mk.injEq] at h; obtain ⟨rfl, rfl⟩ := h; rfl
  · cases h

/-- The scope after a lowered statement, as gc's rewrite threads it. -/
def afterS (s : Scope) : Stmt → Scope
  | .decl d => match declExpr? d with
    | some (x, _) => s.bind [x]
    | none => s
  | _ => s

mutual
/-- **No statement fires gc's trigger**, in scope `s`: each is a lowered declaration,
    `return`, `if` or block of lowered expressions (`CC`), and the events of its operands
    (`scanOperands`, `scanE`) do not trigger the rewrite (`Hoist.triggers`). -/
def NoTrigS (s : Scope) : Stmt → Prop
  | .decl d => ∃ x e, declExpr? d = some (x, e) ∧ CC e ∧ triggers (scanOperands s true [e]) = false
  | .ret es => ∃ e, es = [e] ∧ CC e ∧ triggers (scanOperands s true [e]) = false
  | .block ss => NoTrigSs s ss
  | .if_ init c b els => init = none ∧ CC c ∧ triggers (scanE s false c) = false ∧ NoTrigSs s b ∧ NoTrigOS s els
  | .expr _ | .incDec .. | .assign .. | .branch _ | .switch .. | .typeSwitch .. | .for_ .. | .range ..
  | .defer_ _ | .empty | .unsupported _ => False
def NoTrigSs (s : Scope) : List Stmt → Prop
  | [] => True
  | st :: ss => NoTrigS s st ∧ NoTrigSs (afterS s st) ss
def NoTrigOS (s : Scope) : Option Stmt → Prop
  | none => True
  | some st => NoTrigS s st
end

mutual
theorem hS_noTrig (s : Scope) : ∀ (st : Stmt), NoTrigS s st → ∀ h, (hS s st).run h = (([st], afterS s st), h)
  | .decl d, hst, h => by
    simp only [NoTrigS] at hst
    obtain ⟨x, le, hd, hc, ht⟩ := hst
    have hd' := declExpr?_some hd
    subst hd'
    simp only [hS, hSpecs, blocked_off ht, ht, StateT.run_bind]
    rw [isolated_run _ [le] (hOperands_cc s true le hc) h]
    rfl
  | .ret es, hst, h => by
    simp only [NoTrigS] at hst
    obtain ⟨le, rfl, hc, ht⟩ := hst
    simp only [hS, blocked_off ht, ht, StateT.run_bind]
    rw [isolated_run _ [le] (hOperands_cc s true le hc) h]
    rfl
  | .block ss, hss, h => by
    simp only [NoTrigS] at hss
    simp only [hS, StateT.run_bind, hSs_noTrig s ss hss h]
    rfl
  | .if_ init lc la els, hst, h => by
    simp only [NoTrigS] at hst
    obtain ⟨rfl, hc, ht, hb, he⟩ := hst
    obtain ⟨s', hes⟩ := hOS_noTrig s els he h
    simp only [hS, hOS, StateT.run_bind, StateT.run_pure, idb, id_pure]
    simp only [blocked_off ht, ht, StateT.run_bind, isolated_run _ lc (hE_cc s false lc hc), idb,
      hSs_noTrig s la hb h, hes]
    cases els <;> rfl
  | .expr _, h', _ | .incDec .., h', _ | .assign .., h', _ | .branch _, h', _ | .switch .., h', _
  | .typeSwitch .., h', _ | .for_ .., h', _ | .range .., h', _ | .defer_ _, h', _
  | .empty, h', _ | .unsupported _, h', _ => by simp only [NoTrigS] at h'
theorem hSs_noTrig (s : Scope) : ∀ (ss : List Stmt), NoTrigSs s ss → ∀ h, (hSs s ss).run h = (ss, h)
  | [], _, h => rfl
  | st :: ss, ⟨h1, h2⟩, h => by
    simp only [hSs, StateT.run_bind, hS_noTrig s st h1 h, idb, hSs_noTrig (afterS s st) ss h2 h]
    rfl
theorem hOS_noTrig (s : Scope) : ∀ (els : Option Stmt), NoTrigOS s els → ∀ h,
    ∃ s', (hOS s els).run h = ((els.toList, s'), h)
  | none, _, h => ⟨s, rfl⟩
  | some st, hst, h => ⟨afterS s st, by simp only [hOS, hS_noTrig s st hst h]; rfl⟩
end


/-! ## No local of these bodies is one a call may change -/

mutual
theorem chE_cc (pm : Bool → String → Bool) (lit : Bool) : ∀ (e : Expr), CC e → chE pm lit e = []
  | .ident _, _ | .intLit _, _ | .strLit _, _ => by simp [chE]
  | .paren x, hc => by simp only [chE]; exact chE_cc pm lit x hc
  | .unary op x, ⟨hop, hc⟩ => by
    have : (op == "&") = false := by
      simp only [List.mem_cons, List.not_mem_nil, or_false] at hop
      rcases hop with rfl | rfl | rfl | rfl <;> rfl
    simp [chE, this, chE_cc pm lit x hc]
  | .binary _ x y, ⟨hx, hy⟩ => by simp [chE, chE_cc pm lit x hx, chE_cc pm lit y hy]
  | .call (.ident f) args false, hc => by simp [chE, chEs_cc pm lit args hc]
theorem chEs_cc (pm : Bool → String → Bool) (lit : Bool) : ∀ (es : List Expr), CCs es → chEs pm lit es = []
  | [], _ => by simp [chEs]
  | e :: es, ⟨he, hes⟩ => by simp [chEs, chE_cc pm lit e he, chEs_cc pm lit es hes]
end

mutual
theorem chS_noTrig (pm : Bool → String → Bool) (s : Scope) : ∀ (st : Stmt), NoTrigS s st → chS pm false st = []
  | .decl d, hst => by
    simp only [NoTrigS] at hst
    obtain ⟨x, e, hd, hc, -⟩ := hst
    rw [declExpr?_some hd]
    simp [chS, chSpecs, chEs, chE_cc pm false e hc]
  | .ret es, hst => by
    simp only [NoTrigS] at hst
    obtain ⟨e, rfl, hc, -⟩ := hst
    simp [chS, chEs, chE_cc pm false e hc]
  | .block ss, hst => by simp only [NoTrigS] at hst; simp only [chS]; exact chSs_noTrig pm s ss hst
  | .if_ init c b els, hst => by
    simp only [NoTrigS] at hst
    obtain ⟨rfl, hc, -, hb, he⟩ := hst
    simp [chS, chOS, chE_cc pm false c hc, chSs_noTrig pm s b hb, chOS_noTrig pm s els he]
  | .expr _, h' | .incDec .., h' | .assign .., h' | .branch _, h' | .switch .., h'
  | .typeSwitch .., h' | .for_ .., h' | .range .., h' | .defer_ _, h'
  | .empty, h' | .unsupported _, h' => by simp only [NoTrigS] at h'
theorem chSs_noTrig (pm : Bool → String → Bool) (s : Scope) : ∀ (ss : List Stmt), NoTrigSs s ss → chSs pm false ss = []
  | [], _ => by simp [chSs]
  | st :: ss, ⟨h1, h2⟩ => by simp [chSs, chS_noTrig pm s st h1, chSs_noTrig pm (afterS s st) ss h2]
theorem chOS_noTrig (pm : Bool → String → Bool) (s : Scope) : ∀ (els : Option Stmt), NoTrigOS s els →
    chOS pm false els = []
  | none, _ => by simp [chOS]
  | some st, h => by simp only [chOS]; exact chS_noTrig pm s st h
end

/-! ## The orders step 3a allows -/

/-- **A body no statement of which fires gc's trigger**, in any scope where no local is one
    a call may change. -/
def Body3 (body : List Stmt) : Prop :=
  ∀ s : Scope, (∀ x, s.changeable.contains x = false) → NoTrigSs s body

/-- **An order step 3a's bodies run as written in:** every function body `Body3` holds of
    is in its place unchanged. -/
def RunsAsWritten3 (ord : Order) : Prop :=
  ∀ p pi fi i name recv tps sig body, Body3 body →
    declAt p pi fi i = some (.func name recv tps sig (some body)) →
    declAt (ord p) pi fi i = some (.func name recv tps sig (some body))

/-- Left to right, GoCore's evaluator as it is, is such an order. -/
theorem leftToRight_runs3 : RunsAsWritten3 Order.leftToRight :=
  fun _ _ _ _ _ _ _ _ _ _ h => h

/-- A scope with nothing in it: no local is changeable there. -/
def emptyScope : Scope := { c := default, pkg := "", file := 0 }

/-- **GoCore's model of gc 1.25.5's order runs `Body3` bodies as written**: no local of
    such a body is changeable (it takes no address, has no function literal), so the scope
    gc's rewrite gives it is one `Body3` speaks of, where no statement fires the trigger,
    and the rewrite leaves every statement as it is. -/
theorem gc_runs3 : RunsAsWritten3 Order.gc := by
  intro p pi fi i name recv tps sig body hb hd
  simp only [declAt, Option.bind_eq_bind, Option.bind_eq_some_iff] at hd
  obtain ⟨pk, hpk, f, hf, hdi⟩ := hd
  simp only [Order.gc, gcOrder, declAt, Option.bind_eq_bind, Option.bind_eq_some_iff,
    StateT.run']
  refine Exists.imp (fun a ⟨h1, h2⟩ => ⟨h1, h2⟩) (mapM_getElem (σ := HState) _ _ p.packages {} pi pk hpk ?_)
  intro h'
  simp only [StateT.run_bind, StateT.run_pure, idbp]
  refine Exists.imp (fun a ⟨h1, h2⟩ => ⟨h1, h2⟩)
    (mapM_getElem (σ := HState) _ _ pk.files.zipIdx h' fi (f, fi) (by simp [List.getElem?_zipIdx, hf]) ?_)
  intro h''
  simp only [StateT.run_bind, StateT.run_pure, idbp]
  have hch : ∀ pm, chSs pm false body = [] := fun pm =>
    chSs_noTrig pm emptyScope body (hb emptyScope (fun x => by simp [emptyScope]))
  obtain ⟨d', hd', hP⟩ := mapM_getElem
    (hDecl (buildCtx p) (structFields (buildCtx p)) (allMethods (buildCtx p)) pk.path fi)
    (· = Decl.func name recv tps sig (some body)) f.decls h'' i _ hdi (by
      intro h3
      simp only [hDecl, StateT.run_bind, StateT.run_pure]
      rw [hSs_noTrig _ body (hb _ (fun x => by cases sig; simp [Scope.withIfaces, Scope.enter, Scope.bind, hch])) h3]
      rfl)
  subst hP
  exact hd'

end Gala.M5
