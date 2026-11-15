# The Lean 4 port of internal/interpolation as a native executable, packaged
# as `.#gala-lean.interpolation` (the `gala-lean` group marks the staged Lean
# port; see flake.nix).
#
# A development tool, like galaspec: it is not part of a GALA release
# (docs/FORMAL_SEMANTICS.MD, "Licensing"). The port is standalone rather than
# part of the Lake project, so it uses the Lean that nixpkgs packages instead
# of formal/lean-toolchain.
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
  pname = "gala-lean-interpolation";
  inherit version;

  # Only the Lean sources take part in the build. The scripts, tests' Go
  # oracle and README are not needed, and build/ is generated output.
  src = lib.fileset.toSource {
    root = ../formal/interpolation;
    fileset = lib.fileset.fileFilter (file: file.hasExt "lean") ../formal/interpolation;
  };

  nativeBuildInputs = [ lean4 ];
  dontConfigure = true;

  buildPhase = ''
    runHook preBuild
    mkdir -p build/c
    export LEAN_PATH="$PWD/build"

    # Imported modules need their .olean before Lean can emit C.
    lean -R . -o build/Interpolation.olean Interpolation.lean
    lean -R . -o build/InterpolationCli.olean InterpolationCli.lean
    lean -R . -o build/Main.olean Main.lean
    lean -R . --c=build/c/Interpolation.c Interpolation.lean
    lean -R . --c=build/c/InterpolationCli.c InterpolationCli.lean
    lean -R . --c=build/c/Main.c Main.lean

    leanc -O2 -o build/interpolation \
      build/c/Main.c build/c/InterpolationCli.c build/c/Interpolation.c
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
    leanc -O2 -o build/interpolation-tests build/c/Tests.c build/c/Interpolation.c
    build/interpolation-tests
    runHook postCheck
  '';

  installPhase = ''
    runHook preInstall
    install -Dm755 build/interpolation "$out/bin/interpolation"
    runHook postInstall
  '';

  meta = {
    description = "Lean 4 port of GALA's interpolation splitter (development tool)";
    homepage = "https://github.com/martianoff/gala";
    license = lib.licenses.asl20;
    mainProgram = "interpolation";
    platforms = lib.platforms.unix;
  };
}
