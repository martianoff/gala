/-
  # GoCore: running a program

  Package initialization, then `main` (Go spec, "Program initialization and execution").

  * **Packages** are initialized one at a time: of the packages sorted by import path,
    the first not yet initialized whose imports all are (Go spec, "Package
    initialization"). Only the packages the program's document holds count: Go's own
    standard library (`fmt`, ...) is not exported, and its initialization prints
    nothing.
  * **Variables** of a package are initialized repeatedly taking the earliest one in
    declaration order that depends on no uninitialized variable. A variable depends on
    the variables and functions its initializer refers to, and transitively on what those
    functions refer to; a method is referred to by its name (`x.m` refers to every method
    `m` of the package), which can only add dependencies Go does not count.
  * **An imported package's variable whose initializer GoCore cannot run** (it reaches
    a construct outside GoCore, such as reflection in the standard library's hand-written
    Go) is left *poisoned*: reading it stops the run with that construct. This assumes
    such an initializer prints nothing and does not panic, the assumption galaspec makes
    for the packages it lets a program import (`Json.noOutputInitImports`). In the main
    package the run stops there instead.
-/
import Gala.GoCore.Eval

namespace Gala.GoCore.Full

open Std (HashMap)

/-- The names a field list binds. -/
def boundBy (fs : List Field) : List String := fs.flatMap fun | .mk ns _ => ns

/-- The names `:=` declares. -/
def declaredBy : List Expr → List String
  | [] => []
  | .ident x :: es => x :: declaredBy es
  | _ :: es => declaredBy es

mutual
/-- The names an expression refers to, those in `bound` (locals) left out: free
    identifiers, and every selected name (a method is referred to by its name). -/
def refsE (bound : List String) : Expr → List String
  | .ident x => if bound.contains x then [] else [x]
  | .selector x s => s :: refsE bound x
  | .composite t es => refsOE bound t ++ refsEs bound es
  | .keyValue (.ident _) v => refsE bound v
  | .keyValue k v => refsE bound k ++ refsE bound v
  | .funcLit (.mk ps rs _) body => (refsSs (bound ++ boundBy ps ++ boundBy rs) body).1
  | .paren x | .star x | .unary _ x => refsE bound x
  | .index x is => refsE bound x ++ refsEs bound is
  | .typeAssert x t => refsE bound x ++ refsE bound t
  | .call f as _ => refsE bound f ++ refsEs bound as
  | .binary _ x y => refsE bound x ++ refsE bound y
  | _ => []
def refsEs (bound : List String) : List Expr → List String
  | [] => []
  | e :: es => refsE bound e ++ refsEs bound es
def refsOE (bound : List String) : Option Expr → List String
  | none => []
  | some e => refsE bound e
/-- The names statements refer to, each in the scope the statements before it
    left (Go spec, "Declarations and scope"), and the names bound after them. -/
