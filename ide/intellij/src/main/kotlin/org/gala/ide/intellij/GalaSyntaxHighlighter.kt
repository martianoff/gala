package org.gala.ide.intellij

import com.intellij.lexer.Lexer
import com.intellij.openapi.editor.DefaultLanguageHighlighterColors
import com.intellij.openapi.editor.HighlighterColors
import com.intellij.openapi.editor.colors.TextAttributesKey
import com.intellij.openapi.editor.colors.TextAttributesKey.createTextAttributesKey
import com.intellij.openapi.fileTypes.SyntaxHighlighterBase
import com.intellij.psi.TokenType
import com.intellij.psi.tree.IElementType

class GalaSyntaxHighlighter : SyntaxHighlighterBase() {
    companion object {
        val KEYWORD = createTextAttributesKey("GALA_KEYWORD", DefaultLanguageHighlighterColors.KEYWORD)
        val STRING = createTextAttributesKey("GALA_STRING", DefaultLanguageHighlighterColors.STRING)
        val COMMENT = createTextAttributesKey("GALA_COMMENT", DefaultLanguageHighlighterColors.LINE_COMMENT)
        val NUMBER = createTextAttributesKey("GALA_NUMBER", DefaultLanguageHighlighterColors.NUMBER)
        val OPERATOR = createTextAttributesKey("GALA_OPERATOR", DefaultLanguageHighlighterColors.OPERATION_SIGN)
        val PARENTHESES = createTextAttributesKey("GALA_PARENTHESES", DefaultLanguageHighlighterColors.PARENTHESES)
        val BRACES = createTextAttributesKey("GALA_BRACES", DefaultLanguageHighlighterColors.BRACES)
        val BRACKETS = createTextAttributesKey("GALA_BRACKETS", DefaultLanguageHighlighterColors.BRACKETS)
        val DOT = createTextAttributesKey("GALA_DOT", DefaultLanguageHighlighterColors.DOT)
        val COMMA = createTextAttributesKey("GALA_COMMA", DefaultLanguageHighlighterColors.COMMA)
        val BAD_CHARACTER = createTextAttributesKey("GALA_BAD_CHARACTER", HighlighterColors.BAD_CHARACTER)

        private val KEYWORD_KEYS = arrayOf(KEYWORD)
        private val STRING_KEYS = arrayOf(STRING)
        private val COMMENT_KEYS = arrayOf(COMMENT)
        private val NUMBER_KEYS = arrayOf(NUMBER)
        private val OPERATOR_KEYS = arrayOf(OPERATOR)
        private val PARENTHESES_KEYS = arrayOf(PARENTHESES)
        private val BRACES_KEYS = arrayOf(BRACES)
        private val BRACKETS_KEYS = arrayOf(BRACKETS)
        private val DOT_KEYS = arrayOf(DOT)
        private val COMMA_KEYS = arrayOf(COMMA)
        private val BAD_CHAR_KEYS = arrayOf(BAD_CHARACTER)
        private val EMPTY_KEYS = emptyArray<TextAttributesKey>()
    }

    override fun getHighlightingLexer(): Lexer = GalaLexer()

    override fun getTokenHighlights(tokenType: IElementType): Array<TextAttributesKey> {
        return when (tokenType) {
            GalaTypes.KEYWORD -> KEYWORD_KEYS
            GalaTypes.STRING -> STRING_KEYS
            GalaTypes.COMMENT -> COMMENT_KEYS
            GalaTypes.NUMBER -> NUMBER_KEYS
            GalaTypes.OPERATOR -> OPERATOR_KEYS
            GalaTypes.LPAREN, GalaTypes.RPAREN -> PARENTHESES_KEYS
            GalaTypes.LBRACE, GalaTypes.RBRACE -> BRACES_KEYS
            GalaTypes.LBRACKET, GalaTypes.RBRACKET -> BRACKETS_KEYS
            GalaTypes.DOT -> DOT_KEYS
            GalaTypes.COMMA -> COMMA_KEYS
            GalaTypes.BAD_CHARACTER -> BAD_CHAR_KEYS
            else -> EMPTY_KEYS
        }
    }
}
