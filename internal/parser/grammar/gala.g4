grammar gala;

// Entry point
sourceFile: packageClause importDeclaration* topLevelDeclaration* EOF;

packageClause: PACKAGE identifier;

topLevelDeclaration
    : valDeclaration
    | varDeclaration
    | functionDeclaration
    | typeDeclaration
    | structShorthandDeclaration
    ;

structShorthandDeclaration: 'struct' identifier parameters;

declaration
    : valDeclaration
    | varDeclaration
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

// Tuple pattern for destructuring: val (a, b) = tuple
tuplePattern: '(' identifierList ')';

functionDeclaration: 'func' (receiver)? identifier (typeParameters)? signature (block | '=' expression);

receiver: '(' (VAL | VAR)? identifier type ')';

signature: parameters (type)?;

parameters: '(' parameterList? ')';
parameterList: parameter (',' parameter)*;
// Parameters can be:
// - Named with type: "x int", "val x int", "x ...int"
// - Named without type: "x" (type inferred)
// - Type only (for function types): "int", "Option[T]", "...int"
parameter: (VAL | VAR)? (identifier ELLIPSIS? type? | ELLIPSIS? type);

ELLIPSIS: '...';

typeParameters: '[' typeParameterList ']';
typeParameterList: typeParameter (',' typeParameter)*;
typeParameter: identifier identifier; // e.g. [T any]

block: '{' statement* '}';

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
argument: (identifier '=')? pattern;

primary
    : identifier
    | literal
    | '(' expressionList? ')'
    | compositeLiteral
    ;

compositeLiteral: type ('{' (elementList ','?)? '}');
elementList: keyedElement (',' keyedElement)*;
keyedElement: (expression ':')? expression;

lambdaExpression: parameters '=>' (expression | block);

caseClause: 'case' pattern (IF guard=expression)? '=>' (body=expression | bodyBlock=block);

pattern
    : expression ELLIPSIS   # restPattern
    | expression            # expressionPattern
    | identifier ':' type   # typedPattern
    ;

ifExpression: 'if' '(' expression ')' expression 'else' expression;

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
    | 'true'
    | 'false'
    | 'nil'
    ;

// Lexer
VAL: 'val';
VAR: 'var';
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
COLON: ':';

binaryOp: '||' | '&&' | '==' | '!=' | '<' | '<=' | '>' | '>=' | '+' | '-' | '|' | '^' | '*' | '/' | '%' | '<<' | '>>' | '&' | '&^';
unaryOp: '+' | '-' | '!' | '^' | '*' | '&' | '<-';

IDENTIFIER: [a-zA-Z_] [a-zA-Z0-9_]*;
INT_LIT: [0-9]+;
FLOAT_LIT: [0-9]+ '.' [0-9]* | '.' [0-9]+;
STRING: '"' (~["\r\n\\] | '\\' .)* '"';
WS: [ \t\r\n]+ -> skip;
COMMENT: '//' ~[\r\n]* -> skip;
MULTILINE_COMMENT: '/*' .*? '*/' -> skip;
