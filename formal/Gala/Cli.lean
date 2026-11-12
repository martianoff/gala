/-
  # `galaspec`: the reference interpreter's command line

      galaspec run [--fuel N] <file.json>

  Reads a Core JSON document, runs it with `Gala.Spec.run`, and prints the program's
  output on stdout, exactly. On stderr, the last line is

      galaspec: outcome=<ok|panic:CLASS|fuel|unsupported:KIND|malformed>

  and the exit code says the same: 0 ok, 1 panic, 2 fuel (inconclusive), 3 unsupported,
  4 malformed input (or a bad command line). CLASS is `match`, `divide`, `index` or
  `other`. KIND is the Core JSON `unsupported` name when execution reached such a node,
  and otherwise a short name of the construct galaspec does not model.

  A development and CI tool only: no Lean-built binary ships with GALA
  (docs/FORMAL_SEMANTICS.MD, "Licensing").
-/
import Gala.Json
import Gala.Eval

namespace Gala.Spec.Cli

/-- Steps a run may take unless `--fuel` says otherwise. -/
def defaultFuel : Nat := 1000000

def classText : PanicClass → String
  | .match_ => "match"
  | .divide => "divide"
  | .index => "index"
  | .other => "other"

/-- The outcome line's value of an outcome. -/
def outcomeText : Outcome → String
  | .ok => "ok"
  | .panic c _ => s!"panic:{classText c}"
  | .fuel => "fuel"
  | .unsupported k _ => s!"unsupported:{k}"
  | .stuck _ => "unsupported:stuck"

/-- The detail line printed before the outcome line, if any. -/
def detailText : Outcome → Option String
  | .panic _ m => some s!"galaspec: panic: {m}"
  | .unsupported _ "" => none
  | .unsupported k m => some s!"galaspec: unsupported {k}: {m}"
  | .stuck m => some s!"galaspec: stuck: {m}"
  | _ => none

def usage : String := "usage: galaspec run [--fuel N] <file.json>"

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

def malformed (msg : String) : IO UInt32 := do
  IO.eprintln s!"galaspec: {msg}"
  IO.eprintln "galaspec: outcome=malformed"
  pure 4

/-- What running a Core JSON text gives: the program's output, the outcome line's value
    (`malformed` for input that does not decode), and the detail line, if any. -/
def evaluate (text : String) (fuel : Nat) : String × String × Option String :=
  match Json.parse text with
  | .error e => ("", "malformed", some s!"galaspec: malformed input: {e}")
  | .ok prog =>
    let (o, out) := run prog fuel
    (out, outcomeText o, detailText o)

/-- The exit code of an outcome line's value. -/
def exitCode (outcome : String) : UInt32 :=
  if outcome == "ok" then 0
  else if outcome.startsWith "panic:" then 1
  else if outcome == "fuel" then 2
  else if outcome.startsWith "unsupported:" then 3
  else 4

/-- Run a Core JSON text: print its output and outcome, return the exit code. -/
def runText (text : String) (fuel : Nat) : IO UInt32 := do
  let (out, outcome, detail) := evaluate text fuel
  let stdout ← IO.getStdout
  stdout.putStr out
  stdout.flush
  if let some d := detail then IO.eprintln d
  IO.eprintln s!"galaspec: outcome={outcome}"
  pure (exitCode outcome)

end Gala.Spec.Cli
