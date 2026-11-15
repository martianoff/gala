/-
  Lean 4 port of `martianoff/gala/internal/depman/mod`: the `gala.mod`
  manifest reader and writer.

  The Go package is the module system's whole view of a manifest: it parses
  the `module`, `gala`, `require`, `replace` and `exclude` directives (single
  line and block form, `// indirect` / `// go` markers, comments), keeps them
  in order, and formats the result byte for byte. `gala mod init`, `gala mod
  add`, `gala mod remove`, module resolution and the LSP all go through it,
  and it has no parser or transpiler state, which makes it a self-contained
  compiler-plumbing piece of the Lean port.

  The port keeps the parser's quirks as well as its intended behavior,
  because the differential harness compares it with the Go code:

  * a leading BOM is dropped exactly once;
  * `//` starts a comment wherever it appears, even inside a path, and the
    markers are read from the first comment only, split on commas;
  * a line ending in `(` opens a block for any of the three block directives,
    and `)` closes whatever block is open (`)` outside a block is ignored);
  * markers on a line inside a `require` block stick to the following
    `require` entries of that block, while a marker on the block header itself
    is discarded;
  * extra fields after a directive are ignored; the tokenizer splits on Go's
    whitespace (`unicode.IsSpace`, which is wider than Lean's `Char.isWhitespace`).

  File IO (`ParseFile` / `WriteFile`) is the harness's job; like the other
  staged ports, the module is pure. It also assumes valid UTF-8 input: Lean
  strings are Unicode and the protocol rejects invalid UTF-8, while Go strings
  carry arbitrary bytes.
-/

set_option autoImplicit false

namespace Gala.Manifest

/-! ## Strings -/

/-- Go's `unicode.IsSpace`, which is what `strings.TrimSpace` and
`bufio.ScanWords` use. Lean's `Char.isWhitespace` is only space, tab, CR and
LF, so the port spells the full set out. -/
def isGoSpace (c : Char) : Bool :=
  let n := c.toNat
  c == ' ' || c == '\t' || c == '\n' || c == Char.ofNat 0x0B || c == Char.ofNat 0x0C || c == '\r' ||
    n == 0x85 || n == 0xA0 || n == 0x1680 ||
    (n >= 0x2000 && n <= 0x200A) ||
    n == 0x2028 || n == 0x2029 || n == 0x202F || n == 0x205F || n == 0x3000

/-- `strings.TrimSpace`. -/
def trimSpace (s : String) : String :=
  String.ofList (s.toList.dropWhile isGoSpace |>.reverse.dropWhile isGoSpace |>.reverse)

/-- The UTF-8 byte order mark as a character. -/
def bomChar : Char := Char.ofNat 0xFEFF

/-- `galaerr.StripBOM`: remove exactly one leading BOM. -/
def stripBOM (s : String) : String :=
  match s.toList with
  | c :: rest => if c == bomChar then String.ofList rest else s
  | [] => s

/-- `bufio.Scanner` with `ScanWords`: runs of Go whitespace, no empty words. -/
def fields (s : String) : List String :=
  (s.split isGoSpace).toList.map (fun w => w.toString) |>.filter (fun w => !w.isEmpty)

/-- Drop the last character of a string (used for a block header's `(`). -/
def dropLastChar (s : String) : String :=
  String.ofList s.toList.dropLast

/-- `strings.TrimRight(s, "\n")`: drop every trailing newline. -/
def trimRightNewlines (s : String) : String :=
  String.ofList (s.toList.reverse.dropWhile (fun c => c == '\n')).reverse

/-! ## The manifest -/

/-- The `module` directive. -/
structure Module where
  path : String := ""
  deriving Repr, BEq, Inhabited

/-- One `require` entry. `Indirect` and `Go` come from the trailing comment
markers. -/
structure Require where
  path : String
  version : String
  indirect : Bool := false
  go : Bool := false
  deriving Repr, BEq, Inhabited

/-- A module path with an optional version, as used by `replace`. -/
structure ModuleVersion where
  path : String
  version : String := ""
  deriving Repr, BEq, Inhabited

/-- One `replace` entry, `old [version] => new [version]`. -/
structure Replace where
  old : ModuleVersion
  new : ModuleVersion
  deriving Repr, BEq, Inhabited

/-- One `exclude` entry. -/
structure Exclude where
  path : String
  version : String
  deriving Repr, BEq, Inhabited

/-- A parsed `gala.mod`. The Go fields `Module`, `Gala`, `Require`, `Replace`
and `Exclude` are `module`, `gala`, `requires`, `replaces` and `excludes`. -/
structure File where
  module : Module := {}
  gala : String := ""
  requires : List Require := []
  replaces : List Replace := []
  excludes : List Exclude := []
  deriving Repr, BEq, Inhabited

/-- A parse error, `gala.mod:<line>: <message>`, like Go's `*mod.ParseError`. -/
structure ParseError where
  line : Nat
  message : String
  deriving Repr, BEq, Inhabited

/-- `(*ParseError).Error()`. -/
def ParseError.toString (e : ParseError) : String :=
  s!"gala.mod:{e.line}: {e.message}"

/-! ## Parsing -/

/-- The three block directives. -/
inductive Block where
  | require
  | replace
  | exclude
  deriving Repr, BEq

/-- Split a line at the first `//`, returning the code before it and the
comment (without the `//`), as `strings.Index` does. -/
def splitComment (line : String) : String × Option String :=
  match line.splitOn "//" with
  | [] => (line, none)
  | [_] => (line, none)
  | pre :: rest => (pre, some (String.intercalate "//" rest))

/-- The comma-separated markers of a comment, trimmed. -/
def commentMarkers (comment : String) : List String :=
  (trimSpace comment).splitOn "," |>.map trimSpace

def hasMarker (markers : List String) (name : String) : Bool :=
  markers.any (fun m => m == name)

/-- The index of the first element equal to `p`, as a list of fields. -/
def indexOfField (p : String) : List String → Nat → Option Nat
  | [], _ => none
  | x :: xs, i => if x == p then some i else indexOfField p xs (i + 1)

/-- `parseRequireLine`: at least a path and a version; the rest is ignored. -/
def parseRequire (parts : List String) (indirect isGo : Bool) : Except String Require :=
  match parts with
  | path :: version :: _ => .ok { path, version, indirect, go := isGo }
  | _ => .error "require needs path and version"

/-- `parseReplaceLine`: `old [version] => new [version]`. -/
def parseReplace (parts : List String) : Except String Replace :=
  match indexOfField "=>" parts 0 with
  | none => .error "replace requires '=>' separator"
  | some 0 => .error "replace requires old module path before '=>'"
  | some i =>
    if i >= parts.length - 1 then
      .error "replace requires new path after '=>'"
    else
      let old : ModuleVersion :=
        { path := parts.getD 0 "", version := if i > 1 then parts.getD 1 "" else "" }
      let newParts := parts.drop (i + 1)
      match newParts with
      | newPath :: rest => .ok { old, new := { path := newPath, version := rest.headD "" } }
      | [] => .error "replace requires new path after '=>'"

/-- `parseExcludeLine`: `path version`. -/
def parseExclude (parts : List String) : Except String Exclude :=
  match parts with
  | path :: version :: _ => .ok { path, version }
  | _ => .error "exclude needs path and version"

/-- `parseLines`, the line scanner of the Go parser. -/
def parseLines (lines : List String) : Except ParseError File :=
  let rec go (lineNum : Nat) (lines : List String) (f : File) (inBlock : Option Block)
      (blockIndirect blockGo : Bool) : Except ParseError File :=
    match lines with
    | [] => .ok f
    | rawLine :: rest =>
      let n := lineNum + 1
      let (code, comment) := splitComment rawLine
      let markers :=
        match comment with
        | none => []
        | some c => commentMarkers c
      let lineIndirect := hasMarker markers "indirect"
      let lineGo := hasMarker markers "go"
      -- A marker on a line inside a require block sticks to that block's
      -- following entries; elsewhere it only affects this line.
      let blockIndirect := blockIndirect || (lineIndirect && inBlock == some Block.require)
      let blockGo := blockGo || (lineGo && inBlock == some Block.require)
      let line := trimSpace code
      if line.isEmpty then
        go n rest f inBlock blockIndirect blockGo
      else if line == ")" then
        go n rest f none false false
      else if line.endsWith "(" then
        let directive := trimSpace (dropLastChar line)
        match directive with
        | "require" => go n rest f (some Block.require) blockIndirect blockGo
        | "replace" => go n rest f (some Block.replace) blockIndirect blockGo
        | "exclude" => go n rest f (some Block.exclude) blockIndirect blockGo
        | _ => .error { line := n, message := s!"unknown block directive: {directive}" }
      else
        match fields line with
        | [] => go n rest f inBlock blockIndirect blockGo
        | directive :: args =>
          -- Inside a block the whole line is the entry: the first field is
          -- the dependency's path, not a directive.
          match inBlock with
          | some .require =>
            match parseRequire (directive :: args) (blockIndirect || lineIndirect)
                (blockGo || lineGo) with
            | .ok req => go n rest { f with requires := f.requires ++ [req] }
                inBlock blockIndirect blockGo
            | .error message => .error { line := n, message }
          | some .replace =>
            match parseReplace (directive :: args) with
            | .ok rep => go n rest { f with replaces := f.replaces ++ [rep] }
                inBlock blockIndirect blockGo
            | .error message => .error { line := n, message }
          | some .exclude =>
            match parseExclude (directive :: args) with
            | .ok exc => go n rest { f with excludes := f.excludes ++ [exc] }
                inBlock blockIndirect blockGo
            | .error message => .error { line := n, message }
          | none =>
            match directive with
            | "module" =>
              match args with
              | path :: _ => go n rest { f with module := { path } } inBlock
                  blockIndirect blockGo
              | [] => .error { line := n, message := "module directive requires a path" }
            | "gala" =>
              match args with
              | version :: _ => go n rest { f with gala := version } inBlock
                  blockIndirect blockGo
              | [] => .error { line := n, message := "gala directive requires a version" }
            | "require" =>
              match parseRequire args lineIndirect lineGo with
              | .ok req => go n rest { f with requires := f.requires ++ [req] }
                  inBlock blockIndirect blockGo
              | .error message => .error { line := n, message }
            | "replace" =>
              match parseReplace args with
              | .ok rep => go n rest { f with replaces := f.replaces ++ [rep] }
                  inBlock blockIndirect blockGo
              | .error message => .error { line := n, message }
            | "exclude" =>
              match parseExclude args with
              | .ok exc => go n rest { f with excludes := f.excludes ++ [exc] }
                  inBlock blockIndirect blockGo
              | .error message => .error { line := n, message }
            | _ => .error { line := n, message := s!"unknown directive: {directive}" }
  go 0 lines {} none false false

/-- `mod.Parse`: split on newlines after dropping a leading BOM. -/
def parse (content : String) : Except ParseError File :=
  parseLines ((stripBOM content).splitOn "\n")

/-! ## Formatting -/

/-- One require entry, including its `// indirect` / `// go` markers. -/
def formatRequire (r : Require) : String :=
  let markers :=
    (if r.indirect then ["indirect"] else []) ++ (if r.go then ["go"] else [])
  r.path ++ " " ++ r.version ++
    (if markers.isEmpty then "" else " // " ++ String.intercalate ", " markers)

/-- One side of a replace entry. -/
def formatModuleVersion (mv : ModuleVersion) : String :=
  mv.path ++ (if mv.version.isEmpty then "" else " " ++ mv.version)

/-- One replace entry, without the `replace` prefix. -/
def formatReplace (r : Replace) : String :=
  formatModuleVersion r.old ++ " => " ++ formatModuleVersion r.new

/-- `mod.Format`, byte for byte. -/
def format (f : File) : String :=
  let sb :=
    "module " ++ f.module.path ++ "\n\n" ++
    (if f.gala.isEmpty then "" else "gala " ++ f.gala ++ "\n\n") ++
    (match f.requires with
     | [] => ""
     | [r] => "require " ++ formatRequire r ++ "\n\n"
     | rs =>
       "require (\n" ++
         String.join (rs.map (fun r => "\t" ++ formatRequire r ++ "\n")) ++ ")\n\n") ++
    (match f.replaces with
     | [] => ""
     | [r] => "replace " ++ formatReplace r ++ "\n\n"
     | rs =>
       "replace (\n" ++
         String.join (rs.map (fun r => "\t" ++ formatReplace r ++ "\n")) ++ ")\n\n") ++
    (match f.excludes with
     | [] => ""
     | [e] => "exclude " ++ e.path ++ " " ++ e.version ++ "\n"
     | es =>
       "exclude (\n" ++
         String.join (es.map (fun e => "\t" ++ e.path ++ " " ++ e.version ++ "\n")) ++ ")\n")
  trimRightNewlines sb ++ "\n"

/-! ## The `*File` methods -/

/-- `ModuleVersion.IsLocal`: no version and a path that starts with `.`, `/`
or `\`. -/
def ModuleVersion.isLocal (mv : ModuleVersion) : Bool :=
  mv.version.isEmpty &&
    match mv.path.toList with
    | c :: _ => c == '.' || c == '/' || c == '\\'
    | [] => false

/-- `mod.NewFile`. -/
def newFile (modulePath : String) : File :=
  { module := { path := modulePath } }

/-- Replace the first entry satisfying `p`, leaving later duplicates alone. -/
def updateFirst {α : Type} (l : List α) (p : α → Bool) (g : α → α) : List α :=
  match l with
  | [] => []
  | x :: xs => if p x then g x :: xs else x :: updateFirst xs p g

/-- `(*File).AddRequire`: update the version and `indirect` flag of an
existing entry (including its position) or append a new one. The Go flag of
an existing entry is left as it was. -/
def addRequire (f : File) (path version : String) (indirect : Bool) : File :=
  let update : Require → Require := fun r => { r with version := version, indirect := indirect }
  if f.requires.any (fun r => r.path == path) then
    { f with requires := updateFirst f.requires (fun r => r.path == path) update }
  else
    { f with requires := f.requires ++ [{ path, version, indirect }] }

/-- `(*File).RemoveRequire`: remove the first entry with that path. -/
def removeRequire (f : File) (path : String) : File × Bool :=
  let rec go : List Require → List Require × Bool
    | [] => ([], false)
    | r :: rs =>
      if r.path == path then (rs, true)
      else
        let (rs', found) := go rs
        (r :: rs', found)
  let (requires, found) := go f.requires
  ({ f with requires }, found)

/-- `(*File).GetRequire`: the first entry with that path. -/
def getRequire (f : File) (path : String) : Option Require :=
  f.requires.find? (fun r => r.path == path)

/-- `(*File).AddReplace`: update the replacement of an existing `(old path,
old version)` pair, or append a new entry. -/
def addReplace (f : File) (oldPath oldVersion newPath newVersion : String) : File :=
  let target : ModuleVersion := { path := oldPath, version := oldVersion }
  let replacement : ModuleVersion := { path := newPath, version := newVersion }
  let update : Replace → Replace := fun r => { r with new := replacement }
  if f.replaces.any (fun r => r.old == target) then
    { f with replaces := updateFirst f.replaces (fun r => r.old == target) update }
  else
    { f with replaces := f.replaces ++ [{ old := target, new := replacement }] }

/-- `(*File).AddExclude`: add the version unless it is already excluded. -/
def addExclude (f : File) (path version : String) : File :=
  if f.excludes.any (fun e => e.path == path && e.version == version) then f
  else { f with excludes := f.excludes ++ [{ path, version }] }

/-- `(*File).DirectRequires`. -/
def directRequires (f : File) : List Require :=
  f.requires.filter (fun r => !r.indirect)

/-- `(*File).IndirectRequires`. -/
def indirectRequires (f : File) : List Require :=
  f.requires.filter (fun r => r.indirect)

/-- `(*File).GalaRequires`. -/
def galaRequires (f : File) : List Require :=
  f.requires.filter (fun r => !r.go)

/-- `(*File).GoRequires`. -/
def goRequires (f : File) : List Require :=
  f.requires.filter (fun r => r.go)

end Gala.Manifest
