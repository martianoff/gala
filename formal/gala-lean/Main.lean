/-
  `gala-lean`: a draft of GALA re-implemented in Lean, as one executable.

  This is a *combiner*, not a compiler. It links the existing Lean components
  of the repository and gives them one command line:

  * `run` — the reference interpreter (`galaspec`) on a GALA Core JSON program;
  * `interp` — the `s"..."`/`f"..."` splitter (`formal/interpolation`);
  * `diag` — the terminal and JSON diagnostic renderers (`formal/diagnostics`);
  * `mod` — the `gala.mod` reader and writer (`formal/manifest`).

  What it cannot do yet is the part that is still Go: parse GALA source and
  emit Go. `run` therefore reads the Core JSON that
  `internal/formal/coreexport/galacore` exports from `.gala` sources; the
  `run` command says so when given a `.gala` file. The design and the honest
  coverage rules are in `formal/README.md` and
  `docs/FORMAL_SEMANTICS.MD`.
-/
import Gala.Cli
import InterpolationCli
import DiagnosticsCli
import ManifestCli

namespace GalaLean

/-- Version of this draft. Independent of the GALA release version below. -/
def version : String := "0.1.0-draft"

/-- The `gala` version `mod init` writes. Keep it in step with `gala.mod` in
the repository root when the release version changes. -/
def galaVersion : String := "0.101.0"

def usage : String :=
"gala-lean — GALA components re-implemented in Lean (draft)

usage: gala-lean <command> [arguments]

commands:
  run [--fuel N] <file.json|->  run a GALA Core JSON program (galaspec)
  interp [--format] [--batch]   split s\"...\" / f\"...\" bodies read from stdin
  diag [--batch]                render diagnostics read from stdin
  mod init [module-path]        create a gala.mod in the current directory
  mod show [gala.mod]           summarize a manifest
  mod format [gala.mod]         print a manifest canonically
  mod --batch                   run the manifest port's differential protocol
  version                       print the version
  help                          this text

`run` reads Core JSON, the format the formal semantics consumes. This draft
has no GALA source parser yet; export a program from its source with:

  bazel run //internal/formal/coreexport/galacore -- prog.gala > prog.json

The components are development tools and are not part of a GALA release."

/-! ## Shared input -/

/-- Read a stream to EOF. -/
def readAll (stream : IO.FS.Stream) : IO ByteArray := do
  let mut acc := ByteArray.empty
  let mut chunk ← stream.read 65536
  while chunk.size > 0 do
    acc := acc.append chunk
    chunk ← stream.read 65536
  return acc

/-- Read stdin as UTF-8 text. -/
def readStdin : IO String := do
  let bytes ← readAll (← IO.getStdin)
  match String.fromUTF8? bytes with
  | some text => pure text
  | none => throw (IO.userError "input is not valid UTF-8")

/-- Read a file as text, or the error message to print. -/
def readFile (path : String) : IO (Except String String) := do
  match ← (IO.FS.readFile path).toBaseIO with
  | .error e => pure (.error s!"cannot read {path}: {e}")
  | .ok text => pure (.ok text)

def versionText : String :=
s!"gala-lean {version}
  components: galaspec (Core JSON), interpolation, diagnostics, manifest
  gala.mod version: {galaVersion}"

/-! ## `run`: the reference interpreter -/

/-- Parse `run [--fuel N] <file>` with `--fuel` before or after the file.
`-` means stdin. -/
def parseRunArgs (args : List String) : Option (String × Nat) :=
  let rec go : List String → Option String → Nat → Option (String × Nat)
    | [], some f, n => some (f, n)
    | [], none, _ => none
    | "--fuel" :: v :: r, f, _ => v.toNat?.bind fun n => go r f n
    | x :: r, none, n =>
      if x.startsWith "-" && x != "-" then none else go r (some x) n
    | _ :: _, some _, _ => none
  go args none Gala.Spec.Cli.defaultFuel

/-- Run a Core JSON text and report its output and outcome. -/
def runCore (text : String) (fuel : Nat) : IO UInt32 := do
  let (out, outcome, detail) := Gala.Spec.Cli.evaluate text fuel
  let stdout ← IO.getStdout
  stdout.putStr out
  stdout.flush
  if let some d := detail then
    IO.eprintln (d.replace "galaspec:" "gala-lean:")
  IO.eprintln s!"gala-lean: outcome={outcome}"
  pure (Gala.Spec.Cli.exitCode outcome)

def runCmd (args : List String) : IO UInt32 := do
  match parseRunArgs args with
  | none =>
    IO.eprintln "usage: gala-lean run [--fuel N] <file.json|->"
    pure 4
  | some (path, fuel) =>
    if path.endsWith ".gala" then
      IO.eprintln s!"gala-lean: cannot read GALA source yet: {path}"
      IO.eprintln "This draft runs the Lean semantics on Core JSON. Export the source with:"
      IO.eprintln s!"  bazel run //internal/formal/coreexport/galacore -- {path} > prog.json"
      pure 4
    else
      let text ←
        if path == "-" then
          readStdin
        else
          match ← readFile path with
          | .ok text => pure text
          | .error message =>
            IO.eprintln s!"gala-lean: {message}"
            IO.Process.exit 4
      runCore text fuel

/-! ## `mod`: gala.mod -/

