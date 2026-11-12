/-
  # `Println` and `Print` for S1 values

  `Println(a, b)` is Go's `fmt.Println(a, b)` and `Print(a, b)` is `fmt.Print(a, b)`
  (docs/GALA.MD: `Println`/`Print` print their arguments; they are rewritten to `fmt`).
  So the text a value prints is what Go's `%v` prints for the Go value GALA builds:

  * `int`, `bool`, `string`: as Go prints them (a string without quotes).
  * a tuple: its `String()` method, `(a, b)`, each element printed with `%v`.
  * a sealed value: its generated `String()` method, `Case(f1, f2)`, or `Case()`.
  * a shorthand struct (`struct Pt(...)`): its generated `String()` method, `Pt(f1, f2)`,
    each field printed with `%v`, its `Immutable` wrapper looked through (docs/GALA.MD,
    "How Values Print"). A struct with a field named `String` has no such method.
  * a block-form struct (`type T struct { ... }`) has no `String()` method, so `fmt`
    prints its Go struct: `{f1 f2}`. A `val` field is stored in an `Immutable[T]`
    wrapper and a `var` field is not. `fmt` calls a method (`Immutable`'s `Format`, a
    tuple's, sealed type's or shorthand struct's `String`) only on a value it reaches
    through exported fields; through an unexported (lowercase) field it prints the raw
    Go struct instead: `{1}` for an `Immutable[int]`, and for a sealed value its Go
    layout, every case's fields and then the case tag.

  `Println` separates its operands with spaces and ends with a newline. `Print` adds a
  space between two operands only when neither is a string.
-/
import Gala.Scope

namespace Gala.Spec.Print

/-- Printing fails only for a value whose Go text is not deterministic (a pointer's
    address) or that S1 cannot lay out; the string names why. -/
abbrev P := Except String

/-- The type name a field declares. -/
def fieldTy (f : Field) : P String :=
  match f.type with
  | some (.name t) => pure t
  | _ => throw "print-field-type"

/-- A `val` field is stored in an `Immutable` wrapper, which prints raw as `{v}`. -/
def wrapImm (f : Field) (s : String) : String := if f.mutable then s else "{" ++ s ++ "}"

/-- The Go fields of a sealed type's struct, in order: each case's fields, a field
    shared by cases that declare it with the same type, then the tag. Returns each
    field's name and type name. Cases that give one name two types are laid out under
    renamed fields; S1 leaves that layout out. -/
def sealedLayout (vs : List Variant) : P (List (String × String)) :=
  vs.foldlM (init := []) fun acc v => v.fields.foldlM (init := acc) fun acc f => do
    let t ← fieldTy f
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

/-- How deep printing follows nested types; a Go value type cannot nest forever. -/
def printDepth : Nat := 256

/-- The raw Go text of a zero value of type `t` (`depth` bounds nested structs). -/
def rawZero (ds : Decls) : Nat → String → P String
  | 0, _ => throw "print-depth"
  | _ + 1, "int" | _ + 1, "int64" => pure "0"
  | _ + 1, "bool" => pure "false"
  | _ + 1, "string" => pure ""
  | n + 1, t =>
    match ds.structs.lookup t with
    | some fs => do
      let parts ← fs.mapM fun f => do pure (wrapImm f (← rawZero ds n (← fieldTy f)))
      pure ("{" ++ " ".intercalate parts ++ "}")
    | none =>
    match ds.sealeds.lookup t with
    | some vs => do
      let parts ← (← sealedLayout vs).mapM fun (_, ft) =>
        if ft == t then pure "<nil>" else do pure ("{" ++ (← rawZero ds n ft) ++ "}")
      pure ("{" ++ " ".intercalate (parts ++ ["0"]) ++ "}")
    | none => throw s!"print-type-{t}"

/-- The Go fields of a sealed value printed raw, given its layout, the names of its active
    case's fields and their raw texts: an active field holds its value, every other field
    its zero value, each in an `Immutable`. A field of the sealed type itself is a
    pointer: `<nil>` when unset, an address (not deterministic) when set. -/
def rawSealed (ds : Decls) (ty : String) :
    List (String × String) → List String → List String → P (List String)
  | [], _, _ => pure []
  | (name, ft) :: rest, active, texts => do
    let s ← match active.idxOf? name with
      | some i =>
        if ft == ty then throw "print-pointer"
        else match texts[i]? with
          | some t => pure ("{" ++ t ++ "}")
          | none => throw "print-sealed-layout"
      | none =>
        if ft == ty then pure "<nil>" else do pure ("{" ++ (← rawZero ds printDepth ft) ++ "}")
    pure (s :: (← rawSealed ds ty rest active texts))

/-- Tuple fields `V1`, `V2`, … are `val` fields: each sits in an `Immutable`. -/
private def tupleField : Field := { name := "V", type := none, mutable := false }

mutual
/-- `%v` of a value. With `methods`, it was reached as an operand of `Println` or through
    exported fields and `Format`/`String` methods, so a tuple's or sealed value's
    `String` runs. Without, it was reached through an unexported field, and `fmt` prints
    the Go struct. -/
def fmtV (ds : Decls) (methods : Bool) : Val → P String
  | .int i => pure (toString i)
  | .bool b => pure (if b then "true" else "false")
  | .str s => pure s
  | .unit => throw "print-unit"
  | .tuple vs => do
    if methods then pure ("(" ++ ", ".intercalate (← fmtVs ds true vs) ++ ")")
    else pure ("{" ++ " ".intercalate ((← fmtVs ds false vs).map (wrapImm tupleField)) ++ "}")
  | .struct_ ty fs => do
    let fields := (ds.structs.lookup ty).getD []
    if methods && ds.shorthands.contains ty && !fields.any (·.name == "String") then
      -- Its generated `String`: each field through `%v`, an `Immutable` looked through.
      pure (ty ++ "(" ++ ", ".intercalate (← fmtVs ds true fs) ++ ")")
    else
      pure ("{" ++ " ".intercalate (← fmtFields ds methods fields fs) ++ "}")
  | .sealed ty c fs => do
    if methods then pure (c ++ "(" ++ ", ".intercalate (← fmtVs ds true fs) ++ ")")
    else
      let vs := (ds.sealeds.lookup ty).getD []
      let active := (ds.caseFields ty c).map (·.name)
      let parts ← rawSealed ds ty (← sealedLayout vs) active (← fmtVs ds false fs)
      pure ("{" ++ " ".intercalate (parts ++ [toString (caseTag vs c)]) ++ "}")
/-- `%v` of each value. -/
def fmtVs (ds : Decls) (methods : Bool) : List Val → P (List String)
  | [] => pure []
  | v :: vs => do pure ((← fmtV ds methods v) :: (← fmtVs ds methods vs))
/-- The fields of a plain struct. With methods allowed, an exported field calls its
    methods (an `Immutable`'s `Format` prints the value it holds); any other field
    prints raw, a `val` field inside its `Immutable`. -/
def fmtFields (ds : Decls) (methods : Bool) : List Field → List Val → P (List String)
  | f :: fs, v :: vs => do
    let s ← if methods && capitalized f.name then fmtV ds true v
      else do pure (wrapImm f (← fmtV ds false v))
    pure (s :: (← fmtFields ds methods fs vs))
  | _, _ => pure []
end


/-- The text `Println(args)` prints: operands separated by spaces, then a newline. -/
def println (ds : Decls) (args : List Val) : P String := do
  pure (" ".intercalate (← fmtVs ds true args) ++ "\n")

/-- The value of an interpolated string `s"..."` whose parts evaluated to `vs`
    (docs/GALA.MD, "String Interpolation"). The transpiler writes `fmt.Sprintf` with a
    verb per embedded expression, chosen from its type: `%d` for an `int`, `%s` for a
    `string`, `%t` for a `bool` and `%v` for anything else. On an S1 value each prints
    what `%v` prints, as `Println` does; a literal run is a string and prints itself. -/
def interp (ds : Decls) (vs : List Val) : P String := do
  pure (String.join (← fmtVs ds true vs))

/-- The text `Print(args)` prints: a space between two operands when neither is a
    string. -/
def print (ds : Decls) (args : List Val) : P String := do
  let strs ← fmtVs ds true args
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
