package org.gala.ide.intellij

import com.intellij.lang.BracePair
import com.intellij.lang.PairedBraceMatcher
import com.intellij.psi.PsiFile
import com.intellij.psi.tree.IElementType
import org.antlr.intellij.adaptor.lexer.TokenIElementType
import org.gala.ide.intellij.psi.GalaTokenTypes

class GalaPairedBraceMatcher : PairedBraceMatcher {
    companion object {
        /**
         * Find a TokenIElementType by its literal display name from the ANTLR vocabulary.
         * ANTLR assigns token types to inline literals like '{', '(', '['.
         */
        private fun findTokenByLiteral(literal: String): TokenIElementType? {
            return GalaTokenTypes.tokenIElementTypes.find { tokenType ->
                tokenType.antlrTokenType >= 0 &&
                    org.gala.ide.intellij.parser.galaLexer.VOCABULARY
                        .getLiteralName(tokenType.antlrTokenType) == "'$literal'"
            }
        }

        private val LBRACE = findTokenByLiteral("{")
        private val RBRACE = findTokenByLiteral("}")
        private val LPAREN = findTokenByLiteral("(")
        private val RPAREN = findTokenByLiteral(")")
        private val LBRACKET = findTokenByLiteral("[")
        private val RBRACKET = findTokenByLiteral("]")
    }

    override fun getPairs(): Array<BracePair> {
        val pairs = mutableListOf<BracePair>()
        if (LBRACE != null && RBRACE != null) pairs.add(BracePair(LBRACE, RBRACE, true))
        if (LPAREN != null && RPAREN != null) pairs.add(BracePair(LPAREN, RPAREN, false))
        if (LBRACKET != null && RBRACKET != null) pairs.add(BracePair(LBRACKET, RBRACKET, false))
        return pairs.toTypedArray()
    }

    override fun isPairedBracesAllowedBeforeType(lbraceType: IElementType, contextType: IElementType?): Boolean = true

    override fun getCodeConstructStart(file: PsiFile?, openingBraceOffset: Int): Int = openingBraceOffset
}
