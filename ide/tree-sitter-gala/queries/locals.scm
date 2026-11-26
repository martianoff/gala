; Scopes

[
  (source_file)
  (function_declaration)
  (func_literal)
  (lambda_expression)
  (block)
  (case_clause)
  (for_statement)
  (if_statement)
] @local.scope

; Definitions

(function_declaration
  name: (identifier) @local.definition.function)

(type_declaration
  name: (type_identifier) @local.definition.type)

(struct_declaration
  name: (type_identifier) @local.definition.type)

(sealed_type_declaration
  name: (type_identifier) @local.definition.type)

(opaque_type_declaration
  name: (type_identifier) @local.definition.type)

(type_parameter
  name: (type_identifier) @local.definition.type)

(sealed_case
  name: (type_identifier) @local.definition.type)

; Only the parameters of a function, a function literal or a lambda: a name in
; a function type, `f func(x int) int`, binds nothing.
(function_declaration
  parameters: (parameter_list
    (parameter
      name: (identifier) @local.definition.parameter)))

(func_literal
  parameters: (parameter_list
    (parameter
      name: (identifier) @local.definition.parameter)))

(lambda_expression
  parameters: (parameter_list
    (parameter
      name: (identifier) @local.definition.parameter)))

(receiver
  name: (identifier) @local.definition.parameter)

(val_declaration
  name: (identifier) @local.definition.var)

(var_declaration
  name: (identifier) @local.definition.var)

(tuple_pattern
  (identifier) @local.definition.var)

(embed_declaration
  name: (identifier) @local.definition.var)

(bind_declaration
  name: (identifier) @local.definition.var)

(also_declaration
  name: (identifier) @local.definition.var)

(use_declaration
  name: (identifier) @local.definition.var)

(short_var_declaration
  left: (identifier) @local.definition.var)

(range_clause
  left: (identifier) @local.definition.var)

(typed_pattern
  name: (identifier) @local.definition.var)

; Names a pattern binds: `case x`, `case Some(x)`, `case (a, b)`. A pattern is
; an expression, so only names at its top level and one extractor deep are
; caught; a capitalized name is a variant (`case None`), not a binding.
((case_clause
  pattern: [
    (identifier) @local.definition.var
    (call_expression
      arguments: (argument_list
        (identifier) @local.definition.var))
    (tuple_expression
      (identifier) @local.definition.var)
  ])
  (#match? @local.definition.var "^[a-z_]"))

(import_spec
  name: (package_identifier) @local.definition.import)

; References

(identifier) @local.reference

(type_identifier) @local.reference
