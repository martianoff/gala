/-
  # How much of a corpus steps 3a, 3b and 3c cover

      lake env lean --run scripts/CallCount.lean <file.json or directory>...

  For each Core JSON document (as `galacore` exports a `.gala` file), each function is
  sorted by the first reason the lowering leaves it out, in this order, or counted as lowered:
  `unit-result` (no result), `signature` (a parameter or result not `int`, `bool` or
  `string`), `param-names`, then, for a body the lowering rejects: `reads-package-level`
  (it names a package-level variable), `named-args-or-defaults` (a call of a program
  function names an argument or leaves one out), `calls-outside` (it calls something not
  lowered: another function left out, `Println`, a constructor, a lambda, ...),
  `match-expression` (a `match`, step 4), `return-in-iife` (a `return` inside an arm of an
  `if` used as a value: Go's would leave the IIFE, GALA's the function), `tail-loop-refused`
  (the transformer loops it, `loopGate`, and the loop lowering refuses it where the
  recursive one would not), `body-shape` (anything else). A lowered body (`lowerFun`) is
  `order-predicate` when `Body3` fails (`Gala/M5/CallOrder.lean`), `changeable-param` when
  a call may change a parameter (`chSs`: a function literal assigns it), else
  `lowered-step3c` when it is a self-tail-call loop, `lowered-step3b` when it needs step 3b
  (an `if` used as a value, or one as its last value), else `lowered`. The functions the lowering calls are the
  greatest set whose bodies lower calling only each other. Run on constant-folded
  programs, as galaspec runs them. A development tool for `formal/COVERAGE.md`.
-/
import Gala.Json
import Gala.Const
import Gala.M5.CallOrder

open Gala Gala.Spec Gala.M5

/-- Every subexpression, the expression included. -/
partial def subs (e : Expr) : List Expr :=
  e :: match e with
  | .unary _ x | .select x _ | .paren x | .incdec _ x | .named _ x => subs x
  | .lambda _ _ b => subs b
  | .binary _ l r => subs l ++ subs r
  | .call c as => subs c ++ as.flatMap subs
  | .tuple es | .block es | .interp es => es.flatMap subs
  | .ifExpr c a b => subs c ++ subs a ++ subs b
  | .ifStmt c a b => subs c ++ subs a ++ (b.map subs).getD []
  | .mtch s cs => subs s ++ cs.flatMap fun (_, g, b) => (g.map subs).getD [] ++ subs b
  | .ret v => (v.map subs).getD []
  | .assign _ ts vs => ts.flatMap subs ++ vs.flatMap subs
  | .decl _ _ _ _ vs => vs.flatMap subs
  | _ => []

/-- The arms of the `if`s used as values (IIFEs) in `e`; with `tail`, `e` is a tail (a
    function's last value, or an arm's), whose own `if` is no IIFE. Lambdas are not
    entered. -/
partial def iifeArms (tail : Bool) (e : Expr) : List Expr :=
  match e with
  | .ifExpr c a b => (if tail then [] else [a, b]) ++ iifeArms false c ++ iifeArms tail a ++ iifeArms tail b
  | .ifStmt c a b => iifeArms false c ++ iifeArms tail a ++ ((b.map (iifeArms tail)).getD [])
  | .block es => match es.reverse with
    | [] => []
    | l :: rest => rest.flatMap (iifeArms false) ++ iifeArms tail l
  | .ret v => (v.map (iifeArms false)).getD []
  | .lambda .. => []
  | e => (subs e).tail.flatMap fun x => match x with
    | .ifExpr .. => iifeArms false x
    | _ => []

/-- A body that needs step 3b: an `if` used as a value, or one as its last value. -/
def usesIf (body : Expr) : Bool :=
  (subs body).any (fun | .ifExpr .. => true | _ => false) ||
  match body with
  | .block ss => match ss.getLast? with
    | some (.ifStmt _ _ (some _)) => true
    | _ => false
  | .ifStmt _ _ (some _) => true
  | _ => false

def K : Nat := 1000000

