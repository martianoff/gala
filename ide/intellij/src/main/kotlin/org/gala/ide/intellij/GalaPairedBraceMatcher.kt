package org.gala.ide.intellij

import com.intellij.lang.BracePair
import com.intellij.lang.PairedBraceMatcher
import com.intellij.psi.PsiFile
import com.intellij.psi.tree.IElementType

class GalaPairedBraceMatcher : PairedBraceMatcher {
    override fun getPairs(): Array<BracePair> = arrayOf(
        BracePair(GalaTypes.LBRACE, GalaTypes.RBRACE, true),
        BracePair(GalaTypes.LPAREN, GalaTypes.RPAREN, false),
        BracePair(GalaTypes.LBRACKET, GalaTypes.RBRACKET, false)
    )

    override fun isPairedBracesAllowedBeforeType(lbraceType: IElementType, contextType: IElementType?): Boolean = true

    override fun getCodeConstructStart(file: PsiFile?, openingBraceOffset: Int): Int = openingBraceOffset
}
