/-
  # GoCore JSON decoder

  Reads a GoCore JSON document (`internal/formal/gocoreexport/SCHEMA.md`) into the syntax of
  `Gala/GoCore/Syntax.lean`. It rejects a document whose `gocore` version is not
  `supportedVersion`, and any node whose kind, fields or field types do not follow the
  schema: decoding fails with a message, and the runner reports the input as malformed.

  An `{"unsupported": ...}` node is not an error: it decodes to the `unsupported`
  constructor of the category it stands in, so a run reports it only if it reaches it.
-/
import Lean.Data.Json
import Gala.GoCore.Syntax

namespace Gala.GoCore.Full.Json

open Lean (Json)

/-- The GoCore JSON version this decoder reads. -/
def supportedVersion : Nat := 1

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

private def strs (j : Json) (k : String) : D (List String) := do
  (← arr j k).mapM fun v => match v.getStr? with
    | .ok s => pure s
    | .error _ => throw s!"field `{k}` holds a non-string"

/-- An optional child: `null` is absent. -/
private def opt (j : Json) (k : String) : D (Option Json) := do
  match ← field j k with
  | .null => pure none
  | v => pure (some v)

/-- For an `unrepresentable` node (JSON could not carry it exactly), the construct a
    run that reaches it stops at: `unrepresentable-<name>`. -/
private def unsupportedOf (j : Json) : D (Option String) :=
  match j.getObjVal? "unrepresentable" with
  | .ok (.str s) => .ok (some s!"unrepresentable-{s}")
  | .ok _ => .error "field `unrepresentable` is not a string"
  | .error _ => .ok none

/-- The kinds of the schema GoCore does not run: they decode to `unsupported <kind>`,
    without their children. A kind the schema does not have is malformed. -/
def outsideExprs : List String :=
  ["map-type", "chan-type", "float-lit", "imag-lit"]
def outsideStmts : List String := ["labeled", "select", "send", "go"]

private def kindOf (j : Json) : D String := str j "kind"

private def int (j : Json) (k : String) : D Int := do
  match (← field j k).getInt? with
  | .ok n => pure n
  | .error _ => throw s!"field `{k}` is not an integer"

mutual
partial def decodeExpr (j : Json) : D Expr := do
  if let some k ← unsupportedOf j then return .unsupported k
  match ← kindOf j with
  | "ident" => return .ident (← str j "name")
  | "int-lit" =>
    let v ← str j "value"
    match v.toInt? with
    | some n => return .intLit n
    | none => throw s!"int-lit value `{v}` is not a decimal integer"
  | "string-lit" => return .strLit (← str j "value")
  | "char-lit" => return .charLit (← int j "value")
  | "composite-lit" =>
    return .composite (← optExpr j "type") (← (← arr j "elts").mapM decodeExpr)
  | "key-value" => return .keyValue (← decodeExpr (← field j "key")) (← decodeExpr (← field j "value"))
  | "func-lit" =>
    return .funcLit (← decodeSig (← field j "type")) (← decodeBlock (← field j "body"))
  | "paren" => return .paren (← decodeExpr (← field j "x"))
  | "selector" => return .selector (← decodeExpr (← field j "x")) (← str j "sel")
  | "index" => return .index (← decodeExpr (← field j "x")) (← (← arr j "indices").mapM decodeExpr)
  | "type-assert" =>
    match ← optExpr j "type" with
    | some t => return .typeAssert (← decodeExpr (← field j "x")) t
    | none => return .unsupported "type-switch"
  | "call" =>
    return .call (← decodeExpr (← field j "fun")) (← (← arr j "args").mapM decodeExpr)
      (← bool j "ellipsis")
  | "star" => return .star (← decodeExpr (← field j "x"))
  | "unary" => return .unary (← str j "op") (← decodeExpr (← field j "x"))
  | "binary" =>
    return .binary (← str j "op") (← decodeExpr (← field j "x")) (← decodeExpr (← field j "y"))
  | "struct-type" => return .structType (← (← arr j "fields").mapM decodeField)
  | "interface-type" => return .interfaceType (← (← arr j "elems").mapM decodeField)
  | "func-type" => return .funcType (← decodeSig j)
  | "array-type" => return .arrayType (← optExpr j "len") (← decodeExpr (← field j "elt"))
  | "ellipsis" => return .ellipsis
  | "slice-expr" =>
    return .sliceExpr (← decodeExpr (← field j "x")) (← optExpr j "low") (← optExpr j "high")
      (← optExpr j "max")
  | k =>
    if outsideExprs.contains k then return .unsupported k
    throw s!"expected an expression, got kind `{k}`"
partial def optExpr (j : Json) (k : String) : D (Option Expr) := do
  match ← opt j k with
  | none => pure none
  | some e => some <$> decodeExpr e
partial def decodeField (j : Json) : D Field := do
  return .mk (← strs j "names") (← decodeExpr (← field j "type"))
partial def decodeSig (j : Json) : D Sig := do
  if (← kindOf j) != "func-type" then throw "expected a func-type"
  return .mk (← (← arr j "params").mapM decodeField) (← (← arr j "results").mapM decodeField)
    (← bool j "variadic")
/-- A `block` node, as its statements. -/
partial def decodeBlock (j : Json) : D (List Stmt) := do
  if (← kindOf j) != "block" then throw "expected a block"
  (← arr j "stmts").mapM decodeStmt
