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

/-- The byte at `i` of the UTF-8 encoding, or 0 when `i` is past the end. -/
@[inline] def byteAt (s : String) (i : Nat) : UInt8 :=
  if h : i < s.utf8ByteSize then s.getUTF8Byte ⟨i⟩ h else 0

/-- The byte at the `USize` index `i`, or 0 when it is at or past `size`.
`size` is the byte length of the whole input and `h` witnesses that it fits
`s`; the caller only has to track its own logical end.  A `USize` index and
`lean_string_uget_byte_fast` keep the hot scanners free of `Nat` boxing, which
is why every line and field scan below is spelled with `USize`. -/
@[inline] def byteAtU (s : String) (i size : USize)
    (h : size.toNat ≤ s.utf8ByteSize) : UInt8 :=
  if hi : i < size then
    String.Internal.ugetUTF8Byte s i (Nat.lt_of_lt_of_le (USize.lt_iff_toNat_lt.1 hi) h)
  else 0

/-- UTF-8 length of the character whose lead byte is `b`. -/
@[inline] def utf8LeadLen (b : UInt8) : Nat :=
  if b < 0xC0 then 1 else if b < 0xE0 then 2 else if b < 0xF0 then 3 else 4

/-- Decode the code point that starts at byte `i` (the input must be UTF-8). -/
def decodeCodePoint (s : String) (i : Nat) : Nat :=
  let b0 := (byteAt s i).toNat
  if b0 < 0x80 then b0
  else if b0 < 0xE0 then
    ((b0 &&& 0x1F) <<< 6) ||| ((byteAt s (i + 1)).toNat &&& 0x3F)
  else if b0 < 0xF0 then
    ((b0 &&& 0x0F) <<< 12) ||| (((byteAt s (i + 1)).toNat &&& 0x3F) <<< 6) |||
      ((byteAt s (i + 2)).toNat &&& 0x3F)
  else
    ((b0 &&& 0x07) <<< 18) ||| (((byteAt s (i + 1)).toNat &&& 0x3F) <<< 12) |||
      (((byteAt s (i + 2)).toNat &&& 0x3F) <<< 6) ||| ((byteAt s (i + 3)).toNat &&& 0x3F)

/-- The ASCII whitespace bytes of Go's `unicode.IsSpace`: space and the C0
whitespace controls `0x09`..`0x0D`.  The range test compiles to two `UInt8`
comparisons instead of one per character, which dominates the field scans. -/
@[inline] def isAsciiSpace (b : UInt8) : Bool :=
  b == 0x20 || (0x09 ≤ b && b ≤ 0x0D)

/-- Byte length of the Go whitespace character at `i`, or 0 when it is not
whitespace.  ASCII takes a branch-only fast path; the wider
`unicode.IsSpace` characters are decoded. -/
def wsLenAt (s : String) (i : Nat) : Nat :=
  let b := byteAt s i
  if b < 0x80 then
    if isAsciiSpace b then 1 else 0
  else
    let l := utf8LeadLen b
    if isGoSpace (Char.ofNat (decodeCodePoint s i)) then l else 0

/-- Start byte of the character that ends at `e`: step back over UTF-8
continuation bytes (`0x80`..`0xBF`). -/
partial def leadStartGo (s : String) (j size : USize)
    (h : size.toNat ≤ s.utf8ByteSize) : USize :=
  if j > 0 then
    let b := byteAtU s (j - 1) size h
    if b >= 0x80 && b < 0xC0 then leadStartGo s (j - 1) size h else j - 1
  else 0

/-- `strings.TrimSpace` from the left, within `s[i:stop)`.  Most manifest bytes
are ASCII, so the ASCII branch avoids the generic code-point decoder. -/
partial def skipSpaceTo (s : String) (i stop size : USize)
    (h : size.toNat ≤ s.utf8ByteSize) : USize :=
  if i < stop then
    let b := byteAtU s i size h
    if b < 0x80 then
      if isAsciiSpace b then skipSpaceTo s (i + 1) stop size h else i
    else
      let l := wsLenAt s i.toNat
      if l == 0 then i else skipSpaceTo s (i + USize.ofNat l) stop size h
  else i

