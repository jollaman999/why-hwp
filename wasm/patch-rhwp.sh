#!/usr/bin/env bash
# Patch a clean rhwp source tree so it builds and runs as a wasm32-wasip1 command.
#
# Usage: patch-rhwp.sh <rhwp-source-dir>
#
# Every step checks whether it is already applied, so running this twice on the
# same tree gives the same result. A step that finds neither the original nor
# the patched text fails, so an rhwp change that moves the code is caught here
# instead of producing a silently different engine.
set -euo pipefail

src=${1:?usage: patch-rhwp.sh <rhwp-source-dir>}
patch_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/patches
cd "$src"

fail() {
	echo "patch-rhwp: $*" >&2
	exit 1
}

# contains <file> <text>: whether the file holds the exact (possibly multi-line) text.
contains() {
	TEXT=$2 perl -e 'local $/; open my $f, "<", $ARGV[0] or exit 2; my $c = <$f> // ""; exit(index($c, $ENV{TEXT}) >= 0 ? 0 : 1)' "$1"
}

# replace <file> <original> <patched>: swap one exact piece of text.
replace() {
	local file=$1 from=$2 to=$3
	if contains "$file" "$to"; then
		return
	fi
	contains "$file" "$from" || fail "$file: text not found: $from"
	FROM=$from TO=$to perl -0pi -e 's/\Q$ENV{FROM}\E/$ENV{TO}/' "$file"
	contains "$file" "$to" || fail "$file: replacement did not apply: $to"
}

# 1. rhwp uses target_arch = "wasm32" to mean "the browser build" and turns off
#    PDF export, native fonts and file access there. WASI is a wasm32 target
#    too, so narrow every such condition to wasm32-unknown-unknown.
files=(Cargo.toml)
while IFS= read -r -d '' f; do
	files+=("$f")
done < <(find src crates -name '*.rs' -print0)
perl -pi -e 's/target_arch = "wasm32"(?!, target_os = "unknown")/all(target_arch = "wasm32", target_os = "unknown")/g' "${files[@]}"
left=$(perl -ne 'print "$ARGV\n" if /target_arch\s*=\s*"wasm32"(?!, target_os = "unknown")/' "${files[@]}")
[ -z "$left" ] || fail "target_arch = \"wasm32\" left unnarrowed in: $left"

# 2. fontdb memory-maps font files by default, which fails on WASI and leaves
#    PDFs without fonts. Turn memmap off in both fontdb users.
replace Cargo.toml \
	'usvg = "0.45"' \
	'usvg = { version = "0.45", default-features = false, features = ["text", "system-fonts"] }'
replace vendor/svg2pdf/Cargo.toml \
	'fontdb = "0.23"' \
	'fontdb = { version = "0.23", default-features = false, features = ["std", "fs", "fontconfig"] }'
replace src/renderer/svg/system_fonts.rs \
	'Source::File(path) | Source::SharedFile(path, _) if path.is_file() => {' \
	'Source::File(path) if path.is_file() => {'

# 3. Memo times are written in local time. WASI has no time zone, so the host
#    passes its UTC offset in WHY_HWP_TZ_OFFSET_MIN.
replace src/model/memo_time.rs \
	'date.with_timezone(&Local)' \
	'date.with_timezone(&memo_zone())'
if ! grep -q 'fn memo_zone' src/model/memo_time.rs; then
	cat >>src/model/memo_time.rs <<'EOF'

#[cfg(not(target_os = "wasi"))]
fn memo_zone() -> Local {
    Local
}

// WASI has no local time zone. The host passes its UTC offset in minutes.
#[cfg(target_os = "wasi")]
fn memo_zone() -> chrono::FixedOffset {
    std::env::var("WHY_HWP_TZ_OFFSET_MIN")
        .ok()
        .and_then(|value| value.trim().parse::<i32>().ok())
        .and_then(|minutes| minutes.checked_mul(60))
        .and_then(chrono::FixedOffset::east_opt)
        .unwrap_or_else(|| chrono::FixedOffset::east_opt(0).unwrap())
}
EOF
fi

# 4. Downloading fonts inside wasm dies when the HTTP client starts a thread.
#    The host downloads instead: record which URLs an offline run needed, and
#    let the host hand a downloaded original back to be checked and cached
#    exactly like rhwp's own download path does.
replace src/renderer/native_webfonts.rs \
	'                return Err("오프라인 모드이며 검증된 캐시가 없습니다".into());' \
	'                why_hwp_record_missing(&url);
                return Err("오프라인 모드이며 검증된 캐시가 없습니다".into());'
