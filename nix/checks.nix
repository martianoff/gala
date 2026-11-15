{ self, eachSystem }:
eachSystem (
  system: pkgs: {
    package = self.packages.${system}.gala;
    gala-local = self.packages.${system}.gala-local;

    # The batch protocol is what `formal/interpolation/test.sh` uses to
    # compare the port with the Go oracle: one `c <hex>` line in, one part per
    # line and `--` out.
    gala-lean-interpolation =
      let
        interpolation = self.packages.${system}.gala-lean-interpolation;
      in
      pkgs.runCommand "gala-lean-interpolation-test" { } ''
        printf 'c 6120246e616d652062\n' | ${interpolation}/bin/interpolation --batch > out
        printf '%s\n' 'L 0 6120' 'E 3  6e616d65' 'L 7 2062' '--' > expected
        if [ "$(cat out)" != "$(cat expected)" ]; then
          echo "unexpected gala-lean-interpolation output:" >&2
          cat out >&2
          exit 1
        fi
        touch "$out"
      '';

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

    stdlib-source-priority =
      let
        gala = self.packages.${system}.gala;
        releaseGalaBin = gala.passthru.releaseGalaBin;
        stdlib = self.packages.${system}.gala-stdlib;
      in
      pkgs.runCommand "gala-stdlib-source-priority-test" { nativeBuildInputs = [ pkgs.go ]; } ''
        export HOME="$TMPDIR/home"
        export GALA_HOME="$TMPDIR/gala-home"
        export GOCACHE="$TMPDIR/go-cache"
        export GOMODCACHE="$TMPDIR/go-mod-cache"
        mkdir -p "$HOME" "$GALA_HOME" "$GOCACHE" "$GOMODCACHE" project
        cd project

        cp -R ${stdlib}/files/lazy "$PWD/lazy"
        chmod -R u+w "$PWD/lazy"

        cat > gala.mod <<'GALA_MOD'
        module martianoff/gala

        gala ${gala.version}
        GALA_MOD

        cat > lazy/nix_local_sentinel.gala <<'GALA'
        package lazy

        func NixLocalStdlibSentinel() string {
          return "local"
        }
        GALA

        cat > main.gala <<'GALA'
        package main

        import "martianoff/gala/lazy"

        func main() {
          Println(lazy.NixLocalStdlibSentinel())
        }
        GALA

        GOPROXY=off ${releaseGalaBin}/bin/gala transpile-package \
          --inputs "$PWD/main.gala" \
          --outputs "$TMPDIR/main.gen.go" \
          --search "$PWD" \
          --goroot="${pkgs.go}/share/go"

        test -d "$GALA_HOME/stdlib/v${gala.version}/lazy"
        test ! -e "$GALA_HOME/stdlib/v${gala.version}/lazy/nix_local_sentinel.gala"
        grep -F 'NixLocalStdlibSentinel' "$TMPDIR/main.gen.go"
        touch "$out"
      '';
  }
)
