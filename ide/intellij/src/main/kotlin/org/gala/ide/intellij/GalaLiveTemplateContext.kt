package org.gala.ide.intellij

import com.intellij.codeInsight.template.TemplateActionContext
import com.intellij.codeInsight.template.TemplateContextType

/**
 * Defines when GALA live templates are available.
 * Templates are shown in .gala files when the caret is in a code context.
 */
class GalaLiveTemplateContext : TemplateContextType("GALA", "GALA") {
    override fun isInContext(context: TemplateActionContext): Boolean {
        return context.file.name.endsWith(".gala")
    }
}