def refsSs (bound : List String) : List Stmt → List String × List String
  | [] => ([], bound)
  | s :: ss =>
    let (r, b) := refsS bound s
    let (rs, b') := refsSs b ss
    (r ++ rs, b')
/-- An optional statement (an `if`'s or `for`'s init, an `else`, a post). -/
def refsOS (bound : List String) : Option Stmt → List String × List String
  | none => ([], bound)
  | some s => refsS bound s
/-- The names a statement refers to, and the names bound after it. -/
def refsS (bound : List String) : Stmt → List String × List String
  | .block ss => ((refsSs bound ss).1, bound)
  | .decl (.value _ specs) => (refsSpecs bound specs, bound ++ specNames specs)
  | .expr x | .incDec _ x => (refsE bound x, bound)
  | .assign tok l r =>
    if tok == ":=" then (refsEs bound r, bound ++ declaredBy l)
    else (refsEs bound l ++ refsEs bound r, bound)
  | .ret es => (refsEs bound es, bound)
  | .if_ i c b e =>
    let (ri, bi) := refsOS bound i
    (ri ++ refsE bi c ++ (refsSs bi b).1 ++ (refsOS bi e).1, bound)
  | .switch i t cs =>
    let (ri, bi) := refsOS bound i
    (ri ++ refsOE bi t ++ refsClauses bi bi cs, bound)
  | .typeSwitch i x y cs =>
    let (ri, bi) := refsOS bound i
    (ri ++ refsE bi y ++ refsClauses bi (bi ++ x.toList) cs, bound)
  | .for_ i c p b =>
    let (ri, bi) := refsOS bound i
    (ri ++ refsOE bi c ++ (refsOS bi p).1 ++ (refsSs bi b).1, bound)
  | .range k v tok x b =>
    let bk := if tok == ":=" then bound ++ declaredBy (k.toList ++ v.toList) else bound
    (refsE bound x ++ (refsSs bk b).1, bound)
  | _ => ([], bound)
/-- Switch clauses: their cases in scope `bl`, their bodies in scope `bb`. -/
def refsClauses (bl bb : List String) : List Clause → List String
  | [] => []
  | .mk l _ b :: cs => refsEs bl l ++ (refsSs bb b).1 ++ refsClauses bl bb cs
/-- The values of `var` specs. -/
def refsSpecs (bound : List String) : List Spec → List String
  | [] => []
  | .mk _ t vs :: specs => refsOE bound t ++ refsEs bound vs ++ refsSpecs bound specs
/-- The names `var` specs declare. -/
def specNames : List Spec → List String
  | [] => []
  | .mk ns _ _ :: specs => ns ++ specNames specs
end


/-- The package-level variables of `p` the expressions `es` depend on, transitively
    through the functions and methods they refer to. -/
def varDeps (p : PkgCtx) (es : List Expr) : List String := Id.run do
  let body (sig : Sig) (extra : List String) (ss : List Stmt) : List String :=
    let .mk ps rs _ := sig
    (refsSs (extra ++ boundBy ps ++ boundBy rs) ss).1
  let mut seen : List String := []
  let mut todo := es.flatMap (refsE [])
  let mut vars : List String := []
  -- Each declaration's body is added once (`seen`), so the worklist empties; the
  -- bound only makes that structural.
  for _ in [0:1000000] do
    match todo with
    | [] => break
    | x :: rest =>
      todo := rest
      if seen.contains x then continue
      seen := x :: seen
      match p.objs.get? x with
      | some (.var ..) => vars := x :: vars
      | some (.func _ _ sig (some ss)) => todo := body sig [] ss ++ todo
      | _ => pure ()
      -- `x` may name methods.
      for (k, m) in p.methods.toList do
        if k.endsWith s!".{x}" then
          if let some ss := m.body then todo := body m.sig [m.recvName] ss ++ todo
  return vars

/-- Initialize the package-level variables of package `path`. -/
def initPkg (c : Ctx) (n : Nat) (path : String) (isMain : Bool) : GM Unit := do
  let p ← pkgCtx c path
  let decls := p.vars.toList.zipIdx
  let deps := decls.map fun (d, i) => (i, varDeps p (d.values ++ d.ty.toList))
  let mut done : List Nat := []
  for _ in [0:decls.length] do
    let initialized := done.flatMap fun i => (p.vars[i]?.map (·.names)).getD []
    let ready := decls.find? fun (d, i) =>
      !done.contains i && ((deps.lookup i).getD []).all fun x => initialized.contains x || d.names.contains x
    let some (d, i) := ready | stuck s!"initialization cycle in {path}"
    let env : Env := { pkg := path, file := d.file }
    let r ← tryCatch (do
        let vs ← if d.values.isEmpty then pure [] else evalArgs c n env d.values
        let t? ← d.ty.mapM (evalType c n env)
        let mut out : List Val := []
        for k in [0:d.names.length] do
          let v ← match vs[k]?, t? with
            | some v, some t => convert c n t v
            | some v, none => pure (defaultVal v)
            | none, some t => zero c n t
            | none, none => stuck "var without type or value"
          out := out ++ [v]
        pure (Except.ok out))
      fun
        | .unsupported what => if isMain then throw (.unsupported what) else pure (.error what)
        | e => throw e
    let vals := match r with
      | .ok vs => vs
      | .error what => d.names.map fun _ => .poison what
    for (x, v) in d.names.zip vals do
      if x != "_" then
        let cell ← alloc v
        modify fun s => { s with globals := s.globals.insert s!"{path}.{x}" cell }
    done := i :: done

/-- The order packages are initialized in (Go spec, "Package initialization"). -/
def pkgOrder (c : Ctx) : List String := Id.run do
  let paths := (c.pkgs.toList.map (·.1)).mergeSort (fun a b => decide (a ≤ b))
  let mut order : List String := []
  for _ in [0:paths.length] do
    let next := paths.find? fun q =>
      !order.contains q && match c.pkgs.get? q with
        | some p => p.imports.toList.all fun is => is.all fun (_, ip) =>
            order.contains ip || !c.pkgs.contains ip || ip == q
        | none => false
    match next with
    | some q => order := order ++ [q]
    | none => break
  return order

/-- How a run ended. -/
inductive Outcome
  | ok
  /-- A panic, with the text Go's runtime prints after `panic: `. -/
  | panic (text : String)
  | fuel
  | unsupported (what : String)
  | stuck (msg : String)

/-- The most nested evaluation levels a run may take, about eight per Go call. The
    compiled evaluator uses the native stack for them, so the depth is capped well
    below what that stack holds: a deeper run ends in `fuel` (inconclusive), as a
    deep Go recursion would end in a stack overflow, which the differential run
    reports instead of comparing. -/
def maxDepth : Nat := 1000000

/-- Run a program: initialize its packages, then call the main package's `main`.
    `fuel` bounds the steps (calls and loop iterations), and the depth up to
    `maxDepth`. -/
def runProgram (prog : Program) (fuel : Nat) : Outcome × String :=
  let c := buildCtx prog
  let depth := min fuel maxDepth
  let go : GM Unit := do
    for path in pkgOrder c do
      initPkg c depth path (path == c.main)
    let mainFn ← funcVal c depth c.main "main" []
    let _ ← callValue c depth mainFn []
  let (r, s) := (go.run).run { cells := #[], closures := #[], out := "", steps := fuel }
  let o := match r with
    | .ok () => .ok
    | .error (.panic _ text) => .panic text
    | .error .fuel => .fuel
    | .error (.unsupported what) => .unsupported what
    | .error (.stuck msg) => .stuck msg
    | .error (.ret _) => .stuck "return outside a function"
    | .error .brk | .error .cont => .stuck "break or continue outside a loop"
  (o, s.out)

end Gala.GoCore.Full
