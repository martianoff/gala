/-
  # `Println` and `Print` for S1 values

  `Println(a, b)` is Go's `fmt.Println(a, b)` and `Print(a, b)` is `fmt.Print(a, b)`
  (docs/GALA.MD: `Println`/`Print` print their arguments; they are rewritten to `fmt`).
  So the text a value prints is what Go's `%v` prints for the Go value GALA builds:

  * `int`, `bool`, `string`: as Go prints them (a string without quotes).
  * a tuple: its `String()` method, `(a, b)`, each element printed with `%v`.
  * a sealed value: its generated `String()` method, `Case(f1, f2)`, or `Case()`.
  * a plain struct has no `String()` method, so `fmt` prints its Go struct: `{f1 f2}`.
    A `val` field is stored in an `Immutable[T]` wrapper and a `var` field is not.
    `fmt` calls a method (`Immutable`'s `Format`, a tuple's or sealed type's `String`)
    only on a value it reaches through exported fields; through an unexported (lowercase)
    field it prints the raw Go struct instead: `{1}` for an `Immutable[int]`, and for a
    sealed value its Go layout, every case's fields and then the case tag.

  `Println` separates its operands with spaces and ends with a newline. `Print` adds a
  space between two operands only when neither is a string.

  This is the format `master` prints today. A pending change prints plain structs as
  `Pt(1, 2)`; when it lands, `fmtStruct` changes with it (see COVERAGE.md).
-/
import Gala.Scope

namespace Gala.Spec.Print

/-- Printing fails only for a value whose Go text is not deterministic (a pointer's
    address) or that S1 cannot lay out; the string names why. -/
abbrev P := Except String

/-- Go exports a field whose name starts with an upper-case letter. -/
def exported (name : String) : Bool := capitalized name

/-- The Go fields of a sealed type's struct, in order: each case's fields, a field
    shared by cases that declare it with the same type, then the tag. Returns each
    field's name and type name. Cases that give one name two types are laid out under
    renamed fields; S1 leaves that layout out. -/
def sealedLayout (vs : List Variant) : P (List (String × String)) :=
  vs.foldlM (init := []) fun acc v => v.fields.foldlM (init := acc) fun acc f => do
    let t ← match f.type with
      | some (.name t) => pure t
      | _ => throw "print-sealed-layout"
    match acc.lookup f.name with
    | some t' => if t' == t then pure acc else throw "print-sealed-layout"
    | none => pure (acc ++ [(f.name, t)])

/-- The Go tag of a case: 0 for the default case, else 1, 2, … in declaration order
    over the other cases. -/
def caseTag (vs : List Variant) (name : String) : Nat :=
  match vs.find? (·.name == name) with
  | some v => if v.isDefault then 0 else
      ((vs.filter (!·.isDefault)).takeWhile (·.name != name)).length + 1
  | none => 0

/-- The raw Go text of a zero value of type `t` (`depth` bounds nested structs). -/
def rawZero (ds : Decls) : Nat → String → P String
  | 0, _ => throw "print-depth"
  | _ + 1, "int" | _ + 1, "int64" => pure "0"
  | _ + 1, "bool" => pure "false"
  | _ + 1, "string" => pure ""
  | n + 1, t =>
    match ds.structs.lookup t with
    | some fs => do
      let parts ← fs.mapM fun f => do
        let ft ← match f.type with | some (.name ft) => pure ft | _ => throw "print-field-type"
        let z ← rawZero ds n ft
        pure (if f.mutable then z else "{" ++ z ++ "}")
      pure ("{" ++ " ".intercalate parts ++ "}")
    | none =>
    match ds.sealeds.lookup t with
    | some vs => do
      let layout ← sealedLayout vs
      let parts ← layout.mapM fun (_, ft) =>
        if ft == t then pure "<nil>" else do pure ("{" ++ (← rawZero ds n ft) ++ "}")
      pure ("{" ++ " ".intercalate (parts ++ ["0"]) ++ "}")
    | none => throw s!"print-type-{t}"

mutual
/-- `%v` of a value reached with methods allowed (an operand of `Println`, or through
    exported fields and `Format`/`String` methods). -/
def fmtV (ds : Decls) (n : Nat) : Val → P String
  | .int i => pure (toString i)
  | .bool b => pure (if b then "true" else "false")
  | .str s => pure s
  | .unit => throw "print-unit"
  | .tuple vs => do pure ("(" ++ ", ".intercalate (← fmtVs ds n vs) ++ ")")
  | .sealed _ c fs => do pure (c ++ "(" ++ ", ".intercalate (← fmtVs ds n fs) ++ ")")
  | .struct_ ty fs => do
    match n with
    | 0 => throw "print-depth"
    | n + 1 =>
      let fields := (ds.structs.lookup ty).getD []
      pure ("{" ++ " ".intercalate (← fmtFields ds n fields fs) ++ "}")
/-- `%v` of each value, methods allowed. -/
def fmtVs (ds : Decls) (n : Nat) : List Val → P (List String)
  | [] => pure []
  | v :: vs => do pure ((← fmtV ds n v) :: (← fmtVs ds n vs))
/-- The fields of a plain struct printed with methods allowed: an exported field calls
    its methods, an unexported one prints raw; a `val` field is in an `Immutable`. -/
def fmtFields (ds : Decls) (n : Nat) : List Field → List Val → P (List String)
  | f :: fs, v :: vs => do
    let s ← if exported f.name then fmtV ds n v
      else if f.mutable then rawV ds n v
      else do pure ("{" ++ (← rawV ds n v) ++ "}")
    pure (s :: (← fmtFields ds n fs vs))
  | _, _ => pure []
/-- `%v` of a value reached through an unexported field: no methods, the Go struct. -/
def rawV (ds : Decls) (n : Nat) : Val → P String
  | .int i => pure (toString i)
  | .bool b => pure (if b then "true" else "false")
  | .str s => pure s
  | .unit => throw "print-unit"
  | .tuple vs => do pure ("{" ++ " ".intercalate (← rawVs ds n vs) ++ "}")
  | .struct_ ty fs => do
    match n with
    | 0 => throw "print-depth"
    | n + 1 =>
      let fields := (ds.structs.lookup ty).getD []
      pure ("{" ++ " ".intercalate (← rawFields ds n fields fs) ++ "}")
  | .sealed ty c fs => do
    match n with
    | 0 => throw "print-depth"
    | n + 1 =>
      let vs := (ds.sealeds.lookup ty).getD []
      let layout ← sealedLayout vs
      let active := ((vs.find? (·.name == c)).map (·.fields)).getD []
      let parts ← rawSealed ds n ty layout (active.map (·.name)) fs
      pure ("{" ++ " ".intercalate (parts ++ [toString (caseTag vs c)]) ++ "}")
/-- Raw `%v` of each value. -/
def rawVs (ds : Decls) (n : Nat) : List Val → P (List String)
  | [] => pure []
  | v :: vs => do pure ((← rawV ds n v) :: (← rawVs ds n vs))
/-- The fields of a plain struct printed raw. -/
def rawFields (ds : Decls) (n : Nat) : List Field → List Val → P (List String)
  | f :: fs, v :: vs => do
    let s ← rawV ds n v
    pure ((if f.mutable then s else "{" ++ s ++ "}") :: (← rawFields ds n fs vs))
  | _, _ => pure []
/-- The Go fields of a sealed value printed raw: the active case's fields hold its
    values, every other field its zero value. A field of the sealed type itself is a
    pointer: `<nil>` when unset, an address (not deterministic) when set. -/
def rawSealed (ds : Decls) (n : Nat) (ty : String) :
    List (String × String) → List String → List Val → P (List String)
  | [], _, _ => pure []
  | (name, ft) :: rest, active, fs => do
    let s ← match active.idxOf? name with
      | some i =>
        if ft == ty then throw "print-pointer"
        else match fs[i]? with
          | some v => do pure ("{" ++ (← rawV ds n v) ++ "}")
          | none => throw "print-sealed-layout"
      | none =>
        if ft == ty then pure "<nil>" else do pure ("{" ++ (← rawZero ds n ft) ++ "}")
    pure (s :: (← rawSealed ds n ty rest active fs))
end

/-- How deep printing follows nested structs; a Go value type cannot nest forever. -/
def printDepth : Nat := 256

/-- The text `Println(args)` prints: operands separated by spaces, then a newline. -/
def println (ds : Decls) (args : List Val) : P String := do
  pure (" ".intercalate (← fmtVs ds printDepth args) ++ "\n")

/-- The text `Print(args)` prints: a space between two operands when neither is a
    string. -/
def print (ds : Decls) (args : List Val) : P String := do
  let strs ← fmtVs ds printDepth args
  let isStr : Val → Bool := fun | .str _ => true | _ => false
  let rec go : List Val → List String → Option Val → String
    | v :: vs, s :: ss, prev =>
      let sep := match prev with
        | some p => if !isStr p && !isStr v then " " else ""
        | none => ""
      sep ++ s ++ go vs ss (some v)
    | _, _, _ => ""
  pure (go args strs none)

end Gala.Spec.Print