def modUsage : String :=
"usage: gala-lean mod <init|show|format|--batch> [arguments]

  mod init [module-path]  create gala.mod (and an empty gala.sum)
  mod show [gala.mod]     print the parsed manifest, entry by entry
  mod format [gala.mod]   print the manifest in canonical form
  mod --batch             the port's differential protocol (stdin)"

/-- Detect a module path from `go.mod`, as `gala mod init` does. -/
def detectModulePath : IO (Option String) := do
  let goMod : System.FilePath := "go.mod"
  if ← goMod.pathExists then
    let text ← IO.FS.readFile goMod
    let line := text.splitOn "\n" |>.find? fun l =>
      (l.trimAscii.toString).startsWith "module "
    match line with
    | some l => pure (some ((l.trimAscii.toString).drop 7 |>.toString).trimAscii.toString)
    | none => pure none
  else
    pure none

def writeManifest (f : Gala.Manifest.File) : IO Unit := do
  IO.FS.writeFile "gala.mod" (Gala.Manifest.format f)
  let sum : System.FilePath := "gala.sum"
  if !(← sum.pathExists) then
    IO.FS.writeFile sum ""

def modInit (args : List String) : IO UInt32 := do
  let galaMod : System.FilePath := "gala.mod"
  if ← galaMod.pathExists then
    IO.eprintln "gala-lean: gala.mod already exists"
    pure 1
  else
    let modulePath ←
      match args with
      | [p] => pure p
      | [] =>
        match ← detectModulePath with
        | some p => pure p
        | none =>
          let cwd ← IO.currentDir
          let name := (cwd.fileName).getD "module"
          IO.eprintln s!"gala-lean: no go.mod found; using directory name {name}"
          pure name
      | _ =>
        IO.eprintln "usage: gala-lean mod init [module-path]"
        IO.Process.exit 1
    if modulePath.isEmpty then
      IO.eprintln "gala-lean: module path cannot be empty"
      pure 1
    else
      let f := { Gala.Manifest.newFile modulePath with gala := galaVersion }
      writeManifest f
      IO.println s!"Initialized gala.mod for module {modulePath}"
      pure 0

/-- Parse a manifest for `mod show` / `mod format`, reporting the parse error
through the diagnostics port's rendering. -/
def withManifest (path : String)
    (k : Gala.Manifest.File → IO UInt32) : IO UInt32 := do
  match ← readFile path with
  | .error message =>
    IO.eprintln s!"gala-lean: {message}"
    pure 1
  | .ok content =>
    match Gala.Manifest.parse content with
    | .error e =>
      let err := Gala.Diagnostics.Error.raw s!"gala-lean: {e.toString}"
      IO.eprintln (Gala.Diagnostics.renderRich err
        { fallbackPath := path, fallbackSource := content })
      pure 1
    | .ok f => k f

def showManifest (f : Gala.Manifest.File) : IO Unit := do
  IO.println s!"module {f.module.path}"
  if f.gala != "" then
    IO.println s!"gala {f.gala}"
  for r in f.requires do
    let mut line := s!"require {r.path} {r.version}"
    if r.indirect then line := line ++ " // indirect"
    if r.go then line := line ++ " // go"
    IO.println line
  for r in f.replaces do
    IO.println s!"replace {Gala.Manifest.formatModuleVersion r.old} => {Gala.Manifest.formatModuleVersion r.new}"
  for e in f.excludes do
    IO.println s!"exclude {e.path} {e.version}"

def modShow (args : List String) : IO UInt32 :=
  withManifest (args.headD "gala.mod") fun f => do
    showManifest f
    pure 0

def modFormat (args : List String) : IO UInt32 :=
  withManifest (args.headD "gala.mod") fun f => do
    IO.print (Gala.Manifest.format f)
    pure 0

def modCmd : List String → IO UInt32
  | [] => do
    IO.eprintln modUsage
    pure 1
  | "init" :: rest => modInit rest
  | "show" :: rest => modShow rest
  | "format" :: rest => modFormat rest
  | "fmt" :: rest => modFormat rest
  | rest => Gala.Manifest.Cli.main rest

/-! ## Dispatch -/

def main (args : List String) : IO UInt32 :=
  match args with
  | [] => do
    IO.println usage
    pure 0
  | "help" :: _ => do
    IO.println usage
    pure 0
  | "--help" :: _ => do
    IO.println usage
    pure 0
  | "-h" :: _ => do
    IO.println usage
    pure 0
  | "version" :: _ => do
    IO.println versionText
    pure 0
  | "--version" :: _ => do
    IO.println versionText
    pure 0
  | "run" :: rest => runCmd rest
  | "spec" :: rest => runCmd rest
  | "interp" :: rest => Gala.Interpolation.Cli.main rest
  | "interpolation" :: rest => Gala.Interpolation.Cli.main rest
  | "diag" :: rest => Gala.Diagnostics.Cli.main rest
  | "diagnostics" :: rest => Gala.Diagnostics.Cli.main rest
  | "mod" :: rest => modCmd rest
  | cmd :: rest =>
    if cmd.endsWith ".gala" then
      -- `gala-lean prog.gala` fails with the Core JSON export instruction,
      -- as `gala prog.gala` transpiles the source.
      runCmd (cmd :: rest)
    else do
      IO.eprintln s!"gala-lean: unknown command '{cmd}'"
      IO.eprintln "Run 'gala-lean help' for usage."
      pure 1

end GalaLean

def main (args : List String) : IO UInt32 :=
  GalaLean.main args
