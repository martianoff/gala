grammar gala;

// Entry point
sourceFile: declaration* EOF;

declaration
    : valDeclaration
    | varDeclaration
    | functionDeclaration
    | typeDeclaration
    | importDeclaration
    | ifStatement
    | forStatement
    | expressionStatement
    ;

importDeclaration: 'import' ( STRING | '(' STRING+ ')' );

typeDeclaration: 'type' identifier (typeParameters)? (structType | interfaceType | typeAlias);

typeAlias: identifier | type;

structType: 'struct' '{' structField* '}';
structField: (VAL | VAR)? identifier type (STRING)?;

interfaceType: 'interface' '{' methodSpec* '}';
methodSpec: identifier signature;

valDeclaration: 'val' identifier (type)? '=' expression;
varDeclaration: 'var' identifier (type)? ('=' expression)?;

functionDeclaration: 'func' identifier (typeParameters)? signature (block | '=' expression);

signature: parameters (type)?;

parameters: '(' parameterList? ')';
parameterList: parameter (',' parameter)*;
parameter: (VAL | VAR)? identifier (type)?;

typeParameters: '[' typeParameterList ']';
typeParameterList: typeParameter (',' typeParameter)*;
typeParameter: identifier identifier; // e.g. [T any]

block: '{' statement* '}';

statement
    : declaration
    | returnStatement
    ;

expressionStatement: expression;
returnStatement: 'return' expression?;

ifStatement: 'if' (simpleStatement ';')? expression block ('else' (block | ifStatement))?;

forStatement: 'for' (forClause | rangeClause)? block;
forClause: simpleStatement? ';' expression? ';' simpleStatement?;
rangeClause: (identifierList (':=' | '=') )? 'range' expression;

simpleStatement
    : expression
    | assignment
    | shortVarDecl
    ;

identifierList: identifier (',' identifier)*;
assignment: expressionList ('=' | '+=' | '-=' | '*=' | '/=') expressionList;
shortVarDecl: identifierList ':=' expressionList;
expressionList: expression (',' expression)*;

expression
    : primary
    | expression ('.' identifier | '[' expressionList ']' | '(' expressionList? ')' )
    | unaryOp expression
    | expression binaryOp expression
    | lambdaExpression
    | expression 'match' '{' caseClause+ '}'
    | ifExpression
    ;

primary
    : identifier
    | literal
    | '(' expression ')'
    ;

lambdaExpression: parameters '=>' (expression | block);

caseClause: 'case' expression '=>' (expression | block);

ifExpression: 'if' '(' expression ')' expression 'else' expression;

type
    : identifier (typeArguments)?
    | '[' ']' type // slice
    | '*' type     // pointer
    | 'map' '[' type ']' type
    | 'func' signature
    ;

typeArguments: '[' typeList ']';
typeList: type (',' type)*;

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

binaryOp: '||' | '&&' | '==' | '!=' | '<' | '<=' | '>' | '>=' | '+' | '-' | '|' | '^' | '*' | '/' | '%' | '<<' | '>>' | '&' | '&^';
unaryOp: '+' | '-' | '!' | '^' | '*' | '&' | '<-';

IDENTIFIER: [a-zA-Z_] [a-zA-Z0-9_]*;
INT_LIT: [0-9]+;
FLOAT_LIT: [0-9]+ '.' [0-9]* | '.' [0-9]+;
STRING: '"' (~["\r\n\\] | '\\' .)* '"';
WS: [ \t\r\n]+ -> skip;
COMMENT: '//' ~[\r\n]* -> skip;
MULTILINE_COMMENT: '/*' .*? '*/' -> skip;
