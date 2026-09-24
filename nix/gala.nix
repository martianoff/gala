# GALA, built from source with the same bootstrap pipeline Bazel uses:
#
#   1. ANTLR generates internal/parser/grammar/*.go from gala.g4
#   2. cmd/gala_bootstrap (transpiler without stdlib embedding) is built
#   3. every stdlib .gala file listed in internal/stdlib/BUILD.bazel's
#      generate_embedded genrule is transpiled to <name>.gen.go
#   4. cmd/stdlib_gen packs those files (plus the hand-written .go helpers)
#      into internal/stdlib/embedded_gen.go
#   5. cmd/gala is built with the stdlib embedded
#
# Everything except the grammar invocation is driven from the checked-in
# BUILD files, so stdlib additions/removals are picked up automatically.
{
  lib,
  buildGoModule,
  fetchurl,
  go,
  jdk21,
  version ? (
    let
      line = lib.findFirst (l: lib.hasPrefix "gala " l) "gala 0.0.0" (
        lib.splitString "\n" (builtins.readFile ../gala.mod)
      );
    in
    lib.removePrefix "gala " line
  ),
  # `source` rather than `src`: callPackage would otherwise hand us
  # pkgs.src, which nixpkgs defines as a throw.
  source ? lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions (
      [
        # Module metadata. gala.mod is also the file `version` above is
        # read from, and the transpiler's module resolver looks it up
        # relative to the search path.
        ../gala.mod
        ../go.mod
        ../go.sum

        # Everything `go build ./cmd/...` compiles, plus the Bazel
        # metadata and ANTLR grammar the preBuild pipeline reads.
        ../cmd
        ../internal
        ../galaerr
        ../docs/errors # errdocs.go embeds GALA-E*.md
      ]
      # Stdlib packages referenced by internal/stdlib/BUILD.bazel's
      # generate_embedded genrule: transpiled .gala sources, .gala files
      # embedded verbatim, and hand-written .go helpers.
      ++ map (pkg: ../. + "/${pkg}") [
        "collection_immutable"
        "collection_mutable"
        "concurrent"
        "crypto"
        "fs"
        "go_builtins"
        "go_interop"
        "io"
        "json"
        "lazy"
        "path"
        "regex"
        "resource"
        "std"
        "stream"
        "strings"
        "subprocess"
        "test"
        "time_utils"
        "validation"
        "yaml"
      ]
      # `go mod vendor` scans every main-module package, including these
      # Go test fixtures; dropping them would change the vendor tree and
      # so break vendorHash.
      ++ map (dir: lib.fileset.fileFilter (file: file.hasExt "go") (../. + "/${dir}")) [
        "bazel_test_fixtures"
        "examples"
      ]
    );
  },
}:

