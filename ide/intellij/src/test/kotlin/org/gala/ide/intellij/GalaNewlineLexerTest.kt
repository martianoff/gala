package org.gala.ide.intellij

import org.antlr.v4.runtime.BaseErrorListener
import org.antlr.v4.runtime.CharStreams
import org.antlr.v4.runtime.CommonTokenStream
import org.antlr.v4.runtime.RecognitionException
import org.antlr.v4.runtime.Recognizer
import org.gala.ide.intellij.parser.galaParser
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The plugin applies the compiler's line-break rule: a '(' that starts a line
 * after a name, literal, ')', ']' or '}' begins a new statement.
 */
class GalaNewlineLexerTest {
    private fun parse(source: String): Pair<galaParser.SourceFileContext, List<String>> {
        val errors = mutableListOf<String>()
        val parser = galaParser(CommonTokenStream(GalaNewlineLexer(CharStreams.fromString(source))))
        parser.removeErrorListeners()
        parser.addErrorListener(object : BaseErrorListener() {
            override fun syntaxError(
                recognizer: Recognizer<*, *>?, offendingSymbol: Any?,
                line: Int, charPositionInLine: Int, msg: String, e: RecognitionException?,
            ) {
                errors.add(msg)
            }
        })
        parser.errorHandler = GalaErrorStrategy()
        return parser.sourceFile() to errors
    }

    private fun statements(body: String): Int {
        val (tree, errors) = parse("package main\n\nfunc f() {\n$body\n}\n")
        assertEquals(emptyList<String>(), errors)
        return tree.topLevelDeclaration(0).functionDeclaration().block().statement().size
    }

    @Test
    fun parenOnNewLineStartsStatement() {
        assertEquals(2, statements("Println(\"zero\")\n(1, 2)"))
        assertEquals(2, statements("val a int64 = 1\n(a, 2)"))
        assertEquals(2, statements("Println(\"zero\") // note\n(x) => x"))
    }

    @Test
    fun otherLineBreaksContinue() {
        assertEquals(1, statements("f(1)(2)"))
        assertEquals(1, statements("Println(\n    1,\n    (2, 3),\n)"))
        assertEquals(1, statements("xs\n    .Map((x) => x)"))
        assertEquals(1, statements("val a = 1\n    + 2"))
        assertEquals(1, statements("return\n(1, 2)"))
        assertEquals(1, statements("val s = f(`a\nb`)(1)"))
    }

    @Test
    fun syntaxErrorsDoNotNameNewlineParen() {
        val (_, errors) = parse("package main\n\nval f func int = g\n")
        assertTrue(errors.isNotEmpty())
        errors.forEach { assertTrue(it, !it.contains("NL_LPAREN")) }
    }
}
