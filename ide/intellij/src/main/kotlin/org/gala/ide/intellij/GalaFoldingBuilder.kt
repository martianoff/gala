package org.gala.ide.intellij

import com.intellij.lang.ASTNode
import com.intellij.lang.folding.FoldingBuilderEx
import com.intellij.lang.folding.FoldingDescriptor
import com.intellij.openapi.editor.Document
import com.intellij.openapi.project.DumbAware
import com.intellij.openapi.util.TextRange
import com.intellij.psi.PsiElement
import com.intellij.psi.util.PsiTreeUtil
import org.gala.ide.intellij.psi.*

class GalaFoldingBuilder : FoldingBuilderEx(), DumbAware {
    override fun buildFoldRegions(root: PsiElement, document: Document, quick: Boolean): Array<FoldingDescriptor> {
        val descriptors = mutableListOf<FoldingDescriptor>()

        PsiTreeUtil.findChildrenOfType(root, GalaPsiNode::class.java).forEach { node ->
            when (node) {
                is BlockNode -> addBraceFold(node, descriptors)
                is SealedTypeDeclarationNode -> addBraceFold(node, descriptors)
                is ImportDeclarationNode -> {
                    // Fold multi-line import blocks
                    val text = node.text
                    if (text.contains("(") && text.contains(")")) {
                        val range = node.textRange
                        if (range.length > 10) {
                            descriptors.add(FoldingDescriptor(node.node, range))
                        }
                    }
                }
            }
        }
        return descriptors.toTypedArray()
    }

    private fun addBraceFold(element: PsiElement, descriptors: MutableList<FoldingDescriptor>) {
        val text = element.text
        val lbrace = text.indexOf('{')
        val rbrace = text.lastIndexOf('}')
        if (lbrace >= 0 && rbrace > lbrace + 1) {
            val start = element.textRange.startOffset + lbrace
            val end = element.textRange.startOffset + rbrace + 1
            descriptors.add(FoldingDescriptor(element.node, TextRange(start, end)))
        }
    }

    override fun getPlaceholderText(node: ASTNode): String = "{...}"

    override fun isCollapsedByDefault(node: ASTNode): Boolean = false
}
