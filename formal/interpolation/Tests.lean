import Interpolation

open Gala.Interpolation

/-- Curated tests for the port; these run in the native binary, so they also
exercise the generated C. -/

def lit (s : String) (off start stop : Nat) : Part :=
  { isLiteral := true, text := s.toUTF8, byteOffset := off,
    start := start, endPos := stop }

def expr (s : String) (off start stop : Nat) (fmt : String := "") : Part :=
  { isLiteral := false, text := s.toUTF8, formatSpec := fmt.toUTF8,
    byteOffset := off, start := start, endPos := stop }

def check (name : String) (formatSpecs : Bool) (content : String)
    (expected : Array Part) : IO Unit := do
  let got := split content.toUTF8 formatSpecs
  if got == expected then
    pure ()
  else do
    IO.eprintln s!"interpolation test failed: {name}"
    IO.Process.exit 1

def checkPrinted (name : String) (p : Part) (expected : Bool) : IO Unit := do
  if printedByString p == expected then
    pure ()
  else do
    IO.eprintln s!"interpolation test failed: {name} (printedByString)"
    IO.Process.exit 1

def checkPos (name : String) (content : ByteArray) (offset line col
    expectedLine expectedCol : Nat) : IO Unit := do
  let (l, c) := position content offset line col
  if l == expectedLine && c == expectedCol then
    pure ()
  else do
    IO.eprintln s!"interpolation test failed: {name} (position {l}:{c}, want {expectedLine}:{expectedCol})"
    IO.Process.exit 1

def bytes (xs : List UInt8) : ByteArray := ByteArray.mk xs.toArray

def main : IO UInt32 := do
  check "empty" false "" #[]
  check "plain" false "hello" #[lit "hello" 0 0 5]
  check "identifier" false "a $name b"
    #[lit "a " 0 0 2, expr "name" 3 2 7, lit " b" 7 7 9]
  check "double dollar" false "$$" #[lit "$" 0 0 2]
  check "embedded dollar" false "a$$b" #[lit "a$b" 0 0 4]
  check "braced expression" false "${x + 1}" #[expr "x + 1" 2 0 8]
  check "nested braces" false "${a{b}c}" #[expr "a{b}c" 2 0 8]
  check "empty expression" false "${}" #[expr "" 2 0 3]
  check "unterminated expression" false "${" #[expr "" 2 0 2]
  check "unterminated nonempty" false "${a" #[expr "a" 2 0 3]
  check "percent is literal in s" false "${a}%04d"
    #[expr "a" 2 0 4, lit "%04d" 4 4 8]
  check "identifier percent is literal in s" false "$name%s"
    #[expr "name" 1 0 5, lit "%s" 5 5 7]
  check "format spec" true "${a}%04d" #[expr "a" 2 0 4 "%04d"]
  check "identifier format spec" true "$name%s" #[expr "name" 1 0 5 "%s"]
  check "flags" true "${a}%#x" #[expr "a" 2 0 4 "%#x"]
  check "space flag" true "${a}% d" #[expr "a" 2 0 4 "% d"]
  check "width star" true "${a}%*d" #[expr "a" 2 0 4 "%*d"]
  check "precision star" true "${a}%.*f" #[expr "a" 2 0 4 "%.*f"]
  check "width precision" true "${a}%-5.2f" #[expr "a" 2 0 4 "%-5.2f"]
  check "invalid verb" true "${a}%i"
    #[expr "a" 2 0 4, lit "%i" 4 4 6]
  check "incomplete width" true "${a}%04"
    #[expr "a" 2 0 4, lit "%04" 4 4 7]
  check "lone percent" true "${a}%"
    #[expr "a" 2 0 4, lit "%" 4 4 5]
  check "two specs" true "${x}%s${y}%d"
    #[expr "x" 2 0 4 "%s", expr "y" 8 6 10 "%d"]
  check "two interpolations" false "$a$b"
    #[expr "a" 1 0 2, expr "b" 3 2 4]
  check "adjacent braced" false "${x}${y}"
    #[expr "x" 2 0 4, expr "y" 6 4 8]
  check "dollar digit" false "$1" #[lit "$1" 0 0 2]
  check "trailing dollar" false "$" #[lit "$" 0 0 1]
  check "three dollars" false "$$$" #[lit "$$" 0 0 3]
  check "escaped quote in expr" false "${a\\\"b}" #[expr "a\"b" 2 0 7]
  check "double backslash before quote" false "${a\\\\\"b}"
    #[expr "a\\\"b" 2 0 8]
  check "backslash at end" false "\\" #[lit "\\" 0 0 1]
  check "escaped dollar" false "\\$x" #[lit "\\$x" 0 0 3]
  check "escaped interpolation" false "\\${x}" #[lit "\\${x}" 0 0 5]
  check "unicode" false "héllo ${π}"
    #[lit "héllo " 0 0 7, expr "π" 9 7 12]
  check "non-ascii after dollar is literal" false "π=$π" #[lit "π=$π" 0 0 6]

  checkPrinted "no spec" (expr "a" 2 0 4) true
  checkPrinted "percent v" (expr "a" 2 0 4 "%v") true
  checkPrinted "percent s" (expr "a" 2 0 4 "%s") true
  checkPrinted "go syntax v" (expr "a" 2 0 4 "%#v") false
  checkPrinted "percent x" (expr "a" 2 0 4 "%x") false
  checkPrinted "width s" (expr "a" 2 0 4 "%-5.2s") true

  checkPos "start" "héllo ${π}".toUTF8 0 3 7 3 7
  checkPos "after unicode" "héllo ${π}".toUTF8 9 3 7 3 15
  checkPos "end" "héllo ${π}".toUTF8 12 3 7 3 17
  checkPos "past end" "abc".toUTF8 4 3 7 3 7
  checkPos "newline" "a\nb".toUTF8 2 3 7 4 0
  checkPos "newline end" "a\nb".toUTF8 3 3 7 4 1
  checkPos "line start" "a\nb".toUTF8 1 3 7 3 8
  checkPos "invalid utf8" (bytes [0xE0, 0x80, 0x80]) 3 1 0 1 3
  checkPos "overlong" (bytes [0xC0, 0xAF]) 2 1 0 1 2
  checkPos "surrogate" (bytes [0xED, 0xA0, 0x80]) 3 1 0 1 3
  checkPos "four byte rune" (bytes [0xF0, 0x9F, 0x98, 0x80]) 4 1 0 1 1
  checkPos "truncated four byte" (bytes [0xF0, 0x9F]) 2 1 0 1 2

  IO.println "interpolation tests: ok"
  return 0
