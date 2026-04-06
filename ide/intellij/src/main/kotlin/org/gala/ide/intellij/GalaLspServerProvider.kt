package org.gala.ide.intellij

import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.platform.lsp.api.LspServerSupportProvider
import com.intellij.platform.lsp.api.ProjectWideLspServerDescriptor
import java.io.File

/**
 * LSP server provider for GALA.
 * Starts `gala lsp` when a .gala file is opened.
 *
 * The gala CLI is expected to be on PATH (installed via `gala install`).
 * Override with GALA_PATH environment variable.
 */
class GalaLspServerSupportProvider : LspServerSupportProvider {
    override fun fileOpened(
        project: Project,
        file: VirtualFile,
        serverStarter: LspServerSupportProvider.LspServerStarter
    ) {
        if (file.extension == "gala") {
            serverStarter.ensureServerStarted(GalaLspServerDescriptor(project))
        }
    }
}

private class GalaLspServerDescriptor(project: Project) :
    ProjectWideLspServerDescriptor(project, "GALA") {

    override fun isSupportedFile(file: VirtualFile): Boolean = file.extension == "gala"

    override fun createCommandLine(): GeneralCommandLine {
        val galaPath = findGalaBinary()
        return GeneralCommandLine(galaPath, "lsp").apply {
            withWorkDirectory(project.basePath)
        }
    }

    private fun findGalaBinary(): String {
        // 1. GALA_PATH env var
        System.getenv("GALA_PATH")?.let { path ->
            if (File(path).exists()) return path
        }

        // 2. Fall back to PATH
        return "gala"
    }
}
