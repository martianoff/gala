/-
  Lean 4 port of `martianoff/gala/internal/interpolation`.

  The Go original is a pure byte-level splitter for the body of a GALA
  interpolated (`s"..."`) or format (`f"..."`) string.  This port keeps the
  same byte-oriented semantics exactly, including:

  * `$identifier`, `${ expression }`, `$$` (literal `$`);
  * `\`-escape pass-through and `\"` unescaping inside `${...}`;
  * the optional trailing printf spec after an embedded expression, captured
    only for a format string (`Split(content, true)`);
  * byte offsets and the `Start`/`End` source span of every part;
  * `PrintedByString`, and `Position` (line and rune column of a byte offset).

  It is deliberately self-contained: no parser or transpiler state.  The
  recursive scanners carry fuel so their termination is structural and the
  generated C stays straightforward.
-/

set_option autoImplicit false

namespace Gala.Interpolation

/-- One piece of an interpolated string: literal text or an embedded
expression with an optional explicit printf format spec.

`start` and `endPos` delimit the part in the content, as Go's `Part.Start` and
`Part.End`: a literal run as written (`$$` included), or an embedded expression
from its `$` to just past its name or closing `}`. A format spec after an
expression is not included. -/
structure Part where
  isLiteral : Bool
  text : ByteArray
  formatSpec : ByteArray := ByteArray.empty
  byteOffset : Nat
  start : Nat
  endPos : Nat
  deriving BEq, Inhabited

namespace Bytes

/-- Byte comparison against a small numeric literal, avoiding `UInt8` literal
inference noise in guards. -/
def is (b : UInt8) (n : Nat) : Bool := b.toNat == n

def isIdentStart (b : UInt8) : Bool :=
  is b 0x5F ||
  (b.toNat >= 0x61 && b.toNat <= 0x7A) ||
  (b.toNat >= 0x41 && b.toNat <= 0x5A)

def isIdentPart (b : UInt8) : Bool :=
  isIdentStart b || (b.toNat >= 0x30 && b.toNat <= 0x39)

def isDigit (b : UInt8) : Bool :=
  b.toNat >= 0x30 && b.toNat <= 0x39

def isFormatFlag (b : UInt8) : Bool :=
  is b 0x2B || is b 0x2D || is b 0x23 || is b 0x20 || is b 0x30

/-- The Go printf verb set accepted by the original. -/
def isFormatVerb (b : UInt8) : Bool :=
  is b 0x62 || is b 0x63 || is b 0x64 || is b 0x65 || is b 0x45 ||
  is b 0x66 || is b 0x46 || is b 0x67 || is b 0x47 || is b 0x6F ||
  is b 0x4F || is b 0x70 || is b 0x71 || is b 0x73 || is b 0x74 ||
  is b 0x54 || is b 0x55 || is b 0x76 || is b 0x78 || is b 0x58

def get? (a : ByteArray) (i : Nat) : Option UInt8 :=
  if h : i < a.size then some (a.get i h) else none

/-- Whether `b` occurs in `a`. -/
def contains (a : ByteArray) (b : UInt8) : Bool :=
  let rec go : Nat → Nat → Bool
    | 0, _ => false
    | fuel + 1, i =>
      match get? a i with
      | none => false
      | some c => if c == b then true else go fuel (i + 1)
  go (a.size + 1) 0

/-- The last byte of `a`, if it has one. -/
def last? (a : ByteArray) : Option UInt8 :=
  if a.isEmpty then none else some (a.get! (a.size - 1))

/-- The length in bytes of the UTF-8 sequence `a[i]` begins, from its lead byte
alone: `1` for ASCII, `2`–`4` for a lead byte that could begin a sequence, and
`0` when the byte cannot begin one. Continuation bytes and overlong, surrogate
and out-of-range sequences are rejected by `runeLenAt`, not here. -/
def utf8LeadLen (a : ByteArray) (i : Nat) : Nat :=
  match get? a i with
  | none => 0
  | some b =>
    if b.toNat < 0x80 then 1
    else if b.toNat >= 0xC2 && b.toNat <= 0xDF then 2
    else if b.toNat >= 0xE0 && b.toNat <= 0xEF then 3
    else if b.toNat >= 0xF0 && b.toNat <= 0xF4 then 4
    else 0

