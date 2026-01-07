package org.gala.ide.intellij

import com.intellij.ide.structureView.StructureViewTreeElement
import com.intellij.ide.util.treeView.smartTree.TreeElement
import com.intellij.navigation.ItemPresentation
import com.intellij.psi.PsiElement
import com.intellij.psi.util.PsiTreeUtil
import javax.swing.Icon
import com.intellij.extapi.psi.ASTWrapperPsiElement

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

                val type = element.node.elementType
                if (type == GalaTypes.FUNCTION || type == GalaTypes.TYPE_DECL) {
                    // For now, let's just return the first few words of the text
                    val text = element.text
                    val firstLine = text.split("\n")[0].trim()
                    return firstLine.substringBefore("{").trim()
                }
                return element.text
            }

            override fun getLocationString(): String? = null

            override fun getIcon(unused: Boolean): Icon? {
                val type = element.node.elementType
                return when (type) {
                    GalaTypes.FUNCTION -> GalaIcons.FILE // Should be a function icon
                    GalaTypes.TYPE_DECL -> GalaIcons.FILE // Should be a type icon
                    else -> GalaIcons.FILE
                }
            }
        }
    }

    override fun getChildren(): Array<TreeElement> {
        if (element is GalaFile) {
            val children = mutableListOf<TreeElement>()
            for (child in element.children) {
                if (child.node.elementType == GalaTypes.FUNCTION || child.node.elementType == GalaTypes.TYPE_DECL) {
                    children.add(GalaStructureViewElement(child))
                }
            }
            return children.toTypedArray()
        }
        return emptyArray()
    }
}
