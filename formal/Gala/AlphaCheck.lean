/-
  # Deciding the renaming relation

  `alphaCheck p p'` decides whether `p'` is `p` with its binders renamed in the way T2
  covers (`AlphaProgram`, Gala/AlphaProg.lean). It is sound, `alphaCheck_sound`: when it
  answers `true` the programs are related, so they print the same output and end the same
  way (`run_alpha`). It is not claimed complete: where the relation leaves a choice (a
  pattern name kept the same that could also count as a renamed one), it makes one.

  `galaspec alpha a.json b.json` runs it (Gala/Cli.lean). The renamings and pattern slots
  are defined here, in the interpreter, so that the checker can use them; Gala/Alpha.lean
  defines the relations on top of them.
-/
import Gala.Eval

namespace Gala.Spec

/-! ## Renamings -/

/-- The binders in scope, newest first: each binder's name in the one program and in the
    other. -/
abbrev Ren := List (String × String)

/-- Push a binder, as `bind` does: `_` binds nothing. -/
def Ren.push (Γ : Ren) (xx : String × String) : Ren := if xx.1 = "_" then Γ else xx :: Γ

/-- Push binders in order, the last newest, as `pushAll` and a function's frame do. -/
def Ren.pushAll (Γ : Ren) (xs : Ren) : Ren := xs.foldl Ren.push Γ

/-- A bare pattern name's part in a renaming (Gala/Alpha.lean, "Patterns"). -/
inductive Slot
  /-- Always binds: `x` on the one side, `x'` on the other. -/
  | bind (x x' : String)
  /-- Binds `x` on both sides, or on neither. -/
  | maybe (x : String)

/-- The binders a pattern's slots can push, for each way the `maybe`s go. -/
def Slot.outs : List Slot → List Ren
  | [] => [[]]
  | .bind x x' :: ss => (Slot.outs ss).map ((x, x') :: ·)
  | .maybe x :: ss => (Slot.outs ss).map ((x, x) :: ·) ++ Slot.outs ss

namespace Check

/-! ## Equality of syntax

`Expr` is a nested inductive, for which Lean derives no `DecidableEq`. -/

mutual
def tyEq : Ty → Ty → Bool
  | .name a, .name b => a == b
  | .func ps r, .func ps' r' => tysEq ps ps' && optTyEq r r'
  | .unsupported a, .unsupported b => a == b
  | _, _ => false
def tysEq : List Ty → List Ty → Bool
  | [], [] => true
  | t :: ts, t' :: ts' => tyEq t t' && tysEq ts ts'
  | _, _ => false
def optTyEq : Option Ty → Option Ty → Bool
  | none, none => true
  | some t, some t' => tyEq t t'
  | _, _ => false
end

def optTysEq : List (Option Ty) → List (Option Ty) → Bool
  | [], [] => true
  | t :: ts, t' :: ts' => optTyEq t t' && optTysEq ts ts'
  | _, _ => false

def litEq : Lit → Lit → Bool
  | .int a, .int b => a == b
  | .bool a, .bool b => a == b
  | .str a, .str b => a == b
  | _, _ => false

mutual
def patEq : Pat → Pat → Bool
  | .wild, .wild => true
  | .name a, .name b => a == b
  | .lit l, .lit l' => litEq l l'
  | .tuple ps, .tuple ps' => patsEq ps ps'
  | .extract c ps, .extract c' ps' => c == c' && patsEq ps ps'
  | .unsupported a, .unsupported b => a == b
  | _, _ => false
def patsEq : List Pat → List Pat → Bool
  | [], [] => true
  | p :: ps, p' :: ps' => patEq p p' && patsEq ps ps'
  | _, _ => false
end

def paramsEq : List Param → List Param → Bool
  | [], [] => true
  | p :: ps, p' :: ps' => p.name == p'.name && optTyEq p.type p'.type && paramsEq ps ps'
  | _, _ => false

