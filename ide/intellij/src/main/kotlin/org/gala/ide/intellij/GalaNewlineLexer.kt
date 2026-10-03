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
 * PSI tree matches what the compiler parses: a '(' separated by a line break
 * from a token that can end an expression (an identifier, a literal, ')', ']'
 * or '}') is re-typed as NL_LPAREN. The grammar's call suffix rejects
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
 * the messages print. Only the messages change; error recovery still uses the
 * full expected set, exactly as the compiler's parser does.
 */
class GalaErrorStrategy : DefaultErrorStrategy() {
    override fun reportInputMismatch(recognizer: Parser, e: InputMismatchException) {
        val msg = "mismatched input " + getTokenErrorDisplay(e.offendingToken) +
            " expecting " + expectedDisplay(recognizer, e.expectedTokens)
        recognizer.notifyErrorListeners(e.offendingToken, msg, e)
    }

    override fun reportUnwantedToken(recognizer: Parser) {
        if (inErrorRecoveryMode(recognizer)) return
        beginErrorCondition(recognizer)
        val t = recognizer.currentToken
        val msg = "extraneous input " + getTokenErrorDisplay(t) +
            " expecting " + expectedDisplay(recognizer, getExpectedTokens(recognizer))
        recognizer.notifyErrorListeners(t, msg, null)
    }

    override fun reportMissingToken(recognizer: Parser) {
        if (inErrorRecoveryMode(recognizer)) return
        beginErrorCondition(recognizer)
        val t = recognizer.currentToken
        val msg = "missing " + expectedDisplay(recognizer, getExpectedTokens(recognizer)) +
            " at " + getTokenErrorDisplay(t)
        recognizer.notifyErrorListeners(t, msg, null)
    }

    private fun expectedDisplay(recognizer: Parser, set: IntervalSet): String =
        IntervalSet(set).apply { remove(galaParser.NL_LPAREN) }.toString(recognizer.vocabulary)
}