def isCont (a : ByteArray) (i : Nat) : Bool :=
  match get? a i with
  | some b => b.toNat >= 0x80 && b.toNat <= 0xBF
  | none => false

/-- The byte at `i`, or 0 past the end. -/
def byteAt (a : ByteArray) (i : Nat) : Nat :=
  match get? a i with
  | some b => b.toNat
  | none => 0

/-- The length of the UTF-8 encoding beginning at `a[i]` when it is valid, and
`1` otherwise: Go's `utf8.RuneLen`, with an invalid byte counting as one
`RuneError`. This is what `utf8.RuneCount` walks. -/
def runeLenAt (a : ByteArray) (i : Nat) : Nat :=
  let n := utf8LeadLen a i
  let b := byteAt a i
  if n == 1 then 1
  else if n == 2 then
    if isCont a (i + 1) then 2 else 1
  else if n == 3 then
    if isCont a (i + 1) && isCont a (i + 2) &&
        (b != 0xE0 || byteAt a (i + 1) >= 0xA0) &&
        (b != 0xED || byteAt a (i + 1) <= 0x9F) then 3 else 1
  else if n == 4 then
    if isCont a (i + 1) && isCont a (i + 2) && isCont a (i + 3) &&
        (b != 0xF0 || byteAt a (i + 1) >= 0x90) &&
        (b != 0xF4 || byteAt a (i + 1) <= 0x8F) then 4 else 1
  else 1

/-- The number of runes in `a`, as Go's `utf8.RuneCountInString`: an invalid
byte is one `RuneError`. -/
def runeCount (a : ByteArray) : Nat :=
  let rec go : Nat → Nat → Nat → Nat
    | 0, _, acc => acc
    | fuel + 1, i, acc =>
      if i >= a.size then acc
      else go fuel (i + max 1 (runeLenAt a i)) (acc + 1)
  go (a.size + 1) 0 0

/-- The index of the last `\n` in `a`, if any. -/
def lastNewline (a : ByteArray) : Option Nat :=
  let rec go : Nat → Nat → Option Nat → Option Nat
    | 0, _, found => found
    | fuel + 1, i, found =>
      match get? a i with
      | none => found
      | some c => go fuel (i + 1) (if c == 0x0A then some i else found)
  go (a.size + 1) 0 none

def newlineCount (a : ByteArray) : Nat :=
  let rec go : Nat → Nat → Nat → Nat
    | 0, _, acc => acc
    | fuel + 1, i, acc =>
      match get? a i with
      | none => acc
      | some c => go fuel (i + 1) (if c == 0x0A then acc + 1 else acc)
  go (a.size + 1) 0 0

end Bytes

open Bytes

/-- Append one byte to the pending literal run, recording the run's first
byte offset the first time the run becomes non-empty. -/
def writeLit (lit : ByteArray) (litStart : Nat) (pos : Nat) (b : UInt8) :
    ByteArray × Nat :=
  if lit.isEmpty then (lit.push b, pos) else (lit.push b, litStart)

/-- Flush the pending literal run as a part ending at `stop`. -/
def flushLiteral (parts : Array Part) (lit : ByteArray) (litStart stop : Nat) :
    Array Part :=
  if lit.isEmpty then parts
  else parts.push
    { isLiteral := true, text := lit, byteOffset := litStart,
      start := litStart, endPos := stop }

/-- Scan an identifier after `$`.  Returns the index one past its last byte. -/
def scanIdent (a : ByteArray) (fuel : Nat) (j : Nat) : Nat :=
  match fuel with
  | 0 => j
  | fuel + 1 =>
    match get? a j with
    | none => j
    | some c => if isIdentPart c then scanIdent a fuel (j + 1) else j

/-- Locate the `}` closing the `${` at `content[dollar]`.

