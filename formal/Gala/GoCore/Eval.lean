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
import Gala.GoCore.GcRuntime

namespace Gala.GoCore.Full

open Std (HashMap)

/-- Where code runs: its package and file (for imports), the local scopes (newest
    first; each binds names to cells), the type parameters and local types in scope, the
    type arguments of the generic function or method running (`targs`), and `iota` in a
    constant declaration. -/
structure Env where
  pkg : String
  file : Nat
  scopes : List (List (String × Nat)) := [[]]
  tenv : List (String × Ty) := []
  targs : List Ty := []
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

/-- The predeclared interface `error` (Go spec, "Errors"). -/
def errorTy : Ty := .iface [("Error", .func_ [] [.string] false)]

/-- `fmt.Stringer`'s underlying type (fmt/print.go). -/
def stringerTy : Ty := .iface [("String", .func_ [] [.string] false)]

/-- The underlying types of the defined types of Go's standard library that GoCore
    knows without their source, by import path and name: `fmt.Stringer`
    (fmt/print.go, `type Stringer interface { String() string }`). Such a type stays
    the defined type `.named path name []`, as distinct from an identical literal
    interface as in Go (Go spec, "Type identity"). -/
def libType? : String → String → Option Ty
  | "fmt", "Stringer" => some stringerTy
  | _, _ => none

/-- The predeclared types (Go spec, "Predeclared identifiers"). -/
def universeType? : String → Option Ty
  | "bool" => some .bool
  | "string" => some .string
  | "any" => some (.iface [])
  | "error" => some errorTy
  | "float32" => some (.float 32)
  | "float64" => some (.float 64)
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

/-- Whether a name is exported (Go spec, "Exported identifiers"). -/
def exported (x : String) : Bool := x.front.isUpper

/-- The package a bare, non-local name `x` is declared in: the current one, else a
    package the file dot-imports (Go spec, "Import declarations": `import . "p"` declares
    `p`'s exported names in the file's block). `none` for a predeclared name or one of
    no package the document holds. -/
def nameOwner (c : Ctx) (env : Env) (x : String) : Option String :=
  match c.pkgs.get? env.pkg with
  | none => none
  | some p =>
    if p.objs.contains x then some env.pkg
    else if !exported x then none
    else ((p.imports[env.file]?).getD []).findSome? fun (n, path) =>
      if n == "." && ((c.pkgs.get? path).map (·.objs.contains x)).getD false then some path else none

/-- Whether names of the package at `path` may be missing from the document: it holds
    none of the package's files (Go's own standard library), or only some (`isPartial`, Go's
    runnable files). A name such a package lacks is outside what GoCore runs, not a
    GoCore or exporter bug. -/
def partialPkg (c : Ctx) (path : String) : Bool :=
  match c.pkgs.get? path with
  | some p => p.isPartial
  | none => true

/-- A package the file dot-imports that the document does not hold (Go's own standard
    library), whose names GoCore cannot see. -/
def foreignDotImport (c : Ctx) (env : Env) : Option String :=
  ((c.pkgs.get? env.pkg).bind (·.imports[env.file]?)).bind fun is =>
    is.findSome? fun (n, path) => if n == "." && !c.pkgs.contains path then some path else none

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

/-- What a `fmt.Formatter` of the standard library prints, with the very verb and flags
    it is given, by `v`'s dynamic value as fmt looks at it (`handleMethods`): its wrapped
    value for `Immutable.Format` (std/types.go, lines 97-101), its error for
    `(*panicError).Format` (`fmt.Fprintf(s, fmt.FormatString(s, verb), e.err)`; `err`
    is the struct's first field, TestRecoveredErrorShape pins both). `none` when `v` has
    no `Format`. Any other Formatter is unsupported, and so is a nil `*panicError`,
    whose `Format` panics and which fmt then prints as `<nil>`. -/
def stdFormatted? (c : Ctx) (v : Val) : GM (Option Val) := do
  let d := v.dyn
  let some (p, tn, _, _) := methodSet c d.ty "Format" | return none
  if p == stdPath && tn == "Immutable" then
    let .struct_ _ [inner] := d | unsupported "fmt-formatter-pointer"
    return some inner
  if p == stdPath && tn == "panicError" then
    let .ptr _ (some l) := d | unsupported "fmt-formatter-nil"
    let .struct_ _ (e :: _) ← load l | stuck "panicError is not a struct"
    return some e
  unsupported "fmt-formatter"

/-- Whether `e` is certainly a constant expression (Go spec, "Constant expressions"): a
    literal, `true` or `false`, a package-level constant of this package or an imported
    one, or an operator over such. A local constant is not recognized: the answer is
    "no" when unsure. -/
def constExpr (c : Ctx) (env : Env) : Expr → Bool
  | .intLit _ | .strLit _ | .charLit _ | .floatLit .. => true
  | .ident x =>
    if (env.lookup x).isSome then false
    else match nameOwner c env x with
      | some p => match (c.pkgs.get? p).bind (·.objs.get? x) with
        | some (.const ..) => true
        | _ => false
      | none => x == "true" || x == "false"
  | .selector (.ident q) x =>
    if (env.lookup q).isSome then false
    else match (importOf c env q).bind fun p => (c.pkgs.get? p).bind (·.objs.get? x) with
      | some (.const ..) => true
      | _ => false
  | .paren x => constExpr c env x
  | .unary op x => ["-", "+", "!", "^"].contains op && constExpr c env x
  | .binary _ x y => constExpr c env x && constExpr c env y
  | _ => false

/-- The models of hand-written standard-library functions (see `Lib` below), by name.
    The exported Go of a function named here is never run. -/
def libFuncs : List String := ["FormatStruct", "Copy", "Equal", "EvalOperand", "recoveredError"]

/-- The `fmt` functions modelled (see `Fmt` below). -/
def fmtFuncs : List String := ["Println", "Print", "Printf", "Sprintf", "Sprint"]

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
  -- An integral float constant where Go needs an integer (an index, a length).
  | .rat _ _ n 1 => pure n
  | _ => stuck "not an integer"

def asStr : Val → GM String
  | .str _ s => pure s
  | _ => stuck "not a string"

/-- The types a field list declares, one per name. -/
def fieldTypes (fs : List Field) : List Expr :=
  fs.flatMap fun | .mk ns t => if ns.isEmpty then [t] else ns.map fun _ => t

/-- The depth bound of `unify`: each level of a type, and each list of type arguments,
    parameters or results, takes one, so it allows types about 32 levels deep. -/
def maxUnifyDepth : Nat := 64

mutual
/-- Type parameters bound by matching a parameter's type expression against an
    argument's type, for function argument type inference (Go spec, "Type inference":
    the type equations of the call's typed arguments). `k` bounds the depth of the
    types matched; past it nothing is bound, and inference reports what it lacks. -/
def unify (tps : List String) : Nat → Expr → Ty → List (String × Ty)
  | 0, _, _ => []
  | k + 1, e, t =>
    match e, t with
    | .ident x, t => if tps.contains x then [(x, t)] else []
    | .star e, .ptr t => unify tps k e t
    -- The same generic type's arguments, pairwise; a generic alias, named otherwise,
    -- binds nothing here.
    | .index (.ident n) es, .named _ m args | .index (.selector _ n) es, .named _ m args =>
      if n == m then unifyAll tps k es args else []
    | .paren e, t => unify tps k e t
    | .arrayType none e, .slice t => unify tps k e t
    | .arrayType (some _) e, .array _ t => unify tps k e t
    | .mapType ke ve, .map kt vt => unify tps k ke kt ++ unify tps k ve vt
    -- A function type, parameter by parameter and result by result; a final `...T`
    -- parameter has type `[]T` (Go spec, "Function types").
    | .funcType (.mk ps rs v), .func_ pts rts _ =>
      let pes := fieldTypes ps
      let pes := if v then pes.dropLast ++ (pes.getLast?.map (Expr.arrayType none)).toList else pes
      unifyAll tps k pes pts ++ unifyAll tps k (fieldTypes rs) rts
    | _, _ => []
/-- `unify` on type arguments, pairwise. -/
def unifyAll (tps : List String) : Nat → List Expr → List Ty → List (String × Ty)
  | 0, _, _ => []
  | k + 1, es, ts => (es.zip ts).flatMap fun (e, t) => unify tps k e t
end

/-- Whether the type expression `e` may mention the name `x`; past the depth bound `k`,
    or at a form it does not look into, it answers yes. -/
def mayMention (x : String) : Nat → Expr → Bool
  | 0, _ => true
  | k + 1, e =>
    match e with
    | .ident y => y == x
    | .star e | .paren e | .selector e _ => mayMention x k e
    | .index e es => mayMention x k e || es.any (mayMention x k)
    | .arrayType l e => (l.map (mayMention x k)).getD false || mayMention x k e
    | .mapType a b => mayMention x k a || mayMention x k b
    | .funcType (.mk ps rs _) => (fieldTypes ps ++ fieldTypes rs).any (mayMention x k)
    | .structType fs | .interfaceType fs => (fieldTypes fs).any (mayMention x k)
    | .intLit _ | .strLit _ | .charLit _ | .ellipsis => false
    | _ => true

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

/-- A `Printf` verb's flags, width and precision (fmt package documentation,
    "Printing"; `fmt/print.go`, `doPrintf`). -/
structure FmtSpec where
  minus : Bool := false
  plus : Bool := false
  space : Bool := false
  zero : Bool := false
  sharp : Bool := false
  wid : Option Nat := none
  prec : Option Nat := none
  deriving BEq

/-- No flag, width or precision. -/
def FmtSpec.plain (s : FmtSpec) : Bool := s == {}

/-- A decimal number at the head of `cs`, and the rest. -/
def parseNum (cs : List Char) : Option Nat × List Char :=
  let ds := cs.takeWhile Char.isDigit
  if ds.isEmpty then (none, cs)
  else (some (ds.foldl (fun n d => n * 10 + (d.toNat - '0'.toNat)) 0), cs.drop ds.length)

/-- The flags, width and precision after a `%`, and the rest, from the verb on
    (`doPrintf`). An argument index or a `*` width is left in the rest, where the verb
    is, for the caller to report. -/
def parseSpec (cs : List Char) : FmtSpec × List Char :=
  let rec flags : FmtSpec → List Char → FmtSpec × List Char
    | s, '-' :: cs => flags { s with minus := true } cs
    | s, '+' :: cs => flags { s with plus := true } cs
    | s, ' ' :: cs => flags { s with space := true } cs
    | s, '0' :: cs => flags { s with zero := true } cs
    | s, '#' :: cs => flags { s with sharp := true } cs
    | s, cs => (s, cs)
  let (s, cs) := flags {} cs
  let (wid, cs) := parseNum cs
  match cs with
  | '.' :: cs =>
    let (prec, cs) := parseNum cs
    ({ s with wid, prec := some (prec.getD 0) }, cs)
  | cs => ({ s with wid }, cs)

/-- `s` padded to the width with spaces, or zeros under the `0` flag, on the left, or
    on the right under `-` (`fmt.pad`, `writePadding`). -/
def FmtSpec.pad (spec : FmtSpec) (s : String) : String :=
  match spec.wid with
  | none => s
  | some w =>
    let p := String.ofList (List.replicate (w - s.length) (if spec.zero && !spec.minus then '0' else ' '))
    if spec.minus then s ++ p else p ++ s

/-- An integer in base 10 or 16, with its sign, precision (at least that many digits),
    `0x` under `#`, and padding: `fmt.fmtInteger`, step by step. -/
def FmtSpec.integer (spec : FmtSpec) (x : Int) (base : Nat) (upper : Bool) : String :=
  let neg := x < 0
  let u := x.natAbs
  if spec.prec == some 0 && u == 0 then { spec with zero := false }.pad ""
  else
    let prec := match spec.prec with
      | some p => p
      | none =>
        match spec.wid with
        | some w => if spec.zero && !spec.minus then w - (if neg || spec.plus || spec.space then 1 else 0) else 0
        | none => 0
    let digits := (Nat.toDigits base u).map fun d => if upper then d.toUpper else d
    let digits := List.replicate (prec - digits.length) '0' ++ digits
    let pre := if spec.sharp && base == 16 then (if upper then "0X" else "0x") else ""
    let sign := if neg then "-" else if spec.plus then "+" else if spec.space then " " else ""
    { spec with zero := false }.pad (sign ++ pre ++ String.ofList digits)

/-- A float's text from `FloatRep.formatVerb` (sign, infinity or NaN, unsigned text)
    signed and padded as `fmt.fmtFloat` does: a `+` sign under the `+` flag, a space
    under the space flag; an infinity always signed, a NaN only under `+` or space,
    both padded with spaces even under `0`; under `0`, the zeros after the sign. -/
