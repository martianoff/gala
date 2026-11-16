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
  small scanners carry fuel so their termination is structural.  The
  top-level byte loop is `partial`: its bound is the input size, and avoiding
  a per-byte fuel argument is what keeps the native port competitive.
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

/-- Byte comparison against a small numeric literal.  Keeping both operands
`UInt8` avoids boxing them as `Nat` on every test. -/
def is (b : UInt8) (n : UInt8) : Bool := b == n

def isIdentStart (b : UInt8) : Bool :=
  is b 0x5F ||
  (b >= 0x61 && b <= 0x7A) ||
  (b >= 0x41 && b <= 0x5A)

def isIdentPart (b : UInt8) : Bool :=
  isIdentStart b || (b >= 0x30 && b <= 0x39)

def isDigit (b : UInt8) : Bool :=
  b >= 0x30 && b <= 0x39

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

/-- Collapse every `$$` in `content[start:stop)` to a single `$`, which is the
literal text that run stands for. -/
partial def collapseDoubleDollars (content : ByteArray) (start stop : Nat)
    (out : ByteArray) : ByteArray :=
  if start < stop then
    match Bytes.get? content start with
    | some 0x5C =>
      -- An escape inside a literal run was copied as-is, so its second byte
      -- must not take part in `$$` collapsing.
      if start + 1 < stop then
        match Bytes.get? content (start + 1) with
        | some c => collapseDoubleDollars content (start + 2) stop ((out.push 0x5C).push c)
        | none => out.push 0x5C
      else out.push 0x5C
    | some 0x24 =>
      if start + 1 < stop then
        match Bytes.get? content (start + 1) with
        | some 0x24 => collapseDoubleDollars content (start + 2) stop (out.push 0x24)
        | _ => collapseDoubleDollars content (start + 1) stop (out.push 0x24)
      else out.push 0x24
    | some c => collapseDoubleDollars content (start + 1) stop (out.push c)
    | none => out
  else out

/-- The literal text of the run `content[start:stop)`.  A run without `$$` is
the source slice itself; one with `$$` needs the doubled dollars collapsed.
Literal runs are kept as source offsets while scanning, so a run costs one
slice, not one array push per byte. -/
def literalText (content : ByteArray) (start stop : Nat)
    (hasDoubleDollar : Bool) : ByteArray :=
  if hasDoubleDollar then collapseDoubleDollars content start stop ByteArray.empty
  else content.extract start stop

/-- Flush the pending literal run (the `litLen` source bytes ending at `stop`)
as a part. -/
def flushLiteral (parts : Array Part) (content : ByteArray) (stop litLen : Nat)
    (hasDoubleDollar : Bool) : Array Part :=
  if litLen == 0 then parts
  else
    let start := stop - litLen
    parts.push
      { isLiteral := true, text := literalText content start stop hasDoubleDollar,
        byteOffset := start, start := start, endPos := stop }

/-- Scan an identifier after `$`.  Returns the index one past its last byte. -/
def scanIdent (a : ByteArray) (fuel : Nat) (j : Nat) : Nat :=
  match fuel with
  | 0 => j
  | fuel + 1 =>
    if h : j < a.size then
      if isIdentPart (a.get j h) then scanIdent a fuel (j + 1) else j
    else j

/-- The result of scanning an embedded expression: the index of the closing
`}`, and whether the slice contains a `\"` that `unescapeExpr` must rewrite.

Recording the escape while the braces are scanned avoids a second pass over
every expression; the expression is only rebuilt when it actually contains
one. -/
structure ExprScan where
  stop : Nat
  escaped : Bool

/-- Locate the `}` closing the `${` at `content[dollar]`.

