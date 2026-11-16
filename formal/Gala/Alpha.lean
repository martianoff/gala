/-
  # T2, part a: renamings and the lookups that commute with them

  docs/FORMAL_SEMANTICS.MD, "M2": renaming bound variables apart does not change what
  galaspec says a program does. This file defines when one program is another with its
  binders renamed, and proves that the evaluator's name lookups do not tell them apart:
  `lookup_rel`, `resolve_rel`, `matchPat_rel`, `argSlots_rel` and `byNameOk_rel`. T2 itself,
  for expressions and for programs, is not proved yet.

  The renaming is a relation, not a function, so that shadowing is allowed: `Ren` holds the
  binders in scope with their names on both sides, and `NameR` says two occurrences refer to
  the same binder, or are the same free name (a package-level declaration or a builtin)
  bound on neither side, so that no binder captures a free name such as `Println`.

  Conditions on a renamed binder (`BinderR`, `PatR.bind`): it is lowercase on both sides,
  never `_`, and never a package-level name (functions, package-level variables, structs,
  sealed cases: the names in `pkg`). This is stricter than keeping the capitalization.
  `DeclsR` puts every function, struct and case name in `pkg`; that the package-level
  variables' are in it too is left to the relation on whole programs (T2, part c), since
  `Decls` does not list them. A function's parameters stay distinct, and a named argument
  follows its parameter's new name.

  The relation covers the shapes the parser produces: a declaration only as a statement of a
  block, and a named argument only as a call's argument. Binders inside a struct or case
  field's default are not renamed, and such a default may not pass a named argument whose
  name the callee's parameter renaming changes (`FieldR`): struct and sealed declarations are
  the same on both sides. A program of another shape is not related, so T2 says nothing about
  it.
-/
import Gala.Order

namespace Gala.Spec

/-! ## Renamings -/

/-- The binders in scope, newest first: each binder's name in the one program and in the
    other. -/
abbrev Ren := List (String × String)

/-- `x` and `x'` name the same binder of `Γ`: the newest binder named `x` on the left is
    the newest named `x'` on the right. -/
