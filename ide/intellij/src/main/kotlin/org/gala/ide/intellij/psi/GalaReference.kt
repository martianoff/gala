package org.gala.ide.intellij.psi

import com.intellij.openapi.util.TextRange
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiNameIdentifierOwner
import com.intellij.psi.PsiReferenceBase
import com.intellij.psi.util.PsiTreeUtil

/**
 * Reference from an identifier usage to its declaration.
 * Enables Go to Definition (Ctrl+B / Ctrl+Click) and Find Usages.
 *
 * Resolution strategy: walk up the PSI tree from the identifier,
 * checking each scope for a matching declaration (innermost scope wins).
 */
class GalaReference(element: PsiElement) : PsiReferenceBase<PsiElement>(element, TextRange(0, element.textLength)) {

    override fun resolve(): PsiElement? {
        val name = element.text
        if (name.isBlank()) return null

        // Walk up the tree looking for declarations that define this name
        var scope: PsiElement? = element.parent
        while (scope != null) {
            val found = findDeclarationInScope(scope, name)
            if (found != null && found !== element && !isNameOf(element, found)) {
                return found
            }
            scope = scope.parent
        }
        return null
    }

    override fun getVariants(): Array<Any> = emptyArray()

    override fun handleElementRename(newElementName: String): PsiElement {
        // TODO: implement rename by replacing ANTLR leaf node
        return element
    }

    /**
     * Check if [ident] is the name identifier OF [declaration] (i.e., the declaration itself).
     * We don't want a declaration's own name to resolve to itself.
     */
    private fun isNameOf(ident: PsiElement, declaration: PsiElement): Boolean {
        if (declaration is PsiNameIdentifierOwner) {
            val nameId = declaration.nameIdentifier
            // Check if ident is the nameIdentifier or a descendant of it
            if (nameId != null) {
                var current: PsiElement? = ident
                while (current != null) {
                    if (current === nameId) return true
                    if (current === declaration) return false
                    current = current.parent
                }
            }
        }
        return false
    }

    companion object {
        /**
         * Search for a declaration with the given name within a scope node.
         * Only checks direct children — does not recurse into nested scopes.
         */
        fun findDeclarationInScope(scope: PsiElement, name: String): PsiElement? {
            // Check all named declarations in this scope
            for (child in scope.children) {
                val found = checkDeclaration(child, name)
                if (found != null) return found

                // Also check inside wrapper nodes (topLevelDeclaration, declaration, statement)
                if (child is GalaPsiNode
                    && child !is BlockNode
                    && child !is FunctionDeclarationNode
                    && child !is TypeDeclarationNode
                    && child !is SealedTypeDeclarationNode
                ) {
                    for (grandchild in child.children) {
                        val found2 = checkDeclaration(grandchild, name)
                        if (found2 != null) return found2
                    }
                }
            }

            // For function declarations, check parameters
            if (scope is FunctionDeclarationNode) {
                val params = PsiTreeUtil.findChildrenOfType(scope, GalaPsiNode::class.java)
                for (param in params) {
                    if (param.node.elementType == GalaTokenTypes.RULE_PARAMETERS) {
                        val paramDecl = findParameterByName(param, name)
                        if (paramDecl != null) return paramDecl
                    }
                }
            }

            // For sealed type declarations, check case names
            if (scope is SealedTypeDeclarationNode) {
                for (child in scope.children) {
                    if (child is SealedCaseNode && child.name == name) {
                        return child
                    }
                }
            }

            return null
        }

        private fun checkDeclaration(element: PsiElement, name: String): PsiElement? {
            return when {
                element is PsiNameIdentifierOwner && element.name == name -> element
                else -> null
            }
        }

        private fun findParameterByName(parametersNode: PsiElement, name: String): PsiElement? {
            // Parameters contain parameterList -> parameter -> identifier
            val allNodes = PsiTreeUtil.findChildrenOfType(parametersNode, GalaPsiNode::class.java)
            for (node in allNodes) {
                if (node.node.elementType == GalaTokenTypes.RULE_IDENTIFIER) {
                    // Check if this identifier is inside a parameter node
                    val parent = node.parent
                    if (parent != null && parent.node.elementType == GalaTokenTypes.ruleIElementTypes[org.gala.ide.intellij.parser.galaParser.RULE_parameter]) {
                        if (node.text == name) return node
                    }
                }
            }
            return null
        }
    }
}
