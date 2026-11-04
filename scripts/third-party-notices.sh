#!/bin/sh
# Write docs/THIRD_PARTY_NOTICES.md: the license of every module compiled into
# zellij-reaper, of Go itself, and of the font the logo is drawn from. The
# release archives and packages carry it next to the binary. Run it after
# changing dependencies:
#
#   make notices
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

# Only what is linked into the binary is distributed with it, not every module
# go.mod names, so the list comes from the binary itself.
CGO_ENABLED=0 go build -trimpath -o "$tmp/zellij-reaper" ./cmd/zellij-reaper
go version -m "$tmp/zellij-reaper" | awk '$1 == "dep" { print $2, $3 }' >"$tmp/deps"

# license_file prints the license file of the module in directory $1.
license_file() {
  for name in LICENSE LICENSE.txt LICENSE.md COPYING LICENCE; do
    if [ -f "$1/$name" ]; then
      echo "$1/$name"
      return 0
    fi
  done
  return 1
}

# kind names the license in file $1 from words every copy of it carries.
kind() {
  if grep -q "Apache License" "$1"; then
    echo "Apache-2.0"
  elif grep -q "Permission is hereby granted, free of charge" "$1"; then
    echo "MIT"
  elif grep -q "Neither the name" "$1"; then
    echo "BSD-3-Clause"
  elif grep -q "Redistribution and use in source and binary forms" "$1"; then
    echo "BSD-2-Clause"
  else
    echo "see the text below"
  fi
}

# The Go release go.mod asks for, not the one this machine has: the file must
# read the same wherever it is generated.
gover=$(awk '$1 == "go" { print $2 }' go.mod)
# The logo's lettering, with the font's license kept next to it.
font_license=docs/assets/JetBrainsMono-OFL.txt

out="$tmp/notices.md"
{
  echo "# Third-party notices"
  echo
  echo "zellij-reaper's own code is under the MIT License ([LICENSE](../LICENSE)). Its binary is built from the Go modules"
  echo "and the Go standard library below, and its logo is drawn from the JetBrains Mono font. Their licenses follow."
  echo "\`scripts/third-party-notices.sh\` writes this file from what the binary is built from (\`make notices\`)."
  echo
  echo "| Component | Version | License | Source |"
  echo "|:---|:---:|:---:|:---|"
  while read -r mod ver; do
    dir=$(go list -m -f '{{.Dir}}' "$mod")
    # A module without a license file must not go unnoticed.
    file=$(license_file "$dir") || { echo "no license file in $mod" >&2; exit 1; }
    echo "| \`$mod\` | $ver | $(kind "$file") | https://pkg.go.dev/$mod |"
  done <"$tmp/deps"
  echo "| Go standard library | $gover | $(kind "$(go env GOROOT)/LICENSE") | https://go.dev |"
  echo "| JetBrains Mono (the logo's lettering) | 2.211 | OFL-1.1 | https://github.com/JetBrains/JetBrainsMono |"
  while read -r mod ver; do
    dir=$(go list -m -f '{{.Dir}}' "$mod")
    file=$(license_file "$dir")
    echo
    echo "## $mod $ver"
    echo
    echo '```text'
    cat "$file"
    echo '```'
  done <"$tmp/deps"
  echo
  echo "## Go standard library"
  echo
  echo '```text'
  cat "$(go env GOROOT)/LICENSE"
  echo '```'
  echo
  echo "## JetBrains Mono 2.211"
  echo
  echo '```text'
  cat "$font_license"
  echo '```'
} >"$out"
mv "$out" docs/THIRD_PARTY_NOTICES.md
echo "wrote docs/THIRD_PARTY_NOTICES.md ($(wc -l <"$tmp/deps" | tr -d ' ') modules)"
