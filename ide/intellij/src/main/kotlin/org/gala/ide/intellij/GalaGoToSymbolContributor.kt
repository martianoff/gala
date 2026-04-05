package org.gala.ide.intellij

import com.intellij.navigation.ChooseByNameContributorEx
import com.intellij.navigation.NavigationItem
import com.intellij.openapi.project.Project
import com.intellij.psi.PsiManager
import com.intellij.psi.PsiNameIdentifierOwner
import com.intellij.psi.search.FilenameIndex
import com.intellij.psi.search.GlobalSearchScope
import com.intellij.psi.util.PsiTreeUtil
import com.intellij.util.Processor
import com.intellij.util.indexing.FindSymbolParameters
import com.intellij.util.indexing.IdFilter
import org.gala.ide.intellij.psi.GalaPsiNode

/**
 * Enables Go to Symbol (Ctrl+Shift+Alt+N) for GALA declarations.
 * Scans all .gala files in the project for named declarations.
 */
class GalaGoToSymbolContributor : ChooseByNameContributorEx {

    override fun processNames(
        processor: Processor<in String>,
        scope: GlobalSearchScope,
        filter: IdFilter?
    ) {
        val project = scope.project ?: return
        for (decl in findAllDeclarations(project, scope)) {
            val name = decl.name ?: continue
            if (name.isNotBlank()) {
                if (!processor.process(name)) return
            }
        }
    }

    override fun processElementsWithName(
        name: String,
        processor: Processor<in NavigationItem>,
        parameters: FindSymbolParameters
    ) {
        for (decl in findAllDeclarations(parameters.project, parameters.searchScope)) {
            if (decl.name == name && decl is NavigationItem) {
                val item: NavigationItem = decl
                if (!processor.process(item)) return
            }
        }
    }

    private fun findAllDeclarations(project: Project, scope: GlobalSearchScope): List<PsiNameIdentifierOwner> {
        val result = mutableListOf<PsiNameIdentifierOwner>()
        val psiManager = PsiManager.getInstance(project)
        val galaFiles = FilenameIndex.getAllFilesByExt(project, "gala", scope)
        for (virtualFile in galaFiles) {
            val psiFile = psiManager.findFile(virtualFile) ?: continue
            val nodes = PsiTreeUtil.findChildrenOfType(psiFile, GalaPsiNode::class.java)
            for (node in nodes) {
                if (node is PsiNameIdentifierOwner) {
                    result.add(node)
                }
            }
        }
        return result
    }
}
