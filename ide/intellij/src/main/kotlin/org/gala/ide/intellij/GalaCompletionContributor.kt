package org.gala.ide.intellij

import com.intellij.codeInsight.completion.*
import com.intellij.codeInsight.lookup.LookupElementBuilder
import com.intellij.patterns.PlatformPatterns
import com.intellij.util.ProcessingContext

class GalaCompletionContributor : CompletionContributor() {
    companion object {
        private val DECLARATION_KEYWORDS = listOf(
            "package", "import", "val", "var", "func", "type", "struct", "interface", "sealed", "embed"
        )

        private val CONTROL_KEYWORDS = listOf(
            "if", "else", "for", "range", "return", "match", "case",
            "break", "continue", "defer", "go", "select", "switch", "default"
        )

        private val LITERAL_KEYWORDS = listOf("true", "false", "nil")

        private val TYPE_KEYWORDS = listOf("map", "chan")

        private val ALL_KEYWORDS = DECLARATION_KEYWORDS + CONTROL_KEYWORDS + LITERAL_KEYWORDS + TYPE_KEYWORDS

        private val BUILTIN_TYPES = listOf(
            "any", "bool", "byte", "rune", "error",
            "int", "int8", "int16", "int32", "int64",
            "uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
            "float32", "float64",
            "complex64", "complex128",
            "string"
        )

        private val STD_TYPES = listOf(
            "Option", "Some", "None",
            "Either", "Left", "Right",
            "Try", "Success", "Failure",
            "Tuple",
            "List", "Array", "Seq",
            "Immutable"
        )
    }

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
                    for (keyword in ALL_KEYWORDS) {
                        result.addElement(LookupElementBuilder.create(keyword).bold())
                    }
                    for (type in BUILTIN_TYPES) {
                        result.addElement(
                            LookupElementBuilder.create(type).withTypeText("builtin type")
                        )
                    }
                    for (type in STD_TYPES) {
                        result.addElement(
                            LookupElementBuilder.create(type).withTypeText("std")
                        )
                    }
                }
            }
        )
    }
}
