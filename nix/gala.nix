# GALA, built from source.
#
# The stdlib is written in GALA, so compiling it needs a working GALA
# transpiler. The full CLI embeds the stdlib it would have to transpile, so it
# cannot produce that stdlib itself; instead the stdlib is transpiled by a
# downloaded release binary (`releaseGalaBin`). `cmd/gala_bootstrap` does not
# import `internal/stdlib`, so it can be built from this tree and serves as the
# `useLocalBootstrap` escape hatch.
#
#   localAntlr       gala.g4 -> internal/parser/grammar/*.go
#   localTranspiled  releaseGalaBin transpiles the local stdlib, one
#                    `transpile-package` invocation per package (every
#                    non-test .gala file as that package's input list, which
#                    is directory-scan semantics)
#   gala             cmd/gala built from the local Go sources, with the local
#                    grammar overlaid and the stdlib packed in by
#                    cmd/stdlib_gen built from those same sources
#
# Incrementality:
#   edit Go under internal/ or cmd/   -> gala rebuilds
#   edit a stdlib .gala file          -> localTranspiled + gala
#   edit gala.g4                      -> localAntlr + gala
#   bump galaReleaseVersion           -> releaseGalaBin + localTranspiled + gala
#
# `useLocalBootstrap = true` (packaged as `.#gala-local`) replaces the release
# binary with `cmd/gala_bootstrap` built from this tree, for grammar or codegen
# work the release cannot handle yet. The trade: any Go edit then rebuilds the
# bootstrap and retranspiles the stdlib.
{
  lib,
  buildGoModule,
  fetchurl,
  go,
  jdk21,
  stdenv,
  # Escape hatch: transpile the stdlib with cmd/gala_bootstrap built from this
  # tree instead of a downloaded release binary.
  useLocalBootstrap ? false,
  # Downloaded release binary used to transpile the stdlib. Bump the version
  # and all four hashes together; the asset names match .github/workflows/
  # release.yml. A release can lag the tree (codegen/grammar changes), in
  # which case `gala-local` is the escape hatch.
  galaReleaseVersion ? "0.87.1",
  galaReleaseHashes ? {
    x86_64-linux = "sha256-vZkFXvMVDvDVRLQAJNRSsG5B0DnslAIbtOcF+yvp330=";
    aarch64-linux = "sha256-xf3BqXKJzZwURkHP5XMs5zlNHavI0iPUnoozaPB9ecs=";
    x86_64-darwin = "sha256-xBtGdKmYggh+IZS4hEQGtXtBP0eEfa8uEormQYcQpV8=";
    aarch64-darwin = "sha256-o0MEXVm5p1IxJaihAOcxRwinPuyCoJ+Xid4nGEYUGaM=";
  },
  # Hash of the vendored Go dependencies in the current go.mod. Run
  # `nix build` once and copy the hash it suggests if this goes stale.
  galaVendorHash ? "sha256-jMYrSlutgTyeKv+n4J3gdP0LmBKFd4O8QQG5eJBkQB8=",
  version ? (
    let
      line = lib.findFirst (l: lib.hasPrefix "gala " l) "gala 0.0.0" (
        lib.splitString "\n" (builtins.readFile ../gala.mod)
      );
    in
    lib.removePrefix "gala " line
  ),
  repoFileset ? lib.fileset.unions (
    [
      # Module metadata. gala.mod is also the file `version` above is read
      # from, and the transpiler's module resolver looks it up relative to
      # the search path.
      ../gala.mod
      ../go.mod
      ../go.sum

      # Everything `go build ./cmd/...` compiles, plus the Bazel metadata and
      # ANTLR grammar the stdlib script reads.
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
    # `go mod vendor` scans every main-module package, including these Go test
    # fixtures; dropping them would change the vendor tree and so break
    # vendorHash.
    ++ map (dir: lib.fileset.fileFilter (file: file.hasExt "go") (../. + "/${dir}")) [
      "bazel_test_fixtures"
      "examples"
    ]
  ),
}:

let
  repoRoot = ../.;

  stdlibPkgs = [
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
  ];

  galaFileset = lib.fileset.intersection repoFileset (
    lib.fileset.fileFilter (file: file.hasExt "gala") repoRoot
  );

  # Go-only source for Go builds: .gala edits must not invalidate them.
  goSource = lib.fileset.toSource {
    root = repoRoot;
    fileset = lib.fileset.difference repoFileset galaFileset;
  };

  # Stdlib source tree for the transpile + stdlib_gen steps. Whole package
  # dirs so hand-written .go helpers and verbatim .gala sources are present;
  # the BUILD files are read by the stdlib script, and gala.mod/go.mod/go.sum
  # by the resolver.
  stdlibSource = lib.fileset.toSource {
    root = repoRoot;
    fileset = lib.fileset.unions (
      [
        (repoRoot + "/gala.mod")
        (repoRoot + "/go.mod")
        (repoRoot + "/go.sum")
        (repoRoot + "/internal/stdlib/BUILD.bazel")
      ]
      ++ map (p: repoRoot + "/${p}") stdlibPkgs
    );
  };

  # Keep in sync with MODULE.bazel's @antlr4_tool http_jar.
  antlrJar = fetchurl {
    url = "https://www.antlr.org/download/antlr-4.13.1-complete.jar";
    hash = "sha256-vBOpxXqN19UZaIghHl7eZXy2SjzpaGCGl+T2aCUahIc=";
  };

  mkAntlr =
    label: grammarFile:
    stdenv.mkDerivation {
      pname = "gala-antlr-${label}";
      inherit version;
      dontUnpack = true;
      dontConfigure = true;
      dontInstall = true;
      nativeBuildInputs = [ jdk21 ];
      buildPhase = ''
        runHook preBuild
        mkdir -p "$out/internal/parser/grammar" internal/parser/grammar
        # ANTLR requires the basename to equal the grammar name and mirrors
        # the input's relative path under -o, so stage a relative copy
        # (passing the store path directly would write flat into antlr/).
        cp ${grammarFile} internal/parser/grammar/gala.g4
        java -jar ${antlrJar} -Dlanguage=Go -package grammar \
          -o antlr internal/parser/grammar/gala.g4
        cp antlr/internal/parser/grammar/*.go "$out/internal/parser/grammar/"
        sed -i 's|github.com/antlr/antlr4/runtime/Go/antlr|github.com/antlr4-go/antlr/v4|g' \
          "$out"/internal/parser/grammar/*.go
        runHook postBuild
      '';
    };
  localAntlr = mkAntlr "local" (repoRoot + "/internal/parser/grammar/gala.g4");

  # Downloaded release binary used to transpile the stdlib. fetchurl does not
  # preserve the executable bit, so wrap it with install -m755. The tool runs
  # at build time, so select the asset by buildPlatform, not hostPlatform.
  releaseAssets = {
    x86_64-linux = "gala-linux-amd64";
    aarch64-linux = "gala-linux-arm64";
    x86_64-darwin = "gala-darwin-amd64";
    aarch64-darwin = "gala-darwin-arm64";
  };
  releaseSystem = stdenv.buildPlatform.system;
  releaseGalaBin = stdenv.mkDerivation {
    pname = "gala-release-bin";
    version = galaReleaseVersion;
    src = fetchurl {
      url = "https://github.com/martianoff/gala/releases/download/${galaReleaseVersion}/${releaseAssets.${releaseSystem}}";
      hash = galaReleaseHashes.${releaseSystem};
    };
    dontUnpack = true;
    dontConfigure = true;
    dontBuild = true;
    dontFixup = true;
    installPhase = ''
      mkdir -p "$out/bin"
      install -m755 "$src" "$out/bin/gala"
    '';
  };

  # Escape hatch for grammar work the release cannot parse: build the minimal
  # transpiler from this tree. cmd/gala_bootstrap does not import
  # internal/stdlib, so no cycle.
  localBootstrap = buildGoModule {
    pname = "gala-bootstrap-local";
    inherit version;
    src = goSource;
    vendorHash = galaVendorHash;
    subPackages = [ "cmd/gala_bootstrap" ];
    doCheck = false;
    overrideModAttrs = _final: _prev: {
      preBuild = "";
      postPatch = "";
    };
    postPatch = ''
      cp ${localAntlr}/internal/parser/grammar/*.go internal/parser/grammar/
    '';
  };

  localTranspiled = mkTranspiled {
    name = "gala-stdlib";
    transpiler =
      if useLocalBootstrap then "${localBootstrap}/bin/gala_bootstrap" else "${releaseGalaBin}/bin/gala";
    perPackage = !useLocalBootstrap;
  };

  # Transpiles the stdlib and stages the generate_embedded inputs under
  # $out/files:
  #   - transpiled .gala targets as <name>.gen.go
  #   - verbatim .gala/.go inputs under their original names
  # The final `gala` build packs them with cmd/stdlib_gen; keeping the pack
  # step out of this derivation means a Go edit does not retranspile the
  # stdlib, and a stdlib edit does not rebuild any Go derivation.
  #
  # Every non-test .gala file on disk is transpiled (release transpile-package
  # calls list all of them, so its sibling set matches a directory scan), but
  # only the generate_embedded set is staged; a file missing from that list is
  # transpiled and dropped (//internal/stdlib:stdlib_test fails on such a gap).
  mkTranspiled =
    {
      name,
      transpiler,
      # Release binaries before --scan need one transpile-package invocation
      # per package; gala_bootstrap's batch mode takes all files at once.
      perPackage,
    }:
    stdenv.mkDerivation {
      pname = name;
      inherit version;
      src = stdlibSource;
      nativeBuildInputs = [ go ];
      dontConfigure = true;
      dontInstall = true;

      # The release binary extracts its embedded stdlib into GALA_HOME (and
      # falls back to HOME); both must be writable in the sandbox. --search
      # "$PWD" comes first, so the local stdlib wins over the extracted one.
      GALA_HOME = "$TMPDIR/gala-home";
      HOME = "$TMPDIR/home";

      buildPhase = ''
        runHook preBuild
        shopt -s nullglob
        mkdir -p "$out/files" "$TMPDIR/transpiled" "$GALA_HOME" "$HOME"
        embeddedSrcs="$TMPDIR/embedded_srcs.txt"
        awk '/name = "generate_embedded"/,/outs = \["embedded_gen.go"\]/' \
          internal/stdlib/BUILD.bazel \
          | grep -o '"//[^"]*:[^"]*"' | tr -d '"' | sort -u > "$embeddedSrcs"
      ''
      + lib.optionalString perPackage ''
        for pkg in ${lib.escapeShellArgs stdlibPkgs}; do
          batchInputs=()
          batchOutputs=()
          for f in "$pkg"/*.gala; do
            case "$f" in *_test.gala) continue ;; esac
            stem="''${f##*/}"
            batchInputs+=("$f")
            batchOutputs+=("$TMPDIR/transpiled/$pkg/''${stem%.gala}.gen.go")
          done
          [ "''${#batchInputs[@]}" -eq 0 ] && continue
          mkdir -p "$TMPDIR/transpiled/$pkg"
          echo "gala: transpiling ''${#batchInputs[@]} files in $pkg"
          ${transpiler} transpile-package \
            --inputs "$(IFS=,; echo "''${batchInputs[*]}")" \
            --outputs "$(IFS=,; echo "''${batchOutputs[*]}")" \
            --search "$PWD" --goroot="${go}/share/go"
        done
      ''
      + lib.optionalString (!perPackage) ''
        batchInputs=()
        batchOutputs=()
        for pkg in ${lib.escapeShellArgs stdlibPkgs}; do
          for f in "$pkg"/*.gala; do
            case "$f" in *_test.gala) continue ;; esac
            stem="''${f##*/}"
            batchInputs+=("$f")
            batchOutputs+=("$TMPDIR/transpiled/$pkg/''${stem%.gala}.gen.go")
          done
        done
        echo "gala: transpiling ''${#batchInputs[@]} files (local bootstrap)"
        GOGC=300 GOMEMLIMIT=6GiB ${transpiler} \
          --inputs "$(IFS=,; echo "''${batchInputs[*]}")" \
          --outputs "$(IFS=,; echo "''${batchOutputs[*]}")" \
          --search "$PWD" --goroot="${go}/share/go"
      ''
      + ''
        # Stage only the embedded set: transpiled files must not carry the
        # extra outputs (or stdlib_gen would embed them).
        while IFS= read -r label; do
          pkg="''${label#//}"
          pkg="''${pkg%%:*}"
          file="''${label##*:}"
          case "$file" in
            *_go)
              stem="''${file%_go}"
              # Resolve the target to its src attribute so a renamed .gala
              # file is still staged from the right transpiled output.
              srcfile=$(awk -v name="\"$file\"" '
                $0 ~ ("name = " name) { found = 1; next }
                found && /src = "/ {
                  match($0, /src = "[^"]+"/)
                  print substr($0, RSTART + 7, RLENGTH - 8)
                  exit
                }' "$pkg/BUILD.bazel")
              [ -n "$srcfile" ] || srcfile="$stem.gala"
              srcstem="''${srcfile%.gala}"
              mkdir -p "$out/files/$pkg"
              cp "$TMPDIR/transpiled/$pkg/$srcstem.gen.go" "$out/files/$pkg/$stem.gen.go"
              ;;
            *)
              mkdir -p "$out/files/$pkg"
              cp "$pkg/$file" "$out/files/$pkg/$file"
              ;;
          esac
        done < "$embeddedSrcs"
        echo "gala: staged $(find "$out/files" -type f | wc -l) stdlib files"
        runHook postBuild
      '';
    };
