import Interpolation

open Gala.Interpolation

def parseReps (s : String) : Nat :=
  match s.toNat? with
  | some n => if n == 0 then 1 else n
  | none => 1

/-- Lean driver for the interpolation splitter: `InterpBench <s|f> <reps> <cases-file>`.

Prints one `iter=<r> ns=<elapsed>` line per repetition, then a checksum line.
The Go driver in `formal/bench/driver` prints the same checksum for the same
input; `run.sh` refuses to report timings when they differ. -/
def main (args : List String) : IO UInt32 := do
  match args with
  | mode :: repsArg :: path :: _ =>
    if mode != "s" && mode != "f" then
      IO.eprintln s!"InterpBench: mode must be s or f, got {mode}"
      return 2
    let reps := parseReps repsArg
    let text ← IO.FS.readFile path
    let lines := (text.splitOn "\n").filter (fun l => !l.isEmpty)
    let contents := lines.map (fun l => l.toUTF8) |>.toArray
    let formatSpecs := mode == "f"
    let mut parts := 0
    let mut bytes := 0
    let mut total := 0
    for r in List.range reps do
      let start ← IO.monoNanosNow
      for content in contents do
        let ps := split content formatSpecs
        parts := parts + ps.size
        for p in ps do
          bytes := bytes + p.text.size + p.formatSpec.size
      let stop ← IO.monoNanosNow
      total := total + (stop - start)
      IO.println s!"iter={r} ns={stop - start}"
    IO.println s!"parts={parts} bytes={bytes} ns={total}"
    return 0
  | _ =>
    IO.eprintln "usage: InterpBench <s|f> <reps> <cases-file>"
    return 2
