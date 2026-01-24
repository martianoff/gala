package org.gala.ide.intellij

import com.intellij.codeInsight.completion.*
import com.intellij.codeInsight.lookup.LookupElementBuilder
import com.intellij.patterns.PlatformPatterns
import com.intellij.util.ProcessingContext

class GalaCompletionContributor : CompletionContributor() {
    private val keywords = listOf(
        // Declaration keywords
        "package", "import", "val", "var", "func", "type", "struct", "interface",
        // Control flow
        "if", "else", "for", "range", "return", "match", "case",
        "break", "continue", "defer", "go", "select", "switch", "default",
        // Literals
        "true", "false", "nil",
        // Type keywords
        "map", "chan"
    )

    private val builtinTypes = listOf(
        "any", "bool", "byte", "rune", "error",
        "int", "int8", "int16", "int32", "int64",
        "uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
        "float32", "float64",
        "complex64", "complex128",
        "string"
    )

    private val stdTypes = listOf(
        // Option monad
        "Option", "Some", "None",
        // Either monad
        "Either", "Left", "Right",
        // Tuple
        "Tuple",
        // Collections
        "List", "Array", "Seq"
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
                    // Keywords (bold)
                    for (keyword in keywords) {
                        result.addElement(LookupElementBuilder.create(keyword).bold())
                    }
                    // Built-in types
                    for (type in builtinTypes) {
                        result.addElement(
                            LookupElementBuilder.create(type)
                                .withTypeText("builtin type")
                        )
                    }
                    // Standard library types
                    for (type in stdTypes) {
                        result.addElement(
                            LookupElementBuilder.create(type)
                                .withTypeText("std")
                        )
                    }
                }
            }
        )
    }
}
