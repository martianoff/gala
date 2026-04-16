package org.gala.ide.intellij

import com.intellij.lang.annotation.AnnotationHolder
import com.intellij.lang.annotation.Annotator
import com.intellij.lang.annotation.HighlightSeverity
import com.intellij.openapi.editor.DefaultLanguageHighlighterColors
import com.intellij.openapi.editor.colors.TextAttributesKey
import com.intellij.psi.PsiElement
import org.antlr.intellij.adaptor.lexer.TokenIElementType
import org.gala.ide.intellij.parser.galaLexer
import org.gala.ide.intellij.psi.*

/**
 * Semantic annotator for GALA — provides highlighting beyond lexer-level tokens.
 *
 * Highlights:
 * - Built-in type names (int, string, bool, etc.) as class/type color
 * - true/false/nil as keyword color
 * - Unresolved references with weak warning
 * - String interpolation variables inside s"..." and f"..."
 */
class GalaAnnotator : Annotator {

    companion object {
        val BUILTIN_TYPE = TextAttributesKey.createTextAttributesKey(
            "GALA_BUILTIN_TYPE", DefaultLanguageHighlighterColors.CLASS_NAME
        )
        val FUNCTION_CALL = TextAttributesKey.createTextAttributesKey(
            "GALA_FUNCTION_CALL", DefaultLanguageHighlighterColors.FUNCTION_CALL
        )
        val FUNCTION_DECLARATION = TextAttributesKey.createTextAttributesKey(
            "GALA_FUNCTION_DECLARATION", DefaultLanguageHighlighterColors.FUNCTION_DECLARATION
        )
        val PARAMETER = TextAttributesKey.createTextAttributesKey(
            "GALA_PARAMETER", DefaultLanguageHighlighterColors.PARAMETER
        )
        val LOCAL_VARIABLE = TextAttributesKey.createTextAttributesKey(
            "GALA_LOCAL_VARIABLE", DefaultLanguageHighlighterColors.LOCAL_VARIABLE
        )
        val TYPE_PARAMETER = TextAttributesKey.createTextAttributesKey(
            "GALA_TYPE_PARAMETER", DefaultLanguageHighlighterColors.CLASS_NAME
        )
        val INTERPOLATION_VAR = TextAttributesKey.createTextAttributesKey(
            "GALA_INTERPOLATION_VAR", DefaultLanguageHighlighterColors.TEMPLATE_LANGUAGE_COLOR
        )

        private val BUILTIN_TYPE_NAMES = setOf(
            "any", "bool", "byte", "rune", "error",
            "int", "int8", "int16", "int32", "int64",
            "uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
            "float32", "float64",
            "complex64", "complex128",
            "string"
        )

        // Built-in functions available without import
        private val BUILTIN_FUNCTION_NAMES = setOf(
            "Println", "Print",
            "SliceOf",
            "len", "cap", "make", "append", "copy", "delete",
            "close", "panic", "recover"
        )

        // Standard library types (auto-imported)
        private val STD_TYPE_NAMES = setOf(
            "Option", "Some", "None",
            "Either", "Left", "Right",
            "Try", "Success", "Failure",
            "Tuple", "Immutable", "ConstPtr", "Void",
            "Seq", "Traversable", "Iterable"
        )
    }

    override fun annotate(element: PsiElement, holder: AnnotationHolder) {
        val elementType = element.node?.elementType ?: return

        // Check leaf tokens
        if (elementType is TokenIElementType) {
            when (elementType.antlrTokenType) {
                galaLexer.IDENTIFIER -> annotateIdentifier(element, holder)
                galaLexer.INTERPOLATED_STRING, galaLexer.FORMAT_STRING -> annotateInterpolatedString(element, holder)
            }
            return
        }

        // Also check children of composite nodes for interpolated strings
        // (the annotator may be called on the wrapper node, not the token)
        for (child in element.children) {
            val childType = child.node?.elementType
            if (childType is TokenIElementType) {
                when (childType.antlrTokenType) {
                    galaLexer.INTERPOLATED_STRING, galaLexer.FORMAT_STRING -> annotateInterpolatedString(child, holder)
                }
            }
        }
    }

