package org.gala.ide.intellij

import com.intellij.psi.tree.IElementType
import com.intellij.psi.tree.TokenSet

interface GalaTypes {
    class GalaElementType(debugName: String) : IElementType(debugName, GalaLanguage)
    class GalaTokenType(debugName: String) : IElementType(debugName, GalaLanguage)

    companion object {
        val KEYWORD = GalaTokenType("KEYWORD")
        val TYPE = GalaTokenType("TYPE")
        val STRING = GalaTokenType("STRING")
        val COMMENT = GalaTokenType("COMMENT")
        val IDENTIFIER = GalaTokenType("IDENTIFIER")
        val NUMBER = GalaTokenType("NUMBER")
        val OPERATOR = GalaTokenType("OPERATOR")

        val LPAREN = GalaTokenType("LPAREN")
        val RPAREN = GalaTokenType("RPAREN")
        val LBRACE = GalaTokenType("LBRACE")
        val RBRACE = GalaTokenType("RBRACE")
        val LBRACKET = GalaTokenType("LBRACKET")
        val RBRACKET = GalaTokenType("RBRACKET")

        val DOT = GalaTokenType("DOT")
        val COMMA = GalaTokenType("COMMA")

        val WHITE_SPACE = GalaTokenType("WHITE_SPACE")
        val BAD_CHARACTER = GalaTokenType("BAD_CHARACTER")

        val BLOCK = GalaElementType("BLOCK")
        val FUNCTION = GalaElementType("FUNCTION")
        val TYPE_DECL = GalaElementType("TYPE_DECL")
    }
}
