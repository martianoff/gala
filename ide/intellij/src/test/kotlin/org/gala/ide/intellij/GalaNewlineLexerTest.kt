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
 * The plugin applies the compiler's line-break rule: a '(', or a '*' or '&'
 * written against its operand, that starts a line after a name, literal, ')',
 * ']' or '}' begins a new statement.
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
        assertEquals(2, statements("Println(\"zero\") /* a\nb */ (1, 2)"))
        assertEquals(2, statements("val s = \"a\\\nb\"\n(s, 1)"))
    }

    @Test
    fun prefixStarOrAmpersandOnNewLineStartsStatement() {
        assertEquals(3, statements("var n = 1\nval p = &n\n*p + 1"))
        assertEquals(2, statements("f()\n*p = 5"))
        assertEquals(2, statements("val x = 1\n**pp"))
        assertEquals(2, statements("val x = 1\n&x"))
        assertEquals(2, statements("val a = b\n*(p)"))
        assertEquals(2, statements("Println(\"zero\")\n        *p = 1"))
        assertEquals(2, statements("if (a) { f(x) }\n*p = 1"))
        assertEquals(2, statements("val r = Rect{w: 1}\n*p = 1"))
        assertEquals(2, statements("var x\n*p = 5"))
        assertEquals(2, statements("val r = x match {\n    case _ => 0\n}\n*p = 1"))
    }

    @Test
    fun starAtLineStartInDeclarations() {
        assertEquals(emptyList<String>(), parse("package main\n\nfunc g()\n*int = nil\n").second)
        assertEquals(emptyList<String>(), parse("package main\n\ntype I interface {\n    M()\n    *int\n}\n").second)
        assertEquals(emptyList<String>(), parse("package main\n\nval a = b\n*c\n").second)
    }

    @Test
    fun blockAfterHeaderEndingInType() {
        fun bodyStatements(src: String): Int {
            val (tree, errors) = parse("package main\n\n$src\n")
            assertEquals(emptyList<String>(), errors)
            return tree.topLevelDeclaration().last().functionDeclaration().block().statement().size
        }
        assertEquals(3, bodyStatements("func f() int{\n    val p = &n\n    *p = 5\n    n\n}"))
        assertEquals(3, bodyStatements("func f() Option[int]{\n    val p = &n\n    *p = 5\n    None()\n}"))
        assertEquals(3, bodyStatements("func g() int = 1\n\nfunc f() int {\n    val p = &n\n    *p = 5\n    n\n}"))
        assertEquals(3, bodyStatements("func apply(f func(int) int) int {\n    val p = &n\n    *p = 5\n    n\n}"))
        assertEquals(
            3,
            bodyStatements("func (o Box[T]) m[U any](f func(T) Option[U]) Option[U] {\n    val p = &n\n    *p = 5\n    None()\n}"),
        )
        assertEquals(
            1,
            bodyStatements("func f() {\n    for i := 0; i < n; i = i + step {\n        val p = &n\n        *p = 5\n    }\n}"),
        )
        assertEquals(3, bodyStatements("type F func(int) int\n\nfunc f() int {\n    val p = &n\n    *p = 5\n    n\n}"))
        assertEquals(emptyList<String>(), parse("package main\n\ntype F func(int) int\n\ntype I interface {\n    M()\n    *int\n}\n").second)
    }

    @Test
    fun otherLineBreaksContinue() {
        assertEquals(1, statements("val a = b\n    * c"))
        assertEquals(1, statements("val a = b\n    & c"))
        assertEquals(1, statements("val a =\n    *p"))
        assertEquals(1, statements("val a = b\n    * *p"))
        assertEquals(1, statements("val a = b *\n    *p"))
        assertEquals(1, statements("f(1,\n*p)"))
        assertEquals(1, statements("f(w\n    *h)"))
        assertEquals(1, statements("val a = (w\n    &h)"))
        assertEquals(1, statements("val a = xs[i\n    *2]"))
        assertEquals(1, statements("val a = b\n    *// times\n    c"))
        assertEquals(1, statements("val a = b\n    */* times */c"))
        assertEquals(1, statements("xs.Map((x) => {\n    val p = &x\n    *p\n})"))
        assertEquals(1, statements("val r = x match {\n    case Some(v) => v\n        *factor\n    case _ => 0\n}"))
        assertEquals(1, statements("xs.Collect({\n    case v => v\n        *k\n})"))
        assertEquals(1, statements("val r = Rect{\n    w: width\n        *scale,\n}"))
        assertEquals(1, statements("val r = Array[int]{\n    w\n        *scale,\n}"))
        assertEquals(1, statements("val r = Rect {\n    w: width\n        *scale,\n}"))
        assertEquals(1, statements("val r = if (c) Rect{\n    w: a\n        *b,\n} else d"))
        assertEquals(
            1,
            statements("val r = x match {\n    case v if v > 0 => v\n    case _ => Rect{\n        w: a\n            *b,\n    }\n}"),
        )
        assertEquals(2, statements("val a = if (c) x else y\nval r = Rect{\n    w: v\n        *s,\n}"))
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
        val (_, starErrors) = parse("package main\n\nfunc f() {\n    val x = 1\n    *}\n")
        assertTrue(starErrors.isNotEmpty())
        starErrors.forEach { assertTrue(it, !it.contains("NL_")) }
    }
}
