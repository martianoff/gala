package org.gala.ide.intellij

import com.intellij.codeInsight.completion.*
import com.intellij.patterns.PlatformPatterns
import com.intellij.util.ProcessingContext

/**
 * Minimal completion contributor for GALA.
 *
 * The LSP server (gala lsp) provides all smart completions:
 * keywords, types, methods, named args, match cases, etc.
 *
 * This contributor is intentionally empty — it exists only to
 * register the GALA language for the completion framework.
 * All actual completion items come from the LSP server.
 */
class GalaCompletionContributor : CompletionContributor()
