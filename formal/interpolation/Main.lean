/-
  The standalone `interpolation` executable: read an `s"..."` or `f"..."`
  body on stdin and print its parts (see README.md). The implementation is in
  `InterpolationCli.lean`, which `gala-lean` also imports.
-/
import InterpolationCli

def main (args : List String) : IO UInt32 :=
  Gala.Interpolation.Cli.main args
