/-
  # `gocore`: GoCore's command line

      gocore run [--fuel N] <prog.json>

  Reads a GoCore JSON document (`internal/formal/gocoreexport/SCHEMA.md`), runs it with
  `runProgram`, and prints the program's output on stdout, exactly. On stderr, a panic
  adds the line `gocore: panic: <text>`, `<text>` being what Go's runtime prints after
  `panic: `, and the last line is

      gocore: outcome=<ok|panic|fuel|unsupported:CONSTRUCT|stuck|malformed>

  The exit code says the same: 0 ok, 1 panic, 2 fuel (inconclusive), 3 unsupported,
  4 malformed input (or a bad command line), 5 stuck (a GoCore or exporter bug).

  A development and CI tool only, like galaspec: no Lean-built binary ships with GALA
  (docs/FORMAL_SEMANTICS.MD, "Licensing").
-/
import Gala.GoCore.Json
import Gala.GoCore.Run

namespace Gala.GoCore.Full.Cli

/-- Steps, and depth, a run may take unless `--fuel` says otherwise. -/
def defaultFuel : Nat := 10000000

def usage : String := "usage: gocore run [--fuel N] <prog.json>"

/-- Parse `run [--fuel N] <file>` with `--fuel` before or after the file. -/
def parseArgs : List String → Option (String × Nat)
  | "run" :: rest =>
    let rec go : List String → Option String → Nat → Option (String × Nat)
      | [], some f, n => some (f, n)
      | [], none, _ => none
      | "--fuel" :: v :: r, f, _ => v.toNat?.bind fun n => go r f n
      | x :: r, none, n => if x.startsWith "-" then none else go r (some x) n
      | _ :: _, some _, _ => none
    go rest none defaultFuel
  | _ => none

/-- The outcome line's value, the detail line, and the exit code of an outcome. -/
def report : Outcome → String × Option String × UInt32
  | .ok => ("ok", none, 0)
  | .panic text => ("panic", some s!"gocore: panic: {text}", 1)
  | .fuel => ("fuel", none, 2)
  | .unsupported what => (s!"unsupported:{what}", none, 3)
  | .stuck msg => ("stuck", some s!"gocore: stuck: {msg}", 5)

def malformed (msg : String) : IO UInt32 := do
  IO.eprintln s!"gocore: {msg}"
  IO.eprintln "gocore: outcome=malformed"
  pure 4

/-- Run a GoCore JSON text: print its output and outcome, return the exit code. -/
def runText (text : String) (fuel : Nat) : IO UInt32 := do
  match Json.parseProgram text with
  | .error e => malformed s!"malformed input: {e}"
  | .ok prog =>
    let (o, out) := runProgram prog fuel
    let stdout ← IO.getStdout
    stdout.putStr out
    stdout.flush
    let (line, detail, code) := report o
    if let some d := detail then IO.eprintln d
    IO.eprintln s!"gocore: outcome={line}"
    pure code

def main (args : List String) : IO UInt32 := do
  match parseArgs args with
  | none => malformed usage
  | some (path, fuel) =>
    match ← (IO.FS.readFile path).toBaseIO with
    | .error e => malformed s!"cannot read {path}: {e}"
    | .ok text => runText text fuel

end Gala.GoCore.Full.Cli
