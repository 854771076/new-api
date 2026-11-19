#!/bin/sh
set -eu
server=${1:?Usage: sh install.sh https://your-new-api-server}
server=${server%/}
case "$server" in https://*) ;; *) echo 'An HTTPS server URL is required' >&2; exit 1;; esac
case "$(uname -s)" in Linux) os=linux;; Darwin) os=darwin;; *) echo 'Unsupported OS' >&2; exit 1;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64;; aarch64|arm64) arch=arm64;; *) echo 'Unsupported architecture' >&2; exit 1;; esac
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
name="newapi-cli-$os-$arch"
curl --fail --silent --show-error --proto '=https' --max-time 180 -D "$tmp/headers" "$server/api/cli/download/$name" -o "$tmp/newapi-cli"
expected=$(awk 'tolower($1)=="x-checksum-sha256:" {gsub("\r", "", $2); print $2}' "$tmp/headers")
if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$tmp/newapi-cli" | awk '{print $1}'); else actual=$(shasum -a 256 "$tmp/newapi-cli" | awk '{print $1}'); fi
test -n "$expected" && test "$actual" = "$expected" || { echo 'Checksum mismatch' >&2; exit 1; }
chmod 700 "$tmp/newapi-cli"
"$tmp/newapi-cli" self-update --run --server "$server"
echo 'Add this directory to PATH, then restart your shell: ~/.newapi-cli/bin'
echo 'For this shell: export PATH="$HOME/.newapi-cli/bin:$PATH"'
