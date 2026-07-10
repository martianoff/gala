grammar gala;

// Entry point
sourceFile: packageClause importDeclaration* topLevelDeclaration* EOF;

packageClause: PACKAGE identifier;

topLevelDeclaration
    : embedDeclaration
    | valDeclaration
    | varDeclaration
    | functionDeclaration
    | typeDeclaration
    | structShorthandDeclaration
    | sealedTypeDeclaration
    ;

embedDeclaration: EMBED VAL identifier type? '=' embedPatterns;
embedPatterns: STRING (',' STRING)*;

structShorthandDeclaration: 'struct' identifier (typeParameters)? parameters;

sealedTypeDeclaration: SEALED 'type' identifier (typeParameters)? '{' sealedCase+ '}';
// Parentheses are optional for zero-field variants: `case Debug` and `case Debug()` are both valid.
// Variants with fields still require parentheses: `case Add(l Expr, r Expr)`.
sealedCase: CASE identifier ('(' sealedCaseFieldList? ')')?;
sealedCaseFieldList: sealedCaseField (',' sealedCaseField)* ','?;
sealedCaseField: identifier type;

declaration
    : valDeclaration
    | varDeclaration
    | bindDeclaration
    | alsoDeclaration
    | useDeclaration
    | functionDeclaration
    | typeDeclaration
    | importDeclaration
    | ifStatement
    | forStatement
    | simpleStatement
    ;

importDeclaration: 'import' ( importSpec | '(' importSpec* ')' );

importSpec: ('.' | identifier)? STRING;

typeDeclaration: 'type' identifier (typeParameters)? (structType | interfaceType | typeAlias);

typeAlias: identifier | type;

structType: 'struct' '{' structField* '}';
structField: (VAL | VAR)? identifier type (STRING)?;

interfaceType: 'interface' '{' methodSpec* '}';
methodSpec: identifier (typeParameters)? signature;

valDeclaration: 'val' (tuplePattern | identifierList) (type)? '=' expressionList;
varDeclaration: 'var' (tuplePattern | identifierList) (type)? ('=' expressionList)?;

// Monadic binding (do-notation). Legal only inside a block whose result type is
// a bindable monad. `also` must follow a `bind`/`also` in the same block; that
// ordering is enforced in the transformer, not the grammar, for better errors.
bindDeclaration: BIND identifier (type)? '=' expression;
alsoDeclaration: ALSO identifier (type)? '=' expression;

// Scoped resource binding (RAII). `use x = acquire` binds x for the rest of the
// enclosing block and guarantees x.Close() runs when the block exits, on every
// path — the GALA-native replacement for Go's `defer x.Close()`. Multiple `use`
// bindings in a block close in reverse (LIFO) order. Desugared in the
// transformer to nested resource.Using scopes; the resource must be Closeable.
useDeclaration: USE identifier (type)? '=' expression;

// Tuple pattern for destructuring: val (a, b) = tuple
tuplePattern: '(' identifierList ')';

functionDeclaration: 'func' (receiver)? identifier (typeParameters)? signature (block | '=' expression);

receiver: '(' (VAL | VAR)? identifier type ')';

signature: parameters (type)?;

parameters: '(' parameterList? ')';
parameterList: parameter (',' parameter)* ','?;
// Parameters can be:
// - Named with type: "x int", "val x int", "x ...int"
// - Named without type: "x" (type inferred)
// - Type only (for function types): "int", "Option[T]", "...int"
// - With default value: "x int = 42", "host string = \"localhost\""
parameter: (VAL | VAR)? (identifier ELLIPSIS? type? paramDefault? | ELLIPSIS? type);
paramDefault: '=' expression;

ELLIPSIS: '...';

typeParameters: '[' typeParameterList ']';
typeParameterList: typeParameter (',' typeParameter)*;
typeParameter: identifier identifier; // e.g. [T any]

// Block bodies accept optional ';' separators between statements, so
// Go-style one-liners like `if v < 0 { neg = true; v = -v }` parse.
// Statements remain primarily newline/whitespace-separated; the semicolon
// is a permissive alternative, not required.
block: '{' ';'* (statement ';'*)* '}';

statement
    : declaration
    | returnStatement
    ;

returnStatement: 'return' expression?;

ifStatement: 'if' (simpleStatement ';')? expression block ('else' (block | ifStatement))?;

forStatement: 'for' (forClause | rangeClause | forCondition)? block;
forClause: simpleStatement? ';' expression? ';' simpleStatement?;
forCondition: expression;
rangeClause: (identifierList (':=' | '=') )? 'range' expression;

simpleStatement
    : incDecStmt
    | assignment
    | shortVarDecl
    | expression
    ;

incDecStmt: expression ('++' | '--');

identifierList: identifier (',' identifier)*;
assignment: expressionList ('=' | '+=' | '-=' | '*=' | '/=') expressionList;
shortVarDecl: identifierList ':=' expressionList;
expressionList: expression (',' expression)*;

// Expression with proper operator precedence
// Uses multiple rules to separate precedence levels clearly
expression
    : orExpr
    ;

orExpr
    : andExpr ('||' andExpr)*
    ;

andExpr
    : equalityExpr ('&&' equalityExpr)*
    ;

equalityExpr
    : relationalExpr (('==' | '!=') relationalExpr)*
    ;

relationalExpr
    : additiveExpr (('<' | '<=' | '>' | '>=') additiveExpr)*
    ;

additiveExpr
    : multiplicativeExpr (('+' | '-' | '|' | '^') multiplicativeExpr)*
    ;

