/-
  Lean 4 port of `martianoff/gala/galaerr`: compiler diagnostics.

  The Go package has two user-facing renderings of the same typed errors:

  * `RenderRich`, the framed terminal diagnostic (a locus, the source line, a
    caret span and a hint);
  * `RenderJSON`, the machine-readable envelope used by `gala build --json`.

  This port keeps both byte-for-byte, including the ANSI colorization, tab
  alignment, rune-counted columns, caret-width derivation, hint shortening,
  the JSON field order and Go's JSON string escaping.

  Source-file resolution is the caller's job here: the port takes the source
  text the Go renderer would have read (the CLI's fallback source), so the
  module stays free of file IO. `Error` is the normalized renderer view of a
  typed error; a `MultiError` is `multi`, and anything that is not a GALA error
  is `raw`, which both renderings pass through (JSON adds its severity and
  message).
-/

set_option autoImplicit false

namespace Gala.Diagnostics

/-! ## Strings and JSON -/

def isWhitespace (c : Char) : Bool := c.isWhitespace

/-- `strings.TrimSpace`. -/
def trimSpace (s : String) : String :=
  let cs := s.toList
  let rec dropFront : List Char → List Char
    | c :: rest => if isWhitespace c then dropFront rest else c :: rest
    | [] => []
  let rec dropBack (fuel : Nat) (cs : List Char) : List Char :=
    match fuel with
    | 0 => cs
    | fuel + 1 =>
      match cs.reverse with
      | c :: rest => if isWhitespace c then dropBack fuel rest.reverse else cs
      | [] => []
  String.ofList (dropBack (cs.length + 1) (dropFront cs))

/-- Remove exactly one leading UTF-8 byte order mark, as `galaerr.StripBOM`. -/
def stripBOM (s : String) : String :=
  if s.startsWith "\uFEFF" then String.ofList (s.toList.drop 1) else s

/-- The lowercase hexadecimal form of `n` padded to two digits. -/
def hex2 (n : Nat) : String :=
  let digits : List Char := "0123456789abcdef".toList
  String.ofList [digits.getD (n / 16) '0', digits.getD (n % 16) '0']

/-- One character of a JSON string, escaped as Go's `encoding/json` escapes it
with `SetEscapeHTML(false)`: quotes, backslashes, the four short control
escapes, the remaining C0 controls, and U+2028/U+2029. -/
def jsonEscapeChar (c : Char) : String :=
  if c == '"' then "\\\""
  else if c == '\\' then "\\\\"
  else if c == '\n' then "\\n"
  else if c == '\r' then "\\r"
  else if c == '\t' then "\\t"
  else if c == '\u0008' then "\\b"
  else if c == '\u000C' then "\\f"
  else if c.toNat < 0x20 then "\\u00" ++ hex2 c.toNat
  else if c.toNat == 0x2028 then "\\u2028"
  else if c.toNat == 0x2029 then "\\u2029"
  else String.singleton c

def jsonEscape (s : String) : String :=
  String.join (s.toList.map jsonEscapeChar)

/-! ## The normalized diagnostic -/

/-- One diagnostic as the renderer sees it. `line` is 1-based and 0 when the
diagnostic has no position; `column` and `endColumn` are 0-based rune indexes
(`endColumn` exclusive and `<= column` when unset). -/
structure Diagnostic where
  code : String := ""
  message : String := ""
  hint : String := ""
  filePath : String := ""
  line : Nat := 0
  column : Nat := 0
  endColumn : Nat := 0
  deriving BEq, Inhabited

/-- A renderable error: one typed diagnostic, an untyped error passed through,
or the flattening `MultiError` (whose parts the two renderings join). -/
inductive Error where
  | diag (d : Diagnostic)
  | raw (text : String)
  | multi (errs : List Error)
  deriving Inhabited

structure Options where
  fallbackPath : String := ""
  fallbackSource : String := ""
  color : Bool := false
  deriving Inhabited

/-! ## The terminal rendering -/

def esc : String := String.ofList [Char.ofNat 27]

def ansiReset : String := esc ++ "[0m"
def ansiRedBold : String := esc ++ "[1;31m"
def ansiDim : String := esc ++ "[2m"
def ansiCyan : String := esc ++ "[36m"

def colorize (s code : String) (color : Bool) : String :=
  if color then code ++ s ++ ansiReset else s

/-- The source text the Go renderer would frame: the diagnostic's own file when
it could read it (not modelled here), else the caller's fallback source when
the diagnostic has no path or names the fallback. -/
def resolveSource (d : Diagnostic) (opts : Options) : String :=
  if opts.fallbackSource != "" &&
      (d.filePath == "" || d.filePath == opts.fallbackPath) then
    stripBOM opts.fallbackSource
  else
    ""

def renderHeader (d : Diagnostic) (opts : Options) : String :=
  let label := if d.code == "" then "error" else "error[" ++ d.code ++ "]"
  colorize label ansiRedBold opts.color ++ ": " ++ d.message

def renderFooter (hint : String) (color : Bool) : String :=
  "= " ++ colorize "hint:" ansiCyan color ++ " " ++ hint

def isIdentRune (c : Char) : Bool :=
  c == '_' ||
  (c.toNat >= 0x61 && c.toNat <= 0x7A) ||
  (c.toNat >= 0x41 && c.toNat <= 0x5A) ||
  (c.toNat >= 0x30 && c.toNat <= 0x39)

/-- The width of the caret row: an explicit span when one is set (clamped to
the line), else the token at `column` — an identifier run, a quoted string to
its closing delimiter, or one character. -/
def spanWidth (srcLine : String) (column endColumn : Nat) : Nat :=
  let runes := srcLine.toList
  if endColumn > column then
    let e := min endColumn runes.length
    if e > column then e - column else 1
  else if column >= runes.length then 1
  else
    let ch := runes.getD column ' '
    if ch == '"' || ch == '`' then
      let rec find (fuel j : Nat) : Nat :=
        match fuel with
        | 0 => runes.length - column
        | fuel + 1 =>
          if j >= runes.length then runes.length - column
          else if runes.getD j ' ' == ch then j - column + 1
          else find fuel (j + 1)
      find (runes.length + 1) (column + 1)
    else if isIdentRune ch then
      let rec run (fuel j : Nat) : Nat :=
        match fuel with
        | 0 => j
        | fuel + 1 =>
          if j < runes.length && isIdentRune (runes.getD j ' ') then run fuel (j + 1)
          else j
      run (runes.length + 1) column - column
    else 1

/-- Whitespace aligning the caret with `column`; tabs in the source stay tabs,
so the caret lines up with tab-expanded text. -/
def caretIndent (srcLine : String) (column : Nat) : String :=
  let runes := srcLine.toList
  String.ofList ((List.range column).map fun i =>
    if i < runes.length && runes.getD i ' ' == '\t' then '\t' else ' ')

/-- The first clause of a hint, for the caret row: the text before the first
` (`, `; ` or `. `, trimmed and shortened to 60 runes with an ellipsis. -/
def terseHint (hint : String) : String :=
  if hint == "" then ""
  else
    let cs := hint.toList
    let rec firstSep : List Char → Nat → Option Nat
      | [], _ => none
      | [_], _ => none
      | c1 :: c2 :: rest, i =>
        if (c1 == ' ' && c2 == '(') || (c1 == ';' && c2 == ' ') ||
            (c1 == '.' && c2 == ' ') then some i
        else firstSep (c2 :: rest) (i + 1)
    let cut := (firstSep cs 0).getD cs.length
    let s := trimSpace (String.ofList (cs.take cut))
    let clipped :=
      if s.toList.length > 60 then
        let short := trimSpace (String.ofList (s.toList.take 60))
        short ++ "…"
      else s
    if clipped == "" then trimSpace hint else clipped

def renderDiagnostic (d : Diagnostic) (opts : Options) : String :=
  let header := renderHeader d opts
  let src := resolveSource d opts
  let lines := src.splitOn "\n"
  if d.line == 0 || src == "" || d.line - 1 >= lines.length then
    if d.hint != "" then header ++ "\n" ++ renderFooter d.hint opts.color
    else header
  else
    let srcLine := lines.getD (d.line - 1) ""
    let gutter := toString d.line
    let pad := String.ofList (List.replicate gutter.length ' ')
    let pipe := colorize "|" ansiDim opts.color
    let arrow := colorize "-->" ansiDim opts.color
    let displayPath :=
      if d.filePath != "" then d.filePath
      else if opts.fallbackPath != "" then opts.fallbackPath
      else "<input>"
    let carets :=
      colorize (String.ofList (List.replicate
        (spanWidth srcLine d.column d.endColumn) '^')) ansiRedBold opts.color
    let ann :=
      let t := terseHint d.hint
      if t == "" then "" else " " ++ t
    header
      ++ "\n" ++ "  " ++ arrow ++ " " ++ displayPath ++ ":" ++
        toString d.line ++ ":" ++ toString (d.column + 1)
      ++ "\n" ++ pad ++ " " ++ pipe
      ++ "\n" ++ gutter ++ " " ++ pipe ++ " " ++ srcLine
      ++ "\n" ++ pad ++ " " ++ pipe ++ " " ++ caretIndent srcLine d.column ++
        carets ++ ann
      ++ "\n" ++ pad ++ " " ++ pipe
      ++ (if d.hint != "" then "\n" ++ pad ++ " " ++ renderFooter d.hint opts.color
          else "")

/-- `RenderRich`: one framed diagnostic, a `MultiError`'s diagnostics separated
by a blank line, or an untyped error's text unchanged. -/
def renderRich : Error → Options → String
  | .multi errs, opts => String.intercalate "\n\n" (errs.map fun e => renderRich e opts)
  | .raw text, _ => text
  | .diag d, opts => renderDiagnostic d opts

/-! ## The JSON rendering -/

def docsBaseURL : String := "https://gala.fyi/docs/errors"

/-- The published page for a code, or "" when the diagnostic has none. -/
def docsURL (code : String) : String :=
  if code == "" then "" else docsBaseURL ++ "/" ++ code.toLower ++ "/"

/-- One JSON record, with the 1-based columns and derived docs URL. -/
structure JSONDiagnostic where
  severity : String := "error"
  code : String := ""
  message : String := ""
  hint : String := ""
  file : String := ""
  line : Nat := 0
  column : Nat := 0
  endColumn : Nat := 0
  docsURL : String := ""
  deriving BEq, Inhabited

def toJSON (d : Diagnostic) : JSONDiagnostic :=
  { code := d.code, message := d.message, hint := d.hint, file := d.filePath
    line := d.line, docsURL := docsURL d.code
    column := if d.line > 0 then d.column + 1 else 0
    endColumn :=
      if d.line > 0 && d.endColumn > d.column then d.endColumn + 1 else 0 }

/-- The records of an error, flattening a `MultiError` in order. -/
def toJSONList : Error → List JSONDiagnostic
  | .diag d => [toJSON d]
  | .raw text => [{ message := text }]
  | .multi errs => errs.flatMap toJSONList

def jsonStringField (k v : String) : String :=
  "      \"" ++ k ++ "\": \"" ++ jsonEscape v ++ "\""

def jsonNumberField (k : String) (n : Nat) : String :=
  "      \"" ++ k ++ "\": " ++ toString n

def renderJSONDiagnostic (d : JSONDiagnostic) : String :=
  let fields : List (Option String) :=
    [ some (jsonStringField "severity" d.severity),
      if d.code == "" then none else some (jsonStringField "code" d.code),
      some (jsonStringField "message" d.message),
      if d.hint == "" then none else some (jsonStringField "hint" d.hint),
      if d.file == "" then none else some (jsonStringField "file" d.file),
      if d.line == 0 then none else some (jsonNumberField "line" d.line),
      if d.column == 0 then none else some (jsonNumberField "column" d.column),
      if d.endColumn == 0 then none else some (jsonNumberField "endColumn" d.endColumn),
      if d.docsURL == "" then none else some (jsonStringField "docsUrl" d.docsURL) ]
  let rendered := fields.filterMap id
  "    {\n" ++ String.intercalate ",\n" rendered ++ "\n    }"

/-- `RenderJSON`: the indented `{"diagnostics": [...]}` envelope, with the
caller's hint filling the first diagnostic that has none (the CLI's extra
remediation). An empty error is an empty list. -/
def renderJSON (err : Option Error) (hint : String := "") : String :=
  let records := match err with
    | none => []
    | some e => toJSONList e
  let records :=
    if hint == "" then records
    else
      let rec fill : List JSONDiagnostic → List JSONDiagnostic
        | [] => []
        | d :: rest =>
          if d.hint == "" then { d with hint := hint } :: rest
          else d :: fill rest
      fill records
  let body :=
    if records.isEmpty then "[]"
    else "[\n" ++ String.intercalate ",\n" (records.map renderJSONDiagnostic) ++ "\n  ]"
  "{\n  \"diagnostics\": " ++ body ++ "\n}"

end Gala.Diagnostics
