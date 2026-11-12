/-
  # Core JSON v1 decoder

  Reads a Core JSON document (`internal/formal/coreexport/SCHEMA.md`) into the syntax of
  `Gala/Syntax.lean`. It rejects a document whose `core` version is not 1, and any node
  whose kind, fields or field types do not follow the schema: decoding fails with a
  message, and `galaspec` reports the input as malformed.

  An `{"unsupported": ...}` node is not an error: it decodes to the `unsupported`
  constructor of the syntax category it stands in, so execution reports it only if it
  reaches it.

  Uses `Lean.Data.Json` from Lean's own library (no Lake dependency).
-/
import Lean.Data.Json
import Gala.Syntax

namespace Gala.Spec.Json

open Lean (Json)

/-- The Core JSON versions this decoder reads. -/
def supportedCore : Nat := 1

/-- Decoding either succeeds or names what is malformed. -/
abbrev D := Except String

private def field (j : Json) (k : String) : D Json :=
  match j.getObjVal? k with
  | .ok v => .ok v
  | .error _ => .error s!"missing field `{k}`"

private def str (j : Json) (k : String) : D String := do
  match (← field j k).getStr? with
  | .ok s => pure s
  | .error _ => throw s!"field `{k}` is not a string"

private def bool (j : Json) (k : String) : D Bool := do
  match (← field j k).getBool? with
  | .ok b => pure b
  | .error _ => throw s!"field `{k}` is not a bool"

private def arr (j : Json) (k : String) : D (List Json) := do
  match (← field j k).getArr? with
  | .ok a => pure a.toList
  | .error _ => throw s!"field `{k}` is not a list"

/-- An optional child: `null` is absent. -/
private def opt (j : Json) (k : String) : D (Option Json) := do
  match ← field j k with
  | .null => pure none
  | v => pure (some v)

/-- The `unsupported` name of a node, if it is an unsupported node. -/
def unsupportedOf (j : Json) : D (Option String) :=
  match j.getObjVal? "unsupported" with
  | .ok (.str s) => .ok (some s)
  | .ok _ => .error "field `unsupported` is not a string"
  | .error _ => .ok none

private def kindOf (j : Json) : D String := str j "kind"

/-- The value of a Go integer literal: decimal, `0x`, `0b` or `0o`, with optional `_`
    separators. A leading-zero octal literal is exported as unsupported, never here. -/
def parseIntLit (text : String) : Option Int :=
  let s := text.toList.filter (· != '_')
  let digits (base : Nat) (cs : List Char) : Option Nat :=
    if cs.isEmpty then none else
    cs.foldl (fun acc c => acc.bind fun n =>
      let d? : Option Nat :=
        if c.isDigit then some (c.toNat - '0'.toNat)
        else if 'a' ≤ c ∧ c ≤ 'f' then some (c.toNat - 'a'.toNat + 10)
        else if 'A' ≤ c ∧ c ≤ 'F' then some (c.toNat - 'A'.toNat + 10)
        else none
      d?.bind fun d => if d < base then some (n * base + d) else none) (some 0)
  let n? := match s with
    | '0' :: x :: rest =>
      if x == 'x' || x == 'X' then digits 16 rest
      else if x == 'b' || x == 'B' then digits 2 rest
      else if x == 'o' || x == 'O' then digits 8 rest
      else digits 10 s
    | _ => digits 10 s
  n?.map Int.ofNat

/-- A type node. -/
def decodeTy (j : Json) : D Ty := do
  if let some k ← unsupportedOf j then return .unsupported k
  match ← kindOf j with
  | "type-name" => return .name (← str j "name")
  | k => throw s!"expected a type, got kind `{k}`"

private def optTy (j : Json) (k : String) : D (Option Ty) := do
  match ← opt j k with
  | none => pure none
  | some t => some <$> decodeTy t

/-- A literal node: `int`, `bool` or `string`. An `int` whose hint is not `int` is
    rejected: S1 has one integer type, and the hint is checked against it. -/
def decodeLit (j : Json) : D Lit := do
  match ← kindOf j with
  | "int" =>
    let text ← str j "text"
    match j.getObjVal? "hint" with
    | .ok .null | .error _ => pure ()
    | .ok h =>
      let t ← str h "type"
      unless t == "int" do throw s!"int literal `{text}` has hint `{t}`, not `int`"
    match parseIntLit text with
    | some v => return .int v
    | none => throw s!"bad int literal `{text}`"
  | "bool" => return .bool (← bool j "value")
  | "string" => return .str (← str j "value")
  | k => throw s!"expected a literal, got kind `{k}`"

/-- A pattern node. -/
partial def decodePat (j : Json) : D Pat := do
  if let some k ← unsupportedOf j then return .unsupported k
  match ← kindOf j with
  | "pat-wild" => return .wild
  | "pat-name" => return .name (← str j "name")
  | "pat-lit" =>
    let v ← field j "value"
    if (← kindOf v) == "unary" then
      unless (← str v "op") == "-" do throw "a literal pattern's unary operator is not `-`"
      match ← decodeLit (← field v "operand") with
      | .int n => return .lit (.int (-n))
      | _ => throw "`-` applied to a non-integer literal pattern"
    return .lit (← decodeLit v)
  | "pat-tuple" => return .tuple (← (← arr j "elems").mapM decodePat)
  | "pat-extract" =>
    let c ← field j "callee"
    let args ← (← arr j "args").mapM decodePat
    if let some k ← unsupportedOf c then return .unsupported k
    match ← kindOf c with
    | "name" => return .extract (← str c "name") args
    | "select" => return .unsupported "qualified-extractor"
    | k => throw s!"extractor callee of kind `{k}`"
  | k => throw s!"expected a pattern, got kind `{k}`"

