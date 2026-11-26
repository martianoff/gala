/**
 * @file Tree-sitter grammar for GALA, a functional language that transpiles to Go.
 * @license Apache-2.0
 *
 * The source of truth is GALA's ANTLR grammar, internal/parser/grammar/gala.g4.
 * Each rule below names the gala.g4 rule it follows. Where the two differ, the
 * comment says why.
 *
 * Newlines. GALA has no statement terminator: statements are separated by
 * whitespace, and an expression continues across a line break wherever it can
 * (`a\n+ b`, a `.Map(...)` chain). The one exception is GALA's line-break rule
 * (internal/parser/newline.go): a '(' that starts a line is never a call, and a
 * '*' or '&' that starts a line inside a block, written against its operand,
 * is a prefix operator. The external scanner (src/scanner.c) implements it:
 *
 *   - `_call_lparen` is a '(' with no line break before it. A call takes only
 *     this token; every other '(' takes it or a plain '(' (`_lparen`), since
 *     the scanner produces it wherever a call could follow.
 *   - `_nl_lparen` is a '(' after a line break, which no call takes. The
 *     scanner reads through comments to find the line break, so
 *     `f()\n/* note *\/ (x)` is two statements.
 *   - `_nl_star` / `_nl_amp` are a '*' / '&' after a line break, directly
 *     against their operand, where a prefix operator can begin. Only the unary
 *     operator takes them, so they never continue the line before.
 */

/* eslint-disable arrow-parens, camelcase, max-len */
/// <reference types="tree-sitter-cli/dsl" />
// @ts-check

const PREC = {
  lambda: -1,
  or: 1,
  and: 2,
  equality: 3,
  relational: 4,
  additive: 5,
  multiplicative: 6,
  unary: 7,
  postfix: 8,
};

const commaSep1 = (rule) => seq(rule, repeat(seq(',', rule)));
// A list that may end in a trailing comma, for multi-line argument lists.
const trailingCommaSep1 = (rule) => seq(commaSep1(rule), optional(','));

// `bind`, `also` and `use`: keyword identifier type? '=' expression
const binding = ($, keyword) => seq(
  keyword,
  field('name', $.identifier),
  optional(field('type', $._type)),
  '=',
  field('value', $._expression),
);