def FmtSpec.float (spec : FmtSpec) (neg special : Bool) (body : String) : String :=
  let sign := if neg then "-" else if spec.space && !spec.plus then " " else "+"
  let num := sign ++ body
  if special then
    let spaces := { spec with zero := false }
    if body == "NaN" && !spec.space && !spec.plus then spaces.pad body else spaces.pad num
  else if spec.plus || sign != "+" then
    match spec.wid with
    | some w =>
      if spec.zero && !spec.minus && w > num.length then
        sign ++ String.ofList (List.replicate (w - num.length) '0') ++ body
      else spec.pad num
    | none => num
  else spec.pad body

/-- A string truncated to the precision, in runes, and padded (`fmt.fmtS`). -/
def FmtSpec.string (spec : FmtSpec) (s : String) : String :=
  spec.pad (match spec.prec with | some p => String.ofList (s.toList.take p) | none => s)

/-- The largest slice capacity GoCore allocates; a larger one is a question of memory,
    which the comparison leaves out (`unsupported`). -/
def maxSliceCap : Nat := 10000000

/-- The elements of a slice, in order. -/
def sliceElems (v : Val) : GM (List Val) := do
  match v with
  | .slice _ none => pure []
  | .slice _ (some r) =>
    match ← load r.base with
    | .array _ es => pure (es.extract r.off (r.off + r.len)).toList
    | _ => stuck "a slice without its array"
  | _ => stuck s!"... of {v.ty}"

/-- The arguments a model of a variadic library function sees: for `f(xs...)`, the
    elements of the slice `xs` in place of it, which are what the function's `...`
    parameter holds (Go spec, "Passing arguments to ... parameters"). -/
def spreadArgs (spread : Bool) (vs : List Val) : GM (List Val) := do
  if !spread then return vs
  match vs.getLast? with
  | some s => return vs.dropLast ++ (← sliceElems s)
  | none => stuck "... without an argument"

