# Executed only in the selected WSL/SSH user's login environment.
# All network/package output is private; stdout carries only fixed diagnostics.
set -eu
umask 077
fail() { printf '__DUO_ENGINE_ERROR__\n%s\n' "$1"; exit 1; }
prefix="$HOME/.local/share/duo/engine-tools/{{ID}}"
mkdir -p -- "$prefix" || fail directory
runtime="$prefix/runtime"
node=''
npm_cli=''
if command -v node >/dev/null 2>&1 && command -v npm >/dev/null 2>&1; then
  # WSL can inherit Windows npm shims. Require a native, sufficiently new Node
  # and an npm entry that this same Node can actually execute.
  if [ "$(node -p 'process.platform!=="win32" && Number(process.versions.node.split(".")[0])>=22' 2>/dev/null)" = true ]; then
    candidate=$(command -v npm)
    if node "$candidate" --version >/dev/null 2>&1; then
      node=$(node -p 'process.execPath' 2>/dev/null) || fail runtime_version
      npm_cli="$candidate"
    fi
  fi
fi
if [ -n "$node" ]; then
  mkdir -p -- "$runtime/bin" || fail directory
  ln -s -- "$node" "$runtime/bin/node" || fail directory
else
  [ "$(uname -s)" = Linux ] || fail runtime_platform
  case "$(uname -m)" in
    x86_64|amd64) arch=x64; checksum={{X64_SHA}} ;;
    aarch64|arm64) arch=arm64; checksum={{ARM64_SHA}} ;;
    *) fail runtime_arch ;;
  esac
  for tool in curl tar sha256sum; do command -v "$tool" >/dev/null 2>&1 || fail runtime_tools; done
  archive="$prefix/node.tar.gz"
  log=''
  trap 'rm -f -- "$archive" ${log:+"$log"}' EXIT
  name=node-v{{VERSION}}-linux-$arch
  curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-redirs 3 --connect-timeout 20 --max-time 240 --retry 2 --retry-delay 2 --retry-max-time 240 "https://nodejs.org/dist/v{{VERSION}}/$name.tar.gz" -o "$archive" >/dev/null 2>&1 || fail runtime_download
  actual=$(sha256sum "$archive") || fail runtime_checksum
  actual=${actual%% *}
  [ "$actual" = "$checksum" ] || fail runtime_checksum
  tar -xzf "$archive" -C "$prefix" --no-same-owner >/dev/null 2>&1 || fail runtime_unpack
  mv -- "$prefix/$name" "$runtime" || fail runtime_unpack
  node="$runtime/bin/node"
  npm_cli="$runtime/lib/node_modules/npm/bin/npm-cli.js"
  [ "$("$node" -p 'process.platform==="linux" && Number(process.versions.node.split(".")[0])>=22' 2>/dev/null)" = true ] || fail runtime_version
fi
PATH="$runtime/bin:$PATH"
export PATH
log=$(mktemp "$prefix/npm-output.XXXXXX") || fail directory
archive="$prefix/node.tar.gz"
trap 'rm -f -- "$archive" "$log"' EXIT
if ! "$node" "$npm_cli" install --global --prefix "$prefix" --cache "$prefix/npm-cache" --registry=https://registry.npmjs.org --no-audit --no-fund '{{PACKAGE}}@latest' >"$log" 2>&1; then
  if grep -Eq '^npm (ERR!|error) code (EACCES|EPERM)$' "$log"; then fail package_permission; fi
  if grep -Eq '^npm (ERR!|error) code (ENOTFOUND|EAI_AGAIN|ECONNRESET|ETIMEDOUT|ECONNREFUSED|ENETUNREACH)$' "$log"; then fail package_network; fi
  if grep -Eq '^npm (ERR!|error) code ENOSPC$' "$log"; then fail package_space; fi
  fail package_install
fi
test -x "$prefix/bin/{{BINARY}}" || fail entry_missing
# Persist the runtime path alongside the CLI. Future tasks do not depend on an
# interactive shell loading nvm or on Windows interop providing node.exe.
mkdir -p -- "$prefix/duo-bin" || fail directory
cat > "$prefix/duo-bin/{{BINARY}}" <<'DUO_WRAPPER'
#!/bin/sh
set -eu
tool_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
PATH="$tool_dir/runtime/bin:$PATH"
export PATH
exec "$tool_dir/bin/{{BINARY}}" "$@"
DUO_WRAPPER
chmod 700 "$prefix/duo-bin/{{BINARY}}" || fail directory
"$prefix/duo-bin/{{BINARY}}" --version >/dev/null 2>&1 || fail entry_start
printf '__DUO_ENGINE_PATH__\n%s\n' "$prefix/duo-bin/{{BINARY}}"
