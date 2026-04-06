package org.gala.ide.intellij

import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.platform.lsp.api.LspServerSupportProvider
import com.intellij.platform.lsp.api.ProjectWideLspServerDescriptor

/**
 * LSP server provider for GALA.
 * Starts the gala-lsp server when a .gala file is opened.
 *
 * The server binary is found via:
 * 1. GALA_LSP_PATH environment variable
 * 2. "gala-lsp" on PATH
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
        val lspPath = System.getenv("GALA_LSP_PATH") ?: "gala-lsp"
        return GeneralCommandLine(lspPath, "--stdio").apply {
            withWorkDirectory(project.basePath)
        }
    }
}
