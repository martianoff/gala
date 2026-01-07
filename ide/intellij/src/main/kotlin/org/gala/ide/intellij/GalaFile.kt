package org.gala.ide.intellij

import com.intellij.extapi.psi.PsiFileBase
import com.intellij.openapi.fileTypes.FileType
import com.intellij.psi.FileViewProvider

class GalaFile(viewProvider: FileViewProvider) : PsiFileBase(viewProvider, GalaLanguage) {
    override fun getFileType(): FileType = GalaFileType
    override fun toString(): String = "GALA File"
}
