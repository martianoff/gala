# GALA-E0010 — Duplicate package name in directory

**When it fires.** Two or more `.gala` files in the same directory declare
different `package` names, and neither file is a `_test.gala` file. Test
files may diverge (like Go's `pkg_test` convention); regular sources cannot.

**Minimal repro.**

```
lib/a.gala   // package mylib
lib/b.gala   // package other
```

`lib/a.gala`:

```gala
package mylib

func Hello() string = "hello"
```

`lib/b.gala`:

```gala
package other

func Bye() string = "bye"
```

**Error output.** Two details are easy to misread:

* the message names the **sibling** file and the sibling's package (`b.gala`
  / `"other"`), while `but sibling files declare "mylib"` reports the package
  of the file currently being compiled; and
* the caret is on the `package` keyword of the **file being compiled**
  (`a.gala`), not on the sibling the message names.

```text
error[GALA-E0010]: package file lib/b.gala declares package "other" but sibling files declare "mylib"
  --> lib/a.gala:1:1
  |
1 | package mylib
  | ^^^^^^^ use the same package name across all sibling .gala files, or…
  |
  = hint: use the same package name across all sibling .gala files, or move the file to a different directory
```

The paths are shown here workspace-relative; the CLI prints them absolute.
Which file appears where depends on which one the compiler reached first, so
the two names may be swapped in your output.

**Second wording.** The analyzer raises this code from two places. Sibling
*discovery* produces the message above; the later sibling-*filtering* pass
produces a differently-worded one that names the directory instead:

```text
error[GALA-E0010]: directory lib has files with different package names: "other" and "mylib"
```

(First name = the file being compiled, second = the sibling; the directory is
printed absolute.) Both wordings carry the same code and the same hint.
`gala build` normally surfaces the first.

**Fix.** Either rename one of the packages so both files agree, or move the
outlier file into its own directory.

**Rationale.** GALA's sibling-file resolution treats every `.gala` file in a
directory as part of the same compilation unit, so conflicting package
names break cross-file type resolution. Catching the mismatch at the
analyzer layer is much cheaper than letting it surface as a series of
"unresolved identifier" errors during transform.
