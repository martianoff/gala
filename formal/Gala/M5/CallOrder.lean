/-
  # M5, step 3a: the orders a lowered body with calls allows

  docs/FORMAL_SEMANTICS.MD, "Evaluation order". Step 3a's theorems (`Gala/M5/Call.lean`) are
  about GoCore's evaluator as it is, left to right. GoCore's model of gc 1.25.5's order
  (`Order.gc`, the rewrite in `Gala/GoCore/Hoist.lean`) rewrites a statement only where its
  rewrite's trigger (`Hoist.triggers`) fires on the statement's events. `Body3` says no
  statement of a lowered body fires it on `canon`, the events read without a scope;
  `canon_eq` proves those are the model's events in any scope the body fits (`FitsE`: no
  name it reads may be changed by a call there, and each name it calls is a call of a
  single-result function). `gc_runs3` shows `Order.gc` runs a `Body3` body as written when
  it fits the scope the rewrite gives it (`gcScope`), a hypothesis (`FitsSs`) that step 3d
  owes for lowered programs.

  In plain words: no statement in which an operand that may panic (`/` or `%` by a
  non-literal, a shift by a non-literal count) precedes a call in the same operand list, a
  call's arguments being a list of their own (`f(x / z) + g()` is allowed: the division is
  in `f`'s arguments; `x / z + f(x)` and `g(x / z, f())` are not). The other half of the
  trigger, a read of a variable a call may change before the call, is ruled out by
  `FitsSs`, not proved here. Step 3b's IIFE (a function literal called at once) is a call
  like any other, and its body is checked the same way, as a level of its own, in the scope
  the rewrite gives it (`LitsQuiet`).

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
/-- A lowered expression's shape in GoCore: literals, names, Go's operators of step 2a,
    calls of a function by name, and a function literal called at once with no arguments
    (an IIFE: its body is checked statement by statement, `LitsQuiet`). -/
def CC : Expr → Prop
  | .ident _ | .intLit _ | .strLit _ => True
  | .paren x => CC x
  | .unary op x => op ∈ ["-", "+", "^", "!"] ∧ CC x
  | .binary _ x y => CC x ∧ CC y
  | .call (.ident _) args false => CCs args
  | .call (.funcLit _ _) [] false => True
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
  | .ifExpr c a b, k, le, h => by
    obtain ⟨st, -, -, rfl⟩ := lowerC_ifExpr K sg h
    simp [iife, CC]
  | .call (.lit _) _, _, _, h | .call (.paren _) _, _, _, h | .call (.unary ..) _, _, _, h
  | .call (.binary ..) _, _, _, h | .call (.call ..) _, _, _, h | .call (.select ..) _, _, _, h
  | .call (.tuple _) _, _, _, h | .call (.ifExpr ..) _, _, _, h | .call (.mtch ..) _, _, _, h
  | .call (.block _) _, _, _, h | .call (.ifStmt ..) _, _, _, h | .call (.ret _) _, _, _, h
  | .call (.assign ..) _, _, _, h | .call (.incdec ..) _, _, _, h | .call (.decl ..) _, _, _, h
  | .call (.interp _) _, _, _, h | .call (.lambda ..) _, _, _, h | .call (.named ..) _, _, _, h
  | .call (.unsupported _) _, _, _, h
  | .select .., _, _, h | .tuple _, _, _, h | .mtch .., _, _, h | .block _, _, _, h
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

theorem idbp {α β : Type} (x : Id α) (g : α → β) : (x >>= fun p => pure (g p) : Id β) = g x := rfl

/-- The scope gc's rewrite gives a function literal's body. -/
def litScope (s : Scope) (sig : Sig) : Scope := (s.enter sig).withIfaces sig

theorem callKind_funcLit (s : Scope) (sig : Sig) (b : List Stmt) : callKind s (.funcLit sig b) = .call := rfl

mutual
/-- **The rewrite leaves its IIFEs' bodies as they are**, each in the scope it gives it
    (`litScope`). -/
def LitsQuiet (s : Scope) : Expr → Prop
  | .paren x | .unary _ x => LitsQuiet s x
  | .binary _ x y => LitsQuiet s x ∧ LitsQuiet s y
  | .call (.ident _) args _ => LitsQuietArgs s args
  | .call (.funcLit sig b) [] _ => ∀ h, (hSs (litScope s sig) b).run h = (b, h)
  | _ => True
def LitsQuietArgs (s : Scope) : List Expr → Prop
  | [] => True
  | a :: as => LitsQuiet s a ∧ LitsQuietArgs s as
