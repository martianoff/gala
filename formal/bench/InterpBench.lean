import Interpolation

open Gala.Interpolation

def parseReps (s : String) : Nat :=
  match s.toNat? with
  | some n => if n == 0 then 1 else n
  | none => 1

/-- This process's peak resident set size in KiB, read from `VmHWM` in
procfs. `none` on platforms without `/proc/self/status`; `mem.sh` treats zero
as an error instead of reporting a made-up number. Reading a high-water mark
does not disturb the measured loop. -/
def peakRssKiB : IO (Option Nat) := do
  try
    let status ← IO.FS.readFile "/proc/self/status"
    let mut peak : Option Nat := none
    for line in status.splitOn "\n" do
      if line.startsWith "VmHWM:" then
        let rest := (line.drop "VmHWM:".length).trimAscii.toString
        peak := (String.splitOn rest " ").headD "" |>.toNat?
    return peak
  catch _ =>
    return none

def peakRssOrZero : IO Nat := do
  return (← peakRssKiB).getD 0

/-- Lean driver for the interpolation splitter: `InterpBench <s|f> <reps> <cases-file>`.

Prints one `iter=<r> ns=<elapsed>` line per repetition, then a checksum line.
The Go driver in `formal/bench/driver` prints the same checksum for the same
input; `run.sh` refuses to report timings when they differ. A final
`mem load_peak_kib=<n> peak_kib=<n>` line carries the procfs VmHWM readings
that `mem.sh` compares. -/
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
    -- Corpus load is complete here; mem.sh reports this high-water mark next
    -- to the final peak so the port's work can be separated from the load.
    let loadPeak ← peakRssOrZero
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
    IO.println s!"mem load_peak_kib={loadPeak} peak_kib={← peakRssOrZero}"
    return 0
  | _ =>
    IO.eprintln "usage: InterpBench <s|f> <reps> <cases-file>"
    return 2
