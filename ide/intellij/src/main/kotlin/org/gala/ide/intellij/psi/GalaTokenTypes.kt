package org.gala.ide.intellij.psi

import com.intellij.psi.tree.IElementType
import com.intellij.psi.tree.TokenSet
import org.antlr.intellij.adaptor.lexer.PSIElementTypeFactory
import org.antlr.intellij.adaptor.lexer.TokenIElementType
import org.gala.ide.intellij.GalaLanguage
import org.gala.ide.intellij.parser.galaLexer
import org.gala.ide.intellij.parser.galaParser

/**
 * Central registry for GALA token and rule element types.
 * Uses PSIElementTypeFactory to bridge ANTLR tokens/rules to IntelliJ IElementTypes.
 */
object GalaTokenTypes {
    init {
        @Suppress("DEPRECATION")
        PSIElementTypeFactory.defineLanguageIElementTypes(
            GalaLanguage,
            galaParser.tokenNames,
            galaParser.ruleNames
        )
    }

    val tokenIElementTypes: List<TokenIElementType> =
        PSIElementTypeFactory.getTokenIElementTypes(GalaLanguage)

    val ruleIElementTypes: List<IElementType> =
        PSIElementTypeFactory.getRuleIElementTypes(GalaLanguage)

    // --- Token sets for ParserDefinition ---

    val WHITESPACE: TokenSet = PSIElementTypeFactory.createTokenSet(
        GalaLanguage,
        galaLexer.WS
    )

    val COMMENTS: TokenSet = PSIElementTypeFactory.createTokenSet(
        GalaLanguage,
        galaLexer.COMMENT,
        galaLexer.MULTILINE_COMMENT
    )

    val STRINGS: TokenSet = PSIElementTypeFactory.createTokenSet(
        GalaLanguage,
        galaLexer.STRING,
        galaLexer.RAW_STRING,
        galaLexer.INTERPOLATED_STRING,
        galaLexer.FORMAT_STRING,
        galaLexer.CHAR_LIT
    )

    // --- Frequently referenced token types ---
    val ID: TokenIElementType get() = tokenIElementTypes[galaLexer.IDENTIFIER]

    // --- Rule element types (for PSI node creation) ---
    val RULE_SOURCE_FILE: IElementType get() = ruleIElementTypes[galaParser.RULE_sourceFile]
    val RULE_FUNCTION_DECLARATION: IElementType get() = ruleIElementTypes[galaParser.RULE_functionDeclaration]
    val RULE_TYPE_DECLARATION: IElementType get() = ruleIElementTypes[galaParser.RULE_typeDeclaration]
    val RULE_SEALED_TYPE_DECLARATION: IElementType get() = ruleIElementTypes[galaParser.RULE_sealedTypeDeclaration]
    val RULE_VAL_DECLARATION: IElementType get() = ruleIElementTypes[galaParser.RULE_valDeclaration]
    val RULE_VAR_DECLARATION: IElementType get() = ruleIElementTypes[galaParser.RULE_varDeclaration]
    val RULE_STRUCT_SHORTHAND_DECLARATION: IElementType get() = ruleIElementTypes[galaParser.RULE_structShorthandDeclaration]
    val RULE_IMPORT_DECLARATION: IElementType get() = ruleIElementTypes[galaParser.RULE_importDeclaration]
    val RULE_PACKAGE_CLAUSE: IElementType get() = ruleIElementTypes[galaParser.RULE_packageClause]
    val RULE_BLOCK: IElementType get() = ruleIElementTypes[galaParser.RULE_block]
    val RULE_EXPRESSION: IElementType get() = ruleIElementTypes[galaParser.RULE_expression]
    val RULE_MATCH_SUFFIX: IElementType get() = ruleIElementTypes[galaParser.RULE_postfixExpr]
    val RULE_CASE_CLAUSE: IElementType get() = ruleIElementTypes[galaParser.RULE_caseClause]
    val RULE_LAMBDA_EXPRESSION: IElementType get() = ruleIElementTypes[galaParser.RULE_lambdaExpression]
    val RULE_SEALED_CASE: IElementType get() = ruleIElementTypes[galaParser.RULE_sealedCase]
    val RULE_STRUCT_FIELD: IElementType get() = ruleIElementTypes[galaParser.RULE_structField]
    val RULE_METHOD_SPEC: IElementType get() = ruleIElementTypes[galaParser.RULE_methodSpec]
    val RULE_IDENTIFIER: IElementType get() = ruleIElementTypes[galaParser.RULE_identifier]
    val RULE_IF_STATEMENT: IElementType get() = ruleIElementTypes[galaParser.RULE_ifStatement]
    val RULE_FOR_STATEMENT: IElementType get() = ruleIElementTypes[galaParser.RULE_forStatement]
    val RULE_PARAMETERS: IElementType get() = ruleIElementTypes[galaParser.RULE_parameters]
    val RULE_SIGNATURE: IElementType get() = ruleIElementTypes[galaParser.RULE_signature]
    val RULE_RECEIVER: IElementType get() = ruleIElementTypes[galaParser.RULE_receiver]
    val RULE_TYPE: IElementType get() = ruleIElementTypes[galaParser.RULE_type]
    val RULE_QUALIFIED_IDENTIFIER: IElementType get() = ruleIElementTypes[galaParser.RULE_qualifiedIdentifier]
}