/-- `strings.TrimSpace` from the right, within `s[start:e)`: the result is
never below `start`, so trimming one span cannot walk into an earlier one
(and a run of blank lines stays linear). -/
partial def trimEndTo (s : String) (start e size : USize)
    (h : size.toNat ≤ s.utf8ByteSize) : USize :=
  if e > start then
    let st := leadStartGo s e size h
    if st < start then e
    else
      let b := byteAtU s st size h
      if b < 0x80 then
        if isAsciiSpace b && st + 1 == e then trimEndTo s start st size h else e
      else
        let l := wsLenAt s st.toNat
        if 0 < l && st + USize.ofNat l == e then trimEndTo s start st size h else e
  else e

/-- The UTF-8 byte order mark as a character. -/
def bomChar : Char := Char.ofNat 0xFEFF

/-- The byte length of a leading UTF-8 byte order mark, if there is one. -/
def bomLen (s : String) : Nat :=
  if s.utf8ByteSize >= 3 && byteAt s 0 == 0xEF && byteAt s 1 == 0xBB &&
      byteAt s 2 == 0xBF then 3 else 0

/-- The index of the next `\n` in `s[i:stop)`, or `stop`. -/
partial def newlineIndexTo (s : String) (i stop : USize)
    (h : stop.toNat ≤ s.utf8ByteSize) : USize :=
  if hi : i < stop then
    let b := String.Internal.ugetUTF8Byte s i
      (Nat.lt_of_lt_of_le (USize.lt_iff_toNat_lt.1 hi) h)
    if b == 0x0A then i else newlineIndexTo s (i + 1) stop h
  else stop

/-- Scan one line in a single pass: the newline index (or `stop`) together
with the index of the first `//` (or the line end when there is none, so the
caller tests `comment < lineStop`).  Finding both at once avoids a second
pass over every line. -/
partial def scanLineTo (s : String) (i stop : USize)
    (h : stop.toNat ≤ s.utf8ByteSize) : USize × USize :=
  if hi : i < stop then
    let b := String.Internal.ugetUTF8Byte s i
      (Nat.lt_of_lt_of_le (USize.lt_iff_toNat_lt.1 hi) h)
    if b == 0x0A then (i, i)
    else if b == 0x2F then
      if hi2 : i + 1 < stop then
        let b2 := String.Internal.ugetUTF8Byte s (i + 1)
          (Nat.lt_of_lt_of_le (USize.lt_iff_toNat_lt.1 hi2) h)
        if b2 == 0x2F then (newlineIndexTo s (i + 2) stop h, i)
        else scanLineTo s (i + 1) stop h
      else scanLineTo s (i + 1) stop h
    else scanLineTo s (i + 1) stop h
  else (stop, stop)

/-- The index of the first `,` in `s[i:stop)`, or `stop`. -/
partial def commaIndexTo (s : String) (i stop size : USize)
    (h : size.toNat ≤ s.utf8ByteSize) : USize :=
  if i < stop then
    if byteAtU s i size h == 0x2C then i else commaIndexTo s (i + 1) stop size h
  else stop

/-- Whether `s[start:stop)` is exactly the literal `lit`. -/
def spanEquals (s : String) (start stop size : USize)
    (h : size.toNat ≤ s.utf8ByteSize) (lit : String) : Bool :=
  (stop - start).toNat == lit.utf8ByteSize &&
    let rec go (i : Nat) (fuel : Nat) : Bool :=
      match fuel with
      | 0 => true
      | fuel + 1 =>
        if hlt : i < lit.utf8ByteSize then
          if byteAtU s (start + USize.ofNat i) size h == lit.getUTF8Byte ⟨i⟩ hlt then
            go (i + 1) fuel
          else false
        else true
    go 0 (lit.utf8ByteSize + 1)

/-- Copy `s[start:stop)`; this is the only place parsing actually copies text
into a new string. -/
def spanExtract (s : String) (start stop : USize) : String :=
  String.Pos.Raw.extract s ⟨start.toNat⟩ ⟨stop.toNat⟩

