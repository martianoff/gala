{ self, eachSystem }:
eachSystem (
  system: pkgs: {
    package = self.packages.${system}.gala;
    gala-local = self.packages.${system}.gala-local;

    # The batch protocol is what `formal/interpolation/test.sh` uses to
    # compare the port with the Go oracle: one `s <hex>` / `f <hex>` /
    # `p <offset> <line> <col> <hex>` case per line, one part (or position)
    # per line and `--` out.
    gala-lean-interpolation =
      let
        interpolation = self.legacyPackages.${system}.gala-lean.interpolation;
      in
      pkgs.runCommand "gala-lean-interpolation-test" { } ''
        printf 's 6120246e616d652062\nf 247825303464\np 9 3 7 68c3a96c6c6f20247bcf807d\n' \
          | ${interpolation}/bin/interpolation --batch > out
        printf '%s\n' \
          'L 0 0 2 6120' \
          'E 3 2 7  6e616d65 1' \
          'L 7 7 9 2062' \
          '--' \
          'E 1 0 2 25303464 78 0' \
          '--' \
          'P 3 15' \
          '--' > expected
        if [ "$(cat out)" != "$(cat expected)" ]; then
          echo "unexpected gala-lean-interpolation output:" >&2
          cat out >&2
          exit 1
        fi
        touch "$out"
      '';

    # The batch protocol is what `formal/diagnostics/test.sh` uses to compare
    # the port with the Go renderers: one `r|color|path|source|hint|errors`
    # case in, the terminal rendering and the JSON envelope out, as hex.
    gala-lean-diagnostics =
      let
        diagnostics = self.legacyPackages.${system}.gala-lean.diagnostics;
      in
      pkgs.runCommand "gala-lean-diagnostics-test" { } ''
        printf '%s\n' \
          'r|0|6e6f2d737563682d66696c652d376133662e67616c61|7061636b616765206d61696e0a||d:47414c412d4530303031:626164:646f20626574746572::1:0:0' \
          | ${diagnostics}/bin/diagnostics --batch > out
        printf '%s\n' \
          'R 6572726f725b47414c412d45303030315d3a206261640a20202d2d3e206e6f2d737563682d66696c652d376133662e67616c613a313a310a20207c0a31207c207061636b616765206d61696e0a20207c205e5e5e5e5e5e5e20646f206265747465720a20207c0a20203d2068696e743a20646f20626574746572' \
          'J 7b0a202022646961676e6f7374696373223a205b0a202020207b0a202020202020227365766572697479223a20226572726f72222c0a20202020202022636f6465223a202247414c412d4530303031222c0a202020202020226d657373616765223a2022626164222c0a2020202020202268696e74223a2022646f20626574746572222c0a202020202020226c696e65223a20312c0a20202020202022636f6c756d6e223a20312c0a20202020202022646f637355726c223a202268747470733a2f2f67616c612e6679692f646f63732f6572726f72732f67616c612d65303030312f220a202020207d0a20205d0a7d' \
          '--' > expected
        if [ "$(cat out)" != "$(cat expected)" ]; then
          echo "unexpected gala-lean-diagnostics output:" >&2
          cat out >&2
          exit 1
        fi
        touch "$out"
      '';

    # The batch protocol is what `formal/manifest/test.sh` uses to compare the
    # port with the Go parser and formatter: one `c|content|operations` case
    # in, a `P` dump, the operations' results, the formatted manifest as hex,
    # and `--` out. A parse error is `E <hex of the Go error>` and `--`.
    gala-lean-manifest =
      let
        manifest = self.legacyPackages.${system}.gala-lean.manifest;
      in
      pkgs.runCommand "gala-lean-manifest-test" { } ''
        printf '%s\n' \
          'c|6d6f64756c65206d0a726571756972650a' \
          'c|6d6f64756c65206d0a0a67616c6120312e300a0a7265717569726520280a09612076310a0962207632202f2f20696e6469726563740a290a|a:63:7633:1|g:61|d:62|direct|format' \
          | ${manifest}/bin/manifest --batch > out
        printf '%s\n' \
          'E 67616c612e6d6f643a323a2072657175697265206e65656473207061746820616e642076657273696f6e' \
          '--' \
          'P' \
          'module 6d' \
          'gala 312e30' \
          'require 61 7631 0 0' \
          'require 63 7633 1 0' \
          'O' \
          'get 1 7631 0 0' \
          'remove 1' \
          'direct 1 61' \
          'format 6d6f64756c65206d0a0a67616c6120312e300a0a7265717569726520280a09612076310a0963207633202f2f20696e6469726563740a290a' \
          'F 6d6f64756c65206d0a0a67616c6120312e300a0a7265717569726520280a09612076310a0963207633202f2f20696e6469726563740a290a' \
          '--' > expected
        if ! cmp -s out expected; then
          echo "unexpected gala-lean-manifest output:" >&2
          diff -u expected out >&2 || true
          exit 1
        fi
        touch "$out"
      '';

    # The combined `gala-lean` executable: one binary, every component.
    gala-lean-main =
      let
        galaLean = self.legacyPackages.${system}.gala-lean.main;
      in
      pkgs.runCommand "gala-lean-main-test" { nativeBuildInputs = [ pkgs.diffutils ]; } ''
        export HOME="$TMPDIR/home"
        mkdir -p "$HOME" work
        cd work

        ${galaLean}/bin/gala-lean version | grep -F 'gala-lean '

        # `run` is the reference interpreter (galaspec) behind one command.
        ${galaLean}/bin/gala-lean run ${../formal/tests/ex_hex_literals.json} > run.out
        diff -u ${../formal/tests/ex_hex_literals.out} run.out

        # `interp` is the interpolation port.
        printf '%s' 'a $name b' | ${galaLean}/bin/gala-lean interp > interp.out
        printf '%s\n' 'L 0 0 2 6120' 'E 3 2 7  6e616d65 1' 'L 7 7 9 2062' > interp.expected
        diff -u interp.expected interp.out

        # `diag` is the diagnostics port.
        printf '%s\n' \
          'r|0|6e6f2d737563682d66696c652d376133662e67616c61|7061636b616765206d61696e0a||d:47414c412d4530303031:626164:646f20626574746572::1:0:0' \
          | ${galaLean}/bin/gala-lean diag --batch > diag.out
        grep -q '^R ' diag.out
        grep -q '^J ' diag.out
        grep -q '^--$' diag.out

        # `mod` is the gala.mod port, plus a small `init`/`show`/`format` CLI.
        ${galaLean}/bin/gala-lean mod init example.com/draft > init.out
        grep -F 'module example.com/draft' gala.mod
        grep -F 'gala 0.101.0' gala.mod
        ${galaLean}/bin/gala-lean mod show > show.out
        printf '%s\n' 'module example.com/draft' 'gala 0.101.0' > show.expected
        diff -u show.expected show.out

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
