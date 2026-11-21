/-
  # GoCore: gc's order of evaluation (Go 1.25.5)

  The Go spec orders the calls, method calls and receives of an expression among
  themselves, left to right, and leaves every other operand unordered against them (Go
  spec, "Order of evaluation"). GoCore's evaluator (`Eval.lean`) evaluates every operand
  list left to right. gc does not: its order pass (cmd/compile/internal/walk/order.go)
  copies some operands into temporaries before the statement, in lexical post-order, and
  evaluates the rest after them. So `a + f()`, with `f` changing `a`, is `a`'s new value
  plus `f()` under gc. This file models that pass as a rewrite of the program, applied
  before the run: the evaluator stays as it is, and the order is a parameter of the run
  (`Order`, `runProgramAt`).

  This is **gc 1.25.5's choice, not the spec's**, kept apart like `GcRuntime` and checked
  by the same version guard (`GcRuntime.goVersion`). It is pinned by the `go run`
  fixtures `eval_order`, `eval_order2`, `eval_order3`, `eval_order4` and `eval_order_call`
  (tests/gocore/src).

  What order.go copies into a temporary at its lexical place (an *O-node*):
  * a call of a function, method or function value (`OCALLFUNC`, `OCALLINTER`);
  * a builtin call: `len cap new make min max complex real imag append copy recover`;
  * a slice expression `x[i:j]` (`OSLICE`);
  * a type assertion to a type that is not pointer-shaped (`ODOTTYPE`; one to `*T` or a
    function type is read late, and one to a type whose shape the syntax does not show
    is ambiguous, below);
  * the conversions `[]byte(s)` and `[]rune(s)` of a string (`OSTR2BYTES`, `OSTR2RUNES`);
  * `x && y` and `x || y`, as `r := x; if r { r = y }` with `y`'s own copies inside the
    `if` (`OANDAND`, `OOROR`).
  Every other operand is evaluated where the statement is executed, after all of them:
  variables, fields, index expressions, arithmetic, `+` on strings, other conversions.
  A call's own operands are evaluated when the call is: `a + g(a, f())` is `f()`, then
  `g` reading `a`, then `a`.

  Not every statement is rewritten: only one where the two orders can be told apart
  (`triggers`). They run the O-nodes in the same order, so they differ only in when a
  late operand is read (before a later O-node under left to right, after it under gc)
  and in which of two panics comes first. A statement is rewritten when, in the same
  level, a late operand that reads state a call can change, or that can panic, comes
  before an O-node that can change state (a call, `append`, `copy`), or a late operand
  that can panic comes before an O-node that can panic with another text. On every other
  statement the two orders give the same outcome and output; this is the equivalence a
  later M5 step owes as a lemma, `untriggered_orders_agree` (docs/FORMAL_SEMANTICS.MD,
  "Evaluation order"), tested meanwhile by the differential run. The classifiers are
  conservative: a read is "changeable" unless it is a constant, a function, a type, or a
  local that no function literal assigns, whose address is not taken (by `&`, by slicing
  it, or by a method with a pointer receiver, called or as a method value; a call of a
  method no declaration names counts too).

  gc's choice depends on static types in one place, which GoCore's syntax does not
  carry: a value converted to an interface whose type the runtime's conversion takes by
  address (`addrTemp`: `bool`, bytes, structs, arrays, pointers) is copied at its place
  when it is not addressable or is a local gc keeps in registers, and read late
  otherwise; a value of interface type is never copied (a local declared with one is read
  late). A place converts a value only if its declared type is an interface: the
  rewrite reads the parameter types of a declared function or of a local of function
  type, and the field or element types of a composite literal, and counts every other
  place as one that may. Where the copy can be observed (such a value in a place that
  may convert it, reading changeable state, before an O-node that can change state), the
  run stops `unsupported "eval-order-interface-conversion"` rather than guess. One such
  case is certain and modelled: a comparison or negation passed to a `fmt` function is
  a `bool` converted to `any`, copied at its place. The run also stops where a rewrite is
  needed but the statement's place cannot hold statements before it: a `for` condition,
  a `switch` case, a package-level variable's initializer, and a call of unknown arity as
  the only argument of a call whose function part reads changeable state.
-/
import Gala.GoCore.Eval

namespace Gala.GoCore.Full

/-- An order of evaluation for operands, as a rewrite of the program before it runs:
    `leftToRight` is GoCore's evaluator as it is, `gc` is gc 1.25.5's order pass. -/
abbrev Order := Program → Program

/-- The names `:=` declares. -/
def declaredBy : List Expr → List String
  | [] => []
  | .ident x :: es => x :: declaredBy es
  | _ :: es => declaredBy es

/-- The names a signature binds: its parameters and results. -/
def sigNames : Sig → List String
  | .mk ps rs _ => fieldListNames ps ++ fieldListNames rs

/-- The names of the fields of `ps` declared `*T`: locals of pointer type. -/
def pointerNames (ps : List Field) : List String :=
  ps.flatMap fun | .mk ns (.star _) => ns | _ => []

namespace Hoist

/-- What the rewrite knows where an expression is: the package and file (for names and
    imports), the locals in scope, the type parameters, and the locals a call may change. -/
structure Scope where
  c : Ctx
  pkg : String
  file : Nat
  locals : List String := []
  tparams : List String := []
  /-- The local types in scope (`Stmt.localType`), by name, newest first. -/
  ltypes : List (String × TypeSpec) := []
  changeable : Std.HashSet String := {}
  /-- Locals of pointer type (parameters declared `*T`), not shadowed since. -/
  pointers : List String := []
  /-- Locals of function type (parameters declared `func(...) ...`), with their signatures,
      not shadowed since. -/
  funcTypes : List (String × Sig) := []
  /-- Locals of interface type (parameters and `var`s declared so), not shadowed since. -/
  ifaces : List String := []
  /-- The names of every struct field of the program. -/
  fields : Std.HashSet String := {}
  /-- The methods of the program, by name. -/
  methods : Std.HashMap String (List Method) := {}

namespace Scope

def env (s : Scope) : Env := { pkg := s.pkg, file := s.file }

def isLocal (s : Scope) (x : String) : Bool := s.locals.contains x

/-- The spec of the local type a name stands for, if it stands for one. -/
def ltype? (s : Scope) (x : String) : Option TypeSpec := s.ltypes.lookup x

def isLType (s : Scope) (x : String) : Bool := (s.ltype? x).isSome

/-- The package-level declaration a bare name stands for, if it is not local. -/
def obj? (s : Scope) (x : String) : Option Obj :=
  if s.isLocal x || s.tparams.contains x || s.isLType x then none
  else (nameOwner s.c s.env x).bind fun p => (s.c.pkgs.get? p).bind (·.objs.get? x)

/-- The package an import name stands for. -/
def import? (s : Scope) (q : String) : Option String :=
  if s.isLocal q || s.isLType q then none else importOf s.c s.env q

/-- The declaration `q.x` stands for, `q` an import. -/
def qualified? (s : Scope) (q x : String) : Option Obj :=
  (s.import? q).bind fun p => (s.c.pkgs.get? p).bind (·.objs.get? x)

