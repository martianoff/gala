package org.gala.ide.intellij

import com.intellij.lexer.LexerBase
import com.intellij.psi.tree.IElementType

class GalaLexer : LexerBase() {
    private var buffer: CharSequence = ""
    private var startOffset: Int = 0
    private var endOffset: Int = 0
    private var currentOffset: Int = 0
    private var tokenType: IElementType? = null

    private val keywords = setOf(
        // Declaration keywords
        "package", "import", "val", "var", "func", "type", "struct", "interface",
        // Control flow
        "if", "else", "for", "range", "return", "match", "case",
        "break", "continue", "defer", "go", "select", "switch", "default",
        // Literals
        "true", "false", "nil",
        // Type keywords
        "map", "chan"
    )

    private val builtinTypes = setOf(
        "any", "bool", "byte", "rune", "error",
        "int", "int8", "int16", "int32", "int64",
        "uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
        "float32", "float64",
        "complex64", "complex128",
        "string"
    )

    override fun start(buffer: CharSequence, startOffset: Int, endOffset: Int, initialState: Int) {
        this.buffer = buffer
        this.startOffset = startOffset
        this.endOffset = endOffset
        this.currentOffset = startOffset
        advance()
    }

    override fun getState(): Int = 0

    override fun getTokenType(): IElementType? = tokenType

    override fun getTokenStart(): Int = startOffset

    override fun getTokenEnd(): Int = currentOffset

    override fun advance() {
        if (currentOffset >= endOffset) {
            tokenType = null
            return
        }

        startOffset = currentOffset
        val c = buffer[currentOffset]

        when {
            c.isWhitespace() -> {
                while (currentOffset < endOffset && buffer[currentOffset].isWhitespace()) {
                    currentOffset++
                }
                tokenType = GalaTypes.WHITE_SPACE
            }
            c == '/' && currentOffset + 1 < endOffset && buffer[currentOffset + 1] == '/' -> {
                // Single-line comment
                while (currentOffset < endOffset && buffer[currentOffset] != '\n') {
                    currentOffset++
                }
                tokenType = GalaTypes.COMMENT
            }
            c == '/' && currentOffset + 1 < endOffset && buffer[currentOffset + 1] == '*' -> {
                // Multi-line comment
                currentOffset += 2
                while (currentOffset + 1 < endOffset) {
                    if (buffer[currentOffset] == '*' && buffer[currentOffset + 1] == '/') {
                        currentOffset += 2
                        break
                    }
                    currentOffset++
                }
                if (currentOffset >= endOffset) currentOffset = endOffset
                tokenType = GalaTypes.COMMENT
            }
            c == '"' -> {
                // Regular string
                currentOffset++
                while (currentOffset < endOffset && buffer[currentOffset] != '"') {
                    if (buffer[currentOffset] == '\\' && currentOffset + 1 < endOffset) {
                        currentOffset++
                    }
                    currentOffset++
                }
                if (currentOffset < endOffset) currentOffset++
                tokenType = GalaTypes.STRING
            }
            c == '`' -> {
                // Raw string (backtick)
                currentOffset++
                while (currentOffset < endOffset && buffer[currentOffset] != '`') {
                    currentOffset++
                }
                if (currentOffset < endOffset) currentOffset++
                tokenType = GalaTypes.STRING
            }
            c.isLetter() || c == '_' -> {
                val start = currentOffset
                while (currentOffset < endOffset && (buffer[currentOffset].isLetterOrDigit() || buffer[currentOffset] == '_')) {
                    currentOffset++
                }
                val text = buffer.subSequence(start, currentOffset).toString()
                tokenType = when {
                    text in keywords -> GalaTypes.KEYWORD
                    text in builtinTypes -> GalaTypes.TYPE
                    else -> GalaTypes.IDENTIFIER
                }
            }
            c.isDigit() -> {
                while (currentOffset < endOffset && buffer[currentOffset].isDigit()) {
                    currentOffset++
                }
                if (currentOffset < endOffset && buffer[currentOffset] == '.' && currentOffset + 1 < endOffset && buffer[currentOffset + 1].isDigit()) {
                    currentOffset++
                    while (currentOffset < endOffset && buffer[currentOffset].isDigit()) {
                        currentOffset++
                    }
                }
                tokenType = GalaTypes.NUMBER
            }
            c == '.' -> {
                if (currentOffset + 1 < endOffset && buffer[currentOffset + 1].isDigit()) {
                    currentOffset++
                    while (currentOffset < endOffset && buffer[currentOffset].isDigit()) {
                        currentOffset++
                    }
                    tokenType = GalaTypes.NUMBER
                } else {
                    currentOffset++
                    tokenType = GalaTypes.DOT
                }
            }
            c == ',' -> {
                currentOffset++
                tokenType = GalaTypes.COMMA
            }
            c == '(' -> {
                currentOffset++
                tokenType = GalaTypes.LPAREN
            }
            c == ')' -> {
                currentOffset++
                tokenType = GalaTypes.RPAREN
            }
            c == '{' -> {
                currentOffset++
                tokenType = GalaTypes.LBRACE
            }
            c == '}' -> {
                currentOffset++
                tokenType = GalaTypes.RBRACE
            }
            c == '[' -> {
                currentOffset++
                tokenType = GalaTypes.LBRACKET
            }
            c == ']' -> {
                currentOffset++
                tokenType = GalaTypes.RBRACKET
            }
            c in "+-*/=<>!&|:%^~" -> {
                val start = currentOffset
                currentOffset++
                if (currentOffset < endOffset) {
                    val next = buffer[currentOffset]
                    val op2 = "$c$next"
                    when {
                        // Three-character operators
                        op2 == "<<" && currentOffset + 1 < endOffset && buffer[currentOffset + 1] == '=' -> {
                            currentOffset += 2 // <<=
                        }
                        op2 == ">>" && currentOffset + 1 < endOffset && buffer[currentOffset + 1] == '=' -> {
                            currentOffset += 2 // >>=
                        }
                        op2 == "&^" && currentOffset + 1 < endOffset && buffer[currentOffset + 1] == '=' -> {
                            currentOffset += 2 // &^=
                        }
                        // Ellipsis (...)
                        c == '.' && next == '.' && currentOffset + 1 < endOffset && buffer[currentOffset + 1] == '.' -> {
                            currentOffset += 2
                        }
                        // Two-character operators
                        op2 in setOf("=>", ":=", "==", "!=", "<=", ">=", "&&", "||",
                                    "++", "--", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=",
                                    "<<", ">>", "&^", "<-") -> {
                            currentOffset++
                        }
                    }
                }
                tokenType = GalaTypes.OPERATOR
            }
            else -> {
                currentOffset++
                tokenType = GalaTypes.BAD_CHARACTER
            }
        }
    }

    override fun getBufferSequence(): CharSequence = buffer

    override fun getBufferEnd(): Int = endOffset
}