This mirrors `EndOfEmbeddedExpr`: brace nesting is counted, `\` skips the
following byte, and an unterminated block returns `content.size`. -/
def endOfEmbeddedExprAux (a : ByteArray) (fuel : Nat) (j depth : Nat) : Nat :=
  match fuel with
  | 0 => j
  | fuel + 1 =>
    if h : j < a.size ∧ depth > 0 then
      let c := a.get j h.left
      if is c 0x7B then
        endOfEmbeddedExprAux a fuel (j + 1) (depth + 1)
      else if is c 0x7D then
        if depth == 1 then j
        else endOfEmbeddedExprAux a fuel (j + 1) (depth - 1)
      else if is c 0x5C ∧ j + 1 < a.size then
        endOfEmbeddedExprAux a fuel (j + 2) depth
      else
        endOfEmbeddedExprAux a fuel (j + 1) depth
    else j

def endOfEmbeddedExpr (a : ByteArray) (dollar : Nat) : Nat :=
  endOfEmbeddedExprAux a (a.size + 1) (dollar + 2) 1

/-- Replace `\"` with `"` inside an embedded expression. -/
def unescapeExprAux (a : ByteArray) (fuel : Nat) (i : Nat) (out : ByteArray) :
    ByteArray :=
  match fuel with
  | 0 => out
  | fuel + 1 =>
    match get? a i with
    | none => out
    | some c =>
      match get? a (i + 1) with
      | some c2 =>
        if is c 0x5C && is c2 0x22 then
          unescapeExprAux a fuel (i + 2) (out.push 0x22)
        else
          unescapeExprAux a fuel (i + 1) (out.push c)
      | none =>
        unescapeExprAux a fuel (i + 1) (out.push c)

def unescapeExpr (a : ByteArray) : ByteArray :=
  unescapeExprAux a (a.size + 1) 0 ByteArray.empty

def skipFlags (a : ByteArray) (fuel : Nat) (j : Nat) : Nat :=
  match fuel with
  | 0 => j
  | fuel + 1 =>
    match get? a j with
    | none => j
    | some c => if isFormatFlag c then skipFlags a fuel (j + 1) else j

def skipDigits (a : ByteArray) (fuel : Nat) (j : Nat) : Nat :=
  match fuel with
  | 0 => j
  | fuel + 1 =>
    match get? a j with
    | none => j
    | some c => if isDigit c then skipDigits a fuel (j + 1) else j

/-- Extract a Go printf spec starting at `%`; on failure return `("", i)` so
the caller treats the `%` as literal text. -/
def extractFormatSpec (a : ByteArray) (i : Nat) : ByteArray × Nat :=
  if h : i < a.size then
    if is (a.get i h) 0x25 then
      let fuel := a.size + 1
      let j1 := skipFlags a fuel (i + 1)
      let j2 :=
        match get? a j1 with
        | some c => if is c 0x2A then j1 + 1 else skipDigits a fuel j1
        | none => j1
      let j3 :=
        match get? a j2 with
        | some c =>
          if is c 0x2E then
            let k := j2 + 1
            match get? a k with
            | some c2 => if is c2 0x2A then k + 1 else skipDigits a fuel k
            | none => k
          else j2
        | none => j2
      match get? a j3 with
      | some c =>
        if isFormatVerb c then
          (a.extract i (j3 + 1), j3 + 1)
        else
          (ByteArray.empty, i)
      | none => (ByteArray.empty, i)
    else
      (ByteArray.empty, i)
  else
    (ByteArray.empty, i)

/-- Split the CONTENT of an interpolated/format string (the text between the
opening `s"`/`f"` and its closing quote).

With `formatSpecs`, as for an `f"..."` string, a `%spec` following an embedded
expression is captured; in an `s"..."` string a `%` is literal text. -/
def splitAux (content : ByteArray) (formatSpecs : Bool) :
    Nat → Nat → ByteArray → Nat → Array Part → Array Part
  | 0, i, lit, litStart, parts => flushLiteral parts lit litStart i
  | fuel + 1, i, lit, litStart, parts =>
    match get? content i with
    | none => flushLiteral parts lit litStart i
    | some c =>
      if is c 0x5C then
        match get? content (i + 1) with
        | some c2 =>
          let (lit1, ls1) := writeLit lit litStart i c
          let (lit2, ls2) := writeLit lit1 ls1 (i + 1) c2
          splitAux content formatSpecs fuel (i + 2) lit2 ls2 parts
        | none =>
          let (lit1, ls1) := writeLit lit litStart i c
          splitAux content formatSpecs fuel (i + 1) lit1 ls1 parts
      else if !is c 0x24 then
        let (lit1, ls1) := writeLit lit litStart i c
        splitAux content formatSpecs fuel (i + 1) lit1 ls1 parts
      else
        match get? content (i + 1) with
        | none =>
          let (lit1, ls1) := writeLit lit litStart i c
          splitAux content formatSpecs fuel (i + 1) lit1 ls1 parts
        | some next =>
          if is next 0x24 then
            let (lit1, ls1) := writeLit lit litStart i c
            splitAux content formatSpecs fuel (i + 2) lit1 ls1 parts
          else if is next 0x7B then
            let parts1 := flushLiteral parts lit litStart i
            let j0 := endOfEmbeddedExpr content i
            let exprStart := i + 2
            let exprText := unescapeExpr (content.extract exprStart j0)
            let j1 := min (j0 + 1) content.size
            let (fmtSpec, j2) :=
              if formatSpecs && j1 < content.size && is (content.get! j1) 0x25
              then extractFormatSpec content j1
              else (ByteArray.empty, j1)
            let part : Part :=
              { isLiteral := false, text := exprText,
                formatSpec := fmtSpec, byteOffset := exprStart,
                start := i, endPos := j1 }
            splitAux content formatSpecs fuel j2 ByteArray.empty 0
              (parts1.push part)
          else if isIdentStart next then
            let parts1 := flushLiteral parts lit litStart i
            let identStart := i + 1
            let j0 := scanIdent content (content.size + 1) identStart
            let identName := content.extract identStart j0
            let (fmtSpec, j2) :=
              if formatSpecs && j0 < content.size && is (content.get! j0) 0x25
              then extractFormatSpec content j0
              else (ByteArray.empty, j0)
            let part : Part :=
              { isLiteral := false, text := identName,
                formatSpec := fmtSpec, byteOffset := identStart,
                start := i, endPos := j0 }
            splitAux content formatSpecs fuel j2 ByteArray.empty 0
              (parts1.push part)
          else
            let (lit1, ls1) := writeLit lit litStart i c
            splitAux content formatSpecs fuel (i + 1) lit1 ls1 parts

/-- `split content formatSpecs` parses the body of an interpolated (`s"..."`,
`formatSpecs = false`) or format (`f"..."`, `formatSpecs = true`) string into
ordered literal and expression parts. -/
def split (content : ByteArray) (formatSpecs : Bool := false) : Array Part :=
  splitAux content formatSpecs (content.size + 1) 0 ByteArray.empty 0 #[]

/-- `printedByString part` reports whether `fmt` prints the embedded expression
as it prints an operand of `Println`, by a `String` it has: with no spec, or
with a `%v`/`%s` one that is not Go syntax (`%#v`). -/
def printedByString (p : Part) : Bool :=
  if p.formatSpec.isEmpty then true
  else
    match last? p.formatSpec with
    | some verb => (verb == 0x76 || verb == 0x73) && !contains p.formatSpec 0x23
    | none => true

/-- `position content offset line col` is the line and column of
`content[offset]`, given that `content` starts at line/col of its file.
Columns count runes (what diagnostics index by) and restart at 0 after a
newline; `offset` is a byte index into `content`. -/
def position (content : ByteArray) (offset line col : Nat) : Nat × Nat :=
  if offset > content.size then (line, col)
  else
    let pre := content.extract 0 offset
    match lastNewline pre with
    | some nl =>
      (line + newlineCount pre, runeCount (pre.extract (nl + 1) pre.size))
    | none => (line, col + runeCount pre)

end Gala.Interpolation
