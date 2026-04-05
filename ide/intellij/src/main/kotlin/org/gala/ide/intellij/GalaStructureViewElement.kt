package org.gala.ide.intellij

import com.intellij.ide.structureView.StructureViewTreeElement
import com.intellij.ide.util.treeView.smartTree.TreeElement
import com.intellij.navigation.ItemPresentation
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiNameIdentifierOwner
import com.intellij.psi.util.PsiTreeUtil
import org.gala.ide.intellij.psi.*
import javax.swing.Icon

class GalaStructureViewElement(private val element: PsiElement) : StructureViewTreeElement {
    override fun getValue(): Any = element

    override fun navigate(requestFocus: Boolean) {
        if (element is com.intellij.psi.NavigatablePsiElement) {
            element.navigate(requestFocus)
        }
    }

    override fun canNavigate(): Boolean = element is com.intellij.psi.NavigatablePsiElement
    override fun canNavigateToSource(): Boolean = element is com.intellij.psi.NavigatablePsiElement

    override fun getPresentation(): ItemPresentation {
        return object : ItemPresentation {
            override fun getPresentableText(): String? {
                if (element is GalaFile) return element.name
                if (element is PsiNameIdentifierOwner) {
                    val name = element.name ?: return element.text.lines().firstOrNull()?.trim()
                    return when (element) {
                        is FunctionDeclarationNode -> buildFunctionPresentation(element, name)
                        is SealedTypeDeclarationNode -> "sealed type $name"
                        is SealedCaseNode -> "case $name"
                        is TypeDeclarationNode -> "type $name"
                        is ValDeclarationNode -> "val $name"
                        is VarDeclarationNode -> "var $name"
                        is StructShorthandDeclarationNode -> "struct $name"
                        is MethodSpecNode -> name
                        else -> name
                    }
                }
                return element.text.lines().firstOrNull()?.trim()?.take(60)
            }

            override fun getLocationString(): String? = null

            override fun getIcon(unused: Boolean): Icon? = GalaIcons.FILE
        }
    }

    override fun getChildren(): Array<TreeElement> {
        val children = mutableListOf<TreeElement>()

        if (element is GalaFile) {
            // Show top-level declarations
            collectDeclarations(element, children)
        } else if (element is SealedTypeDeclarationNode) {
            // Show sealed cases
            for (child in element.children) {
                if (child is SealedCaseNode) {
                    children.add(GalaStructureViewElement(child))
                }
            }
        } else if (element is TypeDeclarationNode) {
            // Show method specs in interfaces, fields in structs
            PsiTreeUtil.findChildrenOfType(element, MethodSpecNode::class.java).forEach {
                children.add(GalaStructureViewElement(it))
            }
        }

        return children.toTypedArray()
    }

    private fun collectDeclarations(parent: PsiElement, out: MutableList<TreeElement>) {
        for (child in parent.children) {
            when (child) {
                is FunctionDeclarationNode,
                is TypeDeclarationNode,
                is SealedTypeDeclarationNode,
                is ValDeclarationNode,
                is VarDeclarationNode,
                is StructShorthandDeclarationNode -> out.add(GalaStructureViewElement(child))
                else -> {
                    // Recurse into wrapper nodes (topLevelDeclaration, declaration, etc.)
                    if (child is GalaPsiNode && child !is BlockNode && child !is ImportDeclarationNode) {
                        collectDeclarations(child, out)
                    }
                }
            }
        }
    }

    private fun buildFunctionPresentation(node: FunctionDeclarationNode, name: String): String {
        // Try to extract signature text from the first line
        val text = node.text
        val firstLine = text.lines().firstOrNull() ?: return "func $name"
        val sig = firstLine.substringBefore("{").trim()
        return if (sig.isNotEmpty()) sig else "func $name"
    }
}
