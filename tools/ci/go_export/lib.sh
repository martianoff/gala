# Shared steps of the Go export checks; sourced by check_*_export.sh.

# prepare_workdir <dir> — empties <dir> for a run and sets $work to its
# absolute path. The dir is deleted on every run, so only one this script
# created (it carries the marker) or an empty one is accepted.
prepare_workdir() {
  local dir=$1 marker=.go-export-check
  if [ -d "$dir" ] && [ -n "$(ls -A "$dir")" ] && [ ! -e "$dir/$marker" ]; then
    echo "::error::$dir is not empty and was not created by this script; pass a new directory"
    exit 2
  fi
  rm -rf "$dir"
  mkdir -p "$dir"
  work=$(cd "$dir" && pwd)
  touch "$work/$marker"
}

# use_proxy — runs go against $work/proxy only: no network, no checksum
# database, a private module cache (-modcacherw keeps it deletable).
use_proxy() {
  export GOPROXY="file://$work/proxy" GOSUMDB=off GOFLAGS="-mod=mod -modcacherw" GOWORK=off
  export GOMODCACHE="$work/modcache"
  unset GOPRIVATE GONOPROXY GONOSUMDB
}

# check_consumer <program> <expected-output> <go-version> <module@version>...
# builds <program> as a plain Go module with go mod tidy, checks each
# requirement resolved to exactly that version with no replace directive,
# and compares the program's output.
check_consumer() {
  local program=$1 expected=$2 go_version=$3
  shift 3
  local consumer="$work/consumer-$(basename "$(dirname "$program")")"
  mkdir -p "$consumer"
  cp "$program" "$consumer/main.go"
  printf 'module example.com/consumer\n\ngo %s\n' "$go_version" >"$consumer/go.mod"
  (
    cd "$consumer"
    go mod tidy
    local resolved
    resolved=$(go list -m -f '{{.Path}}@{{.Version}}' all)
    for req in "$@"; do
      if ! grep -qxF "$req" <<<"$resolved"; then
        echo "::error::go mod tidy did not resolve $req; it resolved:"
        echo "$resolved"
        exit 1
      fi
    done
    if grep -q '^replace' go.mod; then
      echo "::error::the consumer's go.mod needs a replace directive"
      exit 1
    fi
    go run . >"$consumer/out"
  )
  diff -u "$expected" <(tr -d '\r' <"$consumer/out")
}

# go_version_of <go.mod> — the go directive's version.
go_version_of() { awk '$1 == "go" { print $2; exit }' "$1"; }
