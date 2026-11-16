# The draft combined `gala-lean` executable, packaged as `.#gala-lean.main`
# (the `gala-lean` group marks the staged Lean ports; see flake.nix).
#
# It is one native binary linking the Lean reference interpreter (galaspec)
# and the three staged ports: interpolation, diagnostics and the gala.mod
# reader/writer. It is a development tool, like galaspec and the ports, and
# never ships with GALA (docs/FORMAL_SEMANTICS.MD, "Licensing").
#
# The build is Lake's, not a hand-written Lean -> C pipeline: the package at
# formal/gala-lean/ borrows the port sources by directory, so there is one
# build graph and no duplicated list of C translation units.
{
  lib,
  stdenv,
  lean4,
  version ? "0.1.0-draft",
}:

stdenv.mkDerivation {
  pname = "gala-lean";
  inherit version;

  # The Lake package needs its own sources plus the modules it imports from
  # the reference semantics and the three ports. Only Lean sources (and the
  # one fixture the check runs) take part; the ports' build/ directories,
  # harnesses and Go oracles stay out. `Gala.lean` is the root module a newer
  # formal/Gala ships; this branch's copy has none, hence `maybeMissing`.
  src = lib.fileset.toSource {
    root = ../formal;
    fileset = lib.fileset.unions [
      ../formal/gala-lean/lakefile.toml
      (lib.fileset.fileFilter (file: file.hasExt "lean") ../formal/gala-lean)
      (lib.fileset.maybeMissing ../formal/Gala.lean)
      (lib.fileset.fileFilter (file: file.hasExt "lean") ../formal/Gala)
      (lib.fileset.fileFilter (file: file.hasExt "lean") ../formal/interpolation)
      (lib.fileset.fileFilter (file: file.hasExt "lean") ../formal/diagnostics)
      (lib.fileset.fileFilter (file: file.hasExt "lean") ../formal/manifest)
      ../formal/tests/ex_hex_literals.json
      ../formal/tests/ex_hex_literals.out
    ];
  };

  nativeBuildInputs = [ lean4 ];
  dontConfigure = true;

  buildPhase = ''
    runHook preBuild
    ( cd gala-lean && lake build gala-lean )
    runHook postBuild
  '';

  # Run a real program through the reference interpreter, so the derivation
  # checks the combination rather than only that the binary links.
  doCheck = true;
  checkPhase = ''
    runHook preCheck
    ( cd gala-lean && ./.lake/build/bin/gala-lean run ../tests/ex_hex_literals.json > "$TMPDIR/out" )
    if ! diff -u tests/ex_hex_literals.out "$TMPDIR/out"; then
      echo "unexpected gala-lean run output" >&2
      exit 1
    fi
    ( cd gala-lean && ./.lake/build/bin/gala-lean version | grep -F 'gala-lean ' )
    runHook postCheck
  '';

  installPhase = ''
    runHook preInstall
    install -Dm755 gala-lean/.lake/build/bin/gala-lean "$out/bin/gala-lean"
    runHook postInstall
  '';

  meta = {
    description = "Draft Lean implementation of the GALA toolchain (development tool)";
    homepage = "https://github.com/martianoff/gala";
    license = lib.licenses.asl20;
    mainProgram = "gala-lean";
    platforms = lib.platforms.unix;
  };
}