/-- A field of a struct or variant. -/
def decodeField (j : Json) : D Field := do
  if let some k ← unsupportedOf j then throw s!"unsupported field ({k}) cannot be modelled"
  unless (← kindOf j) == "field" do throw "expected a field"
  return { name := ← str j "name", type := ← optTy j "type", mutable := ← bool j "mutable" }

mutual
/-- An expression or statement node. -/
partial def decodeExpr (j : Json) : D Expr := do
  if let some k ← unsupportedOf j then return .unsupported k
  match ← kindOf j with
  | "int" | "bool" | "string" => return .lit (← decodeLit j)
  | "name" => return .name (← str j "name")
  | "unary" => return .unary (← str j "op") (← decodeExpr (← field j "operand"))
  | "binary" =>
    return .binary (← str j "op") (← decodeExpr (← field j "left")) (← decodeExpr (← field j "right"))
  | "call" => return .call (← decodeExpr (← field j "callee")) (← (← arr j "args").mapM decodeExpr)
  | "select" => return .select (← decodeExpr (← field j "target")) (← str j "name")
  | "paren" => return .paren (← decodeExpr (← field j "expr"))
  | "tuple" => return .tuple (← (← arr j "elems").mapM decodeExpr)
  | "if-expr" =>
    return .ifExpr (← decodeExpr (← field j "cond")) (← decodeExpr (← field j "then"))
      (← decodeExpr (← field j "else"))
  | "match" => return .mtch (← decodeExpr (← field j "subject")) (← (← arr j "cases").mapM decodeCase)
  | "block" => return .block (← (← arr j "stmts").mapM decodeExpr)
  | "if" =>
    let e ← match ← opt j "else" with
      | none => pure none
      | some e => some <$> decodeExpr e
    return .ifStmt (← decodeExpr (← field j "cond")) (← decodeExpr (← field j "then")) e
  | "return" =>
    match ← opt j "value" with
    | none => return .ret none
    | some v => return .ret (some (← decodeExpr v))
  | "assign" =>
    return .assign (← str j "op") (← (← arr j "targets").mapM decodeExpr)
      (← (← arr j "values").mapM decodeExpr)
  | "incdec" => return .incdec (← str j "op") (← decodeExpr (← field j "target"))
  | "val" => decodeDecl .val j
  | "var" => decodeDecl .var j
  | "func" => return .localDecl "local-func"
  | "struct" => return .localDecl "local-struct"
  | k => throw s!"expected an expression or statement, got kind `{k}`"

/-- A `val` or `var` in a block. -/
partial def decodeDecl (m : Mut) (j : Json) : D Expr := do
  let names ← (← arr j "names").mapM fun n => match n.getStr? with
    | .ok s => pure s
    | .error _ => throw "a declared name is not a string"
  return .decl m names (← bool j "destructure") (← optTy j "type") (← (← arr j "values").mapM decodeExpr)

/-- A `case`: pattern, optional guard, body. -/
partial def decodeCase (j : Json) : D (Pat × Option Expr × Expr) := do
  unless (← kindOf j) == "case" do throw "expected a case"
  let g ← match ← opt j "guard" with
    | none => pure none
    | some g => some <$> decodeExpr g
  return (← decodePat (← field j "pattern"), g, ← decodeExpr (← field j "body"))
end

/-- A top-level declaration. -/
def decodeTop (j : Json) : D Decl := do
  if let some k ← unsupportedOf j then return .unsupported k
  match ← kindOf j with
  | "func" =>
    let params ← (← arr j "params").mapM fun p => do
      if let some k ← unsupportedOf p then return Sum.inl k
      unless (← kindOf p) == "param" do throw "expected a param"
      return Sum.inr ({ name := ← str p "name", type := ← optTy p "type" } : Param)
    -- A function with an unsupported parameter is unsupported as a whole.
    if let some (.inl k) := params.find? (·.isLeft) then return .unsupported k
    let ps := params.filterMap fun | .inr p => some p | .inl _ => none
    let bodyJ ← field j "body"
    let exprBody := (← kindOf bodyJ <|> pure "") != "block"
    return .func { name := ← str j "name", params := ps, result := ← optTy j "result",
                   body := ← decodeExpr bodyJ, exprBody }
  | "struct" => return .struct_ (← str j "name") (← (← arr j "fields").mapM decodeField)
  | "sealed" =>
    let vs ← (← arr j "variants").mapM fun v => do
      unless (← kindOf v) == "variant" do throw "expected a variant"
      return ({ name := ← str v "name", isDefault := ← bool v "default",
                fields := ← (← arr v "fields").mapM decodeField } : Variant)
    return .sealed (← str j "name") vs
  | "val" | "var" =>
    match ← decodeExpr j with
    | .decl m ns d t vs => return .global m ns d t vs
    | _ => throw "bad top-level declaration"
  | k => throw s!"expected a top-level declaration, got kind `{k}`"

/-- A whole document. Rejects a `core` version other than `supportedCore`. -/
def decodeProgram (j : Json) : D Program := do
  match (← field j "core").getNat? with
  | .ok v => unless v == supportedCore do throw s!"unknown Core JSON version {v}; galaspec reads {supportedCore}"
  | .error _ => throw "field `core` is not a natural number"
  let files ← (← arr j "files").mapM fun f => do
    unless (← kindOf f) == "file" do throw "expected a file"
    return ({ path := ← str f "path", package := ← str f "package",
              decls := ← (← arr f "decls").mapM decodeTop } : File)
  return { files }

/-- Parse and decode a Core JSON text. -/
def parse (text : String) : D Program := do
  match Lean.Json.parse text with
  | .ok j => decodeProgram j
  | .error e => throw s!"not JSON: {e}"

end Gala.Spec.Json
