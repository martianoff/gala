package org.gala.ide.intellij.psi

import com.intellij.extapi.psi.ASTWrapperPsiElement
import com.intellij.lang.ASTNode
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiNameIdentifierOwner
import com.intellij.psi.PsiReference
import com.intellij.psi.util.PsiUtilCore

/**
 * Base PSI node for GALA elements backed by ANTLR parse tree nodes.
 *
 * Extends [ASTWrapperPsiElement] directly instead of the ANTLR adaptor's
 * `ANTLRPsiNode`. The adaptor routes `getChildren()` and `getContext()` through
 * `org.antlr.intellij.adaptor.psi.Trees` / `SymtabUtils`, and `Trees` can no
 * longer be loaded: it holds an anonymous subclass of `WriteCommandAction`,
 * which recent IntelliJ platforms made final, so the JVM rejects the class with
 * `IncompatibleClassChangeError` and every PSI walk (go to definition,
 * annotator, inspections, structure view) dies. The only adaptor behaviour the
 * plugin actually relies on is reimplemented below.
 */
open class GalaPsiNode(node: ASTNode) : ASTWrapperPsiElement(node) {
    /**
     * All children, token leaves included.
     *
     * The platform default keeps composite nodes only; GALA declarations locate
     * their name identifier and keywords by walking this list, so leaves must
     * stay visible.
     */
    override fun getChildren(): Array<PsiElement> {
        var child: PsiElement? = firstChild ?: return PsiElement.EMPTY_ARRAY
        val result = ArrayList<PsiElement>()
        while (child != null) {
            result.add(child)
            child = child.nextSibling
        }
        return PsiUtilCore.toPsiElementArray(result)
    }
}

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
 * PSI node for bind declarations (monadic do-notation: `bind n = ...`).
 * The bound name behaves exactly like a val: it is a name-owner so it is
 * clickable (go-to-definition), find-usages/rename anchor, and gets a tooltip.
 */
class BindDeclarationNode(node: ASTNode) : GalaPsiNode(node), PsiNameIdentifierOwner {
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
 * PSI node for also declarations (monadic do-notation: `also n = ...`).
 * Behaves exactly like a bind/val bound name.
 */
class AlsoDeclarationNode(node: ASTNode) : GalaPsiNode(node), PsiNameIdentifierOwner {
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
 * PSI node for use declarations (scoped-resource binding: `use x = acquire`).
 * The bound name behaves exactly like a val: it is a name-owner so it is
 * clickable (go-to-definition), find-usages/rename anchor, and gets a tooltip.
 */
class UseDeclarationNode(node: ASTNode) : GalaPsiNode(node), PsiNameIdentifierOwner {
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