This mirrors `EndOfEmbeddedExpr`: brace nesting is counted, `\` skips the
following byte, and an unterminated block returns `content.size`.

`escaped` is sticky.  A `\"` can start at a byte the brace scanner skips (the
second backslash of `\\\"`), so each visited backslash also looks one byte
further: the pair at the skipped position starts with that next byte.  This
still visits each expression once. -/
def endOfEmbeddedExprAux (a : ByteArray) (fuel : Nat) (j depth : Nat)
    (escaped : Bool) : ExprScan :=
  match fuel with
  | 0 => { stop := j, escaped }
  | fuel + 1 =>
    if h : j < a.size then
      let c := a.get j h
      if is c 0x7B then
        endOfEmbeddedExprAux a fuel (j + 1) (depth + 1) escaped
      else if is c 0x7D then
        if depth == 1 then { stop := j, escaped }
        else endOfEmbeddedExprAux a fuel (j + 1) (depth - 1) escaped
      else if is c 0x5C then
        if h2 : j + 1 < a.size then
          let escaped :=
            escaped || is (a.get (j + 1) h2) 0x22 ||
              (is (a.get (j + 1) h2) 0x5C &&
                match Bytes.get? a (j + 2) with
                | some 0x22 => true
                | _ => false)
          endOfEmbeddedExprAux a fuel (j + 2) depth escaped
        else
          endOfEmbeddedExprAux a fuel (j + 1) depth escaped
      else
        endOfEmbeddedExprAux a fuel (j + 1) depth escaped
    else { stop := j, escaped }

def endOfEmbeddedExprScan (a : ByteArray) (dollar : Nat) : ExprScan :=
  endOfEmbeddedExprAux a (a.size + 1) (dollar + 2) 1 false

def endOfEmbeddedExpr (a : ByteArray) (dollar : Nat) : Nat :=
  (endOfEmbeddedExprScan a dollar).stop

/-- Whether `a` contains a `\"` pair.  Expressions rarely do, and a plain
slice is cheaper than rebuilding the array byte by byte. -/
def hasQuoteEscape (a : ByteArray) (fuel : Nat) (i : Nat) : Bool :=
  match fuel with
  | 0 => false
  | fuel + 1 =>
    if h : i < a.size then
      if h2 : i + 1 < a.size then
        if is (a.get i h) 0x5C && is (a.get (i + 1) h2) 0x22 then true
        else hasQuoteEscape a fuel (i + 1)
      else false
    else false

/-- Replace `\"` with `"` inside an embedded expression. -/
def unescapeExprAux (a : ByteArray) (fuel : Nat) (i : Nat) (out : ByteArray) :
    ByteArray :=
  match fuel with
  | 0 => out
  | fuel + 1 =>
    if h : i < a.size then
      let c := a.get i h
      if h2 : i + 1 < a.size then
        let c2 := a.get (i + 1) h2
        if is c 0x5C && is c2 0x22 then
          unescapeExprAux a fuel (i + 2) (out.push 0x22)
        else
          unescapeExprAux a fuel (i + 1) (out.push c)
      else
        unescapeExprAux a fuel (i + 1) (out.push c)
    else out

def unescapeExprIf (a : ByteArray) (escaped : Bool) : ByteArray :=
  if escaped then unescapeExprAux a (a.size + 1) 0 ByteArray.empty
  else a

def unescapeExpr (a : ByteArray) : ByteArray :=
  unescapeExprIf a (hasQuoteEscape a (a.size + 1) 0)

def skipFlags (a : ByteArray) (fuel : Nat) (j : Nat) : Nat :=
  match fuel with
  | 0 => j
  | fuel + 1 =>
    if h : j < a.size then
      if isFormatFlag (a.get j h) then skipFlags a fuel (j + 1) else j
    else j

def skipDigits (a : ByteArray) (fuel : Nat) (j : Nat) : Nat :=
  match fuel with
  | 0 => j
  | fuel + 1 =>
    if h : j < a.size then
      if isDigit (a.get j h) then skipDigits a fuel (j + 1) else j
    else j

/-- Extract a Go printf spec starting at `%`; on failure return `("", i)` so
the caller treats the `%` as literal text. -/
def extractFormatSpec (a : ByteArray) (i : Nat) : ByteArray × Nat :=
  if h : i < a.size then
    if is (a.get i h) 0x25 then
      let fuel := a.size + 1
      let j1 := skipFlags a fuel (i + 1)
      let j2 :=
        if h1 : j1 < a.size then
          if is (a.get j1 h1) 0x2A then j1 + 1 else skipDigits a fuel j1
        else j1
      let j3 :=
        if h2 : j2 < a.size then
          if is (a.get j2 h2) 0x2E then
            let k := j2 + 1
            if h3 : k < a.size then
              if is (a.get k h3) 0x2A then k + 1 else skipDigits a fuel k
            else k
          else j2
        else j2
      if h4 : j3 < a.size then
        if isFormatVerb (a.get j3 h4) then
          (a.extract i (j3 + 1), j3 + 1)
        else
          (ByteArray.empty, i)
      else (ByteArray.empty, i)
    else
      (ByteArray.empty, i)
  else
    (ByteArray.empty, i)

