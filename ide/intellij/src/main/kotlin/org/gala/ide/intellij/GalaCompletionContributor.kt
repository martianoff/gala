package org.gala.ide.intellij

import com.intellij.codeInsight.completion.*
import com.intellij.codeInsight.lookup.LookupElementBuilder
import com.intellij.patterns.PlatformPatterns
import com.intellij.psi.PsiNameIdentifierOwner
import com.intellij.psi.util.PsiTreeUtil
import com.intellij.util.ProcessingContext
import org.gala.ide.intellij.psi.*

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
            "Immutable", "HashMap"
        )

        private val POSTFIX_TEMPLATES = mapOf(
            "match" to "match {\n    case \$END\$ => \n}",
            "Map" to "Map((\$PARAM\$) => \$END\$)",
            "Filter" to "Filter((\$PARAM\$) => \$END\$)",
            "FoldLeft" to "FoldLeft(\$INIT\$, (\$ACC\$, \$PARAM\$) => \$END\$)",
            "ForEach" to "ForEach((\$PARAM\$) => \$END\$)",
            "FlatMap" to "FlatMap((\$PARAM\$) => \$END\$)",
            "Get" to "Get()",
            "IsDefined" to "IsDefined()",
            "IsEmpty" to "IsEmpty()",
            "GetOrElse" to "GetOrElse(\$DEFAULT\$)",
        )
    }

    init {
        // Basic keyword and type completion
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

                    // In-scope declarations from the current file
                    addInScopeDeclarations(parameters, result)

                    // Common methods after dot
                    addPostfixMethods(parameters, result)
                }
            }
        )
    }

    private fun addInScopeDeclarations(parameters: CompletionParameters, result: CompletionResultSet) {
        val file = parameters.originalFile
        val declarations = file.children.flatMap { child ->
            if (child is GalaPsiNode) collectDeclarations(child) else emptyList()
        }
        for (decl in declarations) {
            if (decl is PsiNameIdentifierOwner) {
                val name = decl.name ?: continue
                val typeText = when (decl) {
                    is FunctionDeclarationNode -> "func"
                    is TypeDeclarationNode -> "type"
                    is SealedTypeDeclarationNode -> "sealed type"
                    is SealedCaseNode -> "case"
                    is ValDeclarationNode -> "val"
                    is VarDeclarationNode -> "var"
                    is StructShorthandDeclarationNode -> "struct"
                    is MethodSpecNode -> "method"
                    else -> null
                }
                val builder = LookupElementBuilder.create(name)
                if (typeText != null) {
                    result.addElement(builder.withTypeText(typeText))
                } else {
                    result.addElement(builder)
                }
            }
        }
    }

    private fun addPostfixMethods(parameters: CompletionParameters, result: CompletionResultSet) {
        // Check if we're completing after a dot by looking at text before caret
        val offset = parameters.offset
        val text = parameters.editor.document.text
        // Walk backwards past whitespace to find a dot
        var i = offset - 1
        while (i >= 0 && text[i].isLetterOrDigit()) i--
        if (i >= 0 && text[i] == '.') {
            for ((method, _) in POSTFIX_TEMPLATES) {
                result.addElement(
                    LookupElementBuilder.create(method).withTypeText("method")
                )
            }
        }
    }

    private fun collectDeclarations(element: com.intellij.psi.PsiElement): List<PsiNameIdentifierOwner> {
        val result = mutableListOf<PsiNameIdentifierOwner>()
        if (element is PsiNameIdentifierOwner) {
            result.add(element)
        }
        for (child in element.children) {
            if (child is GalaPsiNode && child !is BlockNode) {
                result.addAll(collectDeclarations(child))
            }
        }
        return result
    }
}
