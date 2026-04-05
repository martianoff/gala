package org.gala.ide.intellij

import com.intellij.openapi.editor.colors.TextAttributesKey
import com.intellij.openapi.fileTypes.SyntaxHighlighter
import com.intellij.openapi.options.colors.AttributesDescriptor
import com.intellij.openapi.options.colors.ColorDescriptor
import com.intellij.openapi.options.colors.ColorSettingsPage
import javax.swing.Icon

class GalaColorSettingsPage : ColorSettingsPage {

    companion object {
        private val DESCRIPTORS = arrayOf(
            AttributesDescriptor("Keyword", GalaSyntaxHighlighter.KEYWORD),
            AttributesDescriptor("Identifier", GalaSyntaxHighlighter.IDENTIFIER),
            AttributesDescriptor("Built-in type", GalaAnnotator.BUILTIN_TYPE),
            AttributesDescriptor("Function declaration", GalaAnnotator.FUNCTION_DECLARATION),
            AttributesDescriptor("Function call", GalaAnnotator.FUNCTION_CALL),
            AttributesDescriptor("Parameter", GalaAnnotator.PARAMETER),
            AttributesDescriptor("Local variable", GalaAnnotator.LOCAL_VARIABLE),
            AttributesDescriptor("String", GalaSyntaxHighlighter.STRING),
            AttributesDescriptor("String interpolation", GalaAnnotator.INTERPOLATION_VAR),
            AttributesDescriptor("Number", GalaSyntaxHighlighter.NUMBER),
            AttributesDescriptor("Line comment", GalaSyntaxHighlighter.LINE_COMMENT),
            AttributesDescriptor("Block comment", GalaSyntaxHighlighter.BLOCK_COMMENT),
            AttributesDescriptor("Operator", GalaSyntaxHighlighter.OPERATOR),
            AttributesDescriptor("Parentheses", GalaSyntaxHighlighter.PARENTHESES),
            AttributesDescriptor("Braces", GalaSyntaxHighlighter.BRACES),
            AttributesDescriptor("Brackets", GalaSyntaxHighlighter.BRACKETS),
            AttributesDescriptor("Dot", GalaSyntaxHighlighter.DOT),
            AttributesDescriptor("Comma", GalaSyntaxHighlighter.COMMA),
            AttributesDescriptor("Bad character", GalaSyntaxHighlighter.BAD_CHARACTER),
        )
    }

    override fun getIcon(): Icon? = GalaIcons.FILE

    override fun getHighlighter(): SyntaxHighlighter = GalaSyntaxHighlighter()

    override fun getDemoText(): String = """
package main

import "fmt"

// A sealed type for shapes
sealed type Shape {
    case Circle(radius float64)
    case Rectangle(width float64, height float64)
}

type Person struct {
    val name string
    val age int
}

func greet(p Person) string {
    val msg = s"Hello, ${'$'}{p.name}! Age: ${'$'}{p.age}"
    return msg
}

func area(s Shape) float64 {
    return s match {
        case Circle(r) => 3.14 * r * r
        case Rectangle(w, h) => w * h
    }
}

func main() {
    val p = Person(name = "Alice", age = 30)
    fmt.Println(greet(p))
    val c = Circle(radius = 5.0)
    fmt.Println(area(c))
}
""".trimIndent()

    override fun getAdditionalHighlightingTagToDescriptorMap(): Map<String, TextAttributesKey>? = null

    override fun getAttributeDescriptors(): Array<AttributesDescriptor> = DESCRIPTORS

    override fun getColorDescriptors(): Array<ColorDescriptor> = ColorDescriptor.EMPTY_ARRAY

    override fun getDisplayName(): String = "GALA"
}