def Ren.bound : Ren → String → String → Prop
  | [], _, _ => False
  | (a, a') :: r, x, x' => (a = x ∧ a' = x') ∨ (a ≠ x ∧ a' ≠ x' ∧ Ren.bound r x x')

/-- `x` is bound by no binder of `Γ`, on either side. -/
def Ren.free (Γ : Ren) (x : String) : Prop := x ∉ Γ.map (·.1) ∧ x ∉ Γ.map (·.2)

/-- An occurrence of `x` and one of `x'` refer to the same thing: the same binder, or the
    same free name (a package-level declaration or a builtin), captured on neither side. -/
def NameR (Γ : Ren) (x x' : String) : Prop := Γ.bound x x' ∨ (x = x' ∧ Γ.free x)

/-- A binder named `x` in the one program and `x'` in the other. A renamed binder is
    lowercase (not capitalized) on both sides, never `_` and never a package-level name
    (`pkg`: functions, package-level variables, structs and sealed cases). This is
    stricter than keeping the capitalization. -/
def BinderR (pkg : List String) (x x' : String) : Prop :=
  x = x' ∨ (x ≠ "_" ∧ x' ≠ "_" ∧ capitalized x = false ∧ capitalized x' = false ∧
    x ∉ pkg ∧ x' ∉ pkg)

/-- Push a binder, as `bind` does: `_` binds nothing. -/
def Ren.push (Γ : Ren) (xx : String × String) : Ren := if xx.1 = "_" then Γ else xx :: Γ

/-- Push binders in order, the last newest, as `pushAll` and a function's frame do. -/
def Ren.pushAll (Γ : Ren) (xs : Ren) : Ren := xs.foldl Ren.push Γ

/-! ## Patterns

A bare pattern name is a binding, a case test or a stable identifier depending on the
subject's type and on what is in scope at run time (`classifyPatName`). A renamed one is
lowercase and no package-level name, so it is always a binding (`slot.bind`). One kept
the same may be either: it binds on both sides or on neither (`slot.maybe`). -/

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

mutual
/-- Related patterns, with their slots in order. -/
inductive PatR (pkg : List String) (Γ : Ren) : Pat → Pat → List Slot → Prop
  | wild : PatR pkg Γ .wild .wild []
  | lit (l : Lit) : PatR pkg Γ (.lit l) (.lit l) []
  | unsupported (k : String) : PatR pkg Γ (.unsupported k) (.unsupported k) []
  | bind {x x' : String} : x ≠ "_" → x' ≠ "_" → capitalized x = false →
      capitalized x' = false → x ∉ pkg → x' ∉ pkg → PatR pkg Γ (.name x) (.name x') [.bind x x']
  /-- A bare name kept the same. Its scope is related and `NameR Γ x x`, so `lookupVar`
      agrees on both sides, and so do the subject's cases and `capitalized`: both sides
      classify it the same way (`classify_same`), so it binds on both or on neither. That
      is why `CasesR` may require the arm to be related for each way, and `MatchR` ties the
      two sides to the same way. -/
  | same {x : String} : NameR Γ x x → PatR pkg Γ (.name x) (.name x) [.maybe x]
  | tuple {ps ps' : List Pat} {ss : List Slot} :
      PatsR pkg Γ ps ps' ss → PatR pkg Γ (.tuple ps) (.tuple ps') ss
  | extract (c : String) {ps ps' : List Pat} {ss : List Slot} :
      PatsR pkg Γ ps ps' ss → PatR pkg Γ (.extract c ps) (.extract c ps') ss
inductive PatsR (pkg : List String) (Γ : Ren) : List Pat → List Pat → List Slot → Prop
  | nil : PatsR pkg Γ [] [] []
  | cons {p p' : Pat} {ps ps' : List Pat} {s ss : List Slot} :
      PatR pkg Γ p p' s → PatsR pkg Γ ps ps' ss → PatsR pkg Γ (p :: ps) (p' :: ps') (s ++ ss)
end

/-! ## Expressions

`AlphaE pkg pm Γ e e'`: `e'` is `e` with its binders renamed, in scope `Γ`. `pm f` is the
renaming of top-level function `f`'s parameters, which a named argument `n = v` of a call
of `f` follows. A declaration is related only as a statement of a block, where the names
it binds are in scope for the rest of the block. -/

/-- Each name of a declaration or parameter list, renamed as a binder. -/
def NamesR (pkg : List String) (ns ns' : List String) : Prop :=
  ns.length = ns'.length ∧ ∀ p ∈ ns.zip ns', BinderR pkg p.1 p.2

mutual
inductive AlphaE (pkg : List String) (pm : String → Ren) : Ren → Expr → Expr → Prop
  | lit {Γ : Ren} (l : Lit) : AlphaE pkg pm Γ (.lit l) (.lit l)
  | name {Γ : Ren} {x x' : String} : NameR Γ x x' → AlphaE pkg pm Γ (.name x) (.name x')
  | unsupported {Γ : Ren} (k : String) : AlphaE pkg pm Γ (.unsupported k) (.unsupported k)
  | unary {Γ : Ren} (op : String) {e e' : Expr} :
      AlphaE pkg pm Γ e e' → AlphaE pkg pm Γ (.unary op e) (.unary op e')
  | binary {Γ : Ren} (op : String) {l l' r r' : Expr} : AlphaE pkg pm Γ l l' →
      AlphaE pkg pm Γ r r' → AlphaE pkg pm Γ (.binary op l r) (.binary op l' r')
  /-- A call of a free name `f` (a top-level function, say): named arguments follow `pm f`. -/
  | callFree {Γ : Ren} {f : String} {as as' : List Expr} : Γ.free f →
      ArgsR pkg pm (pm f) Γ as as' → AlphaE pkg pm Γ (.call (.name f) as) (.call (.name f) as')
  /-- Any other call: a named argument keeps its name. -/
  | call {Γ : Ren} {c c' : Expr} {as as' : List Expr} : (∀ f, c = .name f → ¬ Γ.free f) →
      AlphaE pkg pm Γ c c' → ArgsR pkg pm [] Γ as as' → AlphaE pkg pm Γ (.call c as) (.call c' as')
  | select {Γ : Ren} {t t' : Expr} (f : String) :
      AlphaE pkg pm Γ t t' → AlphaE pkg pm Γ (.select t f) (.select t' f)
  | paren {Γ : Ren} {e e' : Expr} : AlphaE pkg pm Γ e e' → AlphaE pkg pm Γ (.paren e) (.paren e')
  | tuple {Γ : Ren} {es es' : List Expr} :
      AlphaL pkg pm Γ es es' → AlphaE pkg pm Γ (.tuple es) (.tuple es')
  | interp {Γ : Ren} {es es' : List Expr} :
      AlphaL pkg pm Γ es es' → AlphaE pkg pm Γ (.interp es) (.interp es')
  | ifExpr {Γ : Ren} {c c' a a' b b' : Expr} : AlphaE pkg pm Γ c c' → AlphaE pkg pm Γ a a' →
      AlphaE pkg pm Γ b b' → AlphaE pkg pm Γ (.ifExpr c a b) (.ifExpr c' a' b')
  | ifStmt {Γ : Ren} {c c' a a' : Expr} {b b' : Option Expr} : AlphaE pkg pm Γ c c' →
      AlphaE pkg pm Γ a a' → OptR pkg pm Γ b b' → AlphaE pkg pm Γ (.ifStmt c a b) (.ifStmt c' a' b')
  | mtch {Γ : Ren} {s s' : Expr} {cs cs' : List (Pat × Option Expr × Expr)} :
      AlphaE pkg pm Γ s s' → CasesR pkg pm Γ cs cs' → AlphaE pkg pm Γ (.mtch s cs) (.mtch s' cs')
  | block {Γ : Ren} {ss ss' : List Expr} :
      BlockR pkg pm Γ ss ss' → AlphaE pkg pm Γ (.block ss) (.block ss')
  | ret {Γ : Ren} {v v' : Option Expr} : OptR pkg pm Γ v v' → AlphaE pkg pm Γ (.ret v) (.ret v')
  | assign {Γ : Ren} (op : String) {ts ts' vs vs' : List Expr} : AlphaL pkg pm Γ ts ts' →
      AlphaL pkg pm Γ vs vs' → AlphaE pkg pm Γ (.assign op ts vs) (.assign op ts' vs')
  | incdec {Γ : Ren} (op : String) {t t' : Expr} :
      AlphaE pkg pm Γ t t' → AlphaE pkg pm Γ (.incdec op t) (.incdec op t')
  /-- A lambda: its parameters are binders in scope in its body. -/
  | lambda {Γ : Ren} {ps ps' : List Param} (r : Option Ty) {b b' : Expr} :
      ps.map (·.type) = ps'.map (·.type) → NamesR pkg (ps.map (·.name)) (ps'.map (·.name)) →
      AlphaE pkg pm (Γ.pushAll ((ps.map (·.name)).zip (ps'.map (·.name)))) b b' →
      AlphaE pkg pm Γ (.lambda ps r b) (.lambda ps' r b')
inductive AlphaL (pkg : List String) (pm : String → Ren) : Ren → List Expr → List Expr → Prop
  | nil {Γ : Ren} : AlphaL pkg pm Γ [] []
  | cons {Γ : Ren} {e e' : Expr} {es es' : List Expr} :
      AlphaE pkg pm Γ e e' → AlphaL pkg pm Γ es es' → AlphaL pkg pm Γ (e :: es) (e' :: es')
inductive OptR (pkg : List String) (pm : String → Ren) : Ren → Option Expr → Option Expr → Prop
  | none {Γ : Ren} : OptR pkg pm Γ none none
  | some {Γ : Ren} {e e' : Expr} : AlphaE pkg pm Γ e e' → OptR pkg pm Γ (some e) (some e')
/-- A call's arguments: a named argument's name follows the callee's parameter renaming
    `m`, or stays as it is when `m` has none for it. -/
inductive ArgsR (pkg : List String) (pm : String → Ren) : Ren → Ren → List Expr → List Expr → Prop
  | nil {m Γ : Ren} : ArgsR pkg pm m Γ [] []
  | pos {m Γ : Ren} {e e' : Expr} {as as' : List Expr} : isNamed e = false →
      AlphaE pkg pm Γ e e' → ArgsR pkg pm m Γ as as' → ArgsR pkg pm m Γ (e :: as) (e' :: as')
  | named {m Γ : Ren} {n n' : String} {v v' : Expr} {as as' : List Expr} :
      (m.lookup n = some n' ∨ (n = n' ∧ n ∉ m.map (·.1) ∧ n ∉ m.map (·.2))) → AlphaE pkg pm Γ v v' →
      ArgsR pkg pm m Γ as as' → ArgsR pkg pm m Γ (.named n v :: as) (.named n' v' :: as')
/-- A block's statements: a declaration's names are in scope for the rest of the block. -/
inductive BlockR (pkg : List String) (pm : String → Ren) : Ren → List Expr → List Expr → Prop
  | nil {Γ : Ren} : BlockR pkg pm Γ [] []
  | decl {Γ : Ren} (m : Mut) {ns ns' : List String} (d : Bool) (ty : Option Ty)
      {vs vs' : List Expr} {rest rest' : List Expr} : NamesR pkg ns ns' →
      AlphaL pkg pm Γ vs vs' → BlockR pkg pm (Γ.pushAll (ns.zip ns')) rest rest' →
      BlockR pkg pm Γ (.decl m ns d ty vs :: rest) (.decl m ns' d ty vs' :: rest')
  | stmt {Γ : Ren} {e e' : Expr} {rest rest' : List Expr} :
      AlphaE pkg pm Γ e e' → BlockR pkg pm Γ rest rest' → BlockR pkg pm Γ (e :: rest) (e' :: rest')
/-- A `match`'s arms: the guard and body in scope of whatever the pattern binds. -/
inductive CasesR (pkg : List String) (pm : String → Ren) :
    Ren → List (Pat × Option Expr × Expr) → List (Pat × Option Expr × Expr) → Prop
  | nil {Γ : Ren} : CasesR pkg pm Γ [] []
  | cons {Γ : Ren} {p p' : Pat} {g g' : Option Expr} {b b' : Expr} {ss : List Slot}
      {rest rest' : List (Pat × Option Expr × Expr)} : PatR pkg Γ p p' ss →
      (∀ Δ ∈ Slot.outs ss, OptR pkg pm (Γ.pushAll Δ) g g') →
      (∀ Δ ∈ Slot.outs ss, AlphaE pkg pm (Γ.pushAll Δ) b b') →
      CasesR pkg pm Γ rest rest' →
      CasesR pkg pm Γ ((p, g, b) :: rest) ((p', g', b') :: rest')
end

/-! ## Functions and declarations -/

/-- A list of parameter or field names as a renaming, in order. -/
def pairsOf (ns ns' : List String) : Ren := ns.zip ns'

/-- A top-level function with its parameters renamed (distinct on each side, as Go needs),
    each default related in the scope of the parameters before it, and its body in the scope
    of all of them. -/
structure FuncR (pkg : List String) (pm : String → Ren) (f f' : Func) : Prop where
  name : f.name = f'.name
  types : f.params.map (·.type) = f'.params.map (·.type)
  names : NamesR pkg (f.params.map (·.name)) (f'.params.map (·.name))
  nodup : (f.params.map (·.name)).Nodup ∧ (f'.params.map (·.name)).Nodup
  map : pm f.name = pairsOf (f.params.map (·.name)) (f'.params.map (·.name))
  dlen : f.defaults.length = f'.defaults.length
  defaults : ∀ j, OptR pkg pm
    (Ren.pushAll [] ((pairsOf (f.params.map (·.name)) (f'.params.map (·.name))).take j))
    (f.defaults.getD j none) (f'.defaults.getD j none)
  result : f.result = f'.result
  body : AlphaE pkg pm (Ren.pushAll [] (pairsOf (f.params.map (·.name)) (f'.params.map (·.name))))
    f.body f'.body

/-- A struct or case field: the same on both sides, its default included. Binders inside a
    field's default are not renamed (a restriction of the relation; a function parameter's
    default is renamed, by `FuncR`). The default is evaluated in each program's own
    declarations, with no local in scope, so it must be related to itself there: equal text
    is not enough, since a named argument it passes whose name the callee's parameter
    renaming changes would name no parameter on the other side. -/
def FieldR (pkg : List String) (pm : String → Ren) (a b : Field) : Prop :=
  a = b ∧ OptR pkg pm [] a.default a.default

/-- Two lists related elementwise. -/
inductive Fall2 (R : α → β → Prop) : List α → List β → Prop
  | nil : Fall2 R [] []
  | cons {a b as bs} : R a b → Fall2 R as bs → Fall2 R (a :: as) (b :: bs)

/-- A sealed case related by the renaming: the same case (`VarR.eq`). -/
def VarR (pkg : List String) (pm : String → Ren) (v w : Variant) : Prop :=
  v.name = w.name ∧ v.isDefault = w.isDefault ∧ Fall2 (FieldR pkg pm) v.fields w.fields

/-- The declarations of two programs: the same, but for renamed binders inside function
    bodies and parameter defaults (structs and sealed types are the same, `DeclsR.structs_eq`,
    `DeclsR.sealeds_eq`). `pkg` holds every function, struct and sealed case
    name (`inPkg`); the names of package-level variables, which `Decls` does not carry, are
    required to be in it by the relation on whole programs (T2, part c). -/
structure DeclsR (pkg : List String) (pm : String → Ren) (ds ds' : Decls) : Prop where
  funcs : Fall2 (fun (a : String × Func) (b : String × Func) => a.1 = b.1 ∧ a.2.name = a.1 ∧
    FuncR pkg pm a.2 b.2) ds.funcs ds'.funcs
  noFunc : ∀ f, ds.funcs.lookup f = none → pm f = []
  structs : Fall2 (fun (a b : String × List Field) => a.1 = b.1 ∧ Fall2 (FieldR pkg pm) a.2 b.2)
    ds.structs ds'.structs
  shorthands : ds.shorthands = ds'.shorthands
  sealeds : Fall2 (fun (a b : String × List Variant) => a.1 = b.1 ∧ Fall2 (VarR pkg pm) a.2 b.2)
    ds.sealeds ds'.sealeds
  unsupported : ds.unsupported = ds'.unsupported
  inPkg : ∀ x, ((ds.funcs.lookup x).isSome ∨ (ds.structs.lookup x).isSome ∨
    (ds.variant? x).isSome) → x ∈ pkg

/-! ## Values and states -/

mutual
/-- Values related by the renaming: equal but for the binders inside closures and
    functions. -/
inductive ValR (pkg : List String) (pm : String → Ren) : Val → Val → Prop
  | int (n : Int) : ValR pkg pm (.int n) (.int n)
  | bool (b : Bool) : ValR pkg pm (.bool b) (.bool b)
  | str (s : String) : ValR pkg pm (.str s) (.str s)
  | unit : ValR pkg pm .unit .unit
  | ref (n : Nat) : ValR pkg pm (.ref n) (.ref n)
  | tuple {vs vs' : List Val} : ValsR pkg pm vs vs' → ValR pkg pm (.tuple vs) (.tuple vs')
  | struct_ (ty : String) {vs vs' : List Val} :
      ValsR pkg pm vs vs' → ValR pkg pm (.struct_ ty vs) (.struct_ ty vs')
  | sealed (ty c : String) {vs vs' : List Val} :
      ValsR pkg pm vs vs' → ValR pkg pm (.sealed ty c vs) (.sealed ty c vs')
  | fn {f f' : Func} : FuncR pkg pm f f' → ValR pkg pm (.fn f) (.fn f')
  /-- A closure: its captured scope related binder by binder, its parameters renamed, its
      body in the scope of both. -/
  | closure {ps ps' : List Param} {b b' : Expr} {env env' : List (String × Val)} :
      CapR pkg pm env env' → ps.map (·.type) = ps'.map (·.type) →
      NamesR pkg (ps.map (·.name)) (ps'.map (·.name)) →
      AlphaE pkg pm (Ren.pushAll ((env.map (·.1)).zip (env'.map (·.1)))
        (pairsOf (ps.map (·.name)) (ps'.map (·.name)))) b b' →
      ValR pkg pm (.closure ps b env) (.closure ps' b' env')
inductive ValsR (pkg : List String) (pm : String → Ren) : List Val → List Val → Prop
  | nil : ValsR pkg pm [] []
  | cons {v v' : Val} {vs vs' : List Val} :
      ValR pkg pm v v' → ValsR pkg pm vs vs' → ValsR pkg pm (v :: vs) (v' :: vs')
inductive CapR (pkg : List String) (pm : String → Ren) :
    List (String × Val) → List (String × Val) → Prop
  | nil : CapR pkg pm [] []
  | cons {k k' : String} {v v' : Val} {e e' : List (String × Val)} :
      ValR pkg pm v v' → CapR pkg pm e e' → CapR pkg pm ((k, v) :: e) ((k', v') :: e')
end

/-- Locals related along `Γ`: the same binders, newest first, with related values. -/
inductive EnvR (pkg : List String) (pm : String → Ren) : Ren → Env → Env → Prop
  | nil : EnvR pkg pm [] [] []
  | cons {Γ : Ren} {k k' : String} {b b' : Binding} {ρ ρ' : Env} :
      b.mutable = b'.mutable → ValR pkg pm b.val b'.val → EnvR pkg pm Γ ρ ρ' →
      EnvR pkg pm ((k, k') :: Γ) ((k, b) :: ρ) ((k', b') :: ρ')

/-- Package-level variables: the same names, related values. -/
def GlobR (pkg : List String) (pm : String → Ren) (g g' : Env) : Prop :=
  EnvR pkg pm (g.map fun kb => (kb.1, kb.1)) g g'

/-- The heaps: the same cells, related values. -/
def HeapR (pkg : List String) (pm : String → Ren) (h h' : Array Val) : Prop :=
  h.size = h'.size ∧ ∀ i (hi : i < h.size) (hi' : i < h'.size), ValR pkg pm h[i] h'[i]

/-- What a program sees, related along `Γ`: the locals, package-level variables, heap, and
    the same output. -/
def CoreR (pkg : List String) (pm : String → Ren) (Γ : Ren) (c c' : Core) : Prop :=
  EnvR pkg pm Γ c.locals c'.locals ∧ GlobR pkg pm c.globals c'.globals ∧
    HeapR pkg pm c.heap c'.heap ∧ c.out = c'.out

/-- Stops related by the renaming: a panic of the same class and message, out of fuel,
    `unsupported` of the same kind, `stuck`, or a `return` of related values. Only the text of
    `unsupported` and `stuck`, which may name a binder, may differ.

    Checked at every site: the panic messages are constants (`binop`'s division and shifts,
    `evalCases`'s "no match arm matched"); an `unsupported` kind is a constant, a type name
    (`type-…`, `print-type-…`; types are not renamed), a top-level declaration's kind or an
    exporter's kind, and a name only ever goes into the detail text. -/
inductive StopR (pkg : List String) (pm : String → Ren) : Stop → Stop → Prop
  | panic (c : PanicClass) (m : String) : StopR pkg pm (.panic c m) (.panic c m)
  | fuel : StopR pkg pm .fuel .fuel
  | unsupported (k : String) (m m' : String) : StopR pkg pm (.unsupported k m) (.unsupported k m')
  | stuck (m m' : String) : StopR pkg pm (.stuck m) (.stuck m')
  | ret {v v' : Val} : ValR pkg pm v v' → StopR pkg pm (.ret v) (.ret v')

/-- Results related by `R`, or `Stop`s related by the renaming. -/
def ResR (pkg : List String) (pm : String → Ren) (R : α → β → Prop) :
    Except Stop α → Except Stop β → Prop
  | .ok a, .ok b => R a b
  | .error e, .error e' => StopR pkg pm e e'
  | _, _ => False

/-! ## The commuting lemmas (T2, part a) -/

/-- Bindings related by the renaming. -/
def BindR (pkg : List String) (pm : String → Ren) (b b' : Binding) : Prop :=
  b.mutable = b'.mutable ∧ ValR pkg pm b.val b'.val

def OptBindR (pkg : List String) (pm : String → Ren) : Option Binding → Option Binding → Prop
  | none, none => True
  | some b, some b' => BindR pkg pm b b'
  | _, _ => False

/-- What a name resolves to, related by the renaming. -/
def RefR (pkg : List String) (pm : String → Ren) : Ref → Ref → Prop
  | .var b, .var b' => BindR pkg pm b b'
  | .func f, .func f' => FuncR pkg pm f f'
  | .struct_ n fs, .struct_ n' fs' => n = n' ∧ Fall2 (FieldR pkg pm) fs fs'
  | .variant ty v, .variant ty' v' => ty = ty' ∧ VarR pkg pm v v'
  | .print nl, .print nl' => nl = nl'
  | .unbound, .unbound => True
  | _, _ => False

/-- A pattern's results: no match on both sides, or bindings whose names are one of the
    ways the pattern's slots go and whose values are related. -/
def MatchR (pkg : List String) (pm : String → Ren) (ss : List Slot) :
    Option (List (String × Val)) → Option (List (String × Val)) → Prop
  | none, none => True
  | some bs, some bs' => bs.length = bs'.length ∧
      (bs.map (·.1)).zip (bs'.map (·.1)) ∈ Slot.outs ss ∧ ValsR pkg pm (bs.map (·.2)) (bs'.map (·.2))
  | _, _ => False

/-- Name lookup commutes with the renaming. -/
theorem lookup_rel {pkg : List String} {pm : String → Ren} {Γ : Ren} {ρ ρ' : Env} {x x' : String}
    (h : EnvR pkg pm Γ ρ ρ') (hx : NameR Γ x x') :
    OptBindR pkg pm (lookup ρ x) (lookup ρ' x') := by
  induction h with
  | nil =>
    rcases hx with hb | ⟨rfl, _⟩
    · exact absurd hb (by simp [Ren.bound])
    · trivial
  | @cons Γ k k' b b' ρ ρ' hm hv _ ih =>
    simp only [lookup]
    rcases hx with hb | ⟨rfl, hf⟩
    · simp only [Ren.bound] at hb
      rcases hb with ⟨rfl, rfl⟩ | ⟨h1, h2, hb⟩
      · simp only [ite_true]; exact ⟨hm, hv⟩
      · simp only [h1, h2, ite_false]; exact ih (Or.inl hb)
    · simp only [Ren.free, List.map_cons, List.mem_cons, not_or] at hf
      simp only [Ne.symm hf.1.1, Ne.symm hf.2.1, ite_false]
      exact ih (Or.inr ⟨rfl, hf.1.2, hf.2.2⟩)

theorem bound_lookup_isSome {pkg : List String} {pm : String → Ren} {Γ : Ren} {ρ ρ' : Env}
    {x x' : String} (h : EnvR pkg pm Γ ρ ρ') (hb : Γ.bound x x') : (lookup ρ x).isSome := by
  induction h with
  | nil => exact absurd hb (by simp [Ren.bound])
  | @cons Γ k k' b b' ρ ρ' _ _ _ ih =>
    simp only [Ren.bound] at hb
    simp only [lookup]
    rcases hb with ⟨rfl, rfl⟩ | ⟨h1, _, hb⟩
    · simp
    · simp only [h1, ite_false]; exact ih hb

/-- Along self-pairs every name refers to itself. -/
theorem nameR_self (g : Env) (x : String) : NameR (g.map fun kb => (kb.1, kb.1)) x x := by
  induction g with
  | nil => exact Or.inr ⟨rfl, by simp [Ren.free]⟩
  | cons kb g ih =>
    by_cases hk : kb.1 = x
    · exact Or.inl (Or.inl ⟨hk, hk⟩)
    · rcases ih with hb | ⟨_, hf⟩
      · exact Or.inl (Or.inr ⟨hk, hk, hb⟩)
      · refine Or.inr ⟨rfl, ?_, ?_⟩ <;> simp only [List.map_cons, List.mem_cons, not_or] <;>
          exact ⟨Ne.symm hk, by simpa [Ren.free] using hf.1⟩

theorem lookup_fall2 {S : String × α → String × β → Prop} {R : α → β → Prop}
    (hS : ∀ a b, S a b → a.1 = b.1 ∧ R a.2 b.2) :
    ∀ {l : List (String × α)} {l' : List (String × β)} (x : String), Fall2 S l l' →
      (l.lookup x = none ∧ l'.lookup x = none) ∨
        ∃ a b, l.lookup x = some a ∧ l'.lookup x = some b ∧ R a b
  | [], [], _, .nil => Or.inl ⟨rfl, rfl⟩
  | (k, a) :: l, (k', b) :: l', x, .cons hab ht => by
    obtain ⟨hk, hr⟩ := hS _ _ hab
    simp only at hk hr
    subst hk
    simp only [List.lookup_cons]
    by_cases hx : x = k
    · subst hx; exact Or.inr ⟨a, b, by simp, by simp, hr⟩
    · have : (x == k) = false := by simp [hx]
      rw [this]
      exact lookup_fall2 hS x ht

/-- Variants related as in `DeclsR`. -/
theorem find_name_fall2 {pkg : List String} {pm : String → Ren} (x : String) :
    ∀ {vs ws : List Variant}, Fall2 (VarR pkg pm) vs ws →
      (vs.find? (·.name == x) = none ∧ ws.find? (·.name == x) = none) ∨
        ∃ v w, vs.find? (·.name == x) = some v ∧ ws.find? (·.name == x) = some w ∧ VarR pkg pm v w
  | [], [], .nil => Or.inl ⟨rfl, rfl⟩
  | v :: vs, w :: ws, .cons hvw ht => by
    simp only [List.find?_cons]
    rw [← hvw.1]
    by_cases hx : (v.name == x) = true
    · simp only [hx]; exact Or.inr ⟨v, w, rfl, rfl, hvw⟩
    · simp only [Bool.not_eq_true] at hx
      simp only [hx]
      exact find_name_fall2 x ht

theorem variant_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls}
    (hD : DeclsR pkg pm ds ds') (x : String) :
    (ds.variant? x = none ∧ ds'.variant? x = none) ∨
      ∃ ty v w, ds.variant? x = some (ty, v) ∧ ds'.variant? x = some (ty, w) ∧ VarR pkg pm v w := by
  unfold Decls.variant?
  have := hD.sealeds
  generalize ds.sealeds = l at this ⊢
  generalize ds'.sealeds = l' at this ⊢
  induction this with
  | nil => exact Or.inl ⟨rfl, rfl⟩
  | @cons a b as bs hab _ ih =>
    obtain ⟨k, vs⟩ := a
    obtain ⟨k', ws⟩ := b
    obtain ⟨hk, hvw⟩ := hab
    simp only at hk hvw
    subst hk
    simp only [List.findSome?_cons]
    rcases find_name_fall2 x hvw with ⟨h1, h2⟩ | ⟨v, w, h1, h2, hr⟩
    · simp only [h1, h2, Option.map_none]; exact ih
    · simp only [h1, h2, Option.map_some]; exact Or.inr ⟨k, v, w, rfl, rfl, hr⟩

/-- Resolution of a name used as an expression commutes with the renaming. -/
theorem resolve_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {Γ : Ren}
    {l l' g g' : Env} {x x' : String} (hD : DeclsR pkg pm ds ds') (hl : EnvR pkg pm Γ l l')
    (hg : GlobR pkg pm g g') (hx : NameR Γ x x') :
    RefR pkg pm (resolve ds l g x) (resolve ds' l' g' x') := by
  have hloc := lookup_rel hl hx
  unfold resolve lookupVar
  cases h1 : lookup l x with
  | some b =>
    cases h1' : lookup l' x' with
    | none => rw [h1, h1'] at hloc; exact absurd hloc id
    | some b' => rw [h1, h1'] at hloc; exact hloc
  | none =>
    cases h1' : lookup l' x' with
    | some b' => rw [h1, h1'] at hloc; exact absurd hloc id
    | none =>
    -- Not a local on either side, so not bound by `Γ`: the same free name on both sides.
    obtain rfl : x = x' := by
      rcases hx with hb | ⟨h, _⟩
      · have := bound_lookup_isSome hl hb; rw [h1] at this; exact absurd this (by simp)
      · exact h
    have hglob := lookup_rel hg (nameR_self g x)
    cases h2 : lookup g x with
    | some b =>
      cases h2' : lookup g' x with
      | none => rw [h2, h2'] at hglob; exact absurd hglob id
      | some b' => rw [h2, h2'] at hglob; exact hglob
    | none =>
      cases h2' : lookup g' x with
      | some b' => rw [h2, h2'] at hglob; exact absurd hglob id
      | none =>
      rw [show ((none : Option Binding) <|> none) = none from rfl]
      rcases lookup_fall2 (R := FuncR pkg pm) (fun a b h => ⟨h.1, h.2.2⟩) x hD.funcs with
        ⟨hf, hf'⟩ | ⟨f, f', hf, hf', hr⟩
      · rw [hf, hf']
        rcases lookup_fall2 (R := Fall2 (FieldR pkg pm)) (fun a b h => h) x hD.structs with
          ⟨hs, hs'⟩ | ⟨fs, fs', hs, hs', hr⟩
        · rw [hs, hs']
          rcases variant_rel hD x with ⟨hv, hv'⟩ | ⟨ty, v, w, hv, hv', hr⟩
          · rw [hv, hv']
            simp only
            repeat (first | split | simp [RefR])
          · rw [hv, hv']; exact ⟨rfl, hr⟩
        · rw [hs, hs']; exact ⟨rfl, hr⟩
      · rw [hf, hf']; exact hr

/-! ### Helpers for `matchPat_rel` -/

theorem outs_append : ∀ (s ss : List Slot) (Δ₁ Δ₂ : Ren), Δ₁ ∈ Slot.outs s →
    Δ₂ ∈ Slot.outs ss → Δ₁ ++ Δ₂ ∈ Slot.outs (s ++ ss)
  | [], ss, Δ₁, Δ₂, h₁, h₂ => by simp [Slot.outs] at h₁; subst h₁; simpa using h₂
  | .bind x x' :: s, ss, Δ₁, Δ₂, h₁, h₂ => by
    simp only [Slot.outs, List.mem_map] at h₁
    obtain ⟨Δ, hΔ, rfl⟩ := h₁
    simp only [List.cons_append, Slot.outs, List.mem_map]
    exact ⟨Δ ++ Δ₂, outs_append s ss Δ Δ₂ hΔ h₂, rfl⟩
  | .maybe x :: s, ss, Δ₁, Δ₂, h₁, h₂ => by
    simp only [Slot.outs, List.mem_append, List.mem_map] at h₁
    simp only [List.cons_append, Slot.outs, List.mem_append, List.mem_map]
    rcases h₁ with ⟨Δ, hΔ, rfl⟩ | hΔ
    · exact Or.inl ⟨Δ ++ Δ₂, outs_append s ss Δ Δ₂ hΔ h₂, rfl⟩
    · exact Or.inr (outs_append s ss Δ₁ Δ₂ hΔ h₂)

theorem valsR_length {pkg : List String} {pm : String → Ren} :
    ∀ {vs vs' : List Val}, ValsR pkg pm vs vs' → vs.length = vs'.length
  | _, _, .nil => rfl
  | _, _, .cons _ h => by simp [valsR_length h]

mutual
theorem valEq_rel {pkg : List String} {pm : String → Ren} :
    ∀ {a a' b b' : Val}, ValR pkg pm a a' → ValR pkg pm b b' → valEq a b = valEq a' b'
  | _, _, _, _, ha, hb => by
    cases ha <;> cases hb
    case tuple.tuple _ _ h1 _ _ h2 => simp only [valEq]; exact valEqs_rel h1 h2
    case struct_.struct_ _ _ _ h1 _ _ _ h2 => simp only [valEq]; rw [valEqs_rel h1 h2]
    case sealed.sealed _ _ _ _ h1 _ _ _ _ h2 => simp only [valEq]; rw [valEqs_rel h1 h2]
    all_goals rfl
theorem valEqs_rel {pkg : List String} {pm : String → Ren} :
    ∀ {as as' bs bs' : List Val}, ValsR pkg pm as as' → ValsR pkg pm bs bs' →
      valEq.valEqs as bs = valEq.valEqs as' bs'
  | _, _, _, _, .cons ha has, .cons hb hbs => by
    simp only [valEq.valEqs]; rw [valEq_rel ha hb, valEqs_rel has hbs]
  | _, _, _, _, .nil, .nil => rfl
  | _, _, _, _, .nil, .cons _ _ => rfl
  | _, _, _, _, .cons _ _, .nil => rfl
end

theorem deref_rel {pkg : List String} {pm : String → Ren} {c c' : Core} {v v' : Val}
    (hh : HeapR pkg pm c.heap c'.heap) (hv : ValR pkg pm v v') :
    ResR pkg pm (ValR pkg pm) ((deref v).run.run c).1 ((deref v').run.run c').1 := by
  cases hv with
  | ref n =>
    unfold deref
    rw [run_get_bind, run_get_bind]
    by_cases hn : n < c.heap.size
    · have hn' : n < c'.heap.size := hh.1 ▸ hn
      simp only [Array.getElem?_eq_getElem hn, Array.getElem?_eq_getElem hn']
      exact hh.2 n hn hn'
    · have hn' : ¬ n < c'.heap.size := hh.1 ▸ hn
      simp only [Array.getElem?_eq_none (Nat.le_of_not_lt hn),
        Array.getElem?_eq_none (Nat.le_of_not_lt hn')]
      exact .stuck _ _
  | _ => first | exact .int _ | exact .bool _ | exact .str _ | exact .unit | (constructor <;> assumption)

theorem litMatches_rel {pkg : List String} {pm : String → Ren} {c c' : Core} (l : Lit) {v v' : Val}
    (hv : ValR pkg pm v v') :
    ResR pkg pm Eq ((litMatches l v).run.run c).1 ((litMatches l v').run.run c').1 := by
  cases l <;> cases hv <;> first | rfl | exact .stuck _ _

theorem outsideS1_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {c c' : Core}
    {R : α → β → Prop} (hu : ds.unsupported = ds'.unsupported) (m m' : String) :
    ResR pkg pm R ((outsideS1 ds m : H α).run.run c).1 ((outsideS1 ds' m' : H β).run.run c').1 := by
  unfold outsideS1
  rw [← hu]
  split
  · exact .unsupported _ _ _
  · exact .stuck _ _

/-- The cases a bare pattern name is checked against: the subject's sealed type's. -/
def subjCases (ds : Decls) : Val → List Variant
  | .sealed ty _ _ => (ds.sealeds.lookup ty).getD []
  | _ => []

theorem mem_of_lookup {β : Type} : ∀ {l : List (String × β)} {k : String} {b : β},
    l.lookup k = some b → (k, b) ∈ l
  | (k', b') :: l, k, b, h => by
    simp only [List.lookup_cons] at h
    by_cases hk : k = k'
    · subst hk; simp at h; subst h; simp
    · have : (k == k') = false := by simp [hk]
      rw [this] at h
      exact List.mem_cons_of_mem _ (mem_of_lookup h)

theorem variant_isSome_of_mem {ds : Decls} {ty : String} {v : Variant}
    (h : v ∈ (ds.sealeds.lookup ty).getD []) : (ds.variant? v.name).isSome := by
  unfold Decls.variant?
  cases hl : ds.sealeds.lookup ty with
  | none => simp [hl] at h
  | some vs =>
    simp only [hl, Option.getD_some] at h
    have hm : (ty, vs) ∈ ds.sealeds := mem_of_lookup hl
    rw [Option.isSome_iff_ne_none]
    intro hn
    rw [List.findSome?_eq_none_iff] at hn
    have := hn _ hm
    simp only [Option.map_eq_none_iff, List.find?_eq_none] at this
    exact this v h (by simp)

/-- A renamed pattern binder is always a binder: it is no case name and not capitalized. -/
theorem classify_binder {ds : Decls} {pkg : List String} {x : String} (v : Val) (l g : Env)
    (hpkg : ∀ y, (ds.variant? y).isSome → y ∈ pkg) (hx : x ∉ pkg) (hc : capitalized x = false) :
    classifyPatName (subjCases ds v) l g x = .binder := by
  unfold classifyPatName
  have hf : (subjCases ds v).find? (·.name == x) = none := by
    rw [List.find?_eq_none]
    intro w hw heq
    simp only [beq_iff_eq] at heq
    cases v <;> simp only [subjCases, List.not_mem_nil] at hw
    exact hx (heq ▸ hpkg _ (variant_isSome_of_mem hw))
  rw [hf, hc]

/-- What a bare pattern name kept the same means, on both sides. -/
def PatNameR (pkg : List String) (pm : String → Ren) : PatName → PatName → Prop
  | .variant v, .variant v' => VarR pkg pm v v'
  | .stable b, .stable b' => BindR pkg pm b b'
  | .binder, .binder => True
  | _, _ => False

theorem lookupVar_rel {pkg : List String} {pm : String → Ren} {Γ : Ren} {l l' g g' : Env}
    {x x' : String} (hl : EnvR pkg pm Γ l l') (hg : GlobR pkg pm g g') (hx : NameR Γ x x') :
    OptBindR pkg pm (lookupVar l g x) (lookupVar l' g' x') := by
  have hloc := lookup_rel hl hx
  unfold lookupVar
  cases h1 : lookup l x <;> cases h1' : lookup l' x' <;> rw [h1, h1'] at hloc
  · obtain rfl : x = x' := by
      rcases hx with hb | ⟨h, _⟩
      · have := bound_lookup_isSome hl hb; rw [h1] at this; exact absurd this (by simp)
      · exact h
    exact lookup_rel hg (nameR_self g x)
  · exact absurd hloc id
  · exact absurd hloc id
  · exact hloc

theorem subjCases_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {v v' : Val}
    (hD : DeclsR pkg pm ds ds') (hv : ValR pkg pm v v') :
    Fall2 (VarR pkg pm) (subjCases ds v) (subjCases ds' v') := by
  cases hv with
  | sealed ty c _ =>
    simp only [subjCases]
    rcases lookup_fall2 (R := Fall2 (VarR pkg pm)) (fun a b h => h) ty hD.sealeds with
      ⟨h1, h2⟩ | ⟨vs, ws, h1, h2, hr⟩
    · rw [h1, h2]; exact .nil
    · rw [h1, h2]; exact hr
  | _ => exact .nil

/-- A bare pattern name kept the same is classified the same way on both sides: the
    environments are related and `NameR Γ x x`, so `lookupVar` agrees, and so do the
    subject's cases and `capitalized`. -/
theorem classify_same {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {Γ : Ren}
    {l l' g g' : Env} {x : String} {v v' : Val} (hD : DeclsR pkg pm ds ds')
    (hl : EnvR pkg pm Γ l l') (hg : GlobR pkg pm g g') (hx : NameR Γ x x) (hv : ValR pkg pm v v') :
    PatNameR pkg pm (classifyPatName (subjCases ds v) l g x)
      (classifyPatName (subjCases ds' v') l' g' x) := by
  unfold classifyPatName
  rcases find_name_fall2 x (subjCases_rel hD hv) with ⟨h1, h2⟩ | ⟨w, w', h1, h2, hr⟩
  · rw [h1, h2]
    have hb := lookupVar_rel hl hg hx
    simp only
    cases capitalized x
    · trivial
    · cases h3 : lookupVar l g x <;> cases h3' : lookupVar l' g' x <;> rw [h3, h3'] at hb
      · trivial
      · exact absurd hb id
      · exact absurd hb id
      · exact hb
  · rw [h1, h2]; exact hr

theorem hpkg_left {pkg : List String} {pm : String → Ren} {ds ds' : Decls}
    (hD : DeclsR pkg pm ds ds') : ∀ y, (ds.variant? y).isSome → y ∈ pkg :=
  fun y h => hD.inPkg y (Or.inr (Or.inr h))

theorem hpkg_right {pkg : List String} {pm : String → Ren} {ds ds' : Decls}
    (hD : DeclsR pkg pm ds ds') : ∀ y, (ds'.variant? y).isSome → y ∈ pkg := fun y h => by
  rcases variant_rel hD y with ⟨h1, h2⟩ | ⟨ty, v, w, h1, h2, _⟩
  · rw [h2] at h; exact absurd h (by simp)
  · exact hpkg_left hD y (by rw [h1]; rfl)

/-- Bind an `H` step whose results are related to a continuation. -/
theorem resR_bind {pkg : List String} {pm : String → Ren} {R : α → α' → Prop}
    {S : β → β' → Prop} {x : H α} {x' : H α'} {f : α → H β} {f' : α' → H β'} {c c' : Core}
    (hx : ResR pkg pm R (x.run.run c).1 (x'.run.run c').1) (hro : ReadOnly x) (hro' : ReadOnly x')
    (hf : ∀ a a', R a a' → ResR pkg pm S ((f a).run.run c).1 ((f' a').run.run c').1) :
    ResR pkg pm S ((x >>= f).run.run c).1 ((x' >>= f').run.run c').1 := by
  rw [run_bind_eq, run_bind_eq]
  rcases h : x.run.run c with ⟨r, t⟩
  rcases h' : x'.run.run c' with ⟨r', t'⟩
  have ht : t = c := by have := hro c; rw [h] at this; exact this
  have ht' : t' = c' := by have := hro' c'; rw [h'] at this; exact this
  subst ht ht'
  rw [h, h'] at hx
  cases r <;> cases r' <;> simp only at hx ⊢
  · exact hx
  · exact absurd hx id
  · exact absurd hx id
  · exact hf _ _ hx

theorem patsR_length {pkg : List String} {Γ : Ren} :
    ∀ {ps ps' : List Pat} {ss : List Slot}, PatsR pkg Γ ps ps' ss → ps.length = ps'.length
  | _, _, _, .nil => rfl
  | _, _, _, .cons _ h => by simp [patsR_length h]

theorem valsR_append {pkg : List String} {pm : String → Ren} :
    ∀ {a a' b b' : List Val}, ValsR pkg pm a a' → ValsR pkg pm b b' →
      ValsR pkg pm (a ++ b) (a' ++ b')
  | _, _, _, _, .nil, h => h
  | _, _, _, _, .cons h1 h2, h => .cons h1 (valsR_append h2 h)

/-- `matchPat` on a bare name, given how the name is classified. -/
def nameMatch (x : String) (v : Val) : PatName → H (Option (List (String × Val)))
  | .variant c =>
    if !c.fields.isEmpty then throw (.stuck s!"bare pattern {x} for a case with fields")
    else match v with
      | .sealed _ c' _ => pure (if c' == c.name then some [] else none)
      | _ => pure none
  | .stable b => do pure (if valEq (← deref b.val) v then some [] else none)
  | .binder => pure (some [(x, v)])

theorem matchPat_name (ds : Decls) (l g : Env) (x : String) (v : Val) :
    matchPat ds l g (.name x) v = nameMatch x v (classifyPatName (subjCases ds v) l g x) := by
  rw [matchPat.eq_def]
  cases v <;> rfl

theorem fall2_length {R : α → β → Prop} : ∀ {l : List α} {l' : List β}, Fall2 R l l' →
    l.length = l'.length
  | _, _, .nil => rfl
  | _, _, .cons _ h => by simp [fall2_length h]

theorem nameMatch_rel {pkg : List String} {pm : String → Ren} {c c' : Core} {x : String}
    {v v' : Val} {k k' : PatName} (hh : HeapR pkg pm c.heap c'.heap) (hv : ValR pkg pm v v')
    (hk : PatNameR pkg pm k k') :
    ResR pkg pm (MatchR pkg pm [.maybe x]) ((nameMatch x v k).run.run c).1
      ((nameMatch x v' k').run.run c').1 := by
  cases k <;> cases k' <;> simp only [PatNameR] at hk
  · -- a case test: the same case on both sides
    rename_i w w'
    obtain ⟨hn, _, hf⟩ := hk
    have he : w.fields.isEmpty = w'.fields.isEmpty := by
      have hl := fall2_length hf; revert hl; cases w.fields <;> cases w'.fields <;> simp
    simp only [nameMatch, he]
    split
    · exact .stuck _ _
    · cases hv <;> simp only [hn] <;> (try split) <;>
        first | trivial | exact ⟨rfl, by simp [Slot.outs], .nil⟩
  · exact resR_bind (deref_rel hh hk.2) (readOnly_deref _) (readOnly_deref _) fun a a' ha => by
      simp only [valEq_rel ha hv]
      split
      · exact ⟨rfl, by simp [Slot.outs], .nil⟩
      · trivial
  · exact ⟨rfl, by simp [Slot.outs], .cons hv .nil⟩

mutual
/-- Matching a pattern commutes with the renaming: related patterns on related values, in
    related states, give the same verdict and related bindings, one of the ways the slots
    go (a renamed binder always binds, one kept the same binds on both sides or neither). -/
theorem matchPat_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {Γ : Ren}
    {c c' : Core} {p p' : Pat} {ss : List Slot} {v v' : Val}
    (hD : DeclsR pkg pm ds ds') (hc : CoreR pkg pm Γ c c') (hp : PatR pkg Γ p p' ss)
    (hv : ValR pkg pm v v') :
    ResR pkg pm (MatchR pkg pm ss) ((matchPat ds c.locals c.globals p v).run.run c).1
      ((matchPat ds' c'.locals c'.globals p' v').run.run c').1 := by
  cases hp with
  | wild => exact ⟨rfl, by simp [Slot.outs], .nil⟩
  | unsupported k => exact .unsupported _ _ _
  | lit l =>
    rw [matchPat.eq_def, matchPat.eq_def]
    dsimp only
    refine resR_bind (litMatches_rel l hv) (readOnly_litMatches _ _) (readOnly_litMatches _ _) ?_
    intro a a' h
    subst h
    cases a
    · trivial
    · exact ⟨rfl, by simp [Slot.outs], .nil⟩
  | bind h1 h2 h3 h4 h5 h6 =>
    rw [matchPat.eq_def, matchPat.eq_def]
    dsimp only
    have hb := classify_binder v c.locals c.globals (hpkg_left hD) h5 h3
    have hb' := classify_binder v' c'.locals c'.globals (hpkg_right hD) h6 h4
    have hv0 := hv
    cases hv <;> simp only [subjCases] at hb hb' <;> dsimp only <;> rw [hb, hb'] <;>
      exact ⟨rfl, by simp [Slot.outs], .cons hv0 .nil⟩
  | same hx =>
    rw [matchPat_name, matchPat_name]
    exact nameMatch_rel hc.2.2.1 hv (classify_same hD hc.1 hc.2.1 hx hv)
  | tuple hps =>
    have hpl := patsR_length hps
    rw [matchPat.eq_def, matchPat.eq_def]
    dsimp only
    cases hv with
    | tuple hvs =>
      dsimp only
      have hvl := valsR_length hvs
      simp only [hpl, hvl]
      split
      · rename_i hl
        exact matchPats_rel hD hc hps hvs (by simpa [hpl, hvl] using hl)
      · exact .stuck _ _
    | _ => exact .stuck _ _
  | extract cn hps =>
    have hpl := patsR_length hps
    rw [matchPat.eq_def, matchPat.eq_def]
    dsimp only
    cases hv with
    | sealed ty c0 hvs =>
      dsimp only
      have hvl := valsR_length hvs
      rcases find_name_fall2 cn (subjCases_rel hD (ValR.sealed ty c0 hvs)) with
        ⟨h1, h2⟩ | ⟨w, w', h1, h2, hw⟩
      · simp only [subjCases] at h1 h2
        rw [h1, h2]
        exact outsideS1_rel hD.unsupported _ _
      · simp only [subjCases] at h1 h2
        rw [h1, h2]
        simp only [hpl, fall2_length hw.2.2, hvl]
        split
        · trivial
        · split
          · exact .stuck _ _
          · rename_i _ hl
            exact matchPats_rel hD hc hps hvs (by
              simp only [Bool.or_eq_true, bne_iff_ne, ne_eq, not_or, Decidable.not_not] at hl
              omega)
    | struct_ ty hvs =>
      dsimp only
      have hvl := valsR_length hvs
      simp only [hpl, hvl]
      split
      · exact outsideS1_rel hD.unsupported _ _
      · split
        · exact .stuck _ _
        · rename_i _ hl
          exact matchPats_rel hD hc hps hvs (by
            simp only [bne_iff_ne, ne_eq, Decidable.not_not] at hl; omega)
    | _ => exact outsideS1_rel hD.unsupported _ _
theorem matchPats_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {Γ : Ren}
    {c c' : Core} {ps ps' : List Pat} {ss : List Slot} {vs vs' : List Val}
    (hD : DeclsR pkg pm ds ds') (hc : CoreR pkg pm Γ c c') (hp : PatsR pkg Γ ps ps' ss)
    (hv : ValsR pkg pm vs vs') (hl : ps.length = vs.length) :
    ResR pkg pm (MatchR pkg pm ss) ((matchPats ds c.locals c.globals ps vs).run.run c).1
      ((matchPats ds' c'.locals c'.globals ps' vs').run.run c').1 := by
  cases hp with
  | nil =>
    cases hv with
    | nil => exact ⟨rfl, by simp [Slot.outs], .nil⟩
    | cons _ _ => simp at hl
  | cons hp1 hps =>
    cases hv with
    | nil => simp at hl
    | cons hv1 hvs =>
      rw [matchPats.eq_def, matchPats.eq_def]
      dsimp only
      refine resR_bind (matchPat_rel hD hc hp1 hv1) (matchPat_readOnly _ _ _ _ _)
        (matchPat_readOnly _ _ _ _ _) fun a a' ha => ?_
      cases a <;> cases a' <;> simp only [MatchR] at ha
      · trivial
      · rename_i b b'
        obtain ⟨hbl, hbn, hbv⟩ := ha
        refine resR_bind (matchPats_rel hD hc hps hvs (by simpa using hl))
          (matchPats_readOnly _ _ _ _ _) (matchPats_readOnly _ _ _ _ _) fun r r' hr => ?_
        cases r <;> cases r' <;> simp only [MatchR] at hr
        · trivial
        · rename_i bs bs'
          obtain ⟨hl2, hn2, hv2⟩ := hr
          refine ⟨by simp [hbl, hl2], ?_, by rw [List.map_append, List.map_append]; exact valsR_append hbv hv2⟩
          simp only [List.map_append]
          rw [List.zip_append (by simpa using hbl)]
          exact outs_append _ _ _ _ hbn hn2
end

theorem alphaE_not_named {pkg : List String} {pm : String → Ren} {Γ : Ren} {e e' : Expr}
    (h : AlphaE pkg pm Γ e e') : isNamed e' = false := by
  cases h <;> rfl

/-- A parameter's position is the same as its renamed one's. -/
theorem idxOf_zip : ∀ {names names' : List String} {n n' : String},
    names.length = names'.length → names.Nodup → names'.Nodup →
    (pairsOf names names').lookup n = some n' → names.idxOf? n = names'.idxOf? n'
  | a :: as, a' :: as', n, n', hl, hnd, hnd', h => by
    simp only [pairsOf, List.zip_cons_cons, List.lookup_cons] at h
    simp only [List.nodup_cons] at hnd hnd'
    by_cases hn : n = a
    · subst hn
      simp only [beq_self_eq_true, Option.some.injEq] at h
      subst h
      simp [List.idxOf?_cons]
    · have : (n == a) = false := by simp [hn]
      rw [this] at h
      have hn' : n' ≠ a' := by
        intro he; subst he
        exact hnd'.1 (List.of_mem_zip (mem_of_lookup h)).2
      have ih := idxOf_zip (by simpa using hl) hnd.2 hnd'.2 h
      simp only [List.idxOf?_cons, beq_iff_eq, Ne.symm hn, Ne.symm hn', ite_false, ih]
  | [], [], _, _, _, _, _, h => by simp [pairsOf] at h
  | [], _ :: _, _, _, hl, _, _, _ | _ :: _, [], _, _, hl, _, _, _ => by simp at hl

theorem idxOf_none {names : List String} {n : String} (h : n ∉ names) : names.idxOf? n = none := by
  simpa [List.idxOf?_eq_none_iff] using h

theorem argSlots_go_rel {pkg : List String} {pm : String → Ren} {Γ : Ren}
    {names names' : List String} (hn : names.length = names'.length)
    (hnd : names.Nodup ∧ names'.Nodup) :
    ∀ {args args' : List Expr} (nm : Bool) (acc : List Nat),
      ArgsR pkg pm (pairsOf names names') Γ args args' →
      ResR pkg pm Eq (argSlots.go names args nm acc) (argSlots.go names' args' nm acc)
  | _, _, nm, acc, .nil => by
    simp only [argSlots.go, hn]
    split
    · exact .stuck _ _
    · rfl
  | _, _, nm, acc, .pos (e := e) (e' := e') hne he ha => by
    have hne' := alphaE_not_named he
    cases e <;> simp [isNamed] at hne <;> cases e' <;> simp [isNamed] at hne' <;>
      simp only [argSlots.go] <;>
      (split
       · exact StopR.unsupported _ _ _
       · exact argSlots_go_rel hn hnd nm _ ha)
  | _, _, nm, acc, .named (n := n) (n' := n') hm _ ha => by
    simp only [argSlots.go]
    have hidx : names.idxOf? n = names'.idxOf? n' := by
      rcases hm with h | ⟨rfl, h1, h2⟩
      · exact idxOf_zip hn hnd.1 hnd.2 h
      · have hf : (pairsOf names names').map (·.1) = names := List.map_fst_zip (Nat.le_of_eq hn)
        have hs : (pairsOf names names').map (·.2) = names' :=
          List.map_snd_zip (Nat.le_of_eq hn.symm)
        have h1' : n ∉ names := hf ▸ h1
        have h2' : n ∉ names' := hs ▸ h2
        rw [idxOf_none h1', idxOf_none h2']
    rw [hidx]
    split
    · exact argSlots_go_rel hn hnd true _ ha
    · exact .stuck _ _

theorem argSlots_same_go_rel {pkg : List String} {pm : String → Ren} {Γ : Ren}
    (names : List String) :
    ∀ {args args' : List Expr} (nm : Bool) (acc : List Nat), ArgsR pkg pm [] Γ args args' →
      ResR pkg pm Eq (argSlots.go names args nm acc) (argSlots.go names args' nm acc)
  | _, _, nm, acc, .nil => by
    simp only [argSlots.go]
    split
    · exact .stuck _ _
    · rfl
  | _, _, nm, acc, .pos (e := e) (e' := e') hne he ha => by
    have hne' := alphaE_not_named he
    cases e <;> simp [isNamed] at hne <;> cases e' <;> simp [isNamed] at hne' <;>
      simp only [argSlots.go] <;>
      (split
       · exact StopR.unsupported _ _ _
       · exact argSlots_same_go_rel names nm _ ha)
  | _, _, nm, acc, .named (n := n) (n' := n') hm _ ha => by
    obtain rfl : n = n' := by
      rcases hm with h | ⟨h, _⟩
      · simp at h
      · exact h
    simp only [argSlots.go]
    split
    · exact argSlots_same_go_rel names true _ ha
    · exact .stuck _ _

/-- A constructor's arguments, whose field names are not renamed (`AlphaE.callFree` with
    `pm` giving no renaming), get the same slots on both sides. -/
theorem argSlots_same_rel {pkg : List String} {pm : String → Ren} {Γ : Ren}
    (names : List String) {args args' : List Expr} (ha : ArgsR pkg pm [] Γ args args') :
    ResR pkg pm Eq (argSlots names args) (argSlots names args') :=
  argSlots_same_go_rel names false [] ha

/-- Binding arguments to parameters commutes with renaming the parameters: a named
    argument follows its parameter's new name, so each argument gets the same slot. -/
theorem argSlots_rel {pkg : List String} {pm : String → Ren} {Γ : Ren} {names names' : List String}
    {args args' : List Expr} (hn : names.length = names'.length)
    (hnd : names.Nodup ∧ names'.Nodup) (ha : ArgsR pkg pm (pairsOf names names') Γ args args') :
    ResR pkg pm Eq (argSlots names args) (argSlots names' args') :=
  argSlots_go_rel hn hnd false [] ha

/-- The by-name check commutes with the renaming. -/
theorem byNameOk_rel {pkg : List String} {pm : String → Ren} {ds ds' : Decls} {Γ : Ren}
    {l l' : Env} {c c' : Core} {ty : Option Ty} {a a' : Expr}
    (hD : DeclsR pkg pm ds ds') (hl : EnvR pkg pm Γ l l') (hg : GlobR pkg pm c.globals c'.globals)
    (hh : HeapR pkg pm c.heap c'.heap) (ha : AlphaE pkg pm Γ a a') :
    ResR pkg pm Eq ((byNameOk ds l ty a).run.run c).1 ((byNameOk ds' l' ty a').run.run c').1 := by
  -- Whether a function value is named: the same on both sides.
  have hname : ∀ {x x' : String}, NameR Γ x x' →
      ResR pkg pm Eq
        ((match resolve ds l c.globals x with
          | .func _ => (pure true : H Bool)
          | .var b => do pure (isFn (← deref b.val))
          | _ => pure false).run.run c).1
        ((match resolve ds' l' c'.globals x' with
          | .func _ => (pure true : H Bool)
          | .var b => do pure (isFn (← deref b.val))
          | _ => pure false).run.run c').1 := by
    intro x x' hx
    have hr := resolve_rel hD hl hg hx
    generalize resolve ds l c.globals x = r at hr ⊢
    generalize resolve ds' l' c'.globals x' = r' at hr ⊢
    cases r <;> cases r' <;> simp only [RefR] at hr <;> try rfl
    all_goals first
      | exact absurd hr id
      | (refine resR_bind (deref_rel hh hr.2) (readOnly_deref _) (readOnly_deref _) ?_
         intro v v' hv
         cases hv <;> rfl)
  unfold byNameOk
  rw [run_get_bind, run_get_bind]
  rcases ty with _ | t
  · cases ha <;> rfl
  · cases t with
    | func ps r =>
      cases ps with
      | nil =>
        cases ha with
        | name hx => exact hname hx
        | _ => rfl
      | cons _ _ => cases ha <;> rfl
    | _ => cases ha <;> rfl

end Gala.Spec
