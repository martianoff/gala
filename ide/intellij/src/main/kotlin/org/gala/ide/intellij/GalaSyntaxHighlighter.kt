package org.gala.ide.intellij

import com.intellij.lexer.Lexer
import com.intellij.openapi.editor.DefaultLanguageHighlighterColors
import com.intellij.openapi.editor.HighlighterColors
import com.intellij.openapi.editor.colors.TextAttributesKey
import com.intellij.openapi.editor.colors.TextAttributesKey.createTextAttributesKey
import com.intellij.openapi.fileTypes.SyntaxHighlighterBase
import com.intellij.psi.tree.IElementType
import org.antlr.intellij.adaptor.lexer.ANTLRLexerAdaptor
import org.antlr.intellij.adaptor.lexer.TokenIElementType
import org.gala.ide.intellij.parser.galaLexer

class GalaSyntaxHighlighter : SyntaxHighlighterBase() {
    companion object {
        // Text attribute keys
        val KEYWORD = createTextAttributesKey("GALA_KEYWORD", DefaultLanguageHighlighterColors.KEYWORD)
        val TYPE = createTextAttributesKey("GALA_TYPE", DefaultLanguageHighlighterColors.CLASS_NAME)
        val STRING = createTextAttributesKey("GALA_STRING", DefaultLanguageHighlighterColors.STRING)
        val LINE_COMMENT = createTextAttributesKey("GALA_LINE_COMMENT", DefaultLanguageHighlighterColors.LINE_COMMENT)
        val BLOCK_COMMENT = createTextAttributesKey("GALA_BLOCK_COMMENT", DefaultLanguageHighlighterColors.BLOCK_COMMENT)
        val NUMBER = createTextAttributesKey("GALA_NUMBER", DefaultLanguageHighlighterColors.NUMBER)
        val OPERATOR = createTextAttributesKey("GALA_OPERATOR", DefaultLanguageHighlighterColors.OPERATION_SIGN)
        val IDENTIFIER = createTextAttributesKey("GALA_IDENTIFIER", DefaultLanguageHighlighterColors.IDENTIFIER)
        val PARENTHESES = createTextAttributesKey("GALA_PARENTHESES", DefaultLanguageHighlighterColors.PARENTHESES)
        val BRACES = createTextAttributesKey("GALA_BRACES", DefaultLanguageHighlighterColors.BRACES)
        val BRACKETS = createTextAttributesKey("GALA_BRACKETS", DefaultLanguageHighlighterColors.BRACKETS)
        val DOT = createTextAttributesKey("GALA_DOT", DefaultLanguageHighlighterColors.DOT)
        val COMMA = createTextAttributesKey("GALA_COMMA", DefaultLanguageHighlighterColors.COMMA)
        val SEMICOLON = createTextAttributesKey("GALA_SEMICOLON", DefaultLanguageHighlighterColors.SEMICOLON)
        val BAD_CHARACTER = createTextAttributesKey("GALA_BAD_CHARACTER", HighlighterColors.BAD_CHARACTER)

        private val EMPTY_KEYS = emptyArray<TextAttributesKey>()

        // Built-in type names for semantic highlighting in the lexer-level highlighter
        private val BUILTIN_TYPE_NAMES = setOf(
            "any", "bool", "byte", "rune", "error",
            "int", "int8", "int16", "int32", "int64",
            "uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
            "float32", "float64",
            "complex64", "complex128",
            "string"
        )
    }

    override fun getHighlightingLexer(): Lexer {
        val lexer = galaLexer(null)
        return ANTLRLexerAdaptor(GalaLanguage, lexer)
    }

    override fun getTokenHighlights(tokenType: IElementType): Array<TextAttributesKey> {
        if (tokenType !is TokenIElementType) return EMPTY_KEYS

        val ttype = tokenType.antlrTokenType
        return when (ttype) {
            // Keywords
            galaLexer.VAL,
            galaLexer.VAR,
            galaLexer.BIND,
            galaLexer.ALSO,
            galaLexer.FUNC,
            galaLexer.TYPE,
            galaLexer.STRUCT,
            galaLexer.INTERFACE,
            galaLexer.MATCH,
            galaLexer.CASE,
            galaLexer.IF,
            galaLexer.ELSE,
            galaLexer.FOR,
            galaLexer.RANGE,
            galaLexer.RETURN,
            galaLexer.IMPORT,
            galaLexer.PACKAGE,
            galaLexer.SEALED,
            galaLexer.EMBED -> arrayOf(KEYWORD)

            // Strings
            galaLexer.STRING,
            galaLexer.RAW_STRING,
            galaLexer.INTERPOLATED_STRING,
            galaLexer.FORMAT_STRING,
            galaLexer.CHAR_LIT -> arrayOf(STRING)

            // Comments
            galaLexer.COMMENT -> arrayOf(LINE_COMMENT)
            galaLexer.MULTILINE_COMMENT -> arrayOf(BLOCK_COMMENT)

            // Numbers
            galaLexer.INT_LIT,
            galaLexer.FLOAT_LIT -> arrayOf(NUMBER)

            // Identifiers (built-in types get special highlighting)
            galaLexer.IDENTIFIER -> arrayOf(IDENTIFIER)

            else -> EMPTY_KEYS
        }
    }
}
