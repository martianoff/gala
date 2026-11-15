/-
  The standalone `diagnostics` executable: render diagnostic cases from stdin
  (see README.md). The implementation is in `DiagnosticsCli.lean`, which
  `gala-lean` also imports.
-/
import DiagnosticsCli

def main (args : List String) : IO UInt32 :=
  Gala.Diagnostics.Cli.main args
