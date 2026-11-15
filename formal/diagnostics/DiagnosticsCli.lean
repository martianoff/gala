/-
  The command line of the diagnostics port, without the `main` binding.

  `Main.lean` is the standalone `diagnostics` executable; `gala-lean`
  (`formal/gala-lean`) imports this module for its `diag` subcommand. The
  batch protocol lives here so both binaries share it.
-/
import Diagnostics

open Gala.Diagnostics

namespace Gala.Diagnostics.Cli

/-- Read a stream to EOF. -/
def readAll (stream : IO.FS.Stream) : IO ByteArray := do
  let mut acc := ByteArray.empty
  let mut chunk ← stream.read 65536
  while chunk.size > 0 do
    acc := acc.append chunk
    chunk ← stream.read 65536
  return acc

def hexDigits : List Char := "0123456789abcdef".toList

def hexByte (b : UInt8) : String :=
  let n := b.toNat
  String.ofList [hexDigits[n / 16]!, hexDigits[n % 16]!]

def hexBytes (a : ByteArray) : String :=
  a.toList.foldl (fun acc b => acc ++ hexByte b) ""

def hexVal (c : Char) : Option Nat :=
  let n := c.toNat
  if n >= 0x30 && n <= 0x39 then some (n - 0x30)
  else if n >= 0x61 && n <= 0x66 then some (n - 0x61 + 10)
  else if n >= 0x41 && n <= 0x46 then some (n - 0x41 + 10)
  else none

def decodeHexAux : List Char → ByteArray → Option ByteArray
  | [], acc => some acc
  | c1 :: c2 :: rest, acc =>
    match hexVal c1, hexVal c2 with
    | some hi, some lo => decodeHexAux rest (acc.push (UInt8.ofNat (hi * 16 + lo)))
    | _, _ => none
  | [_], _ => none

def decodeHex (s : String) : Option ByteArray :=
  decodeHexAux s.toList ByteArray.empty

def decodeString (s : String) : Option String :=
  decodeHex s |>.bind String.fromUTF8?

/-- One error of a case: `d:code:msg:hint:file:line:column:endColumn` (hex
strings, decimal numbers) or `x:msg-hex` for an untyped error. -/
def parseError (spec : String) : Option Error :=
  match spec.splitOn ":" with
  | ["d", code, msg, hint, file, line, column, endColumn] =>
    match decodeString code, decodeString msg, decodeString hint, decodeString file,
        line.toNat?, column.toNat?, endColumn.toNat? with
    | some code, some msg, some hint, some file, some line, some column, some endColumn =>
      some (.diag { code := code, message := msg, hint := hint, filePath := file,
                    line := line, column := column, endColumn := endColumn })
    | _, _, _, _, _, _, _ => none
  | ["x", msg] =>
    decodeString msg |>.map fun msg => Error.raw msg
  | _ => none

structure Case where
  color : Bool
  path : String
  source : String
  hint : String
  errs : List Error

/-- `r|color|path-hex|source-hex|json-hint-hex|error|error|...`. -/
def parseCase (line : String) : Option Case :=
  match line.splitOn "|" with
  | "r" :: color :: path :: source :: hint :: errs =>
    let parsedErrs := errs.map parseError
    if parsedErrs.any (fun e => e.isNone) then none
    else
      match color, decodeString path, decodeString source, decodeString hint with
      | "0", some path, some source, some hint =>
        some { color := false, path := path, source := source, hint := hint,
               errs := parsedErrs.filterMap id }
      | "1", some path, some source, some hint =>
        some { color := true, path := path, source := source, hint := hint,
               errs := parsedErrs.filterMap id }
      | _, _, _, _ => none
  | _ => none

def renderCase (c : Case) : String :=
  let opts : Options :=
    { fallbackPath := c.path, fallbackSource := c.source, color := c.color }
  let err :=
    match c.errs with
    | [] => .multi []
    | [e] => e
    | es => .multi es
  let rich := renderRich err opts
  let json := renderJSON (some err) c.hint
  s!"R {hexBytes rich.toUTF8}\nJ {hexBytes json.toUTF8}\n--\n"

def stripCR (s : String) : String :=
  match s.toList.reverse with
  | '\r' :: rest => String.ofList rest.reverse
  | _ => s

def main (args : List String) : IO UInt32 := do
  let batch := args.any (fun a => a == "--batch")
  let input ← readAll (← IO.getStdin)
  let text ←
    match String.fromUTF8? input with
    | some s => pure s
    | none => do
      IO.eprintln "diagnostics: input is not valid UTF-8"
      IO.Process.exit 1
  if batch then
    let mut out := ""
    for rawLine in text.splitOn "\n" do
      let line := stripCR rawLine
      if line.isEmpty || line.startsWith "#" then
        pure ()
      else
        match parseCase line with
        | none => do
          IO.eprintln s!"diagnostics: malformed case line: {line}"
          IO.Process.exit 1
        | some c => out := out ++ renderCase c
    IO.print out
    return 0
  else
    IO.eprintln "usage: diagnostics --batch < cases"
    return 1

end Gala.Diagnostics.Cli
