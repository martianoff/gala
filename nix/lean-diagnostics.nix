# The Lean 4 port of galaerr's diagnostics renderers as a native executable,
# packaged as `.#gala-lean-diagnostics` (the `gala-lean-` prefix marks a piece
# of the staged Lean port; see flake.nix).
#
# A development tool, like galaspec and gala-lean-interpolation: it is not part
# of a GALA release (docs/FORMAL_SEMANTICS.MD, "Licensing"). The port is
# standalone rather than part of the Lake project, so it uses the Lean that
# nixpkgs packages instead of formal/lean-toolchain.
{
  lib,
  stdenv,
  lean4,
  version ? (
    let
      line = lib.findFirst (l: lib.hasPrefix "gala " l) "gala 0.0.0" (
        lib.splitString "\n" (builtins.readFile ../gala.mod)
      );
    in
    lib.removePrefix "gala " line
  ),
}:

stdenv.mkDerivation {
  pname = "gala-lean-diagnostics";
  inherit version;

  # Only the Lean sources take part in the build. The scripts, tests' Go
  # oracle and README are not needed, and build/ is generated output.
  src = lib.fileset.toSource {
    root = ../formal/diagnostics;
    fileset = lib.fileset.fileFilter (file: file.hasExt "lean") ../formal/diagnostics;
  };

  nativeBuildInputs = [ lean4 ];
  dontConfigure = true;

  buildPhase = ''
    runHook preBuild
    mkdir -p build/c
    export LEAN_PATH="$PWD/build"

    # Imported modules need their .olean before Lean can emit C.
    lean -R . -o build/Diagnostics.olean Diagnostics.lean
    lean -R . -o build/Main.olean Main.lean
    lean -R . --c=build/c/Diagnostics.c Diagnostics.lean
    lean -R . --c=build/c/Main.c Main.lean

    leanc -O2 -o build/diagnostics build/c/Main.c build/c/Diagnostics.c
    runHook postBuild
  '';

  # Tests.lean is compiled natively too, so this runs the generated C rather
  # than only the Lean interpreter.
  doCheck = true;
  checkPhase = ''
    runHook preCheck
    export LEAN_PATH="$PWD/build"

    lean -R . -o build/Tests.olean Tests.lean
    lean -R . --c=build/c/Tests.c Tests.lean
    leanc -O2 -o build/diagnostics-tests build/c/Tests.c build/c/Diagnostics.c
    build/diagnostics-tests
    runHook postCheck
  '';

  installPhase = ''
    runHook preInstall
    install -Dm755 build/diagnostics "$out/bin/diagnostics"
    runHook postInstall
  '';

  meta = {
    description = "Lean 4 port of galaerr's diagnostics renderers (development tool)";
    homepage = "https://github.com/martianoff/gala";
    license = lib.licenses.asl20;
    mainProgram = "diagnostics";
    platforms = lib.platforms.unix;
  };
}