/-- Skip one `ScanWords` field within `s[i:stop)`.  Printable ASCII is the
common case by far, so it gets its own two-comparison test before the
whitespace table. -/
partial def skipWordTo (s : String) (i stop size : USize)
    (h : size.toNat ≤ s.utf8ByteSize) : USize :=
  if i < stop then
    let b := byteAtU s i size h
    if 0x20 < b && b < 0x7F then
      skipWordTo s (i + 1) stop size h
    else if b < 0x80 then
      if isAsciiSpace b then i else skipWordTo s (i + 1) stop size h
    else
      let l := wsLenAt s i.toNat
      if l == 0 then
        skipWordTo s (i + USize.ofNat (utf8LeadLen b)) stop size h
      else i
  else i

/-- The marker bits of one comment. -/
partial def markerMaskGo (s : String) (i stop size : USize)
    (h : size.toNat ≤ s.utf8ByteSize) (mask : UInt8) : UInt8 :=
  if i < stop then
    let comma := commaIndexTo s i stop size h
    let segStart := skipSpaceTo s i comma size h
    let segStop := trimEndTo s segStart comma size h
    let mask := if spanEquals s segStart segStop size h "indirect" then mask ||| 2 else mask
    let mask := if spanEquals s segStart segStop size h "go" then mask ||| 1 else mask
    if comma < stop then markerMaskGo s (comma + 1) stop size h mask else mask
  else mask

/-- The `// indirect` and `// go` markers of the comment in `s[start:stop)`,
bit 0 for `go` and bit 1 for `indirect`.  The Go parser trims the whole comment
and every comma-separated piece; trimming each piece directly is equivalent,
and comparing byte spans avoids allocating the marker strings.  A `UInt8`
result avoids allocating a pair on every commented line. -/
def markerMask (s : String) (start stop size : USize)
    (h : size.toNat ≤ s.utf8ByteSize) : UInt8 :=
  markerMaskGo s start stop size h 0

/-- `strings.TrimRight(s, "\n")`: drop every trailing newline. -/
def trimRightNewlines (s : String) : String :=
  (s.dropEndWhile (fun c => c == '\n')).toString

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

/-- `parseRequireLine` from field spans: at least a path and a version; the
rest is ignored. -/
def parseRequireFrom (s : String) (i stop size : USize)
    (h : size.toNat ≤ s.utf8ByteSize) (indirect isGo : Bool) :
    Except String Require :=
  let pathStart := skipSpaceTo s i stop size h
  if pathStart >= stop then .error "require needs path and version"
  else
    let pathStop := skipWordTo s pathStart stop size h
    let versionStart := skipSpaceTo s pathStop stop size h
    if versionStart >= stop then .error "require needs path and version"
    else
      let versionStop := skipWordTo s versionStart stop size h
      .ok { path := spanExtract s pathStart pathStop,
            version := spanExtract s versionStart versionStop, indirect, go := isGo }

/-- `parseExcludeLine`: `path version`. -/
def parseExcludeFrom (s : String) (i stop size : USize)
    (h : size.toNat ≤ s.utf8ByteSize) : Except String Exclude :=
  let pathStart := skipSpaceTo s i stop size h
  if pathStart >= stop then .error "exclude needs path and version"
  else
    let pathStop := skipWordTo s pathStart stop size h
    let versionStart := skipSpaceTo s pathStop stop size h
    if versionStart >= stop then .error "exclude needs path and version"
    else
      let versionStop := skipWordTo s versionStart stop size h
      .ok { path := spanExtract s pathStart pathStop,
            version := spanExtract s versionStart versionStop }