// The text of an interpreted string between escapes.
const stringContent = ($) => alias(token.immediate(prec(1, /[^"\r\n\\]+/)), $.string_content);

module.exports = grammar({
  name: 'gala',

  externals: $ => [
    $._call_lparen,
    $._nl_lparen,
    $._nl_star,
    $._nl_amp,
    // Never produced: lets the scanner detect error recovery, when tree-sitter
    // marks every external token valid at once.
    $._error_sentinel,
  ],

  extras: $ => [/\s/, $.comment],

  word: $ => $.identifier,

  // gala.g4 is parsed by an ALL(*) parser, which looks as far ahead as it
  // needs. Each conflict below is a place where it does, and where this
  // grammar lets tree-sitter's GLR parser try both readings instead.
  conflicts: $ => [
    // `(x)` is a lambda's parameter list or a parenthesized expression, and a
    // parameter `x` a name or a type, until `=>` (or its absence) decides.
    [$._primary, $.parameter],
    [$._primary, $._type_name],
    [$._primary, $._type_name, $.parameter],
    [$.parameter, $._type_name],
    [$.parameter_list, $.tuple_expression],
    // `func (` starts a function literal's parameters, a function type's, or
    // (in a statement) a method's receiver.
    [$.parameter_list, $._function_type_parameter_list],
    [$.parameter, $._function_type_parameter],
    [$.parameter, $._type_name, $._function_type_parameter],
    [$._type_name, $._function_type_parameter],
    [$.receiver, $.parameter, $._function_type_parameter],
    [$.receiver, $.parameter, $._type_name, $._function_type_parameter],
    [$._primary, $._type],
    // `Foo{...}` and `pkg.Foo[T]{...}` are composite literals; in
    // `if x { ... }` the `{` opens a block.
    [$._primary, $.qualified_type],
    [$._type_name, $.qualified_type],
    [$._type, $.generic_type],
    // `a, b := ...` and `for k, v := range ...` start like an expression list.
    [$.short_var_declaration, $._primary],
    [$.range_clause, $._primary],
    [$.range_clause, $.short_var_declaration, $._primary],
    // `for ;; {` — the `{` may begin the update statement or the body.
    [$.for_clause],
    // A result type is optional, so the next line may be one or start anew:
    // an interface method `ForEach(f func(T))` followed by `Size() int`.
    [$._signature],
    // So is a function type's: a struct field `f func()` followed by `g int`.
    [$.function_type],
    // And a bare `return`'s: `return` then `x = 1` on the next line.
    [$.return_statement],
    // `if (c) a else b` is an if expression; `if (c) { ... }` an if statement
    // with a parenthesized condition.
    [$.parenthesized_expression, $.if_expression],
  ],

  rules: {
    // sourceFile: packageClause importDeclaration* topLevelDeclaration* EOF
    source_file: $ => seq(
      $.package_clause,
      repeat($.import_declaration),
      repeat($._top_level_declaration),
    ),

    // packageClause
    package_clause: $ => seq('package', alias($.identifier, $.package_identifier)),

    // topLevelDeclaration
    _top_level_declaration: $ => choice(
      $.embed_declaration,
      $.val_declaration,
      $.var_declaration,
      $.function_declaration,
      $.type_declaration,
      $.struct_declaration,
      $.sealed_type_declaration,
      $.opaque_type_declaration,
    ),

    // embedDeclaration: EMBED VAL identifier type? '=' embedPatterns
    embed_declaration: $ => seq(
      'embed',
      'val',
      field('name', $.identifier),
      optional(field('type', $._type)),
      '=',
      commaSep1(field('pattern', $.interpreted_string_literal)),
    ),

    // structShorthandDeclaration: 'struct' identifier typeParameters? parameters
    struct_declaration: $ => seq(
      'struct',
      field('name', alias($.identifier, $.type_identifier)),
      optional(field('type_parameters', $.type_parameter_list)),
      field('parameters', $.parameter_list),
    ),

    // sealedTypeDeclaration: SEALED 'type' identifier typeParameters? '{' sealedCase+ '}'
    sealed_type_declaration: $ => seq(
      'sealed',
      'type',
      field('name', alias($.identifier, $.type_identifier)),
      optional(field('type_parameters', $.type_parameter_list)),
      '{',
      repeat1($.sealed_case),
      '}',
    ),

    // sealedCase: DEFAULT? CASE identifier (('(' | NL_LPAREN) sealedCaseFieldList? ')')?
    sealed_case: $ => seq(
      optional('default'),
      'case',
      field('name', alias($.identifier, $.type_identifier)),
      optional(seq(
        $._lparen,
        optional(trailingCommaSep1($.sealed_case_field)),
        ')',
      )),
    ),

    // sealedCaseField: identifier type paramDefault?
    sealed_case_field: $ => seq(
      field('name', alias($.identifier, $.field_identifier)),
      field('type', $._type),
      optional(field('default', $.default_value)),
    ),

    // opaqueTypeDeclaration: OPAQUE 'type' identifier typeParameters? type
    opaque_type_declaration: $ => seq(
      'opaque',
      'type',
      field('name', alias($.identifier, $.type_identifier)),
      optional(field('type_parameters', $.type_parameter_list)),
      field('type', $._type),
    ),

    // importDeclaration: 'import' (importSpec | '(' importSpec* ')')
    import_declaration: $ => seq(
      'import',
      choice(
        $.import_spec,
        seq($._lparen, repeat($.import_spec), ')'),
      ),
    ),

    // importSpec: ('.' | identifier)? STRING
    import_spec: $ => seq(
      optional(field('name', choice(
        alias('.', $.dot),
        alias($.identifier, $.package_identifier),
      ))),
      field('path', $.interpreted_string_literal),
    ),

    // typeDeclaration: 'type' identifier typeParameters? (structType | interfaceType | typeAlias)
    type_declaration: $ => seq(
      'type',
      field('name', alias($.identifier, $.type_identifier)),
      optional(field('type_parameters', $.type_parameter_list)),
      field('type', choice($.struct_type, $.interface_type, $._type)),
    ),

    // structType: 'struct' '{' structField* '}'
    struct_type: $ => seq('struct', '{', repeat($.field_declaration), '}'),

    // structField: (VAL | VAR)? identifier type
    field_declaration: $ => seq(
      optional(choice('val', 'var')),
      field('name', alias($.identifier, $.field_identifier)),
      field('type', $._type),
    ),

    // interfaceType: 'interface' '{' methodSpec* '}'
    interface_type: $ => seq('interface', '{', repeat($.method_spec), '}'),

    // methodSpec: INOUT? identifier typeParameters? signature
    method_spec: $ => seq(
      optional('inout'),
      field('name', alias($.identifier, $.field_identifier)),
      optional(field('type_parameters', $.type_parameter_list)),
      $._signature,
    ),

    // valDeclaration: 'val' (tuplePattern | identifierList) type? '=' expressionList
    val_declaration: $ => seq(
      'val',
      $._binding_names,
      optional(field('type', $._type)),
      '=',
      commaSep1(field('value', $._expression)),
    ),

    // varDeclaration: 'var' (tuplePattern | identifierList) type? ('=' expressionList)?
    var_declaration: $ => prec.right(seq(
      'var',
      $._binding_names,
      optional(field('type', $._type)),
      optional(seq('=', commaSep1(field('value', $._expression)))),
    )),

    _binding_names: $ => choice(
      field('pattern', $.tuple_pattern),
      commaSep1(field('name', $.identifier)),
    ),

    // tuplePattern: '(' identifierList ')'
    tuple_pattern: $ => seq($._lparen, commaSep1($.identifier), ')'),

    // bindDeclaration: BIND identifier type? '=' expression
    bind_declaration: $ => binding($, 'bind'),

    // alsoDeclaration: ALSO identifier type? '=' expression
    also_declaration: $ => binding($, 'also'),

    // useDeclaration: USE identifier type? '=' expression
    use_declaration: $ => binding($, 'use'),

    // functionDeclaration: 'func' receiver? identifier typeParameters? signature (block | '=' expression)
    function_declaration: $ => seq(
      'func',
      optional(field('receiver', $.receiver)),
      field('name', $.identifier),
      optional(field('type_parameters', $.type_parameter_list)),
      $._signature,
      choice(
        field('body', $.block),
        seq('=', field('body', $._expression)),
      ),
    ),

    // receiver: '(' (VAL | VAR | INOUT)? identifier type ')'
    receiver: $ => seq(
      $._lparen,
      optional(choice('val', 'var', 'inout')),
      field('name', $.identifier),
      field('type', $._type),
      ')',
    ),

    // signature: parameters (type | goResults)?
    _signature: $ => seq(
      field('parameters', $.parameter_list),
      optional(field('result', choice($._type, $.result_list))),
    ),

    // goResults: '(' type (',' type)+ ')'
    // A '(' on the same line only, unlike `parameters`: one that starts a
    // line is never a result list (newline.go makes it NL_LPAREN, which
    // goResults does not take).
    result_list: $ => seq(
      alias($._call_lparen, '('),
      $._type,
      repeat1(seq(',', $._type)),
      ')',
    ),

    // parameters: ('(' | NL_LPAREN) parameterList? ')'
    parameter_list: $ => seq(
      $._lparen,
      optional(trailingCommaSep1($.parameter)),
      ')',
    ),

    // parameter: (VAL | VAR | INOUT)? (identifier ELLIPSIS? type? paramDefault? | ELLIPSIS? type)
    parameter: $ => seq(
      optional(choice('val', 'var', 'inout')),
      choice(
        prec.dynamic(1, seq(
          field('name', $.identifier),
          optional('...'),
          optional(field('type', $._type)),
          optional(field('default', $.default_value)),
        )),
        seq(optional('...'), field('type', $._type)),
      ),
    ),

    // paramDefault: '=' expression
    default_value: $ => seq('=', $._expression),

    // typeParameters: '[' typeParameterList ']'
    type_parameter_list: $ => seq('[', commaSep1($.type_parameter), ']'),

    // typeParameter: identifier type
    type_parameter: $ => seq(
      field('name', alias($.identifier, $.type_identifier)),
      field('constraint', $._type),
    ),

    // block: '{' ';'* (statement ';'*)* '}'
    block: $ => seq(
      '{',
      repeat(';'),
      repeat(seq($._statement, repeat(';'))),
      '}',
    ),

    // statement: declaration | returnStatement
    // declaration: val | var | bind | also | use | func | type | import | if | for | simpleStatement
    _statement: $ => choice(
      $.val_declaration,
      $.var_declaration,
      $.bind_declaration,
      $.also_declaration,
      $.use_declaration,
      $.function_declaration,
      $.type_declaration,
      $.import_declaration,
      $.if_statement,
      $.for_statement,
      $._simple_statement,
      $.return_statement,
    ),

    // returnStatement: 'return' expression?
    // A value on the next line is still returned, as in gala.g4.
    // A bare `return` may be followed by a statement on the next line that
    // does not continue it (`x = 1`, `x++`): a conflict for GLR to decide,
    // preferring the value when both readings parse, as gala.g4's greedy
    // `expression?` does.
    return_statement: $ => choice(
      prec.dynamic(1, seq('return', $._expression)),
      'return',
    ),

    // ifStatement: 'if' (simpleStatement ';')? expression block ('else' (block | ifStatement))?
    // `if (c) { a } else { b }` in statement position reads as both this and
    // an if expression; gala.g4 lists ifStatement first, so it wins here too.
    if_statement: $ => prec.dynamic(1, prec.right(seq(
      'if',
      optional(seq(field('initializer', $._simple_statement), ';')),
      field('condition', $._expression),
      field('consequence', $.block),
      optional(seq(
        'else',
        field('alternative', choice($.block, $.if_statement)),
      )),
    ))),

    // forStatement: 'for' (forClause | rangeClause | forCondition)? block
    for_statement: $ => seq(
      'for',
      optional(choice($.for_clause, $.range_clause, field('condition', $._expression))),
      field('body', $.block),
    ),

    // forClause: simpleStatement? ';' expression? ';' simpleStatement?
    for_clause: $ => seq(
      optional(field('initializer', $._simple_statement)),
      ';',
      optional(field('condition', $._expression)),
      ';',
      optional(field('update', $._simple_statement)),
    ),

    // rangeClause: (identifierList (':=' | '='))? 'range' expression
    range_clause: $ => seq(
      optional(seq(
        commaSep1(field('left', $.identifier)),
        choice(':=', '='),
      )),
      'range',
      field('right', $._expression),
    ),

    // simpleStatement: incDecStmt | assignment | shortVarDecl | expression
    _simple_statement: $ => choice(
      $.inc_dec_statement,
      $.assignment_statement,
      $.short_var_declaration,
      $.expression_statement,
    ),

    expression_statement: $ => $._expression,

    // incDecStmt: expression ('++' | '--')
    inc_dec_statement: $ => seq(
      field('operand', $._expression),
      field('operator', choice('++', '--')),
    ),

    // assignment: expressionList ('=' | '+=' | '-=' | '*=' | '/=') expressionList
    assignment_statement: $ => seq(
      field('left', $.expression_list),
      field('operator', choice('=', '+=', '-=', '*=', '/=')),
      field('right', $.expression_list),
    ),

    // shortVarDecl: identifierList ':=' expressionList
    short_var_declaration: $ => seq(
      commaSep1(field('left', $.identifier)),
      ':=',
      field('right', $.expression_list),
    ),

    expression_list: $ => commaSep1($._expression),

    // expression: orExpr; orExpr .. multiplicativeExpr
    _expression: $ => choice(
      $.binary_expression,
      $.unary_expression,
      $.match_expression,
      $._postfix_expression,
    ),

    binary_expression: $ => {
      const table = [
        [PREC.or, '||'],
        [PREC.and, '&&'],
        [PREC.equality, choice('==', '!=')],
        [PREC.relational, choice('<', '<=', '>', '>=')],
        [PREC.additive, choice('+', '-', '|', '^')],
        [PREC.multiplicative, choice('*', '/', '%', '<<', '>>', '&', '&^')],
      ];
      return choice(...table.map(([precedence, operator]) => prec.left(precedence, seq(
        field('left', $._expression),
        // @ts-ignore
        field('operator', operator),
        field('right', $._expression),
      ))));
    },

    // unaryExpr: unaryOp unaryExpr
    // unaryOp: '+' | '-' | '!' | '^' | '*' | NL_STAR | '&' | NL_AMP | '<-'
    unary_expression: $ => prec(PREC.unary, seq(
      field('operator', choice(
        '+', '-', '!', '^', '*', '&', '<-',
        alias($._nl_star, '*'),
        alias($._nl_amp, '&'),
      )),
      field('operand', $._expression),
    )),

    // postfixExpr: primaryExpr postfixSuffix* ('match' '{' caseClause+ '}')?
    // A match ends the chain: no suffix follows it.
    match_expression: $ => prec(PREC.postfix, seq(
      field('value', $._postfix_expression),
      'match',
      '{',
      repeat1($.case_clause),
      '}',
    )),

    _postfix_expression: $ => choice(
      $._primary_expression,
      $.call_expression,
      $.selector_expression,
      $.index_expression,
    ),

    // postfixSuffix: '(' argumentList? ')' — a plain '(' only, never NL_LPAREN.
    call_expression: $ => prec(PREC.postfix, seq(
      field('function', $._postfix_expression),
      field('arguments', $.argument_list),
    )),

    // postfixSuffix: '.' identifier
    selector_expression: $ => prec(PREC.postfix, seq(
      field('operand', $._postfix_expression),
      '.',
      field('field', alias($.identifier, $.field_identifier)),
    )),

    // postfixSuffix: '[' expressionList ']' — an index, or explicit type arguments.
    index_expression: $ => prec(PREC.postfix, seq(
      field('operand', $._postfix_expression),
      '[',
      commaSep1(field('index', $._expression)),
      ']',
    )),

    // argumentList: argument (',' argument)* ','?
    argument_list: $ => seq(
      alias($._call_lparen, '('),
      optional(trailingCommaSep1(choice($.named_argument, $._pattern))),
      ')',
    ),

    // argument: (identifier '=')? (lambdaExpression | pattern)
    named_argument: $ => seq(
      field('name', $.identifier),
      '=',
      field('value', $._pattern),
    ),

    // primaryExpr: lambdaExpression | primary | ifExpression | partialFunctionLiteral
    _primary_expression: $ => choice(
      $.lambda_expression,
      $._primary,
      $.if_expression,
      $.partial_function_literal,
    ),

    // primary: identifier | literal | '(' tupleExpressionList? ')' | 'func' signature block? | compositeLiteral
    _primary: $ => choice(
      $.identifier,
      $._literal,
      $.parenthesized_expression,
      $.tuple_expression,
      $.func_literal,
      $.function_type,
      $.composite_literal,
    ),

    parenthesized_expression: $ => seq($._lparen, $._expression, ')'),

    // tupleExpressionList: expression (',' expression)* ','?
    // `()` is the unit tuple; one element needs a trailing comma to be a tuple.
    tuple_expression: $ => seq(
      $._lparen,
      optional(choice(
        seq($._expression, ','),
        seq($._expression, repeat1(seq(',', $._expression)), optional(',')),
      )),
      ')',
    ),

    // 'func' signature block — a Go function literal (rejected by the
    // compiler, GALA-E0117). Without the block, `'func' signature` is a
    // function type written as a type argument: `function_type` in `_primary`.
    func_literal: $ => seq(
      'func',
      $._signature,
      field('body', $.block),
    ),

    // compositeLiteral: type '{' (elementList ','?)? '}'
    composite_literal: $ => seq(
      field('type', $._type),
      field('body', $.literal_value),
    ),

    literal_value: $ => seq(
      '{',
      optional(trailingCommaSep1($.keyed_element)),
      '}',
    ),

    // keyedElement: (expression ':')? expression
    keyed_element: $ => seq(
      optional(seq(field('key', $._expression), ':')),
      field('value', $._expression),
    ),

    // lambdaExpression: parameters type? '=>' (expression | block)
    lambda_expression: $ => prec.right(PREC.lambda, seq(
      field('parameters', $.parameter_list),
      optional(field('result', $._type)),
      '=>',
      field('body', choice($._expression, $.block)),
    )),

    // ifExpression: 'if' '(' expression ')' ifExprBranch 'else' ifExprBranch
    if_expression: $ => prec.right(seq(
      'if',
      $._lparen,
      field('condition', $._expression),
      ')',
      field('consequence', choice($.block, $._expression)),
      'else',
      field('alternative', choice($.block, $._expression)),
    )),

    // partialFunctionLiteral: '{' caseClause+ '}'
    partial_function_literal: $ => seq('{', repeat1($.case_clause), '}'),

    // caseClause: 'case' pattern (IF guard=expression)? '=>' (simpleStatement | block)
    case_clause: $ => seq(
      'case',
      field('pattern', $._pattern),
      optional(seq('if', field('guard', $._expression))),
      '=>',
      field('body', choice($._simple_statement, $.block)),
    ),

    // pattern: expression ELLIPSIS | expression | identifier ':' type
    _pattern: $ => choice(
      $._expression,
      $.typed_pattern,
      $.rest_pattern,
    ),

    typed_pattern: $ => seq(
      field('name', $.identifier),
      ':',
      field('type', $._type),
    ),

    rest_pattern: $ => seq($._expression, '...'),

    // type
    _type: $ => choice(
      $._type_name,
      $.generic_type,
      $.slice_type,
      $.pointer_type,
      $.map_type,
      $.function_type,
    ),

    // qualifiedIdentifier: identifier ('.' identifier)*
    _type_name: $ => choice(
      alias($.identifier, $.type_identifier),
      $.qualified_type,
    ),

    qualified_type: $ => seq(
      field('package', alias($.identifier, $.package_identifier)),
      '.',
      field('name', alias($.identifier, $.type_identifier)),
    ),

    // qualifiedIdentifier typeArguments
    generic_type: $ => seq(
      field('type', $._type_name),
      field('type_arguments', $.type_arguments),
    ),

    // typeArguments: '[' typeList ']'
    type_arguments: $ => seq('[', commaSep1($._type), ']'),

    // '[' ']' type
    slice_type: $ => seq('[', ']', field('element', $._type)),

    // '*' type
    pointer_type: $ => prec(PREC.unary, seq('*', $._type)),

    // 'map' '[' type ']' type
    map_type: $ => seq('map', '[', field('key', $._type), ']', field('value', $._type)),

    // 'func' signature
    // gala.g4 parses its parameters with the `parameter` rule, where a lone
    // name could be either; in a function type it is a type, `func(int) bool`,
    // so here a parameter has a name only when a type follows it.
    // The result is optional, so a name on the next line may be the result or
    // what follows the type (`f func()` then a field `g int`): a conflict, for
    // GLR to decide, as gala.g4's lookahead does.
    function_type: $ => seq(
      'func',
      field('parameters', alias($._function_type_parameter_list, $.parameter_list)),
      optional(field('result', choice($._type, $.result_list))),
    ),

    _function_type_parameter_list: $ => seq(
      $._lparen,
      optional(trailingCommaSep1(alias($._function_type_parameter, $.parameter))),
      ')',
    ),

    _function_type_parameter: $ => seq(
      optional(choice('val', 'var', 'inout')),
      optional(choice(seq(field('name', $.identifier), optional('...')), '...')),
      field('type', $._type),
      optional(field('default', $.default_value)),
    ),

    // Any '(' but a call's: gala.g4's `'(' | NL_LPAREN`. The scanner produces
    // `_call_lparen` wherever a call could also follow, so every '(' takes it.
    _lparen: $ => choice('(', alias($._call_lparen, '('), alias($._nl_lparen, '(')),

    // literal
    _literal: $ => choice(
      $.int_literal,
      $.float_literal,
      $.interpreted_string_literal,
      $.rune_literal,
      $.raw_string_literal,
      $.interpolated_string_literal,
      $.true,
      $.false,
      $.nil,
    ),

    true: _ => 'true',
    false: _ => 'false',
    nil: _ => 'nil',

    // INT_LIT
    int_literal: _ => token(choice(
      /0[xX][0-9a-fA-F]+/,
      /0[bB][01]+/,
      /0[oO][0-7]+/,
      /[0-9]+/,
    )),

    // FLOAT_LIT
    float_literal: _ => token(choice(
      /[0-9]+\.[0-9]*([eE][+-]?[0-9]+)?/,
      /\.[0-9]+([eE][+-]?[0-9]+)?/,
      /[0-9]+[eE][+-]?[0-9]+/,
    )),

    // STRING: '"' (~["\r\n\\] | '\\' .)* '"'
    // Inside an interpolation a string may also be written with escaped
    // quotes, `${m.Get(\"k\")}`: the interpolation splitter unescapes them.
    interpreted_string_literal: $ => choice(
      seq(
        '"',
        repeat(choice(
          stringContent($),
          $.escape_sequence,
        )),
        token.immediate('"'),
      ),
      seq(
        '\\"',
        repeat(choice(
          stringContent($),
          alias(token.immediate(/\\[^"]/), $.escape_sequence),
        )),
        token.immediate('\\"'),
      ),
    ),

    // ANTLR's `.` in `'\\' .` matches a line break; a tree-sitter regex's `.`
    // does not, so the escaped character spells it out.
    escape_sequence: _ => token.immediate(/\\(.|\r?\n)/),

    // CHAR_LIT: '\'' (~['\r\n\\] | '\\' .) '\''
    rune_literal: _ => token(seq('\'', choice(/[^'\r\n\\]/, /\\(.|\r?\n)/), '\'')),

    // RAW_STRING: '`' ~[`]* '`' — GALA's multi-line string.
    raw_string_literal: _ => token(seq('`', /[^`]*/, '`')),

    // INTERPOLATED_STRING: 's"' INTERP_BODY '"'
    // FORMAT_STRING:       'f"' INTERP_BODY '"'
    // The ANTLR lexer keeps the body opaque; the transpiler splits it
    // (internal/interpolation). Here it is parsed so that the embedded
    // expressions are highlighted: `$name`, `${expression}`, and `$$` for a
    // literal `$`.
    interpolated_string_literal: $ => seq(
      choice('s"', 'f"'),
      repeat(choice(
        alias(token.immediate(prec(1, /[^"\r\n\\$]+/)), $.string_content),
        // A `$` that starts no interpolation is literal text.
        alias(token.immediate(prec(1, /\$[^{"\r\n\\$a-zA-Z_]/)), $.string_content),
        alias(token.immediate('$$'), $.escape_sequence),
        $.escape_sequence,
        $.interpolation,
      )),
      token.immediate('"'),
    ),

    interpolation: $ => choice(
      seq(token.immediate('$'), alias(token.immediate(/[a-zA-Z_][a-zA-Z0-9_]*/), $.identifier)),
      seq(token.immediate('${'), $._expression, '}'),
    ),

    // IDENTIFIER: [a-zA-Z_] [a-zA-Z0-9_]*
    identifier: _ => /[a-zA-Z_][a-zA-Z0-9_]*/,

    // COMMENT, MULTILINE_COMMENT
    comment: _ => token(choice(
      seq('//', /[^\r\n]*/),
      seq('/*', /[^*]*\*+([^/*][^*]*\*+)*/, '/'),
    )),
  },
});