/-- An index `i` out of the range `[0, len)`: a run-time panic (Go spec, "Index
    expressions"), with the text Go's runtime prints (runtime/error.go, `boundsError`). -/
def indexOutOfRange (i : Int) (len : Nat) : GM α :=
  if i < 0 then runtimePanic s!"index out of range [{i}]"
  else runtimePanic s!"index out of range [{i}] with length {len}"

/-- The bounds of `x[lo:hi]` or `x[lo:hi:max]` for an operand of length `len` and
    capacity `cap`: `lo` defaults to 0, `hi` to the length, `max` to the capacity, and
    out of range, `0 ≤ lo ≤ hi ≤ max ≤ cap` failing, is a run-time panic (Go spec, "Slice
    expressions"). The text is Go's runtime's (runtime/error.go, `boundsError`), checked
    in the order the compiled code checks: `max`, then `hi`, then `lo`. `capWord` is
    "capacity" for a slice operand, "length" for an array or string. -/
def sliceBounds (lo hi mx : Option Int) (len cap : Nat) (capWord : String) : GM (Nat × Nat × Nat) := do
  let oob := fun (s : String) => runtimePanic (α := Nat × Nat × Nat) s!"slice bounds out of range {s}"
  let l := lo.getD 0
  match mx with
  | some m =>
    let h := hi.getD len
    if m < 0 then return ← oob s!"[::{m}]"
    if m > cap then return ← oob s!"[::{m}] with {capWord} {cap}"
    if h < 0 then return ← oob s!"[:{h}:]"
    if h > m then return ← oob s!"[:{h}:{m}]"
    if l < 0 then return ← oob s!"[{l}::]"
    if l > h then return ← oob s!"[{l}:{h}:]"
    pure (l.toNat, h.toNat, m.toNat)
  | none =>
    let h := hi.getD len
    if h < 0 then return ← oob s!"[:{h}]"
    if h > cap then return ← oob s!"[:{h}] with {capWord} {cap}"
    if l < 0 then return ← oob s!"[{l}:]"
    if l > h then return ← oob s!"[{l}:{h}]"
    pure (l.toNat, h.toNat, cap)

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
        -- A package the document does not hold: a type GoCore knows without its source.
        | none => return (libType? path sel).map fun _ => .named path sel []
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
        | some (.type fi (.mk _ tps@(_ :: _) alias te)) =>
          let targs ← args.mapM (evalType c n env)
          -- A generic alias denotes its type with the arguments substituted, no
          -- new named type (Go spec, "Alias declarations").
          if alias then
            return some (← evalType c n { pkg := path, file := fi, tenv := ← bindTParams tps targs } te)
          return some (.named path t targs)
        | _ => return none
      | none => return none
    | .star x => return (← typeOf? c n env x).map .ptr
    | .paren x => typeOf? c n env x
    | .structType _ | .interfaceType _ | .funcType _ | .arrayType .. | .mapType .. =>
      return some (← evalType c n env e)
    | .unsupported k => unsupported k
    | _ => return none

/-- The type a type expression denotes (Go spec, "Types"). -/
def evalType (c : Ctx) : Nat → Env → Expr → GM Ty
  | 0, _, _ => throw .fuel
  | n + 1, env, e => do
    match e with
    | .structType fs =>
      -- An embedded field promotes its type's methods to the struct (Go spec,
      -- "Struct types"), which GoCore's method sets do not include: an embedded
      -- interface, or a type (or pointer to one) that declares methods, is outside.
      let tys ← (fieldTypes fs).mapM (evalType c n env)
      let embedded := fs.flatMap fun | .mk ns _ => if ns.isEmpty then [true] else ns.map fun _ => false
      for (e, t) in embedded.zip tys do
        if e then
          if let .iface _ ← underlying c n t then unsupported "embedded-interface-field"
          let base := match t with | .ptr b => b | b => b
          -- A type of a package the document holds none or only some of has
          -- methods GoCore may not see: taken as having some. A type that embeds
          -- such a field in turn stops here when its own type is evaluated.
          if let .named p tn _ := base then
            let pre := tn ++ "."
            if partialPkg c p ||
                ((c.pkgs.get? p).map fun pk => pk.methods.fold (fun b k _ => b || k.startsWith pre) false).getD false then
              unsupported "embedded-methods"
      return .struct_ ((fieldNames fs).zip tys)
    | .interfaceType es =>
      -- An embedded element (`error`, a constraint's type union) would add to the
      -- method set, which GoCore does not compute; only methods it lists itself.
      if es.any (fun | .mk ns _ => ns.isEmpty) then unsupported "embedded-interface"
      let ms ← es.flatMapM fun (.mk ns te) => do
        let t ← evalType c n env te
        pure (ns.map (·, t))
      -- Sorted, so an interface type does not depend on the order it lists them in.
      return .iface (ms.mergeSort (fun a b => decide (a.1 ≤ b.1)))
    | .funcType (.mk ps rs v) =>
      let pts ← (fieldTypes ps).mapM (evalType c n env)
      -- A final `...T` parameter has type `[]T` (Go spec, "Function types").
      let pts := if v then pts.dropLast ++ (pts.getLast?.map Ty.slice).toList else pts
      return .func_ pts (← (fieldTypes rs).mapM (evalType c n env)) v
    | .arrayType none elt => return .slice (← evalType c n env elt)
    | .arrayType (some .ellipsis) _ => stuck "[...]T outside a composite literal"
    | .arrayType (some len) elt =>
      -- The length is a non-negative constant (Go spec, "Array types").
      let k ← asInt (← evalExpr c n env len)
      if k < 0 then stuck "negative array length"
      return .array k.toNat (← evalType c n env elt)
    | .mapType k v =>
      let kt ← evalType c n env k
      -- GoCore runs maps of integer, string and boolean keys only (`MapKey`): no NaN or
      -- signed-zero float keys, and no interface keys whose dynamic type could make
      -- hashing panic.
      match ← underlying c n kt with
      | .int _ | .string | .bool => pure ()
      | .float _ => unsupported "map-key-float"
      | .ptr _ => unsupported "map-key-pointer"
      | .iface _ => unsupported "map-key-interface"
      | .struct_ _ => unsupported "map-key-struct"
      | .array .. => unsupported "map-key-array"
      | u => unsupported s!"map-key-{u}"
      return .map kt (← evalType c n env v)
    | .unsupported k => unsupported k
    -- So that a pointer to, or a parenthesized, type GoCore does not hold reaches the
    -- checks below.
    | .star x => return .ptr (← evalType c n env x)
    | .paren x => evalType c n env x
    | _ =>
      match ← typeOf? c n env e with
      | some t => pure t
      | none =>
        -- A type of a package the document does not hold (Go's own standard library).
        if let .selector (.ident q) sel := e then
          if let some path := importOf c env q then
            if partialPkg c path then unsupported s!"{path}.{sel}"
        match e with
        | .index (.selector (.ident q) sel) _ =>
          if let some path := importOf c env q then
            if partialPkg c path then unsupported s!"{path}.{sel}"
          stuck "not a type"
        | .ident x =>
          if ["comparable", "complex64", "complex128"].contains x then
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
      if isLocalTypeName name then
        let some lt := (← get).localType? path name args | stuck s!"no local type {name}"
        let lenv : Env := { pkg := path, file := lt.file, scopes := lt.scopes, tenv := lt.tenv, targs := lt.targs }
        return ← underlying c n (← evalType c n lenv lt.ty)
      -- A package the document does not hold: a type GoCore knows without its source.
      if !c.pkgs.contains path then
        if let some u := libType? path name then return u
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
    | .slice _ => pure (.slice t none)
    | .map .. => pure (.map t none)
    | .float _ => pure (.float t 0.0)
    | .array k et =>
      if k > maxSliceCap then unsupported "array-large"
      pure (.array t (Array.replicate k (← zero c n et)))
    | u => unsupported s!"zero-value-{u}"

/-- The numeric constant `x / d` given type `t`, of underlying type `u`: for a float
    type, a typed constant rounded to the type, ties to even (Go spec, "Constants",
    "Representability"); for an integer type, the integer it must be. A constant
    that overflows the type, or is not an integer, is a compile error (stuck). -/
def constTo (t u : Ty) (x : Int) (d : Nat) : GM Val :=
  match u with
  | .float b =>
    if let some (num, den) := FloatRep.roundRat b x d then pure (.rat t b num den)
    else stuck s!"constant {x}/{d} overflows {t}"
  | .int k =>
    if d == 1 && k.represents x then pure (.int t x) else stuck s!"constant {x}/{d} is not an {k.name}"
  | _ => stuck s!"cannot use constant {x}/{d} as {t}"

/-- `v` assigned to a variable of type `t` (Go spec, "Assignability", "Representability"):
    an untyped constant takes the type, checked to be representable; a value assigned to
    an interface type keeps its own dynamic type; a value of an identical underlying type
    takes the variable's type. -/
def convert (c : Ctx) : Nat → Ty → Val → GM Val
  | 0, _, _ => throw .fuel
  | n + 1, t, v => do
    -- A typed float constant stored in a variable is a float.
    let v := v.materializeTyped
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
    | .slice _, .nilIface (.untyped .nil) => pure (.slice t none)
    | .map .., .nilIface (.untyped .nil) => pure (.map t none)
    | _, .map vt r => if (← underlying c n vt) == u then pure (.map t r) else stuck s!"cannot use {vt} as {t}"
    | _, .slice vt r => if (← underlying c n vt) == u then pure (.slice t r) else stuck s!"cannot use {vt} as {t}"
    | _, .array vt es => if (← underlying c n vt) == u then pure (.array t es) else stuck s!"cannot use {vt} as {t}"
    -- An untyped numeric constant given a float type: the nearest float (`constTo`);
    -- a float constant given an integer type must be an integer.
    | .float _, .int (.untyped _) x => return (← constTo t u x 1).materialize
    | .float _, .rat _ _ x d | .int _, .rat _ _ x d => return (← constTo t u x d).materialize
    | _, .float vt x => if (← underlying c n vt) == u then pure (.float t x) else stuck s!"cannot use {vt} as {t}"
    | _, .int vt x => if (← underlying c n vt) == u then pure (.int t x) else stuck s!"cannot use {vt} as {t}"
    | _, .bool _ b => if u == .bool then pure (.bool t b) else stuck s!"cannot use bool as {t}"
    | _, .str _ s => if u == .string then pure (.str t s) else stuck s!"cannot use string as {t}"
    | _, .struct_ vt fs => if (← underlying c n vt) == u then pure (.struct_ t fs) else stuck s!"cannot use {vt} as {t}"
    | .ptr _, .ptr _ l => pure (.ptr t l)
    | .func_ .., .func_ _ f => pure (.func_ t f)
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
    -- An integer or float to a float type: the nearest value of its width, ties to
    -- even. A float to an integer type: the value truncated toward zero; one out of the
    -- type's range, an infinity or a NaN is implementation-specific (Go spec,
    -- "Conversions between numeric types"), not modelled.
    -- A constant converted to a float type is a typed constant, its value rounded to
    -- the type (Go spec, "Conversions": "x is representable by a value of type T").
    | .float _, .int (.untyped _) x => constTo t u x 1
    | .float _, .rat _ _ x d | .int _, .rat _ _ x d => constTo t u x d
    | .float b, .int _ x => pure (.float t (FloatRep.ofRat b x 1))
    | .float b, .float _ x => pure (.float t (FloatRep.toSize b x))
    | .int k, .float _ x =>
      match FloatRep.truncate x with
      | some i => if k.represents i then pure (.int t i) else unsupported "float-to-int-out-of-range"
      | none => unsupported "float-to-int-out-of-range"
    -- `[]byte(s)` of a constant string: a new array of its bytes (Go spec,
    -- "Conversions to and from a string type"), of the capacity gc gives it
    -- (`GcRuntime.constStringBytesCap`). Of another string the capacity depends on
    -- escape analysis, and a typed constant cannot be told from a variable here: not
    -- modelled, but in a range clause (`rangeLoop`). `[]rune(s)` is not modelled.
    | .slice et, .str (.untyped _) s =>
      let .int .uint8 ← underlying c n et | unsupported "slice-of-string"
      stringBytes c n t et s
    | .slice _, .str .. => unsupported "slice-of-string"
    -- `string(b)` of a byte slice: the string of its bytes (Go spec, "Conversions to
    -- and from a string type"); a nil slice gives "". GoCore's strings hold valid
    -- UTF-8 only, so other bytes are not modelled, nor is `string(runes)`.
    | .string, .slice st _ =>
      let .slice et ← underlying c n st | stuck "a slice of a non-slice type"
      let .int .uint8 ← underlying c n et | unsupported "string-of-slice"
      let bs ← (← sliceElems v).mapM fun e => do pure (← asInt e).toNat.toUInt8
      match String.fromUTF8? ⟨bs.toArray⟩ with
      | some s => pure (.str t s)
      | none => unsupported "string-of-invalid-utf8"
    | .array .., .slice .. | .ptr (.array ..), .slice .. => unsupported "array-of-slice"
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
    | .floatLit num den => pure (.rat (.untyped .float) 0 num den)
    | .strLit s => pure (.str (.untyped .string) s)
    | .paren x => evalExpr c n env x
    | .composite (some (.arrayType (some .ellipsis) elt)) elts =>
      -- `[...]T{...}`: the length is the largest index plus one (Go spec, "Composite
      -- literals").
      let et ← evalType c n env elt
      let es ← elements c n env et elts
      pure (.array (.array es.size et) es)
    | .composite (some te) elts => composite c n env (← evalType c n env te) elts
    | .composite none _ => stuck "a composite literal without a type outside another"
    | .funcLit sig body => do
      let id := (← get).closures.size
      let cl : Closure :=
        { pkg := env.pkg, file := env.file, scopes := env.scopes, tenv := env.tenv, targs := env.targs, sig, body }
      modify fun s => { s with closures := s.closures.push cl }
      let t ← evalType c n env (.funcType sig)
      pure (.func_ t (some (.closure id)))
    | .selector x sel => selector c n env x sel
    | .index x is =>
      match ← funcValue? c n env e with
      | some v => pure v
      | none =>
        let [i] := is | stuck "an index with several indices"
        let v ← evalExpr c n env x
        return (← element c n v none (← evalExpr c n env i)).1
    | .sliceExpr x lo hi mx => sliceOf c n env x lo hi mx
    | .typeAssert x te => do
      let v ← evalExpr c n env x
      let t ← evalType c n env te
      match ← assertType c n v t with
      | some w => pure w
      -- Go's text names the operand's static interface type in Go's own spelling;
      -- the panic is not modelled.
      | none => unsupported "failed-type-assertion"
    | .call fn args ellipsis => Val.single (← evalCall c n env fn args ellipsis)
    | .star x => do
      match ← evalExpr c n env x with
      | .ptr _ (some l) => load l
      | .ptr _ none => nilDeref
      | _ => stuck "* of a non-pointer"
    | .unary "&" x =>
      match x with
      | .composite (some _) _ => do
        let v ← evalExpr c n env x
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
      -- In a constant declaration (`iota` is set) every operand is a constant, so a
      -- shifted untyped constant is one too, whatever the count's type (Go spec,
      -- "Constant expressions").
      if env.iota.isSome && (op == "<<" || op == ">>") then
        if let .int (.untyped k) v := a then return .int (.untyped k) (← shiftOp op none v (← asInt b))
      binary c n op a b
    | .unsupported k => unsupported k
    | .keyValue .. => stuck "key: value outside a composite literal"
    | .structType _ | .interfaceType _ | .funcType _ | .arrayType .. | .mapType .. => stuck "a type as a value"
    | .ellipsis => stuck "... outside an array type"

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
        -- A typed constant: its value converted to the type, a constant still.
        | some te => conversion c n (← evalType c n { pkg := path, file := fi } te) v
        | none => pure v
    | some (.func _ tps _ _) =>
      -- Its type arguments come from the context it is used in (Go spec, "Type
      -- inference": a generic function assigned to a typed variable or parameter).
      if !tps.isEmpty then unsupported "type-inference"
      funcVal c n path x []
    | some (.type ..) => stuck s!"type {x} as a value"
    | none =>
      if path != env.pkg then
        if partialPkg c path then unsupported s!"{path}.{x}"
        stuck s!"no {path}.{x}"
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
        -- A name of the package's own that one of its files the document leaves out
        -- declares (Go's runnable files: `bits.go` reads `divideError` of
        -- `bits_errors.go`).
        if partialPkg c env.pkg then unsupported s!"{env.pkg}.{x}"
        stuck s!"undefined: {x}"

/-- A declared function as a value. -/
def funcVal (c : Ctx) : Nat → String → String → List Ty → GM Val
  | 0, _, _, _ => throw .fuel
  | n + 1, path, x, targs => do
    let p ← pkgCtx c path
    match p.objs.get? x with
    | some (.func fi tps sig _) =>
      -- The type arguments not given come from the context (Go spec, "Type inference").
      if targs.length < (fieldListNames tps).length then unsupported "type-inference"
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
    | .slice et =>
      let es ← elements c n env et elts
      pure (.slice t (some (← newArray c n et es es.size)))
    | .array k et =>
      let es ← elements c n env et elts
      if es.size > k then stuck "an index out of an array literal's bounds"
      pure (.array t (es ++ Array.replicate (k - es.size) (← zero c n et)))
    -- A map literal of constant keys and elements, stored in order (Go spec, "Composite
    -- literals"; two equal constant keys are a compile error). gc stores the constant
    -- entries first and then each other one as an assignment of its own, which its
    -- order pass may move ahead of the enclosing statement (cmd/compile walk/complit.go
    -- `maplit`): GoCore does not model that, so a literal with any other entry stops.
    | .map kt et =>
      let m ← newMap
      for e in elts do
        let .keyValue ke ve := e | stuck "a map literal element without a key"
        if !((← staticElement c n env kt ke) && (← staticElement c n env et ve)) then
          unsupported "map-literal-dynamic"
        let key ← literalElement c n env kt ke
        mapStore (some m) key (← mapKeyOf key) (← literalElement c n env et ve)
      pure (.map t (some m))
    | _ => unsupported s!"composite-{t}"

/-- A new underlying array of `size` elements of type `et`: `es`, then zero values. A
    slice over it is as long as `es`. -/
def newArray (c : Ctx) : Nat → Ty → Array Val → Nat → GM SliceRep
  | 0, _, _, _ => throw .fuel
  | n + 1, et, es, size => do
    let len := es.size
    let es := es ++ Array.replicate (size - len) (← zero c n et)
    let cell ← allocAs (.array size et) (.array (.array size et) es)
    pure { base := ⟨cell, []⟩, off := 0, len, cap := size }

/-- `[]byte(s)` of slice type `t`, element type `et`: a new array of the string's bytes,
    exactly as long (`GcRuntime.constStringBytesCap`). -/
def stringBytes (c : Ctx) : Nat → Ty → Ty → String → GM Val
  | 0, _, _, _ => throw .fuel
  | n + 1, t, et, s => do
    let bs := s.toUTF8.data.map fun b => Val.int et b.toNat
    if bs.size > maxSliceCap then unsupported "slice-of-string-large"
    pure (.slice t (some (← newArray c n et bs (GcRuntime.constStringBytesCap bs.size))))

/-- The indices of an array or slice literal's elements: a key's constant value, else
    the previous index plus one, from 0 (Go spec, "Composite literals"). -/
def elementKeys (c : Ctx) : Nat → Env → List Expr → GM (List Nat)
  | 0, _, _ => throw .fuel
  | n + 1, env, elts => do
    let mut next := 0
    let mut keys := #[]
    for e in elts do
      if let .keyValue k _ := e then
        let i ← asInt (← evalExpr c n env k)
        if i < 0 then stuck "a negative index in a literal"
        if i > maxSliceCap then unsupported "literal-large"
        next := i.toNat
      keys := keys.push next
      next := next + 1
    pure keys.toList

/-- The elements of an array or slice literal of element type `et`, as many as its
    largest index plus one, the ones it does not give zero. An element may elide its
    composite literal's type, `&T` for a pointer element type (Go spec, "Composite
    literals"). -/
def elements (c : Ctx) : Nat → Env → Ty → List Expr → GM (Array Val)
  | 0, _, _, _ => throw .fuel
  | n + 1, env, et, elts => do
    let keys ← elementKeys c n env elts
    let z ← zero c n et
    let mut es := Array.replicate (keys.foldl (fun m k => max m (k + 1)) 0) z
    for (k, e) in keys.zip elts do
      let ve := match e with | .keyValue _ v => v | v => v
      es := es.set! k (← literalElement c n env et ve)
    pure es

/-- Whether a map literal's key or element `e`, of type `t`, is one gc stores as a
    static entry (cmd/compile walk/complit.go `isStaticCompositeLiteral`): a constant,
    `nil`, or a struct or array literal of those. It answers no for some of gc's static
    entries (nested literals, local constants), which then stop the run, never for a
    dynamic one. -/
def staticElement (c : Ctx) : Nat → Env → Ty → Expr → GM Bool
  | 0, _, _, _ => throw .fuel
  | n + 1, env, t, e => do
    let leaf := fun (x : Expr) => constExpr c env x ||
      (x matches .ident "nil" && (env.lookup "nil").isNone && (nameOwner c env "nil").isNone)
    match e with
    | .composite te inner =>
      let lt ← match te with
        | some te => evalType c n env te
        | none => pure t
      match ← underlying c n lt with
      | .struct_ _ | .array .. => pure (inner.all fun x => leaf (match x with | .keyValue _ v => v | v => v))
      | _ => pure false
    | x => pure (leaf x)

/-- An element `ve` of type `et` of an array, slice or map literal. It may elide its
    composite literal's type, `&T` for a pointer element type (Go spec, "Composite
    literals"). -/
def literalElement (c : Ctx) : Nat → Env → Ty → Expr → GM Val
  | 0, _, _, _ => throw .fuel
  | n + 1, env, et, ve => do
    let elided := match ve with
      | .composite none inner => some (false, inner)
      | .unary "&" (.composite none inner) => some (true, inner)
      | _ => none
    match elided with
    | some (amp, inner) =>
      match ← underlying c n et, amp with
      | .ptr pt, _ => do
        let w ← composite c n env pt inner
        pure (.ptr et (some ⟨← alloc w, []⟩))
      | _, false => composite c n env et inner
      | _, true => stuck "&{...} for a non-pointer element"
    | none => convert c n et (← evalExpr c n env ve)

/-- The operand of `len`, `cap`, or a `range` with at most one iteration variable.
    Go does not evaluate it when it is an array, or a pointer to one, without function
    calls or receives: the length is a constant (Go spec, "Length and capacity", "For
    statements with range clause"). GoCore evaluates it, which is the same unless it
    panics; a panic ends the run `unsupported`, as GoCore cannot tell the two apart. -/
def constantOperand (c : Ctx) : Nat → Env → Expr → GM Val
  | 0, _, _ => throw .fuel
  | n + 1, env, x =>
    tryCatch (evalExpr c n env x) fun
      | .panic .. => unsupported "constant-length-operand"
      | e => throw e

/-- Element `iv` of `v`, which is at `l` if addressable (Go spec, "Index expressions"):
    its value, where it is if it is addressable (a slice's element always is, an
    array's when the array is, Go spec "Address operators"), and its type. A pointer to
    an array indexes the array; a string gives its byte. -/
def element (c : Ctx) : Nat → Val → Option Loc → Val → GM (Val × Option Loc × Ty)
  | 0, _, _, _ => throw .fuel
  | n + 1, v, l, iv => do
    -- A map's element: the key's, else the element type's zero value, of a nil map too
    -- (Go spec, "Index expressions"). It is not addressable.
    if let .map t r := v then
      let (_, k, et) ← mapKey c n t iv
      return ((← mapIndex c n r k et).1, none, et)
    let i ← asInt iv
    match v with
    | .slice t r =>
      let .slice et ← underlying c n t | stuck "a slice of a non-slice type"
      match r with
      | some r =>
        if i < 0 || i ≥ r.len then return ← indexOutOfRange i r.len
        let el := r.loc i.toNat
        return (← load el, some el, et)
      | none => indexOutOfRange i 0
    | .array t es =>
      let .array _ et ← underlying c n t | stuck "an array of a non-array type"
      if i < 0 || i ≥ es.size then return ← indexOutOfRange i es.size
      return (es[i.toNat]!, l.map fun l => ⟨l.cell, l.path ++ [i.toNat]⟩, et)
    | .str _ s =>
      let bs := s.toUTF8
      if i < 0 || i ≥ bs.size then return ← indexOutOfRange i bs.size
      return (.int (.int .uint8) bs[i.toNat]!.toNat, none, .int .uint8)
    | .ptr _ (some pl) =>
      match ← load pl with
      | w@(.array ..) => element c n w (some pl) iv
      | _ => stuck "an index through a pointer to a non-array"
    | .ptr _ none => nilDeref
    | _ => stuck s!"an index of {v.ty}"

/-- A key `iv` of a map of type `t`: converted to the key type, its encoding, and the
    map's element type. -/
def mapKey (c : Ctx) : Nat → Ty → Val → GM (Val × MapKey × Ty)
  | 0, _, _ => throw .fuel
  | n + 1, t, iv => do
    let .map kt et ← underlying c n t | stuck "a map of a non-map type"
    let key ← convert c n kt iv
    pure (key, ← mapKeyOf key, et)

/-- The element at key `k` of the map `r` refers to, of element type `et`, and whether
    the map has the key: else the zero value, of a nil map too (Go spec, "Index
    expressions"). -/
def mapIndex (c : Ctx) : Nat → Option Nat → MapKey → Ty → GM (Val × Bool)
  | 0, _, _, _ => throw .fuel
  | n + 1, r, k, et => do
    match ← mapLookup r k with
    | some v => pure (v, true)
    | none => pure (← zero c n et, false)

/-- `x[lo:hi]` and `x[lo:hi:max]` (Go spec, "Slice expressions"): of a string, a
    string of its bytes; of a slice, a slice of the same type and underlying array; of
    an addressable array (through a pointer too), a slice of it. -/
def sliceOf (c : Ctx) : Nat → Env → Expr → Option Expr → Option Expr → Option Expr → GM Val
  | 0, _, _, _, _, _ => throw .fuel
  | n + 1, env, x, lo, hi, mx => do
    let (v, l) ← operand c n env x
    let idx := fun (e : Option Expr) => e.mapM fun e => do asInt (← evalExpr c n env e)
    let lo ← idx lo
    let hi ← idx hi
    let mx ← idx mx
    -- A slice of the array at `al`, of type `[]et`.
    let ofArray := fun (al : Loc) (t : Ty) (size : Nat) => do
      let .array _ et ← underlying c n t | stuck "an array of a non-array type"
      let (a, b, m) ← sliceBounds lo hi mx size size "length"
      pure (Val.slice (.slice et) (some ⟨al, a, b - a, m - a, none⟩))
    match v with
    | .str _ s =>
      let bs := s.toUTF8
      let (a, b, _) ← sliceBounds lo hi mx bs.size bs.size "length"
      let t := (defaultVal v).ty
      match String.fromUTF8? (bs.extract a b) with
      | some r => pure (.str t r)
      -- GoCore's strings are valid UTF-8; Go's may not be.
      | none => unsupported "string-slice-mid-rune"
    | .slice t none =>
      let _ ← sliceBounds lo hi mx 0 0 "capacity"
      pure (.slice t none)
    | .slice t (some r) =>
      -- With two possible capacities, a bound between them panics with one only.
      if let some alt := r.alt then
        let bound := mx.getD (hi.getD r.len)
        if bound > min r.cap alt then unsupported "slice-escape-dependent"
      let (a, b, m) ← sliceBounds lo hi mx r.len r.cap "capacity"
      -- A full slice expression sets the capacity; otherwise both shrink by `lo`.
      let alt := if mx.isSome then none else r.alt.map (· - a)
      pure (.slice t (some { base := r.base, off := r.off + a, len := b - a, cap := m - a, alt }))
    | .array t es =>
      match l with
      | some al => ofArray al t es.size
      | none => stuck "a slice of an unaddressable array"
    | .ptr _ (some pl) =>
      match ← load pl with
      | .array t es => ofArray pl t es.size
      | _ => stuck "a slice through a pointer to a non-array"
    | .ptr _ none => nilDeref
    | _ => stuck s!"a slice of {v.ty}"

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
    | .index x [i] =>
      if let some f ← funcValue? c n env e then return (f, none)
      let (v, l) ← operand c n env x
      let (w, el, _) ← element c n v l (← evalExpr c n env i)
      return (w, el)
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
              if !partialPkg c path then stuck s!"{q}.{sel} is not addressable"
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
    | .index x [i] =>
      let (v, l) ← operand c n env x
      match ← element c n v l (← evalExpr c n env i) with
      | (_, some el, t) => return (el, t)
      | _ => stuck "an element of an unaddressable value"
    | _ => stuck "not addressable"

/-- Where an assignment's target `e` stores, its operands evaluated (Go spec,
    "Assignment statements", "IncDec statements"): an index `x[i]` is the map's entry,
    or the element, its bounds checked when it is loaded or stored; anything else its
    location (`evalAddr`). -/
def evalPlace (c : Ctx) : Nat → Env → Expr → GM Place
  | 0, _, _ => throw .fuel
  | n + 1, env, e => do
    match e with
    | .paren x => evalPlace c n env x
    | .index x [i] =>
      let (v, l) ← operand c n env x
      let iv ← evalExpr c n env i
      if let .map t r := v then
        let (key, k, et) ← mapKey c n t iv
        return .entry r key k et
      return .elem v l iv
    | _ =>
      let (l, t) ← evalAddr c n env e
      pure (.loc l t)

/-- Where element `iv` of `v` is, its bounds checked. -/
def elemLoc (c : Ctx) : Nat → Val → Option Loc → Val → GM (Loc × Ty)
  | 0, _, _, _ => throw .fuel
  | n + 1, v, l, iv => do
    match ← element c n v l iv with
    | (_, some el, t) => pure (el, t)
    | _ => stuck "an element of an unaddressable value"

/-- The value at a place: a map entry's element, else the element type's zero value. -/
def loadPlace (c : Ctx) : Nat → Place → GM Val
  | 0, _ => throw .fuel
  | _, .loc l _ => load l
  | n + 1, .elem v l iv => do load (← elemLoc c n v l iv).1
  | n + 1, .entry r _ k t => do pure (← mapIndex c n r k t).1

/-- `v` stored at a place, converted to its type; into a nil map's entry it panics
    (`mapStore`). -/
def storePlace (c : Ctx) : Nat → Place → Val → GM Unit
  | 0, _, _ => throw .fuel
  | n + 1, .loc l t, v => do store l (← convert c n t v)
  | n + 1, .elem w l iv, v => do
    let (el, t) ← elemLoc c n w l iv
    store el (← convert c n t v)
  | n + 1, .entry r key k t, v => do mapStore r key k (← convert c n t v)

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
        for (m, mt) in ms do
          if (← methodOfType c n d.ty m mt).isNone then return none
        return some (.boxed t d)
      | _ => return if d.ty == t then some d else none

/-- Method `m` of the method set of type `t`, if it has one of function type `mt`
    exactly (Go spec, "Method sets", "Implementing an interface", "Type identity"). -/
def methodOfType (c : Ctx) : Nat → Ty → String → Ty → GM (Option (String × List Ty × Method))
  | 0, _, _, _ => throw .fuel
  | n + 1, t, m, mt => do
    match methodSet c t m with
    | some (p, _, args, me) =>
      let ft ← evalType c n { pkg := p, file := me.file, tenv := me.recvTParams.zip args } (.funcType me.sig)
      return if ft == mt then some (p, args, me) else none
    | none => return none

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
    -- A float's negation flips its sign, of a zero and a NaN too (IEEE 754).
    | "+", .float .. | "+", .rat .. => pure v
    | "-", .float t x => pure (.float t (-x))
    | "-", .rat t size x d => pure (.rat t size (-x) d)
    | _, _ => stuck s!"unary {op} on {v.ty}"

/-- A binary operator other than `&&` and `||` (Go spec, "Operators"). An untyped
    operand is first converted to the other's type. -/
def binary (c : Ctx) : Nat → String → Val → Val → GM Val
  | 0, _, _, _ => throw .fuel
  | n + 1, op, a, b => do
    if op == "<<" || op == ">>" then
      if let .rat .. := a then unsupported "float-shift"
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
    -- An untyped integer or rune constant with an untyped float constant is a float
    -- constant (Go spec, "Constant expressions": the operand of the kind later in
    -- integer, rune, floating-point).
    let lift := fun (v : Val) => match v with
      | .int (.untyped _) x => Val.rat (.untyped .float) 0 x 1
      | v => v
    let (a, b) := match a, b with
      | .rat .., .int (.untyped _) _ | .int (.untyped _) _, .rat .. => (lift a, lift b)
      | _, _ => (a, b)
    -- Two float constants: exact rational arithmetic (Go spec, "Constant expressions"),
    -- the result rounded to the type of a typed one (a typed constant's value must be
    -- representable: Go rejects one that overflows).
    if let (.rat ta sa x dx, .rat tb sb y dy) := (a, b) then
      let (t, size) := if sa == 0 then (tb, sb) else (ta, sa)
      let cmp := compare (x * dy) (y * dx)
      match op with
      | "==" => return .bool (.untyped .bool) (cmp == .eq)
      | "!=" => return .bool (.untyped .bool) (cmp != .eq)
      | "<" | "<=" | ">" | ">=" => return .bool (.untyped .bool) (← cmpOp op cmp)
      | _ => pure ()
      let (num, den) : Int × Int ← match op with
        | "+" => pure (x * dy + y * dx, dx * dy)
        | "-" => pure (x * dy - y * dx, dx * dy)
        | "*" => pure (x * y, dx * dy)
        | "/" => if y == 0 then stuck "constant division by zero" else pure (x * dy, dx * y)
        | _ => stuck s!"{op} on float constants"
      let (num, den) := if den < 0 then (-num, -den) else (num, den)
      let g := Nat.gcd num.natAbs den.toNat
      let (num, den) := (num / g, den.toNat / g)
      if size == 0 then return .rat t 0 num den
      match FloatRep.roundRat size num den with
      | some (num, den) => return .rat t size num den
      | none => stuck s!"constant overflows {t}"
    -- A typed float constant meeting a variable is a float.
    let (a, b) := (a.materializeTyped, b.materializeTyped)
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
    -- Floats: one IEEE 754 operation, rounded to the type's width; comparisons of a NaN
    -- are false (Go spec, "Arithmetic operators", "Comparison operators").
    | .float t x, .float _ y =>
      let .float size ← underlying c n t | stuck "a float of a non-float type"
      match op with
      | "+" => return .float t (FloatRep.toSize size (x + y))
      | "-" => return .float t (FloatRep.toSize size (x - y))
      | "*" => return .float t (FloatRep.toSize size (x * y))
      | "/" => return .float t (FloatRep.toSize size (x / y))
      | "<" => return .bool (.untyped .bool) (x < y)
      | "<=" => return .bool (.untyped .bool) (x ≤ y)
      | ">" => return .bool (.untyped .bool) (y < x)
      | ">=" => return .bool (.untyped .bool) (y ≤ x)
      | _ => stuck s!"{op} on floats"
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
    -- Interface values whose dynamic values are slices: Go's runtime panics comparing
    -- them (not modelled).
    match a, b with
    | .boxed _ (.slice ..), _ | _, .boxed _ (.slice ..) => unsupported "uncomparable"
    -- Two maps of one type through interfaces: Go panics; a map against nil or a value
    -- of another dynamic type is unequal (below).
    | .boxed _ (.map t _), .boxed _ (.map u _) => if t == u then unsupported "uncomparable"
    | _, _ => pure ()
    let (a, b) := (a.dyn, b.dyn)
    -- A float constant compared with a variable (through an interface) is a float.
    let (a, b) := match a, b with
      | .rat .., .rat .. => (a, b)
      | _, _ => (a.materialize, b.materialize)
    -- Values of different dynamic types are unequal: by the precondition (see the
    -- module doc), only two interface operands can differ in type.
    let same := a.ty == b.ty || a.ty.isUntyped || b.ty.isUntyped
    match a, b with
    | .ptr _ l, .nilIface (.untyped .nil) | .nilIface (.untyped .nil), .ptr _ l => pure l.isNone
    | .func_ _ f, .nilIface (.untyped .nil) | .nilIface (.untyped .nil), .func_ _ f => pure f.isNone
    | .slice _ r, .nilIface (.untyped .nil) | .nilIface (.untyped .nil), .slice _ r => pure r.isNone
    | .map _ r, .nilIface (.untyped .nil) | .nilIface (.untyped .nil), .map _ r => pure r.isNone
    | .nilIface _, .nilIface _ => pure true
    | .nilIface _, _ | _, .nilIface _ => pure false
    -- Go compiles `==` on functions only against nil, which `binary` has converted to
    -- the function's type. Two non-nil functions meet here only as the dynamic values
    -- of interface operands, where Go panics at run time (not modelled).
    | .func_ _ f, .func_ _ g =>
      if f.isNone || g.isNone then pure (f.isNone && g.isNone) else unsupported "uncomparable"
    -- A slice compares only with nil, likewise.
    | .slice _ r, .slice _ q =>
      if r.isNone || q.isNone then pure (r.isNone && q.isNone) else unsupported "uncomparable"
    -- A map too.
    | .map _ r, .map _ q =>
      if !same then return false
      if r.isNone || q.isNone then pure (r.isNone && q.isNone) else unsupported "uncomparable"
    | _, _ =>
      if !same then return false
      match a, b with
      | .int _ x, .int _ y => pure (x == y)
      | .bool _ x, .bool _ y => pure (x == y)
      | .str _ x, .str _ y => pure (x == y)
      -- IEEE 754 equality: a NaN equals nothing, +0 equals -0.
      | .float _ x, .float _ y => pure (x == y)
      | .rat _ _ x dx, .rat _ _ y dy => pure (x == y && dx == dy)
      | .ptr _ l, .ptr _ m => pure (l == m)
      | .struct_ _ xs, .struct_ _ ys => equalAll c n xs ys
      -- Arrays element by element (Go spec, "Comparison operators").
      | .array _ xs, .array _ ys => equalAll c n xs.toList ys.toList
      | _, _ => pure false

/-- Fields or elements pairwise, in order, stopping at the first unequal pair. A
    function, slice or map among them makes the type incomparable: Go rejects the
    comparison, or through interfaces panics at run time; not modelled. -/
def equalAll (c : Ctx) : Nat → List Val → List Val → GM Bool
  | 0, _, _ => throw .fuel
  | n + 1, xs, ys => do
    for (x, y) in xs.zip ys do
      if let .func_ .. := x then unsupported "uncomparable"
      if let .slice .. := x then unsupported "uncomparable"
      if let .map .. := x then unsupported "uncomparable"
      if !(← equal c n x y) then return false
    return true

-- Calls

/-- A call `fn(args)`, with its results: a conversion, a builtin, a model of a library
    function, a method call or a call of a function value (Go spec, "Calls",
    "Conversions", "Built-in functions"). The function value and the arguments are
    evaluated left to right. `spread` is set for `fn(args...)`. -/
def evalCall (c : Ctx) : Nat → Env → Expr → List Expr → Bool → GM (List Val)
  | 0, _, _, _, _ => throw .fuel
  | n + 1, env, fn, args, spread => do
    -- A conversion `T(x)`.
    if let some t ← typeOf? c n env fn then
      let [a] := args | stuck "a conversion takes one argument"
      -- `T(x << s)` of an untyped constant `x` (Go spec, "Operators"): with an untyped
      -- (constant) count, a constant shift, converted after; with a typed count, `x`
      -- takes the type `T` first, as it would without the shift when the count is a
      -- variable. A typed constant count gives a constant shift instead, which is the
      -- same value when `x` fits the integer type `T` (an overflowing constant result
      -- is a compile error); otherwise GoCore cannot tell the two apart.
      if let .binary op l r := a then
        if op == "<<" || op == ">>" then
          let lv ← evalExpr c n env l
          let rv ← evalExpr c n env r
          if let .int (.untyped _) x := lv then
            if !(rv.ty matches .untyped _) then
              let .int k ← underlying c n t | unsupported "untyped-shift"
              if !k.represents x then unsupported "untyped-shift"
              return [← conversion c n t (← binary c n op (← conversion c n t lv) rv)]
          return [← conversion c n t (← binary c n op lv rv)]
      return [← conversion c n t (← evalExpr c n env a)]
    match fn with
    | .ident b =>
      if (env.lookup b).isNone && (nameOwner c env b).isNone then
        return ← builtin c n env b args spread
    | .selector (.ident q) f =>
      if (env.lookup q).isNone then
        if let some path := importOf c env q then
          if path == "fmt" && fmtFuncs.contains f then
            return ← fmtCall c n f (← spreadArgs spread (← evalArgs c n env args))
          -- `slices.Clip(s)` is `s[:len(s):len(s)]` (its documentation, package slices).
          if path == "slices" && f == "Clip" && !spread then
            match ← evalArgs c n env args with
            | [.slice t r] => return [.slice t (r.map fun r => { r with cap := r.len, alt := none })]
            | _ => stuck "slices.Clip of a non-slice"
          -- `stringslite.Clone(s)` returns a copy of `s` (internal/stringslite, which
          -- strconv's errors use): strings are immutable, so the copy is `s`.
          if path == "internal/stringslite" && f == "Clone" && !spread then
            match ← evalArgs c n env args with
            | [s@(.str ..)] => return [s]
            | _ => stuck "stringslite.Clone of a non-string"
          if !c.pkgs.contains path then unsupported s!"{path}.{f}"
    | _ => pure ()
    -- A declared function called by name: a generic one gets its type arguments by
    -- inference from the arguments (Go spec, "Type inference").
    if let some fv ← declaredFunc? c n env fn then
      return ← callValue c n fv (← evalArgs c n env args) spread
    let fv ← evalExpr c n env fn
    let vs ← evalArgs c n env args
    callValue c n fv vs spread

/-- `append(s, xs...)` of a slice `s` of type `t`, element type `et` (Go spec,
    "Appending to and copying slices"): within its capacity, the values are written
    into its underlying array and the result shares it; past it, they go with `s`'s
    elements into a new array, whose capacity is the gc runtime's choice
    (`GcRuntime.grownCap`). -/
def appendValues (c : Ctx) : Nat → Ty → Ty → Val → List Val → Bool → GM Val
  | 0, _, _, _, _, _ => throw .fuel
  | n + 1, t, et, s, xs, spread => do
    if xs.isEmpty then return s
    let (len, cap, alt, r?) := match s with
      | .slice _ (some r) => (r.len, r.cap, r.alt, some r)
      | _ => (0, 0, none, none)
    let newLen := len + xs.length
    -- With two possible capacities (`SliceRep.alt`), an append that fits one only
    -- writes into the old array in one run of gc and into a new one in the other.
    -- GoCore takes the new array, and the old one becomes unreadable: until it is
    -- read or written again, the two runs cannot be told apart.
    let ambiguous := match r?, alt with
      | some _, some a => newLen > min cap a && newLen ≤ max cap a
      | _, _ => false
    if let some r := r? then
      if newLen ≤ cap && !ambiguous then
        for (v, i) in xs.zipIdx do
          store (r.loc (len + i)) v
        return .slice t (some { r with len := newLen })
    let elem ← layout c n et
    -- The new array's capacity, and the other one it may have (`SliceRep.alt`).
    let (newCap, alt) := match alt with
      | some a =>
        if ambiguous then (GcRuntime.grownCap newLen (min cap a) elem, some (max cap a))
        else (GcRuntime.grownCap newLen cap elem, some (GcRuntime.grownCap newLen a elem))
      | none => (GcRuntime.grownCap newLen cap elem, none)
    let alt := alt.filter (· != newCap)
    -- Grown from empty, the slice may get a stack buffer (`GcRuntime.stackAppendCap`);
    -- with two capacities already, that may be a third, more than GoCore follows.
    let stack := (GcRuntime.stackAppendCap elem len newLen spread).filter (· != newCap)
    let alt ← match alt, stack with
      | some a, some k => if k == a then pure (some a) else unsupported "append-escape-dependent"
      | some a, none => pure (some a)
      | none, k => pure k
    if max newCap (alt.getD 0) > maxSliceCap then unsupported "append-large"
    let old ← sliceElems s
    if ambiguous then
      let some r := r? | stuck "an ambiguous append of a nil slice"
      if !r.base.path.isEmpty then stuck "a slice of two capacities inside a variable"
      storeCell r.base.cell (.poison "append-escape-dependent")
    -- The array holds the larger capacity, so either run's appends fit it.
    let rep ← newArray c n et (old ++ xs).toArray (max newCap (alt.getD 0))
    pure (.slice t (some { rep with cap := newCap, alt }))

/-- `t`'s size, alignment and whether it holds pointers, as the gc toolchain lays it
    out (`GcRuntime.Layout`). -/
def layout (c : Ctx) : Nat → Ty → GM GcRuntime.Layout
  | 0, _ => throw .fuel
  | n + 1, t => do
    match ← underlying c n t with
    | .int k => pure (.ofInt k)
    | .bool => pure .bool
    | .string => pure .string
    | .ptr _ | .func_ .. | .map .. => pure .word
    | .iface _ => pure .iface
    | .slice _ => pure .slice
    | .float b => pure (.ofFloat b)
    | .array k e => pure (.array k (← layout c n e))
    | .struct_ fs => pure (.struct_ (← fs.mapM fun (_, ft) => layout c n ft))
    | u => unsupported s!"layout-{u}"

/-- A declared function named by `fn` (`f`, `pkg.F`, or with some of its type
    arguments, `f[T]`), as a value whose remaining type arguments a call infers from
    its arguments (Go spec, "Type inference"); `none` if `fn` names none. -/
def declaredFunc? (c : Ctx) : Nat → Env → Expr → GM (Option Val)
  | 0, _, _ => throw .fuel
  | n + 1, env, fn => do
    let named : Expr → Option (String × String) := fun
      | .ident f => if (env.lookup f).isSome then none else (nameOwner c env f).map (·, f)
      | .selector (.ident q) f => if (env.lookup q).isSome then none else (importOf c env q).map (·, f)
      | _ => none
    let (declared, targExprs) := match fn with
      | .index g ts => (named g, ts)
      | g => (named g, [])
    let some (path, f) := declared | return none
    let some (.func ..) := (c.pkgs.get? path).bind (·.objs.get? f) | return none
    let targs ← targExprs.mapM (evalType c n env)
    return some (.func_ (.func_ [] [] false) (some (.decl path f targs)))

/-- `defer fn(args)`: the function value and the arguments are evaluated now and the
    call is saved, to run when the surrounding function returns (Go spec, "Defer
    statements"). Deferred builtins, conversions and functions of packages the
    document does not hold are not modelled. -/
def deferCall (c : Ctx) : Nat → Env → Expr → List Expr → Bool → GM Unit
  | 0, _, _, _, _ => throw .fuel
  | n + 1, env, fn, args, spread => do
    if (← typeOf? c n env fn).isSome then unsupported "defer-conversion"
    match fn with
    | .ident b =>
      if (env.lookup b).isNone && (nameOwner c env b).isNone then unsupported s!"defer-{b}"
    | .selector (.ident q) f =>
      if (env.lookup q).isNone then
        if let some path := importOf c env q then
          if !c.pkgs.contains path then unsupported s!"{path}.{f}"
    | _ => pure ()
    let fv ← match ← declaredFunc? c n env fn with
      | some fv => pure fv
      | none => evalExpr c n env fn
    let vs ← evalArgs c n env args
    match (← get).defers with
    | d :: ds => modify fun s => { s with defers := ((fv, vs, spread) :: d) :: ds }
    | [] => stuck "defer outside a function"

/-- Arguments, left to right; `f(g())` passes `g`'s results (Go spec, "Calls"). -/
def evalArgs (c : Ctx) : Nat → Env → List Expr → GM (List Val)
  | 0, _, _ => throw .fuel
  | n + 1, env, args => do
    match args with
    | [a] => pure (← evalExpr c n env a).results
    | _ => args.mapM (evalExpr c n env)

/-- The builtins the transpiler emits (Go spec, "Built-in functions", "Handling
    panics"). -/
def builtin (c : Ctx) : Nat → Env → String → List Expr → Bool → GM (List Val)
  | 0, _, _, _, _ => throw .fuel
  | n + 1, env, b, args, spread => do
    if spread && b != "append" then stuck s!"... in a call of {b}"
    match b, args with
    | "panic", [a] =>
      let v ← evalExpr c n env a
      -- `panic(nil)` panics with a `*runtime.PanicNilError` (Go 1.21), a runtime
      -- error GoCore does not model.
      if let .nilIface _ := v then unsupported "panic-nil"
      throw (.panic v false)
    | "new", [te] =>
      let t ← evalType c n env te
      let cell ← allocAs t (← zero c n t)
      pure [.ptr (.ptr t) (some ⟨cell, []⟩)]
    -- (Go spec, "Length and capacity".)
    | "len", [a] | "cap", [a] =>
      -- Of an array (or pointer to one), without calls, a constant: the operand is not
      -- evaluated (Go spec, "Length and capacity"). See `constantOperand`.
      let k ← match ← constantOperand c n env a with
        | .str _ s => if b == "len" then pure s.utf8ByteSize else stuck "cap of a string"
        | .slice _ none => pure 0
        | .slice _ (some r) =>
          if b == "len" then pure r.len
          else if r.alt.isSome then unsupported "cap-escape-dependent"
          else pure r.cap
        | .array _ es => pure es.size
        -- A map's number of entries; a nil map has none (`cap` of a map does not compile).
        | .map _ none => pure 0
        | .map _ (some i) => pure (← getMap i).entries.size
        -- A pointer to an array, nil too: the array type's length.
        | v@(.ptr t _) => do
          let .ptr pt ← underlying c n t | stuck s!"{b} of {v.ty}"
          let .array k _ ← underlying c n pt | stuck s!"{b} of {v.ty}"
          pure k
        | v => unsupported s!"{b}-{v.ty}"
      pure [.int (.int .int) k]
    -- (Go spec, "Making slices, maps and channels".)
    | "make", te :: sizes =>
      let t ← evalType c n env te
      -- A map: a new, empty one. Its size is only a hint, evaluated and not used; gc
      -- takes a negative one as 0 (Go 1.25's runtime/map_swiss.go, `makemap`; a
      -- negative constant does not compile).
      if let .map .. ← underlying c n t then
        for e in sizes do
          let _ ← evalExpr c n env e
        return [.map t (some (← newMap))]
      let .slice et ← underlying c n t | unsupported s!"make-{t}"
      let sizes ← sizes.mapM fun e => do asInt (← evalExpr c n env e)
      let (len, cap) ← match sizes with
        | [l] => pure (l, l)
        | [l, m] => pure (l, m)
        | _ => stuck "make of a slice takes one or two sizes"
      if len < 0 then runtimePanic "makeslice: len out of range"
      -- A slice that large is a resource question, which the comparison leaves out;
      -- and which of gc's panics a huge size gets depends on escape analysis.
      if len > maxSliceCap || cap > maxSliceCap then unsupported "make-large"
      if cap < len then runtimePanic "makeslice: cap out of range"
      let z ← zero c n et
      pure [.slice t (some (← newArray c n et (Array.replicate len.toNat z) cap.toNat))]
    -- (Go spec, "Appending to and copying slices".)
    | "append", s :: rest =>
      let sv ← evalExpr c n env s
      let t := sv.ty
      let .slice et ← underlying c n t | stuck s!"append to {t}"
      let xs ← if spread then
          match rest with
          | [r] =>
            match ← evalExpr c n env r with
            -- `append(bytes, s...)` appends a string's bytes.
            | .str _ str => pure (str.toUTF8.toList.map fun b => Val.int et b.toNat)
            | v => sliceElems v
          | _ => stuck "append(s, x, ys...)"
        else
          let vs ← rest.mapM (evalExpr c n env)
          vs.mapM (convert c n et)
      return [← appendValues c n t et sv xs spread]
    | "copy", [d, s] =>
      let dv ← evalExpr c n env d
      let src ← match ← evalExpr c n env s with
        -- `copy(bytes, s)` copies a string's bytes.
        | .str _ str => pure (str.toUTF8.toList.map fun b => Val.int (.int .uint8) b.toNat)
        | v => sliceElems v
      match dv with
      | .slice _ none => pure [.int (.int .int) 0]
      | .slice dt (some r) =>
        let .slice et ← underlying c n dt | stuck "copy to a non-slice"
        let k := min r.len src.length
        -- The source is read whole first, so overlapping slices copy as `memmove`.
        for (v, i) in (src.take k).zipIdx do
          store (r.loc i) (← convert c n et v)
        pure [.int (.int .int) k]
      | _ => stuck "copy to a non-slice"
    -- (Go spec, "Handling panics".) A panic is recovered only by a deferred call
    -- calling `recover` itself, while its function panics; else nil. A runtime
    -- error's value is a `runtime.Error`, which GoCore does not model.
    --
    -- "Called directly by a deferred function" is an exact call depth: `callBody`
    -- runs the panicking function's deferred calls one call deeper than it, and
    -- records that depth with the panic (`recoverable`). Every call GoCore makes, of
    -- a function, method, method value or closure, adds one level, and nothing else
    -- does, so `recover` is at that depth exactly when the deferred function itself
    -- calls it. A deferred function's own deferred calls run when it returns,
    -- outside the panic, with nothing recoverable: their `recover` is nil, as `go
    -- run` shows (fixture `defer`, `nestedDefer`).
    | "recover", [] =>
      let s ← get
      match s.recoverable with
      | some (d, v, runtime) =>
        if s.defers.length != d then return [.nilIface (.iface [])]
        if runtime then unsupported "recover-runtime-error"
        modify fun s => { s with recoverable := none }
        pure [← convert c n (.iface []) v]
      | none => pure [.nilIface (.iface [])]
    -- (Go spec, "Deletion of map elements".) Of a nil map, or a key it does not hold,
    -- nothing.
    | "delete", [m, k] =>
      let mv ← evalExpr c n env m
      let kv ← evalExpr c n env k
      let .map t r := mv | stuck "delete of a non-map"
      let (_, key, _) ← mapKey c n t kv
      if let some i := r then modifyMap i (·.erase key)
      pure []
    | "print", _ | "println", _ =>
      -- They write to standard error, which the comparison leaves out (Go spec,
      -- "Bootstrapping").
      let _ ← args.mapM (evalExpr c n env)
      pure []
    | b, _ =>
      -- An exported name is no builtin: one of a dot-imported package the
      -- document does not hold.
      if exported b then
        if let some p := foreignDotImport c env then unsupported s!"dot-import-{p}"
      -- A name of a package of which the document holds only some files (Go's
      -- runnable ones): one of its other files declares it.
      if (c.pkgs.get? env.pkg).any (·.isPartial) then unsupported s!"{env.pkg}.{b}"
      unsupported s!"builtin-{b}"

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
    -- `Error() string` or `String() string` exactly: the `error` and `fmt.Stringer`
    -- interfaces.
    match ← methodOfType c n v.ty m (.func_ [] [.string] false) with
    | some (p, args, me) => return some (← Val.single (← callMethod c n p args me v [] false))
    | none => return none

/-- Call a function value with argument values. -/
def callValue (c : Ctx) : Nat → Val → List Val → Bool → GM (List Val)
  | 0, _, _, _ => throw .fuel
  | n + 1, fv, vs, spread => do
    match fv with
    | .func_ _ none => nilDeref
    | .func_ _ (some (.decl path x targs)) =>
      -- A function a model replaces runs as the model, however it is reached:
      -- qualified, from inside its own package, instantiated or as a value.
      if path == stdPath && libFuncs.contains x then
        -- The modelled function is a call of its own: what it calls runs one level
        -- deeper, as `recover`'s depth check needs.
        modify fun s => { s with defers := [] :: s.defers }
        let pop : GM Unit := modify fun s => { s with defers := s.defers.tail }
        let r ← tryCatch (libCall c n x (← spreadArgs spread vs)) fun e => do pop; throw e
        pop
        return r
      let p ← pkgCtx c path
      match p.objs.get? x with
      | some (.func fi tps sig body) =>
        let targs ← if targs.length == (fieldListNames tps).length then pure targs
          else inferTArgs tps sig targs vs spread
        let tenv ← bindTParams tps targs
        callBody c n { pkg := path, file := fi, tenv, targs } sig body none vs spread
      | _ => stuck s!"{x} is not a function"
    | .func_ _ (some (.method p tn m args cell)) =>
      match methodOf c p tn m with
      | some me => callMethod c n p args me (← loadCell cell) vs spread
      | none => stuck s!"no method {tn}.{m}"
    | .func_ _ (some (.closure id)) =>
      match (← get).closures[id]? with
      | some cl =>
        callBody c n { pkg := cl.pkg, file := cl.file, scopes := cl.scopes, tenv := cl.tenv, targs := cl.targs }
          cl.sig (some cl.body) none vs spread
      | none => stuck "no closure"
    | _ => stuck "call of a non-function"

/-- Call method `m` of type `p.tn[args]` with receiver `recv` (Go spec, "Method
    declarations"): a pointer receiver gets the pointer, a value receiver a copy of
    the value (through a pointer too, Go spec, "Calls"). -/
def callMethod (c : Ctx) : Nat → String → List Ty → Method → Val → List Val → Bool → GM (List Val)
  | 0, _, _, _, _, _, _ => throw .fuel
  | n + 1, p, args, me, recv, vs, spread => do
    let recv ← if me.ptr then pure recv else
      match recv with
      | .ptr _ (some l) => load l
      | .ptr _ none => nilDeref
      | v => pure v
    let tenv := me.recvTParams.zip args
    callBody c n { pkg := p, file := me.file, tenv, targs := args } me.sig me.body (some (me.recvName, recv)) vs spread

/-- Type arguments of a generic function from its arguments' types (Go spec, "Type
    inference"); an untyped constant argument has its default type. Each argument for
    a final `...T` parameter is typed `T`; the slice `f(xs...)` passes, `[]T`. -/
def inferTArgs (tps : List Field) (sig : Sig) (explicit : List Ty) (vs : List Val) (spread : Bool) : GM (List Ty) := do
  let names := fieldListNames tps
  let .mk ps _ variadic := sig
  let ptys := fieldTypes ps
  let pairs := if variadic then
      let k := ptys.length - 1
      let last := ptys.getLast?.getD (.ident "_")
      let lastTy := if spread then Expr.arrayType none last else last
      (ptys.take k).zip vs ++ (vs.drop k).map (lastTy, ·)
    else ptys.zip vs
  -- Typed arguments first (Go spec, "Type inference"). GoCore unifies only identical
  -- shapes: where Go's inexact unification would go through a defined type's
  -- underlying type, or two equations disagree, it reports `type-inference`.
  let typed := pairs.filter (!·.2.ty.isUntyped)
  let eqs := typed.flatMap fun (t, v) => unify names maxUnifyDepth t v.ty
  -- An untyped constant then binds a type parameter to its default type, an untyped
  -- rune's if one of them is a rune; untyped `nil` binds nothing. GoCore does so only
  -- where Go would too: the parameter is in no typed argument's parameter type (which
  -- unification above may have failed to read) and no constraint has a core type that
  -- could fix it (every constraint is `any` or `comparable`).
  let trivial := tps.all fun | .mk _ (.ident "any") | .mk _ (.ident "comparable") => true | _ => false
  let untypedKinds := fun (x : String) => pairs.filterMap fun (t, v) => match t, v.ty with
    | .ident y, .untyped k => if y == x && k != .nil then some k else none
    | _, _ => none
  -- The type arguments given explicitly come first (Go spec, "Instantiations": a
  -- partial type argument list leaves the rest to inference).
  names.zipIdx.mapM fun (x, i) => do
    if let some t := explicit[i]? then return t
    match (eqs.filter (·.1 == x)).map (·.2) with
    | t :: ts => if ts.all (· == t) then pure t else unsupported "type-inference"
    | [] =>
      if !trivial || typed.any (mayMention x maxUnifyDepth ·.1) then unsupported "type-inference"
      let ks := untypedKinds x
      -- The kind later in integer, rune, floating-point.
      let k := if ks.contains .float then some UKind.float else if ks.contains .rune then some UKind.rune else ks.head?
      match k.bind UKind.default with
      | some t => pure t
      | none => unsupported "type-inference"

/-- Run a function body: parameters (and the receiver) in new cells, converted to
    their types; named results in cells holding zero values; the results of its
    `return` (Go spec, "Function declarations", "Return statements"). -/
def callBody (c : Ctx) : Nat → Env → Sig → Option (List Stmt) → Option (String × Val) → List Val → Bool → GM (List Val)
  | 0, _, _, _, _, _, _ => throw .fuel
  | n + 1, env, .mk ps rs variadic, body, recv, vs, spread => do
    tick
    let some body := body | unsupported "func-without-body"
    -- The signature's types are read where the function is declared: the parameters',
    -- receiver's and results' scope is the body (Go spec, "Declarations and scope").
    let sigEnv := env
    let mut env := env.push
    if let some (r, v) := recv then
      env := env.bind r (← alloc v)
    let params := ps.flatMap fun | .mk ns t => if ns.isEmpty then [("_", t)] else ns.map (·, t)
    -- A final `...T` parameter is a `[]T` holding the arguments past the others
    -- (Go spec, "Passing arguments to ... parameters"): nil for none, else a new slice
    -- with a new underlying array of them; `f(xs...)` passes `xs` itself.
    let (params, vs) ← if !variadic then pure (params, vs) else do
      let k := params.length - 1
      let (x, te) := params.getLast?.getD ("_", .ident "_")
      let rest := vs.drop k
      let sv ← if spread then
          match rest with
          | [s] => pure s
          | _ => stuck "... with other arguments for the variadic parameter"
        else
          let et ← evalType c n sigEnv te
          let st := Ty.slice et
          if rest.isEmpty then pure (Val.slice st none)
          else
            let es ← rest.mapM (convert c n et)
            pure (Val.slice st (some (← newArray c n et es.toArray (GcRuntime.variadicCap es.length))))
      pure (params.take k ++ [(x, Expr.arrayType none te)], vs.take k ++ [sv])
    if spread && !variadic then stuck "... for a function that is not variadic"
    if params.length != vs.length then stuck "wrong number of arguments"
    for ((x, te), v) in params.zip vs do
      let t ← evalType c n sigEnv te
      env := env.bind x (← allocAs t (← convert c n t v))
    let results := rs.flatMap fun | .mk ns t => if ns.isEmpty then [("", t)] else ns.map (·, t)
    let rtys ← results.mapM fun (_, te) => evalType c n sigEnv te
    let mut named : List Nat := []
    for ((x, _), t) in results.zip rtys do
      if !x.isEmpty then
        let cell ← allocAs t (← zero c n t)
        env := env.bind x cell
        named := named ++ [cell]
    -- The body runs with a list of deferred calls of its own.
    let depth := (← get).defers.length + 1
    modify fun s => { s with defers := [] :: s.defers }
    let outcome ← tryCatch (do let _ ← execStmts c n env body; pure (Except.ok none)) fun
      | .ret ws => pure (.ok (some ws))
      | .panic v rt => pure (.error (v, rt))
      | e => throw e
    -- A `return`'s values become the results (named ones in their cells) before the
    -- deferred calls run (Go spec, "Return statements", "Defer statements").
    let mut values : List Val := []
    match outcome with
    | .ok none => if !results.isEmpty && named.isEmpty then stuck "missing return"
    | .ok (some []) => pure ()
    | .ok (some ws) =>
      let ws := if ws.length == 1 && rtys.length > 1 then (ws.head!).results else ws
      if ws.length != rtys.length then stuck "wrong number of results"
      let ws ← (rtys.zip ws).mapM fun (t, w) => convert c n t w
      if named.isEmpty then values := ws
      else for (cell, w) in named.zip ws do storeCell cell w
    | .error _ => pure ()
    -- The deferred calls, newest first. While the function panics, each runs with the
    -- panic recoverable by a `recover` it calls directly; a panic in one replaces the
    -- current one (Go spec, "Defer statements", "Handling panics").
    let deferred := ((← get).defers.headD [])
    let mut panic : Option (Val × Bool) := match outcome with | .error p => some p | _ => none
    if !deferred.isEmpty then
      let saved := (← get).recoverable
      for (fv, args, spread) in deferred do
        modify fun s => { s with recoverable := panic.map fun (v, t) => (depth + 1, v, t) }
        let raised ← tryCatch (do let _ ← callValue c n fv args spread; pure none) fun
          | .panic v rt => pure (some (v, rt))
          | e => throw e
        -- `recover` took the panic.
        if panic.isSome && (← get).recoverable.isNone then panic := none
        if let some p := raised then panic := some p
      modify fun s => { s with recoverable := saved }
    modify fun s => { s with defers := s.defers.tail }
    if let some (v, rt) := panic then throw (.panic v rt)
    -- A function that recovered from a panic returns its results as they are: named
    -- ones from their cells, unnamed ones zero.
    if results.isEmpty then pure []
    else if !named.isEmpty then named.mapM loadCell
    else if values.isEmpty then rtys.mapM (zero c n)
    else pure values

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
        let vs ← match values, names with
          | [], _ => pure []
          -- `var v, ok = m[k]` (`rhsValues`).
          | [.index _ [_]], [_, _] => rhsValues c n env 2 values
          | _, _ => evalArgs c n env values
        let t? ← te.mapM (evalType c n env)
        for (x, i) in names.zipIdx do
          let v ← declValue c n t? vs[i]?
          env := env.bind x (← allocAs (t?.getD v.ty) v)
      pure env
    -- Local constants, as package-level ones (`Prog`): a spec without a type and
    -- values repeats the previous one's, each spec's `iota` is its index, a typed one
    -- is converted. The name holds the constant's value, untyped or typed, which no
    -- compiled program assigns or takes the address of (Go spec, "Constant
    -- declarations").
    | .decl (.value true specs) =>
      let mut env := env
      let mut prev : Option Expr × List Expr := (none, [])
      for (.mk names ty values, iota) in specs.zipIdx do
        let (t, vs) := if ty.isNone && values.isEmpty then prev else (ty, values)
        prev := (t, vs)
        let cenv := { env with iota := some iota }
        for (x, k) in names.zipIdx do
          let some ve := vs[k]? | stuck s!"constant {x} without a value"
          let v ← evalExpr c n cenv ve
          let v ← match t with
            | some te => conversion c n (← evalType c n cenv te) v
            | none => pure v
          env := env.bind x (← allocAs v.ty v)
      pure env
    -- Local types (Go spec, "Type declarations", "Declarations and scope"). Each spec's
    -- name is in scope from the spec on, in it too, so a type can refer to itself, and a
    -- later spec of the declaration is not yet in scope for an earlier one. It shadows
    -- every outer name, variables too. A type declaration denotes one type wherever and
    -- however often it runs, and in a generic function one per instantiation: the type is
    -- named by its site (`localTypeName`) and takes the function's type arguments.
    | .localType specs =>
      let mut env := env
      for (.mk x _ alias te, site) in specs do
        let bindName (t : Ty) (e : Env) : Env :=
          { e with tenv := (x, t) :: e.tenv, scopes := e.scopes.map (·.filter (·.1 != x)) }
        if alias then
          env := bindName (← evalType c n env te) env
        else
          let name := localTypeName x env.file site
          env := bindName (.named env.pkg name env.targs) env
          -- Its definition is evaluated where it is written: the local constants there
          -- included (`[n]T`), whose cells never change.
          if ((← get).localType? env.pkg name env.targs).isNone then
            let lt : LocalType := { pkg := env.pkg, name, targs := env.targs, file := env.file,
                                    scopes := env.scopes, tenv := env.tenv, ty := te }
            modify fun s => { s with localTypes := lt :: s.localTypes }
      pure env
    | .decl (.type _) => stuck "a type declaration in a function body outside `localType`"
    | .decl (.func ..) | .decl (.unsupported _) => unsupported "local-decl"
    | .incDec op x => do
      let p ← evalPlace c n env x
      let v ← loadPlace c n p
      storePlace c n p (← binary c n (if op == "++" then "+" else "-") v (.int (.untyped .int) 1))
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
    | .range k v tok x body => do rangeLoop c n env k v tok x body; pure env
    | .defer_ (.call fn args spread) => do deferCall c n env fn args spread; pure env
    | .defer_ _ => stuck "defer of a non-call"
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

/-- `for k, v := range x { body }` (Go spec, "For statements with range clause"), over
    a slice, an array (through a pointer too), a string or an integer. `x` is evaluated
    once; a slice's elements, or a pointed-to array's, are read as each iteration
    reaches them, an array value's from its copy. A string's iterations are its runes:
    the byte index and the rune. With `:=`, each iteration has its own variables (Go
    1.22); with `=`, the iteration values are assigned to `k` and `v`. -/
def rangeLoop (c : Ctx) : Nat → Env → Option Expr → Option Expr → String → Expr → List Stmt → GM Unit
  | 0, _, _, _, _, _, _ => throw .fuel
  | n + 1, env, k, v, tok, x, body => do
    -- With one iteration variable, Go does not evaluate an array (or pointer to one)
    -- whose length is constant (Go spec, "For statements with range clause"); GoCore
    -- does not know the static type, so a panic evaluating it ends the run instead.
    -- `range []byte(s)` ranges over a new array of the string's bytes, which nothing
    -- else can reach, so its capacity is never observed (Go spec, "Conversions to and
    -- from a string type"), of any string.
    let bytesOf? : GM (Option Val) := do
      let .call fn [a] false := x | return none
      let some t ← typeOf? c n env fn | return none
      let .slice et ← underlying c n t | return none
      let .int .uint8 ← underlying c n et | return none
      let av ← evalExpr c n env a
      let .str _ s := av | return some (← conversion c n t av)
      return some (← stringBytes c n t et s)
    let xv ← match ← bytesOf? with
      | some bv => pure bv
      | none => if (v.map (· matches .ident "_")).getD true then constantOperand c n env x else evalExpr c n env x
    let intVal := fun (i : Nat) => Val.int (.int .int) i
    -- The number of iterations, and the key and value of each.
    let (count, item) : Nat × (Nat → GM (Val × Val)) ← match xv with
      | .slice _ none => pure (0, fun i => pure (intVal i, .tuple []))
      | .slice _ (some r) => pure (r.len, fun i => do pure (intVal i, ← load (r.loc i)))
      | .array _ es => pure (es.size, fun i => pure (intVal i, es[i]!))
      | .ptr _ (some pl) => do
        match ← load pl with
        | .array _ es => pure (es.size, fun i => do pure (intVal i, ← load ⟨pl.cell, pl.path ++ [i]⟩))
        | _ => stuck "range over a pointer to a non-array"
      | .str _ s =>
        let runes := (s.toList.foldl (fun (acc : List (Nat × Char) × Nat) ch =>
          ((acc.2, ch) :: acc.1, acc.2 + ch.utf8Size)) ([], 0)).1.reverse.toArray
        pure (runes.size, fun i =>
          let (off, ch) := runes[i]!
          pure (intVal off, .int (.int .int32) ch.toNat))
      | .int _ m => do
        let t := (defaultVal xv).ty
        pure (m.toNat, fun i => pure (.int t i, .tuple []))
      -- A map's iteration order is not specified, and gc's differs from run to run (Go
      -- spec, "For statements with range clause"): a map of two or more entries ends
      -- the run, as no order can be told. A nil or empty one ranges zero times, one of
      -- a single entry once.
      | .map _ none => pure (0, fun _ => pure (.tuple [], .tuple []))
      | .map _ (some m) => do
        let es := (← getMap m).entries
        if es.size > 1 then unsupported "map-range-order"
        pure (es.size, fun _ => pure (es.fold (fun _ _ e => e) (.tuple [], .tuple [])))
      | v => unsupported s!"range-{v.ty}"
    -- An entry added to a map while it is ranged over "may be produced during the
    -- iteration or may be skipped" (same section): a run that adds one, deleted
    -- before or not, and goes on ranging ends.
    let added? ← match xv with
      | .map _ (some m) => pure (some (m, (← getMap m).added))
      | _ => pure none
    for i in [0:count] do
      tick
      let (kv, vv) ← item i
      let mut env' := env.push
      for (e?, w) in [(k, kv), (v, vv)] do
        match e?, tok with
        | none, _ | some (.ident "_"), _ => pure ()
        | some (.ident y), ":=" => env' := env'.bind y (← alloc w)
        | some e, "=" => storePlace c n (← evalPlace c n env e) w
        | some _, _ => stuck s!"range with {tok}"
      let r ← tryCatch (do execBlock c n env' body; pure true) fun
        | .brk => pure false
        | .cont => pure true
        | e => throw e
      if !r then break
      if let some (m, a) := added? then
        if (← getMap m).added != a then unsupported "map-range-insert"

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
      -- An index expression's operands are evaluated with the rest, its bounds checked
      -- when its assignment is carried out (Go spec, "Assignment statements": `x[1],
      -- x[3] = 4, 5` sets `x[1]`, then panics).
      -- A nil map's entry panics when it is stored (`mapStore`).
      let targets ← lhs.mapM fun x => match x with
        | .ident "_" => pure none
        | x => do pure (some (← evalPlace c n env x))
      let vs ← rhsValues c n env lhs.length rhs
      for (t, v) in targets.zip vs do
        if let some p := t then storePlace c n p v
      return env
    -- `x op= y`: `x` is evaluated once.
    let op := String.ofList tok.toList.dropLast
    let [x] := lhs | stuck s!"{tok} with several targets"
    let [y] := rhs | stuck s!"{tok} with several values"
    let p ← evalPlace c n env x
    -- `m[k] op= y` on a map entry: gc rewrites `/` and `%` to `m[k] = m[k] op y`, so a
    -- division by zero panics before any store; for every other operator it takes the
    -- entry's slot first (`mapassign`, which panics for a nil map and adds the key with
    -- the zero element), then computes `op` (cmd/compile order.go `OASOP`, walk
    -- `walkAssign`; with the pinned go run, `m["new"] <<= s` for `s < 0` leaves
    -- `map[new:0]` and panics). Whether `y` is evaluated before or after the slot is
    -- taken shows only when `y` panics and the slot is new: such a run stops.
    if let .entry r key k t := p then
      if op != "/" && op != "%" then
        let fresh ← match r with
          | none => pure true
          | some i => pure !((← getMap i).entries.contains k)
        let b ← tryCatch (evalExpr c n env y) fun
          | e@(.panic ..) => if fresh then unsupported "map-op-assign-order" else throw e
          | e => throw e
        if fresh then mapStore r key k (← zero c n t)
        let a ← loadPlace c n p
        storePlace c n p (← binary c n op a b)
        return env
    let a ← loadPlace c n p
    let b ← evalExpr c n env y
    storePlace c n p (← binary c n op a b)
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
    -- `v, ok := m[k]`: the element and whether the key is in the map (Go spec, "Index
    -- expressions"); only a map's index has two values.
    | [.index x [i]] =>
      if k != 2 then return [← evalExpr c n env rhs.head!]
      let v ← evalExpr c n env x
      let iv ← evalExpr c n env i
      let .map t r := v | stuck "v, ok = x[i] of a non-map"
      let (_, key, et) ← mapKey c n t iv
      let (e, ok) ← mapIndex c n r key et
      return [e, .bool (.untyped .bool) ok]
    | [e] =>
      let v ← evalExpr c n env e
      let vs := v.results
      if vs.length != k then stuck "assignment count mismatch"
      return vs
    | es => es.mapM (evalExpr c n env)

-- Library models

/-- `fmt` (fmt package documentation; `fmt/print.go`). `Println` prints its operands
    with `%v`, always separated by spaces, and a newline; `Print` puts a space between
    two operands when neither is a string; `Sprint` is `Print` into a string;
    `Sprintf` and `Printf` with the verbs `%v %d %s %t %x %X %%` and the flags,
    width and precision `fmtSpec` models. -/
def fmtCall (c : Ctx) : Nat → String → List Val → GM (List Val)
  | 0, _, _ => throw .fuel
  | n + 1, f, vs => do
    -- `Println`, `Print` and `Printf` write their text and return its length in bytes
    -- and a nil error.
    let printed := fun (s : String) => do
      emit s
      pure [Val.int (.int .int) s.utf8ByteSize, .nilIface errorTy]
    match f with
    | "Println" =>
      let parts ← vs.mapM fun v => fmtArg c n v 'v'
      printed (" ".intercalate parts ++ "\n")
    | "Print" => printed (← sprint c n vs)
    | "Sprint" => pure [.str .string (← sprint c n vs)]
    | "Sprintf" =>
      let fmtStr :: args := vs | stuck "Sprintf without a format"
      pure [.str .string (← sprintf c n (← asStr fmtStr).toList args)]
    | "Printf" =>
      let fmtStr :: args := vs | stuck "Printf without a format"
      printed (← sprintf c n (← asStr fmtStr).toList args)
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
    | '%' :: rest =>
      let (spec, rest) := parseSpec rest
      -- Go refuses a width or precision over 10^6 (`parsenum`'s `tooLarge`).
      if spec.wid.any (· > 1000000) || spec.prec.any (· > 1000000) then unsupported "fmt-large-width"
      let verb :: rest := rest | unsupported "fmt-no-verb"
      if !['v', 'd', 's', 't', 'x', 'X', 'e', 'E', 'f', 'F', 'g', 'G'].contains verb then
        unsupported s!"fmt-verb-{verb}"
      let a :: args := args | unsupported "fmt-missing-arg"
      pure ((← fmtSpec c n a verb spec) ++ (← sprintf c n rest args))
    | ch :: rest => pure (String.singleton ch ++ (← sprintf c n rest args))

/-- One operand printed with `verb` under a flags, width and precision `spec`
    (`fmt/print.go`, `printArg`, `handleMethods`; `fmt/format.go`). Without any, as
    `fmtArg`. A formatter of the standard library (`stdFormatted?`, which passes the
    whole spec on) comes first; an `Error` or `String` method gives the text for `%v` and `%s`, which is
    then formatted as a string; integers, booleans and strings are formatted as
    `fmtInteger`, `fmtBoolean` and `fmtS` do, floats as `fmtFloat` does (`%e %E %f %F
    %g %G %v`; `FmtSpec.float`, `FloatRep.formatVerb`). A flag on a composite value,
    `%+v` and `%#v`, `#` on a float, and `%x` of anything but an integer are not
    modelled. -/
def fmtSpec (c : Ctx) : Nat → Val → Char → FmtSpec → GM String
  | 0, _, _, _ => throw .fuel
  | n + 1, v, verb, spec => do
    let v := v.materialize
    if spec.plain && ['v', 's', 'd', 't'].contains verb then return ← fmtArg c n v verb
    if verb == 'v' && (spec.sharp || spec.plus) then unsupported "fmt-flag-v"
    -- A nil operand: `padString("<nil>")`, padded, never truncated.
    if let .nilIface _ := v then
      return ← if verb == 'v' then pure (spec.pad "<nil>") else unsupported "fmt-nil-verb"
    let d := v.dyn
    if let some inner ← stdFormatted? c d then return ← fmtSpec c n inner verb spec
    -- `handleMethods`: an `error` or `fmt.Stringer` gives the text for `%v %s %x %X`.
    let stringMethod := fun m => do
      pure (← methodOfType c n d.ty m (.func_ [] [.string] false)).isSome
    if (← stringMethod "Error") || (← stringMethod "String") then
      if verb == 'x' || verb == 'X' then unsupported "fmt-verb-x-string"
      if verb == 'v' || verb == 's' then
        -- A nil pointer whose method panics prints a bare `<nil>` (`catchPanic`).
        if let .ptr _ none := d then unsupported "fmt-nil-method"
        return spec.string (← fmtArg c n d verb)
    match d with
    | .int _ x =>
      match verb with
      | 'd' | 'v' => pure (spec.integer x 10 false)
      | 'x' => pure (spec.integer x 16 false)
      | 'X' => pure (spec.integer x 16 true)
      | _ => unsupported s!"fmt-verb-{verb}-int"
    | .bool _ b =>
      if verb == 'v' || verb == 't' then pure (spec.pad (if b then "true" else "false"))
      else unsupported "fmt-verb-bool"
    | .str _ s =>
      if verb == 'v' || verb == 's' then pure (spec.string s) else unsupported s!"fmt-verb-{verb}-string"
    -- A float (`printArg`'s `fmtFloat`): `%v` is `%g`; `%e %f` have precision 6 unless
    -- given, `%g` its shortest digits; `%F` is `%f`, and `%E %G` write `E`.
    | .float t x =>
      if spec.sharp then unsupported "fmt-flag-sharp-float"
      let lower := match verb with | 'v' | 'G' => 'g' | 'E' => 'e' | 'F' => 'f' | v => v
      if !['e', 'f', 'g'].contains lower then unsupported s!"fmt-verb-{verb}-float"
      let .float size ← underlying c n t | stuck "a float of a non-float type"
      let (neg, special, body) := FloatRep.formatSized size x lower spec.prec
      let body := if (verb == 'E' || verb == 'G') && !special then body.map Char.toUpper else body
      pure (spec.float neg special body)
    | _ => unsupported "fmt-spec-composite"

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
      -- A Formatter, before `Error` and `String`, by the dynamic value: those of the
      -- standard library print another value with the same verb (`stdFormatted?`).
      if let some inner ← stdFormatted? c v then return ← fmtArg c n inner verb
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
    -- A float prints as `strconv.FormatFloat(x, 'g', -1, size)` (`fmtFloat`).
    | .float t x =>
      if verb != 'v' then unsupported s!"fmt-verb-{verb}-float"
      let .float size ← underlying c n t | stuck "a float of a non-float type"
      pure (FloatRep.format size x)
    | .rat .. => fmtValue c n v.materialize verb depth canIface
    -- A slice or array prints its elements in brackets (`printValue`'s
    -- `reflect.Array, reflect.Slice`).
    | .slice .. | .array .. =>
      if verb != 'v' then unsupported "fmt-verb-slice"
      let es ← match v with
        | .array _ es => pure es.toList
        | _ => sliceElems v
      let parts ← es.mapM fun e => fmtValue c n e verb (depth + 1) canIface
      pure ("[" ++ " ".intercalate parts ++ "]")
    -- A map prints `map[k:v ...]`, its entries sorted by key (`printValue`'s
    -- `reflect.Map`, `fmtsort.Sort`; `MapKey.lt`), each key and element printed with the
    -- same verb; a nil map prints `map[]`.
    | .map _ r =>
      let es ← match r with
        | none => pure []
        | some i => pure (← getMap i).entries.toList
      let parts ← (es.mergeSort fun a b => !b.1.lt a.1).mapM fun (_, (k, e)) => do
        pure ((← fmtValue c n k verb (depth + 1) canIface) ++ ":" ++
          (← fmtValue c n e verb (depth + 1) canIface))
      pure ("map[" ++ " ".intercalate parts ++ "]")
    | .ptr _ none => pure "<nil>"
    | .ptr _ (some l) =>
      let w ← load l
      if depth == 0 then
        match w with
        | .struct_ .. | .slice .. | .array .. | .map .. =>
          return "&" ++ (← fmtValue c n w verb (depth + 1) canIface)
        | _ => pure ()
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
    `Immutable.Format` and `(*panicError).Format` (`stdFormatted?`) they are the whole
    list. They are trusted, like
    the `fmt` model, and the differential run against `go run` is what tests them. -/
def libCall (c : Ctx) : Nat → String → List Val → GM (List Val)
  | 0, _, _ => throw .fuel
  | n + 1, f, vs => do
    match f, vs with
    -- `EvalOperand[T](v T) T { return v }` (std/types.go, line 114).
    | "EvalOperand", [v] => pure [v]
    -- `recoveredError(r any) error` (std/types.go, lines 318-335), the error a Try
    -- Failure holds for a recovered panic: a `*panicError` as it is; else a new
    -- `*panicError` of the error, of `errors.New(s)` for a string, or (not modelled)
    -- of `fmt.Errorf("panic: %v", r)`. Its `pcs`, the stack `runtime.Callers` records,
    -- are left nil: only `(*panicError).stack` reads them, through
    -- `runtime.CallersFrames`, which GoCore does not run (`TestRecoveredErrorShape` and
    -- `TestPanicErrorPcsReaders` in internal/formal/gocoreexport pin both).
    | "recoveredError", [r] =>
      let d := r.dyn
      let pt : Ty := .named stdPath "panicError" []
      if d.ty == .ptr pt then return [← convert c n errorTy d]
      let err ← if (← methodOfType c n d.ty "Error" (.func_ [] [.string] false)).isSome then pure d
        else match d with
          | .str .string _ =>
            if !c.pkgs.contains "errors" then unsupported "errors.New"
            Val.single (← callValue c n (← funcVal c n "errors" "New" []) [d] false)
          | _ => unsupported "fmt.Errorf"
      -- `err` is the first field (TestRecoveredErrorShape), as `stdFormatted?` reads it.
      let .struct_ _ (_ :: rest) ← zero c n pt | stuck "panicError is not a struct"
      let vals := (← convert c n errorTy err) :: rest
      let cell ← alloc (.struct_ pt vals)
      return [← convert c n errorTy (.ptr (.ptr pt) (some ⟨cell, []⟩))]
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
      | .int _ | .bool | .string | .float _ => pure [.bool .bool (← equal c n a b)]
      | _ =>
        -- `Equatable[T]`: `Equal(T) bool` exactly.
        match ← methodOfType c n a.ty "Equal" (.func_ [a.ty] [.bool] false) with
        | some (p, args, me) => callMethod c n p args me a [b] false
        | none => unsupported "std.Equal-reflective"
    -- `Copy[T](v T) T` (std/types.go, `Copy`, `copiesAsIs`, `copiesPointee`): a nil
    -- pointer is returned as it is, and so is a pointer whose pointee's (value-receiver)
    -- `Copy` does not return the pointer type; else a `Copyable[T]`, with `Copy() T`
    -- exactly, is copied by it; a predeclared scalar is returned as it is (`copyAny`);
    -- the reflective rest is unsupported.
    | "Copy", [v] =>
      match v with
      | .ptr _ none | .nilIface _ => pure [v]
      | _ =>
        let copiesPointee ← match v with
          | .ptr t _ => do
            let .ptr pt ← underlying c n t | stuck "a pointer of a non-pointer type"
            match methodSet c pt "Copy" with
            | some _ => pure (← methodOfType c n v.ty "Copy" (.func_ [] [v.ty] false)).isNone
            | none => pure false
          | _ => pure false
        if copiesPointee then return [v]
        match ← methodOfType c n v.ty "Copy" (.func_ [] [v.ty] false) with
        | some (p, args, me) => callMethod c n p args me v [] false
        | none =>
          match v with
          | .int (.int _) _ | .bool .bool _ | .str .string _ | .float (.float _) _ => pure [v]
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
