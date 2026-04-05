package org.gala.ide.intellij

import com.intellij.testFramework.ParsingTestCase

/**
 * Tests that the ANTLR-based parser produces valid PSI trees for GALA source files.
 * Each test parses a .gala fixture file and verifies no errors in the PSI tree.
 */
class GalaParsingTest : ParsingTestCase("", "gala", GalaParserDefinition()) {

    override fun getTestDataPath(): String = "src/test/testData"

    override fun skipSpaces(): Boolean = true
    override fun includeRanges(): Boolean = true

    fun testSimpleFunction() {
        doTest(true)
    }

    fun testSealedType() {
        doTest(true)
    }

    fun testImports() {
        doTest(true)
    }

    fun testTypeDeclarations() {
        doTest(true)
    }

    fun testLambdas() {
        doTest(true)
    }

    fun testStringInterpolation() {
        doTest(true)
    }
}
