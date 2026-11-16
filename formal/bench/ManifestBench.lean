import Manifest

open Gala.Manifest

def parseReps (s : String) : Nat :=
  match s.toNat? with
  | some n => if n == 0 then 1 else n
  | none => 1

/-- Lean driver for the gala.mod reader/writer: `ManifestBench <reps> <file> [both|parse|format]`.

Prints one `iter=<r> ns=<elapsed>` line per repetition, then a checksum line.
The Go driver in `formal/bench/driver` prints the same checksum for the same
input; `run.sh` refuses to report timings when they differ. -/
def main (args : List String) : IO UInt32 := do
  match args with
  | repsArg :: path :: rest =>
    let reps := parseReps repsArg
    let mode := rest.getD 0 "both"
    if mode != "both" && mode != "parse" && mode != "format" then
      IO.eprintln s!"ManifestBench: mode must be both, parse or format, got {mode}"
      return 2
    let text ← IO.FS.readFile path
    let base ←
      match parse text with
      | .error e =>
        IO.eprintln s!"parse error: {e.toString}"
        return 1
      | .ok f => pure f
    let mut reqs := 0
    let mut repn := 0
    let mut excs := 0
    let mut bytes := 0
    for r in List.range reps do
      let start ← IO.monoNanosNow
      let f ←
        if mode == "format" then pure base
        else
          match parse text with
          | .error e =>
            IO.eprintln s!"parse error: {e.toString}"
            return 1
          | .ok f => pure f
      if mode != "parse" then
        bytes := bytes + (format f).utf8ByteSize
      reqs := reqs + f.requires.length
      repn := repn + f.replaces.length
      excs := excs + f.excludes.length
      let stop ← IO.monoNanosNow
      IO.println s!"iter={r} ns={stop - start}"
    IO.println s!"require={reqs} replace={repn} exclude={excs} bytes={bytes}"
    return 0
  | _ =>
    IO.eprintln "usage: ManifestBench <reps> <manifest-file> [both|parse|format]"
    return 2
