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
 * or '}') is re-typed as NL_LPAREN, and a '*' or '&' there as NL_STAR or
 * NL_AMP when it is directly inside a block's '{' (not a composite literal's or
 * one that opens case arms) and written against its operand (`*p`, `&n`). The grammar's call suffix, multiplication and bitwise and
 * reject the re-typed tokens, so such a token begins a new statement instead
 * of continuing the line before; `* b` with a space still continues it.
 * Mirrors internal/parser/newline.go in the compiler.
 */
class GalaNewlineLexer(input: CharStream?) : galaLexer(input) {
    private var prevEndLine = 0
    private var prevEndsExpr = false
    private var prevType = 0
    private var prevStop = -1

    // The brackets open at this point, innermost last: true for a block's
    // '{', false for any other bracket.
    private val open = ArrayDeque<Boolean>()

    override fun reset() {
        super.reset()
        prevEndLine = 0
        prevEndsExpr = false
        prevType = 0
        prevStop = -1
        open.clear()
    }

    override fun nextToken(): Token {
        val tok = super.nextToken()
        if (tok.channel != Token.DEFAULT_CHANNEL) return tok
        if (prevEndsExpr && tok.line > prevEndLine) {
            val retyped = when (tok.type) {
                LPAREN -> galaParser.NL_LPAREN
                STAR -> galaParser.NL_STAR.takeIf { isPrefixOperator() }
                AMP -> galaParser.NL_AMP.takeIf { isPrefixOperator() }
                else -> null
            }
            if (retyped != null) (tok as WritableToken).type = retyped
        }
        // A '{' opens a block unless it is written against a type name or ']'
        // (a composite literal); a 'case' right after a '{' shows that '{'
        // opens case arms instead.
        when {
            tok.type in OPENS -> open.addLast(
                tok.type == LBRACE &&
                    !((prevType == galaLexer.IDENTIFIER || prevType == RBRACK) && tok.startIndex == prevStop + 1),
            )
            tok.type in CLOSES -> open.removeLastOrNull()
            tok.type == CASE && prevType == LBRACE && open.isNotEmpty() -> open[open.lastIndex] = false
        }
        prevType = tok.type
        prevStop = tok.stopIndex
        prevEndsExpr = tok.type in ENDS_EXPR
        prevEndLine = tok.line + if (tok.type in SPANS_LINES) tok.text.count { it == '\n' } else 0
        return tok
    }

    // Whether the '*' or '&' just consumed is a prefix operator: directly
    // inside a block's '{', where a statement can begin, and followed by its operand
    // rather than by whitespace the grammar's WS rule skips or a comment. The
    // lexer has just consumed it, so LA(1) is the character after it.
    private fun isPrefixOperator(): Boolean {
        if (open.lastOrNull() != true) return false
        return when (_input.LA(1)) {
            ' '.code, '\t'.code, '\r'.code, '\n'.code -> false
            '/'.code -> _input.LA(2).let { it != '/'.code && it != '*'.code }
            else -> true
        }
    }

    companion object {
        private fun typeOf(name: String): Int =
            (0..galaParser.VOCABULARY.maxTokenType).first {
                galaParser.VOCABULARY.getLiteralName(it) == name ||
                    galaParser.VOCABULARY.getSymbolicName(it) == name
            }

        private val LPAREN = typeOf("'('")
        private val LBRACE = typeOf("'{'")
        private val RBRACK = typeOf("']'")
        private val CASE = typeOf("'case'")
        private val STAR = typeOf("'*'")
        private val AMP = typeOf("'&'")

        // The internal tokens the line-break rule re-types into.
        internal val RETYPED = intArrayOf(galaParser.NL_LPAREN, galaParser.NL_STAR, galaParser.NL_AMP)

        private val OPENS = setOf(LPAREN, galaParser.NL_LPAREN, typeOf("'['"), LBRACE)
        private val CLOSES = listOf("')'", "']'", "'}'").map(::typeOf).toSet()

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
        IntervalSet(set).apply { GalaNewlineLexer.RETYPED.forEach { remove(it) } }.toString(recognizer.vocabulary)
}
