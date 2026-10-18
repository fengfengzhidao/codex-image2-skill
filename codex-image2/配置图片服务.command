#!/bin/sh
set -eu
image2_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
case "$(uname -m)" in
  arm64) image2_arch=arm64 ;;
  *) image2_arch=amd64 ;;
esac
image2_bin="$image2_dir/bin/codex-image2-darwin-$image2_arch"
chmod +x "$image2_bin"
exec "$image2_bin" configure
