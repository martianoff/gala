package org.gala.ide.intellij

import com.intellij.lang.cacheBuilder.DefaultWordsScanner
import com.intellij.lang.cacheBuilder.WordsScanner
import com.intellij.lang.findUsages.FindUsagesProvider
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiNameIdentifierOwner
import com.intellij.psi.tree.TokenSet
import org.antlr.intellij.adaptor.lexer.ANTLRLexerAdaptor
import org.gala.ide.intellij.parser.galaLexer
import org.gala.ide.intellij.psi.*

class GalaFindUsagesProvider : FindUsagesProvider {

    override fun getWordsScanner(): WordsScanner {
        return DefaultWordsScanner(
            ANTLRLexerAdaptor(GalaLanguage, galaLexer(null)),
            TokenSet.create(GalaTokenTypes.ID),
            GalaTokenTypes.COMMENTS,
            GalaTokenTypes.STRINGS
        )
    }

    override fun canFindUsagesFor(psiElement: PsiElement): Boolean {
        return psiElement is PsiNameIdentifierOwner
    }

    override fun getHelpId(psiElement: PsiElement): String? = null

    override fun getType(element: PsiElement): String {
        return when (element) {
            is FunctionDeclarationNode -> "function"
            is TypeDeclarationNode -> "type"
            is SealedTypeDeclarationNode -> "sealed type"
            is SealedCaseNode -> "case"
            is ValDeclarationNode -> "value"
            is VarDeclarationNode -> "variable"
            is StructShorthandDeclarationNode -> "struct"
            is MethodSpecNode -> "method"
            else -> "symbol"
        }
    }

    override fun getDescriptiveName(element: PsiElement): String {
        return (element as? PsiNameIdentifierOwner)?.name ?: ""
    }

    override fun getNodeText(element: PsiElement, useFullName: Boolean): String {
        if (element is PsiNameIdentifierOwner) {
            val name = element.name ?: return ""
            if (useFullName && element is FunctionDeclarationNode) {
                // Show signature for functions
                val text = element.text.lines().firstOrNull() ?: return name
                return text.substringBefore("{").trim()
            }
            return name
        }
        return ""
    }
}
