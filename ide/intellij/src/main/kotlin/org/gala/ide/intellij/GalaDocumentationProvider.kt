package org.gala.ide.intellij

import com.intellij.lang.documentation.AbstractDocumentationProvider
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiNameIdentifierOwner
import org.gala.ide.intellij.psi.*

/**
 * Quick documentation provider for GALA (Ctrl+Q).
 * Shows declaration info for functions, types, sealed types, vals, vars.
 */
class GalaDocumentationProvider : AbstractDocumentationProvider() {

    override fun generateDoc(element: PsiElement?, originalElement: PsiElement?): String? {
        if (element == null) return null

        // If the element is an identifier that resolves to a declaration, show the declaration
        val target = when {
            element is PsiNameIdentifierOwner -> element
            element is IdentifierNode -> {
                val ref = element.reference
                ref?.resolve() as? PsiNameIdentifierOwner
            }
            else -> null
        } ?: return null

        return buildDoc(target)
    }

    override fun getQuickNavigateInfo(element: PsiElement?, originalElement: PsiElement?): String? {
        if (element is PsiNameIdentifierOwner) {
            return buildQuickInfo(element)
        }
        return null
    }

    private fun buildDoc(element: PsiNameIdentifierOwner): String {
        val name = element.name ?: "unknown"
        val kind = getKind(element)
        val signature = getSignature(element)
        val file = element.containingFile?.name ?: ""

        return buildString {
            append("<div class='definition'><pre>")
            append("<b>$kind</b> $signature")
            append("</pre></div>")
            if (file.isNotEmpty()) {
                append("<div class='content'>")
                append("<p>Defined in <code>$file</code></p>")
                append("</div>")
            }
        }
    }

    private fun buildQuickInfo(element: PsiNameIdentifierOwner): String {
        val kind = getKind(element)
        val signature = getSignature(element)
        return "$kind $signature"
    }

    private fun getKind(element: PsiNameIdentifierOwner): String {
        return when (element) {
            is FunctionDeclarationNode -> "func"
            is TypeDeclarationNode -> "type"
            is SealedTypeDeclarationNode -> "sealed type"
            is SealedCaseNode -> "case"
            is ValDeclarationNode -> "val"
            is VarDeclarationNode -> "var"
            is StructShorthandDeclarationNode -> "struct"
            is MethodSpecNode -> "method"
            else -> "declaration"
        }
    }

    private fun getSignature(element: PsiNameIdentifierOwner): String {
        return when (element) {
            is FunctionDeclarationNode -> {
                val text = element.text
                text.lines().firstOrNull()?.substringBefore("{")?.trim()
                    ?.removePrefix("func ")
                    ?: element.name ?: ""
            }
            is SealedTypeDeclarationNode -> {
                val text = element.text
                text.lines().firstOrNull()?.substringBefore("{")?.trim()
                    ?.removePrefix("sealed ")?.removePrefix("type ")
                    ?: element.name ?: ""
            }
            is SealedCaseNode -> {
                val text = element.text.trim()
                text.removePrefix("case ")
            }
            is TypeDeclarationNode -> {
                val text = element.text
                text.lines().firstOrNull()?.substringBefore("{")?.trim()
                    ?.removePrefix("type ")
                    ?: element.name ?: ""
            }
            is ValDeclarationNode -> {
                val text = element.text.trim()
                text.substringBefore("=").trim()
            }
            is VarDeclarationNode -> {
                val text = element.text.trim()
                text.substringBefore("=").trim()
            }
            else -> element.name ?: ""
        }
    }
}