if ! grep -q 'fn why_hwp_install' src/renderer/native_webfonts.rs; then
	cat >>src/renderer/native_webfonts.rs <<'EOF'

// why-hwp: the host downloads fonts for the WASI build.
static WHY_HWP_MISSING: std::sync::Mutex<Vec<String>> = std::sync::Mutex::new(Vec::new());

fn why_hwp_record_missing(url: &str) {
    if let Ok(mut missing) = WHY_HWP_MISSING.lock() {
        if !missing.iter().any(|known| known == url) {
            missing.push(url.to_owned());
        }
    }
}

/// URLs that `prepare` needed in offline mode but had no verified cache for.
pub fn why_hwp_take_missing() -> Vec<String> {
    WHY_HWP_MISSING
        .lock()
        .map(|mut missing| std::mem::take(&mut *missing))
        .unwrap_or_default()
}

/// SHA-256 the downloaded original must have, when rhwp pins one.
pub fn why_hwp_expected_source_sha256(url: &str) -> Option<&'static str> {
    expected_kopub_hash(url)
}

/// Whether `prepare` can ask for `url`: a ledger supply or one of its fixed fallbacks.
pub fn why_hwp_known_url(url: &str) -> bool {
    let Ok(registry) = serde_json::from_str::<serde_json::Value>(REGISTRY) else {
        return false;
    };
    let mut sources: Vec<String> = vec![
        GENERIC_WEBFONT.into(),
        NOTO_SERIF.into(),
        "fonts/NotoSansKR-ExtraLight.woff2".into(),
    ];
    for rule in registry["rules"].as_array().into_iter().flatten() {
        if rule["status"] == "active"
            && rule["supply"]["kind"] == "canvas2d-webfont"
            && rule["supply"]["unicodeRange"].is_null()
        {
            if let Some(source) = rule["supply"]["sourceUrl"].as_str() {
                sources.push(source.to_owned());
            }
        }
    }
    sources
        .iter()
        .filter_map(|source| source_url(source))
        .any(|resolved| resolved == url)
}

/// Check and cache a host-downloaded original in the layout `prepare_resource` reads.
pub fn why_hwp_install(
    url: &str,
    bytes: &[u8],
    cache: &Path,
) -> Result<NativeWebfontResource, String> {
    if !why_hwp_known_url(url) {
        return Err(format!("not a font supply URL in the rhwp ledger: {url}"));
    }
    if expected_kopub_hash(url).is_some_and(|expected| hex(bytes) != expected) {
        return Err("SHA-256 of the downloaded font does not match the ledger".into());
    }
    let data = decode_font(bytes)?;
    let folder = cache.join(hex(url.as_bytes()));
    std::fs::create_dir_all(&folder).map_err(|e| e.to_string())?;
    let receipt = NativeWebfontResource {
        url: url.to_owned(),
        source_sha256: hex(bytes),
        native_sha256: hex(&data),
        native_bytes: data.len(),
    };
    let receipt_bytes = serde_json::to_vec(&receipt).map_err(|e| e.to_string())?;
    why_hwp_write(&folder.join("font.otf"), &data)?;
    why_hwp_write(&folder.join("resource.json"), &receipt_bytes)?;
    Ok(receipt)
}

// atomic_write names its temporary file after the process id, which WASI does not have.
fn why_hwp_write(path: &Path, data: &[u8]) -> Result<(), String> {
    let mut nonce = [0u8; 16];
    getrandom::fill(&mut nonce).map_err(|e| e.to_string())?;
    let temp = path.with_file_name(format!(".font-{}", hex(&nonce)));
    if let Err(error) = std::fs::write(&temp, data) {
        let _ = std::fs::remove_file(&temp);
        return Err(error.to_string());
    }
    if let Err(error) = std::fs::rename(&temp, path) {
        let _ = std::fs::remove_file(&temp);
        return Err(error.to_string());
    }
    Ok(())
}
EOF
fi

# 5. Apply wasm/patches/*.patch in name order. They are zero-context diffs
#    against the tree the steps above produce. A patch that reverses cleanly is
#    already applied; one that neither applies nor reverses fails the build.
#    git apply takes paths from the enclosing repository's top, so stop it from
#    looking above the source tree (wasm/rhwp-src sits inside this repository).
export GIT_CEILING_DIRECTORIES=$(dirname "$(pwd -P)")
for p in "$patch_dir"/*.patch; do
	[ -e "$p" ] || continue
	if git apply --unidiff-zero --reverse --check "$p" 2>/dev/null; then
		continue
	fi
	git apply --unidiff-zero --check "$p" || fail "$p: does not apply and is not applied"
	git apply --unidiff-zero "$p"
done
