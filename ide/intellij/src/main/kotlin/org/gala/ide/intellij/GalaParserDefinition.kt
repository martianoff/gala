package org.gala.ide.intellij

import com.intellij.lang.ASTNode
import com.intellij.lang.ParserDefinition
import com.intellij.lang.PsiParser
import com.intellij.lexer.Lexer
import com.intellij.openapi.project.Project
import com.intellij.psi.FileViewProvider
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiFile
import com.intellij.psi.tree.IFileElementType
import com.intellij.psi.tree.TokenSet
import org.antlr.intellij.adaptor.lexer.ANTLRLexerAdaptor
import org.antlr.intellij.adaptor.parser.ANTLRParserAdaptor
import org.antlr.v4.runtime.Parser
import org.antlr.v4.runtime.tree.ParseTree
import org.gala.ide.intellij.parser.galaLexer
import org.gala.ide.intellij.parser.galaParser
import org.gala.ide.intellij.psi.*

class GalaParserDefinition : ParserDefinition {
    companion object {
        val FILE = IFileElementType(GalaLanguage)

        init {
            // Trigger GalaTokenTypes initialization, which registers ANTLR tokens
            // with PSIElementTypeFactory. Must happen before any lexer/parser usage.
            @Suppress("unused")
            val ensureInit = GalaTokenTypes.WHITESPACE
        }
    }

    override fun createLexer(project: Project?): Lexer {
        val lexer = galaLexer(null)
        return ANTLRLexerAdaptor(GalaLanguage, lexer)
    }

    override fun createParser(project: Project?): PsiParser {
        val parser = galaParser(null)
        return object : ANTLRParserAdaptor(GalaLanguage, parser) {
            override fun parse(parser: Parser, root: com.intellij.psi.tree.IElementType): ParseTree {
                return (parser as galaParser).sourceFile()
            }
        }
    }

    override fun getWhitespaceTokens(): TokenSet = GalaTokenTypes.WHITESPACE
    override fun getCommentTokens(): TokenSet = GalaTokenTypes.COMMENTS
    override fun getStringLiteralElements(): TokenSet = GalaTokenTypes.STRINGS
    override fun getFileNodeType(): IFileElementType = FILE

    override fun createFile(viewProvider: FileViewProvider): PsiFile = GalaFile(viewProvider)

    override fun spaceExistenceTypeBetweenTokens(
        left: ASTNode?,
        right: ASTNode?
    ): ParserDefinition.SpaceRequirements = ParserDefinition.SpaceRequirements.MAY

    override fun createElement(node: ASTNode): PsiElement {
        val elementType = node.elementType
        return when (elementType) {
            GalaTokenTypes.RULE_FUNCTION_DECLARATION -> FunctionDeclarationNode(node)
            GalaTokenTypes.RULE_TYPE_DECLARATION -> TypeDeclarationNode(node)
            GalaTokenTypes.RULE_SEALED_TYPE_DECLARATION -> SealedTypeDeclarationNode(node)
            GalaTokenTypes.RULE_SEALED_CASE -> SealedCaseNode(node)
            GalaTokenTypes.RULE_VAL_DECLARATION -> ValDeclarationNode(node)
            GalaTokenTypes.RULE_VAR_DECLARATION -> VarDeclarationNode(node)
            GalaTokenTypes.RULE_STRUCT_SHORTHAND_DECLARATION -> StructShorthandDeclarationNode(node)
            GalaTokenTypes.RULE_IMPORT_DECLARATION -> ImportDeclarationNode(node)
            GalaTokenTypes.RULE_BLOCK -> BlockNode(node)
            GalaTokenTypes.RULE_METHOD_SPEC -> MethodSpecNode(node)
            GalaTokenTypes.RULE_IDENTIFIER -> IdentifierNode(node)
            else -> GalaPsiNode(node)
        }
    }
}
