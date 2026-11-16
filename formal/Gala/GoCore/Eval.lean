/-
  # GoCore: evaluation of whole programs

  Big-step evaluation of the Go the transpiler emits (docs/FORMAL_SEMANTICS.MD, "M4").
  Every construct cites the Go specification section it follows; `Gala/GoCore/Value.lean`
  gives the modelling choices for values. Nothing here is GALA-specific: the program's
  sealed types, `Unapply`, `Equal` and `String` methods and the standard library's
  generated Go run as the Go they are. Only the hand-written, reflective functions of the
  standard library that `Lib` names, and the `fmt` functions `Fmt` names, are models.

  **Fuel.** Every function takes a depth `n` and passes `n - 1` to what it calls, so
  evaluation is structural recursion and always ends; a run whose depth runs out ends in
  `fuel`. Each call and each loop iteration also takes a step from the state's budget
  (`tick`). Either way, `fuel` is inconclusive.

  **Precondition.** GoCore's semantics is defined for programs `go build` accepts; on a
  program Go rejects, its answer is unspecified. Values carry their dynamic type, so
  some answers rest on this: `==` on values of different dynamic types is `false`, as for
  two interface operands, since a comparison of different static types does not
  compile. The exporter only ever sees the transpiler's compiled output, and the
  differential run only runs programs that built.

  **What stops a run.** A construct GoCore does not model stops it with
  `unsupported <construct>`. Something no program Go compiles does (a type error) is
  `stuck`: a GoCore or exporter bug, never a verdict.
-/
import Gala.GoCore.Ops
import Gala.GoCore.Prog

namespace Gala.GoCore.Full

open Std (HashMap)

/-- Where code runs: its package and file (for imports), the local scopes (newest
    first; each binds names to cells), the type parameters in scope, and `iota` in a
    constant declaration. -/
structure Env where
  pkg : String
  file : Nat
  scopes : List (List (String × Nat)) := [[]]
  tenv : List (String × Ty) := []
  iota : Option Int := none
  deriving Inhabited

def Env.lookup (env : Env) (x : String) : Option Nat :=
  env.scopes.findSome? (·.lookup x)

def Env.bind (env : Env) (x : String) (cell : Nat) : Env :=
  if x == "_" then env else
  match env.scopes with
  | s :: rest => { env with scopes := ((x, cell) :: s) :: rest }
  | [] => { env with scopes := [[(x, cell)]] }

def Env.push (env : Env) : Env := { env with scopes := [] :: env.scopes }

/-- The predeclared types (Go spec, "Predeclared identifiers"). -/
def universeType? : String → Option Ty
  | "bool" => some .bool
  | "string" => some .string
  | "any" => some (.iface [])
  | "error" => some (.iface ["Error"])
  | n => (IntKind.ofName? n).map .int

/-- The standard library is the package with this import path; `Lib` replaces some of
    its hand-written functions by models. -/
def stdPath : String := "martianoff/gala/std"

def pkgCtx (c : Ctx) (path : String) : GM PkgCtx :=
  match c.pkgs.get? path with
  | some p => pure p
  | none => stuck s!"no package {path}"

/-- The import path a file's import name stands for. -/
def importOf (c : Ctx) (env : Env) (x : String) : Option String :=
  (c.pkgs.get? env.pkg).bind fun p => (p.imports[env.file]?).bind (·.lookup x)

/-- The package a bare, non-local name `x` is declared in: the current one, else a
    package the file dot-imports (Go spec, "Import declarations": `import . "p"` declares
    `p`'s exported names in the file's block). `none` for a predeclared name or one of
    no package the document holds. -/
def nameOwner (c : Ctx) (env : Env) (x : String) : Option String :=
  match c.pkgs.get? env.pkg with
  | none => none
  | some p =>
    if p.objs.contains x then some env.pkg
    else ((p.imports[env.file]?).getD []).findSome? fun (n, path) =>
      if n == "." && ((c.pkgs.get? path).map (·.objs.contains x)).getD false then some path else none

/-- A package the file dot-imports that the document does not hold (Go's own standard
    library), whose names GoCore cannot see. -/
def foreignDotImport (c : Ctx) (env : Env) : Option String :=
  ((c.pkgs.get? env.pkg).bind (·.imports[env.file]?)).bind fun is =>
    is.findSome? fun (n, path) => if n == "." && !c.pkgs.contains path then some path else none

/-- Whether a name is exported (Go spec, "Exported identifiers"). -/
def exported (x : String) : Bool := x.front.isUpper

/-- The methods of a type: those of `pkg.name`, keyed by method name. -/
def methodOf (c : Ctx) (pkg tyName m : String) : Option Method :=
  (c.pkgs.get? pkg).bind (·.methods.get? s!"{tyName}.{m}")

/-- The method `m` in the method set of a value of type `t` (Go spec, "Method sets"): a
    defined type's value-receiver methods, and for a pointer to it all of them. Returns
    the type's package and name, and the method. -/
def methodSet (c : Ctx) (t : Ty) (m : String) : Option (String × String × List Ty × Method) :=
  match t with
  | .named p n args => (methodOf c p n m).bind fun me => if me.ptr then none else some (p, n, args, me)
  | .ptr (.named p n args) => (methodOf c p n m).map fun me => (p, n, args, me)
  | _ => none

/-- The models of hand-written standard-library functions (see `Lib` below), by name.
    The exported Go of a function named here is never run. -/
def libFuncs : List String := ["FormatStruct", "Copy", "Equal", "EvalOperand"]

/-- The `fmt` functions modelled (see `Fmt` below). -/
def fmtFuncs : List String := ["Println", "Print", "Sprintf", "Sprint"]

/-- The names a field list declares: parameters, type parameters, interface methods. -/
def fieldListNames (fs : List Field) : List String := fs.flatMap fun | .mk ns _ => ns

/-- Bind type parameters to type arguments. -/
def bindTParams (ps : List Field) (args : List Ty) : GM (List (String × Ty)) := do
  let names := fieldListNames ps
  if names.length != args.length then stuck "wrong number of type arguments"
  pure (names.zip args)

/-- The number of fields of a struct field list, names counted. -/
def fieldNames (fs : List Field) : List String :=
  fs.flatMap fun
    | .mk [] (.star (.ident n)) | .mk [] (.ident n) => [n]
    | .mk [] (.star (.selector _ n)) | .mk [] (.selector _ n) => [n]
    | .mk [] (.index (.ident n) _) => [n]
    | .mk [] _ => ["?"]
    | .mk ns _ => ns

/-- The results of a call as a list. -/
def Val.results : Val → List Val
  | .tuple vs => vs
  | v => [v]

def Val.single : List Val → GM Val
  | [v] => pure v
  | vs => pure (.tuple vs)

def asBool : Val → GM Bool
  | .bool _ b => pure b
  | _ => stuck "not a bool"

def asInt : Val → GM Int
  | .int _ n => pure n
  | _ => stuck "not an integer"

def asStr : Val → GM String
  | .str _ s => pure s
  | _ => stuck "not a string"

mutual
/-- Type parameters bound by matching a parameter's type expression against an
    argument's type, for function argument type inference (Go spec, "Type inference":
    the type equations of the call's typed arguments). -/
def unify (tps : List String) : Expr → Ty → List (String × Ty)
  | .ident x, t => if tps.contains x then [(x, t)] else []
  | .star e, .ptr t => unify tps e t
  | .index (.ident _) es, .named _ _ args => unifyAll tps es args
  | .index (.selector _ _) es, .named _ _ args => unifyAll tps es args
  | .paren e, t => unify tps e t
  | _, _ => []
/-- `unify` on type arguments, pairwise. -/
def unifyAll (tps : List String) : List Expr → List Ty → List (String × Ty)
  | e :: es, t :: ts => unify tps e t ++ unifyAll tps es ts
  | _, _ => []
end

/-- Run `body` until it returns `none`, at most `k` times: a loop's iterations. Each
    iteration of a GoCore loop takes a step (`tick`), so the bound is never what ends
    it; a loop that would run past it has run out of steps first. -/
def loopM (k : Nat) (env : Env) (body : Env → GM (Option Env)) : GM Unit :=
  match k with
  | 0 => throw .fuel
  | k + 1 => do
    match ← body env with
    | some env' => loopM k env' body
    | none => pure ()

