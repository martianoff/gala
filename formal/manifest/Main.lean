/-
  The standalone `manifest` executable: run the `gala.mod` batch protocol from
  stdin (see README.md). The implementation is in `ManifestCli.lean`, which
  `gala-lean` also imports.
-/
import ManifestCli

def main (args : List String) : IO UInt32 :=
  Gala.Manifest.Cli.main args