/-- `parseReplaceLine`: `old [version] => new [version]`.  The fields are
walked once, and only the spans the entry keeps are copied. -/
partial def replaceFields (s : String) (i stop size : USize)
    (h : size.toNat ≤ s.utf8ByteSize) (count : Nat) (first second : USize × USize)
    (arrow : Option Nat) (newPath newVersion : Option (USize × USize)) :
    Except String Replace :=
  let fs := skipSpaceTo s i stop size h
  if fs >= stop then
    match arrow with
    | none => .error "replace requires '=>' separator"
    | some a =>
      if a == 0 then .error "replace requires old module path before '=>'"
      else if a >= count - 1 then .error "replace requires new path after '=>'"
      else
        match newPath with
        | none => .error "replace requires new path after '=>'"
        | some np =>
          let oldVersion := if a > 1 then spanExtract s second.1 second.2 else ""
          let newVersion :=
            match newVersion with
            | some nv => spanExtract s nv.1 nv.2
            | none => ""
          .ok { old := { path := spanExtract s first.1 first.2, version := oldVersion },
                new := { path := spanExtract s np.1 np.2, version := newVersion } }
  else
    let fe := skipWordTo s fs stop size h
    let first := if count == 0 then (fs, fe) else first
    let second := if count == 1 then (fs, fe) else second
    let arrow :=
      match arrow with
      | some _ => arrow
      | none => if spanEquals s fs fe size h "=>" then some count else none
    let newPath :=
      match arrow with
      | some a => if count == a + 1 then some (fs, fe) else newPath
      | none => newPath
    let newVersion :=
      match arrow with
      | some a => if count == a + 2 then some (fs, fe) else newVersion
      | none => newVersion
    replaceFields s fe stop size h (count + 1) first second arrow newPath newVersion

/-- `parseReplaceLine`: `old [version] => new [version]`. -/
def parseReplaceFrom (s : String) (i stop size : USize)
    (h : size.toNat ≤ s.utf8ByteSize) : Except String Replace :=
  replaceFields s i stop size h 0 (0, 0) (0, 0) none none none

/-- The line scanner of the Go parser, over byte offsets into `content`.

