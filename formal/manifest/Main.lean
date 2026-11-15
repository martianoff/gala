import Manifest

open Gala.Manifest

/- The differential harness protocol of the `gala.mod` port.

A case is one `c|` line: the manifest text as hex of its UTF-8 bytes, then
optional operations separated by `|`. Operations are applied to the parsed
file in order; `a`/`p`/`e` mutate it, `d`/`g` return what the Go methods
return, and `direct`/`indirect`/`gala`/`go` are the filtered views.

Output for `c|<content>|<ops...>`:

```
P
module <hex path>
gala <hex version>
require <hex path> <hex version> <0|1 indirect> <0|1 go>
replace <hex old path> <hex old version> <hex new path> <hex new version>
exclude <hex path> <hex version>
O
<one line per operation that returns something>
F <hex of mod.Format>
--
```

A parse error prints `E <hex of *ParseError.Error()>` and `--` instead. -/

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

/-- Hex of the UTF-8 bytes of a string. -/
def hx (s : String) : String := hexBytes s.toUTF8

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

def bit (b : Bool) : String := if b then "1" else "0"

/-- The canonical dump of a parsed file, after any operations. -/
def dump (f : File) : String :=
  "P\n" ++
  "module " ++ hx f.module.path ++ "\n" ++
  "gala " ++ hx f.gala ++ "\n" ++
  String.join (f.requires.map (fun r =>
    s!"require {hx r.path} {hx r.version} {bit r.indirect} {bit r.go}\n")) ++
  String.join (f.replaces.map (fun r =>
    s!"replace {hx r.old.path} {hx r.old.version} {hx r.new.path} {hx r.new.version}\n")) ++
  String.join (f.excludes.map (fun e => s!"exclude {hx e.path} {hx e.version}\n"))

def pathsLine (name : String) (ps : List String) : String :=
  name ++ " " ++ toString ps.length ++ String.join (ps.map (fun p => " " ++ hx p))

/-- Apply one protocol operation. Returns the new file and the lines the
operation prints (state changes are shown by the final dump). -/
def applyOp (f : File) (op : String) : Except String (File × List String) :=
  match op.splitOn ":" with
  | ["a", pathHex, versionHex, indirect] =>
    match decodeString pathHex, decodeString versionHex with
    | some path, some version =>
      if indirect == "0" then .ok (addRequire f path version false, [])
      else if indirect == "1" then .ok (addRequire f path version true, [])
      else .error s!"bad indirect flag: {indirect}"
    | _, _ => .error s!"bad add operation: {op}"
  | ["d", pathHex] =>
    match decodeString pathHex with
    | some path =>
      let (f', found) := removeRequire f path
      .ok (f', [s!"remove {bit found}"])
    | none => .error s!"bad remove operation: {op}"
  | ["g", pathHex] =>
    match decodeString pathHex with
    | some path =>
      match getRequire f path with
      | some r => .ok (f, [s!"get 1 {hx r.version} {bit r.indirect} {bit r.go}"])
      | none => .ok (f, ["get 0"])
    | none => .error s!"bad get operation: {op}"
  | ["p", oldPathHex, oldVersionHex, newPathHex, newVersionHex] =>
    match decodeString oldPathHex, decodeString oldVersionHex,
        decodeString newPathHex, decodeString newVersionHex with
    | some oldPath, some oldVersion, some newPath, some newVersion =>
      .ok (addReplace f oldPath oldVersion newPath newVersion, [])
    | _, _, _, _ => .error s!"bad replace operation: {op}"
  | ["e", pathHex, versionHex] =>
    match decodeString pathHex, decodeString versionHex with
    | some path, some version => .ok (addExclude f path version, [])
    | _, _ => .error s!"bad exclude operation: {op}"
  | ["direct"] => .ok (f, [pathsLine "direct" ((directRequires f).map (fun r => r.path))])
  | ["indirect"] =>
    .ok (f, [pathsLine "indirect" ((indirectRequires f).map (fun r => r.path))])
  | ["gala"] => .ok (f, [pathsLine "gala" ((galaRequires f).map (fun r => r.path))])
  | ["go"] => .ok (f, [pathsLine "go" ((goRequires f).map (fun r => r.path))])
  | ["format"] => .ok (f, [s!"format {hx (format f)}"])
  | _ => .error s!"unknown operation: {op}"

def runOps (f : File) : List String → Except String (File × List String)
  | [] => .ok (f, [])
  | op :: ops => do
    let (f', lines) ← applyOp f op
    let (f'', rest) ← runOps f' ops
    return (f'', lines ++ rest)

structure Case where
  content : String
  ops : List String

/-- `c|<content-hex>|<op>|...`. -/
def parseCase (line : String) : Option Case :=
  match line.splitOn "|" with
  | "c" :: contentHex :: ops =>
    decodeString contentHex |>.map fun content => { content, ops }
  | _ => none

def renderCase (c : Case) : Except String String := do
  match parse c.content with
  | .error e => return s!"E {hx e.toString}\n--\n"
  | .ok f =>
    let (f', lines) ← runOps f c.ops
    return dump f' ++ "O\n" ++ String.join (lines.map (fun l => l ++ "\n")) ++
      s!"F {hx (format f')}\n--\n"

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
      IO.eprintln "manifest: input is not valid UTF-8"
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
          IO.eprintln s!"manifest: malformed case line: {line}"
          IO.Process.exit 1
        | some c =>
          match renderCase c with
          | .ok rendered => out := out ++ rendered
          | .error message => do
            IO.eprintln s!"manifest: {message}"
            IO.Process.exit 1
    IO.print out
    return 0
  else
    IO.eprintln "usage: manifest --batch < cases"
    return 1