partial def optStmt (j : Json) (k : String) : D (Option Stmt) := do
  match ← opt j k with
  | none => pure none
  | some s => some <$> decodeStmt s
partial def decodeStmt (j : Json) : D Stmt := do
  if let some k ← unsupportedOf j then return .unsupported k
  match ← kindOf j with
  | "block" => return .block (← decodeBlock j)
  | "decl-stmt" => return .decl (← decodeDecl (← field j "decl"))
  | "expr-stmt" => return .expr (← decodeExpr (← field j "x"))
  | "inc-dec" => return .incDec (← str j "op") (← decodeExpr (← field j "x"))
  | "assign" =>
    return .assign (← str j "tok") (← (← arr j "lhs").mapM decodeExpr) (← (← arr j "rhs").mapM decodeExpr)
  | "return" => return .ret (← (← arr j "results").mapM decodeExpr)
  | "branch" =>
    let tok ← str j "tok"
    if !(← str j "label").isEmpty then return .unsupported "labeled-branch"
    if tok == "break" || tok == "continue" then return .branch tok
    return .unsupported tok
  | "if" =>
    return .if_ (← optStmt j "init") (← decodeExpr (← field j "cond")) (← decodeBlock (← field j "body"))
      (← optStmt j "else")
  | "switch" =>
    return .switch (← optStmt j "init") (← optExpr j "tag") (← (← arr j "clauses").mapM decodeClause)
  | "type-switch" =>
    let clauses ← (← arr j "clauses").mapM decodeClause
    -- The guard: `x := y.(type)` or `y.(type)`.
    let a ← field j "assign"
    let (bind, guard) ← match ← kindOf a with
      | "assign" =>
        match ← arr a "lhs", ← arr a "rhs" with
        | [l], [r] => pure (some (← str l "name"), r)
        | _, _ => throw "a type switch guard with several sides"
      | "expr-stmt" => pure (none, ← field a "x")
      | k => throw s!"a type switch guard of kind `{k}`"
    if (← kindOf guard) != "type-assert" then throw "a type switch guard that is not `.(type)`"
    return .typeSwitch (← optStmt j "init") bind (← decodeExpr (← field guard "x")) clauses
  | "for" =>
    return .for_ (← optStmt j "init") (← optExpr j "cond") (← optStmt j "post")
      (← decodeBlock (← field j "body"))
  | "range" =>
    return .range (← optExpr j "key") (← optExpr j "value") (← str j "tok") (← decodeExpr (← field j "x"))
      (← decodeBlock (← field j "body"))
  | "empty" => return .empty
  | "defer" => return .defer_ (← decodeExpr (← field j "call"))
  | k =>
    if outsideStmts.contains k then return .unsupported k
    throw s!"expected a statement, got kind `{k}`"
/-- A switch clause: `case list:` or, with an empty list, `default:`. -/
partial def decodeClause (c : Json) : D Clause := do
  let list ← (← arr c "list").mapM decodeExpr
  return Clause.mk list list.isEmpty (← (← arr c "body").mapM decodeStmt)
partial def decodeDecl (j : Json) : D Decl := do
  if let some k ← unsupportedOf j then return .unsupported k
  match ← kindOf j with
  | "func-decl" =>
    let recv ← match ← opt j "recv" with
      | none => pure none
      | some r => some <$> decodeField r
    let body ← match ← opt j "body" with
      | none => pure none
      | some b => some <$> decodeBlock b
    return .func (← str j "name") recv (← (← arr j "typeParams").mapM decodeField)
      (← decodeSig (← field j "type")) body
  | "type-decl" =>
    let specs ← (← arr j "specs").mapM fun s => do
      return TypeSpec.mk (← str s "name") (← (← arr s "typeParams").mapM decodeField)
        (← bool s "alias") (← decodeExpr (← field s "type"))
    return .type specs
  | "value-decl" =>
    let specs ← (← arr j "specs").mapM fun s => do
      return Spec.mk (← strs s "names") (← optExpr s "type") (← (← arr s "values").mapM decodeExpr)
    return .value (← bool j "const") specs
  | k => throw s!"expected a declaration, got kind `{k}`"
end

def decodeFile (j : Json) : D File := do
  if (← kindOf j) != "file" then throw "expected a file"
  let imports ← (← arr j "imports").mapM fun i => do return Import.mk (← str i "name") (← str i "path")
  return { name := ← str j "name", imports, decls := ← (← arr j "decls").mapM decodeDecl }

/-- Decode a GoCore JSON document. -/
def decode (j : Json) : D Program := do
  let v ← match (← field j "gocore").getNat? with
    | .ok v => pure v
    | .error _ => throw "field `gocore` is not a version number"
  unless v == supportedVersion do
    throw s!"GoCore JSON version {v}; this decoder reads version {supportedVersion}"
  let pkgs ← (← arr j "packages").mapM fun p => do
    return Package.mk (← str p "path") (← str p "name") (← (← arr p "files").mapM decodeFile)
  return { packages := pkgs }

/-- Parse and decode a GoCore JSON text. -/
def parseProgram (text : String) : D Program := do
  decode (← Lean.Json.parse text)

end Gala.GoCore.Full.Json
