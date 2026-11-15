# GALA flake.
#
#   * packages.<system>.gala / .default - the gala CLI, built from source;
#     the stdlib is transpiled by a downloaded release binary (see
#     nix/gala.nix)
#   * packages.<system>.gala-local        - escape hatch for grammar or
#     codegen work the release cannot handle yet: transpiles with
#     cmd/gala_bootstrap built from this tree
#   * packages.<system>.gala-lean-interpolation - the Lean 4 port of GALA's
#     interpolation splitter, as a native executable (a development tool).
#     Ported pieces are named gala-lean-<component>; bare gala-lean is
#     reserved for the complete Lean implementation.
#   * overlays.default                   - adds pkgs.gala to nixpkgs
#   * devShells.default                  - Bazelisk, Go, JDK for this repo
#   * checks.<system>.smoke              - offline build-and-run smoke test
#
# Downstream flakes:
#
#   inputs.gala.url = "github:martianoff/gala";
#   ...
#   environment.systemPackages = [ gala.packages.${pkgs.system}.default ];
{
  description = "GALA - a functional programming language that transpiles to Go";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      eachSystem = f: nixpkgs.lib.genAttrs systems (system: f system nixpkgs.legacyPackages.${system});
    in
    {
      packages = eachSystem (
        system: pkgs: {
          gala = pkgs.callPackage ./nix/gala.nix { };
          gala-local = pkgs.callPackage ./nix/gala.nix { useLocalBootstrap = true; };
          gala-lean-interpolation = pkgs.callPackage ./nix/lean-interpolation.nix { };
          gala-stdlib = self.packages.${system}.gala.passthru.localTranspiled;
          default = self.packages.${system}.gala;
        }
      );

      # `pkgs.gala` for nixpkgs consumers.
      overlays.default = final: _prev: {
        gala = final.callPackage ./nix/gala.nix { };
      };

      devShells = eachSystem (
        _: pkgs: {
          default = pkgs.mkShell {
            packages = [
              # Does not ship a `bazel` command; add one so the documented
              # `bazel build //...` invocations work. Bazelisk honours the
              # .bazelversion pin (9.2.0). Bazelisk downloads a dynamically
              # linked Bazel: on NixOS it needs `nix-ld` (or an FHS wrapper),
              # while macOS and non-NixOS Linux run it as-is.
              (pkgs.writeShellScriptBin "bazel" ''
                exec ${pkgs.bazelisk}/bin/bazelisk "$@"
              '')
              pkgs.bazelisk
              pkgs.go
              pkgs.jdk21
              pkgs.git
            ];
            # The Bazel build reads GOROOT through --action_env so the
            # transpiler can use go/importer for type inference.
            shellHook = ''
              export GOROOT="$(go env GOROOT)"
            '';
          };
        }
      );

      checks = import ./nix/checks.nix { inherit self eachSystem; };

      formatter = eachSystem (_: pkgs: pkgs.nixfmt);
    };
}
