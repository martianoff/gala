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
import com.intellij.lang.LanguageUtil
import com.intellij.psi.TokenType

import com.intellij.extapi.psi.ASTWrapperPsiElement

class GalaParserDefinition : ParserDefinition {
    companion object {
        val FILE = IFileElementType(GalaLanguage)
        val WHITE_SPACES = TokenSet.create(GalaTypes.WHITE_SPACE, TokenType.WHITE_SPACE)
        val COMMENTS = TokenSet.create(GalaTypes.COMMENT)
        val STRINGS = TokenSet.create(GalaTypes.STRING)
    }

    override fun createLexer(project: Project?): Lexer = GalaLexer()

    override fun getWhitespaceTokens(): TokenSet = WHITE_SPACES

    override fun getCommentTokens(): TokenSet = COMMENTS

    override fun getStringLiteralElements(): TokenSet = STRINGS

    override fun createParser(project: Project?): PsiParser = PsiParser { root, builder ->
        val rootMarker = builder.mark()
        while (!builder.eof()) {
            builder.advanceLexer()
        }
        rootMarker.done(root)
        builder.treeBuilt
    }

    override fun getFileNodeType(): IFileElementType = FILE

    override fun createFile(viewProvider: FileViewProvider): PsiFile = GalaFile(viewProvider)

    override fun spaceExistenceTypeBetweenTokens(left: ASTNode?, right: ASTNode?): ParserDefinition.SpaceRequirements {
        return ParserDefinition.SpaceRequirements.MAY
    }

    override fun createElement(node: ASTNode): PsiElement = ASTWrapperPsiElement(node)
}
