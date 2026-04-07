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

        // Source: internal/transpiler/transformer — built-in functions
        // These are rewritten by the transpiler and available without import
        private val BUILTIN_FUNCTIONS = listOf(
            // Print functions (rewritten to fmt.Println/fmt.Print)
            "Println", "Print",
            // Go interop constructors
            "SliceOf",
            // Std constructors
            "Some", "None", "Left", "Right", "Success", "Failure",
            "NewImmutable", "NewConstPtr",
            // Go built-in functions available in GALA
            "len", "cap", "make", "append", "copy", "delete",
            "close", "panic", "recover",
        )

        // Source: std/*.gala — ONLY auto-imported types (std is implicit)
        // Other packages (collection_immutable, etc.) require explicit import
        // and will be suggested by the LSP server in Phase 5.
        private val STD_AUTO_IMPORTED = listOf(
            // Sealed types + constructors (always available without import)
            "Option", "Some", "None",
            "Either", "Left", "Right",
            "Try", "Success", "Failure",
            // Core types (always available without import)
            "Tuple", "Immutable",
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
                    for (type in STD_AUTO_IMPORTED) {
                        result.addElement(
                            LookupElementBuilder.create(type).withTypeText("std")
                        )
                    }
                    for (func in BUILTIN_FUNCTIONS) {
                        result.addElement(
                            LookupElementBuilder.create(func).withTypeText("builtin").bold()
                        )
                    }

                    // In-scope declarations from the current file
                    addInScopeDeclarations(parameters, result)

                    // Note: dot-completion methods are provided by the LSP server
                    // (type-aware, not static lists)
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
