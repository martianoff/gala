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
 * NL_AMP when it is directly inside a block's '{' and written against its
 * operand (`*p`, `&n`). The grammar's call suffix, multiplication and bitwise
 * and reject the re-typed tokens, so such a token begins a new statement
 * instead of continuing the line before; `* b` with a space still continues
 * it. Mirrors internal/parser/newline.go in the compiler, including which '{'
 * opens a block (see trackBrackets there).
 */
class GalaNewlineLexer(input: CharStream?) : galaLexer(input) {
    private var prevEndLine = 0
    private var prevEndsExpr = false
    private var prevType = 0

    // The brackets open at this point, innermost last.
    private val open = ArrayDeque<Int>()

    // The function declaration, `if` and `for` headers whose block's '{' is
    // still to come, innermost last (see newline.go's header).
    private class Header(val depth: Int, val fn: Boolean) {
        var cond = 0 // COND_OPEN while an if's parenthesized condition is open, COND_CLOSED after
    }
    private val headers = ArrayDeque<Header>()

    override fun reset() {
        super.reset()
        prevEndLine = 0
        prevEndsExpr = false
        prevType = 0
        open.clear()
        headers.clear()
    }

    override fun nextToken(): Token {
        val tok = super.nextToken()
        if (tok.channel != Token.DEFAULT_CHANNEL) return tok
        val lineBreak = tok.line > prevEndLine
        endHeader(tok.type)
        if (prevEndsExpr && lineBreak) {
            val retyped = when (tok.type) {
                LPAREN -> galaParser.NL_LPAREN
                STAR -> galaParser.NL_STAR.takeIf { isPrefixOperator() }
                AMP -> galaParser.NL_AMP.takeIf { isPrefixOperator() }
                else -> null
            }
            if (retyped != null) (tok as WritableToken).type = retyped
        }
        trackBrackets(tok.type, lineBreak)
        prevType = tok.type
        prevEndsExpr = tok.type in ENDS_EXPR
        prevEndLine = tok.line + if (tok.type in SPANS_LINES) tok.text.count { it == '\n' } else 0
        return tok
    }

    private fun pendingHeader(): Header? = headers.lastOrNull()?.takeIf { it.depth == open.size }

    // Drops the pending header of an if whose parenthesized condition is not
    // followed by '{': it has no block. Runs before the token is re-typed,
    // since a statement cannot begin inside a header.
    private fun endHeader(type: Int) {
        val h = pendingHeader() ?: return
        if (h.cond == COND_CLOSED && type != LBRACE) headers.removeLast()
    }

    // Directly inside a block's '{' and not inside a header still waiting for
    // its own '{': where a statement can begin.
    private fun atStatementLevel(): Boolean = open.lastOrNull() == BLOCK_OPEN && pendingHeader() == null

    // Whether a `func` here declares a function rather than writing a func
    // type (which has no block, so is not a header): it starts a line or
    // follows a block's '{', at the top level or directly inside a block, and
    // not inside another header (where a `func` on its own line is a
    // func-typed result).
    private fun declarationCanBegin(lineBreak: Boolean): Boolean =
        (open.isEmpty() || open.last() == BLOCK_OPEN) && pendingHeader() == null &&
            (lineBreak || prevType == LBRACE)

    private fun trackBrackets(type: Int, lineBreak: Boolean) {
        val depth = open.size
        val h = pendingHeader()
        when {
            type == LBRACE -> {
                var kind = BLOCK_OPEN
                if (h != null) {
                    headers.removeLast()
                } else if (prevType in NOT_BLOCK_AFTER) {
                    kind = BRACE_OPEN
                }
                open.addLast(kind)
            }
            type in OPENS -> {
                if (h != null && type == LPAREN && prevType == IF) h.cond = COND_OPEN
                open.addLast(PAREN_OPEN)
            }
            // A '}' closes the innermost '{', and with it any bracket an edit
            // left open inside it.
            type == RBRACE -> {
                while (open.isNotEmpty() && open.removeLast() == PAREN_OPEN) {
                    // keep popping until the '{' itself is closed
                }
            }
            // A stray ')' or ']' does not close a '{'.
            type in CLOSES -> if (open.lastOrNull() == PAREN_OPEN) open.removeLast()
            type == CASE && prevType == LBRACE && depth > 0 -> open[open.lastIndex] = BRACE_OPEN
            type in OPENS_HEADER && (type != FUNC || declarationCanBegin(lineBreak)) ->
                headers.addLast(Header(depth, type == FUNC))
            // An expression body, an if-expression or a match guard ending:
            // the header has no block.
            h != null && (type == ASSIGN && h.fn || type == ELSE || type == ARROW) -> headers.removeLast()
        }
        // A header whose depth has been closed is gone; one whose condition
        // has just closed waits to see whether a '{' follows.
        while (headers.isNotEmpty() && headers.last().depth > open.size) headers.removeLast()
        val closed = pendingHeader()
        if (closed != null && closed.cond == COND_OPEN && type in CLOSES) closed.cond = COND_CLOSED
    }

    // Whether the '*' or '&' just consumed is a prefix operator: where a
    // statement can begin, and followed by its operand rather than by
    // whitespace the grammar's WS rule skips or a comment. The lexer has just
    // consumed it, so LA(1) is the character after it.
    private fun isPrefixOperator(): Boolean {
        if (!atStatementLevel()) return false
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

        // Kinds of open bracket.
        private const val PAREN_OPEN = 0 // '(' or '['
        private const val BRACE_OPEN = 1 // a '{' that is not a block
        private const val BLOCK_OPEN = 2 // a block's '{'

        private val LPAREN = typeOf("'('")
        private val LBRACE = typeOf("'{'")
        private val RBRACE = typeOf("'}'")
        private val ASSIGN = typeOf("'='")
        private val ARROW = typeOf("'=>'")
        private val ELSE = typeOf("'else'")
        private val FUNC = typeOf("'func'")
        private val IF = typeOf("'if'")
        private const val COND_OPEN = 1
        private const val COND_CLOSED = 2
        private val CASE = typeOf("'case'")
        private val STAR = typeOf("'*'")
        private val AMP = typeOf("'&'")

        // The internal tokens the line-break rule re-types into.
        internal val RETYPED = intArrayOf(galaParser.NL_LPAREN, galaParser.NL_STAR, galaParser.NL_AMP)

        private val OPENS = setOf(LPAREN, galaParser.NL_LPAREN, typeOf("'['"))
        private val CLOSES = setOf(typeOf("')'"), typeOf("']'"))
        private val OPENS_HEADER = listOf("'func'", "'if'", "'for'").map(::typeOf).toSet()
        private val NOT_BLOCK_AFTER = setOf(galaLexer.IDENTIFIER, typeOf("']'"), typeOf("'struct'"), typeOf("'interface'"))

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
 * from the expected set the messages print. Only the messages change; error
 * recovery still uses the full expected set, exactly as the compiler's parser
 * does.
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