mutual
def exprEq : Expr → Expr → Bool
  | .lit l, .lit l' => litEq l l'
  | .name a, .name b => a == b
  | .unary o e, .unary o' e' => o == o' && exprEq e e'
  | .binary o l r, .binary o' l' r' => o == o' && exprEq l l' && exprEq r r'
  | .call c as, .call c' as' => exprEq c c' && exprsEq as as'
  | .select t f, .select t' f' => f == f' && exprEq t t'
  | .paren e, .paren e' => exprEq e e'
  | .tuple es, .tuple es' => exprsEq es es'
  | .ifExpr c a b, .ifExpr c' a' b' => exprEq c c' && exprEq a a' && exprEq b b'
  | .mtch s cs, .mtch s' cs' => exprEq s s' && casesEq cs cs'
  | .block ss, .block ss' => exprsEq ss ss'
  | .ifStmt c a b, .ifStmt c' a' b' => exprEq c c' && exprEq a a' && optExprEq b b'
  | .ret v, .ret v' => optExprEq v v'
  | .assign o ts vs, .assign o' ts' vs' => o == o' && exprsEq ts ts' && exprsEq vs vs'
  | .incdec o t, .incdec o' t' => o == o' && exprEq t t'
  | .decl m ns d ty vs, .decl m' ns' d' ty' vs' =>
    decide (m = m') && ns == ns' && d == d' && optTyEq ty ty' && exprsEq vs vs'
  | .interp es, .interp es' => exprsEq es es'
  | .lambda ps r b, .lambda ps' r' b' => paramsEq ps ps' && optTyEq r r' && exprEq b b'
  | .named n v, .named n' v' => n == n' && exprEq v v'
  | .unsupported a, .unsupported b => a == b
  | _, _ => false
def exprsEq : List Expr → List Expr → Bool
  | [], [] => true
  | e :: es, e' :: es' => exprEq e e' && exprsEq es es'
  | _, _ => false
def optExprEq : Option Expr → Option Expr → Bool
  | none, none => true
  | some e, some e' => exprEq e e'
  | _, _ => false
def casesEq : List (Pat × Option Expr × Expr) → List (Pat × Option Expr × Expr) → Bool
  | [], [] => true
  | (p, g, b) :: cs, (p', g', b') :: cs' =>
    patEq p p' && optExprEq g g' && exprEq b b' && casesEq cs cs'
  | _, _ => false
end

def fieldEq (a b : Field) : Bool :=
  a.name == b.name && optTyEq a.type b.type && a.mutable == b.mutable && optExprEq a.default b.default

/-! ## The renaming relation, decided -/

/-- `Ren.bound`. -/
def boundB : Ren → String → String → Bool
  | [], _, _ => false
  | (a, a') :: r, x, x' => (a == x && a' == x') || (a != x && a' != x' && boundB r x x')

/-- `Ren.free`. -/
def freeB (Γ : Ren) (x : String) : Bool := !(Γ.map (·.1)).contains x && !(Γ.map (·.2)).contains x

/-- `NameR`. -/
def nameB (Γ : Ren) (x x' : String) : Bool := boundB Γ x x' || (x == x' && freeB Γ x)

/-- A renamed binder's conditions: lowercase on both sides, never `_`, no package-level name. -/
def renamedB (pkg : List String) (x x' : String) : Bool :=
  x != "_" && x' != "_" && !capitalized x && !capitalized x' && !pkg.contains x && !pkg.contains x'

/-- `BinderR`. -/
def binderB (pkg : List String) (x x' : String) : Bool := x == x' || renamedB pkg x x'

/-- `NamesR`. -/
def namesB (pkg : List String) (ns ns' : List String) : Bool :=
  ns.length == ns'.length && (ns.zip ns').all fun p => binderB pkg p.1 p.2

/-- Two lists related element by element. -/
def fall2B (r : α → β → Bool) : List α → List β → Bool
  | [], [] => true
  | a :: as, b :: bs => r a b && fall2B r as bs
  | _, _ => false

mutual
/-- `PatR`: the slots, if the patterns are related. A bare name kept the same is a `maybe`
    slot when the name refers to the same thing on both sides, else a renamed one. -/
def patB (pkg : List String) (Γ : Ren) : Pat → Pat → Option (List Slot)
  | .wild, .wild => some []
  | .lit l, .lit l' => if litEq l l' then some [] else none
  | .unsupported k, .unsupported k' => if k == k' then some [] else none
  | .name x, .name x' =>
    if x == x' && nameB Γ x x then some [.maybe x]
    else if renamedB pkg x x' then some [.bind x x'] else none
  | .tuple ps, .tuple ps' => patsB pkg Γ ps ps'
  | .extract c ps, .extract c' ps' => if c == c' then patsB pkg Γ ps ps' else none
  | _, _ => none
def patsB (pkg : List String) (Γ : Ren) : List Pat → List Pat → Option (List Slot)
  | [], [] => some []
  | p :: ps, p' :: ps' =>
    match patB pkg Γ p p', patsB pkg Γ ps ps' with
    | some s, some ss => some (s ++ ss)
    | _, _ => none
  | _, _ => none
end

/-- The named-argument condition of `ArgsR`. -/
def namedB (m : Ren) (n n' : String) : Bool :=
  m.lookup n == some n' || (n == n' && !(m.map (·.1)).contains n && !(m.map (·.2)).contains n)

mutual
/-- `AlphaE`. -/
def exprB (pkg : List String) (pm : String → Ren) (Γ : Ren) : Expr → Expr → Bool
  | .lit l, .lit l' => litEq l l'
  | .name x, .name x' => nameB Γ x x'
  | .unsupported k, .unsupported k' => k == k'
  | .unary op e, .unary op' e' => op == op' && exprB pkg pm Γ e e'
  | .binary op l r, .binary op' l' r' => op == op' && exprB pkg pm Γ l l' && exprB pkg pm Γ r r'
  | .call c as, .call c' as' =>
    match c, c' with
    | .name f, .name f' =>
      if freeB Γ f then f == f' && argsB pkg pm (pm f) Γ as as'
      else nameB Γ f f' && argsB pkg pm [] Γ as as'
    | .name _, _ => false
    | _, _ => exprB pkg pm Γ c c' && argsB pkg pm [] Γ as as'
  | .select t f, .select t' f' => f == f' && exprB pkg pm Γ t t'
  | .paren e, .paren e' => exprB pkg pm Γ e e'
  | .tuple es, .tuple es' => listB pkg pm Γ es es'
  | .interp es, .interp es' => listB pkg pm Γ es es'
  | .ifExpr c a b, .ifExpr c' a' b' =>
    exprB pkg pm Γ c c' && exprB pkg pm Γ a a' && exprB pkg pm Γ b b'
  | .ifStmt c a b, .ifStmt c' a' b' =>
    exprB pkg pm Γ c c' && exprB pkg pm Γ a a' && optB pkg pm Γ b b'
  | .mtch s cs, .mtch s' cs' => exprB pkg pm Γ s s' && casesB pkg pm Γ cs cs'
  | .block ss, .block ss' => blockB pkg pm Γ ss ss'
  | .ret v, .ret v' => optB pkg pm Γ v v'
  | .assign op ts vs, .assign op' ts' vs' =>
    op == op' && listB pkg pm Γ ts ts' && listB pkg pm Γ vs vs'
  | .incdec op t, .incdec op' t' => op == op' && exprB pkg pm Γ t t'
  | .lambda ps r b, .lambda ps' r' b' =>
    optTyEq r r' && optTysEq (ps.map (·.type)) (ps'.map (·.type)) &&
      namesB pkg (ps.map (·.name)) (ps'.map (·.name)) &&
      exprB pkg pm (Γ.pushAll ((ps.map (·.name)).zip (ps'.map (·.name)))) b b'
  | _, _ => false
  termination_by structural e _ => e
/-- `AlphaL`. -/
def listB (pkg : List String) (pm : String → Ren) (Γ : Ren) : List Expr → List Expr → Bool
  | [], [] => true
  | e :: es, e' :: es' => exprB pkg pm Γ e e' && listB pkg pm Γ es es'
  | _, _ => false
  termination_by structural es _ => es
/-- `OptR`. -/
def optB (pkg : List String) (pm : String → Ren) (Γ : Ren) : Option Expr → Option Expr → Bool
  | none, none => true
  | some e, some e' => exprB pkg pm Γ e e'
  | _, _ => false
  termination_by structural o _ => o
/-- `ArgsR`. -/
def argsB (pkg : List String) (pm : String → Ren) (m Γ : Ren) : List Expr → List Expr → Bool
  | [], [] => true
  | .named n v :: as, .named n' v' :: as' =>
    namedB m n n' && exprB pkg pm Γ v v' && argsB pkg pm m Γ as as'
  | e :: as, e' :: as' => !isNamed e && exprB pkg pm Γ e e' && argsB pkg pm m Γ as as'
  | _, _ => false
  termination_by structural as _ => as
/-- `BlockR`. -/
def blockB (pkg : List String) (pm : String → Ren) (Γ : Ren) : List Expr → List Expr → Bool
  | [], [] => true
  | .decl m ns d ty vs :: rest, .decl m' ns' d' ty' vs' :: rest' =>
    decide (m = m') && d == d' && optTyEq ty ty' && namesB pkg ns ns' && listB pkg pm Γ vs vs' &&
      blockB pkg pm (Γ.pushAll (ns.zip ns')) rest rest'
  | e :: rest, e' :: rest' => exprB pkg pm Γ e e' && blockB pkg pm Γ rest rest'
  | _, _ => false
  termination_by structural ss _ => ss
/-- `CasesR`: each arm for every way its pattern's slots go. -/
def casesB (pkg : List String) (pm : String → Ren) (Γ : Ren) :
    List (Pat × Option Expr × Expr) → List (Pat × Option Expr × Expr) → Bool
  | [], [] => true
  | (p, g, b) :: cs, (p', g', b') :: cs' =>
    match patB pkg Γ p p' with
    | some ss =>
      (Slot.outs ss).all (fun Δ => optB pkg pm (Γ.pushAll Δ) g g' && exprB pkg pm (Γ.pushAll Δ) b b') &&
        casesB pkg pm Γ cs cs'
    | none => false
  | _, _ => false
  termination_by structural cs _ => cs
end

/-! ## Declarations and programs -/

/-- `FuncR`. -/
def funcB (pkg : List String) (pm : String → Ren) (f f' : Func) : Bool :=
  let ns := f.params.map (·.name)
  let ns' := f'.params.map (·.name)
  f.name == f'.name && optTysEq (f.params.map (·.type)) (f'.params.map (·.type)) &&
    namesB pkg ns ns' && decide ns.Nodup && decide ns'.Nodup && pm f.name == ns.zip ns' &&
    f.defaults.length == f'.defaults.length &&
    (List.range f.defaults.length).all (fun j =>
      optB pkg pm (Ren.pushAll [] ((ns.zip ns').take j)) (f.defaults.getD j none) (f'.defaults.getD j none)) &&
    optTyEq f.result f'.result && exprB pkg pm (Ren.pushAll [] (ns.zip ns')) f.body f'.body

/-- `FieldR`. -/
def fieldB (pkg : List String) (pm : String → Ren) (a b : Field) : Bool :=
  fieldEq a b && optB pkg pm [] a.default a.default

/-- `VarR`. -/
def varB (pkg : List String) (pm : String → Ren) (v w : Variant) : Bool :=
  v.name == w.name && v.isDefault == w.isDefault && fall2B (fieldB pkg pm) v.fields w.fields

/-- `DeclsR`, but for `noFunc` and `inPkg`, which `alphaPm` and `alphaPkg` meet by
    construction. -/
def declsB (pkg : List String) (pm : String → Ren) (ds ds' : Decls) : Bool :=
  fall2B (fun (a b : String × Func) => a.1 == b.1 && a.2.name == a.1 && funcB pkg pm a.2 b.2)
      ds.funcs ds'.funcs &&
    fall2B (fun (a b : String × List Field) => a.1 == b.1 && fall2B (fieldB pkg pm) a.2 b.2)
      ds.structs ds'.structs &&
    ds.shorthands == ds'.shorthands &&
    fall2B (fun (a b : String × List Variant) => a.1 == b.1 && fall2B (varB pkg pm) a.2 b.2)
      ds.sealeds ds'.sealeds &&
    ds.unsupported == ds'.unsupported

/-- `GlobalR`. -/
def globalB (pkg : List String) (pm : String → Ren) (g g' : GlobalDecl) : Bool :=
  g.mutable == g'.mutable && g.names == g'.names && g.destructure == g'.destructure &&
    optTyEq g.type g'.type && listB pkg pm [] g.values g'.values

end Check

open Check in
/-- The package-level names of a program: its functions, structs, sealed cases and
    package-level variables. -/
def alphaPkg (ds : Decls) (gs : List GlobalDecl) : List String :=
  ds.funcs.map (·.1) ++ ds.structs.map (·.1) ++ ds.sealeds.flatMap (fun s => s.2.map (·.name)) ++
    gs.flatMap (·.names)

/-- Each function's parameters on the one side paired with those on the other. -/
def alphaPm (ds ds' : Decls) (f : String) : Ren :=
  match ds.funcs.lookup f, ds'.funcs.lookup f with
  | some a, some b => (a.params.map (·.name)).zip (b.params.map (·.name))
  | _, _ => []

open Check in
/-- Whether `p'` is `p` with its binders renamed as T2 covers (`alphaCheck_sound`). -/
def alphaCheck (p p' : Program) : Bool :=
  let (ds, gs) := collect p
  let (ds', gs') := collect p'
  let pkg := alphaPkg ds gs
  let pm := alphaPm ds ds'
  declsB pkg pm ds ds' && fall2B (globalB pkg pm) gs gs'

end Gala.Spec
