package org.gala.ide.intellij

import org.antlr.v4.runtime.CharStream
import org.antlr.v4.runtime.DefaultErrorStrategy
import org.antlr.v4.runtime.InputMismatchException
import org.antlr.v4.runtime.Parser
import org.antlr.v4.runtime.Token
import org.antlr.v4.runtime.WritableToken
import org.antlr.v4.runtime.misc.IntervalSet
import org.gala.ide.intellij.parser.galaLexer
import org.gala.ide.intellij.parser.galaParser

/**
 * The GALA lexer with the compiler's line-break rule applied, so the plugin's
 * PSI tree matches what the compiler parses: a '(' that starts a line, right
 * after a token that can end an expression (an identifier, a literal, ')', ']'
 * or '}'), is re-typed as NL_LPAREN. The grammar's call suffix rejects
 * NL_LPAREN, so such a '(' begins a new statement instead of calling the line
 * before. Mirrors internal/parser/newline.go in the compiler.
 */
class GalaNewlineLexer(input: CharStream?) : galaLexer(input) {
    private var prevEndLine = 0
    private var prevEndsExpr = false

    override fun reset() {
        super.reset()
        prevEndLine = 0
        prevEndsExpr = false
    }

    override fun nextToken(): Token {
        val tok = super.nextToken()
        if (tok.channel != Token.DEFAULT_CHANNEL) return tok
        if (tok.type == LPAREN && prevEndsExpr && tok.line > prevEndLine) {
            (tok as WritableToken).type = galaParser.NL_LPAREN
        }
        prevEndsExpr = tok.type in ENDS_EXPR
        prevEndLine = tok.line + if (tok.type in SPANS_LINES) tok.text.count { it == '\n' } else 0
        return tok
    }

    companion object {
        private fun typeOf(name: String): Int =
            (0..galaParser.VOCABULARY.maxTokenType).first {
                galaParser.VOCABULARY.getLiteralName(it) == name ||
                    galaParser.VOCABULARY.getSymbolicName(it) == name
            }

        private val LPAREN = typeOf("'('")

        // Literals whose text can hold a line break: a raw string, or a quoted
        // literal with an escaped newline.
        private val SPANS_LINES = setOf(
            galaLexer.STRING, galaLexer.CHAR_LIT, galaLexer.RAW_STRING,
            galaLexer.INTERPOLATED_STRING, galaLexer.FORMAT_STRING,
        )

        private val ENDS_EXPR = SPANS_LINES + setOf(galaLexer.IDENTIFIER, galaLexer.INT_LIT, galaLexer.FLOAT_LIT) +
            listOf("'true'", "'false'", "'nil'", "')'", "']'", "'}'").map(::typeOf)
    }
}

/**
 * Keeps the internal NL_LPAREN token out of syntax errors: wherever it is
 * expected a plain '(' is expected too, so it is dropped from the expected set
 * and the message reads as it did before NL_LPAREN existed.
 */
class GalaErrorStrategy : DefaultErrorStrategy() {
    override fun getExpectedTokens(recognizer: Parser): IntervalSet =
        withoutNewlineParen(super.getExpectedTokens(recognizer))

    override fun reportInputMismatch(recognizer: Parser, e: InputMismatchException) {
        val msg = "mismatched input " + getTokenErrorDisplay(e.offendingToken) +
            " expecting " + withoutNewlineParen(e.expectedTokens).toString(recognizer.vocabulary)
        recognizer.notifyErrorListeners(e.offendingToken, msg, e)
    }

    private fun withoutNewlineParen(set: IntervalSet): IntervalSet =
        IntervalSet(set).apply { remove(galaParser.NL_LPAREN) }
}
