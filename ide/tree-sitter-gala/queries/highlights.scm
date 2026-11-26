; Highlights for GALA. Capture names follow nvim-treesitter's. Where two
; patterns match one node, the more specific one comes later, for editors in
; which the last match wins, as in Neovim. Where the first wins, as in Helix,
; move the generic `(identifier)`, `(field_identifier)` and `(type_identifier)`
; patterns to the end.

; Identifiers

(identifier) @variable

(field_identifier) @variable.member

(package_identifier) @module

; `_` is a wildcard pattern, a placeholder in `_ * 2`, or a blank name.
((identifier) @variable.builtin
  (#eq? @variable.builtin "_"))

; Keywords

[
  "package"
  "import"
] @keyword.import

"func" @keyword.function

[
  "type"
  "struct"
  "interface"
  "sealed"
  "opaque"
  "map"
] @keyword.type

[
  "val"
  "var"
  "bind"
  "also"
  "use"
  "embed"
] @keyword

[
  "inout"
  "default"
] @keyword.modifier

[
  "if"
  "else"
  "match"
  "case"
] @keyword.conditional

[
  "for"
  "range"
] @keyword.repeat

"return" @keyword.return

; Operators

[
  "+"
  "-"
  "*"
  "/"
  "%"
  "&"
  "|"
  "^"
  "<<"
  ">>"
  "&^"
  "&&"
  "||"
  "!"
  "<-"
  "=="
  "!="
  "<"
  "<="
  ">"
  ">="
  "="
  ":="
  "+="
  "-="
  "*="
  "/="
  "++"
  "--"
  "=>"
  "..."
] @operator

(import_spec
  name: (dot) @operator)

; Punctuation

[
  "("
  ")"
  "["
  "]"
  "{"
  "}"
] @punctuation.bracket

[
  ","
  "."
  ";"
  ":"
] @punctuation.delimiter

; Types

(type_identifier) @type

((type_identifier) @type.builtin
  (#any-of? @type.builtin
    "any" "bool" "byte" "complex64" "complex128" "error" "float32" "float64"
    "int" "int8" "int16" "int32" "int64" "rune" "string"
    "uint" "uint8" "uint16" "uint32" "uint64" "uintptr"))

; Declarations

(function_declaration
  name: (identifier) @function)

(function_declaration
  receiver: (receiver)
  name: (identifier) @function.method)

(method_spec
  name: (field_identifier) @function.method)

(sealed_case
  name: (type_identifier) @constructor)

(parameter
  name: (identifier) @variable.parameter)

(receiver
  name: (identifier) @variable.parameter)

(named_argument
  name: (identifier) @variable.parameter)

; `struct Point(x int, y int)` declares fields, not parameters.
(struct_declaration
  parameters: (parameter_list
    (parameter
      name: (identifier) @variable.member)))

; Calls, with or without explicit type arguments: `f(x)`, `f[T](x)`.

(call_expression
  function: [
    (identifier) @function.call
    (index_expression
      operand: (identifier) @function.call)
    (selector_expression
      field: (field_identifier) @function.method.call)
    (index_expression
      operand: (selector_expression
        field: (field_identifier) @function.method.call))
  ])

; Patterns: the extractor in `case Some(x)` is a constructor. A pattern is an
; expression, so only the outermost extractor is caught: in
; `case Some(Ok(x))`, `Ok` stays a call.

(case_clause
  pattern: (call_expression
    function: [
      (identifier) @constructor
      (index_expression
        operand: (identifier) @constructor)
      (selector_expression
        field: (field_identifier) @constructor)
    ]))

; A variant without fields, `case None =>`, is a bare name; by convention a
; variant is capitalized and a binding is not.
((case_clause
  pattern: (identifier) @constructor)
  (#match? @constructor "^[A-Z]"))

; Literals

[
  (interpreted_string_literal)
  (raw_string_literal)
  (interpolated_string_literal)
] @string

(rune_literal) @character

(escape_sequence) @string.escape

(int_literal) @number

(float_literal) @number.float

[
  (true)
  (false)
] @boolean

(nil) @constant.builtin

; Interpolation: `s"..."`, `f"..."`, `$name`, `${expression}`

(interpolated_string_literal
  [
    "s\""
    "f\""
  ] @string.special)

(interpolation
  [
    "$"
    "${"
    "}"
  ] @punctuation.special)

; Comments

(comment) @comment @spell