Lines, comments and fields are never materialized: each is a `[start, stop)`
range that is scanned in place, and only the path/version values that end up in
the `File` are copied into strings. -/
partial def parseFrom (content : String) (size lineStart : USize) (lineNum : Nat)
    (modulePath gala : String) (reqs : List Require) (reps : List Replace)
    (excs : List Exclude) (inBlock : Option Block) (blockIndirect blockGo : Bool)
    (hSize : size.toNat ≤ content.utf8ByteSize) : Except ParseError File :=
  if lineStart < size then
    let (lineStop, comment) := scanLineTo content lineStart size hSize
    let next := if lineStop < size then lineStop + 1 else size
    let n := lineNum + 1
    -- The markers of a line only matter in a `require` block or on a
    -- `require` line outside one, so other commented lines skip marker parsing.
    let inRequireBlock := inBlock == some Block.require
    let mask :=
      if inRequireBlock && comment < lineStop then
        markerMask content (comment + 2) lineStop size hSize
      else 0
    let lineIndirect := (mask &&& 2) != 0
    let lineGo := (mask &&& 1) != 0
    -- A marker on a line inside a require block sticks to that block's
    -- following entries; elsewhere it only affects this line.
    let blockIndirect := blockIndirect || (lineIndirect && inRequireBlock)
    let blockGo := blockGo || (lineGo && inRequireBlock)
    let codeStart := skipSpaceTo content lineStart comment size hSize
    let codeStop := trimEndTo content codeStart comment size hSize
    if codeStart >= codeStop then
      parseFrom content size next n modulePath gala reqs reps excs inBlock
        blockIndirect blockGo hSize
    else if codeStop - codeStart == 1 && byteAtU content codeStart size hSize == 0x29 then
      parseFrom content size next n modulePath gala reqs reps excs none false false hSize
    else if byteAtU content (codeStop - 1) size hSize == 0x28 then
      let dStart := skipSpaceTo content codeStart (codeStop - 1) size hSize
      let dStop := trimEndTo content dStart (codeStop - 1) size hSize
      if spanEquals content dStart dStop size hSize "require" then
        parseFrom content size next n modulePath gala reqs reps excs
          (some Block.require) blockIndirect blockGo hSize
      else if spanEquals content dStart dStop size hSize "replace" then
        parseFrom content size next n modulePath gala reqs reps excs
          (some Block.replace) blockIndirect blockGo hSize
      else if spanEquals content dStart dStop size hSize "exclude" then
        parseFrom content size next n modulePath gala reqs reps excs
          (some Block.exclude) blockIndirect blockGo hSize
      else
        .error { line := lineNum,
                 message := s!"unknown block directive: {spanExtract content dStart dStop}" }
    else
      -- Inside a block the whole line is the entry: the first field is the
      -- dependency's path, not a directive.
      match inBlock with
      | some .require =>
        match parseRequireFrom content codeStart codeStop size hSize
            (blockIndirect || lineIndirect) (blockGo || lineGo) with
        | .ok req =>
          parseFrom content size next n modulePath gala (req :: reqs) reps
            excs inBlock blockIndirect blockGo hSize
        | .error message => .error { line := lineNum, message }
      | some .replace =>
        match parseReplaceFrom content codeStart codeStop size hSize with
        | .ok rep =>
          parseFrom content size next n modulePath gala reqs (rep :: reps)
            excs inBlock blockIndirect blockGo hSize
        | .error message => .error { line := lineNum, message }
      | some .exclude =>
        match parseExcludeFrom content codeStart codeStop size hSize with
        | .ok exc =>
          parseFrom content size next n modulePath gala reqs reps
            (exc :: excs) inBlock blockIndirect blockGo hSize
        | .error message => .error { line := lineNum, message }
      | none =>
        let directiveStart := skipSpaceTo content codeStart codeStop size hSize
        if directiveStart >= codeStop then
          parseFrom content size next n modulePath gala reqs reps excs inBlock
            blockIndirect blockGo hSize
        else
          let directiveStop := skipWordTo content directiveStart codeStop size hSize
          if spanEquals content directiveStart directiveStop size hSize "module" then
            let argStart := skipSpaceTo content directiveStop codeStop size hSize
            if argStart >= codeStop then
              .error { line := lineNum, message := "module directive requires a path" }
            else
              let argStop := skipWordTo content argStart codeStop size hSize
              parseFrom content size next n (spanExtract content argStart argStop) gala
                reqs reps excs inBlock blockIndirect blockGo hSize
          else if spanEquals content directiveStart directiveStop size hSize "gala" then
            let argStart := skipSpaceTo content directiveStop codeStop size hSize
            if argStart >= codeStop then
              .error { line := lineNum, message := "gala directive requires a version" }
            else
              let argStop := skipWordTo content argStart codeStop size hSize
              parseFrom content size next n modulePath (spanExtract content argStart argStop)
                reqs reps excs inBlock blockIndirect blockGo hSize
          else if spanEquals content directiveStart directiveStop size hSize "require" then
            let mask :=
              if comment < lineStop then markerMask content (comment + 2) lineStop size hSize
              else 0
            match parseRequireFrom content directiveStop codeStop size hSize
                ((mask &&& 2) != 0) ((mask &&& 1) != 0) with
            | .ok req =>
              parseFrom content size next n modulePath gala (req :: reqs) reps
                excs inBlock blockIndirect blockGo hSize
            | .error message => .error { line := lineNum, message }
          else if spanEquals content directiveStart directiveStop size hSize "replace" then
            match parseReplaceFrom content directiveStop codeStop size hSize with
            | .ok rep =>
              parseFrom content size next n modulePath gala reqs (rep :: reps)
                excs inBlock blockIndirect blockGo hSize
            | .error message => .error { line := lineNum, message }
          else if spanEquals content directiveStart directiveStop size hSize "exclude" then
            match parseExcludeFrom content directiveStop codeStop size hSize with
            | .ok exc =>
              parseFrom content size next n modulePath gala reqs reps
                (exc :: excs) inBlock blockIndirect blockGo hSize
            | .error message => .error { line := lineNum, message }
          else
            .error { line := lineNum,
                     message := s!"unknown directive: {spanExtract content directiveStart directiveStop}" }
  else
    .ok { module := { path := modulePath }, gala,
          requires := reqs.reverse, replaces := reps.reverse,
          excludes := excs.reverse }

