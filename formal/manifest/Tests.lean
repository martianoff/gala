import Manifest

open Gala.Manifest

-- The test file is one long `do` block; the nixpkgs Lean has a lower
-- elaboration recursion limit than the pinned toolchain.
set_option maxRecDepth 10000

/-- Curated tests for the port; these run in the native binary, so they also
exercise the generated C. Expectations were taken from the Go package. -/

def check (name : String) (ok : Bool) : IO Unit := do
  if ok then pure ()
  else do
    IO.eprintln s!"manifest test failed: {name}"
    IO.Process.exit 1

def checkEq {α : Type} [BEq α] [Repr α] (name : String) (got want : α) : IO Unit := do
  if got == want then pure ()
  else do
    IO.eprintln s!"manifest test failed: {name}\n  got:  {repr got}\n  want: {repr want}"
    IO.Process.exit 1

def parseOk (name content : String) : IO File :=
  match parse content with
  | .ok f => pure f
  | .error e => do
    IO.eprintln s!"manifest test failed: {name}\n  unexpected parse error: {e.toString}"
    IO.Process.exit 1

def parseErr (name content : String) (line : Nat) (message : String) : IO Unit :=
  match parse content with
  | .ok _ => do
    IO.eprintln s!"manifest test failed: {name}\n  expected parse error, got a file"
    IO.Process.exit 1
  | .error e => do
    checkEq s!"{name}.line" e.line line
    checkEq s!"{name}.message" e.message message

def bomText : String := String.singleton bomChar
def nbsp : String := String.singleton (Char.ofNat 0xA0)
def ideographicSpace : String := String.singleton (Char.ofNat 0x3000)
def verticalTab : String := String.singleton (Char.ofNat 0x0B)

def req (path version : String) (indirect isGo : Bool) : Require :=
  { path, version, indirect, go := isGo }

