import Manifest

open Gala.Manifest

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

/-- Lean driver for the gala.mod reader/writer: `ManifestBench <reps> <file> [both|parse|format]`.

Prints one `iter=<r> ns=<elapsed>` line per repetition, then a checksum line.
The Go driver in `formal/bench/driver` prints the same checksum for the same
input; `run.sh` refuses to report timings when they differ. A final
`mem load_peak_kib=<n> peak_kib=<n>` line carries the procfs VmHWM readings
that `mem.sh` compares. -/
def main (args : List String) : IO UInt32 := do
  match args with
  | repsArg :: path :: rest =>
    let reps := parseReps repsArg
    let mode := rest.getD 0 "both"
    if mode != "both" && mode != "parse" && mode != "format" then
      IO.eprintln s!"ManifestBench: mode must be both, parse or format, got {mode}"
      return 2
    let text ← IO.FS.readFile path
    -- Read the load mark before the setup parse below: for the manifest driver
    -- that parse is part of the work both sides do, not part of loading.
    let loadPeak ← peakRssOrZero
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
    IO.println s!"mem load_peak_kib={loadPeak} peak_kib={← peakRssOrZero}"
    return 0
  | _ =>
    IO.eprintln "usage: ManifestBench <reps> <manifest-file> [both|parse|format]"
    return 2