/-- `mod.Parse`: scan the text directly after dropping one leading BOM. -/
def parse (content : String) : Except ParseError File :=
  let size := content.utf8ByteSize
  let sizeU := USize.ofNat size
  have hSize : sizeU.toNat ≤ content.utf8ByteSize := by
    dsimp only [sizeU]
    exact Nat.mod_le _ _
  parseFrom content sizeU (USize.ofNat (bomLen content)) 1 "" "" [] [] [] none false
    false hSize

/-! ## Formatting -/

/-- Append one require entry, including its `// indirect` / `// go` markers. -/
def appendRequire (out : String) (r : Require) : String :=
  let out := out ++ r.path ++ " " ++ r.version
  if r.indirect then
    if r.go then out ++ " // indirect, go" else out ++ " // indirect"
  else if r.go then out ++ " // go"
  else out

/-- One require entry, including its `// indirect` / `// go` markers. -/
def formatRequire (r : Require) : String :=
  appendRequire "" r

/-- One side of a replace entry. -/
def formatModuleVersion (mv : ModuleVersion) : String :=
  mv.path ++ (if mv.version.isEmpty then "" else " " ++ mv.version)

/-- Append one replace entry, without the `replace` prefix. -/
def appendReplace (out : String) (r : Replace) : String :=
  let out := out ++ r.old.path
  let out := if r.old.version.isEmpty then out else out ++ " " ++ r.old.version
  let out := out ++ " => " ++ r.new.path
  if r.new.version.isEmpty then out else out ++ " " ++ r.new.version

/-- One replace entry, without the `replace` prefix. -/
def formatReplace (r : Replace) : String :=
  appendReplace "" r

/-- A `require` directive: one line for a single entry, otherwise a block.
Entries are appended to the output as they are formatted, so no per-entry
string is built and then copied again. -/
def formatRequires : List Require → String
  | [] => ""
  | [r] => "require " ++ formatRequire r
  | rs =>
    (rs.foldl (fun out r => (appendRequire (out ++ "\t") r) ++ "\n") "require (\n") ++ ")"

/-- A `replace` directive, single line or block. -/
def formatReplaces : List Replace → String
  | [] => ""
  | [r] => "replace " ++ formatReplace r
  | rs =>
    (rs.foldl (fun out r => (appendReplace (out ++ "\t") r) ++ "\n") "replace (\n") ++ ")"

/-- An `exclude` directive, single line or block. -/
def formatExcludes : List Exclude → String
  | [] => ""
  | [e] => "exclude " ++ e.path ++ " " ++ e.version
  | es =>
    (es.foldl (fun out e => (out ++ "\t" ++ e.path ++ " " ++ e.version) ++ "\n")
      "exclude (\n") ++ ")"

/-- The value the formatter writes last, when the last section is a single
line. `mod.Format` trims every trailing newline from the finished output, so
`format` only needs that slow path when this value ends in a newline. A block
section ends with its `)`, and a single entry with markers ends with the
marker text, so those never need it. -/
def lastField (f : File) : String :=
  match f.excludes with
  | [] =>
    match f.replaces with
    | [] =>
      match f.requires with
      | [] => if f.gala.isEmpty then f.module.path else f.gala
      | [r] =>
        if r.go then "go"
        else if r.indirect then "indirect"
        else r.version
      | _ => ""
    | [r] => if r.new.version.isEmpty then r.new.path else r.new.version
    | _ => ""
  | [e] => e.version
  | _ => ""

/-- `mod.Format`, byte for byte.  Sections are joined with the blank line the
Go writer inserts, so the common path does not write trailing newlines and
then copy the whole manifest again to trim them. -/
def format (f : File) : String :=
  let sections : List String :=
    ["module " ++ f.module.path] ++
    (if f.gala.isEmpty then [] else ["gala " ++ f.gala]) ++
    (if f.requires.isEmpty then [] else [formatRequires f.requires]) ++
    (if f.replaces.isEmpty then [] else [formatReplaces f.replaces]) ++
    (if f.excludes.isEmpty then [] else [formatExcludes f.excludes])
  let text := String.intercalate "\n\n" sections
  if (lastField f).endsWith "\n" then trimRightNewlines text ++ "\n" else text ++ "\n"

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
