import Interpolation

open Gala.Interpolation

/-- Curated tests for the port; these run in the native binary, so they also
exercise the generated C. -/
def lit (s : String) (off : Nat) : Part :=
  { isLiteral := true, text := s.toUTF8, byteOffset := off }

def expr (s : String) (off : Nat) (fmt : String := "") : Part :=
  { isLiteral := false, text := s.toUTF8, formatSpec := fmt.toUTF8,
    byteOffset := off }

def check (name : String) (content : String) (expected : Array Part) : IO Unit := do
  let got := split content.toUTF8
  if got == expected then
    pure ()
  else do
    IO.eprintln s!"interpolation test failed: {name}"
    IO.Process.exit 1

def main : IO UInt32 := do
  check "empty" "" #[]
  check "plain" "hello" #[lit "hello" 0]
  check "identifier" "a $name b"
    #[lit "a " 0, expr "name" 3, lit " b" 7]
  check "double dollar" "$$" #[lit "$" 0]
  check "embedded dollar" "a$$b" #[lit "a$b" 0]
  check "braced expression" "${x + 1}" #[expr "x + 1" 2]
  check "nested braces" "${a{b}c}" #[expr "a{b}c" 2]
  check "empty expression" "${}" #[expr "" 2]
  check "unterminated expression" "${" #[expr "" 2]
  check "unterminated nonempty" "${a" #[expr "a" 2]
  check "format spec" "${a}%04d" #[expr "a" 2 "%04d"]
  check "identifier format spec" "$name%s" #[expr "name" 1 "%s"]
  check "flags" "${a}%#x" #[expr "a" 2 "%#x"]
  check "space flag" "${a}% d" #[expr "a" 2 "% d"]
  check "width star" "${a}%*d" #[expr "a" 2 "%*d"]
  check "precision star" "${a}%.*f" #[expr "a" 2 "%.*f"]
  check "width precision" "${a}%-5.2f" #[expr "a" 2 "%-5.2f"]
  check "invalid verb" "${a}%i" #[expr "a" 2, lit "%i" 4]
  check "incomplete width" "${a}%04" #[expr "a" 2, lit "%04" 4]
  check "lone percent" "${a}%" #[expr "a" 2, lit "%" 4]
  check "two specs" "${x}%s${y}%d"
    #[expr "x" 2 "%s", expr "y" 8 "%d"]
  check "two interpolations" "$a$b" #[expr "a" 1, expr "b" 3]
  check "adjacent braced" "${x}${y}" #[expr "x" 2, expr "y" 6]
  check "dollar digit" "$1" #[lit "$1" 0]
  check "trailing dollar" "$" #[lit "$" 0]
  check "three dollars" "$$$" #[lit "$$" 0]
  check "escaped quote in expr" "${a\\\"b}" #[expr "a\"b" 2]
  check "double backslash before quote" "${a\\\\\"b}" #[expr "a\\\"b" 2]
  check "backslash at end" "\\" #[lit "\\" 0]
  check "escaped dollar" "\\$x" #[lit "\\$x" 0]
  check "escaped interpolation" "\\${x}" #[lit "\\${x}" 0]
  check "unicode" "héllo ${π}" #[lit "héllo " 0, expr "π" 9]
  check "non-ascii after dollar is literal" "π=$π" #[lit "π=$π" 0]
  IO.println "interpolation tests: ok"
  return 0
