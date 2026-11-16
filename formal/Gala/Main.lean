/-
  The `galaspec` executable: `galaspec run [--fuel N] <file.json>`, and
  `galaspec alpha <a.json> <b.json>`. The command line is defined in `Gala/Cli.lean`.
-/
import Gala.Cli

open Gala.Spec.Cli

/-- `galaspec alpha a.json b.json`. -/
def alpha (a b : String) : IO UInt32 := do
  match ← (IO.FS.readFile a).toBaseIO, ← (IO.FS.readFile b).toBaseIO with
  | .ok ta, .ok tb => alphaMain ta tb
  | .error e, _ => alphaMalformed s!"cannot read {a}: {e}"
  | _, .error e => alphaMalformed s!"cannot read {b}: {e}"

/-- `galaspec run [--fuel N] <file.json>`. -/
def runCmd (args : List String) : IO UInt32 := do
  match parseArgs args with
  | none => malformed usage
  | some (path, fuel) =>
    match ← (IO.FS.readFile path).toBaseIO with
    | .error e => malformed s!"cannot read {path}: {e}"
    | .ok text => runText text fuel

def main (args : List String) : IO UInt32 :=
  match args with
  | ["alpha", a, b] => alpha a b
  | "alpha" :: _ => alphaMalformed alphaUsage
  | _ => runCmd args
