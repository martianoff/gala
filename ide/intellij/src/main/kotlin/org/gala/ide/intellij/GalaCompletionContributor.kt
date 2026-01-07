package org.gala.ide.intellij

import com.intellij.codeInsight.completion.*
import com.intellij.codeInsight.lookup.LookupElementBuilder
import com.intellij.patterns.PlatformPatterns
import com.intellij.util.ProcessingContext

class GalaCompletionContributor : CompletionContributor() {
    private val keywords = listOf(
        "package", "import", "val", "var", "func", "type", "struct", "interface",
        "if", "else", "for", "range", "return", "match", "case",
        "true", "false", "nil"
    )

    init {
        extend(
            CompletionType.BASIC,
            PlatformPatterns.psiElement(),
            object : CompletionProvider<CompletionParameters>() {
                override fun addCompletions(
                    parameters: CompletionParameters,
                    context: ProcessingContext,
                    result: CompletionResultSet
                ) {
                    for (keyword in keywords) {
                        result.addElement(LookupElementBuilder.create(keyword).bold())
                    }
                }
            }
        )
    }
}
