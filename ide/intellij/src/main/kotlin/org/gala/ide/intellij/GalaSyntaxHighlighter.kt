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
        val BAD_CHARACTER = createTextAttributesKey("GALA_BAD_CHARACTER", HighlighterColors.BAD_CHARACTER)

        private val KEYWORD_KEYS = arrayOf(KEYWORD)
        private val STRING_KEYS = arrayOf(STRING)
        private val COMMENT_KEYS = arrayOf(COMMENT)
        private val NUMBER_KEYS = arrayOf(NUMBER)
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
            GalaTypes.BAD_CHARACTER -> BAD_CHAR_KEYS
            else -> EMPTY_KEYS
        }
    }
}
