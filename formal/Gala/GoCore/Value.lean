/-
  # GoCore: types, values and the state of a run

  The run-time side of GoCore for whole programs (`Gala/GoCore/Eval.lean` runs them).
  Every construct cites the section of the Go language specification
  (https://go.dev/ref/spec) it follows.

  * **Types** are the types the program names, after its type parameters are
    substituted: predeclared types, defined (named) types with their type arguments,
    pointers, struct, interface and function types (Go spec, "Types"). Untyped constants
    have types of their own (`untyped int`, ...), as in the Go spec, "Constants".
  * **Values carry their type.** A value of a non-interface type carries exactly its
    static type, which Go knows at compile time, so carrying it decides nothing Go's
    static semantics would not. A value of interface type is `boxed`: the interface
    type and the dynamic value inside it; or it is `nilIface` (Go spec, "Interface
    types": a value of interface type holds a dynamic type and value, or is nil). So a
    nil pointer in an interface is not a nil interface, as in Go.
  * **Variables are cells.** Every variable is a cell of the heap, so `&x` is a pointer
    to `x`'s cell and a closure shares the cells of the variables it refers to (Go spec,
    "Address operators", "Function literals"). A pointer names a cell and a path of
    struct field indices inside its value (`&x.f`).
  * **Integers** are mathematical integers reduced to their type's width: two's
    complement for signed types, modulo `2^n` for unsigned ones (Go spec, "Numeric
    types", "Integer overflow"). `int` and `uint` are 64 bits wide, as on the 64-bit
    targets the differential run uses.
-/
import Std.Data.HashMap
import Gala.GoCore.Syntax

namespace Gala.GoCore.Full

/-- The integer types (Go spec, "Numeric types"). -/
inductive IntKind
  | int | int8 | int16 | int32 | int64
  | uint | uint8 | uint16 | uint32 | uint64 | uintptr
  deriving DecidableEq, Repr, Inhabited, BEq

def IntKind.name : IntKind → String
  | .int => "int" | .int8 => "int8" | .int16 => "int16" | .int32 => "int32"
  | .int64 => "int64" | .uint => "uint" | .uint8 => "uint8" | .uint16 => "uint16"
  | .uint32 => "uint32" | .uint64 => "uint64" | .uintptr => "uintptr"

def IntKind.bits : IntKind → Nat
  | .int8 | .uint8 => 8
  | .int16 | .uint16 => 16
  | .int32 | .uint32 => 32
  | _ => 64

def IntKind.signed : IntKind → Bool
  | .int | .int8 | .int16 | .int32 | .int64 => true
  | _ => false

/-- The integer type a predeclared name stands for; `byte` and `rune` are aliases of
    `uint8` and `int32` (Go spec, "Numeric types"). -/
def IntKind.ofName? : String → Option IntKind
  | "int" => some .int | "int8" => some .int8 | "int16" => some .int16
  | "int32" | "rune" => some .int32 | "int64" => some .int64
  | "uint" => some .uint | "uint8" | "byte" => some .uint8 | "uint16" => some .uint16
  | "uint32" => some .uint32 | "uint64" => some .uint64 | "uintptr" => some .uintptr
  | _ => none

/-- `x` reduced to the width of `k` (Go spec, "Integer overflow"). -/
def IntKind.wrap (k : IntKind) (x : Int) : Int :=
  if k.signed then x.bmod (2 ^ k.bits) else x.emod (2 ^ k.bits)

/-- Whether `x` is a value of `k`, as a constant converted to `k` must be (Go spec,
    "Representability"). -/
def IntKind.represents (k : IntKind) (x : Int) : Bool := k.wrap x == x

/-- The kind of an untyped constant (Go spec, "Constants"). -/
inductive UKind
  | int | rune | float | bool | string | nil
  deriving DecidableEq, Repr, Inhabited, BEq

mutual
/-- A type (Go spec, "Types"). -/
inductive Ty
  | int (k : IntKind)
  | bool
  | string
  /-- The type of an untyped constant. -/
  | untyped (k : UKind)
  /-- A defined type `pkg.name[args]`. -/
  | named (pkg name : String) (args : List Ty)
  | ptr (elem : Ty)
  /-- A struct type: its fields' names (an embedded field's is its type's name) and
      types, in order. -/
  | struct_ (fields : List (String × Ty))
  /-- An interface type, by its methods: each one's name and function type, sorted by
      name (Go spec, "Interface types"). A type implements it when its method set has
      each method with that name and an identical type ("Implementing an interface"). -/
  | iface (methods : List (String × Ty))
  | func_ (params results : List Ty) (variadic : Bool)
  /-- The results of a call with several, `(T1, T2)`. -/
  | tuple (ts : List Ty)
  /-- `[]elem` (Go spec, "Slice types"). -/
  | slice (elem : Ty)
  /-- `[n]elem` (Go spec, "Array types"). -/
  | array (n : Nat) (elem : Ty)
  /-- `float32` or `float64`, by its width (Go spec, "Numeric types"). -/
  | float (bits : Nat)
end

instance : Inhabited Ty := ⟨.bool⟩

mutual
def Ty.beq : Ty → Ty → Bool
  | .int a, .int b => a == b
  | .bool, .bool | .string, .string => true
  | .untyped a, .untyped b => a == b
  | .named p n as, .named q m bs => p == q && n == m && Ty.beqs as bs
  | .ptr a, .ptr b => a.beq b
  | .struct_ fs, .struct_ gs => Ty.beqFields fs gs
  | .iface ms, .iface ns => Ty.beqFields ms ns
  | .func_ ps rs v, .func_ qs ss w => Ty.beqs ps qs && Ty.beqs rs ss && v == w
  | .tuple as, .tuple bs => Ty.beqs as bs
  | .slice a, .slice b => a.beq b
  | .array n a, .array m b => n == m && a.beq b
  | .float a, .float b => a == b
  | _, _ => false
def Ty.beqs : List Ty → List Ty → Bool
  | [], [] => true
  | a :: as, b :: bs => a.beq b && Ty.beqs as bs
  | _, _ => false
def Ty.beqFields : List (String × Ty) → List (String × Ty) → Bool
  | [], [] => true
  | (n, a) :: as, (m, b) :: bs => n == m && a.beq b && Ty.beqFields as bs
  | _, _ => false
end

instance : BEq Ty := ⟨Ty.beq⟩

mutual
/-- A type's text as `%T` and Go's runtime print it (package names are the last
    element of the import path). -/
def Ty.text : Ty → String
  | .int k => k.name
  | .bool => "bool"
  | .string => "string"
  | .untyped .int => "untyped int"
  | .untyped .rune => "untyped rune"
  | .untyped .bool => "untyped bool"
  | .untyped .string => "untyped string"
  | .untyped .nil => "untyped nil"
  | .untyped .float => "untyped float"
  | .named p n args =>
    let q := (p.splitOn "/").getLast!
    let base := if p.isEmpty then n else s!"{q}.{n}"
    if args.isEmpty then base else base ++ "[" ++ ",".intercalate (Ty.texts args) ++ "]"
  | .ptr t => "*" ++ t.text
  | .struct_ fs => "struct { " ++ "; ".intercalate (Ty.fieldTexts fs) ++ " }"
  | .iface [] => "interface {}"
  | .iface ms => "interface { " ++ "; ".intercalate (ms.map (·.1)) ++ " }"
  | .func_ ps rs _ => "func(" ++ ", ".intercalate (Ty.texts ps) ++ ")" ++
      (if rs.isEmpty then "" else " " ++ ", ".intercalate (Ty.texts rs))
  | .tuple ts => "(" ++ ", ".intercalate (Ty.texts ts) ++ ")"
  | .slice t => "[]" ++ t.text
  | .array n t => s!"[{n}]" ++ t.text
  | .float b => s!"float{b}"
def Ty.texts : List Ty → List String
  | [] => []
  | t :: ts => t.text :: Ty.texts ts
def Ty.fieldTexts : List (String × Ty) → List String
  | [] => []
  | (n, t) :: fs => (n ++ " " ++ t.text) :: Ty.fieldTexts fs
end

instance : ToString Ty := ⟨Ty.text⟩

/-- Where a pointer points: a cell, and a path of field indices inside its value. -/
structure Loc where
  cell : Nat
  path : List Nat
  deriving DecidableEq, Repr, Inhabited, BEq

/-- A non-nil slice (Go spec, "Slice types"): its underlying array, at `base` (a cell
    holding an array value, or such a value inside one), and the window `off`,
    `len` and `cap` into it. Slices sharing an underlying array share `base`. -/
structure SliceRep where
  base : Loc
  off : Nat
  len : Nat
  cap : Nat
  /-- The other capacity the gc toolchain may have given the slice, when that depends
      on its escape analysis, which GoCore does not model (`GcRuntime.stackAppendCap`).
      A run that would observe which one it is stops `unsupported`. -/
  alt : Option Nat := none
  deriving Inhabited, BEq

/-- Where element `i` of the slice is. -/
def SliceRep.loc (r : SliceRep) (i : Nat) : Loc := ⟨r.base.cell, r.base.path ++ [r.off + i]⟩

/-- A function value: a declared function, a method bound to a receiver (a method
    value), or a function literal's closure, by its index in the state. -/
inductive Fn
  | decl (pkg name : String) (targs : List Ty)
  | method (pkg tyName name : String) (targs : List Ty) (recvCell : Nat)
  | closure (id : Nat)
  deriving Inhabited, BEq

/-- A value; see the module doc. -/
inductive Val
  | int (t : Ty) (n : Int)
  | bool (t : Ty) (b : Bool)
  | str (t : Ty) (s : String)
  /-- A struct value, its fields in declaration order. -/
  | struct_ (t : Ty) (fs : List Val)
  /-- A pointer; `none` is nil. -/
  | ptr (t : Ty) (to : Option Loc)
  /-- A function; `none` is nil. -/
  | func_ (t : Ty) (f : Option Fn)
  /-- A nil interface value, or the untyped `nil`. -/
  | nilIface (t : Ty)
  /-- A non-nil value of interface type `t`: its dynamic value, never itself boxed
      (Go spec, "Interface types"). -/
  | boxed (t : Ty) (v : Val)
  /-- The results of a call with several. -/
  | tuple (vs : List Val)
  /-- An array value, its elements in order (Go spec, "Array types"). -/
  | array (t : Ty) (es : Array Val)
  /-- A slice; `none` is nil. -/
  | slice (t : Ty) (s : Option SliceRep)
  /-- A `float64` or `float32` value: an IEEE binary64 value, a `float32` one held as
      the binary64 value it converts to exactly (Go spec, "Numeric types"; `FloatRep`). -/
  | float (t : Ty) (x : Float)
  /-- A floating-point constant, exact: `num / den`, in lowest terms, `den > 0` (Go
      spec, "Constants": numeric constants are exact values, with no negative zero).
      `size` is 0 for an untyped one (type `untyped float`), else the width of its
      float type `t`, whose value it is exactly. A constant stored in a variable, or
      meeting one in an operation, becomes a `float` (`defaultVal`). -/
  | rat (t : Ty) (size : Nat) (num : Int) (den : Nat)
  /-- A value GoCore cannot know, reading or writing into it stopping the run with
      `what`: a package-level variable of an imported package whose initializer GoCore
      could not run (it reached `what`), or the array an append left behind when gc
      may or may not have written into it (`appendValues`). -/
  | poison (what : String)
  deriving Inhabited

def Val.ty : Val → Ty
  | .int t _ | .bool t _ | .str t _ | .struct_ t _ | .ptr t _ | .func_ t _ | .nilIface t
  | .boxed t _ | .array t _ | .slice t _ | .float t _ | .rat t _ _ _ => t
  | .tuple vs => .tuple (vs.map fun _ => .bool)
  | .poison _ => .tuple []

/-- The dynamic value of an interface value; any other value itself. -/
def Val.dyn : Val → Val
  | .boxed _ v => v
  | v => v

/-- Why execution stopped early. -/
inductive Stop
  /-- A panic: its value, and whether it is a runtime error (Go spec, "Run-time
      panics"), whose value is a `runtime.Error` GoCore does not model. The text Go
      prints is made only when the panic ends the run (`runProgram`), as Go makes it
      after the deferred calls ran. -/
  | panic (v : Val) (runtime : Bool)
  | ret (vs : List Val)
  | brk
  | cont
  /-- The step or depth budget ran out: inconclusive. -/
  | fuel
  /-- A construct outside GoCore was reached. -/
  | unsupported (what : String)
  /-- Something no program Go compiles does: a GoCore or exporter bug. -/
  | stuck (msg : String)
  deriving Inhabited

/-- A function literal's closure: the variables in scope where it was written (by
    cell), its type environment, and the literal. -/
structure Closure where
  pkg : String
  file : Nat
  scopes : List (List (String × Nat))
  tenv : List (String × Ty)
  sig : Sig
  body : List Stmt

/-- The state of a run. -/
structure St where
  cells : Array Val
  /-- Each cell's declared type: the variable's static type, which for an interface
      variable is not its value's (Go spec, "Variables"). -/
  types : Array Ty := #[]
  closures : Array Closure
  out : String
  /-- Steps left: each call and loop iteration takes one. -/
  steps : Nat
  /-- The pointers fmt is printing through, for `FormatStruct`'s cycle guard. -/
  printing : List Loc := []
  /-- The cells of package-level variables, keyed `path.name`. -/
  globals : Std.HashMap String Nat := {}
  /-- The deferred calls of each running function, the innermost call's first; each
      list newest first: a function value, its arguments, and whether they end in
      `xs...` (Go spec, "Defer statements"). -/
  defers : List (List (Val × List Val × Bool)) := []
  /-- While a deferred call of a panicking function runs: the depth of that call and
      the panic, which `recover` called by it directly stops (Go spec, "Handling
      panics"). -/
  recoverable : Option (Nat × Val × Bool) := none

abbrev GM := ExceptT Stop (StateM St)

def tick : GM Unit := do
  if (← get).steps == 0 then throw .fuel
  modify fun s => { s with steps := s.steps - 1 }

def emit (text : String) : GM Unit := modify fun s => { s with out := s.out ++ text }

def stuck (msg : String) : GM α := throw (.stuck msg)
def unsupported (what : String) : GM α := throw (.unsupported what)

/-! ## Cells -/

-- The state is updated with `modify`, never `get` then `set`: a `get` keeps a second
-- reference to the cells, and the update would copy them.
def allocAs (t : Ty) (v : Val) : GM Nat := do
  let n := (← get).cells.size
  modify fun s => { s with cells := s.cells.push v, types := s.types.push t }
  pure n

/-- A new variable whose declared type is its value's: one `:=` declares, a receiver, a
    composite literal `&T{...}` points to. -/
def alloc (v : Val) : GM Nat := allocAs v.ty v

/-- A variable's declared type. -/
def cellType (cell : Nat) : GM Ty := do
  match (← get).types[cell]? with
  | some t => pure t
  | none => stuck "no such variable"

/-- The field or array element at `path` inside `v`. -/
def Val.at : Val → List Nat → Option Val
  | v, [] => some v
  | .struct_ _ fs, i :: p => (fs[i]?).bind (·.at p)
  | .array _ es, i :: p => (es[i]?).bind (·.at p)
  -- Inside a value GoCore cannot read, the same.
  | .poison w, _ => some (.poison w)
  | _, _ => none

/-- `v` with the field or element at `path` replaced by `w`; `v` itself where `path`
    leads nowhere (`store` checks it first). An array element is replaced in place
    when the array is not shared, so a store into a slice costs no copy of it. -/
def Val.setAt : Val → List Nat → Val → Val
  | _, [], w => w
  | .struct_ t fs, i :: p, w =>
    match fs[i]? with
    -- The field is taken out first, so it is not shared while it is updated.
    | some f => .struct_ t ((fs.set i default).set i (f.setAt p w))
    | none => .struct_ t fs
  -- `Array.modify` takes the element out while it is updated, so an unshared array is
  -- updated in place.
  | .array t es, i :: p, w => .array t (es.modify i (·.setAt p w))
  | v, _, _ => v

def load (l : Loc) : GM Val := do
  match (← get).cells[l.cell]? >>= (·.at l.path) with
  | some (.poison what) => throw (.unsupported what)
  | some v => pure v
  | none => stuck "a pointer to nothing"

/-- A variable's value; a poisoned one stops the run. -/
def loadCell (cell : Nat) : GM Val := do
  match (← get).cells[cell]? with
  | some (.poison what) => throw (.unsupported what)
  | some v => pure v
  | none => stuck "no such variable"

def storeCell (cell : Nat) (v : Val) : GM Unit :=
  modify fun s => { s with cells := s.cells.set! cell v }

def store (l : Loc) (w : Val) : GM Unit := do
  match (← get).cells[l.cell]? >>= (·.at l.path) with
  | none => stuck "a store through a pointer to nothing"
  -- A store into a value GoCore cannot read (`Val.poison`) stops the run.
  | some (.poison what) => if !l.path.isEmpty then throw (.unsupported what)
  | some _ => pure ()
  -- `modify` on the cell, so an unshared value is updated in place.
  modify fun s => { s with cells := s.cells.modify l.cell (·.setAt l.path w) }

end Gala.GoCore.Full
