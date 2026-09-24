# GALA flake.
#
#   * packages.<system>.gala / .default - the gala CLI, built from source
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
        "aarch64-darwin"
      ];
      eachSystem = f: nixpkgs.lib.genAttrs systems (system: f system nixpkgs.legacyPackages.${system});
    in
    {
      packages = eachSystem (
        system: pkgs: {
          gala = pkgs.callPackage ./nix/gala.nix { };
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
              # .bazelversion pin (9.2.0).
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

      checks = eachSystem (
        system: pkgs: {
          package = self.packages.${system}.gala;

          # End-to-end smoke test: scaffold a project, build it with the
          # embedded stdlib, and run it. Works offline (GOPROXY=off).
          smoke =
            let
              gala = self.packages.${system}.gala;
            in
            pkgs.runCommand "gala-smoke-test" { nativeBuildInputs = [ pkgs.go ]; } ''
              export HOME="$TMPDIR/home"
              mkdir -p "$HOME" project
              cd project

              ${gala}/bin/gala mod init example.com/smoke > /dev/null

              cat > main.gala <<'GALA'
              package main

              func main() {
                Println(s"hello ''${1 + 1}")
              }
              GALA

              ${gala}/bin/gala version | grep -F ${gala.version}
              GOPROXY=off ${gala}/bin/gala run | grep -F 'hello 2'
              touch "$out"
            '';
        }
      );

      formatter = eachSystem (_: pkgs: pkgs.nixfmt);
    };
}