/-- `unwrapImmutable` (std/types.go, lines 103-108), a model: an `Immutable` value's
    wrapped value, any other value itself. -/
def unwrapImmutable : Val → Val
  | v@(.struct_ (.named p "Immutable" _) [inner]) => if p == stdPath then inner else v
  | v => v

/-- The type of `a && b` and `a || b`: untyped if both operands are (Go spec, "Logical
    operators"). -/
def boolType (a b : Val) : Ty :=
  match a.ty with
  | .untyped _ => b.ty
  | t => t

/-- The types a field list declares, one per name. -/
def fieldTypes (fs : List Field) : List Expr :=
  fs.flatMap fun | .mk ns t => if ns.isEmpty then [t] else ns.map fun _ => t

mutual

-- Types

/-- The type an expression denotes, or `none` if it denotes no type. -/
def typeOf? (c : Ctx) : Nat → Env → Expr → GM (Option Ty)
  | 0, _, _ => throw .fuel
  | n + 1, env, e => do
    match e with
    | .ident x =>
      if (env.lookup x).isSome then return none
      if let some t := env.tenv.lookup x then return some t
      match nameOwner c env x with
      | some path =>
        match (c.pkgs.get? path).bind (·.objs.get? x) with
        | some (.type fi (.mk _ tps alias te)) =>
          if alias then return some (← evalType c n { pkg := path, file := fi } te)
          if tps.isEmpty then return some (.named path x []) else stuck s!"generic type {x} without type arguments"
        | _ => return none
      | none => return universeType? x
    | .selector (.ident q) sel =>
      if (env.lookup q).isSome then return none
      match importOf c env q with
      | some path =>
        match c.pkgs.get? path with
        | some p =>
          match p.objs.get? sel with
          | some (.type fi (.mk _ tps alias te)) =>
            if alias then return some (← evalType c n { pkg := path, file := fi } te)
            if tps.isEmpty then return some (.named path sel []) else stuck s!"generic type {sel} without type arguments"
          | _ => return none
        | none => return none
      | none => return none
    | .index x args =>
      -- A generic type instantiated: `T[args]` or `pkg.T[args]`.
      let target : Option (String × String) := match x with
        | .ident t => if (env.lookup t).isSome then none else (nameOwner c env t).map (·, t)
        | .selector (.ident q) t =>
          if (env.lookup q).isSome then none else (importOf c env q).map (·, t)
        | _ => none
      match target with
      | some (path, t) =>
        match (c.pkgs.get? path).bind (·.objs.get? t) with
        | some (.type _ (.mk _ (_ :: _) _ _)) =>
          return some (.named path t (← args.mapM (evalType c n env)))
        | _ => return none
      | none => return none
    | .star x => return (← typeOf? c n env x).map .ptr
    | .paren x => typeOf? c n env x
    | .structType _ | .interfaceType _ | .funcType _ => return some (← evalType c n env e)
    | .unsupported k => unsupported k
    | _ => return none

/-- The type a type expression denotes (Go spec, "Types"). -/
def evalType (c : Ctx) : Nat → Env → Expr → GM Ty
  | 0, _, _ => throw .fuel
  | n + 1, env, e => do
    match e with
    | .structType fs =>
      let tys ← (fieldTypes fs).mapM (evalType c n env)
      return .struct_ ((fieldNames fs).zip tys)
    | .interfaceType es =>
      -- An embedded element (`error`, a constraint's type union) would add to the
      -- method set, which GoCore does not compute; only methods it lists itself.
      if es.any (fun | .mk ns _ => ns.isEmpty) then unsupported "embedded-interface"
      -- Sorted, so an interface type does not depend on the order it lists them in.
      return .iface ((fieldListNames es).mergeSort (fun a b => decide (a ≤ b)))
    | .funcType (.mk ps rs v) =>
      return .func_ (← (fieldTypes ps).mapM (evalType c n env)) (← (fieldTypes rs).mapM (evalType c n env)) v
    | .unsupported k => unsupported k
    | _ =>
      match ← typeOf? c n env e with
      | some t => pure t
      | none =>
        -- A type of a package the document does not hold (Go's own standard library).
        if let .selector (.ident q) sel := e then
          if let some path := importOf c env q then
            if !c.pkgs.contains path then unsupported s!"{path}.{sel}"
        match e with
        | .index (.selector (.ident q) sel) _ =>
          if let some path := importOf c env q then
            if !c.pkgs.contains path then unsupported s!"{path}.{sel}"
          stuck "not a type"
        | .ident x =>
          if ["comparable", "float32", "float64", "complex64", "complex128"].contains x then
            unsupported s!"type-{x}"
          if let some p := foreignDotImport c env then unsupported s!"dot-import-{p}"
          stuck s!"{x} is not a type"
        | _ => stuck "not a type"

/-- The underlying type of `t` (Go spec, "Underlying types"). -/
def underlying (c : Ctx) : Nat → Ty → GM Ty
  | 0, _ => throw .fuel
  | n + 1, t => do
    match t with
    | .named path name args =>
      let p ← pkgCtx c path
      match p.objs.get? name with
      | some (.type fi (.mk _ tps _ te)) =>
        let tenv ← bindTParams tps args
        underlying c n (← evalType c n { pkg := path, file := fi, tenv } te)
      | _ => stuck s!"no type {path}.{name}"
    | t => pure t

/-- The zero value of `t` (Go spec, "The zero value"). -/
def zero (c : Ctx) : Nat → Ty → GM Val
  | 0, _ => throw .fuel
  | n + 1, t => do
    match ← underlying c n t with
    | .int _ => pure (.int t 0)
    | .bool => pure (.bool t false)
    | .string => pure (.str t "")
    | .struct_ fs => pure (.struct_ t (← fs.mapM fun (_, ft) => zero c n ft))
    | .ptr _ => pure (.ptr t none)
    | .func_ .. => pure (.func_ t none)
    | .iface _ => pure (.nilIface t)
    | u => unsupported s!"zero-value-{u}"

/-- `v` assigned to a variable of type `t` (Go spec, "Assignability", "Representability"):
    an untyped constant takes the type, checked to be representable; a value assigned to
    an interface type keeps its own dynamic type; a value of an identical underlying type
    takes the variable's type. -/
def convert (c : Ctx) : Nat → Ty → Val → GM Val
  | 0, _, _ => throw .fuel
  | n + 1, t, v => do
    -- A value already of the variable's type: nothing to do (the common case).
    if v.ty == t then return v
    let u ← underlying c n t
    match u, v with
    | .iface _, .nilIface _ => pure (.nilIface t)
    -- Assigned to an interface, a value keeps its dynamic value, an untyped constant
    -- its default type (Go spec, "Assignability", "Constants").
    | .iface _, v => pure (.boxed t (defaultVal v.dyn))
    | .int k, .int (.untyped _) x =>
      if k.represents x then pure (.int t x) else stuck s!"constant {x} overflows {k.name}"
    | .bool, .bool (.untyped _) b => pure (.bool t b)
    | .string, .str (.untyped _) s => pure (.str t s)
    | .ptr _, .nilIface (.untyped .nil) => pure (.ptr t none)
    | .func_ .., .nilIface (.untyped .nil) => pure (.func_ t none)
    | _, .int vt x => if (← underlying c n vt) == u then pure (.int t x) else stuck s!"cannot use {vt} as {t}"
    | _, .bool _ b => if u == .bool then pure (.bool t b) else stuck s!"cannot use bool as {t}"
    | _, .str _ s => if u == .string then pure (.str t s) else stuck s!"cannot use string as {t}"
    | _, .struct_ vt fs => if (← underlying c n vt) == u then pure (.struct_ t fs) else stuck s!"cannot use {vt} as {t}"
    | _, .ptr _ l => pure (.ptr t l)
    | _, .func_ _ f => pure (.func_ t f)
    | _, v => stuck s!"cannot use {v.ty} as {t}"

/-- An explicit conversion `T(x)` (Go spec, "Conversions"): between integer types it
    wraps (a constant must be representable), and between types with identical
    underlying types it retypes. -/
def conversion (c : Ctx) : Nat → Ty → Val → GM Val
  | 0, _, _ => throw .fuel
  | n + 1, t, v => do
    let u ← underlying c n t
    match u, v with
    | .int k, .int (.untyped _) x =>
      if k.represents x then pure (.int t x) else stuck s!"constant {x} overflows {k.name}"
    | .int k, .int _ x => pure (.int t (k.wrap x))
    | .string, .int _ _ => unsupported "string-of-int"
    | _, _ => convert c n t v

-- Expressions

/-- Evaluate an expression to one value (several for a call with several results, as
    a `tuple`). -/
def evalExpr (c : Ctx) : Nat → Env → Expr → GM Val
  | 0, _, _ => throw .fuel
  | n + 1, env, e => do
    match e with
    | .ident "_" => stuck "_ as a value"
    | .ident x =>
      if let some cell := env.lookup x then return ← loadCell cell
      evalName c n env ((nameOwner c env x).getD env.pkg) x
    | .intLit v => pure (.int (.untyped .int) v)
    | .charLit v => pure (.int (.untyped .rune) v)
    | .strLit s => pure (.str (.untyped .string) s)
    | .paren x => evalExpr c n env x
    | .composite (some te) elts => composite c n env (← evalType c n env te) elts
    | .composite none _ => unsupported "elided-composite-type"
    | .funcLit sig body => do
      let id := (← get).closures.size
      let cl : Closure := { pkg := env.pkg, file := env.file, scopes := env.scopes, tenv := env.tenv, sig, body }
      modify fun s => { s with closures := s.closures.push cl }
      let t ← evalType c n env (.funcType sig)
      pure (.func_ t (some (.closure id)))
    | .selector x sel => selector c n env x sel
    | .index x _ =>
      match ← funcValue? c n env e with
      | some v => pure v
      | none =>
        let _ ← evalExpr c n env x
        unsupported "index"
    | .typeAssert x te => do
      let v ← evalExpr c n env x
      let t ← evalType c n env te
      match ← assertType c n v t with
      | some w => pure w
      -- Go's text names the operand's static interface type in Go's own spelling;
      -- the panic is not modelled.
      | none => unsupported "failed-type-assertion"
    | .call fn args ellipsis => do
      if ellipsis then unsupported "call-ellipsis"
      Val.single (← evalCall c n env fn args)
    | .star x => do
      match ← evalExpr c n env x with
      | .ptr _ (some l) => load l
      | .ptr _ none => nilDeref
      | _ => stuck "* of a non-pointer"
    | .unary "&" x =>
      match x with
      | .composite (some te) elts => do
        let v ← composite c n env (← evalType c n env te) elts
        let cell ← alloc v
        pure (.ptr (.ptr v.ty) (some ⟨cell, []⟩))
      | _ => do
        let (l, t) ← evalAddr c n env x
        pure (.ptr (.ptr t) (some l))
    | .unary op x => unary c n op (← evalExpr c n env x)
    | .binary "&&" x y => do
      let a ← evalExpr c n env x
      if ← asBool a then
        let b ← evalExpr c n env y
        pure (.bool (boolType a b) (← asBool b))
      else pure a
    | .binary "||" x y => do
      let a ← evalExpr c n env x
      if ← asBool a then pure a
      else
        let b ← evalExpr c n env y
        pure (.bool (boolType a b) (← asBool b))
    | .binary op x y => do
      let a ← evalExpr c n env x
      let b ← evalExpr c n env y
      binary c n op a b
    | .unsupported k => unsupported k
    | .keyValue .. => stuck "key: value outside a composite literal"
    | .structType _ | .interfaceType _ | .funcType _ => stuck "a type as a value"

/-- A package-level name of package `path`: a variable, constant or function (Go
    spec, "Declarations and scope"), else a predeclared one. -/
def evalName (c : Ctx) : Nat → Env → String → String → GM Val
  | 0, _, _, _ => throw .fuel
  | n + 1, env, path, x => do
    let p ← pkgCtx c path
    match p.objs.get? x with
    | some (.var _ _ _) =>
      match (← get).globals.get? s!"{path}.{x}" with
      | some cell => loadCell cell
      | none => stuck s!"package variable {x} read before its initialization"
    | some (.const fi te ve iota) =>
      match ve with
      | none => stuck s!"constant {x} without a value"
      | some ve =>
        let v ← evalExpr c n { pkg := path, file := fi, iota := some iota } ve
        match te with
        | some te => convert c n (← evalType c n { pkg := path, file := fi } te) v
        | none => pure v
    | some (.func _ tps _ _) =>
      if !tps.isEmpty then stuck s!"generic function {x} without type arguments"
      funcVal c n path x []
    | some (.type ..) => stuck s!"type {x} as a value"
    | none =>
      if path != env.pkg then stuck s!"no {path}.{x}"
      match x with
      | "true" => pure (.bool (.untyped .bool) true)
      | "false" => pure (.bool (.untyped .bool) false)
      | "nil" => pure (.nilIface (.untyped .nil))
      | "iota" =>
        match env.iota with
        | some i => pure (.int (.untyped .int) i)
        | none => stuck "iota outside a constant declaration"
      | _ =>
        -- A name of a dot-imported package the document does not hold.
        if let some p := foreignDotImport c env then unsupported s!"dot-import-{p}"
        stuck s!"undefined: {x}"

/-- A declared function as a value. -/
def funcVal (c : Ctx) : Nat → String → String → List Ty → GM Val
  | 0, _, _, _ => throw .fuel
  | n + 1, path, x, targs => do
    let p ← pkgCtx c path
    match p.objs.get? x with
    | some (.func fi tps sig _) =>
      let tenv ← bindTParams tps targs
      let t ← evalType c n { pkg := path, file := fi, tenv } (.funcType sig)
      pure (.func_ t (some (.decl path x targs)))
    | _ => stuck s!"{x} is not a function"

/-- A generic function instantiated, `f[T]` or `pkg.F[T]`, or `none` if `e` is not one. -/
def funcValue? (c : Ctx) : Nat → Env → Expr → GM (Option Val)
  | 0, _, _ => throw .fuel
  | n + 1, env, e => do
    match e with
    | .index (.ident f) args =>
      if (env.lookup f).isSome then return none
      let some path := nameOwner c env f | return none
      match (c.pkgs.get? path).bind (·.objs.get? f) with
      | some (.func ..) => return some (← funcVal c n path f (← args.mapM (evalType c n env)))
      | _ => return none
    | .index (.selector (.ident q) f) args =>
      if (env.lookup q).isSome then return none
      match importOf c env q with
      | some path =>
        match (c.pkgs.get? path).bind (·.objs.get? f) with
        | some (.func ..) => return some (← funcVal c n path f (← args.mapM (evalType c n env)))
        | _ => return none
      | none => return none
    | _ => return none

/-- `T{elts}` (Go spec, "Composite literals"): a struct, with keyed or positional
    elements, the rest of the fields zero. -/
def composite (c : Ctx) : Nat → Env → Ty → List Expr → GM Val
  | 0, _, _, _ => throw .fuel
  | n + 1, env, t, elts => do
    match ← underlying c n t with
    | .struct_ fs =>
      let mut vals ← fs.mapM fun (_, ft) => zero c n ft
      let mut i := 0
      for e in elts do
        match e with
        | .keyValue (.ident f) ve =>
          match fs.findIdx? (·.1 == f) with
          | some k =>
            let v ← evalExpr c n env ve
            vals := vals.set k (← convert c n fs[k]!.2 v)
          | none => stuck s!"no field {f}"
        | ve =>
          match fs[i]? with
          | some (_, ft) =>
            let v ← evalExpr c n env ve
            vals := vals.set i (← convert c n ft v)
          | none => stuck "too many values in a struct literal"
        i := i + 1
      pure (.struct_ t vals)
    | _ => unsupported s!"composite-{t}"

/-- `x.sel`: a name of an imported package, a field (through a pointer too) or a method
    value (Go spec, "Selectors", "Method values"). -/
def selector (c : Ctx) : Nat → Env → Expr → String → GM Val
  | 0, _, _, _ => throw .fuel
  | n + 1, env, x, sel => do
    if let .ident q := x then
      if (env.lookup q).isNone then
        if let some path := importOf c env q then
          if c.pkgs.contains path then return ← evalName c n env path sel
          else unsupported s!"{path}.{sel}"
    let (v, l) ← operand c n env x
    selectOn c n v l sel

/-- A method value `x.m`: the method with its receiver, kept in a cell of its own (Go
    spec, "Method values": the receiver is evaluated and saved). -/
def boundMethod (c : Ctx) : Nat → String → String → String → List Ty → Val → GM Val
  | 0, _, _, _, _, _ => throw .fuel
  | n + 1, p, tn, m, args, recv => do
    let cell ← alloc recv
    match methodOf c p tn m with
    | some me =>
      let tenv ← bindTParams (me.recvTParams.map fun x => .mk [x] (.ident "any")) args
      let t ← evalType c n { pkg := p, file := me.file, tenv } (.funcType me.sig)
      pure (.func_ t (some (.method p tn m args cell)))
    | none => stuck s!"no method {tn}.{m}"

/-- Field `sel` of `v`, through a pointer too (Go spec, "Selectors": `x.f` is `(*x).f`
    for a pointer `x`), with its index. A promoted field is unsupported. -/
def field? (c : Ctx) : Nat → Val → String → GM (Option (Val × Nat × Ty))
  | 0, _, _ => throw .fuel
  | n + 1, v, sel => do
    match v with
    | .struct_ t fs =>
      match ← underlying c n t with
      | .struct_ ftys =>
        match ftys.findIdx? (·.1 == sel) with
        | some k => return (fs[k]?).map fun f => (f, k, (ftys[k]?.map (·.2)).getD f.ty)
        | none => return none
      | _ => return none
    | .ptr _ (some l) =>
      let w ← load l
      if let .struct_ .. := w then field? c n w sel else return none
    | .ptr t none =>
      -- Selecting a field through a nil pointer panics; a method may still apply.
      match ← underlying c n t with
      | .ptr pt =>
        match ← underlying c n pt with
        | .struct_ ftys =>
          if ftys.any (·.1 == sel) then nilDeref
          else return none
        | _ => return none
      | _ => return none
    | _ => return none

/-- An operand: its value and, if it is addressable, where it is (Go spec, "Address
    operators": a variable, a pointer indirection, or a field selector of an
    addressable struct operand). -/
def operand (c : Ctx) : Nat → Env → Expr → GM (Val × Option Loc)
  | 0, _, _ => throw .fuel
  | n + 1, env, e => do
    match e with
    | .ident x =>
      if let some cell := env.lookup x then return (← loadCell cell, some ⟨cell, []⟩)
      let globals := (← get).globals
      if let some cell := (nameOwner c env x).bind fun p => globals.get? s!"{p}.{x}" then
        return (← loadCell cell, some ⟨cell, []⟩)
      return (← evalExpr c n env e, none)
    | .paren x => operand c n env x
    | .star _ =>
      let (l, _) ← evalAddr c n env e
      return (← load l, some l)
    | .selector x sel =>
      if let .ident q := x then
        if (env.lookup q).isNone then
          if let some path := importOf c env q then
            -- `pkg.V`: a package-level variable of another package is addressable.
            let globals := (← get).globals
            if let some cell := globals.get? s!"{path}.{sel}" then
              return (← loadCell cell, some ⟨cell, []⟩)
            return (← evalExpr c n env e, none)
      let (v, l) ← operand c n env x
      match v with
      | .ptr _ (some pl) =>
        match ← field? c n (← load pl) sel with
        | some (f, k, _) => return (f, some ⟨pl.cell, pl.path ++ [k]⟩)
        | none => return (← selectOn c n v l sel, none)
      | _ =>
        match ← field? c n v sel with
        | some (f, k, _) => return (f, l.map fun l => ⟨l.cell, l.path ++ [k]⟩)
        | none => return (← selectOn c n v l sel, none)
    | _ => return (← evalExpr c n env e, none)

/-- `.sel` of an operand's value `v` (at `l` if addressable): a field, or a method
    value (Go spec, "Selectors", "Method values"); a pointer method of an addressable
    value is `(&x).m` (Go spec, "Calls"). -/
def selectOn (c : Ctx) : Nat → Val → Option Loc → String → GM Val
  | 0, _, _, _ => throw .fuel
  | n + 1, v, l, sel => do
    -- A method of an interface value is its dynamic value's (Go spec, "Interface types").
    if let .boxed _ d := v then return ← selectOn c n d none sel
    if let .nilIface _ := v then return ← nilDeref
    match ← field? c n v sel with
    | some (w, _, _) => pure w
    | none =>
      match methodSet c v.ty sel, l with
      | some (p, tn, args, me), _ =>
        -- A value method through a pointer is `(*p).m`, the pointee evaluated and
        -- saved now (Go spec, "Method values").
        let recv ← match v, me.ptr with
          | .ptr _ (some pl), false => load pl
          | .ptr _ none, false => nilDeref
          | _, _ => pure v
        boundMethod c n p tn sel args recv
      | none, some l =>
        match methodSet c (.ptr v.ty) sel with
        | some (p, tn, args, _) => boundMethod c n p tn sel args (.ptr (.ptr v.ty) (some l))
        | none => unsupported "promoted-selector"
      -- Compiled Go selects only fields and methods that exist, so the rest are
      -- members promoted from an embedded field, which GoCore does not model.
      | none, none => unsupported "promoted-selector"

/-- Where an addressable expression is, and its type (Go spec, "Address operators"). -/
def evalAddr (c : Ctx) : Nat → Env → Expr → GM (Loc × Ty)
  | 0, _, _ => throw .fuel
  | n + 1, env, e => do
    match e with
    | .ident x =>
      match env.lookup x with
      | some cell => return (⟨cell, []⟩, ← cellType cell)
      | none =>
        let globals := (← get).globals
        match (nameOwner c env x).bind fun p => globals.get? s!"{p}.{x}" with
        | some cell => return (⟨cell, []⟩, ← cellType cell)
        | none => stuck s!"{x} is not addressable"
    | .paren x => evalAddr c n env x
    | .star x =>
      match ← evalExpr c n env x with
      | .ptr (.ptr elem) (some l) => return (l, elem)
      | .ptr _ (some l) => return (l, (← load l).ty)
      | .ptr _ none => nilDeref
      | _ => stuck "* of a non-pointer"
    | .selector x sel =>
      if let .ident q := x then
        if (env.lookup q).isNone then
          if let some path := importOf c env q then
            let globals := (← get).globals
            match globals.get? s!"{path}.{sel}" with
            | some cell => return (⟨cell, []⟩, ← cellType cell)
            | none =>
              if c.pkgs.contains path then stuck s!"{q}.{sel} is not addressable"
              else unsupported s!"{path}.{sel}"
      let (v, l) ← operand c n env x
      match v with
      | .ptr _ (some pl) =>
        match ← field? c n (← load pl) sel with
        | some (_, k, ft) => return (⟨pl.cell, pl.path ++ [k]⟩, ft)
        | none => stuck s!"{sel} is not addressable"
      | .ptr _ none => nilDeref
      | _ =>
        match ← field? c n v sel, l with
        | some (_, k, ft), some l => return (⟨l.cell, l.path ++ [k]⟩, ft)
        | _, _ => stuck s!"{sel} is not addressable"
    | _ => stuck "not addressable"

/-- `x.(T)` (Go spec, "Type assertions"): the value if its dynamic type is `T`, or
    implements the interface `T`; `none` if not. -/
def assertType (c : Ctx) : Nat → Val → Ty → GM (Option Val)
  | 0, _, _ => throw .fuel
  | n + 1, v, t => do
    match v with
    | .nilIface _ => return none
    | _ =>
      let d := v.dyn
      match ← underlying c n t with
      | .iface ms =>
        return if ms.all (fun m => (methodSet c d.ty m).isSome) then some (.boxed t d) else none
      | _ => return if d.ty == t then some d else none

/-- A unary operator (Go spec, "Arithmetic operators", "Logical operators"). -/
def unary (c : Ctx) : Nat → String → Val → GM Val
  | 0, _, _ => throw .fuel
  | n + 1, op, v => do
    match op, v with
    | "+", .int _ _ => pure v
    | "-", .int (.untyped k) x => pure (.int (.untyped k) (-x))
    | "-", .int t x => do
      let .int k ← underlying c n t | stuck "- of a non-integer"
      pure (.int t (k.wrap (-x)))
    | "^", .int (.untyped k) x => pure (.int (.untyped k) (bitNot x))
    | "^", .int t x => do
      let .int k ← underlying c n t | stuck "^ of a non-integer"
      pure (.int t (k.wrap (bitNot x)))
    | "!", .bool t b => pure (.bool t !b)
    | "<-", _ => unsupported "receive"
    | _, _ => stuck s!"unary {op} on {v.ty}"

/-- A binary operator other than `&&` and `||` (Go spec, "Operators"). An untyped
    operand is first converted to the other's type. -/
def binary (c : Ctx) : Nat → String → Val → Val → GM Val
  | 0, _, _, _ => throw .fuel
  | n + 1, op, a, b => do
    if op == "<<" || op == ">>" then
      -- The left operand's type is the result's; the count is any integer.
      let s ← asInt b
      match a, b.ty with
      | .int (.untyped k) x, .untyped _ => return .int (.untyped k) (← shiftOp op none x s)
      -- An untyped constant shifted by a non-constant count first takes the type its
      -- context gives it (Go spec, "Operators"), which GoCore does not track; and a
      -- typed constant count cannot be told from a variable here.
      | .int (.untyped _) _, _ => unsupported "untyped-shift"
      | _, _ => pure ()
      match a with
      | .int t x =>
        let .int k ← underlying c n t | stuck "shift of a non-integer"
        return .int t (← shiftOp op (some k) x s)
      | _ => stuck "shift of a non-integer"
    -- Bring an untyped operand to the other's type (Go spec, "Operators").
    let (a, b) ← match a.ty, b.ty with
      | .untyped _, .untyped _ => pure (a, b)
      | .untyped _, t => pure (← convert c n t a, b)
      | t, .untyped _ => pure (a, ← convert c n t b)
      | _, _ => pure (a, b)
    if op == "==" || op == "!=" then
      let eq ← equal c n a b
      return .bool (.untyped .bool) (if op == "==" then eq else !eq)
    match a, b with
    | .int t x, .int _ y =>
      if [ "<", "<=", ">", ">=" ].contains op then
        return .bool (.untyped .bool) (← cmpOp op (compare x y))
      match t with
      | .untyped k =>
        -- An untyped rune with an untyped int is an untyped rune (Go spec,
        -- "Constant expressions").
        let k' := if b.ty == .untyped .rune then UKind.rune else k
        return .int (.untyped k') (← intOp op none x y)
      | _ =>
        let .int k ← underlying c n t | stuck "arithmetic on a non-integer"
        return .int t (← intOp op (some k) x y)
    | .str t x, .str _ y =>
      if op == "+" then return .str t (x ++ y)
      return .bool (.untyped .bool) (← cmpOp op (compare x y))
    | _, _ => stuck s!"{op} on {a.ty} and {b.ty}"

/-- `a == b` (Go spec, "Comparison operators"): integers, booleans and strings by
    value; structs field by field; pointers by what they point to; a function only with
    `nil`; an interface value against another value by dynamic type and value. -/
def equal (c : Ctx) : Nat → Val → Val → GM Bool
  | 0, _, _ => throw .fuel
  | n + 1, a, b => do
    -- Interface values compare by their dynamic values (Go spec, "Comparison
    -- operators"); a nil interface, typed by `binary`'s conversion of `nil`, equals
    -- only another nil interface, so a nil pointer inside one is not nil.
    let (a, b) := (a.dyn, b.dyn)
    -- Values of different dynamic types are unequal: by the precondition (see the
    -- module doc), only two interface operands can differ in type.
    let same := a.ty == b.ty || (match a.ty, b.ty with
      | .untyped _, _ | _, .untyped _ => true
      | _, _ => false)
    match a, b with
    | .ptr _ l, .nilIface (.untyped .nil) | .nilIface (.untyped .nil), .ptr _ l => pure l.isNone
    | .func_ _ f, .nilIface (.untyped .nil) | .nilIface (.untyped .nil), .func_ _ f => pure f.isNone
    | .nilIface _, .nilIface _ => pure true
    | .nilIface _, _ | _, .nilIface _ => pure false
    -- Go compiles `==` on functions only against nil, which `binary` has converted to
    -- the function's type. Two non-nil functions meet here only as the dynamic values
    -- of interface operands, where Go panics at run time (not modelled).
    | .func_ _ f, .func_ _ g =>
      if f.isNone || g.isNone then pure (f.isNone && g.isNone) else unsupported "uncomparable"
    | _, _ =>
      if !same then return false
      match a, b with
      | .int _ x, .int _ y => pure (x == y)
      | .bool _ x, .bool _ y => pure (x == y)
      | .str _ x, .str _ y => pure (x == y)
      | .ptr _ l, .ptr _ m => pure (l == m)
      | .struct_ _ xs, .struct_ _ ys =>
        let rec go : List Val → List Val → GM Bool
          -- A function field makes the struct type incomparable: Go rejects the
          -- comparison, or through interfaces panics at run time; not modelled.
          | .func_ .. :: _, _ :: _ => unsupported "uncomparable"
          | x :: xs, y :: ys => do if ← equal c n x y then go xs ys else pure false
          | _, _ => pure true
        go xs ys
      | _, _ => pure false

-- Calls

/-- A call `fn(args)`, with its results: a conversion, a builtin, a model of a library
    function, a method call or a call of a function value (Go spec, "Calls",
    "Conversions", "Built-in functions"). The function value and the arguments are
    evaluated left to right. -/
def evalCall (c : Ctx) : Nat → Env → Expr → List Expr → GM (List Val)
  | 0, _, _, _ => throw .fuel
  | n + 1, env, fn, args => do
    -- A conversion `T(x)`.
    if let some t ← typeOf? c n env fn then
      let [a] := args | stuck "a conversion takes one argument"
      return [← conversion c n t (← evalExpr c n env a)]
    match fn with
    | .ident b =>
      if (env.lookup b).isNone && (nameOwner c env b).isNone then
        return ← builtin c n env b args
    | .selector (.ident q) f =>
      if (env.lookup q).isNone then
        if let some path := importOf c env q then
          if path == "fmt" && fmtFuncs.contains f then
            return ← fmtCall c n f (← evalArgs c n env args)
          if !c.pkgs.contains path then unsupported s!"{path}.{f}"
    | _ => pure ()
    -- A declared function called by name: a generic one gets its type arguments by
    -- inference from the arguments (Go spec, "Type inference").
    let declared : Option (String × String) := match fn with
      | .ident f => if (env.lookup f).isSome then none else (nameOwner c env f).map (·, f)
      | .selector (.ident q) f => if (env.lookup q).isSome then none else (importOf c env q).map (·, f)
      | _ => none
    if let some (path, f) := declared then
      if let some (.func ..) := (c.pkgs.get? path).bind (·.objs.get? f) then
        let vs ← evalArgs c n env args
        return ← callValue c n (.func_ (.func_ [] [] false) (some (.decl path f []))) vs
    let fv ← evalExpr c n env fn
    let vs ← evalArgs c n env args
    callValue c n fv vs

/-- Arguments, left to right; `f(g())` passes `g`'s results (Go spec, "Calls"). -/
def evalArgs (c : Ctx) : Nat → Env → List Expr → GM (List Val)
  | 0, _, _ => throw .fuel
  | n + 1, env, args => do
    match args with
    | [a] => pure (← evalExpr c n env a).results
    | _ => args.mapM (evalExpr c n env)

/-- The builtins the transpiler emits (Go spec, "Built-in functions", "Handling
    panics"). -/
def builtin (c : Ctx) : Nat → Env → String → List Expr → GM (List Val)
  | 0, _, _, _ => throw .fuel
  | n + 1, env, b, args => do
    match b, args with
    | "panic", [a] =>
      let v ← evalExpr c n env a
      throw (.panic v (← panicText c n v))
    | "new", [te] =>
      let t ← evalType c n env te
      let cell ← allocAs t (← zero c n t)
      pure [.ptr (.ptr t) (some ⟨cell, []⟩)]
    | "len", [a] =>
      match ← evalExpr c n env a with
      | .str _ s => pure [.int (.int .int) s.utf8ByteSize]
      | _ => unsupported "len"
    | "print", _ | "println", _ =>
      -- They write to standard error, which the comparison leaves out (Go spec,
      -- "Bootstrapping").
      let _ ← args.mapM (evalExpr c n env)
      pure []
    | b, _ => unsupported s!"builtin-{b}"

/-- The text Go's runtime prints after `panic: ` for a panic value: a string as it is,
    an error's `Error()`, a Stringer's `String()` (runtime `printpanicval`). -/
def panicText (c : Ctx) : Nat → Val → GM String
  | 0, _ => throw .fuel
  | n + 1, v => do
    let v := v.dyn
    match v with
    | .str (.untyped _) s | .str .string s => pure s
    | _ =>
      if let some r ← callMethod? c n v "Error" then return ← asStr r
      if let some r ← callMethod? c n v "String" then return ← asStr r
      match v with
      | .int (.int .int) x => pure (toString x)
      | .int (.untyped _) x => pure (toString x)
      | _ => unsupported "panic-value"

/-- Call method `m` of `v`'s method set with no arguments, if it has one. -/
def callMethod? (c : Ctx) : Nat → Val → String → GM (Option Val)
  | 0, _, _ => throw .fuel
  | n + 1, v, m => do
    let v := v.dyn
    match methodSet c v.ty m with
    | some (p, _, args, me) =>
      match me.sig with
      | .mk [] [_] _ => return some (← Val.single (← callMethod c n p args me v []))
      | _ => return none
    | none => return none

/-- Call a function value with argument values. -/
def callValue (c : Ctx) : Nat → Val → List Val → GM (List Val)
  | 0, _, _ => throw .fuel
  | n + 1, fv, vs => do
    match fv with
    | .func_ _ none => nilDeref
    | .func_ _ (some (.decl path x targs)) =>
      -- A function a model replaces runs as the model, however it is reached:
      -- qualified, from inside its own package, instantiated or as a value.
      if path == stdPath && libFuncs.contains x then return ← libCall c n x vs
      let p ← pkgCtx c path
      match p.objs.get? x with
      | some (.func fi tps sig body) =>
        let targs ← if tps.isEmpty || !targs.isEmpty then pure targs else inferTArgs tps sig vs
        let tenv ← bindTParams tps targs
        callBody c n { pkg := path, file := fi, tenv } sig body none vs
      | _ => stuck s!"{x} is not a function"
    | .func_ _ (some (.method p tn m args cell)) =>
      match methodOf c p tn m with
      | some me => callMethod c n p args me (← loadCell cell) vs
      | none => stuck s!"no method {tn}.{m}"
    | .func_ _ (some (.closure id)) =>
      match (← get).closures[id]? with
      | some cl =>
        callBody c n { pkg := cl.pkg, file := cl.file, scopes := cl.scopes, tenv := cl.tenv }
          cl.sig (some cl.body) none vs
      | none => stuck "no closure"
    | _ => stuck "call of a non-function"

/-- Call method `m` of type `p.tn[args]` with receiver `recv` (Go spec, "Method
    declarations"): a pointer receiver gets the pointer, a value receiver a copy of
    the value (through a pointer too, Go spec, "Calls"). -/
def callMethod (c : Ctx) : Nat → String → List Ty → Method → Val → List Val → GM (List Val)
  | 0, _, _, _, _, _ => throw .fuel
  | n + 1, p, args, me, recv, vs => do
    let recv ← if me.ptr then pure recv else
      match recv with
      | .ptr _ (some l) => load l
      | .ptr _ none => nilDeref
      | v => pure v
    let tenv := me.recvTParams.zip args
    callBody c n { pkg := p, file := me.file, tenv } me.sig me.body (some (me.recvName, recv)) vs

/-- Type arguments of a generic function from its arguments' types (Go spec, "Type
    inference"); an untyped constant argument has its default type. -/
def inferTArgs (tps : List Field) (sig : Sig) (vs : List Val) : GM (List Ty) := do
  let names := fieldListNames tps
  let .mk ps _ _ := sig
  let ptys := fieldTypes ps
  let eqs := (ptys.zip vs).flatMap fun (t, v) => unify names t (defaultVal v).ty
  names.mapM fun x => match eqs.lookup x with
    | some t => pure t
    | none => unsupported "type-inference"

/-- Run a function body: parameters (and the receiver) in new cells, converted to
    their types; named results in cells holding zero values; the results of its
    `return` (Go spec, "Function declarations", "Return statements"). -/
def callBody (c : Ctx) : Nat → Env → Sig → Option (List Stmt) → Option (String × Val) → List Val → GM (List Val)
  | 0, _, _, _, _, _ => throw .fuel
  | n + 1, env, .mk ps rs variadic, body, recv, vs => do
    tick
    if variadic then unsupported "variadic"
    let some body := body | unsupported "func-without-body"
    let mut env := env.push
    if let some (r, v) := recv then
      env := env.bind r (← alloc v)
    let params := ps.flatMap fun | .mk ns t => if ns.isEmpty then [("_", t)] else ns.map (·, t)
    if params.length != vs.length then stuck "wrong number of arguments"
    for ((x, te), v) in params.zip vs do
      let t ← evalType c n env te
      env := env.bind x (← allocAs t (← convert c n t v))
    let results := rs.flatMap fun | .mk ns t => if ns.isEmpty then [("", t)] else ns.map (·, t)
    let rtys ← results.mapM fun (_, te) => evalType c n env te
    let mut named : List Nat := []
    for ((x, _), t) in results.zip rtys do
      if !x.isEmpty then
        let cell ← allocAs t (← zero c n t)
        env := env.bind x cell
        named := named ++ [cell]
    let r ← tryCatch (do let _ ← execStmts c n env body; pure none) fun
      | .ret ws => pure (some ws)
      | e => throw e
    match r with
    | none =>
      if results.isEmpty then pure []
      else if named.isEmpty then stuck "missing return"
      else named.mapM loadCell
    | some [] =>
      if results.isEmpty then pure [] else named.mapM loadCell
    | some ws =>
      let ws := if ws.length == 1 && rtys.length > 1 then (ws.head!).results else ws
      if ws.length != rtys.length then stuck "wrong number of results"
      (rtys.zip ws).mapM fun (t, w) => convert c n t w

-- Statements

/-- Execute statements in order; declarations extend the scope for the statements
    after them (Go spec, "Blocks", "Declarations and scope"). -/
def execStmts (c : Ctx) : Nat → Env → List Stmt → GM Env
  | 0, _, _ => throw .fuel
  | n + 1, env, ss => do
    let mut env := env
    for s in ss do
      env ← execStmt c n env s
    pure env

/-- A block: its statements in a scope of their own. -/
def execBlock (c : Ctx) : Nat → Env → List Stmt → GM Unit
  | 0, _, _ => throw .fuel
  | n + 1, env, ss => do let _ ← execStmts c n env.push ss

/-- Execute a statement (Go spec, "Statements"). -/
def execStmt (c : Ctx) : Nat → Env → Stmt → GM Env
  | 0, _, _ => throw .fuel
  | n + 1, env, s => do
    match s with
    | .empty => pure env
    | .block ss => do execBlock c n env ss; pure env
    | .expr e => do let _ ← evalExpr c n env e; pure env
    | .decl (.value false specs) =>
      let mut env := env
      for spec in specs do
        let .mk names te values := spec
        let vs ← if values.isEmpty then pure [] else evalArgs c n env values
        let t? ← te.mapM (evalType c n env)
        for (x, i) in names.zipIdx do
          let v ← declValue c n t? vs[i]?
          env := env.bind x (← allocAs (t?.getD v.ty) v)
      pure env
    | .decl (.value true _) => unsupported "local-const"
    | .decl (.type _) => unsupported "local-type"
    | .decl (.func ..) | .decl (.unsupported _) => unsupported "local-decl"
    | .incDec op x => do
      let (l, _) ← evalAddr c n env x
      let v ← load l
      store l (← binary c n (if op == "++" then "+" else "-") v (.int (.untyped .int) 1))
      pure env
    | .assign tok lhs rhs => assign c n env tok lhs rhs
    | .ret es =>
      let vs ← evalArgs c n env es
      throw (.ret (if es.isEmpty then [] else vs))
    | .branch "break" => throw .brk
    | .branch "continue" => throw .cont
    | .branch t => unsupported t
    | .if_ init cond body els => do
      let env' ← execInit c n env init
      if ← asBool (← evalExpr c n env' cond) then execBlock c n env' body
      else match els with
        | some e => let _ ← execStmt c n env' e
        | none => pure ()
      pure env
    | .switch init tag clauses => do
      let env' ← execInit c n env init
      let tv ← match tag with
        | some t => pure (defaultVal (← evalExpr c n env' t))
        | none => pure (.bool .bool true)
      tryCatch (switchClauses c n env' tv clauses) fun
        | .brk => pure ()
        | e => throw e
      pure env
    | .for_ init cond post body => do
      let env' ← execInit c n env init
      forLoop c n env' cond post body
      pure env
    | .typeSwitch init bind x clauses => do
      let env' ← execInit c n env init
      let v ← evalExpr c n env' x
      tryCatch (typeClauses c n env' bind v clauses) fun
        | .brk => pure ()
        | e => throw e
      pure env
    | .range .. => unsupported "range"
    | .unsupported k => unsupported k

/-- The value a declared variable starts with: the initializer's value converted to the
    declared type, the value with its default type, or the declared type's zero value
    (Go spec, "Variable declarations"). Package-level variables start the same way. -/
def declValue (c : Ctx) : Nat → Option Ty → Option Val → GM Val
  | 0, _, _ => throw .fuel
  | n + 1, t?, v? => do
    match v?, t? with
    | some v, some t => convert c n t v
    | some v, none => pure (defaultVal v)
    | none, some t => zero c n t
    | none, none => stuck "var without type or value"

/-- An `if`, `switch` or `for` statement's init statement, in a scope of its own that
    the rest of the statement sees (Go spec, "Blocks": each such statement is in its own
    implicit block). -/
def execInit (c : Ctx) : Nat → Env → Option Stmt → GM Env
  | 0, _, _ => throw .fuel
  | n + 1, env, init => do
    match init with
    | some i => execStmt c n env.push i
    | none => pure env.push

/-- The clauses of an expression switch: the first whose case satisfies `tag == case`,
    else `default` (Go spec, "Expression switches"). -/
def switchClauses (c : Ctx) : Nat → Env → Val → List Clause → GM Unit
  | 0, _, _, _ => throw .fuel
  | n + 1, env, tv, clauses => do
    for clause in clauses do
      let .mk list _ body := clause
      for e in list do
        if ← asBool (← binary c n "==" tv (← evalExpr c n env e)) then
          execBlock c n env body
          return
    for clause in clauses do
      let .mk _ isDefault body := clause
      if isDefault then
        execBlock c n env body
        return

/-- The clauses of a type switch on `v` (Go spec, "Type switches"): the first that
    lists `nil` for a nil interface value or a type `v` has (or implements), else
    `default`. Its body sees the bound variable holding `v`: of the listed type when the
    clause lists one, else of the guard's interface type, which GoCore's values, carrying
    their dynamic type, do not tell apart. -/
def typeClauses (c : Ctx) : Nat → Env → Option String → Val → List Clause → GM Unit
  | 0, _, _, _, _ => throw .fuel
  | n + 1, env, bind, v, clauses => do
    -- The clause's variable holds `w`, of declared type `t`.
    let run := fun (body : List Stmt) (t : Ty) (w : Val) => do
      let mut env := env.push
      if let some x := bind then env := env.bind x (← allocAs t w)
      execBlock c n env body
    for clause in clauses do
      let .mk list _ body := clause
      for te in list do
        let hit ← match te, v with
          | .ident "nil", .nilIface _ => pure (if (env.lookup "nil").isNone then some v else none)
          | .ident "nil", _ => pure none
          | te, _ => do assertType c n v (← evalType c n env te)
        if let some w := hit then
          -- With one type listed, the variable has that type and the asserted value;
          -- otherwise the guard's interface type and value.
          if let [_] := list then run body w.ty w else run body v.ty v
          return
    for clause in clauses do
      let .mk _ isDefault body := clause
      if isDefault then
        run body v.ty v
        return

/-- `for init; cond; post { body }` (Go spec, "For statements with for clause"). Each
    iteration has its own copy of the variables `init` declares: before `post`, they
    are copied into fresh cells (Go 1.22's per-iteration loop variables). `break` ends
    the loop, `continue` the iteration. -/
def forLoop (c : Ctx) : Nat → Env → Option Expr → Option Stmt → List Stmt → GM Unit
  | 0, _, _, _, _ => throw .fuel
  | n + 1, env, cond, post, body => do
    let budget := (← get).steps + 1
    -- The innermost scope is the init statement's: the loop variables.
    loopM budget env fun env => do
      tick
      if let some cnd := cond then
        if !(← asBool (← evalExpr c n env cnd)) then return none
      let r ← tryCatch (do execBlock c n env body; pure true) fun
        | .brk => pure false
        | .cont => pure true
        | e => throw e
      if !r then return none
      -- Fresh copies of the loop variables for the next iteration, in place of the
      -- old ones.
      let fresh ← (env.scopes.headD []).mapM fun (x, cell) => do
        pure (x, ← allocAs (← cellType cell) (← loadCell cell))
      let env := { env with scopes := fresh :: env.scopes.tail }
      if let some p := post then
        let _ ← execStmt c n env p
      return some env

/-- An assignment (Go spec, "Assignment statements", "Short variable declarations"):
    the operands of the left side's indirections and the right side are evaluated
    left to right, then the assignments are carried out left to right. -/
def assign (c : Ctx) : Nat → Env → String → List Expr → List Expr → GM Env
  | 0, _, _, _, _ => throw .fuel
  | n + 1, env, tok, lhs, rhs => do
    if tok == ":=" then
      let vs ← rhsValues c n env lhs.length rhs
      let mut env := env
      let current := env.scopes.headD []
      for (x, v) in lhs.zip vs do
        match x with
        | .ident "_" => pure ()
        | .ident y =>
          match current.lookup y with
          | some cell => storeCell cell (← convert c n (← cellType cell) v)
          | none => env := env.bind y (← alloc (defaultVal v))
        | _ => stuck ":= of a non-name"
      return env
    if tok == "=" then
      let targets ← lhs.mapM fun x => match x with
        | .ident "_" => pure none
        | x => some <$> evalAddr c n env x
      let vs ← rhsValues c n env lhs.length rhs
      for (t, v) in targets.zip vs do
        if let some (l, ty) := t then
          store l (← convert c n ty v)
      return env
    -- `x op= y`: `x` is evaluated once.
    let op := String.ofList tok.toList.dropLast
    let [x] := lhs | stuck s!"{tok} with several targets"
    let [y] := rhs | stuck s!"{tok} with several values"
    let (l, ty) ← evalAddr c n env x
    let a ← load l
    let b ← evalExpr c n env y
    store l (← convert c n ty (← binary c n op a b))
    return env

/-- The values of an assignment's right side, `k` of them: one per expression, a
    call's several results, or `v, ok := x.(T)` (Go spec, "Assignment statements"). -/
def rhsValues (c : Ctx) : Nat → Env → Nat → List Expr → GM (List Val)
  | 0, _, _, _ => throw .fuel
  | n + 1, env, k, rhs => do
    match rhs with
    | [.typeAssert x te] =>
      if k == 2 then
        let v ← evalExpr c n env x
        let t ← evalType c n env te
        match ← assertType c n v t with
        | some w => return [w, .bool (.untyped .bool) true]
        | none => return [← zero c n t, .bool (.untyped .bool) false]
      else return [← evalExpr c n env rhs.head!]
    | [e] =>
      let v ← evalExpr c n env e
      let vs := v.results
      if vs.length != k then stuck "assignment count mismatch"
      return vs
    | es => es.mapM (evalExpr c n env)

-- Library models

/-- `fmt` (fmt package documentation; `fmt/print.go`). `Println` prints its operands
    with `%v`, always separated by spaces, and a newline; `Print` puts a space between
    two operands when neither is a string; `Sprint` is `Print` into a string; `Sprintf`
    with the verbs `%v`, `%d`, `%s` and `%%`, no flags, width or precision. -/
def fmtCall (c : Ctx) : Nat → String → List Val → GM (List Val)
  | 0, _, _ => throw .fuel
  | n + 1, f, vs => do
    match f with
    | "Println" =>
      let parts ← vs.mapM fun v => fmtArg c n v 'v'
      emit (" ".intercalate parts ++ "\n")
      pure []
    | "Print" => emit (← sprint c n vs); pure []
    | "Sprint" => pure [.str .string (← sprint c n vs)]
    | "Sprintf" =>
      let fmtStr :: args := vs | stuck "Sprintf without a format"
      pure [.str .string (← sprintf c n (← asStr fmtStr).toList args)]
    | _ => unsupported s!"fmt.{f}"

/-- `Print`'s text: a space between operands when neither is a string (`fmt/print.go`,
    `doPrint`). -/
def sprint (c : Ctx) : Nat → List Val → GM String
  | 0, _ => throw .fuel
  | n + 1, vs => do
    let mut out := ""
    let mut prevString := false
    let mut first := true
    for v in vs do
      let isString ← (do match v with
        | .str t _ => pure ((← underlying c n t) == .string || t == .untyped .string)
        | _ => pure false)
      if !first && !isString && !prevString then out := out ++ " "
      out := out ++ (← fmtArg c n v 'v')
      prevString := isString
      first := false
    pure out

/-- `Sprintf`'s text (`fmt/print.go`, `doPrintf`). -/
def sprintf (c : Ctx) : Nat → List Char → List Val → GM String
  | 0, _, _ => throw .fuel
  | n + 1, fmtStr, args => do
    match fmtStr with
    | [] => if args.isEmpty then pure "" else unsupported "fmt-extra-args"
    | '%' :: '%' :: rest => pure ("%" ++ (← sprintf c n rest args))
    | '%' :: verb :: rest =>
      if !['v', 'd', 's', 't'].contains verb then unsupported s!"fmt-verb-{verb}"
      let a :: args := args | unsupported "fmt-missing-arg"
      pure ((← fmtArg c n a verb) ++ (← sprintf c n rest args))
    | ch :: rest => pure (String.singleton ch ++ (← sprintf c n rest args))

/-- One operand printed with `verb` (`fmt/print.go`, `printArg`). -/
def fmtArg (c : Ctx) : Nat → Val → Char → GM String
  | 0, _, _ => throw .fuel
  | n + 1, v, verb => do
    match v with
    | .nilIface _ => if verb == 'v' then pure "<nil>" else unsupported "fmt-nil-verb"
    | _ => fmtValue c n v verb 0 true

/-- A value printed with `verb` at `depth` (`fmt/print.go`, `handleMethods` and
    `printValue`). `canIface` is false below an unexported field, where `fmt` does not
    call methods (reflect's `CanInterface`). -/
def fmtValue (c : Ctx) : Nat → Val → Char → Nat → Bool → GM String
  | 0, _, _, _, _ => throw .fuel
  | n + 1, v, verb, depth, canIface => do
    if canIface then
      -- A Formatter: the one the standard library declares is `Immutable.Format`,
      -- which prints the wrapped value with the same verb in a fresh printer
      -- (std/types.go, `Immutable.Format`, lines 97-101).
      if let some (p, tn, _, _) := methodSet c v.ty "Format" then
        if p == stdPath && tn == "Immutable" then
          let .struct_ _ [inner] := v | stuck "Immutable without its value"
          return ← fmtArg c n inner verb
        unsupported "fmt-formatter"
      if ['v', 's'].contains verb then
        -- `catchPanic`: a nil pointer whose method panics prints `<nil>`; any other
        -- panic in the method prints a `%!v(PANIC=...)` text, which is not modelled.
        let guarded := fun (m : String) =>
          tryCatch (callMethod? c n v m) fun
            | .panic .. => match v with
              | .ptr _ none => pure (some (.str .string "<nil>"))
              | _ => unsupported "fmt-method-panic"
            | e => throw e
        if let some r ← guarded "Error" then return ← asStr r
        if let some r ← guarded "String" then return ← asStr r
    match v with
    | .int _ x =>
      if ['v', 'd'].contains verb then pure (toString x) else unsupported s!"fmt-verb-{verb}-int"
    | .bool _ b => if verb == 'v' || verb == 't' then pure (if b then "true" else "false") else unsupported "fmt-verb-bool"
    | .str _ s => if ['v', 's'].contains verb then pure s else unsupported "fmt-verb-string"
    | .struct_ t fs =>
      if verb != 'v' then unsupported "fmt-verb-struct"
      let .struct_ ftys ← underlying c n t | stuck "struct value of a non-struct type"
      let parts ← (fs.zip ftys).mapM fun (f, (name, _)) =>
        fmtValue c n f 'v' (depth + 1) (canIface && exported name)
      pure ("{" ++ " ".intercalate parts ++ "}")
    | .ptr _ none => pure "<nil>"
    | .ptr _ (some l) =>
      let w ← load l
      if depth == 0 then
        if let .struct_ .. := w then
          return "&" ++ (← fmtValue c n w verb (depth + 1) canIface)
      unsupported "print-pointer"
    | .nilIface _ => pure "<nil>"
    | .func_ .. => unsupported "print-pointer"
    | .tuple _ => stuck "printing several values"
    | .poison w => unsupported w
    -- An interface value prints its dynamic value (`printValue`'s `reflect.Interface`);
    -- its interface type has no methods of its own, so none were called above.
    | .boxed _ d => fmtValue c n d verb depth canIface

/-- Models of the standard library's hand-written functions (std/types.go), which use
    reflection and are outside GoCore. Each follows the code it names, by line range at
    the time of writing; `libFuncs` lists the functions they replace by name, and with
    `Immutable.Format` (in `fmtValue`) they are the whole list. They are trusted, like
    the `fmt` model, and the differential run against `go run` is what tests them. -/
def libCall (c : Ctx) : Nat → String → List Val → GM (List Val)
  | 0, _, _ => throw .fuel
  | n + 1, f, vs => do
    match f, vs with
    -- `EvalOperand[T](v T) T { return v }` (std/types.go, line 114).
    | "EvalOperand", [v] => pure [v]
    -- `FormatStruct(name, fields...)` (std/types.go, lines 559-572): `Name(f1, f2, ...)`, each field unwrapped from
    -- its `Immutable` and printed by `formatField`.
    | "FormatStruct", name :: fields =>
      let parts ← fields.mapM fun f => formatField c n (unwrapImmutable f)
      pure [.str .string ((← asStr name) ++ "(" ++ ", ".intercalate parts ++ ")")]
    -- `Equal[T](a, b T)` (std/types.go, lines 205-217 and 259-270): a predeclared primitive type compares with `==`; a type with
    -- `Equal(T) bool` uses it (`equalPrimitive`, `Equatable[T]`); the reflective rest is
    -- unsupported.
    | "Equal", [a, b] =>
      match a.ty with
      | .int _ | .bool | .string => pure [.bool .bool (← equal c n a b)]
      | _ =>
        match methodSet c a.ty "Equal" with
        | some (p, _, args, me) => callMethod c n p args me a [b]
        | none => unsupported "std.Equal-reflective"
    -- `Copy[T](v T) T` (std/types.go, lines 122-145 and 200-203): a nil pointer is returned as it is, a value with its own
    -- `Copy() T` is copied by it; the reflective rest is unsupported.
    | "Copy", [v] =>
      match v with
      | .ptr _ none | .nilIface _ => pure [v]
      | _ =>
        match methodSet c v.ty "Copy" with
        | some (p, _, args, me) =>
          match v, me.ptr with
          | .ptr _ _, false => pure [v]  -- copiesPointee: the pointee's Copy, returned as is
          | _, _ => callMethod c n p args me v []
        | none =>
          match v with
          | .int (.int _) _ | .bool .bool _ | .str .string _ => pure [v]
          | _ => unsupported "std.Copy-reflective"
    | _, _ => unsupported s!"std.{f}"

/-- `formatField` and `printed` (std/types.go, lines 574-610 and 706-711): a function prints `<fn>`; a non-nil pointer to a
    value that prints itself or to a struct prints as `%v` of the pointer, `<cycle>`
    when this printing already passes through it; another pointer prints its pointee;
    anything else as `%v`. -/
def formatField (c : Ctx) : Nat → Val → GM String
  | 0, _ => throw .fuel
  | n + 1, v => do
    match v with
    | .func_ .. => pure "<fn>"
    | .ptr _ (some l) =>
      let w ← load l
      let printsItself := (methodSet c v.ty "String").isSome || (methodSet c v.ty "Format").isSome ||
        (methodSet c v.ty "Error").isSome
      let isStruct := match w with | .struct_ .. => true | _ => false
      if !printsItself && !isStruct then return ← fmtArg c n w 'v'
      if (← get).printing.contains l then return "<cycle>"
      modify fun s => { s with printing := l :: s.printing }
      let r ← fmtArg c n v 'v'
      modify fun s => { s with printing := s.printing.erase l }
      pure r
    | _ =>
      -- `printed`: a value whose `String` is declared on a pointer receiver only.
      if (methodSet c v.ty "String").isNone && (methodSet c (.ptr v.ty) "String").isSome then
        unsupported "pointer-string"
      fmtArg c n v 'v'

end

end Gala.GoCore.Full
