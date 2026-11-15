import Diagnostics

open Gala.Diagnostics

/-- Curated tests for the port; these run in the native binary, so they also
exercise the generated C. Expectations were taken from the Go renderer. -/

def d (code msg hint file : String) (line col endCol : Nat) : Error :=
  .diag { code := code, message := msg, hint := hint, filePath := file,
          line := line, column := col, endColumn := endCol }

def check (name : String) (err : Error) (path source hint : String)
    (color : Bool) (expectedRich expectedJSON : String) : IO Unit := do
  let opts : Options :=
    { fallbackPath := path, fallbackSource := source, color := color }
  let gotRich := renderRich err opts
  let gotJSON := renderJSON (some err) hint
  if gotRich == expectedRich && gotJSON == expectedJSON then
    pure ()
  else do
    IO.eprintln s!"diagnostics test failed: {name}"
    if gotRich != expectedRich then
      IO.eprintln s!"  rich: {repr gotRich}\n  want: {repr expectedRich}"
    if gotJSON != expectedJSON then
      IO.eprintln s!"  json: {repr gotJSON}\n  want: {repr expectedJSON}"
    IO.Process.exit 1

def main : IO UInt32 := do
  check "span-plain" (d "GALA-E0035" "bare `len` is forbidden" "use .Size() instead" "" 4 10 13) "no-such-file-7a3f.gala" "package main\n\nfunc main() {\n  val n = len(s)\n}\n" "" false
    "error[GALA-E0035]: bare `len` is forbidden\n  --> no-such-file-7a3f.gala:4:11\n  |\n4 |   val n = len(s)\n  |           ^^^ use .Size() instead\n  |\n  = hint: use .Size() instead"
    "{\n  \"diagnostics\": [\n    {\n      \"severity\": \"error\",\n      \"code\": \"GALA-E0035\",\n      \"message\": \"bare `len` is forbidden\",\n      \"hint\": \"use .Size() instead\",\n      \"line\": 4,\n      \"column\": 11,\n      \"endColumn\": 14,\n      \"docsUrl\": \"https://gala.fyi/docs/errors/gala-e0035/\"\n    }\n  ]\n}"
  check "span-color" (d "GALA-E0035" "bare `len` is forbidden" "use .Size() instead" "" 4 10 13) "no-such-file-7a3f.gala" "package main\n\nfunc main() {\n  val n = len(s)\n}\n" "" true
    "\u001B[1;31merror[GALA-E0035]\u001B[0m: bare `len` is forbidden\n  \u001B[2m-->\u001B[0m no-such-file-7a3f.gala:4:11\n  \u001B[2m|\u001B[0m\n4 \u001B[2m|\u001B[0m   val n = len(s)\n  \u001B[2m|\u001B[0m           \u001B[1;31m^^^\u001B[0m use .Size() instead\n  \u001B[2m|\u001B[0m\n  = \u001B[36mhint:\u001B[0m use .Size() instead"
    "{\n  \"diagnostics\": [\n    {\n      \"severity\": \"error\",\n      \"code\": \"GALA-E0035\",\n      \"message\": \"bare `len` is forbidden\",\n      \"hint\": \"use .Size() instead\",\n      \"line\": 4,\n      \"column\": 11,\n      \"endColumn\": 14,\n      \"docsUrl\": \"https://gala.fyi/docs/errors/gala-e0035/\"\n    }\n  ]\n}"
  check "file-named" (d "GALA-E0035" "bare `len` is forbidden" "use .Size() instead" "no-such-file-7a3f.gala" 1 0 0) "no-such-file-7a3f.gala" "package main\n\nfunc main() {\n  val n = len(s)\n}\n" "" false
    "error[GALA-E0035]: bare `len` is forbidden\n  --> no-such-file-7a3f.gala:1:1\n  |\n1 | package main\n  | ^^^^^^^ use .Size() instead\n  |\n  = hint: use .Size() instead"
    "{\n  \"diagnostics\": [\n    {\n      \"severity\": \"error\",\n      \"code\": \"GALA-E0035\",\n      \"message\": \"bare `len` is forbidden\",\n      \"hint\": \"use .Size() instead\",\n      \"file\": \"no-such-file-7a3f.gala\",\n      \"line\": 1,\n      \"column\": 1,\n      \"docsUrl\": \"https://gala.fyi/docs/errors/gala-e0035/\"\n    }\n  ]\n}"
  check "no-position" (d "" "unexpected token" "" "" 0 0 0) "no-such-file-7a3f.gala" "package main\n\nfunc main() {\n  val n = len(s)\n}\n" "" false
    "error: unexpected token"
    "{\n  \"diagnostics\": [\n    {\n      \"severity\": \"error\",\n      \"message\": \"unexpected token\"\n    }\n  ]\n}"
  check "no-source" (d "GALA-E0039" "no source" "hint only" "" 4 1 0) "no-such-file-7a3f.gala" "" "" false
    "error[GALA-E0039]: no source\n= hint: hint only"
    "{\n  \"diagnostics\": [\n    {\n      \"severity\": \"error\",\n      \"code\": \"GALA-E0039\",\n      \"message\": \"no source\",\n      \"hint\": \"hint only\",\n      \"line\": 4,\n      \"column\": 2,\n      \"docsUrl\": \"https://gala.fyi/docs/errors/gala-e0039/\"\n    }\n  ]\n}"
  check "no-pos-hint" (d "GALA-E0040" "no pos" "a hint" "" 0 0 0) "no-such-file-7a3f.gala" "package main\n\nfunc main() {\n  val n = len(s)\n}\n" "" false
    "error[GALA-E0040]: no pos\n= hint: a hint"
    "{\n  \"diagnostics\": [\n    {\n      \"severity\": \"error\",\n      \"code\": \"GALA-E0040\",\n      \"message\": \"no pos\",\n      \"hint\": \"a hint\",\n      \"docsUrl\": \"https://gala.fyi/docs/errors/gala-e0040/\"\n    }\n  ]\n}"
  check "tab" (d "GALA-E0099" "tab" "x\ty" "" 2 4 0) "no-such-file-7a3f.gala" "\tA\tB\n\tC\n" "" false
    "error[GALA-E0099]: tab\n  --> no-such-file-7a3f.gala:2:5\n  |\n2 | \tC\n  | \t   ^ x\ty\n  |\n  = hint: x\ty"
    "{\n  \"diagnostics\": [\n    {\n      \"severity\": \"error\",\n      \"code\": \"GALA-E0099\",\n      \"message\": \"tab\",\n      \"hint\": \"x\\ty\",\n      \"line\": 2,\n      \"column\": 5,\n      \"docsUrl\": \"https://gala.fyi/docs/errors/gala-e0099/\"\n    }\n  ]\n}"
  check "bom" (d "GALA-E0101" "bom" "" "" 1 1 0) "no-such-file-7a3f.gala" "﻿package main\n" "" false
    "error[GALA-E0101]: bom\n  --> no-such-file-7a3f.gala:1:2\n  |\n1 | package main\n  |  ^^^^^^\n  |"
    "{\n  \"diagnostics\": [\n    {\n      \"severity\": \"error\",\n      \"code\": \"GALA-E0101\",\n      \"message\": \"bom\",\n      \"line\": 1,\n      \"column\": 2,\n      \"docsUrl\": \"https://gala.fyi/docs/errors/gala-e0101/\"\n    }\n  ]\n}"
  check "unicode" (d "GALA-E0042" "échec: <&>" "utilisez «x»; not (this)." "" 1 9 12) "no-such-file-7a3f.gala" "héllo ${π}\n" "cli" false
    "error[GALA-E0042]: échec: <&>\n  --> no-such-file-7a3f.gala:1:10\n  |\n1 | héllo ${π}\n  |          ^ utilisez «x»\n  |\n  = hint: utilisez «x»; not (this)."
    "{\n  \"diagnostics\": [\n    {\n      \"severity\": \"error\",\n      \"code\": \"GALA-E0042\",\n      \"message\": \"échec: <&>\",\n      \"hint\": \"utilisez «x»; not (this).\",\n      \"line\": 1,\n      \"column\": 10,\n      \"endColumn\": 13,\n      \"docsUrl\": \"https://gala.fyi/docs/errors/gala-e0042/\"\n    }\n  ]\n}"
  check "json-escape" (d "GALA-E0043" "a<b>&c     \u0001 \u007F \u0008\u000C" "" "" 0 0 0) "no-such-file-7a3f.gala" "" "" false
    "error[GALA-E0043]: a<b>&c     \u0001 \u007F \u0008\u000C"
    "{\n  \"diagnostics\": [\n    {\n      \"severity\": \"error\",\n      \"code\": \"GALA-E0043\",\n      \"message\": \"a<b>&c \\u2028 \\u2029 \\u0001 \u007F \\b\\f\",\n      \"docsUrl\": \"https://gala.fyi/docs/errors/gala-e0043/\"\n    }\n  ]\n}"
  check "long-hint" (d "GALA-E0102" "trunc" "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" "" 2 0 0) "no-such-file-7a3f.gala" "package main\n\nfunc main() {\n  val n = len(s)\n}\n" "" false
    "error[GALA-E0102]: trunc\n  --> no-such-file-7a3f.gala:2:1\n  |\n2 | \n  | ^ aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa…\n  |\n  = hint: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    "{\n  \"diagnostics\": [\n    {\n      \"severity\": \"error\",\n      \"code\": \"GALA-E0102\",\n      \"message\": \"trunc\",\n      \"hint\": \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\n      \"line\": 2,\n      \"column\": 1,\n      \"docsUrl\": \"https://gala.fyi/docs/errors/gala-e0102/\"\n    }\n  ]\n}"
  check "span-quote" (d "GALA-E0037" "quote" "" "" 2 9 0) "no-such-file-7a3f.gala" "func main() {\n\tval x = \"str\" + `raw`\n}\n" "" false
    "error[GALA-E0037]: quote\n  --> no-such-file-7a3f.gala:2:10\n  |\n2 | \tval x = \"str\" + `raw`\n  | \t        ^^^^^\n  |"
    "{\n  \"diagnostics\": [\n    {\n      \"severity\": \"error\",\n      \"code\": \"GALA-E0037\",\n      \"message\": \"quote\",\n      \"line\": 2,\n      \"column\": 10,\n      \"docsUrl\": \"https://gala.fyi/docs/errors/gala-e0037/\"\n    }\n  ]\n}"
  check "multi" (.multi [d "GALA-E0001" "one" "" "" 1 0 0, .raw "go build: <bad> & \"stuff\"\nline2"]) "no-such-file-7a3f.gala" "package main\n\nfunc main() {\n  val n = len(s)\n}\n" "cli" false
    "error[GALA-E0001]: one\n  --> no-such-file-7a3f.gala:1:1\n  |\n1 | package main\n  | ^^^^^^^\n  |\n\ngo build: <bad> & \"stuff\"\nline2"
    "{\n  \"diagnostics\": [\n    {\n      \"severity\": \"error\",\n      \"code\": \"GALA-E0001\",\n      \"message\": \"one\",\n      \"hint\": \"cli\",\n      \"line\": 1,\n      \"column\": 1,\n      \"docsUrl\": \"https://gala.fyi/docs/errors/gala-e0001/\"\n    },\n    {\n      \"severity\": \"error\",\n      \"message\": \"go build: <bad> & \\\"stuff\\\"\\nline2\"\n    }\n  ]\n}"
  check "empty" (.multi []) "no-such-file-7a3f.gala" "package main\n\nfunc main() {\n  val n = len(s)\n}\n" "cli" false
    ""
    "{\n  \"diagnostics\": []\n}"
  check "raw" (.raw "go build: <bad> & \"stuff\"\nline2") "no-such-file-7a3f.gala" "package main\n\nfunc main() {\n  val n = len(s)\n}\n" "cli" false
    "go build: <bad> & \"stuff\"\nline2"
    "{\n  \"diagnostics\": [\n    {\n      \"severity\": \"error\",\n      \"message\": \"go build: <bad> & \\\"stuff\\\"\\nline2\",\n      \"hint\": \"cli\"\n    }\n  ]\n}"
  check "hint-not-overridden" (d "GALA-E0004" "has hint" "already" "" 2 1 0) "no-such-file-7a3f.gala" "package main\n\nfunc main() {\n  val n = len(s)\n}\n" "cli" false
    "error[GALA-E0004]: has hint\n  --> no-such-file-7a3f.gala:2:2\n  |\n2 | \n  |  ^ already\n  |\n  = hint: already"
    "{\n  \"diagnostics\": [\n    {\n      \"severity\": \"error\",\n      \"code\": \"GALA-E0004\",\n      \"message\": \"has hint\",\n      \"hint\": \"already\",\n      \"line\": 2,\n      \"column\": 2,\n      \"docsUrl\": \"https://gala.fyi/docs/errors/gala-e0004/\"\n    }\n  ]\n}"
  check "hint-second-empty" (.multi [d "GALA-E0005" "first has" "present" "" 1 0 0, d "GALA-E0006" "second empty" "" "" 2 0 0]) "no-such-file-7a3f.gala" "package main\n\nfunc main() {\n  val n = len(s)\n}\n" "cli" false
    "error[GALA-E0005]: first has\n  --> no-such-file-7a3f.gala:1:1\n  |\n1 | package main\n  | ^^^^^^^ present\n  |\n  = hint: present\n\nerror[GALA-E0006]: second empty\n  --> no-such-file-7a3f.gala:2:1\n  |\n2 | \n  | ^\n  |"
    "{\n  \"diagnostics\": [\n    {\n      \"severity\": \"error\",\n      \"code\": \"GALA-E0005\",\n      \"message\": \"first has\",\n      \"hint\": \"present\",\n      \"line\": 1,\n      \"column\": 1,\n      \"docsUrl\": \"https://gala.fyi/docs/errors/gala-e0005/\"\n    },\n    {\n      \"severity\": \"error\",\n      \"code\": \"GALA-E0006\",\n      \"message\": \"second empty\",\n      \"hint\": \"cli\",\n      \"line\": 2,\n      \"column\": 1,\n      \"docsUrl\": \"https://gala.fyi/docs/errors/gala-e0006/\"\n    }\n  ]\n}"

  IO.println "diagnostics tests: ok"
  return 0
