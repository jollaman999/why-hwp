#!/usr/bin/env bash
# Build the why-hwp-engine WASI module and store it, gzipped, in internal/engine/.
#
# Usage: wasm/build.sh
#
# Environment:
#   RHWP_SRC          where the patched rhwp source lives (default: wasm/rhwp-src).
#                     Reused when it already holds the pinned commit.
#   RHWP_GIT          a local rhwp clone to take the pinned commit from with
#                     `git archive`. Without it the commit is fetched from RHWP_URL.
#   RHWP_URL          rhwp repository (default: https://github.com/edwardkim/rhwp.git)
#   CARGO_TARGET_DIR  cargo build directory (default: wasm/target)
#   CARGO, RUSTC      toolchain overrides; RUSTUP_HOME and CARGO_HOME work as usual.
set -euo pipefail

RHWP_COMMIT=7213ed1f96266b95e22cd5645a07851c9c18298e
TARGET=wasm32-wasip1

wasm_dir=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$wasm_dir")
out_dir=$repo/internal/engine
rhwp_src=${RHWP_SRC:-$wasm_dir/rhwp-src}
rhwp_url=${RHWP_URL:-https://github.com/edwardkim/rhwp.git}
cargo=${CARGO:-cargo}
export CARGO_TARGET_DIR=${CARGO_TARGET_DIR:-$wasm_dir/target}

# Paths of the rhwp tree the library build reads: its manifest, sources, path
# dependencies and the files it include_bytes!/include_str!s.
rhwp_paths=(
	Cargo.toml Cargo.lock build.rs LICENSE THIRD_PARTY_LICENSES.md
	README.md README_EN.md llms.txt
	src crates vendor assets data saved
	mydocs/manual mydocs/tech/agent_roadmap packaging/crates-io
)

fail() {
	echo "build.sh: $*" >&2
	exit 1
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

fetch_rhwp() {
	local marker=$rhwp_src/.why-hwp-rhwp-commit
	if [ -f "$marker" ] && [ "$(cat "$marker")" = "$RHWP_COMMIT" ]; then
		echo "rhwp: reusing $rhwp_src"
		return
	fi
	if [ -e "$rhwp_src" ] && [ -n "$(ls -A "$rhwp_src" 2>/dev/null)" ]; then
		fail "$rhwp_src is not empty and is not rhwp $RHWP_COMMIT; remove it or set RHWP_SRC"
	fi
	mkdir -p "$rhwp_src"
	if [ -n "${RHWP_GIT:-}" ]; then
		echo "rhwp: extracting $RHWP_COMMIT from $RHWP_GIT"
		git -C "$RHWP_GIT" archive "$RHWP_COMMIT" -- "${rhwp_paths[@]}" | tar -x -C "$rhwp_src"
	else
		echo "rhwp: fetching $RHWP_COMMIT from $rhwp_url"
		local patterns=() p
		for p in "${rhwp_paths[@]}"; do
			patterns+=("/$p")
		done
		git -C "$rhwp_src" init -q
		git -C "$rhwp_src" remote add origin "$rhwp_url"
		git -C "$rhwp_src" sparse-checkout set --no-cone "${patterns[@]}"
		git -C "$rhwp_src" fetch -q --depth 1 --filter=blob:none origin "$RHWP_COMMIT"
		git -C "$rhwp_src" checkout -q FETCH_HEAD
	fi
	echo "$RHWP_COMMIT" >"$marker"
}

command -v "$cargo" >/dev/null || fail "cargo not found"
sysroot=$(${RUSTC:-rustc} --print sysroot)
[ -d "$sysroot/lib/rustlib/$TARGET" ] ||
	fail "Rust target $TARGET is not installed (rustup target add $TARGET)"

fetch_rhwp
bash "$wasm_dir/patch-rhwp.sh" "$rhwp_src"

# Cargo.toml points at wasm/rhwp-src; link it when the source lives elsewhere.
link=$wasm_dir/rhwp-src
if [ "$(cd "$rhwp_src" && pwd)" != "$link" ]; then
	if [ -L "$link" ]; then
		rm "$link"
	elif [ -e "$link" ]; then
		fail "$link exists and is not a symlink; remove it or unset RHWP_SRC"
	fi
	ln -s "$(cd "$rhwp_src" && pwd)" "$link"
fi

# Panic messages carry source paths. Rewrite the machine-specific prefixes so the
# module holds no local paths and does not change with the build location.
# When several prefixes match, rustc uses the last one given. Cargo names
# sources under wasm/ relative to it, so rhwp usually shows as /engine/rhwp-src.
cargo_home=${CARGO_HOME:-$HOME/.cargo}
remap=(
	"$sysroot=/rustc"
	"$(cd "$cargo_home" && pwd)=/cargo"
	"$(mkdir -p "$CARGO_TARGET_DIR" && cd "$CARGO_TARGET_DIR" && pwd)=/target"
	"$wasm_dir=/engine"
	"$link=/rhwp"
	"$(cd "$rhwp_src" && pwd -P)=/rhwp"
)
# CARGO_ENCODED_RUSTFLAGS separates flags with 0x1f, so paths may hold spaces.
sep=$(printf '\037')
flags=${CARGO_ENCODED_RUSTFLAGS:-$(echo ${RUSTFLAGS:-} | tr " " "$sep")}
for r in "${remap[@]}"; do
	flags="${flags:+$flags$sep}--remap-path-prefix=$r"
done
export CARGO_ENCODED_RUSTFLAGS=$flags
unset RUSTFLAGS

start=$(date +%s)
WHY_HWP_RHWP_COMMIT=$RHWP_COMMIT "$cargo" build --release --locked \
	--target "$TARGET" --manifest-path "$wasm_dir/Cargo.toml"
echo "cargo build: $(($(date +%s) - start))s"

wasm=$CARGO_TARGET_DIR/$TARGET/release/why-hwp-engine.wasm
[ -f "$wasm" ] || fail "build output not found: $wasm"
mkdir -p "$out_dir"
gzip -9 -n -c "$wasm" >"$out_dir/engine.wasm.gz"
echo "$(sha256 "$out_dir/engine.wasm.gz")  engine.wasm.gz" >"$out_dir/engine.wasm.gz.sha256"

echo "wasm:    $(wc -c <"$wasm") bytes"
echo "gzipped: $(wc -c <"$out_dir/engine.wasm.gz") bytes -> $out_dir/engine.wasm.gz"
cat "$out_dir/engine.wasm.gz.sha256"
