package org.gala.ide.intellij

import com.intellij.codeInsight.daemon.LineMarkerInfo
import com.intellij.codeInsight.daemon.LineMarkerProvider
import com.intellij.icons.AllIcons
import com.intellij.openapi.editor.markup.GutterIconRenderer
import com.intellij.psi.PsiElement
import org.antlr.intellij.adaptor.lexer.TokenIElementType
import org.gala.ide.intellij.parser.galaLexer
import org.gala.ide.intellij.psi.*

/**
 * Provides gutter icons for runnable GALA constructs:
 * - Green play icon on `func main()` declarations
 * - Test icon on test functions (func TestXxx)
 */
class GalaLineMarkerProvider : LineMarkerProvider {

    override fun getLineMarkerInfo(element: PsiElement): LineMarkerInfo<*>? {
        // Only mark leaf IDENTIFIER tokens to avoid duplicate markers
        val elementType = element.node?.elementType
        if (elementType !is TokenIElementType || elementType.antlrTokenType != galaLexer.IDENTIFIER) {
            return null
        }

        val parent = element.parent ?: return null
        // Check if this is a function name identifier
        val grandparent = parent.parent
        if (grandparent !is FunctionDeclarationNode) return null
        if (grandparent.nameIdentifier !== parent) return null

        val funcName = element.text

        return when {
            funcName == "main" -> createRunMarker(element, "Run main()", AllIcons.RunConfigurations.TestState.Run)
            funcName.startsWith("Test") -> createRunMarker(element, "Run $funcName", AllIcons.RunConfigurations.TestState.Run)
            else -> null
        }
    }

    private fun createRunMarker(element: PsiElement, tooltip: String, icon: javax.swing.Icon): LineMarkerInfo<PsiElement> {
        return LineMarkerInfo(
            element,
            element.textRange,
            icon,
            { tooltip },
            null,
            GutterIconRenderer.Alignment.LEFT,
            { tooltip }
        )
    }
}