def bind (s : Scope) (xs : List String) : Scope :=
  { s with locals := xs ++ s.locals, ltypes := s.ltypes.filter fun (x, _) => !xs.contains x,
           pointers := s.pointers.filter (!xs.contains ·),
           funcTypes := s.funcTypes.filter fun (x, _) => !xs.contains x,
           ifaces := s.ifaces.filter (!xs.contains ·) }

/-- A local type declared: it shadows every local of its name. `bind` and `bindType`
    shadow each other, and filter the same fields. -/
def bindType (s : Scope) (spec : TypeSpec) : Scope :=
  let .mk x .. := spec
  { s with ltypes := (x, spec) :: s.ltypes, locals := s.locals.filter (· != x),
           tparams := s.tparams.filter (· != x),
           pointers := s.pointers.filter (· != x), funcTypes := s.funcTypes.filter (·.1 != x),
           ifaces := s.ifaces.filter (· != x) }

/-- The scope of a function body: its signature's names bound, and its parameters of
    pointer and function type recorded. -/
def enter (s : Scope) (sig : Sig) : Scope :=
  let .mk ps _ _ := sig
  let s := s.bind (sigNames sig)
  { s with
    pointers := pointerNames ps ++ s.pointers,
    funcTypes := ps.flatMap (fun | .mk ns (.funcType fs) => ns.map (·, fs) | _ => []) ++ s.funcTypes }

end Scope

def isTypeObj : Option Obj → Bool
  | some (.type ..) => true
  | _ => false

/-- Whether an expression denotes a type, as `typeOf?` decides it: what makes `T(x)` a
    conversion and not a call. -/
def isType (s : Scope) : Expr → Bool
  | .ident x =>
    if s.isLocal x then false
    else if s.tparams.contains x || s.isLType x then true
    else match nameOwner s.c s.env x with
      | some _ => isTypeObj (s.obj? x)
      | none => (universeType? x).isSome
  | .selector (.ident q) x => isTypeObj (s.qualified? q x)
  | .index (.ident t) _ => isTypeObj (s.obj? t)
  | .index (.selector (.ident q) t) _ => isTypeObj (s.qualified? q t)
  | .star x => isType s x
  | .paren x => isType s x
  | .structType _ | .interfaceType _ | .funcType _ | .arrayType .. | .mapType .. => true
  | _ => false

/-- Whether `x[args]` instantiates a generic function or type (no index is read). -/
def isInstantiation (s : Scope) : Expr → Bool
  | .ident f => match s.obj? f with
    | some (.func ..) | some (.type ..) => true
    | _ => false
  | .selector (.ident q) f => match s.qualified? q f with
    | some (.func ..) | some (.type ..) => true
    | _ => false
  | _ => false

/-- What a call `fn(args)` is, as `evalCall` decides it. -/
inductive CallKind
  | conversion
  | builtin (name : String)
  | call

def callKind (s : Scope) (fn : Expr) : CallKind :=
  if isType s fn then .conversion
  else match fn with
    | .ident b => if !s.isLocal b && (nameOwner s.c s.env b).isNone then .builtin b else .call
    | _ => .call

/-- The builtins that change no state: `append` and `copy` write into arrays, `recover`
    ends a panic, and the rest are statements. -/
def pureBuiltins : List String := ["len", "cap", "new", "make", "min", "max", "complex", "real", "imag"]

/-- `[]byte` or `[]rune`: the conversions of a string gc copies. -/
def isByteSliceType (s : Scope) : Expr → Bool
  | .paren t => isByteSliceType s t
  | .arrayType none (.ident e) =>
    !s.isLocal e && !s.isLType e && (nameOwner s.c s.env e).isNone && match universeType? e with
      | some (.int k) => k == .uint8 || k == .int32
      | _ => false
  | _ => false

def isStrLit : Expr → Bool
  | .strLit _ => true
  | .paren x => isStrLit x
  | _ => false

/-- An expression that is a string whatever the types: a literal, or `+` with one. -/
def isStringShaped : Expr → Bool
  | .strLit _ => true
  | .paren x => isStringShaped x
  | .binary "+" x y => isStrLit x || isStrLit y
  | _ => false

def isVarObj : Option Obj → Bool
  | some (.var ..) => true
  | _ => false

/-- Whether a call may change what reading `x` gives: a package-level variable, or a
    local a function literal assigns, whose address is taken or that receives a method
    call. -/
def changeableName (s : Scope) (x : String) : Bool :=
  if s.isLocal x then s.changeable.contains x else isVarObj (s.obj? x)

/-- How an operand can panic: the text of a division by zero, a nil dereference and a
    negative shift is fixed; any other panic's text depends on values. -/
inductive PK
  | div
  | nil
  | shift
  | text
  deriving BEq

/-- Whether two panics may print differently. -/
def PK.differs (a b : PK) : Bool := a == .text || !(a == b)

/-- What evaluating a statement's operands does, in gc's order, as the rewrite's
    trigger reads it. A list of events is one level: the operands evaluated together,
    when the statement is executed (the top level) or when their O-node is. -/
inductive Ev
  /-- A late operand: whether a call may change what it reads, how it may panic. -/
  | read (changeable : Bool) (panics : Option PK)
  /-- A value whose evaluation time gc decides by its static type (`why`). -/
  | slot (why : String)
  /-- An O-node, at its place: whether it or anything in it may change state, how it
      or anything in it may panic, and its own level. -/
  | node (effect : Bool) (panics : List PK) (inner : List Ev)

def innerEffect (evs : List Ev) : Bool := evs.any fun | .node e _ _ => e | _ => false

def innerPanics (evs : List Ev) : List PK :=
  evs.flatMap fun | .read _ (some p) => [p] | .node _ ps _ => ps | _ => []

/-- An O-node over its level's events: it may change state if `effect` or something in
    it may, and panic as `panics` says or as something in it may. -/
def mkNode (effect : Bool) (panics : List PK) (inner : List Ev) : Ev :=
  .node (effect || innerEffect inner) (panics ++ innerPanics inner) inner

def readsChangeable (evs : List Ev) : Bool := evs.any fun | .read true _ => true | _ => false

def isNonzeroLit : Expr → Bool
  | .intLit v | .charLit v => v != 0
  | .paren x => isNonzeroLit x
  | _ => false

def isLit : Expr → Bool
  | .intLit _ | .charLit _ => true
  | .paren x => isLit x
  | _ => false

/-- Whether gc copies a type assertion to `t` at its place: not to a pointer-shaped type
    (`types.IsDirectIface`), whose value is the interface's data word. -/
inductive Shape
  | late
  | copied
  | unknown
  deriving BEq

/-- The shape of a type written `te`: pointers and functions are pointer-shaped;
    interfaces, slices, strings, numbers, booleans and structs of several fields are
    not; a struct of one field or an array is as its element is, and a named type as
    its definition, which this does not look through. -/
def shapeOfTypeExpr : Expr → Shape
  | .star _ | .funcType _ => .late
  | .interfaceType _ | .arrayType none _ => .copied
  | .structType fs => if (fieldNames fs).length == 1 then .unknown else .copied
  | _ => .unknown