/-- Split the CONTENT of an interpolated/format string (the text between the
opening `s"`/`f"` and its closing quote).

With `formatSpecs`, as for an `f"..."` string, a `%spec` following an embedded
expression is captured; in an `s"..."` string a `%` is literal text. -/
partial def splitAux (content : ByteArray) (formatSpecs : Bool)
    (i litLen : Nat) (hasDoubleDollar : Bool) (parts : Array Part) :
    Array Part :=
  if h : i < content.size then
    let c := content.get i h
    if is c 0x5C then
      if i + 1 < content.size then
        splitAux content formatSpecs (i + 2) (litLen + 2) hasDoubleDollar parts
      else
        splitAux content formatSpecs (i + 1) (litLen + 1) hasDoubleDollar parts
    else if !is c 0x24 then
      splitAux content formatSpecs (i + 1) (litLen + 1) hasDoubleDollar parts
    else
      if h2 : i + 1 < content.size then
        let next := content.get (i + 1) h2
        if is next 0x24 then
          splitAux content formatSpecs (i + 2) (litLen + 2) true parts
        else if is next 0x7B then
          let parts1 := flushLiteral parts content i litLen hasDoubleDollar
          let scan := endOfEmbeddedExprScan content i
          let j0 := scan.stop
          let exprStart := i + 2
          let exprText := unescapeExprIf (content.extract exprStart j0) scan.escaped
          let j1 := min (j0 + 1) content.size
          let (fmtSpec, j2) :=
            if formatSpecs then
              if h1 : j1 < content.size then
                if is (content.get j1 h1) 0x25 then extractFormatSpec content j1
                else (ByteArray.empty, j1)
              else (ByteArray.empty, j1)
            else (ByteArray.empty, j1)
          let part : Part :=
            { isLiteral := false, text := exprText,
              formatSpec := fmtSpec, byteOffset := exprStart,
              start := i, endPos := j1 }
          splitAux content formatSpecs j2 0 false
            (parts1.push part)
        else if isIdentStart next then
          let parts1 := flushLiteral parts content i litLen hasDoubleDollar
          let identStart := i + 1
          let j0 := scanIdent content (content.size + 1) identStart
          let identName := content.extract identStart j0
          let (fmtSpec, j2) :=
            if formatSpecs then
              if h1 : j0 < content.size then
                if is (content.get j0 h1) 0x25 then extractFormatSpec content j0
                else (ByteArray.empty, j0)
              else (ByteArray.empty, j0)
            else (ByteArray.empty, j0)
          let part : Part :=
            { isLiteral := false, text := identName,
              formatSpec := fmtSpec, byteOffset := identStart,
              start := i, endPos := j0 }
          splitAux content formatSpecs j2 0 false
            (parts1.push part)
        else
          splitAux content formatSpecs (i + 1) (litLen + 1) hasDoubleDollar parts
      else
        splitAux content formatSpecs (i + 1) (litLen + 1) hasDoubleDollar parts
  else
    flushLiteral parts content i litLen hasDoubleDollar

/-- `split content formatSpecs` parses the body of an interpolated (`s"..."`,
`formatSpecs = false`) or format (`f"..."`, `formatSpecs = true`) string into
ordered literal and expression parts. -/
def split (content : ByteArray) (formatSpecs : Bool := false) : Array Part :=
  -- Most interpolation bodies have a handful of parts; reserving a small
  -- proportional capacity avoids the first few reallocations of `push`
  -- without over-allocating for a long literal-only string.
  splitAux content formatSpecs 0 0 false
    (Array.emptyWithCapacity (min 32 (2 + content.size / 8)))

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
