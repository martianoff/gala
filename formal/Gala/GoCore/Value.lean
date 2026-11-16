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
  | int | rune | bool | string | nil
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
  /-- An interface type, by its methods' names (Go spec, "Interface types"). Method
      sets decide assertions; signatures are Go's type checker's business. -/
  | iface (methods : List String)
  | func_ (params results : List Ty) (variadic : Bool)
  /-- The results of a call with several, `(T1, T2)`. -/
  | tuple (ts : List Ty)
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
  | .iface ms, .iface ns => ms == ns
  | .func_ ps rs v, .func_ qs ss w => Ty.beqs ps qs && Ty.beqs rs ss && v == w
  | .tuple as, .tuple bs => Ty.beqs as bs
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
  | .named p n args =>
    let q := (p.splitOn "/").getLast!
    let base := if p.isEmpty then n else s!"{q}.{n}"
    if args.isEmpty then base else base ++ "[" ++ ",".intercalate (Ty.texts args) ++ "]"
  | .ptr t => "*" ++ t.text
  | .struct_ fs => "struct { " ++ "; ".intercalate (Ty.fieldTexts fs) ++ " }"
  | .iface [] => "interface {}"
  | .iface ms => "interface { " ++ "; ".intercalate ms ++ " }"
  | .func_ ps rs _ => "func(" ++ ", ".intercalate (Ty.texts ps) ++ ")" ++
      (if rs.isEmpty then "" else " " ++ ", ".intercalate (Ty.texts rs))
  | .tuple ts => "(" ++ ", ".intercalate (Ty.texts ts) ++ ")"
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
  /-- A package-level variable of an imported package whose initializer GoCore could
      not run (it reached `what`); reading it stops the run with `what`. -/
  | poison (what : String)
  deriving Inhabited

def Val.ty : Val → Ty
  | .int t _ | .bool t _ | .str t _ | .struct_ t _ | .ptr t _ | .func_ t _ | .nilIface t
  | .boxed t _ => t
  | .tuple vs => .tuple (vs.map fun _ => .bool)
  | .poison _ => .tuple []

/-- The dynamic value of an interface value; any other value itself. -/
def Val.dyn : Val → Val
  | .boxed _ v => v
  | v => v

/-- Why execution stopped early. -/
inductive Stop
  /-- A panic: its value, and the text Go's runtime prints after `panic: `. -/
  | panic (v : Val) (text : String)
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

/-- The field at `path` inside `v`. -/
def Val.at : Val → List Nat → Option Val
  | v, [] => some v
  | .struct_ _ fs, i :: p => (fs[i]?).bind (·.at p)
  | _, _ => none

/-- `v` with the field at `path` replaced by `w`. -/
def Val.setAt : Val → List Nat → Val → Option Val
  | _, [], w => some w
  | .struct_ t fs, i :: p, w => do
    let f ← fs[i]?
    let f' ← f.setAt p w
    pure (.struct_ t (fs.set i f'))
  | _, _, _ => none

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
  match (← get).cells[l.cell]? >>= (·.setAt l.path w) with
  | some v => storeCell l.cell v
  | none => stuck "a store through a pointer to nothing"

end Gala.GoCore.Full