def assertShape (s : Scope) : Expr → Shape
  | .paren t => assertShape s t
  | .ident x =>
    if s.isLocal x || s.tparams.contains x then .unknown
    else if let some (.mk _ _ alias te) := s.ltype? x then (if alias then .unknown else shapeOfTypeExpr te)
    else match nameOwner s.c s.env x with
      | none => if (universeType? x).isSome then .copied else .unknown
      | some _ => match s.obj? x with
        | some (.type _ (.mk _ _ _ te)) => shapeOfTypeExpr te
        | _ => .unknown
  | .selector (.ident q) x => match s.qualified? q x with
    | some (.type _ (.mk _ _ _ te)) => shapeOfTypeExpr te
    | _ => .unknown
  | t => shapeOfTypeExpr t

/-- An O-node: what gc copies into a temporary at its place. -/
def isONode (s : Scope) : Expr → Bool
  | .paren x => isONode s x
  | .call fn args _ =>
    match callKind s fn with
    | .conversion => isByteSliceType s fn && args.all isStringShaped
    | _ => true
  | .typeAssert _ t => assertShape s t != .late
  | .sliceExpr .. | .unary "<-" _ => true
  | .binary op _ _ => op == "&&" || op == "||"
  | _ => false

/-- The root of a field path, read late by gc whatever its type: a package-level
    variable (addressable, not in a register), a local no call changes, a
    dereference, a temporary. -/
def lateRoot (s : Scope) : Expr → Bool
  -- A value of interface type is never copied for a conversion (order.go returns early).
  | .ident x => !(s.isLocal x && s.changeable.contains x) || s.ifaces.contains x
  -- A field through a pointer is addressable, whatever the pointer variable is.
  | .selector (.ident p) _ => s.pointers.contains p || !(s.isLocal p && s.changeable.contains p)
  -- So is a field through an assertion to a pointer type.
  | .selector (.typeAssert _ (.star _)) _ => true
  | .selector x _ | .paren x => lateRoot s x
  | .star _ => true
  | .call fn _ _ => !isType s fn
  | _ => false

/-- A value gc reads late however it converts it: a field path from `lateRoot`, a
    string, a slice literal, an O-node's temporary. -/
def certainlyLate (s : Scope) (e : Expr) : Bool :=
  isONode s e || isStringShaped e || match e with
    | .ident _ | .selector .. | .star _ | .paren _ => lateRoot s e
    | .composite (some (.arrayType none _)) _ => true
    | _ => false

/-- The slot event of a value `e` in a place that may convert it to an interface. -/
def slotEv (s : Scope) (e : Expr) (evs : List Ev) : List Ev :=
  match e with
  | .keyValue .. => []
  | _ => if readsChangeable evs && !certainlyLate s e then [.slot "interface-conversion"] else []

/-- The number of results of a signature. -/
def resultCount : Sig → Nat
  | .mk _ rs _ => rs.foldl (fun n f => match f with | .mk ns _ => n + max 1 ns.length) 0

/-- Whether every method named `m`, of any type of the program, has one result. -/
def methodsSingle (s : Scope) (m : String) : Bool :=
  let ms := s.methods.getD m []
  !ms.isEmpty && ms.all fun me => resultCount me.sig == 1

/-- Whether a call of `fn` is known to have one result: a declared function, or every
    method of that name. -/
def singleResult (s : Scope) : Expr → Bool
  | .paren f => singleResult s f
  | .funcLit sig _ => resultCount sig == 1
  | .index f _ => singleResult s f
  | .ident f => 
    if s.isLocal f then ((s.funcTypes.lookup f).map fun sig => resultCount sig == 1).getD false
    else match s.obj? f with
      | some (.func _ _ sig _) => resultCount sig == 1
      | _ => false
  | .selector (.ident q) f =>
    match s.import? q with
    | some p => match s.qualified? q f with
      | some (.func _ _ sig _) => resultCount sig == 1
      -- A function of a package the document holds none or only some files of (absent
      -- or partial), and not in the files it holds (`qualified?` finds those, with their
      -- signatures): the run stops at the call (`evalCall`), or it is a model,
      -- `slices.Clip` or `fmt`'s, which return one value but for `Print`, `Println` and
      -- `Printf`.
      | _ => if p == "fmt" then ["Sprint", "Sprintf", "Sprintln"].contains f else partialPkg s.c p
    | none => methodsSingle s f
  | .selector _ m => methodsSingle s m
  | _ => false

/-- A call gc does not copy as an operand because its arity is not known here: as the
    only argument of a call, its results may be several. -/
def unknownArity (s : Scope) : Expr → Bool
  | .call fn _ _ => (match callKind s fn with | .call => true | _ => false) && !singleResult s fn
  | _ => false

/-- Whether a call's arguments are one call of unknown arity (`unknownArity`). -/
def soleUnknown (s : Scope) (args : List Expr) : Bool :=
  match args with
  | [a] => unknownArity s a
  | _ => false

/-- A call of a `fmt` function GoCore models, whose operands are `...any`. -/
def isFmtFunc (s : Scope) : Expr → Bool
  | .selector (.ident q) f => s.import? q == some "fmt" && fmtFuncs.contains f
  | _ => false

/-- A `bool` gc cannot take the address of: a comparison or a negation. -/
def isBoolOp : Expr → Bool
  | .paren x => isBoolOp x
  | .unary op _ => op == "!"
  | .binary op _ _ => ["==", "!=", "<", "<=", ">", ">="].contains op
  | _ => false

/-- Whether a type definition `type T def` makes `T` certainly not an interface. -/
def concreteDef : Expr → Bool
  | .structType _ | .star _ | .arrayType .. | .funcType _ | .mapType .. => true
  | .ident x => match universeType? x with
    | some (.iface _) => false
    | some _ => true
    | none => false
  | _ => false

def nonIfaceObj : Option Obj → Bool
  | some (.type _ (.mk _ _ alias te)) => !alias && concreteDef te
  | _ => false

/-- Whether the type written `te`, where `s` is, is certainly not an interface: a value
    passed or stored as one is never converted to an interface (Go spec, "Assignability").
    A type parameter of the code `s` is in counts: its values are passed as they are.
    Unknown types are not. -/
def nonIface (s : Scope) : Expr → Bool
  | .paren t => nonIface s t
  | .star _ | .arrayType .. | .funcType _ | .structType _ | .mapType .. => true
  | .ident x =>
    if s.isLocal x then false
    else if let some (.mk _ _ alias te) := s.ltype? x then !alias && concreteDef te
    else if s.tparams.contains x then true
    else match nameOwner s.c s.env x with
      | some _ => nonIfaceObj (s.obj? x)
      | none => match universeType? x with
        | some (.iface _) => false
        | some _ => true
        | none => false
  | .index (.ident n) _ => nonIfaceObj (s.obj? n)
  | .selector (.ident q) n => nonIfaceObj (s.qualified? q n)
  | .index (.selector (.ident q) n) _ => nonIfaceObj (s.qualified? q n)
  | _ => false

/-- Whether the type written `te` is certainly an interface: an interface literal, `any`,
    `error`, a declared interface type, or `fmt.Stringer`. -/
