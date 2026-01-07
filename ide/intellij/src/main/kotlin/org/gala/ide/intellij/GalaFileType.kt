package org.gala.ide.intellij

import com.intellij.openapi.fileTypes.LanguageFileType
import javax.swing.Icon

object GalaFileType : LanguageFileType(GalaLanguage) {
    override fun getName(): String = "GALA File"

    override fun getDescription(): String = "GALA language file"

    override fun getDefaultExtension(): String = "gala"

    override fun getIcon(): Icon = GalaIcons.FILE
}
