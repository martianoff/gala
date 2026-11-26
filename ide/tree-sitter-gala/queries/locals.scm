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
  ; A type declaration scopes its type parameters.
  (type_declaration)
  (struct_declaration)
  (sealed_type_declaration)
  (opaque_type_declaration)
] @local.scope

; Definitions. A declaration that is a scope of its own defines its name in
; the scope around it ("parent"), where the rest of the file sees it.

(function_declaration
  name: (identifier) @local.definition.function
  (#set! definition.function.scope "parent"))

(type_declaration
  name: (type_identifier) @local.definition.type
  (#set! definition.type.scope "parent"))

(struct_declaration
  name: (type_identifier) @local.definition.type
  (#set! definition.type.scope "parent"))

(sealed_type_declaration
  name: (type_identifier) @local.definition.type
  (#set! definition.type.scope "parent"))

(opaque_type_declaration
  name: (type_identifier) @local.definition.type
  (#set! definition.type.scope "parent"))

(type_parameter
  name: (type_identifier) @local.definition.type)

(sealed_case
  name: (type_identifier) @local.definition.type
  (#set! definition.type.scope "parent"))

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
; caught; a capitalized name is a variant (`case None`), and `_` a wildcard,
; not a binding.
((case_clause
  pattern: [
    (identifier) @local.definition.var
    (call_expression
      arguments: (argument_list
        (identifier) @local.definition.var))
    (tuple_expression
      (identifier) @local.definition.var)
  ])
  (#match? @local.definition.var "^([a-z]|_[a-zA-Z0-9_])"))

(import_spec
  name: (package_identifier) @local.definition.import)

; References

(identifier) @local.reference

(type_identifier) @local.reference