def isIfaceType (s : Scope) : Expr → Bool
  | .paren t => isIfaceType s t
  | .interfaceType _ => true
  | .ident x =>
    !s.isLocal x && !s.tparams.contains x &&
      match s.ltype? x with
      | some (.mk _ _ alias te) => !alias && (te matches .interfaceType _)
      | none => match nameOwner s.c s.env x with
        | some _ => match s.obj? x with
          | some (.type _ (.mk _ _ false (.interfaceType _))) => true
          | _ => false
        | none => x == "any" || x == "error"
  | .selector (.ident q) n => match s.import? q with
    | some "fmt" => n == "Stringer" && !s.c.pkgs.contains "fmt"
    | some _ => match s.qualified? q n with
      | some (.type _ (.mk _ _ false (.interfaceType _))) => true
      | _ => false
    | none => false
  | _ => false

/-- The locals of interface type a signature's parameters declare. -/
def Scope.withIfaces (s : Scope) (sig : Sig) : Scope :=
  let .mk ps _ _ := sig
  { s with ifaces := ps.flatMap (fun | .mk ns ty => if isIfaceType s ty then ns else []) ++ s.ifaces }

/-- A declared type's parameters bound to the type arguments `te` writes, if it writes
    them all. -/
def typeArgs (tps : List Field) (targs : List Expr) : Option (List (String × Expr)) :=
  let names := fieldListNames tps
  if names.length == targs.length then some (names.zip targs) else none

/-- Whether a value of type `ty`, written in the declaration scope `d` of a generic
    declaration whose type parameters are `sub` (each bound to a type argument written
    where the value is, in `s`), is certainly not an interface. A type parameter with no
    written argument may be inferred as an interface: unknown. -/
def nonIfaceIn (s d : Scope) (tps : List String) (sub : Option (List (String × Expr))) (ty : Expr) : Bool :=
  match ty with
  | .ident x =>
    if tps.contains x then match sub.bind (·.lookup x) with
      | some t => nonIface s t
      | none => false
    else nonIface d ty
  | _ => nonIface d ty

/-- The parameter types of a callee whose declaration the syntax shows, with what
    `nonIfaceIn` needs: a declared function (with its written type arguments) or a local
    of function type. -/
def calleeParams (s : Scope) (fn : Expr) : Option (Scope × List String × Option (List (String × Expr)) × Sig) :=
  let declared (path : String) (o : Option Obj) (targs : List Expr) :=
    match o with
    | some (.func fi tps sig _) =>
      some ({ s with pkg := path, file := fi, locals := [], tparams := [], ltypes := [], funcTypes := [], pointers := [] },
        fieldListNames tps, typeArgs tps targs, sig)
    | _ => none
  match fn with
  | .ident f =>
    if s.isLocal f then (s.funcTypes.lookup f).map fun sig => ({ s with locals := [] }, [], none, sig)
    else declared ((nameOwner s.c s.env f).getD s.pkg) (s.obj? f) []
  | .index (.ident f) targs => declared ((nameOwner s.c s.env f).getD s.pkg) (s.obj? f) targs
  | .selector (.ident q) f => (s.import? q).bind fun p => declared p (s.qualified? q f) []
  | .index (.selector (.ident q) f) targs => (s.import? q).bind fun p => declared p (s.qualified? q f) targs
  | _ => none

/-- For each of `n` arguments of a call of `fn`, whether it may be converted to an
    interface: `true` unless its parameter's type is certainly not one. -/
def argConv (s : Scope) (fn : Expr) (n : Nat) : List Bool :=
  match calleeParams s fn with
  | none => List.replicate n true
  | some (d, tps, sub, .mk ps _ variadic) =>
    let tys := ps.flatMap fun | .mk ns ty => List.replicate (max 1 ns.length) ty
    (List.range n).map fun i =>
      let ty? := if variadic && i + 1 ≥ tys.length then tys.getLast? else tys[i]?
      match ty? with
      | some ty => !nonIfaceIn s d tps sub ty
      | none => true

/-- For the elements of a composite literal of type `te`, whether each may be converted
    to an interface, by its position and its key: a slice's or array's element type, a
    struct's field types. -/
def elemConv (s : Scope) (te : Option Expr) : Nat → String → Bool :=
  let structOf (path : String) (o : Option Obj) (targs : List Expr) : Option (Nat → String → Bool) :=
    match o with
    | some (.type fi (.mk _ tps false (.structType fs))) =>
      let d : Scope := { s with pkg := path, file := fi, locals := [], tparams := [], ltypes := [], funcTypes := [], pointers := [] }
      let tpn := fieldListNames tps
      let sub := typeArgs tps targs
      let named := fs.flatMap fun | .mk ns ty => ns.map (·, ty)
      some fun i key =>
        -- A positional element of a struct with an embedded field: not looked up.
        let embedded := fs.any fun | .mk [] _ => true | _ => false
        let ty? := if key.isEmpty then (if embedded then none else (named[i]?).map (·.2))
          else named.lookup key
        match ty? with
        | some ty => !nonIfaceIn s d tpn sub ty
        | none => true
    | _ => none
  let found : Option (Nat → String → Bool) := match te with
    | some (.arrayType _ elt) => some fun (_ : Nat) (_ : String) => !nonIface s elt
    | some (.ident n) => structOf ((nameOwner s.c s.env n).getD s.pkg) (s.obj? n) []
    | some (.index (.ident n) targs) => structOf ((nameOwner s.c s.env n).getD s.pkg) (s.obj? n) targs
    | some (.selector (.ident q) n) => (s.import? q).bind fun p => structOf p (s.qualified? q n) []
    | some (.index (.selector (.ident q) n) targs) => (s.import? q).bind fun p => structOf p (s.qualified? q n) targs
    | _ => none
  found.getD fun (_ : Nat) (_ : String) => true

/-- The key of a keyed element, `""` for a positional one. -/
def elemKey : Expr → String
  | .keyValue (.ident f) _ => f
  | _ => ""

/-- The value of an element. -/
def elemValue : Expr → Expr
  | .keyValue _ v => v
  | e => e

/-- The methods of every package of the program, by name. -/
def allMethods (c : Ctx) : Std.HashMap String (List Method) :=
  c.pkgs.fold (fun acc _ p => p.methodsByName.fold (fun acc m ms => acc.insert m (ms ++ acc.getD m [])) acc) {}

/-- Whether `x.m`, called (`called`) or as a method value, may take `x`'s address: some
    method named `m` has a pointer receiver, or, for a call, none is known (it may be of
    anything). -/
def takesAddress (ms : Std.HashMap String (List Method)) (called : Bool) (m : String) : Bool :=
  match ms.get? m with
  | none => called
  | some l => l.any (·.ptr) || (called && l.isEmpty)

/-- A callee `T{...}.m` that selects a method of a composite literal, not a field: some
    method is named `m` and no struct field is. The receiver is a fresh value, so the
    selection reads no state. (On a variable `p` it may: a value-receiver method of a
    pointer dereferences it.) -/
def isMethodSel (s : Scope) : Expr → Bool
  | .selector (.composite ..) m => s.methods.contains m && !s.fields.contains m
  | _ => false