end

mutual
theorem hE_cc (s : Scope) (top : Bool) : ∀ (e : Expr), CC e → LitsQuiet s e → ∀ h, (hE s false top e).run h = (e, h)
  | .ident _, _, _, h | .intLit _, _, _, h | .strLit _, _, _, h => by simp [hE]; rfl
  | .paren x, hc, hq, h => by
    simp only [hE]
    show (do let x' ← hE s false top x; pure (Expr.paren x')).run h = _
    simp only [StateT.run_bind, hE_cc s top x hc hq h]; rfl
  | .unary op x, ⟨_, hc⟩, hq, h => by
    rw [hE]
    simp only [StateT.run_bind, hE_cc s false x hc hq h, Bool.false_and]
    rfl
  | .binary op x y, ⟨hx, hy⟩, ⟨qx, qy⟩, h => by
    rw [hE]
    simp [hE_cc s false x hx qx h]
    show (fun p => (Expr.binary op x p.fst, p.snd)) <$> (hE s false false y).run h = _
    rw [hE_cc s false y hy qy h]; rfl
  | .call (.ident f) args false, hc, hq, h => by
    rw [hE]
    split
    · simp only [StateT.run_bind, hEs_cc s args hc hq h, idb, Bool.false_and, Bool.false_eq_true, ite_false]
      rfl
    · simp only [StateT.run_bind, Bool.false_and, Bool.false_eq_true, ite_false]
      have hid : (hE s false false (.ident f)).run h = (.ident f, h) := by simp [hE]; rfl
      simp only [idb, StateT.run_pure, id_pure, hid, hArgs_cc s _ args hc hq h]
  | .call (.funcLit sig b) [] false, _, hq, h => by
    rw [hE, callKind_funcLit]
    simp only [StateT.run_bind, Bool.false_and, Bool.false_eq_true, ite_false]
    have hid : (hE s false false (.funcLit sig b)).run h = (.funcLit sig b, h) := by
      simp only [hE, StateT.run_bind]
      rw [show (s.enter sig).withIfaces sig = litScope s sig from rfl, hq h]
      rfl
    simp only [idb, StateT.run_pure, id_pure, hid]
    rfl
theorem hEs_cc (s : Scope) : ∀ (es : List Expr), CCs es → LitsQuietArgs s es → ∀ h, (hEs s false es).run h = (es, h)
  | [], _, _, h => rfl
  | e :: es, ⟨he, hes⟩, ⟨qe, qes⟩, h => by
    simp only [hEs, StateT.run_bind, hE_cc s false e he qe h, idb, hEs_cc s es hes qes h]; rfl
theorem hArgs_cc (s : Scope) (iface : Bool) : ∀ (es : List Expr), CCs es → LitsQuietArgs s es → ∀ h,
    (hArgs s false iface es).run h = (es, h)
  | [], _, _, h => rfl
  | e :: es, ⟨he, hes⟩, ⟨qe, qes⟩, h => by
    simp only [hArgs, StateT.run_bind, hE_cc s false e he qe h, Bool.false_and, Bool.false_eq_true, ite_false,
      idb, StateT.run_pure, id_pure, hArgs_cc s iface es hes qes h]
end

/-! ## Statements no trigger fires on -/

theorem blocked_off {evs : List Ev} (h : triggers evs = false) : blocked evs = none := by
  simp [blocked, h]

theorem hOperands_cc (s : Scope) (top : Bool) (le : Expr) (hc : CC le) (hq : LitsQuiet s le) (h : HState) :
    (hOperands s false top [le]).run h = ([le], h) := by
  simp only [hOperands, hEs, StateT.run_bind, List.isEmpty_nil, Bool.and_true]
  rw [hE_cc s top le hc hq h]
  rfl

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
    `return`, `if` or block of lowered expressions (`CC`), the events of its operands
    (`scanOperands`, `scanE`) do not trigger the rewrite (`Hoist.triggers`), and the rewrite
    leaves its IIFEs' bodies as they are (`LitsQuiet`). -/
