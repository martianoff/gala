import Interpolation

open Gala.Interpolation

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

/-- One `L offset hex` or `E offset spec-hex text-hex` line. -/
def renderPart (p : Part) : String :=
  if p.isLiteral then
    s!"L {p.byteOffset} {hexBytes p.text}"
  else
    s!"E {p.byteOffset} {hexBytes p.formatSpec} {hexBytes p.text}"

def renderParts (parts : Array Part) : String :=
  String.join (parts.toList.map (fun p => renderPart p ++ "\n"))

/-- A batch line is `c <lowercase hex bytes>`; the empty content is `c `. -/
def parseCaseLine (line : String) : Option ByteArray :=
  match line.toList with
  | 'c' :: ' ' :: rest => decodeHex (String.ofList rest)
  | _ => none

def stripCR (s : String) : String :=
  match s.toList.reverse with
  | '\r' :: rest => String.ofList rest.reverse
  | _ => s

def main (args : List String) : IO UInt32 := do
  let batch := args.any (fun a => a == "--batch")
  let input ← readAll (← IO.getStdin)
  if batch then
    let text ←
      match String.fromUTF8? input with
      | some s => pure s
      | none => do
        IO.eprintln "interpolation: batch input is not valid UTF-8"
        IO.Process.exit 1
    let mut out := ""
    for rawLine in text.splitOn "\n" do
      let line := stripCR rawLine
      if line.isEmpty || line.startsWith "#" then
        pure ()
      else
        match parseCaseLine line with
        | none => do
          IO.eprintln s!"interpolation: malformed case line: {line}"
          IO.Process.exit 1
        | some content =>
          out := out ++ renderParts (split content) ++ "--\n"
    IO.print out
    return 0
  else
    IO.print (renderParts (split input))
    return 0
