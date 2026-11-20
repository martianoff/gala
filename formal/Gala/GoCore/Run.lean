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
  * **A variable whose initializer GoCore cannot run** (it reaches a construct outside
    GoCore, such as reflection in the standard library's hand-written Go) is left
    *poisoned*, reading it stopping the run with that construct, only in a package of
    `quietInitPackages`: the packages galaspec and gala2lean assume print nothing and
    do not panic while they initialize, the same list, kept equal by a test. In any
    other package the run stops there, since that initializer might have printed.
  * **`init` functions** run after their package's variables, in source order.
-/
import Gala.GoCore.Hoist

namespace Gala.GoCore.Full

open Std (HashMap)

mutual
/-- The names an expression refers to, those in `bound` (locals) left out: free
    identifiers, and every selected name (a method is referred to by its name). -/
def refsE (bound : List String) : Expr → List String
  | .ident x => if bound.contains x then [] else [x]
  | .selector x s => s :: refsE bound x
  | .composite t es => refsOE bound t ++ refsEs bound es
  | .keyValue (.ident _) v => refsE bound v
  | .keyValue k v => refsE bound k ++ refsE bound v
  | .funcLit sig body => (refsSs (bound ++ sigNames sig) body).1
  | .paren x | .star x | .unary _ x => refsE bound x
  | .index x is => refsE bound x ++ refsEs bound is
  | .sliceExpr x l h m => refsE bound x ++ refsOE bound l ++ refsOE bound h ++ refsOE bound m
  | .arrayType l e => refsOE bound l ++ refsE bound e
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
  | .defer_ x => (refsE bound x, bound)
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




/-- The packages whose initialization prints nothing and does not panic: every package
    galaspec lets a program import (`noOutputInitImports` in `Gala/Json.lean`, which
    gala2lean shares; `TestNoOutputInitImportsInStep` checks this list holds them, and
    `TestQuietInitPackages` checks the assumption for the GALA packages among them), and
    the Go packages of which GoCore runs some files (`gocoreexport.RunnableStdlib`;
    `TestRunnableStdlibIsQuiet`), whose other files' initializers it leaves out. The spec
    need not trust the latter, so this list may be wider than galaspec's:
    `cmp`, `math/bits` and `slices` (Go 1.25.5) have no `init` function and no
    package-level variable but `math/bits`' two constant tables (`bits.go`) and its two
    error variables (`bits_errors.go`), declared without an initializer and linknamed to
    the runtime's; and all three are in `fmt`'s dependency closure (`go list -deps fmt`),
    so any program that prints has initialized them, and what they import. -/
def quietInitPackages : List String :=
  ["bytes", "cmp", "errors", "fmt", "math", "math/bits", "os", "slices", "sort", "strconv",
   "strings", "sync", "time", "unicode", "unicode/utf8", "martianoff/gala/std",
   "martianoff/gala/collection_immutable"]

/-- The package-level variables of `p` the expressions `es` depend on, transitively
    through the functions and methods they refer to; `none` if the walk outgrew its
    bound, which only a program with more than a million references reaches. -/
def varDeps (p : PkgCtx) (es : List Expr) : Option (List String) := Id.run do
  let body (sig : Sig) (extra : List String) (ss : List Stmt) : List String :=
    (refsSs (extra ++ sigNames sig) ss).1
  let mut seen : Std.HashSet String := {}
  let mut todo := es.flatMap (refsE [])
  let mut vars : List String := []
  -- Each declaration's body is added once (`seen`), so the worklist empties; the
  -- bound only makes that structural.
  for _ in [0:1000000] do
    match todo with
    | [] => return some vars
    | x :: rest =>
      todo := rest
      if seen.contains x then continue
      seen := seen.insert x
      match p.objs.get? x with
      | some (.var ..) => vars := x :: vars
      | some (.func _ _ sig (some ss)) => todo := body sig [] ss ++ todo
      | _ => pure ()
      -- `x` may name methods.
      for m in p.methodsByName.getD x [] do
        if let some ss := m.body then todo := body m.sig [m.recvName] ss ++ todo
  return if todo.isEmpty then some vars else none

/-- Initialize the package-level variables of package `path`, then run its `init`
    functions in source order (Go spec, "Package initialization"). In a package of
    `quietInitPackages`, a variable whose initializer reaches a construct outside
    GoCore is poisoned; anywhere else that stops the run. -/
def initPkg (c : Ctx) (n : Nat) (path : String) : GM Unit := do
  let p ← pkgCtx c path
  let quiet := quietInitPackages.contains path
  let decls := p.vars.toList.zipIdx
  let deps ← decls.mapM fun (d, i) => match varDeps p (d.values ++ d.ty.toList) with
    | some vs => pure (i, vs)
    | none => unsupported "init-dependencies"
  let mut done : Std.HashSet Nat := {}
  let mut initialized : Std.HashSet String := {}
  for _ in [0:decls.length] do
    let ready := decls.find? fun (d, i) =>
      !done.contains i && ((deps.lookup i).getD []).all fun x => initialized.contains x || d.names.contains x
    let some (d, i) := ready | stuck s!"initialization cycle in {path}"
    let env : Env := { pkg := path, file := d.file }
    let r ← tryCatch (do
        let vs ← if d.values.isEmpty then pure [] else evalArgs c n env d.values
        let t? ← d.ty.mapM (evalType c n env)
        let vals ← (List.range d.names.length).mapM fun k => declValue c n t? vs[k]?
        pure (Except.ok (vals.map fun v => (t?.getD v.ty, v))))
      fun
        | .unsupported what =>
          if quiet then do
            -- The initializer stopped part-way: no call it made is running any more.
            modify fun s => { s with defers := [], recoverable := none }
            pure (.error what)
          else throw (.unsupported what)
        | e => throw e
    let vals := match r with
      | .ok vs => vs
      | .error what => d.names.map fun _ => (Ty.tuple [], Val.poison what)
    for (x, (t, v)) in d.names.zip vals do
      if x != "_" then
        let cell ← allocAs t v
        modify fun s => { s with globals := s.globals.insert s!"{path}.{x}" cell }
      initialized := initialized.insert x
    done := done.insert i
  for (fi, body) in p.inits do
    let _ ← callBody c n { pkg := path, file := fi } (.mk [] [] false) body none [] false

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

/-- Run a program: initialize its packages, then call the main package's `main`, with
    `depth` nested evaluation levels and `fuel` steps (calls and loop iterations), its
    operands evaluated in the order `ord` (`Hoist.lean`). Unlike `runProgram`, the
    depth is not capped by a constant: galaspec bounds its depth by the fuel alone, and
    M5's theorems relate the two runs at depth and fuel equal (docs/FORMAL_SEMANTICS.MD,
    "M5"). -/
def runProgramAt (ord : Order) (prog : Program) (depth fuel : Nat) : Outcome × String :=
  let c := buildCtx (ord prog)
  let go : GM (Option String) := do
    tryCatch (do
        for path in pkgOrder c do
          initPkg c depth path
        let mainFn ← funcVal c depth c.main "main" []
        let _ ← callValue c depth mainFn [] false
        pure none) fun
      -- A panic that ends the run: the text Go prints, made now, after every
      -- deferred call ran (runtime `printpanics`), by its `Error` or `String` too.
      | .panic v runtime => do
        let text ← if runtime then asStr v else panicText c depth v
        pure (some text)
      | e => throw e
  let (r, s) := (go.run).run { cells := #[], closures := #[], out := "", steps := fuel }
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

/-- Run a program: initialize its packages, then call the main package's `main`, in
    gc 1.25.5's order of evaluation (`Order.gc`). `fuel` bounds the steps (calls and
    loop iterations), and the depth up to `maxDepth`. -/
def runProgram (prog : Program) (fuel : Nat) : Outcome × String :=
  runProgramAt .gc prog (min fuel maxDepth) fuel

end Gala.GoCore.Full