def NoTrigS (s : Scope) : Stmt → Prop
  | .decl d => ∃ x e, declExpr? d = some (x, e) ∧ CC e ∧ LitsQuiet s e ∧ triggers (scanOperands s true [e]) = false
  | .ret es => ∃ e, es = [e] ∧ CC e ∧ LitsQuiet s e ∧ triggers (scanOperands s true [e]) = false
  | .block ss => NoTrigSs s ss
  | .if_ init c b els => init = none ∧ CC c ∧ LitsQuiet s c ∧ triggers (scanE s false c) = false ∧
      NoTrigSs s b ∧ NoTrigOS s els
  | .expr _ | .incDec .. | .assign .. | .branch _ | .switch .. | .typeSwitch .. | .for_ .. | .range ..
  | .defer_ _ | .empty | .unsupported _ | .localType _ => False
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
    obtain ⟨x, le, hd, hc, hq, ht⟩ := hst
    have hd' := declExpr?_some hd
    subst hd'
    simp only [hS, hSpecs, blocked_off ht, ht, StateT.run_bind]
    rw [isolated_run _ [le] (hOperands_cc s true le hc hq) h]
    rfl
  | .ret es, hst, h => by
    simp only [NoTrigS] at hst
    obtain ⟨le, rfl, hc, hq, ht⟩ := hst
    simp only [hS, blocked_off ht, ht, StateT.run_bind]
    rw [isolated_run _ [le] (hOperands_cc s true le hc hq) h]
    rfl
  | .block ss, hss, h => by
    simp only [NoTrigS] at hss
    simp only [hS, StateT.run_bind, hSs_noTrig s ss hss h]
    rfl
  | .if_ init lc la els, hst, h => by
    simp only [NoTrigS] at hst
    obtain ⟨rfl, hc, hq, ht, hb, he⟩ := hst
    obtain ⟨s', hes⟩ := hOS_noTrig s els he h
    simp only [hS, hOS, StateT.run_bind, StateT.run_pure, idb, id_pure]
    simp only [blocked_off ht, ht, StateT.run_bind, isolated_run _ lc (hE_cc s false lc hc hq), idb,
      hSs_noTrig s la hb h, hes]
    cases els <;> rfl
  | .expr _, h', _ | .incDec .., h', _ | .assign .., h', _ | .branch _, h', _ | .switch .., h', _
  | .typeSwitch .., h', _ | .for_ .., h', _ | .range .., h', _ | .defer_ _, h', _
  | .empty, h', _ | .unsupported _, h', _ | .localType _, h', _ => by simp only [NoTrigS] at h'
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


/-! ## The events, read without a scope -/

mutual
/-- **The events of a lowered expression in a scope it fits** (`canon_eq`): reads change
    nothing, `f(args)` is a call node over `f`'s read and its arguments' events, an IIFE a
    call node with nothing read before it (its body is a function of its own), a division
    or modulo by anything but a non-zero literal may panic dividing, a shift by anything but
    a literal may panic shifting. -/
def canon (top : Bool) : Expr → List Ev
  | .ident _ => [.read false none]
  | .paren x => canon top x
  | .unary _ x => canon false x
  | .binary op x y =>
    if op == "&&" || op == "||" then
      [mkNode false [] [mkNode false [] (canon false x), mkNode false [] (canon false y)]]
    else
      canon false x ++ canon false y ++
        (if (op == "/" || op == "%") && !isNonzeroLit y then [.read false (some .div)]
         else if (op == "<<" || op == ">>") && !isLit y then [.read false (some .shift)]
         else [])
  | .call (.ident _) args _ =>
    if top then [.read false none] ++ canonArgs args
    else [mkNode true [.text] ([.read false none] ++ canonArgs args)]
  | .call (.funcLit _ _) args _ =>
    if top then canonArgs args else [mkNode true [.text] (canonArgs args)]
  | _ => []
def canonArgs : List Expr → List Ev
  | [] => []
  | a :: as => canon false a ++ canonArgs as
end

mutual
/-- **An expression fits a scope**: no name it reads is one a call may change there, each
    name it calls is a call there (not a conversion or builtin) of a function with one
    result, not a variable, and each IIFE has one result and a body that fits the scope the
    rewrite gives it. What gc's model reads then is `canon` (`canon_eq`). -/
def FitsE (s : Scope) : Expr → Prop
  | .ident x => changeableName s x = false
  | .paren x | .unary _ x => FitsE s x
  | .binary _ x y => FitsE s x ∧ FitsE s y
  | .call (.ident f) args _ => callKind s (.ident f) = .call ∧ changeableName s f = false ∧
      singleResult s (.ident f) = true ∧ FitsEs s args
  | .call (.funcLit sig b) args _ => resultCount sig = 1 ∧ FitsSs (litScope s sig) b ∧ FitsEs s args
  | _ => True
def FitsEs (s : Scope) : List Expr → Prop
  | [] => True
  | a :: as => FitsE s a ∧ FitsEs s as
