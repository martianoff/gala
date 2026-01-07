package org.gala.ide.intellij

import com.intellij.psi.tree.IElementType
import com.intellij.psi.tree.TokenSet

interface GalaTypes {
    class GalaElementType(debugName: String) : IElementType(debugName, GalaLanguage)
    class GalaTokenType(debugName: String) : IElementType(debugName, GalaLanguage)

    companion object {
        val KEYWORD = GalaTokenType("KEYWORD")
        val STRING = GalaTokenType("STRING")
        val COMMENT = GalaTokenType("COMMENT")
        val IDENTIFIER = GalaTokenType("IDENTIFIER")
        val NUMBER = GalaTokenType("NUMBER")
        val OPERATOR = GalaTokenType("OPERATOR")
        val BRACKETS = GalaTokenType("BRACKETS")

        val WHITE_SPACE = GalaTokenType("WHITE_SPACE")
        val BAD_CHARACTER = GalaTokenType("BAD_CHARACTER")
    }
}
