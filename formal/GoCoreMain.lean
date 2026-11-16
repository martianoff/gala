/-
  The `gocore` executable: `gocore run [--fuel N] <prog.json>`. The command line is
  defined in `Gala/GoCore/Cli.lean`. It lives outside `Gala/`, whose modules the proof
  check imports all together, so that its `main` does not clash with galaspec's.
-/
import Gala.GoCore.Cli

def main (args : List String) : IO UInt32 :=
  Gala.GoCore.Full.Cli.main args