    private fun annotateIdentifier(element: PsiElement, holder: AnnotationHolder) {
        val text = element.text

        // Built-in types get class name highlighting
        if (text in BUILTIN_TYPE_NAMES) {
            holder.newSilentAnnotation(HighlightSeverity.INFORMATION)
                .textAttributes(BUILTIN_TYPE)
                .create()
            return
        }

        // Standard library types get class name highlighting
        if (text in STD_TYPE_NAMES) {
            holder.newSilentAnnotation(HighlightSeverity.INFORMATION)
                .textAttributes(BUILTIN_TYPE)
                .create()
            return
        }

        // Built-in functions get function call highlighting
        if (text in BUILTIN_FUNCTION_NAMES) {
            holder.newSilentAnnotation(HighlightSeverity.INFORMATION)
                .textAttributes(FUNCTION_CALL)
                .create()
            return
        }

        // Check the PSI context to determine the kind of identifier
        val parent = element.parent ?: return

        // Function declaration name
        if (parent is IdentifierNode) {
            val grandparent = parent.parent
            if (grandparent is FunctionDeclarationNode && grandparent.nameIdentifier === parent) {
                holder.newSilentAnnotation(HighlightSeverity.INFORMATION)
                    .textAttributes(FUNCTION_DECLARATION)
                    .create()
                return
            }
        }

        // Any identifier sitting inside a type context (parameter type, return
        // type, struct field type, generic arg, type alias RHS, etc.) gets the
        // class-name color. This catches user-defined types like Request or
        // Handler without requiring them in a hardcoded set.
        if (isInsideTypeContext(element)) {
            holder.newSilentAnnotation(HighlightSeverity.INFORMATION)
                .textAttributes(BUILTIN_TYPE)
                .create()
            return
        }
    }

    private fun isInsideTypeContext(element: PsiElement): Boolean {
        var node = element.parent
        var depth = 0
        while (node != null && depth < 8) {
            if (node.node?.elementType === GalaTokenTypes.RULE_TYPE) {
                return true
            }
            node = node.parent
            depth++
        }
        return false
    }

    private fun annotateInterpolatedString(element: PsiElement, holder: AnnotationHolder) {
        // Highlight $variable and ${expression} inside interpolated strings
        val text = element.text
        val startOffset = element.textRange.startOffset
        var i = 0

        while (i < text.length) {
            // Skip escaped dollars ($$)
            if (text[i] == '$' && i + 1 < text.length && text[i + 1] == '$') {
                i += 2
                continue
            }
            if (text[i] == '$' && i + 1 < text.length && (i == 0 || text[i - 1] != '\\')) {
                if (text[i + 1] == '{') {
                    // ${expression} — highlight the ${ and }
                    val braceStart = i
                    var depth = 1
                    var j = i + 2
                    while (j < text.length && depth > 0) {
                        if (text[j] == '{') depth++
                        else if (text[j] == '}') depth--
                        j++
                    }
                    if (depth == 0) {
                        holder.newSilentAnnotation(HighlightSeverity.INFORMATION)
                            .range(com.intellij.openapi.util.TextRange(startOffset + braceStart, startOffset + j))
                            .textAttributes(INTERPOLATION_VAR)
                            .create()
                        i = j
                        continue
                    }
                } else if (text[i + 1].isLetter() || text[i + 1] == '_') {
                    // $variable — highlight $var
                    val varStart = i
                    var j = i + 1
                    while (j < text.length && (text[j].isLetterOrDigit() || text[j] == '_')) {
                        j++
                    }
                    holder.newSilentAnnotation(HighlightSeverity.INFORMATION)
                        .range(com.intellij.openapi.util.TextRange(startOffset + varStart, startOffset + j))
                        .textAttributes(INTERPOLATION_VAR)
                        .create()
                    i = j
                    continue
                }
            }
            i++
        }
    }
}