in
let
  gala = buildGoModule {
    pname = "gala";
    src = goSource;
    inherit version;

    subPackages = [ "cmd/gala" ];

    vendorHash = galaVendorHash;

    # The dependency-fetching fixed-output derivation inherits preBuild and
    # postPatch, but must not run the overlay below.
    overrideModAttrs = _final: _prev: {
      preBuild = "";
      postPatch = "";
    };

    ldflags = [
      "-X martianoff/gala/cmd/gala/commands.Version=${version}"
    ];

    # The upstream test suite is Bazel-driven (golden files, registered
    # toolchains, no-sandbox genrules); it is not runnable from a plain
    # `go test` sandbox. The flake's `checks` run a real build/run smoke
    # test against the installed binary instead.
    doCheck = false;

    # Overlay the local grammar and pack the stdlib staged by localTranspiled.
    # The stdlib is packed here rather than in localTranspiled so this
    # derivation is the only one that sees the Go source.
    preBuild = ''
      cp ${localAntlr}/internal/parser/grammar/*.go internal/parser/grammar/
      go build -mod=vendor -o "$TMPDIR/stdlib_gen" ./cmd/stdlib_gen
      "$TMPDIR/stdlib_gen" -output internal/stdlib/embedded_gen.go \
        ${localTranspiled}/files/*/*
    '';

    meta = {
      description = "Functional programming language that transpiles to Go";
      homepage = "https://github.com/martianoff/gala";
      license = lib.licenses.asl20;
      mainProgram = "gala";
      platforms = lib.platforms.unix;
    };
  };
in
gala.overrideAttrs (old: {
  passthru = (old.passthru or { }) // {
    inherit
      releaseGalaBin
      localAntlr
      localBootstrap
      localTranspiled
      ;
  };
})
