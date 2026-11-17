; Highlights for GALA. Capture names follow nvim-treesitter's. Where two
; patterns match one node, the more specific one comes later: in Neovim and
; Zed the last match wins.

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

; In a function type, `func(int, string) bool`, an untyped parameter is a type.
(function_type
  parameters: (parameter_list
    (parameter
      name: (identifier) @type
      !type)))

((function_type
  parameters: (parameter_list
    (parameter
      name: (identifier) @type.builtin
      !type)))
  (#any-of? @type.builtin
    "any" "bool" "byte" "complex64" "complex128" "error" "float32" "float64"
    "int" "int8" "int16" "int32" "int64" "rune" "string"
    "uint" "uint8" "uint16" "uint32" "uint64" "uintptr"))

; Calls

(call_expression
  function: (identifier) @function.call)

(call_expression
  function: (index_expression
    operand: (identifier) @function.call))

(call_expression
  function: (selector_expression
    field: (field_identifier) @function.method.call))

(call_expression
  function: (index_expression
    operand: (selector_expression
      field: (field_identifier) @function.method.call)))

; Patterns: the extractor in `case Some(x)` is a constructor.

(case_clause
  pattern: (call_expression
    function: (identifier) @constructor))

(case_clause
  pattern: (call_expression
    function: (index_expression
      operand: (identifier) @constructor)))

(case_clause
  pattern: (call_expression
    function: (selector_expression
      field: (field_identifier) @constructor)))

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
