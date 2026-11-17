; Indentation for Neovim (nvim-treesitter's `indents` query).

[
  (block)
  (literal_value)
  (argument_list)
  (parameter_list)
  (result_list)
  (tuple_expression)
  (partial_function_literal)
  (match_expression)
  (case_clause)
  (sealed_type_declaration)
  (struct_type)
  (interface_type)
  (import_declaration)
  (type_parameter_list)
  (type_arguments)
] @indent.begin

[
  "}"
  ")"
  "]"
] @indent.branch

[
  "}"
  ")"
  "]"
] @indent.end

[
  (comment)
  (raw_string_literal)
] @indent.auto