/-- The functions the lowering calls: start from every function with a signature of the
    kinds, drop those whose body does not lower, until nothing changes. -/
partial def fixSigs (ds : Decls) (sg0 : Sigs) (names : List String) : Sigs :=
  -- Each round keeps the names whose bodies lower calling only the names kept so far; the
  -- table is rebuilt from the names, so no earlier round's lowering is rerun.
  let sg : Sigs := fun f => if names.contains f then sg0 f else none
  let names' := names.filter fun f => match ds.funcs.lookup f with
    | some F => (lowerFun K sg f F).isSome
    | none => false
  if names'.length == names.length then sg else fixSigs ds sg0 names'

def reason (ds : Decls) (globals : List String) (sg0 sg : Sigs) (f : String) (F : Func) : String :=
  if F.result.isNone then "unit-result"
  else match sigOf F with
  | none => "signature"
  | some (xks, _) =>
    let names := xks.map (·.1)
    if !decide (ParamsOK names) then "param-names"
    else match lowerFun K sg f F with
    | some lb =>
      if !body3Ss lb then "order-predicate"
      else if (GoCore.Full.Hoist.chSs (fun _ _ => false) false lb).any names.contains then "changeable-param"
      else if loopGate f names.length F.body then "lowered-step3c"
      else if usesIf F.body then "lowered-step3b" else "lowered"
    | none =>
      -- `fixSigs` dropped `f` itself, so the recursive lowering is tried with `f` put back.
      if loopGate f names.length F.body &&
          (lowerBody K (fun g => if g = f then sg0 f else sg g) F).isSome then "tail-loop-refused" else
      let es := subs F.body
      -- A name the body declares (`val`, `var`) is a local, shadowing a package-level one.
      let locals := names ++ es.flatMap (fun | .decl _ ns _ _ _ => ns | _ => [])
      if es.any (fun | .name x => globals.contains x && !locals.contains x | _ => false) then
        "reads-package-level"
      else if es.any (fun
          | .call (.name g) as => match ds.funcs.lookup g with
            | some G => as.any isNamed || as.length < G.params.length
            | none => false
          | _ => false) then "named-args-or-defaults"
      else if es.any (fun
          | .call (.name g) _ => (sg g).isNone
          | .call _ _ => true
          | _ => false) then "calls-outside"
      else if es.any (fun | .mtch .. => true | _ => false) then "match-expression"
      else if (iifeArms true F.body).any (fun a => (subs a).any (fun | .ret _ => true | _ => false)) then
        "return-in-iife"
      else "body-shape"

def main (args : List String) : IO UInt32 := do
  let mut counts : Std.HashMap String Nat := {}
  let mut paths : Array System.FilePath := #[]
  for a in args do
    if ← System.FilePath.isDir a then
      for e in ← System.FilePath.readDir a do
        if e.path.extension == some "json" then paths := paths.push e.path
    else paths := paths.push a
  for path in paths do
    let text ← IO.FS.readFile path
    match Spec.Json.parse text with
    | .error e => IO.eprintln s!"{path}: {e}"
    | .ok p =>
      let p := foldConstants p
      let (ds, gs) := collect p
      let globals := gs.flatMap (·.names)
      let sg0 : Sigs := fun f => (ds.funcs.lookup f).bind sigOf |>.map fun (xks, ρ) => (xks.map (·.2), ρ)
      let sg := fixSigs ds sg0 (ds.funcs.filterMap fun (f, _) => (sg0 f).map fun _ => f)
      for (f, F) in ds.funcs do
        let r := reason ds globals sg0 sg f F
        counts := counts.insert r (counts.getD r 0 + 1)
  -- Every reason, a zero included.
  for r in ["unit-result", "signature", "param-names", "reads-package-level", "named-args-or-defaults",
      "calls-outside", "match-expression", "return-in-iife", "tail-loop-refused", "body-shape",
      "order-predicate", "changeable-param", "lowered-step3c", "lowered-step3b", "lowered"] do
    IO.println s!"{r} {counts.getD r 0}"
  return 0
