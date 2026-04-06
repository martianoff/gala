package org.gala.ide.intellij

import com.intellij.codeInspection.*
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiElementVisitor
import com.intellij.psi.PsiNameIdentifierOwner
import org.gala.ide.intellij.psi.*

/**
 * Inspection: detects duplicate declarations in the same scope.
 */
class GalaDuplicateDeclarationInspection : LocalInspectionTool() {
    override fun getGroupDisplayName(): String = "GALA"
    override fun getDisplayName(): String = "Duplicate declaration"
    override fun getShortName(): String = "GalaDuplicateDeclaration"

    override fun buildVisitor(holder: ProblemsHolder, isOnTheFly: Boolean): PsiElementVisitor {
        return object : PsiElementVisitor() {
            override fun visitElement(element: PsiElement) {
                if (element !is GalaFile) return
                val seen = mutableMapOf<String, PsiElement>()
                for (child in element.children) {
                    collectTopLevelNames(child, seen, holder)
                }
            }
        }
    }

    private fun collectTopLevelNames(element: PsiElement, seen: MutableMap<String, PsiElement>, holder: ProblemsHolder) {
        if (element is PsiNameIdentifierOwner) {
            // Skip methods (functions with receivers) — same name on different types is valid
            if (element is FunctionDeclarationNode && isMethodDeclaration(element)) {
                return
            }
            val name = element.name
            if (name != null && name.isNotBlank()) {
                val existing = seen[name]
                if (existing != null) {
                    val nameId = element.nameIdentifier
                    if (nameId != null) {
                        holder.registerProblem(
                            nameId,
                            "'$name' is already declared in this scope",
                            ProblemHighlightType.GENERIC_ERROR
                        )
                    }
                } else {
                    seen[name] = element
                }
            }
        }
        // Recurse into wrapper nodes but not into blocks
        if (element is GalaPsiNode && element !is BlockNode
            && element !is FunctionDeclarationNode
            && element !is TypeDeclarationNode
            && element !is SealedTypeDeclarationNode
        ) {
            for (child in element.children) {
                collectTopLevelNames(child, seen, holder)
            }
        }
    }

    /** Check if a function declaration has a receiver (making it a method). */
    private fun isMethodDeclaration(func: FunctionDeclarationNode): Boolean {
        // A method has a receiver node as a child before the function name
        // In the PSI tree: func (receiver) name(...) -> has RULE_RECEIVER child
        for (child in func.children) {
            if (child.node.elementType == GalaTokenTypes.RULE_RECEIVER) {
                return true
            }
        }
        // Also check the text — methods start with "func ("
        val text = func.text.trimStart()
        if (text.startsWith("func (") || text.startsWith("func(")) {
            // Could be a function with params, but if there's a second '(' it's a method
            val firstParen = text.indexOf('(')
            val closeParen = text.indexOf(')', firstParen + 1)
            if (closeParen > 0 && closeParen < text.length - 1) {
                // Check if there's an identifier after the closing paren
                val afterParen = text.substring(closeParen + 1).trimStart()
                if (afterParen.isNotEmpty() && (afterParen[0].isLetter() || afterParen[0] == '_')) {
                    return true
                }
            }
        }
        return false
    }
}

/**
 * Inspection: warns about mutable variables (var) that could be val.
 * Detects var declarations whose value is never reassigned in the same scope.
 */
class GalaVarCouldBeValInspection : LocalInspectionTool() {
    override fun getGroupDisplayName(): String = "GALA"
    override fun getDisplayName(): String = "'var' could be 'val'"
    override fun getShortName(): String = "GalaVarCouldBeVal"

    override fun buildVisitor(holder: ProblemsHolder, isOnTheFly: Boolean): PsiElementVisitor {
        return object : PsiElementVisitor() {
            override fun visitElement(element: PsiElement) {
                if (element !is VarDeclarationNode) return
                val name = element.name ?: return
                val nameId = element.nameIdentifier ?: return

                // Walk up to find the enclosing scope (function body or file)
                val scope = findEnclosingScope(element) ?: return

                // Check if the variable is reassigned anywhere in the scope
                val isReassigned = hasAssignment(scope, name)
                if (!isReassigned) {
                    holder.registerProblem(
                        nameId,
                        "'var $name' is never reassigned — consider using 'val'",
                        ProblemHighlightType.WEAK_WARNING
                    )
                }
            }
        }
    }

    private fun findEnclosingScope(element: PsiElement): PsiElement? {
        var current = element.parent
        while (current != null) {
            if (current is BlockNode || current is GalaFile) return current
            current = current.parent
        }
        return null
    }

    private fun hasAssignment(scope: PsiElement, name: String): Boolean {
        for (child in scope.children) {
            if (child is GalaPsiNode) {
                // Check for assignment: name = ... or name += ... etc.
                val text = child.text
                if (text.startsWith("$name ") && (text.contains("=") && !text.contains("=="))) {
                    // Rough heuristic — assignment detected
                    return true
                }
                if (hasAssignment(child, name)) return true
            }
        }
        return false
    }
}
