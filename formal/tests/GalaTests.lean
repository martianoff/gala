/-
  Tests of galaspec. Run from `formal/`:

      lake exe galaspec-tests

  * The `#guard`s below check small definitions while this file compiles.
  * `main` runs every Core JSON fixture `tests/<name>.json` and compares the program's
    output with `tests/<name>.out` (empty when absent) and its outcome with the single
    line of `tests/<name>.outcome`. The fixtures are exported from `tests/src/*.gala`
    and from S1 examples; README.md says how to regenerate them.
-/
import Gala.Cli

open Gala.Spec

/-! ## Unit checks -/

-- Integers wrap like Go's int64; `/` and `%` truncate toward zero.
#guard wrap 9223372036854775808 == -9223372036854775808
#guard wrap (-9223372036854775809) == 9223372036854775807
#guard (Int.tdiv (-7) 2, Int.tmod (-7) 2) == (-3, -1)

-- Go integer literal forms.
#guard Json.parseIntLit "0x1F" == some 31
#guard Json.parseIntLit "0b101" == some 5
#guard Json.parseIntLit "0o17" == some 15
#guard Json.parseIntLit "12" == some 12
#guard Json.parseIntLit "0x" == none
#guard Json.parseIntLit "1a" == none

-- Println and Print spacing.
private def noDecls : Decls := { funcs := [], structs := [], sealeds := [], unsupported := [] }
#guard (Print.println noDecls [.int 1, .str "a", .bool true]).toOption == some "1 a true\n"
#guard (Print.print noDecls [.str "a", .int 1, .int 2, .str "b", .str "c"]).toOption == some "a1 2bc"
#guard (Print.println noDecls [.tuple [.int 1, .str "x"], .sealed "S" "C" [.int 2]]).toOption == some "(1, x) C(2)\n"

-- An interpolated string's parts print as `Println`'s operands do, with no separator.
#guard (Print.interp noDecls [.str "n=", .int 7, .str "%d ", .tuple [.int 1, .bool true]]).toOption
  == some "n=7%d (1, true)"

-- The decoder rejects an unknown version and malformed documents.
private def decodes (s : String) : Bool := (Json.parse s).toBool
#guard decodes "{\"core\": 2, \"files\": []}"
#guard !decodes "{\"core\": 1, \"files\": []}"
#guard !decodes "{\"files\": []}"
#guard !decodes "{\"core\": 2, \"files\": [{\"kind\": \"nope\"}]}"
#guard !decodes "not json"

-- The command line.
#guard Cli.parseArgs ["run", "p.json"] == some ("p.json", Cli.defaultFuel)
#guard Cli.parseArgs ["run", "--fuel", "5", "p.json"] == some ("p.json", 5)
#guard Cli.parseArgs ["run", "p.json", "--fuel", "5"] == some ("p.json", 5)
#guard Cli.parseArgs ["run"] == none
#guard Cli.parseArgs ["check", "p.json"] == none
#guard Cli.exitCode "ok" == 0 && Cli.exitCode "panic:match" == 1 && Cli.exitCode "fuel" == 2
#guard Cli.exitCode "unsupported:lambda" == 3 && Cli.exitCode "malformed" == 4

/-! ## Fixtures -/

/-- Read a file with Windows line endings normalized, or `""` when it is absent. -/
def readText (p : System.FilePath) : IO String := do
  if ← p.pathExists then
    return (← IO.FS.readFile p).replace "\r\n" "\n"
  return ""

def main : IO UInt32 := do
  let dir : System.FilePath := "tests"
  let mut names : Array String := #[]
  for e in ← dir.readDir do
    if e.fileName.endsWith ".json" then
      names := names.push (e.fileName.dropEnd 5).toString
  names := names.qsort (· < ·)
  let mut failed := 0
  for n in names do
    let (out, outcome, _) := Cli.evaluate (← IO.FS.readFile (dir / s!"{n}.json")) Cli.defaultFuel
    let wantOut ← readText (dir / s!"{n}.out")
    let wantOutcome := (← readText (dir / s!"{n}.outcome")).trimAscii.toString
    if out != wantOut || outcome != wantOutcome then
      failed := failed + 1
      IO.eprintln s!"FAIL {n}: outcome {outcome}, want {wantOutcome}"
      if out != wantOut then
        IO.eprintln s!"--- got stdout:\n{out}--- want stdout:\n{wantOut}---"
    else
      IO.println s!"ok   {n} ({outcome})"
  if names.isEmpty then
    IO.eprintln "galaspec-tests: no fixtures found; run from formal/"
    return 1
  IO.println s!"galaspec-tests: {names.size - failed}/{names.size} fixtures pass"
  return (if failed == 0 then 0 else 1)
