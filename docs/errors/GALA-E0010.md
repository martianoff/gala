# GALA-E0010 — Duplicate package name in directory

**When it fires.** Two or more `.gala` files in the same directory declare
different `package` names, and the current file is not a `_test.gala` file.
Test files may diverge by one character (like Go's `pkg_test` convention);
regular sources cannot.

**Minimal repro.**

```
lib/a.gala  // package mylib
lib/b.gala  // package other   <-- triggers the error
```

**Error output.**

```
[SemanticError GALA-E0010] b.gala:1:0 directory lib has files with different package names: "mylib" and "other" (hint: use the same package name across all sibling .gala files, or move the file to a different directory)
```

**Fix.** Either rename one of the packages so both files agree, or move the
outlier file into its own directory.

**Rationale.** GALA's sibling-file resolution treats every `.gala` file in a
directory as part of the same compilation unit, so conflicting package
names break cross-file type resolution. Catching the mismatch at the
analyzer layer is much cheaper than letting it surface as a series of
"unresolved identifier" errors during transform.
