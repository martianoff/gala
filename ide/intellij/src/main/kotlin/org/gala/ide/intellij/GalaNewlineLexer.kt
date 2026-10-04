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
 * or '}') is re-typed as NL_LPAREN, and a '*' or '&' there written directly
 * against its operand (`*p`, `&n`) as NL_STAR or NL_AMP. The grammar's call
 * suffix, multiplication and bitwise and reject the re-typed tokens, so such a
 * token begins a new statement instead of continuing the line before; `* b`
 * with a space still continues it. Mirrors internal/parser/newline.go in the
 * compiler.
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
        val retyped = AT_LINE_START[tok.type]
        // The lexer has just consumed the token, so LA(1) is the character
        // after it: a '*' or '&' followed by whitespace stays binary.
        if (retyped != null && prevEndsExpr && tok.line > prevEndLine &&
            (tok.type == LPAREN || _input.LA(1) !in WS_CHARS)
        ) {
            (tok as WritableToken).type = retyped
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

        // Each token the line-break rule re-types, and the token it becomes.
        private val AT_LINE_START = mapOf(
            LPAREN to galaParser.NL_LPAREN,
            typeOf("'*'") to galaParser.NL_STAR,
            typeOf("'&'") to galaParser.NL_AMP,
        )

        // The characters the grammar's WS rule skips.
        private val WS_CHARS = setOf(' '.code, '\t'.code, '\r'.code, '\n'.code)

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
 * Keeps the internal re-typed tokens out of syntax errors: wherever one is
 * expected the token it was re-typed from is expected too, so it is dropped
 * from the expected set the messages print. Only the messages change; error recovery still uses the
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
        IntervalSet(set).apply {
            remove(galaParser.NL_LPAREN)
            remove(galaParser.NL_STAR)
            remove(galaParser.NL_AMP)
        }.toString(recognizer.vocabulary)
}