buildGoModule (finalAttrs: {
  pname = "gala";
  src = source;
  inherit version;

  subPackages = [ "cmd/gala" ];

  vendorHash = "sha256-elG9Jqrh0Bug4xWgVVpsPZRR21TWtjeQD7jkffkix34=";

  # The dependency-fetching fixed-output derivation inherits preBuild, but
  # must not run the generation pipeline below (it needs the vendor tree it
  # is itself producing). Keep it to a plain `go mod vendor`.
  overrideModAttrs = _final: _prev: {
    preBuild = "";
  };

  # Keep in sync with MODULE.bazel's @antlr4_tool http_jar.
  antlr4-jar = fetchurl {
    url = "https://www.antlr.org/download/antlr-4.13.1-complete.jar";
    hash = "sha256-vBOpxXqN19UZaIghHl7eZXy2SjzpaGCGl+T2aCUahIc=";
  };

  nativeBuildInputs = [
    go
    jdk21
  ];

  ldflags = [
    "-X martianoff/gala/cmd/gala/commands.Version=${version}"
  ];

  # The upstream test suite is Bazel-driven (golden files, registered
  # toolchains, no-sandbox genrules); it is not runnable from a plain
  # `go test` sandbox. The flake's `checks` run a real build/run smoke
  # test against the installed binary instead.
  doCheck = false;

  preBuild = ''
    set -x

    # 1. ANTLR Go parser (Bazel: //internal/parser/grammar:gala_grammar_go).
    echo "gala: [1/4] generating ANTLR parser from internal/parser/grammar/gala.g4"
    antlrOut="$TMPDIR/antlr"
    mkdir -p "$antlrOut" internal/parser/grammar
    java -jar "${finalAttrs.antlr4-jar}" -Dlanguage=Go -package grammar \
      -o "$antlrOut" internal/parser/grammar/gala.g4
    cp "$antlrOut"/internal/parser/grammar/*.go internal/parser/grammar/
    sed -i 's|github.com/antlr/antlr4/runtime/Go/antlr|github.com/antlr4-go/antlr/v4|g' \
      internal/parser/grammar/*.go
    echo "gala: generated $(ls internal/parser/grammar/*.go | wc -l) parser files"

    # 2. Bootstrap transpiler and stdlib snapshot generator.
    echo "gala: [2/4] building gala_bootstrap and stdlib_gen"
    go build -mod=vendor -o "$TMPDIR/gala_bootstrap" ./cmd/gala_bootstrap
    go build -mod=vendor -o "$TMPDIR/stdlib_gen" ./cmd/stdlib_gen

    # 3. Collect the embed inputs exactly as internal/stdlib/BUILD.bazel's
    #    generate_embedded genrule declares them: "//pkg:target_go" labels
    #    are bootstrap-transpiled .gala files, everything else is source
    #    (.gala files to embed verbatim, .go helpers to bundle as-is).
    echo "gala: [3/4] transpiling stdlib sources from internal/stdlib/BUILD.bazel"
    embeddedSrcs="$TMPDIR/embedded_srcs.txt"
    awk '/name = "generate_embedded"/,/outs = \["embedded_gen.go"\]/' \
      internal/stdlib/BUILD.bazel \
      | grep -o '"//[^"]*:[^"]*"' | tr -d '"' | sort -u > "$embeddedSrcs"
    echo "gala: collected $(wc -l < "$embeddedSrcs") embed inputs"

    mkdir -p "$TMPDIR/transpiled"
    batchInputs=()
    batchOutputs=()
    files=()
    while IFS= read -r label; do
      pkg="''${label#//}"
      pkg="''${pkg%%:*}"
      file="''${label##*:}"
      case "$file" in
        *_go)
          stem="''${file%_go}"
          # Resolve the target to its src attribute so a renamed .gala file
          # is still transpiled from the right source.
          srcfile=$(awk -v name="\"$file\"" '
            $0 ~ ("name = " name) { found = 1; next }
            found && /src = "/ {
              match($0, /src = "[^"]+"/)
              print substr($0, RSTART + 7, RLENGTH - 8)
              exit
            }
          ' "$pkg/BUILD.bazel")
          if [ -z "$srcfile" ]; then
            srcfile="$stem.gala"
          fi
          mkdir -p "$TMPDIR/transpiled/$pkg"
          batchInputs+=("$pkg/$srcfile")
          batchOutputs+=("$TMPDIR/transpiled/$pkg/$stem.gen.go")
          files+=("$TMPDIR/transpiled/$pkg/$stem.gen.go")
          ;;
        *)
          echo "gala: embedding $pkg/$file verbatim"
          files+=("$pkg/$file")
          ;;
      esac
    done < "$embeddedSrcs"

    # One process for every transpile: the expensive transitive type graph
    # (std, collection_immutable, ...) is built once and reused across files
    # instead of once per `gala_bootstrap` fork. Siblings are derived per
    # directory inside the tool, matching the Bazel worker's batch shape.
    batchInputsJoined=$(IFS=,; echo "''${batchInputs[*]}")
    batchOutputsJoined=$(IFS=,; echo "''${batchOutputs[*]}")
    echo "gala: transpiling ''${#batchInputs[@]} .gala files in one batch"
    "$TMPDIR/gala_bootstrap" \
      --inputs "$batchInputsJoined" \
      --outputs "$batchOutputsJoined" \
      --search "$PWD" \
      --goroot="$(go env GOROOT)"
    echo "gala: transpiled $(ls "$TMPDIR"/transpiled/*/*.gen.go 2>/dev/null | wc -l) .gala files"

    # 4. internal/stdlib/embedded_gen.go.
    echo "gala: [4/4] packing ''${#files[@]} files into internal/stdlib/embedded_gen.go"
    "$TMPDIR/stdlib_gen" -output internal/stdlib/embedded_gen.go "''${files[@]}"
  '';

  meta = {
    description = "Functional programming language that transpiles to Go";
    homepage = "https://github.com/martianoff/gala";
    license = lib.licenses.asl20;
    mainProgram = "gala";
    platforms = lib.platforms.unix;
  };
})
