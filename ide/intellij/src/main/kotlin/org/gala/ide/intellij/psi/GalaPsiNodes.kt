package org.gala.ide.intellij.psi

import com.intellij.lang.ASTNode
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiNameIdentifierOwner
import com.intellij.psi.PsiReference
import org.antlr.intellij.adaptor.psi.ANTLRPsiNode

/**
 * Base PSI node for GALA elements backed by ANTLR parse tree nodes.
 */
open class GalaPsiNode(node: ASTNode) : ANTLRPsiNode(node)

/**
 * PSI node for identifier references (usages, not declarations).
 * Provides a GalaReference for Go to Definition and Find Usages.
 */
class IdentifierNode(node: ASTNode) : GalaPsiNode(node) {
    override fun getReference(): PsiReference? {
        // Only provide a reference if this identifier is a usage, not a declaration name.
        // A declaration name is the direct identifier child of a PsiNameIdentifierOwner.
        val parent = parent ?: return null
        if (parent is PsiNameIdentifierOwner && parent.nameIdentifier === this) {
            return null
        }
        return GalaReference(this)
    }
}

/**
 * PSI node for function declarations.
 * Supports named identification for navigation and find usages.
 */
class FunctionDeclarationNode(node: ASTNode) : GalaPsiNode(node), PsiNameIdentifierOwner {
    override fun getName(): String? = nameIdentifier?.text

    override fun getNameIdentifier(): PsiElement? {
        // Walk children to find the function name identifier
        // Skip receiver if present: func (r Type) name(...)
        for (child in children) {
            val type = child.node.elementType
            if (type == GalaTokenTypes.RULE_RECEIVER) continue
            if (type == GalaTokenTypes.RULE_IDENTIFIER) return child
        }
        return null
    }

    override fun setName(name: String): PsiElement = this
}

/**
 * PSI node for type declarations (struct, interface, alias).
 */
class TypeDeclarationNode(node: ASTNode) : GalaPsiNode(node), PsiNameIdentifierOwner {
    override fun getName(): String? = nameIdentifier?.text

    override fun getNameIdentifier(): PsiElement? {
        for (child in children) {
            if (child.node.elementType == GalaTokenTypes.RULE_IDENTIFIER) return child
        }
        return null
    }

    override fun setName(name: String): PsiElement = this
}

/**
 * PSI node for sealed type declarations.
 */
class SealedTypeDeclarationNode(node: ASTNode) : GalaPsiNode(node), PsiNameIdentifierOwner {
    override fun getName(): String? = nameIdentifier?.text

    override fun getNameIdentifier(): PsiElement? {
        for (child in children) {
            if (child.node.elementType == GalaTokenTypes.RULE_IDENTIFIER) return child
        }
        return null
    }

    override fun setName(name: String): PsiElement = this

    fun getCases(): List<PsiElement> {
        return children.filter { it.node.elementType == GalaTokenTypes.RULE_SEALED_CASE }
    }
}

/**
 * PSI node for sealed case.
 */
class SealedCaseNode(node: ASTNode) : GalaPsiNode(node), PsiNameIdentifierOwner {
    override fun getName(): String? = nameIdentifier?.text

    override fun getNameIdentifier(): PsiElement? {
        for (child in children) {
            if (child.node.elementType == GalaTokenTypes.RULE_IDENTIFIER) return child
        }
        return null
    }

    override fun setName(name: String): PsiElement = this
}

/**
 * PSI node for val declarations.
 */
class ValDeclarationNode(node: ASTNode) : GalaPsiNode(node), PsiNameIdentifierOwner {
    override fun getName(): String? = nameIdentifier?.text

    override fun getNameIdentifier(): PsiElement? {
        for (child in children) {
            if (child.node.elementType == GalaTokenTypes.RULE_IDENTIFIER) return child
        }
        return null
    }

    override fun setName(name: String): PsiElement = this
}

/**
 * PSI node for var declarations.
 */
class VarDeclarationNode(node: ASTNode) : GalaPsiNode(node), PsiNameIdentifierOwner {
    override fun getName(): String? = nameIdentifier?.text

    override fun getNameIdentifier(): PsiElement? {
        for (child in children) {
            if (child.node.elementType == GalaTokenTypes.RULE_IDENTIFIER) return child
        }
        return null
    }

    override fun setName(name: String): PsiElement = this
}

/**
 * PSI node for struct shorthand declarations.
 */
class StructShorthandDeclarationNode(node: ASTNode) : GalaPsiNode(node), PsiNameIdentifierOwner {
    override fun getName(): String? = nameIdentifier?.text

    override fun getNameIdentifier(): PsiElement? {
        for (child in children) {
            if (child.node.elementType == GalaTokenTypes.RULE_IDENTIFIER) return child
        }
        return null
    }

    override fun setName(name: String): PsiElement = this
}

/**
 * PSI node for import declarations.
 */
class ImportDeclarationNode(node: ASTNode) : GalaPsiNode(node)

/**
 * PSI node for blocks.
 */
class BlockNode(node: ASTNode) : GalaPsiNode(node)

/**
 * PSI node for method spec in interface.
 */
class MethodSpecNode(node: ASTNode) : GalaPsiNode(node), PsiNameIdentifierOwner {
    override fun getName(): String? = nameIdentifier?.text

    override fun getNameIdentifier(): PsiElement? {
        for (child in children) {
            if (child.node.elementType == GalaTokenTypes.RULE_IDENTIFIER) return child
        }
        return null
    }

    override fun setName(name: String): PsiElement = this
}
