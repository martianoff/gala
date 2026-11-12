/-
  The `galaspec` executable: `galaspec run [--fuel N] <file.json>`. The command line
  is defined in `Gala/Cli.lean`.
-/
import Gala.Cli

open Gala.Spec.Cli in
def main (args : List String) : IO UInt32 := do
  match parseArgs args with
  | none => malformed usage
  | some (path, fuel) =>
    match ← (IO.FS.readFile path).toBaseIO with
    | .error e => malformed s!"cannot read {path}: {e}"
    | .ok text => runText text fuel
