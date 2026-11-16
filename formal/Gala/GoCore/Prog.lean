/-
  # GoCore: a program's declarations

  The tables a run reads a program's declarations from: per package, its top-level
  functions, types, variables and constants, and the methods of its types, each with the
  file it is declared in (a file's imports are its own; Go spec, "Declarations and
  scope", "Import declarations").
-/
import Std.Data.HashMap
import Gala.GoCore.Value

namespace Gala.GoCore.Full

open Std (HashMap)

/-- A package-level declaration, with the index of its file in its package. -/
inductive Obj
  | func (file : Nat) (typeParams : List Field) (sig : Sig) (body : Option (List Stmt))
  | type (file : Nat) (spec : TypeSpec)
  /-- A package-level variable: its declaration's index among the package's variable
      declarations, in source order, and the name's position in it. -/
  | var (file : Nat) (decl : Nat) (pos : Nat)
  /-- A constant: its type and value expression (a spec without them repeats the
      previous one's, Go spec, "Constant declarations"), and its `iota`. -/
  | const (file : Nat) (ty : Option Expr) (value : Option Expr) (iota : Nat)

/-- A method: its receiver's name (`""` for none) and whether the receiver is a
    pointer, the receiver's type parameters' names, its signature and body. -/
structure Method where
  file : Nat
  recvName : String
  ptr : Bool
  recvTParams : List String
  sig : Sig
  body : Option (List Stmt)

/-- A package-level variable declaration `var a, b T = x, y`. -/
structure VarDecl where
  file : Nat
  names : List String
  ty : Option Expr
  values : List Expr

/-- One package's declarations. -/
structure PkgCtx where
  path : String
  name : String
  /-- Each file's imports: local name and path. -/
  imports : Array (List (String × String))
  objs : HashMap String Obj
  /-- Methods, keyed `Type.method`. -/
  methods : HashMap String Method
  vars : Array VarDecl

instance : Inhabited PkgCtx := ⟨⟨"", "", #[], {}, {}, #[]⟩⟩

/-- The program: its packages by import path, and the main package's path. -/
structure Ctx where
  pkgs : HashMap String PkgCtx
  main : String

instance : Inhabited Ctx := ⟨⟨{}, ""⟩⟩

/-- The name of a receiver's base type and its type parameters' names: `T`, `*T`,
    `T[A, B]`, `*T[A]` (Go spec, "Method declarations"). -/
def recvBase : Expr → Option (String × List String × Bool)
  | .star e => (recvBase e).map fun (n, ps, _) => (n, ps, true)
  | .paren e => recvBase e
  | .ident n => some (n, [], false)
  | .index (.ident n) ps =>
    some (n, ps.filterMap fun | .ident p => some p | _ => none, false)
  | _ => none

/-- The local name of an import: its own, else the imported package's name. -/
def importName (pkgNames : HashMap String String) (i : Import) : String :=
  if i.name.isEmpty then (pkgNames.getD i.path ((i.path.splitOn "/").getLast!)) else i.name

/-- Read one package's declarations. A declaration the tables cannot hold (a method on
    a receiver that is not a type name, `init`, a blank function) becomes the run's
    business: it is reported when reached. -/
def buildPkg (pkgNames : HashMap String String) (p : Package) : PkgCtx := Id.run do
  let mut objs : HashMap String Obj := {}
  let mut methods : HashMap String Method := {}
  let mut vars : Array VarDecl := #[]
  let mut imports : Array (List (String × String)) := #[]
  let mut fi := 0
  for f in p.files do
    imports := imports.push (f.imports.map fun i => (importName pkgNames i, i.path))
    for d in f.decls do
      match d with
      | .func name none tps sig body =>
        objs := objs.insert name (.func fi tps sig body)
      | .func name (some (.mk rnames rty)) _ sig body =>
        match recvBase rty with
        | some (base, tps, ptr) =>
          methods := methods.insert s!"{base}.{name}"
            { file := fi, recvName := rnames.headD "", ptr, recvTParams := tps, sig, body }
        | none => pure ()
      | .type specs =>
        for s in specs do
          match s with
          | .mk n _ _ _ => objs := objs.insert n (.type fi s)
      | .value false specs =>
        for s in specs do
          match s with
          | .mk names ty values =>
            let di := vars.size
            vars := vars.push { file := fi, names, ty, values }
            let mut k := 0
            for n in names do
              objs := objs.insert n (.var fi di k)
              k := k + 1
      | .value true specs =>
        let mut prev : Option Expr × Option Expr × List Expr := (none, none, [])
        let mut iota := 0
        for s in specs do
          match s with
          | .mk names ty values =>
            -- A spec with neither a type nor values repeats the previous one's.
            let (t, vs) := if ty.isNone && values.isEmpty then (prev.2.1, prev.2.2) else (ty, values)
            prev := (none, t, vs)
            let mut k := 0
            for n in names do
              objs := objs.insert n (.const fi t vs[k]? iota)
              k := k + 1
            iota := iota + 1
      | .unsupported _ => pure ()
    fi := fi + 1
  return { path := p.path, name := p.name, imports, objs, methods, vars }

/-- The program's tables. The main package is the first one, as the exporter writes
    it. -/
def buildCtx (prog : Program) : Ctx :=
  let names : HashMap String String :=
    prog.packages.foldl (fun m p => m.insert p.path p.name) {}
  { pkgs := prog.packages.foldl (fun m p => m.insert p.path (buildPkg names p)) {},
    main := (prog.packages.head?.map (·.path)).getD "" }

end Gala.GoCore.Full
