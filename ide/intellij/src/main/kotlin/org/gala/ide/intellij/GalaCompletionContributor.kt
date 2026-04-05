package org.gala.ide.intellij

import com.intellij.codeInsight.completion.*
import com.intellij.codeInsight.lookup.LookupElementBuilder
import com.intellij.patterns.PlatformPatterns
import com.intellij.psi.PsiNameIdentifierOwner
import com.intellij.psi.util.PsiTreeUtil
import com.intellij.util.ProcessingContext
import org.gala.ide.intellij.psi.*

/**
 * Completion contributor for GALA.
 *
 * SYNC NOTE: These lists must stay in sync with the grammar and std library.
 * See ide/intellij/SYNC.md for authoritative sources.
 * Run /gala-ide-sync to verify and fix.
 */
class GalaCompletionContributor : CompletionContributor() {
    companion object {
        // Source: internal/parser/grammar/gala.g4 — named lexer tokens
        private val DECLARATION_KEYWORDS = listOf(
            "package", "import", "val", "var", "func", "type", "struct", "interface", "sealed", "embed"
        )

        // Source: internal/parser/grammar/gala.g4 — inline keyword literals in parser rules
        private val CONTROL_KEYWORDS = listOf(
            "if", "else", "for", "range", "return", "match", "case"
        )

        // Source: internal/parser/grammar/gala.g4 — literal rule
        private val LITERAL_KEYWORDS = listOf("true", "false", "nil")

        // Source: internal/parser/grammar/gala.g4 — type rule
        private val TYPE_KEYWORDS = listOf("map")

        private val ALL_KEYWORDS = DECLARATION_KEYWORDS + CONTROL_KEYWORDS + LITERAL_KEYWORDS + TYPE_KEYWORDS

        // Source: internal/transpiler/types.go — IsPrimitiveType()
        private val BUILTIN_TYPES = listOf(
            "any", "bool", "byte", "rune", "error",
            "int", "int8", "int16", "int32", "int64",
            "uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
            "float32", "float64",
            "complex64", "complex128",
            "string"
        )

        // Source: std/*.gala — sealed types, types, and sealed case constructors
        private val STD_TYPES = listOf(
            // Sealed types
            "Option", "Either", "Try",
            // Sealed case constructors
            "Some", "None", "Left", "Right", "Success", "Failure",
            // Struct types
            "Tuple", "Immutable", "ConstPtr", "Void",
            // Interface types
            "Seq", "Traversable", "Iterable", "Hashable", "Ordered",
            // Error types
            "NoSuchElementError",
        )

        // Source: collection_immutable/*.gala, collection_mutable/*.gala
        private val COLLECTION_TYPES = listOf(
            "Array", "List", "HashMap", "HashSet", "TreeMap", "TreeSet"
        )

        // Source: std/*.gala — public methods on std types
        // These are suggested after "." for dot-completion
        private val DOT_METHODS = listOf(
            // Option methods
            "Get", "GetOrElse", "IsDefined", "IsEmpty", "OrElse",
            "OnSome", "OnNone",
            // Either methods
            "IsLeft", "IsRight", "GetLeft", "GetRight", "Swap",
            "OnLeft", "OnRight",
            // Try methods
            "IsSuccess", "IsFailure", "Recover", "RecoverWith",
            "ToOption", "ToEither",
            // Shared monadic methods (Option, Either, Try)
            "Map", "FlatMap", "Filter", "ForEach", "Fold",
            // Iterable/Traversable methods
            "Head", "Last", "Find", "Exists", "ForAll", "Count",
            "FilterNot", "Take", "Drop", "Size", "NonEmpty",
            "MkString", "Concat",
            // Immutable
            "Set",
            // Language keyword (postfix on expressions)
            "match",
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
                    for (type in COLLECTION_TYPES) {
                        result.addElement(
                            LookupElementBuilder.create(type).withTypeText("collection")
                        )
                    }

                    // In-scope declarations from the current file
                    addInScopeDeclarations(parameters, result)

                    // Dot-completion methods
                    addDotMethods(parameters, result)
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

    private fun addDotMethods(parameters: CompletionParameters, result: CompletionResultSet) {
        val offset = parameters.offset
        val text = parameters.editor.document.text
        var i = offset - 1
        while (i >= 0 && (text[i].isLetterOrDigit() || text[i] == '_')) i--
        if (i >= 0 && text[i] == '.') {
            for (method in DOT_METHODS) {
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