def main : IO UInt32 := do
  -- Parsing: module and gala directives.
  let f ← parseOk "simple" "module github.com/user/project\n\ngala 1.0\n"
  checkEq "simple.module" f.module.path "github.com/user/project"
  checkEq "simple.gala" f.gala "1.0"
  checkEq "simple.requires" f.requires []
  checkEq "simple.replaces" f.replaces []
  checkEq "simple.excludes" f.excludes []

  -- The last directive wins; a missing module path or version is an error.
  let f ← parseOk "last-wins" "module a\nmodule b\ngala 1\ngala 2\n"
  checkEq "last-wins.module" f.module.path "b"
  checkEq "last-wins.gala" f.gala "2"
  let f ← parseOk "extra-fields" "module m extra\ngala 1.0 more\n"
  checkEq "extra-fields.module" f.module.path "m"
  checkEq "extra-fields.gala" f.gala "1.0"
  parseErr "missing-module" "module\n" 1 "module directive requires a path"
  parseErr "missing-gala" "gala\n" 1 "gala directive requires a version"
  parseErr "unknown" "module m\nunknown thing\n" 2 "unknown directive: unknown"
  parseErr "unknown-block" "module m\nweird (\n" 2 "unknown block directive: weird"
  parseErr "empty-block" "module m\n(\n" 2 "unknown block directive: "

  -- Require: block form, single-line form, markers and marker persistence.
  let f ← parseOk "requires"
    "module m\n\nrequire (\n\ta v1\n\tb v2 // indirect\n\tc v3 // go\n)\n"
  checkEq "requires" f.requires [req "a" "v1" false false, req "b" "v2" true false,
    req "c" "v3" true true]
  let f ← parseOk "single-require" "module m\n\nrequire a v\n"
  checkEq "single-require" f.requires [req "a" "v" false false]
  let f ← parseOk "both-markers" "module m\n\nrequire a v // go, indirect\n"
  checkEq "both-markers" f.requires [req "a" "v" true true]
  let f ← parseOk "marker-case" "module m\n\nrequire a v // INDIRECT\n"
  checkEq "marker-case" f.requires [req "a" "v" false false]
  let f ← parseOk "marker-extra" "module m\n\nrequire a v // indirect, other\n"
  checkEq "marker-extra" f.requires [req "a" "v" true false]
  -- A marker on its own line inside a require block sticks to the following
  -- entries of that block.
  let f ← parseOk "sticky-marker"
    "module m\n\nrequire (\n\ta v\n\n\t// go\n\tb w\n)\n"
  checkEq "sticky-marker" f.requires [req "a" "v" false false, req "b" "w" false true]
  -- A marker on the block header is dropped with the header line.
  let f ← parseOk "header-marker" "module m\n\nrequire ( // indirect\n\ta v\n)\n"
  checkEq "header-marker" f.requires [req "a" "v" false false]
  -- Inside a block the whole line is an entry, even if it looks like a
  -- different directive.
  let f ← parseOk "block-entry" "module m\n\nrequire (\nreplace a => b\n)\n"
  checkEq "block-entry" f.requires [req "replace" "a" false false]
  let f ← parseOk "nested-block" "module m\n\nrequire (\nrequire (\n)\n"
  checkEq "nested-block" f.requires []
  let f ← parseOk "empty-block-ok" "module m\n\nrequire (\n)\n"
  checkEq "empty-block-ok" f.requires []
  parseErr "require-short" "module m\n\nrequire\n" 3 "require needs path and version"

  -- Replace: optional versions on either side, block form.
  let f ← parseOk "replace-local" "module m\n\nreplace a => ../local\n"
  checkEq "replace-local" f.replaces [{ old := { path := "a" }, new := { path := "../local" } }]
  let f ← parseOk "replace-versioned" "module m\n\nreplace a v1 => b v2\n"
  checkEq "replace-versioned" f.replaces
    [{ old := { path := "a", version := "v1" }, new := { path := "b", version := "v2" } }]
  let f ← parseOk "replace-mixed" "module m\n\nreplace a => b v2\n"
  checkEq "replace-mixed" f.replaces
    [{ old := { path := "a" }, new := { path := "b", version := "v2" } }]
  let f ← parseOk "replace-block" "module m\n\nreplace (\n\ta => ../a\n\tb v1 => c v2\n)\n"
  checkEq "replace-block" f.replaces
    [{ old := { path := "a" }, new := { path := "../a" } },
     { old := { path := "b", version := "v1" }, new := { path := "c", version := "v2" } }]
  parseErr "replace-no-arrow" "module m\n\nreplace a\n" 3 "replace requires '=>' separator"
  parseErr "replace-no-old" "module m\n\nreplace => b\n" 3
    "replace requires old module path before '=>'"
  parseErr "replace-no-new" "module m\n\nreplace a =>\n" 3
    "replace requires new path after '=>'"

  -- Exclude.
  let f ← parseOk "exclude" "module m\n\nexclude a v1\nexclude b v2\n"
  checkEq "exclude" f.excludes [{ path := "a", version := "v1" }, { path := "b", version := "v2" }]
  parseErr "exclude-short" "module m\n\nexclude a\n" 3 "exclude needs path and version"

  -- Comments, BOM, CRLF and the tokenizer.
  let f ← parseOk "comment" "module m // trailing\nrequire a v // indirect\n"
  checkEq "comment.module" f.module.path "m"
  checkEq "comment.requires" f.requires [req "a" "v" true false]
  let f ← parseOk "comment-in-path" "module m//x\nrequire a v // indirect\n"
  checkEq "comment-in-path.module" f.module.path "m"
  checkEq "comment-in-path.requires" f.requires [req "a" "v" true false]
  parseErr "comment-eats-version" "module m\n\nrequire a//b v\n" 3
    "require needs path and version"
  let f ← parseOk "quoted" "module m\n\nrequire \"a b\" v\n"
  checkEq "quoted" f.requires [req "\"a" "b\"" false false]
  let f ← parseOk "bom" (bomText ++ "module m\n")
  checkEq "bom.module" f.module.path "m"
  parseErr "double-bom" (bomText ++ bomText ++ "module m\n") 1
    ("unknown directive: " ++ bomText ++ "module")
  let f ← parseOk "crlf" "module m\r\n\r\ngala 1.0\r\n"
  checkEq "crlf.module" f.module.path "m"
  checkEq "crlf.gala" f.gala "1.0"
  let f ← parseOk "nbsp" ("module m\n\nrequire " ++ nbsp ++ "a" ++ nbsp ++ "v" ++ nbsp ++ "\n")
  checkEq "nbsp" f.requires [req "a" "v" false false]
  let f ← parseOk "ideographic" ("module m\n\nrequire " ++ ideographicSpace ++ "a"
    ++ ideographicSpace ++ "v\n")
  checkEq "ideographic" f.requires [req "a" "v" false false]
  let f ← parseOk "vertical-tab" (verticalTab ++ "require\t a" ++ verticalTab ++ " v\n")
  checkEq "vertical-tab.module" f.module.path ""
  checkEq "vertical-tab.requires" f.requires [req "a" "v" false false]
  let f ← parseOk "empty" ""
  checkEq "empty.module" f.module.path ""
  checkEq "empty.requires" f.requires []
  let f ← parseOk "close-outside" ")\nmodule m\n)\n"
  checkEq "close-outside.module" f.module.path "m"

  -- Formatting, byte for byte.
  let f := newFile "m"
  checkEq "format-new-file" (format f) "module m\n"
  checkEq "format-empty-file" (format ({} : File)) "module \n"
  let f := { newFile "m" with gala := "1.0" }
  checkEq "format-gala" (format f) "module m\n\ngala 1.0\n"
  let f := addRequire f "a" "v1" false
  checkEq "format-one-require" (format f) "module m\n\ngala 1.0\n\nrequire a v1\n"
  let f := addRequire f "b" "v2" true
  checkEq "format-two-requires" (format f)
    "module m\n\ngala 1.0\n\nrequire (\n\ta v1\n\tb v2 // indirect\n)\n"
  let f := addRequire (newFile "m") "a" "v" true
  checkEq "format-indirect" (format f) "module m\n\nrequire a v // indirect\n"
  let f := addRequire (newFile "m") "a" "v" true
  let f := addRequire f "b" "w" false
  checkEq "format-marker-order" (format f)
    "module m\n\nrequire (\n\ta v // indirect\n\tb w\n)\n"
  let f := newFile "m"
  let f := addReplace f "a" "" "../local" ""
  checkEq "format-one-replace" (format f) "module m\n\nreplace a => ../local\n"
  let f := addReplace f "b" "v1" "c" "v2"
  checkEq "format-two-replaces" (format f)
    "module m\n\nreplace (\n\ta => ../local\n\tb v1 => c v2\n)\n"
  let f := addExclude (newFile "m") "a" "v0.9.0"
  checkEq "format-one-exclude" (format f) "module m\n\nexclude a v0.9.0\n"
  let f := addExclude f "b" "v2"
  checkEq "format-two-excludes" (format f)
    "module m\n\nexclude (\n\ta v0.9.0\n\tb v2\n)\n"
  let f := newFile "m"
  let f := { f with gala := "1.0" }
  let f := addRequire f "a" "v1" false
  let f := addReplace f "b" "" "../b" ""
  let f := addExclude f "c" "v9"
  checkEq "format-sections" (format f)
    "module m\n\ngala 1.0\n\nrequire a v1\n\nreplace b => ../b\n\nexclude c v9\n"
  -- The formatter trims every trailing newline and writes exactly one, so a
  -- path or version ending in a newline loses that newline.
  let f : File := { newFile "m" with
    requires := [{ path := "a", version := "v\n", indirect := false }] }
  checkEq "format-trailing-newline" (format f) "module m\n\nrequire a v\n"
  checkEq "format-multiline-value" (format
      { newFile "m" with
        gala := "1\n2" }) "module m\n\ngala 1\n2\n"

  -- Parse -> format -> parse is stable on a canonical file.
  let canonical := "module m\n\ngala 1.0\n\nrequire (\n\ta v1\n\tb v2 // indirect\n)\n\nreplace a => b\n\nexclude c v9\n"
  let f ← parseOk "roundtrip-in" canonical
  checkEq "roundtrip-format" (format f) canonical
  let f' ← parseOk "roundtrip-out" (format f)
  checkEq "roundtrip" f'.requires f.requires
  checkEq "roundtrip-replaces" f'.replaces f.replaces
  checkEq "roundtrip-excludes" f'.excludes f.excludes

  -- ModuleVersion.IsLocal.
  check "is-local-dot" ({ path := ".", version := "" } : ModuleVersion).isLocal
  check "is-local-dot-slash" ({ path := "./rel", version := "" } : ModuleVersion).isLocal
  check "is-local-dot-dot" ({ path := "../rel", version := "" } : ModuleVersion).isLocal
  check "is-local-slash" ({ path := "/abs", version := "" } : ModuleVersion).isLocal
  check "is-local-backslash" ({ path := "\\win", version := "" } : ModuleVersion).isLocal
  check "is-not-local-path" (!({ path := "a/b", version := "" } : ModuleVersion).isLocal)
  check "is-not-local-version" (!({ path := ".", version := "v1" } : ModuleVersion).isLocal)
  check "is-not-local-empty" (!({ path := "", version := "" } : ModuleVersion).isLocal)
  check "is-not-local-unicode" (!({ path := "é", version := "" } : ModuleVersion).isLocal)

  -- AddRequire: append, update in place, first duplicate only, Go flag kept.
  let f := addRequire (newFile "m") "a" "v1" false
  checkEq "add-require-append" f.requires [req "a" "v1" false false]
  let f := addRequire f "b" "v2" true
  checkEq "add-require-second" f.requires [req "a" "v1" false false, req "b" "v2" true false]
  let f0 ← parseOk "add-require-input" "module m\n\nrequire a v1\nrequire b v2\n"
  let f := addRequire f0 "b" "v9" true
  checkEq "add-require-update" f.requires
    [req "a" "v1" false false, req "b" "v9" true false]
  let f0 ← parseOk "add-require-go" "module m\n\nrequire a v // go\n"
  let f := addRequire f0 "a" "w" true
  checkEq "add-require-keeps-go" f.requires [req "a" "w" true true]
  let f0 ← parseOk "add-require-dups" "module m\n\nrequire a v1\nrequire a v2\n"
  let f := addRequire f0 "a" "v9" false
  checkEq "add-require-first-dup" f.requires
    [req "a" "v9" false false, req "a" "v2" false false]

  -- RemoveRequire: first match, then not found.
  let f0 ← parseOk "remove-input" "module m\n\nrequire a v1\nrequire b v2\nrequire a v3\n"
  let (f, found) := removeRequire f0 "a"
  check "remove-found" found
  checkEq "remove-first" f.requires [req "b" "v2" false false, req "a" "v3" false false]
  let (f, found) := removeRequire f "a"
  check "remove-second" found
  checkEq "remove-last" f.requires [req "b" "v2" false false]
  let (_, found) := removeRequire f "a"
  check "remove-missing" (!found)

  -- GetRequire.
  let f0 ← parseOk "get-input" "module m\n\nrequire a v1 // indirect\n"
  checkEq "get-found" ((getRequire f0 "a").map (fun r => (r.version, r.indirect, r.go)))
    (some ("v1", true, false))
  checkEq "get-missing" (getRequire f0 "z") none

  -- AddReplace: update only the new side, append for a new old pair.
  let f0 ← parseOk "add-replace-input" "module m\n\nreplace a v1 => b v2\n"
  let f := addReplace f0 "a" "v1" "c" "v3"
  checkEq "add-replace-update" f.replaces
    [{ old := { path := "a", version := "v1" }, new := { path := "c", version := "v3" } }]
  let f := addReplace f "a" "v2" "d" "v4"
  checkEq "add-replace-append" f.replaces
    [{ old := { path := "a", version := "v1" }, new := { path := "c", version := "v3" } },
     { old := { path := "a", version := "v2" }, new := { path := "d", version := "v4" } }]

  -- AddExclude deduplicates.
  let f := addExclude (newFile "m") "a" "v1"
  let f := addExclude f "a" "v1"
  let f := addExclude f "a" "v2"
  checkEq "add-exclude" f.excludes [{ path := "a", version := "v1" }, { path := "a", version := "v2" }]

  -- The filtered views.
  let f0 ← parseOk "filters" "module m\n\nrequire a v\nrequire b v // indirect\nrequire c v // go\nrequire d v // go, indirect\n"
  checkEq "direct" ((directRequires f0).map (fun r => r.path)) ["a", "c"]
  checkEq "indirect" ((indirectRequires f0).map (fun r => r.path)) ["b", "d"]
  checkEq "gala" ((galaRequires f0).map (fun r => r.path)) ["a", "b"]
  checkEq "go" ((goRequires f0).map (fun r => r.path)) ["c", "d"]

  IO.println "manifest tests: ok"
  return 0
