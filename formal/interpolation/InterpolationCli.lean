/-
  The command line of the interpolation port, without the `main` binding.

  `Main.lean` is the standalone `interpolation` executable; `gala-lean`
  (`formal/gala-lean`) imports this module for its `interp` subcommand. The
  batch protocol lives here so both binaries share it.
-/
import Interpolation

open Gala.Interpolation

namespace Gala.Interpolation.Cli

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
    | some hi, some lo =>
      decodeHexAux rest (acc.push (UInt8.ofNat (hi * 16 + lo)))
    | _, _ => none
  | [_], _ => none

def decodeHex (s : String) : Option ByteArray :=
  decodeHexAux s.toList ByteArray.empty

/-- One `L offset start end text-hex` or
`E offset start end spec-hex text-hex printed` line. -/
def renderPart (p : Part) : String :=
  if p.isLiteral then
    s!"L {p.byteOffset} {p.start} {p.endPos} {hexBytes p.text}"
  else
    let printed := if printedByString p then "1" else "0"
    s!"E {p.byteOffset} {p.start} {p.endPos} {hexBytes p.formatSpec} {hexBytes p.text} {printed}"

def renderParts (parts : Array Part) : String :=
  String.join (parts.toList.map (fun p => renderPart p ++ "\n"))

/-- A batch line is one of

* `s <hex content>` — `split content false`
* `f <hex content>` — `split content true`
* `p <offset> <line> <col> <hex content>` — `position content offset line col`
-/
inductive Case where
  | split (content : ByteArray) (formatSpecs : Bool)
  | position (content : ByteArray) (offset line col : Nat)

def parseCase (line : String) : Option Case :=
  let fields := line.splitOn " "
  match fields with
  | ["s", h] => decodeHex h |>.map fun c => Case.split c false
  | ["f", h] => decodeHex h |>.map fun c => Case.split c true
  | ["p", o, l, c, h] =>
    match o.toNat?, l.toNat?, c.toNat?, decodeHex h with
    | some offset, some line, some col, some content =>
      some (Case.position content offset line col)
    | _, _, _, _ => none
  | _ => none

def stripCR (s : String) : String :=
  match s.toList.reverse with
  | '\r' :: rest => String.ofList rest.reverse
  | _ => s

def renderCase (c : Case) : String :=
  match c with
  | .split content formatSpecs =>
    renderParts (split content formatSpecs) ++ "--\n"
  | .position content offset line col =>
    let (l, c) := position content offset line col
    s!"P {l} {c}\n--\n"

def main (args : List String) : IO UInt32 := do
  let batch := args.any (fun a => a == "--batch")
  let input ← readAll (← IO.getStdin)
  let text ←
    match String.fromUTF8? input with
    | some s => pure s
    | none => do
      IO.eprintln "interpolation: batch input is not valid UTF-8"
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
          IO.eprintln s!"interpolation: malformed case line: {line}"
          IO.Process.exit 1
        | some c =>
          out := out ++ renderCase c
    IO.print out
    return 0
  else
    let formatSpecs := args.any (fun a => a == "--format")
    IO.print (renderParts (split input formatSpecs))
    return 0

end Gala.Interpolation.Cli