multiplicativeExpr
    : unaryExpr (('*' | '/' | '%' | '<<' | '>>' | '&' | '&^') unaryExpr)*
    ;

unaryExpr
    : unaryOp unaryExpr
    | postfixExpr
    ;

postfixExpr
    : primaryExpr postfixSuffix* ('match' '{' caseClause+ '}')?
    ;

postfixSuffix
    : '.' identifier
    | '(' argumentList? ')'
    | '[' expressionList ']'
    ;

primaryExpr
    : lambdaExpression     // Must come before 'primary' to handle () => ... vs ()
    | primary
    | ifExpression
    | partialFunctionLiteral
    ;

partialFunctionLiteral: '{' caseClause+ '}';
argumentList: argument (',' argument)* ','?;  // Allow trailing comma for multiline formatting
argument: (identifier '=')? (lambdaExpression | pattern);

primary
    : identifier
    | literal
    | '(' tupleExpressionList? ')'
    | compositeLiteral
    ;

// Parenthesized tuple/grouping expressions allow an optional trailing comma so
// multi-line tuple literals (Elm-style `(model, cmd,)`) parse cleanly. Kept
// separate from `expressionList`, which is used in statement positions
// (assignments, short var decls, val declarations) where a trailing comma
// would be a syntax error in the surrounding production.
tupleExpressionList: expression (',' expression)* ','?;

compositeLiteral: type ('{' (elementList ','?)? '}');
elementList: keyedElement (',' keyedElement)*;
keyedElement: (expression ':')? expression;

// Lambda expressions may optionally declare their return type before '=>'
// — useful when storing a lambda in a `val` where the return type isn't
// determined by a target context (e.g. a typed method parameter). When
// omitted, the return type is inferred from the body expression.
lambdaExpression: parameters type? '=>' (expression | block);

caseClause: 'case' pattern (IF guard=expression)? '=>' (bodyStmt=simpleStatement | bodyBlock=block);

pattern
    : expression ELLIPSIS   # restPattern
    | expression            # expressionPattern
    | identifier ':' type   # typedPattern
    ;

ifExpression: 'if' '(' expression ')' ifExprBranch 'else' ifExprBranch;
ifExprBranch: block | expression;

type
    : qualifiedIdentifier (typeArguments)?
    | '[' ']' type // slice
    | '*' type     // pointer
    | 'map' '[' type ']' type
    | 'func' signature
    ;

typeArguments: '[' typeList ']';
typeList: type (',' type)*;

qualifiedIdentifier: identifier ('.' identifier)*;
identifier: IDENTIFIER;

literal
    : INT_LIT
    | FLOAT_LIT
    | STRING
    | CHAR_LIT
    | RAW_STRING
    | INTERPOLATED_STRING
    | FORMAT_STRING
    | 'true'
    | 'false'
    | 'nil'
    ;

// Lexer
VAL: 'val';
VAR: 'var';
BIND: 'bind';
ALSO: 'also';
USE: 'use';
FUNC: 'func';
TYPE: 'type';
STRUCT: 'struct';
INTERFACE: 'interface';
MATCH: 'match';
CASE: 'case';
IF: 'if';
ELSE: 'else';
FOR: 'for';
RANGE: 'range';
RETURN: 'return';
IMPORT: 'import';
PACKAGE: 'package';
SEALED: 'sealed';
EMBED: 'embed';
COLON: ':';

binaryOp: '||' | '&&' | '==' | '!=' | '<' | '<=' | '>' | '>=' | '+' | '-' | '|' | '^' | '*' | '/' | '%' | '<<' | '>>' | '&' | '&^';
unaryOp: '+' | '-' | '!' | '^' | '*' | '&' | '<-';

INTERPOLATED_STRING: 's"' INTERP_BODY '"';
FORMAT_STRING: 'f"' INTERP_BODY '"';

fragment INTERP_BODY: (INTERP_CHAR | '\\' . | '${' BRACE_CONTENT '}')*;
fragment INTERP_CHAR: ~["\r\n\\$] | '$' ~["{\r\n\\];
fragment BRACE_CONTENT: (BRACE_CHAR | '\\' . | '"' (~["\r\n\\] | '\\' .)* '"' | '{' BRACE_CONTENT '}')*;
fragment BRACE_CHAR: ~[{}"\r\n\\];

IDENTIFIER: [a-zA-Z_] [a-zA-Z0-9_]*;
// Integer literals support:
//   - 0x / 0X  hex       (0x1F, 0xdead)
//   - 0b / 0B  binary    (0b1010)
//   - 0o / 0O  octal     (0o644) — Go 1.13+ form, matches the spec users expect
//   - 0<digits> implicit octal (0644) — Go-classic form, retained for compatibility
//   - plain decimal
INT_LIT
    : '0' [xX] [0-9a-fA-F]+
    | '0' [bB] [01]+
    | '0' [oO] [0-7]+
    | [0-9]+
    ;
FLOAT_LIT: [0-9]+ '.' [0-9]* ([eE] [+-]? [0-9]+)? | '.' [0-9]+ ([eE] [+-]? [0-9]+)? | [0-9]+ [eE] [+-]? [0-9]+;
STRING: '"' (~["\r\n\\] | '\\' .)* '"';
CHAR_LIT: '\'' (~['\r\n\\] | '\\' .) '\'';
RAW_STRING: '`' ~[`]* '`';
WS: [ \t\r\n]+ -> skip;
COMMENT: '//' ~[\r\n]* -> skip;
MULTILINE_COMMENT: '/*' .*? '*/' -> skip;
