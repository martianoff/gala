package org.gala.ide.intellij

import com.intellij.lang.ASTNode
import com.intellij.lang.folding.FoldingBuilderEx
import com.intellij.lang.folding.FoldingDescriptor
import com.intellij.openapi.editor.Document
import com.intellij.openapi.project.DumbAware
import com.intellij.psi.PsiElement
import com.intellij.psi.util.PsiTreeUtil
import com.intellij.openapi.util.TextRange
import com.intellij.extapi.psi.ASTWrapperPsiElement

class GalaFoldingBuilder : FoldingBuilderEx(), DumbAware {
    override fun buildFoldRegions(root: PsiElement, document: Document, quick: Boolean): Array<FoldingDescriptor> {
        val descriptors = mutableListOf<FoldingDescriptor>()
        val blocks = PsiTreeUtil.findChildrenOfType(root, ASTWrapperPsiElement::class.java)

        for (block in blocks) {
            if (block.node.elementType == GalaTypes.BLOCK) {
                val range = block.textRange
                if (range.length > 2) { // only fold if not empty {}
                    descriptors.add(FoldingDescriptor(block.node, TextRange(range.startOffset + 1, range.endOffset - 1)))
                }
            }
        }
        return descriptors.toTypedArray()
    }

    override fun getPlaceholderText(node: ASTNode): String = "..."

    override fun isCollapsedByDefault(node: ASTNode): Boolean = false
}
