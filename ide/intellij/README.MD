# GALA IntelliJ Plugin

This directory contains the source code for the GALA language support plugin for IntelliJ IDEA.

## Features

- **Syntax highlighting** for:
  - Keywords (`val`, `var`, `func`, `match`, `case`, `if`, `else`, `for`, etc.)
  - Built-in types (`int`, `string`, `bool`, `float64`, `any`, etc.)
  - Strings (regular `"..."` and raw backtick strings)
  - Comments (single-line `//` and multi-line `/* */`)
  - Numbers (integers and floats)
  - Operators (`=>`, `:=`, `++`, `--`, `...`, etc.)
- **Code completion** for keywords, built-in types, and standard library types (`Option`, `Either`, `Tuple`, `List`, `Array`)
- **Brace matching** for `()`, `{}`, `[]`
- **Code folding** for blocks
- **Comment/uncomment** support (Ctrl+/ for line, Ctrl+Shift+/ for block)
- **Structure view** for navigating code
- **File type recognition** (`.gala`)

## How to Build

### Using Bazel (Recommended)

The plugin can be built using Bazel from the project root:

```bash
bazel build //ide/intellij:plugin
```

The resulting ZIP file will be located at `bazel-bin/ide/intellij/gala-intellij-plugin.zip`.

### Using Gradle

To build the plugin directly with Gradle, you need to have JDK 17 installed. Use the provided Gradle wrapper (if available) or your local Gradle installation.

Run the following command in this directory:

```bash
gradle buildPlugin
```

This will produce a ZIP file in `build/distributions/gala-intellij-plugin-1.1.0.zip`.

## How to Install

1. Open IntelliJ IDEA.
2. Go to `Settings` (or `Preferences` on macOS) > `Plugins`.
3. Click the gear icon (top right) and select `Install Plugin from Disk...`.
4. Navigate to the `build/distributions/` folder and select the produced ZIP file.
5. Restart IntelliJ IDEA if prompted.

## Important Note

Do NOT zip the source code directory itself and try to install it. IntelliJ IDEA requires a specific internal structure (JARs in a `lib` folder) which is automatically created by the `buildPlugin` Gradle task.