/-- **A body fits a scope**: its statements' expressions do (`FitsE`), each in the scope gc's
    rewrite gives it (a declaration's name bound for the statements after it). -/
def FitsS (s : Scope) : Stmt → Prop
  | .decl (.value false [.mk [_] none [e]]) => FitsE s e
  | .ret es => FitsEs s es
  | .block ss => FitsSs s ss
  | .if_ _ c b els => FitsE s c ∧ FitsSs s b ∧ FitsOS s els
  | _ => True
def FitsSs (s : Scope) : List Stmt → Prop
  | [] => True
  | st :: ss => FitsS s st ∧ FitsSs (afterS s st) ss
def FitsOS (s : Scope) : Option Stmt → Prop
  | none => True
  | some st => FitsS s st
end

theorem readsChangeable_append (a b : List Ev) :
    readsChangeable (a ++ b) = (readsChangeable a || readsChangeable b) := by
  simp [readsChangeable, List.any_append]

mutual
theorem canon_noRead (top : Bool) : ∀ e, CC e → readsChangeable (canon top e) = false
  | .ident _, _ => rfl
  | .intLit _, _ | .strLit _, _ => rfl
  | .paren x, hc => canon_noRead top x hc
  | .unary _ x, ⟨_, hc⟩ => canon_noRead false x hc
  | .binary op x y, ⟨hx, hy⟩ => by
    simp only [canon]
    split
    · rfl
    · simp only [readsChangeable_append, canon_noRead false x hx, canon_noRead false y hy, Bool.false_or]
      split
      · rfl
      · split <;> rfl
  | .call (.ident _) args false, hc => by
    simp only [canon]
    split
    · simp only [readsChangeable_append, canonArgs_noRead args hc]; rfl
    · rfl
  | .call (.funcLit _ _) [] false, _ => by simp only [canon]; split <;> rfl
  | .charLit _, hc | .floatLit .., hc | .composite .., hc | .keyValue .., hc | .funcLit .., hc
  | .selector .., hc | .index .., hc | .sliceExpr .., hc | .typeAssert .., hc | .star _, hc
  | .structType _, hc | .interfaceType _, hc | .funcType _, hc | .arrayType .., hc | .mapType .., hc
  | .ellipsis, hc | .unsupported _, hc | .call (.ident _) _ true, hc | .call (.funcLit _ _) _ true, hc
  | .call (.funcLit _ _) (_ :: _) false, hc => by simp [CC] at hc
  | .call (.intLit _) _ _, hc | .call (.strLit _) _ _, hc | .call (.charLit _) _ _, hc
  | .call (.floatLit ..) _ _, hc | .call (.composite ..) _ _, hc | .call (.keyValue ..) _ _, hc
  | .call (.paren _) _ _, hc | .call (.selector ..) _ _, hc
  | .call (.index ..) _ _, hc | .call (.sliceExpr ..) _ _, hc | .call (.typeAssert ..) _ _, hc
  | .call (.call ..) _ _, hc | .call (.star _) _ _, hc | .call (.unary ..) _ _, hc | .call (.binary ..) _ _, hc
  | .call (.structType _) _ _, hc | .call (.interfaceType _) _ _, hc | .call (.funcType _) _ _, hc
  | .call (.arrayType ..) _ _, hc | .call (.mapType ..) _ _, hc | .call .ellipsis _ _, hc
  | .call (.unsupported _) _ _, hc => by simp [CC] at hc
theorem canonArgs_noRead : ∀ es, CCs es → readsChangeable (canonArgs es) = false
  | [], _ => rfl
  | a :: as, ⟨ha, has⟩ => by simp only [canonArgs, readsChangeable_append, canon_noRead false a ha, canonArgs_noRead as has]; rfl
end

theorem slotEv_off (s : Scope) (e : Expr) (evs : List Ev) (h : readsChangeable evs = false) : slotEv s e evs = [] := by
  unfold slotEv; split
  · rfl
  · simp [h]

/-- The only argument of a call is no call of unknown arity, when it fits. -/
theorem soleUnknown_fits (s : Scope) (args : List Expr) (hc : CCs args) (hfs : FitsEs s args) :
    soleUnknown s args = false := by
  unfold soleUnknown
  split
  · rename_i a
    have hfa := hfs.1
    have hca := (show CCs [a] from hc).1
    unfold unknownArity
    split
    · rename_i fn as sp
      cases fn
      case ident g =>
        simp only [FitsE] at hfa
        simp [hfa.1, hfa.2.2.1]
      case funcLit sig b =>
        simp only [FitsE] at hfa
        simp [callKind_funcLit, singleResult, hfa.1]
      all_goals simp [CC] at hca
    · rfl
  · rfl

mutual
/-- **The bridge**: in a scope a lowered expression fits, gc's model reads `canon`. -/
theorem canon_eq (s : Scope) (top : Bool) : ∀ (e : Expr), CC e → FitsE s e → scanE s top e = canon top e
  | .ident x, _, hf => by simp only [FitsE] at hf; simp [scanE, canon, hf]
  | .intLit _, _, _ | .strLit _, _, _ => by simp [scanE, canon]
  | .paren x, hc, hf => by simp only [scanE, canon]; exact canon_eq s top x hc hf
  | .unary op x, ⟨hop, hc⟩, hf => by
    have : (op == "<-") = false := by
      simp only [List.mem_cons, List.not_mem_nil, or_false] at hop
      rcases hop with rfl | rfl | rfl | rfl <;> rfl
    simp only [scanE, canon, this, Bool.false_eq_true, ite_false]
    exact canon_eq s false x hc hf
  | .binary op x y, ⟨hx, hy⟩, ⟨fx, fy⟩ => by
    simp only [scanE, canon, canon_eq s false x hx fx, canon_eq s false y hy fy]
    split
    · rfl
    · simp only [slotEv_off s x _ (canon_noRead false x hx), slotEv_off s y _ (canon_noRead false y hy)]
      simp
  | .call (.ident f) args false, hc, ⟨hk, hch, hsr, hfs⟩ => by
    simp only [scanE, hk, isPkgFunc, Bool.false_eq_true, ite_false, isMethodSel, soleUnknown_fits s args hc hfs,
      isFmtFunc]
    simp only [hch, canon]
    rw [scanArgs_eq s _ args hc hfs]
  | .call (.funcLit sig b) [] false, _, _ => by
    simp [scanE, callKind_funcLit, isPkgFunc, isMethodSel, soleUnknown, isFmtFunc, scanArgs, canon, canonArgs]
theorem scanArgs_eq (s : Scope) (conv : List Bool) : ∀ (es : List Expr), CCs es → FitsEs s es →
    scanArgs s conv es = canonArgs es
  | [], _, _ => by simp [scanArgs, canonArgs]
  | e :: es, ⟨he, hes⟩, ⟨fe, fes⟩ => by
    simp only [scanArgs, canonArgs, canon_eq s false e he fe, slotEv_off s e _ (canon_noRead false e he),
      scanArgs_eq s conv.tail es hes fes]
    split <;> simp
end


/-! ## The orders step 3a allows -/

mutual
/-- `CC`, as a check, with each IIFE's body a `Body3` body. -/
def ccB : Expr → Bool
  | .ident _ | .intLit _ | .strLit _ => true
  | .paren x => ccB x
  | .unary op x => decide (op ∈ ["-", "+", "^", "!"]) && ccB x
  | .binary _ x y => ccB x && ccB y
  | .call (.ident _) args false => ccBs args
  | .call (.funcLit _ b) [] false => body3Ss b
  | _ => false
def ccBs : List Expr → Bool
  | [] => true
  | a :: as => ccB a && ccBs as
/-- **No statement fires gc's trigger** on `canon`'s events: decidable. -/
def body3S : Stmt → Bool
  | .decl (.value false [.mk [_] none [e]]) => ccB e && !triggers (canon true e)
  | .ret [e] => ccB e && !triggers (canon true e)
  | .block ss => body3Ss ss
  | .if_ init c b els => init.isNone && (ccB c && !triggers (canon false c)) && body3Ss b && body3OS els
  | _ => false
def body3Ss : List Stmt → Bool
  | [] => true
  | st :: ss => body3S st && body3Ss ss
def body3OS : Option Stmt → Bool
  | none => true
  | some st => body3S st
end

mutual
theorem ccB_sound : ∀ (e : Expr), ccB e = true → CC e
  | .ident _, _ | .intLit _, _ | .strLit _, _ => trivial
  | .paren x, h => ccB_sound x h
  | .unary op x, h => by
    simp only [ccB, Bool.and_eq_true, decide_eq_true_eq] at h
    exact ⟨h.1, ccB_sound x h.2⟩
  | .binary _ x y, h => by
    simp only [ccB, Bool.and_eq_true] at h
    exact ⟨ccB_sound x h.1, ccB_sound y h.2⟩
  | .call (.ident _) args false, h => ccBs_sound args h
  | .call (.funcLit _ _) [] false, _ => trivial
  | .charLit _, h | .floatLit .., h | .composite .., h | .keyValue .., h | .funcLit .., h
  | .selector .., h | .index .., h | .sliceExpr .., h | .typeAssert .., h | .star _, h
  | .structType _, h | .interfaceType _, h | .funcType _, h | .arrayType .., h | .mapType .., h
  | .ellipsis, h | .unsupported _, h | .call (.ident _) _ true, h | .call (.funcLit _ _) _ true, h
  | .call (.funcLit _ _) (_ :: _) false, h => by simp [ccB] at h
  | .call (.intLit _) _ _, h | .call (.strLit _) _ _, h | .call (.charLit _) _ _, h
  | .call (.floatLit ..) _ _, h | .call (.composite ..) _ _, h | .call (.keyValue ..) _ _, h
  | .call (.paren _) _ _, h | .call (.selector ..) _ _, h
  | .call (.index ..) _ _, h | .call (.sliceExpr ..) _ _, h | .call (.typeAssert ..) _ _, h
  | .call (.call ..) _ _, h | .call (.star _) _ _, h | .call (.unary ..) _ _, h | .call (.binary ..) _ _, h
  | .call (.structType _) _ _, h | .call (.interfaceType _) _ _, h | .call (.funcType _) _ _, h
  | .call (.arrayType ..) _ _, h | .call (.mapType ..) _ _, h | .call .ellipsis _ _, h
  | .call (.unsupported _) _ _, h => by simp [ccB] at h
theorem ccBs_sound : ∀ (es : List Expr), ccBs es = true → CCs es
  | [], _ => trivial
  | a :: as, h => by
    simp only [ccBs, Bool.and_eq_true] at h
    exact ⟨ccB_sound a h.1, ccBs_sound as h.2⟩
end

/-- **A body step 3a's order result speaks of**: no statement in which an operand that may
    panic (`/` or `%` by a non-literal, a shift by a non-literal count) precedes a call in
    the same operand list, a call's arguments being a list of their own, and the same in
    each IIFE's body. -/
abbrev Body3 (body : List Stmt) : Prop := body3Ss body = true

theorem noTrigE_spec {s : Scope} {top : Bool} {e : Expr} (h : ccB e && !triggers (canon top e) = true)
    (hf : FitsE s e) : CC e ∧ triggers (scanE s top e) = false := by
  simp only [Bool.and_eq_true, Bool.not_eq_true'] at h
  have hc := ccB_sound e h.1
  exact ⟨hc, by rw [canon_eq s top e hc hf]; exact h.2⟩

theorem body3S_decl_shape {d : Decl} (h : body3S (.decl d) = true) :
    ∃ x e, d = .value false [.mk [x] none [e]] ∧ (ccB e && !triggers (canon true e)) = true := by
  rw [body3S.eq_def] at h
  split at h
  · rename_i x e heq
    cases heq
    exact ⟨x, e, rfl, h⟩
  all_goals first | cases h | (rename_i heq; cases heq)

theorem body3S_ret_shape {es : List Expr} (h : body3S (.ret es) = true) :
    ∃ e, es = [e] ∧ (ccB e && !triggers (canon true e)) = true := by
  match es, h with
  | [e], h => exact ⟨e, rfl, h⟩
  | [], h => cases h
  | _ :: _ :: _, h => cases h

mutual
/-- **In a scope it fits, a `Body3` expression's IIFEs are left as they are**. -/
theorem litsQuiet_of (s : Scope) : ∀ (e : Expr), ccB e = true → FitsE s e → LitsQuiet s e
  | .ident _, _, _ | .intLit _, _, _ | .strLit _, _, _ => trivial
  | .paren x, hb, hf => litsQuiet_of s x hb hf
  | .unary _ x, hb, hf => by
    simp only [ccB, Bool.and_eq_true] at hb
    exact litsQuiet_of s x hb.2 hf
  | .binary _ x y, hb, hf => by
    simp only [ccB, Bool.and_eq_true] at hb
    exact ⟨litsQuiet_of s x hb.1 hf.1, litsQuiet_of s y hb.2 hf.2⟩
  | .call (.ident _) args false, hb, hf => litsQuietArgs_of s args hb hf.2.2.2
  | .call (.funcLit sig b) [] false, hb, hf => fun h => hSs_noTrig _ b (noTrigSs_of (litScope s sig) b hb hf.2.1) h
  | .charLit _, hb, _ | .floatLit .., hb, _ | .composite .., hb, _ | .keyValue .., hb, _ | .funcLit .., hb, _
  | .selector .., hb, _ | .index .., hb, _ | .sliceExpr .., hb, _ | .typeAssert .., hb, _ | .star _, hb, _
  | .structType _, hb, _ | .interfaceType _, hb, _ | .funcType _, hb, _ | .arrayType .., hb, _ | .mapType .., hb, _
  | .ellipsis, hb, _ | .unsupported _, hb, _ | .call (.ident _) _ true, hb, _ | .call (.funcLit _ _) _ true, hb, _
  | .call (.funcLit _ _) (_ :: _) false, hb, _ => by simp [ccB] at hb
  | .call (.intLit _) _ _, hb, _ | .call (.strLit _) _ _, hb, _ | .call (.charLit _) _ _, hb, _
  | .call (.floatLit ..) _ _, hb, _ | .call (.composite ..) _ _, hb, _ | .call (.keyValue ..) _ _, hb, _
  | .call (.paren _) _ _, hb, _ | .call (.selector ..) _ _, hb, _
  | .call (.index ..) _ _, hb, _ | .call (.sliceExpr ..) _ _, hb, _ | .call (.typeAssert ..) _ _, hb, _
  | .call (.call ..) _ _, hb, _ | .call (.star _) _ _, hb, _ | .call (.unary ..) _ _, hb, _
  | .call (.binary ..) _ _, hb, _
  | .call (.structType _) _ _, hb, _ | .call (.interfaceType _) _ _, hb, _ | .call (.funcType _) _ _, hb, _
  | .call (.arrayType ..) _ _, hb, _ | .call (.mapType ..) _ _, hb, _ | .call .ellipsis _ _, hb, _
  | .call (.unsupported _) _ _, hb, _ => by simp [ccB] at hb
theorem litsQuietArgs_of (s : Scope) : ∀ (es : List Expr), ccBs es = true → FitsEs s es → LitsQuietArgs s es
  | [], _, _ => trivial
  | a :: as, hb, hf => by
    simp only [ccBs, Bool.and_eq_true] at hb
    exact ⟨litsQuiet_of s a hb.1 hf.1, litsQuietArgs_of s as hb.2 hf.2⟩
/-- **In a scope it fits, a `Body3` statement fires no trigger** (`canon_eq`). -/
theorem noTrigS_of (s : Scope) : ∀ (st : Stmt), body3S st = true → FitsS s st → NoTrigS s st
  | .decl (.value false [.mk [x] none [e]]), hb, hf => by
    simp only [body3S] at hb
    have hb' := hb
    simp only [Bool.and_eq_true] at hb'
    obtain ⟨hc, ht⟩ := noTrigE_spec hb hf
    exact ⟨x, e, rfl, hc, litsQuiet_of s e hb'.1 hf, by simpa [scanOperands] using ht⟩
  | .ret [e], hb, hf => by
    simp only [body3S] at hb
    have hb' := hb
    simp only [Bool.and_eq_true] at hb'
    obtain ⟨hc, ht⟩ := noTrigE_spec hb hf.1
    exact ⟨e, rfl, hc, litsQuiet_of s e hb'.1 hf.1, by simpa [scanOperands] using ht⟩
  | .block ss, hb, hf => noTrigSs_of s ss hb hf
  | .if_ init c b els, hb, hf => by
    simp only [body3S, Bool.and_eq_true, Option.isNone_iff_eq_none] at hb
    obtain ⟨⟨⟨hi, hcb⟩, hbb⟩, heb⟩ := hb
    obtain ⟨hfc, hfb, hfe⟩ := hf
    obtain ⟨hc, ht⟩ := noTrigE_spec (by simpa using hcb) hfc
    exact ⟨hi, hc, litsQuiet_of s c hcb.1 hfc, ht, noTrigSs_of s b hbb hfb, noTrigOS_of s els heb hfe⟩
  | .decl (.func ..), hb, _ | .decl (.type _), hb, _ | .decl (.unsupported _), hb, _
  | .decl (.value true _), hb, _ | .decl (.value false []), hb, _ | .decl (.value false (_ :: _ :: _)), hb, _
  | .decl (.value false [.mk [] _ _]), hb, _ | .decl (.value false [.mk (_ :: _ :: _) _ _]), hb, _
  | .decl (.value false [.mk [_] (some _) _]), hb, _ | .decl (.value false [.mk [_] none []]), hb, _
  | .decl (.value false [.mk [_] none (_ :: _ :: _)]), hb, _ =>
    by obtain ⟨_, _, hd, -⟩ := body3S_decl_shape hb; cases hd
  | .ret [], hb, _ | .ret (_ :: _ :: _), hb, _ => by obtain ⟨_, hd, -⟩ := body3S_ret_shape hb; cases hd
  | .expr _, hb, _ | .incDec .., hb, _ | .assign .., hb, _ | .branch _, hb, _ | .switch .., hb, _
  | .typeSwitch .., hb, _ | .for_ .., hb, _ | .range .., hb, _ | .defer_ _, hb, _
  | .empty, hb, _ | .unsupported _, hb, _ | .localType _, hb, _ => by simp [body3S] at hb
theorem noTrigSs_of (s : Scope) : ∀ (ss : List Stmt), body3Ss ss = true → FitsSs s ss → NoTrigSs s ss
  | [], _, _ => trivial
  | st :: ss, hb, ⟨h1, h2⟩ => by
    simp only [body3Ss, Bool.and_eq_true] at hb
    exact ⟨noTrigS_of s st hb.1 h1, noTrigSs_of (afterS s st) ss hb.2 h2⟩
theorem noTrigOS_of (s : Scope) : ∀ (els : Option Stmt), body3OS els = true → FitsOS s els → NoTrigOS s els
  | none, _, _ => trivial
  | some st, hb, hf => noTrigS_of s st hb hf
end

/-- The scope gc's rewrite gives function `body` of package `pkg`, file `fi` of `p`: `Hoist.funcScope`,
    as `Hoist.hDecl` builds it over the program's tables. -/
abbrev gcScope (p : Program) (pkg : String) (fi : Nat) (recv : Option Field) (tps : List Field) (sig : Sig)
    (body : List Stmt) : Scope :=
  let c := buildCtx p
  funcScope c (structFields c) (allMethods c) pkg fi recv tps sig body
/-- **An order step 3a's bodies run as written in**: every `Body3` function body that fits
    the scope gc's rewrite gives it is in its place unchanged. That the lowered programs'
    bodies fit is owed by step 3d (from `FunRel` and `buildCtx`). -/
def RunsAsWritten3 (ord : Order) : Prop :=
  ∀ p pi fi i pk name recv tps sig body, p.packages[pi]? = some pk → Body3 body →
    FitsSs (gcScope p pk.path fi recv tps sig body) body →
    declAt p pi fi i = some (.func name recv tps sig (some body)) →
    declAt (ord p) pi fi i = some (.func name recv tps sig (some body))

/-- Left to right, GoCore's evaluator as it is, is such an order. -/
theorem leftToRight_runs3 : RunsAsWritten3 Order.leftToRight :=
  fun _ _ _ _ _ _ _ _ _ _ _ _ _ h => h

/-- **GoCore's model of gc 1.25.5's order runs such bodies as written**: in the scope it
    gives a fitting `Body3` body, no statement fires the trigger (`noTrigSs_of`), and the
    rewrite leaves each as it is (`hSs_noTrig`). -/
theorem gc_runs3 : RunsAsWritten3 Order.gc := by
  intro p pi fi i pk0 name recv tps sig body hpk0 hb hfit hd
  simp only [declAt, Option.bind_eq_bind, Option.bind_eq_some_iff] at hd
  obtain ⟨pk, hpk, f, hf, hdi⟩ := hd
  rw [hpk0] at hpk
  cases hpk
  simp only [Order.gc, gcOrder, declAt, Option.bind_eq_bind, Option.bind_eq_some_iff,
    StateT.run']
  refine Exists.imp (fun a ⟨h1, h2⟩ => ⟨h1, h2⟩) (mapM_getElem (σ := HState) _ _ p.packages {} pi pk0 hpk0 ?_)
  intro h'
  simp only [StateT.run_bind, StateT.run_pure, idbp]
  refine Exists.imp (fun a ⟨h1, h2⟩ => ⟨h1, h2⟩)
    (mapM_getElem (σ := HState) _ _ pk0.files.zipIdx h' fi (f, fi) (by simp [List.getElem?_zipIdx, hf]) ?_)
  intro h''
  simp only [StateT.run_bind, StateT.run_pure, idbp]
  obtain ⟨d', hd', hP⟩ := mapM_getElem
    (hDecl (buildCtx p) (structFields (buildCtx p)) (allMethods (buildCtx p)) pk0.path fi)
    (· = Decl.func name recv tps sig (some body)) f.decls h'' i _ hdi (by
      intro h3
      simp only [hDecl, StateT.run_bind, StateT.run_pure]
      have hid := hSs_noTrig _ body (noTrigSs_of _ body hb hfit) h3
      rw [hid]
      rfl)
  subst hP
  exact hd'

end Gala.M5