/-- A callee that reads nothing a call can change: a function of an imported package
    (not a variable of a package the document holds; one of a package it does not hold is
    a function GoCore models, or the run stops there anyway). -/
def isPkgFunc (s : Scope) : Expr → Bool
  | .selector (.ident q) x =>
    match s.import? q with
    | some p => !s.c.pkgs.contains p || !isVarObj (s.qualified? q x)
    | none => false
  | _ => false

mutual
/-- The events of evaluating `e` in gc's order. With `top`, `e` is a statement's
    top-level operand, which gc does not copy: the call of a call statement, the only
    right-hand side of an assignment to names, an only result. -/
def scanE (s : Scope) (top : Bool) : Expr → List Ev
  | .ident x => [.read (changeableName s x) none]
  | .paren x => scanE s top x
  | .selector (.ident q) x =>
    match s.import? q with
    | some p => [.read (!s.c.pkgs.contains p || isVarObj (s.qualified? q x)) none]
    | none => [.read (changeableName s q) none, .read true (some .nil)]
  | .selector x _ => scanE s false x ++ [.read true (some .nil)]
  | .index x is =>
    if isInstantiation s x then []
    else scanE s false x ++ scanEs s is ++ [.read true (some .text)]
  | .sliceExpr x l h m =>
    let inner := scanE s false x ++ scanOE s l ++ scanOE s h ++ scanOE s m
    if top then inner ++ [.read false (some .text)] else [mkNode false [.text] inner]
  | .typeAssert x t =>
    let ex := scanE s false x
    if top then ex ++ [.read false (some .text)]
    else match assertShape s t with
      | .late => ex ++ [.read true (some .text)]
      | .copied => [mkNode false [.text] ex]
      | .unknown => [mkNode false [.text] ex] ++ (if readsChangeable ex then [.slot "type-assertion"] else [])
  | .star x => if isType s x then [] else scanE s false x ++ [.read true (some .nil)]
  | .unary op x => if op == "<-" then [mkNode true [.text] (scanE s false x)] else scanE s false x
  | .binary op x y =>
    -- Each operand is a level of its own: `x` is evaluated whole before `y` in both
    -- orders (gc's `r := x; if r { r = y }`).
    if op == "&&" || op == "||" then
      [mkNode false [] [mkNode false [] (scanE s false x), mkNode false [] (scanE s false y)]]
    else
      let ex := scanE s false x
      let ey := scanE s false y
      let cmp := op == "==" || op == "!="
      let p : List Ev :=
        if (op == "/" || op == "%") && !isNonzeroLit y then [.read false (some .div)]
        else if (op == "<<" || op == ">>") && !isLit y then [.read false (some .shift)]
        else []
      ex ++ (if cmp then slotEv s x ex else []) ++ ey ++ (if cmp then slotEv s y ey else []) ++ p
  | .call fn args _ =>
    match callKind s fn with
    | .conversion =>
      if isByteSliceType s fn && args.all isStringShaped then [mkNode false [] (scanEs s args)]
      -- `[]byte(x)`: copied at its place if `x` is a string, not if it is a []byte.
      else if isByteSliceType s fn then
        let ev := scanEs s args
        ev ++ (if readsChangeable ev then [.slot "conversion"] else [])
      -- A conversion's operand may be converted to an interface (unless the type is
      -- certainly not one), or be a []byte gc does not copy.
      else scanArgs s [!nonIface s fn] args
    | .builtin b =>
      let inner := scanSlots s args
      if top then inner else [mkNode (!pureBuiltins.contains b) [.text] inner]
    | .call =>
      -- A method selected on a composite literal reads no state (`dropLast` drops the
      -- selector's read), unless some struct has a field of that name.
      let ef := if isPkgFunc s fn then [] else
        let e := scanE s false fn
        if isMethodSel s fn then e.dropLast else e
      let inner :=
        if soleUnknown s args then
          -- Not copied: it runs when this call does, after its function part, which
          -- gc reads after it.
          let amb : List Ev := if readsChangeable ef then [.slot "multi-value"] else []
          ef ++ amb ++ [mkNode true [.text] (scanArgParts s args)]
        else if isFmtFunc s fn then ef ++ scanFmtArgs s args
        else ef ++ scanArgs s (argConv s fn args.length) args
      if top then inner else [mkNode true [.text] inner]
  | .composite t elts => scanElems s (elemConv s t) 0 elts
  | .keyValue k v =>
    let ek := match k with
      | .ident _ => []
      | _ => scanE s false k
    ek ++ scanE s false v
  | _ => []
def scanEs (s : Scope) : List Expr → List Ev
  | [] => []
  | e :: es => scanE s false e ++ scanEs s es
def scanOE (s : Scope) : Option Expr → List Ev
  | none => []
  | some e => scanE s false e
/-- The only argument of a call, itself a call of unknown arity (`soleUnknown`): its
    function part and arguments, the call itself not copied. -/
def scanArgParts (s : Scope) : List Expr → List Ev
  | [] => []
  | e :: es => scanCallParts s e ++ scanArgParts s es
def scanCallParts (s : Scope) : Expr → List Ev
  | .call f as _ => scanE s false f ++ scanSlots s as
  | _ => []  -- not reached: `soleUnknown` holds of calls only
/-- Values in places that may convert them to an interface: arguments, elements,
    results. -/
def scanSlots (s : Scope) : List Expr → List Ev
  | [] => []
  | e :: es => let ev := scanE s false e; ev ++ slotEv s e ev ++ scanSlots s es
/-- A call's arguments, `conv` saying for each whether it may be converted to an
    interface (`argConv`). -/
def scanArgs (s : Scope) (conv : List Bool) : List Expr → List Ev
  | [] => []
  | e :: es =>
    let ev := scanE s false e
    ev ++ (if conv.headD true then slotEv s e ev else []) ++ scanArgs s conv.tail es
/-- A composite literal's elements, the `i`th and those after it (`elemConv`). -/
def scanElems (s : Scope) (conv : Nat → String → Bool) (i : Nat) : List Expr → List Ev
  | [] => []
  | e :: es =>
    let ev := scanE s false e
    ev ++ (if conv i (elemKey e) then slotEv s (elemValue e) ev else []) ++ scanElems s conv (i + 1) es
/-- A `fmt` function's arguments, each converted to `any`: a comparison's `bool` is
    copied at its place (`addrTemp`: it is not addressable), an O-node. -/
def scanFmtArgs (s : Scope) : List Expr → List Ev
  | [] => []
  | e :: es =>
    let ev := scanE s false e
    (if isBoolOp e then [mkNode false [] ev] else ev ++ slotEv s e ev) ++ scanFmtArgs s es
end

/-- The events of a statement's operand list: one operand is the top-level one (`top`),
    several are each in a place that may convert it to an interface. -/
def scanOperands (s : Scope) (top : Bool) (es : List Expr) : List Ev :=
  match es with
  | [e] => scanE s top e
  | _ => scanSlots s es

/-- Whether gc's order and left to right can be told apart on a level's events: a late
    operand that a call may change, or that may panic, before an O-node that may change
    state; or one that may panic before an O-node that may panic with another text. -/
def triggers : List Ev → Bool
  | [] => false
  | e :: es =>
    let here := match e with
      | .read ch pk => (ch || pk.isSome) && es.any fun
        | .node eff pks _ => (eff && (ch || pk.isSome)) || (match pk with
            | some p => pks.any (p.differs ·)
            | none => false)
        | _ => false
      | _ => false
    let inner := match e with
      | .node _ _ inn => triggers inn
      | _ => false
    here || inner || triggers es

/-- Where gc's order depends on a static type GoCore does not have, observably: a slot
    before an O-node, in its level, that may change state. -/
def ambiguity : List Ev → Option String
  | [] => none
  | e :: es =>
    let here := match e with
      | .slot why => if es.any (fun | .node eff _ _ => eff | _ => false) then some why else none
      | _ => none
    let inner := match e with
      | .node _ _ inn => ambiguity inn
      | _ => none
    (here.orElse fun _ => inner).orElse fun _ => ambiguity es

/-- Why a statement with these events stops the run: the order is observable there and
    depends on a static type. -/
def blocked (evs : List Ev) : Option String :=
  if triggers evs then ambiguity evs else none

/-- The rewrite's state: the next temporary's number and the statements hoisted so far. -/
structure HState where
  next : Nat := 0
  out : Array Stmt := #[]

abbrev HM := StateM HState

/-- A fresh temporary. `·` is no Go identifier's character, so it is no program's name. -/
def temp : HM String := modifyGet fun h => (s!"·h{h.next}", { h with next := h.next + 1 })

def emit (st : Stmt) : HM Unit := modify fun h => { h with out := h.out.push st }

/-- `·h := e` before the statement; `·h` in its place. -/
def copyE (e : Expr) : HM Expr := do
  let t ← temp
  emit (.assign ":=" [.ident t] [e])
  pure (.ident t)

/-- Run `m` on its own list of hoisted statements: its result and what it hoisted. -/
def isolated (m : HM α) : HM (α × List Stmt) := do
  let saved ← modifyGet fun h => (h.out, { h with out := #[] })
  let a ← m
  let hs ← modifyGet fun h => (h.out, { h with out := saved })
  pure (a, hs.toList)

/-- The root name of an assigned place, an operand of `&` or a method call's receiver. -/
def rootName : Expr → List String
  | .ident x => [x]
  | .selector x _ | .index x _ | .paren x => rootName x
  | _ => []

mutual
/-- The locals a call may change, by name: those a function literal assigns (`lit`: inside
    one), the operands of `&`, the arrays sliced, and the receivers of `x.m` where
    `pm called m` (`takesAddress`): a method with a pointer receiver takes the receiver's
    address, called or as a method value. -/
def chE (pm : Bool → String → Bool) (lit : Bool) : Expr → List String
  | .funcLit _ body => chSs pm true body
  | .unary op x => (if op == "&" then rootName x else []) ++ chE pm lit x
  | .call fn args _ =>
    (match fn with | .selector x m => (if pm true m then rootName x else []) | _ => []) ++
      chE pm lit fn ++ chEs pm lit args
  | .composite _ es => chEs pm lit es
  | .keyValue k v => chE pm lit k ++ chE pm lit v
  | .selector x m => (if pm false m then rootName x else []) ++ chE pm lit x
  | .paren x | .star x | .typeAssert x _ => chE pm lit x
  | .index x is => chE pm lit x ++ chEs pm lit is
  | .sliceExpr x l h m => rootName x ++ chE pm lit x ++ chOE pm lit l ++ chOE pm lit h ++ chOE pm lit m
  | .binary _ x y => chE pm lit x ++ chE pm lit y
  | _ => []
def chEs (pm : Bool → String → Bool) (lit : Bool) : List Expr → List String
  | [] => []
  | e :: es => chE pm lit e ++ chEs pm lit es
def chOE (pm : Bool → String → Bool) (lit : Bool) : Option Expr → List String
  | none => []
  | some e => chE pm lit e
def chS (pm : Bool → String → Bool) (lit : Bool) : Stmt → List String
  | .block ss => chSs pm lit ss
  | .decl (.value _ specs) => chSpecs pm lit specs
  | .expr x | .defer_ x => chE pm lit x
  | .incDec _ x => (if lit then rootName x else []) ++ chE pm lit x
  | .assign tok l r =>
    (if lit && tok != ":=" then l.flatMap rootName else []) ++ chEs pm lit l ++ chEs pm lit r
  | .ret es => chEs pm lit es
  | .if_ i c b e => chOS pm lit i ++ chE pm lit c ++ chSs pm lit b ++ chOS pm lit e
  | .switch i t cs => chOS pm lit i ++ chOE pm lit t ++ chClauses pm lit cs
  | .typeSwitch i _ x cs => chOS pm lit i ++ chE pm lit x ++ chClauses pm lit cs
  | .for_ i c p b => chOS pm lit i ++ chOE pm lit c ++ chOS pm lit p ++ chSs pm lit b
  | .range k v tok x b =>
    (if lit && tok != ":=" then (k.toList ++ v.toList).flatMap rootName else []) ++
      chOE pm lit k ++ chOE pm lit v ++ chE pm lit x ++ chSs pm lit b
  | _ => []
def chSs (pm : Bool → String → Bool) (lit : Bool) : List Stmt → List String
  | [] => []
  | s :: ss => chS pm lit s ++ chSs pm lit ss
def chOS (pm : Bool → String → Bool) (lit : Bool) : Option Stmt → List String
  | none => []
  | some s => chS pm lit s
def chClauses (pm : Bool → String → Bool) (lit : Bool) : List Clause → List String
  | [] => []
  | .mk l _ b :: cs => chEs pm lit l ++ chSs pm lit b ++ chClauses pm lit cs
def chSpecs (pm : Bool → String → Bool) (lit : Bool) : List Spec → List String
  | [] => []
  | .mk _ _ vs :: specs => chEs pm lit vs ++ chSpecs pm lit specs
end

/-- Wrap an expression that needs a rewrite its place cannot hold: evaluating it stops
    the run (`evalCall` evaluates the function part first), while the names it refers
    to stay for the package-level initialization order (`varDeps`). -/
def stopAt (why : String) (e : Expr) : Expr := .call (.unsupported s!"eval-order-{why}") [e] false

def asStmt : List Stmt → Stmt
  | [st] => st
  | ss => .block ss

/-- An `if`, `switch` or type `switch` after its init's statements `is` and its operand's
    hoisted ones `hs`: the init stays the statement's when nothing was hoisted, else
    both go before it in a block (its scope). -/
def withInit (is hs : List Stmt) (mk : Option Stmt → Stmt) : List Stmt :=
  match is, hs with
  | [], [] => [mk none]
  | [i], [] => [mk (some i)]
  | is, hs => [.block (is ++ hs ++ [mk none])]

/-- The stop of a statement whose order is observable and depends on a static type. -/
def stopStmt (why : String) : Stmt := .unsupported s!"eval-order-{why}"

mutual
/-- `e` in gc's order: when `on`, its O-nodes are hoisted into temporaries (with `top`,
    `e` itself is not, as in `scanE`); either way the bodies of its function literals are
    rewritten. -/
def hE (s : Scope) (on top : Bool) : Expr → HM Expr
  | .paren x => do pure (.paren (← hE s on top x))
  | .selector x f => do pure (.selector (← hE s on false x) f)
  | .index x is =>
    if isInstantiation s x then pure (.index x is)
    else do pure (.index (← hE s on false x) (← hEs s on is))
  | .sliceExpr x l h m => do
    let e' := Expr.sliceExpr (← hE s on false x) (← hOE s on l) (← hOE s on h) (← hOE s on m)
    if on && !top then copyE e' else pure e'
  | .typeAssert x t => do
    let e' := Expr.typeAssert (← hE s on false x) t
    if on && !top && assertShape s t != .late then copyE e' else pure e'
  | .star x => if isType s x then pure (.star x) else do pure (.star (← hE s on false x))
  | .unary op x => do
    let e' := Expr.unary op (← hE s on false x)
    if on && op == "<-" then copyE e' else pure e'
  | .binary op x y =>
    if on && (op == "&&" || op == "||") then do
      let x' ← hE s on false x
      -- `y`'s own copies go inside the `if`: they run only when `y` is evaluated.
      let (y', ys) ← isolated (hE s on false y)
      if ys.isEmpty then copyE (.binary op x' y')
      else do
        let r ← temp
        emit (.assign ":=" [.ident r] [x'])
        let cond := if op == "&&" then Expr.ident r else .unary "!" (.ident r)
        emit (.if_ none cond (ys ++ [.assign "=" [.ident r] [y']]) none)
        pure (.ident r)
    else do pure (.binary op (← hE s on false x) (← hE s on false y))
  | .call fn args sp =>
    match callKind s fn with
    | .conversion => do
      let e' := Expr.call fn (← hEs s on args) sp
      if on && isByteSliceType s fn && args.all isStringShaped then copyE e' else pure e'
    | _ => do
      let fn' ← hE s on false fn
      let args' ← if on && soleUnknown s args then hArgParts s on args
        else hArgs s on (isFmtFunc s fn) args
      let e' := Expr.call fn' args' sp
      if on && !top then copyE e' else pure e'
  | .composite t elts => do pure (.composite t (← hEs s on elts))
  | .keyValue k v => do
    let k' ← match k with
      | .ident _ => pure k
      | _ => hE s on false k
    pure (.keyValue k' (← hE s on false v))
  | .funcLit sig body => do
    pure (.funcLit sig (← hSs ((s.enter sig).withIfaces sig) body))
  | e => pure e
def hEs (s : Scope) (on : Bool) : List Expr → HM (List Expr)
  | [] => pure []
  | e :: es => do
    let e' ← hE s on false e
    pure (e' :: (← hEs s on es))
/-- A statement's operand list (`scanOperands`): an only operand is the top-level one. -/
def hOperands (s : Scope) (on top : Bool) : List Expr → HM (List Expr)
  | [] => pure []
  | e :: es => do
    let e' ← hE s on (top && es.isEmpty) e
    pure (e' :: (← hEs s on es))
/-- A call's arguments; to a `fmt` function (`iface`), a comparison is copied at its
    place (`scanFmtArgs`). -/
def hArgs (s : Scope) (on iface : Bool) : List Expr → HM (List Expr)
  | [] => pure []
  | e :: es => do
    let e' ← hE s on false e
    let e'' ← if on && iface && isBoolOp e then copyE e' else pure e'
    pure (e'' :: (← hArgs s on iface es))
def hOE (s : Scope) (on : Bool) : Option Expr → HM (Option Expr)
  | none => pure none
  | some e => do pure (some (← hE s on false e))
/-- The only argument of a call, itself a call of unknown arity (`soleUnknown`). -/
def hArgParts (s : Scope) (on : Bool) : List Expr → HM (List Expr)
  | [] => pure []
  | e :: es => do
    let e' ← hCallParts s on e
    pure (e' :: (← hArgParts s on es))
/-- A call's function part and arguments in gc's order, the call itself not copied. -/
def hCallParts (s : Scope) (on : Bool) : Expr → HM Expr
  | .call f as sp => do pure (.call (← hE s on false f) (← hEs s on as) sp)
  | e => pure e  -- not reached: `soleUnknown` holds of calls only
/-- Switch case lists: evaluated one by one, with no place before each for statements. -/
def hCases (s : Scope) : List Expr → HM (List Expr)
  | [] => pure []
  | e :: es => do
    let e' ← hE s false false e
    let e'' := if triggers (scanE s false e) then stopAt "switch-case" e' else e'
    pure (e'' :: (← hCases s es))
/-- A statement in gc's order: the statements it becomes, and the scope after it. -/
def hS (s : Scope) : Stmt → HM (List Stmt × Scope)
  | .block ss => do pure ([.block (← hSs s ss)], s)
  | .expr e => do
    let evs := scanE s true e
    if let some why := blocked evs then return ([stopStmt why], s)
    let (e', hs) ← isolated (hE s (triggers evs) true e)
    pure (hs ++ [.expr e'], s)
  | .defer_ e => do
    let evs := scanE s true e
    if let some why := blocked evs then return ([stopStmt why], s)
    let (e', hs) ← isolated (hE s (triggers evs) true e)
    pure (hs ++ [.defer_ e'], s)
  | .incDec op x => do
    let evs := scanE s false x
    if let some why := blocked evs then return ([stopStmt why], s)
    let (x', hs) ← isolated (hE s (triggers evs) false x)
    pure (hs ++ [.incDec op x'], s)
  | .assign tok lhs rhs => do
    -- The right side is the top-level operand when it is one expression assigned to
    -- names (`x := f()`, `x, ok := y.(T)`), several targets of one call included.
    let top := tok == ":=" || (tok == "=" && (lhs.length > 1 || lhs.all fun | .ident _ => true | _ => false))
    let evs := scanEs s lhs ++ scanOperands s top rhs
    let s' := if tok == ":=" then s.bind (declaredBy lhs) else s
    if let some why := blocked evs then return ([stopStmt why], s')
    let on := triggers evs
    let ((lhs', rhs'), hs) ← isolated (do
      let lhs' ← if tok == ":=" then pure lhs else hEs s on lhs
      pure (lhs', ← hOperands s on top rhs))
    pure (hs ++ [.assign tok lhs' rhs'], s')
  | .ret es => do
    let evs := scanOperands s true es
    if let some why := blocked evs then return ([stopStmt why], s)
    let (es', hs) ← isolated (hOperands s (triggers evs) true es)
    pure (hs ++ [.ret es'], s)
  | .decl (.value false specs) => hSpecs s specs
  | .if_ init cond body els => do
    let (is, s1) ← hOS s init
    let evs := scanE s1 false cond
    if let some why := blocked evs then return ([stopStmt why], s)
    let (cond', hs) ← isolated (hE s1 (triggers evs) false cond)
    let body' ← hSs s1 body
    let (els', _) ← hOS s1 els
    let elsS := if els'.isEmpty then none else some (asStmt els')
    pure (withInit is hs fun i => .if_ i cond' body' elsS, s)
  | .switch init tag clauses => do
    let (is, s1) ← hOS s init
    let evs := scanOE s1 tag
    if let some why := blocked evs then return ([stopStmt why], s)
    let (tag', hs) ← isolated (hOE s1 (triggers evs) tag)
    let clauses' ← hClauses s1 none clauses
    pure (withInit is hs fun i => .switch i tag' clauses', s)
  | .typeSwitch init bind x clauses => do
    let (is, s1) ← hOS s init
    let evs := scanE s1 false x
    if let some why := blocked evs then return ([stopStmt why], s)
    let (x', hs) ← isolated (hE s1 (triggers evs) false x)
    let clauses' ← hClauses s1 bind clauses
    pure (withInit is hs fun i => .typeSwitch i bind x' clauses', s)
  | .for_ init cond post body => do
    -- The init runs once, before the loop: its temporaries go before the `for`, and
    -- the init statement stays the loop's, which declares its per-iteration variables.
    let (is, s1) ← hOS s init
    let (pre, init') := match is.reverse with
      | [] => ([], none)
      | i :: rest => (rest.reverse, some i)
    -- The condition is evaluated before each iteration, with no place for statements.
    let c' ← hOE s1 false cond
    let cond' := if triggers (scanOE s1 cond) then c'.map (stopAt "for-condition") else c'
    -- The post statement runs after each iteration; its temporaries in a block of its own.
    let (ps, _) ← hOS s1 post
    let post' := if ps.isEmpty then none else some (asStmt ps)
    let body' ← hSs s1 body
    let loop := Stmt.for_ init' cond' post' body'
    pure (if pre.isEmpty then [loop] else [.block (pre ++ [loop])], s)
  | .range k v tok x body => do
    let evs := scanE s false x
    if let some why := blocked evs then return ([stopStmt why], s)
    let (x', hs) ← isolated (hE s (triggers evs) false x)
    let sb := if tok == ":=" then s.bind (declaredBy (k.toList ++ v.toList)) else s
    let body' ← hSs sb body
    pure (hs ++ [.range k v tok x' body'], s)
  | .localType specs => pure ([.localType specs], specs.foldl (fun s (spec, _) => s.bindType spec) s)
  | st => pure ([st], s)
def hSs (s : Scope) : List Stmt → HM (List Stmt)
  | [] => pure []
  | st :: ss => do
    let (l, s') ← hS s st
    pure (l ++ (← hSs s' ss))
def hOS (s : Scope) : Option Stmt → HM (List Stmt × Scope)
  | none => pure ([], s)
  | some st => hS s st
def hClauses (s : Scope) (bind : Option String) : List Clause → HM (List Clause)
  | [] => pure []
  | .mk l d b :: cs => do
    let l' ← if bind.isSome || l.isEmpty then pure l else hCases s l
    let b' ← hSs (s.bind bind.toList) b
    pure (.mk l' d b' :: (← hClauses s bind cs))
/-- `var` specs, each its own statement, as gc's type checker makes them. -/
def hSpecs (s : Scope) : List Spec → HM (List Stmt × Scope)
  | [] => pure ([], s)
  | .mk names ty vals :: specs => do
    let evs := scanOperands s true vals
    let s1 := s.bind names
    let s1 := match ty with
      | some t => if isIfaceType s t then { s1 with ifaces := names ++ s1.ifaces } else s1
      | none => s1
    let here ← match blocked evs with
      | some why => pure [stopStmt why]
      | none => do
        let (vals', hs) ← isolated (hOperands s (triggers evs) true vals)
        pure (hs ++ [Stmt.decl (.value false [.mk names ty vals'])])
    let (rest, s2) ← hSpecs s1 specs
    pure (here ++ rest, s2)
end

/-- A package-level variable's initializer: it runs inside package initialization, with
    no place for statements before it. -/
def hPkgValue (s : Scope) (vals : List Expr) : HM (List Expr) := do
  let vals' ← hEs s false vals
  pure (if triggers (scanOperands s true vals) then vals'.map (stopAt "package-variable") else vals')

/-- The names of every struct field the program declares. -/
def structFields (c : Ctx) : Std.HashSet String :=
  c.pkgs.fold (fun acc _ p => p.objs.fold (fun acc _ o => match o with
    | .type _ (.mk _ _ _ (.structType fs)) => fs.foldl (fun a f => match f with
        | .mk ns _ => ns.foldl (·.insert ·) a) acc
    | _ => acc) acc) {}

/-- The scope a function or method body of package `pkg`, file `fi`, starts in: its
    receiver and parameters as locals, its own and its receiver's type parameters, the
    locals it may change, a pointer receiver as a pointer local, its interface-typed
    parameters (`withIfaces`), and the program's struct fields and methods. -/
def funcScope (c : Ctx) (fields : Std.HashSet String) (methods : Std.HashMap String (List Method))
    (pkg : String) (fi : Nat) (recv : Option Field) (tps : List Field) (sig : Sig)
    (body : List Stmt) : Scope :=
  let recvTParams := match recv with
    | some (.mk _ rty) => ((recvBase rty).map (·.2.1)).getD []
    | none => []
  (Scope.enter { c, pkg, file := fi, locals := fieldListNames recv.toList,
                 tparams := fieldListNames tps ++ recvTParams,
                 changeable := Std.HashSet.ofList (chSs (takesAddress methods) false body),
                 pointers := pointerNames recv.toList, fields, methods } sig).withIfaces sig

/-- A declaration in gc's order: function and method bodies, and package-level
    variables' initializers. -/
def hDecl (c : Ctx) (fields : Std.HashSet String) (methods : Std.HashMap String (List Method))
    (pkg : String) (fi : Nat) : Decl → HM Decl
  | .func name recv tps sig (some body) => do
    let s := funcScope c fields methods pkg fi recv tps sig body
    pure (.func name recv tps sig (some (← hSs s body)))
  | .value false specs => do
    -- Its function literals' locals: those a nested literal assigns may change.
    let s : Scope := { c, pkg, file := fi, fields, methods,
                       changeable := Std.HashSet.ofList (chSpecs (takesAddress methods) false specs) }
    let specs' ← specs.mapM fun
      | .mk names ty vals => do pure (Spec.mk names ty (← hPkgValue s vals))
    pure (.value false specs')
  | d => pure d

/-- gc 1.25.5's order of evaluation: the program, each statement where the order can be
    observed rewritten as order.go orders it. -/
def gcOrder (prog : Program) : Program :=
  let c := buildCtx prog
  let fields := structFields c
  let methods := allMethods c
  let go : HM (List Package) := prog.packages.mapM fun p => do
    let files ← p.files.zipIdx.mapM fun (f, fi) => do
      pure { f with decls := ← f.decls.mapM (hDecl c fields methods p.path fi) }
    pure { p with files }
  { packages := (go.run' {}) }

end Hoist

/-- GoCore's evaluator as it is: every operand list left to right. -/
def Order.leftToRight : Order := id

/-- gc 1.25.5's order (`Hoist.gcOrder`), the one the differential run compares. -/
def Order.gc : Order := Hoist.gcOrder

end Gala.GoCore.Full
