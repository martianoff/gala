/-
  Lean 4 port of `martianoff/gala/internal/interpolation`.

  The Go original is a pure byte-level splitter for the body of a GALA
  interpolated (`s"..."`) or format (`f"..."`) string.  This port keeps the
  same byte-oriented semantics exactly, including:

  * `$identifier`, `${ expression }`, `$$` (literal `$`);
  * `\`-escape pass-through and `\"` unescaping inside `${...}`;
  * the optional trailing printf spec after an embedded expression;
  * byte offsets into the original content.

  It is deliberately self-contained: no parser or transpiler state.  The
  recursive scanners carry fuel so their termination is structural and the
  generated C stays straightforward.
-/

set_option autoImplicit false

namespace Gala.Interpolation

/-- One piece of an interpolated string: literal text or an embedded
expression with an optional explicit printf format spec. -/
structure Part where
  isLiteral : Bool
  text : ByteArray
  formatSpec : ByteArray := ByteArray.empty
  byteOffset : Nat
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

end Bytes

open Bytes

/-- Append one byte to the pending literal run, recording the run's first
byte offset the first time the run becomes non-empty. -/
def writeLit (lit : ByteArray) (litStart : Nat) (pos : Nat) (b : UInt8) :
    ByteArray × Nat :=
  if lit.isEmpty then (lit.push b, pos) else (lit.push b, litStart)

def flushLiteral (parts : Array Part) (lit : ByteArray) (litStart : Nat) :
    Array Part :=
  if lit.isEmpty then parts
  else parts.push { isLiteral := true, text := lit, byteOffset := litStart }

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
opening `s"`/`f"` and its closing quote). -/
def splitAux (content : ByteArray) :
    Nat → Nat → ByteArray → Nat → Array Part → Array Part
  | 0, _, lit, litStart, parts => flushLiteral parts lit litStart
  | fuel + 1, i, lit, litStart, parts =>
    match get? content i with
    | none => flushLiteral parts lit litStart
    | some c =>
      if is c 0x5C then
        match get? content (i + 1) with
        | some c2 =>
          let (lit1, ls1) := writeLit lit litStart i c
          let (lit2, ls2) := writeLit lit1 ls1 (i + 1) c2
          splitAux content fuel (i + 2) lit2 ls2 parts
        | none =>
          let (lit1, ls1) := writeLit lit litStart i c
          splitAux content fuel (i + 1) lit1 ls1 parts
      else if !is c 0x24 then
        let (lit1, ls1) := writeLit lit litStart i c
        splitAux content fuel (i + 1) lit1 ls1 parts
      else
        match get? content (i + 1) with
        | none =>
          let (lit1, ls1) := writeLit lit litStart i c
          splitAux content fuel (i + 1) lit1 ls1 parts
        | some next =>
          if is next 0x24 then
            let (lit1, ls1) := writeLit lit litStart i c
            splitAux content fuel (i + 2) lit1 ls1 parts
          else if is next 0x7B then
            let parts1 := flushLiteral parts lit litStart
            let j := endOfEmbeddedExpr content i
            let exprStart := i + 2
            let exprText := unescapeExpr (content.extract exprStart j)
            let j1 := j + 1
            let (fmtSpec, j2) :=
              match get? content j1 with
              | some pc =>
                if is pc 0x25 then extractFormatSpec content j1
                else (ByteArray.empty, j1)
              | none => (ByteArray.empty, j1)
            let part : Part :=
              { isLiteral := false, text := exprText,
                formatSpec := fmtSpec, byteOffset := exprStart }
            splitAux content fuel j2 ByteArray.empty 0 (parts1.push part)
          else if isIdentStart next then
            let parts1 := flushLiteral parts lit litStart
            let identStart := i + 1
            let j := scanIdent content (content.size + 1) identStart
            let identName := content.extract identStart j
            let (fmtSpec, j2) :=
              match get? content j with
              | some pc =>
                if is pc 0x25 then extractFormatSpec content j
                else (ByteArray.empty, j)
              | none => (ByteArray.empty, j)
            let part : Part :=
              { isLiteral := false, text := identName,
                formatSpec := fmtSpec, byteOffset := identStart }
            splitAux content fuel j2 ByteArray.empty 0 (parts1.push part)
          else
            let (lit1, ls1) := writeLit lit litStart i c
            splitAux content fuel (i + 1) lit1 ls1 parts

/-- `split content` parses the body of an interpolated or format string into
ordered literal and expression parts. -/
def split (content : ByteArray) : Array Part :=
  splitAux content (content.size + 1) 0 ByteArray.empty 0 #[]

end Gala.Interpolation
