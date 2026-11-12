//! `font-plan` and `font-install`: the host downloads, rhwp checks and caches.
//!
//! rhwp picks fonts from a pinned ledger and downloads them itself, but its HTTP
//! client cannot start a thread inside wasm. So the engine runs rhwp's font
//! supply offline, reports the URLs it missed, and the host fetches them and
//! hands each one back to `font-install`. Some fonts are only asked for once
//! another is present, so the host repeats plan and install until the plan is
//! empty.
//!
//! PDF export also needs one font beyond the document's own: see
//! [`use_pdf_fallback`].

use std::path::{Path, PathBuf};

use resvg::usvg::fontdb;
use rhwp::document_core::DocumentCore;
use rhwp::renderer::font_paths;
use rhwp::renderer::native_webfonts;
use rhwp::renderer::pdf::PdfExportOptions;
use serde_json::{json, Value};
use sha2::{Digest, Sha256};

use crate::args::Args;
use crate::Result;

pub fn plan(args: &Args) -> Result<()> {
    args.only(&["in"])?;
    let input = args.one("in")?;
    let data = std::fs::read(input).map_err(|e| format!("read {input}: {e}"))?;
    let doc = DocumentCore::from_bytes(&data).map_err(|e| format!("open {input}: {e}"))?;
    crate::use_font_cache();
    native_webfonts::why_hwp_take_missing();
    native_webfonts::prepare(&doc.document().doc_info, &[])?;
    let mut missing = native_webfonts::why_hwp_take_missing();
    let installed = font_database(&[]);
    if needs_fallback(|family| has_family(&installed, family))
        && cached_fallback(&cache_dir()).is_none()
        && !missing.iter().any(|url| url == FALLBACK_URL)
    {
        missing.push(FALLBACK_URL.into());
    }
    println!(
        "{}",
        plan_json(&missing, native_webfonts::why_hwp_expected_source_sha256)
    );
    Ok(())
}

fn plan_json(urls: &[String], sha256: impl Fn(&str) -> Option<&'static str>) -> Value {
    Value::Array(
        urls.iter()
            .map(|url| json!({ "url": url, "sha256": sha256(url) }))
            .collect(),
    )
}

pub fn install(args: &Args) -> Result<()> {
    args.only(&["url", "file"])?;
    let url = args.one("url")?;
    let file = args.one("file")?;
    let bytes = std::fs::read(file).map_err(|e| format!("read {file}: {e}"))?;
    crate::use_font_cache();
    native_webfonts::why_hwp_install(url, &bytes, &cache_dir())
        .map_err(|e| format!("install {url}: {e}"))?;
    Ok(())
}

fn cache_dir() -> PathBuf {
    std::env::var_os("RHWP_WEBFONT_CACHE")
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from(crate::WEBFONT_CACHE))
}

/// rhwp's generic public Korean font (`GENERIC_WEBFONT` in its
/// native_webfonts.rs), at the commit its font ledger pins.
const FALLBACK_URL: &str = "https://cdn.jsdelivr.net/gh/edwardkim/rhwp@7d06d5c706f1d89f1662e739abf2b1b25d2a98e4/assets/fonts/NotoSansKR-Regular.woff2";

/// The family name in the font at FALLBACK_URL.
const FALLBACK_FAMILY: &str = "Noto Sans KR";

/// rhwp's PDF stage drops a text run when no family in its font-family list
/// is in the font database. Every list ends in serif, sans-serif or
/// monospace, which rhwp points at Noto CJK KR families that few computers
/// have, so without them a run whose named fonts are all missing disappears.
/// Point each generic family that is not available at FALLBACK_FAMILY, taken
/// from the font cache when it is not installed. The cache folder goes last
/// in the font search path so it only fills in where no other font has the
/// character. Call after the document's fonts are added to `font_paths`.
pub fn use_pdf_fallback(options: &mut PdfExportOptions) {
    let database = font_database(&options.font_paths);
    let has = |family: &str| has_family(&database, family);
    if generic_families(options).iter().all(|family| has(family)) {
        return;
    }
    if !has(FALLBACK_FAMILY) {
        let Some(folder) = cached_fallback(&cache_dir()) else {
            eprintln!(
                "why-hwp-engine: warning: {FALLBACK_FAMILY} is not in the font cache; text in fonts that are not available is left out"
            );
            return;
        };
        std::env::set_var(
            font_paths::FONT_PATH_ENV,
            append_path(&font_paths::env_font_paths(), &folder),
        );
    }
    point_generics_at_fallback(options, has);
}

/// Sets each generic family that `has` does not know to FALLBACK_FAMILY.
fn point_generics_at_fallback(options: &mut PdfExportOptions, has: impl Fn(&str) -> bool) {
    for family in [
        &mut options.fallback_serif,
        &mut options.fallback_sans,
        &mut options.fallback_mono,
    ] {
        if !has(family.as_str()) {
            *family = FALLBACK_FAMILY.into();
        }
    }
}

/// RHWP_FONT_PATH with `last` added at the end. rhwp splits it at ':' on
/// every target but Windows, and the paths here are guest paths.
fn append_path(paths: &[PathBuf], last: &Path) -> String {
    paths
        .iter()
        .map(|path| path.as_path())
        .chain([last])
        .map(|path| path.to_string_lossy())
        .collect::<Vec<_>>()
        .join(":")
}

fn generic_families(options: &PdfExportOptions) -> [&str; 3] {
    [
        &options.fallback_serif,
        &options.fallback_sans,
        &options.fallback_mono,
    ]
}

/// Whether PDF export of a document will need FALLBACK_FAMILY when `has`
/// tells which families are installed.
fn needs_fallback(has: impl Fn(&str) -> bool) -> bool {
    let defaults = PdfExportOptions::default();
    !generic_families(&defaults).iter().all(|family| has(family)) && !has(FALLBACK_FAMILY)
}

/// The fonts rhwp's PDF stage loads, as its `create_fontdb` does.
fn font_database(extra: &[PathBuf]) -> fontdb::Database {
    let mut database = fontdb::Database::new();
    database.load_system_fonts();
    font_paths::load_into_fontdb(&mut database, extra);
    database
}

/// rhwp's own test for a fallback family (`font_family_exists` in pdf.rs).
fn has_family(database: &fontdb::Database, family: &str) -> bool {
    database.faces().any(|face| {
        face.families
            .iter()
            .any(|(name, _)| name == family || name.eq_ignore_ascii_case(family))
    })
}

/// The cache folder holding FALLBACK_URL, when `font-install` stored it there
/// and the font still matches its receipt.
fn cached_fallback(cache: &Path) -> Option<PathBuf> {
    let folder = cache.join(sha256_hex(FALLBACK_URL.as_bytes()));
    let receipt: Value =
        serde_json::from_slice(&std::fs::read(folder.join("resource.json")).ok()?).ok()?;
    let font = std::fs::read(folder.join("font.otf")).ok()?;
    (receipt["url"] == FALLBACK_URL && receipt["nativeSha256"] == sha256_hex(&font).as_str())
        .then_some(folder)
}

fn sha256_hex(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

#[cfg(test)]
mod tests {
    use std::path::{Path, PathBuf};

    use rhwp::renderer::native_webfonts;
    use rhwp::renderer::pdf::PdfExportOptions;

    use super::{
        append_path, cached_fallback, needs_fallback, plan_json, point_generics_at_fallback,
        sha256_hex, FALLBACK_FAMILY, FALLBACK_URL,
    };

    #[test]
    fn plan_lists_url_and_pinned_hash() {
        let urls = vec!["https://a/x.ttf".to_string(), "https://a/y.woff2".to_string()];
        let pinned = |url: &str| (url == "https://a/x.ttf").then_some("abc");
        assert_eq!(
            plan_json(&urls, pinned).to_string(),
            r#"[{"sha256":"abc","url":"https://a/x.ttf"},{"sha256":null,"url":"https://a/y.woff2"}]"#
        );
        assert_eq!(plan_json(&[], pinned).to_string(), "[]");
    }

    #[test]
    fn fallback_url_is_a_ledger_supply() {
        assert!(native_webfonts::why_hwp_known_url(FALLBACK_URL));
    }

    #[test]
    fn fallback_only_when_a_default_family_is_missing() {
        let defaults = PdfExportOptions::default();
        let all = [
            defaults.fallback_serif.clone(),
            defaults.fallback_sans.clone(),
            defaults.fallback_mono.clone(),
        ];
        assert!(!needs_fallback(|family| all.iter().any(|known| known == family)));
        assert!(needs_fallback(|family| family == all[0] || family == all[1]));
        assert!(needs_fallback(|_| false));
        assert!(!needs_fallback(|family| family == FALLBACK_FAMILY));
    }

    #[test]
    fn missing_generics_point_at_fallback() {
        let mut options = PdfExportOptions::default();
        let sans = options.fallback_sans.clone();
        point_generics_at_fallback(&mut options, |family| family == sans);
        assert_eq!(options.fallback_serif, FALLBACK_FAMILY);
        assert_eq!(options.fallback_sans, sans);
        assert_eq!(options.fallback_mono, FALLBACK_FAMILY);
    }

    #[test]
    fn fallback_folder_goes_last() {
        let last = Path::new("/fonts/webfonts/x");
        assert_eq!(append_path(&[], last), "/fonts/webfonts/x");
        let paths = [PathBuf::from("/hostfonts-0"), PathBuf::from("/hostfonts-1")];
        assert_eq!(
            append_path(&paths, last),
            "/hostfonts-0:/hostfonts-1:/fonts/webfonts/x"
        );
    }

    #[test]
    fn cached_fallback_checks_the_receipt() {
        let cache = std::env::temp_dir().join(format!(
            "why-hwp-engine-test-{}-{}",
            std::process::id(),
            sha256_hex(b"cached_fallback")
        ));
        let folder = cache.join(sha256_hex(FALLBACK_URL.as_bytes()));
        std::fs::create_dir_all(&folder).unwrap();
        let font = b"font bytes";
        let receipt = |url: &str, sha: &str| {
            serde_json::json!({"url": url, "nativeSha256": sha}).to_string()
        };
        let write = |json: String| std::fs::write(folder.join("resource.json"), json).unwrap();
        std::fs::write(folder.join("font.otf"), font).unwrap();

        assert_eq!(cached_fallback(&cache), None, "no receipt");
        write(receipt(FALLBACK_URL, &sha256_hex(font)));
        assert_eq!(cached_fallback(&cache), Some(folder.clone()));
        write(receipt(FALLBACK_URL, &sha256_hex(b"other")));
        assert_eq!(cached_fallback(&cache), None, "font changed");
        write(receipt("https://example.com/other.woff2", &sha256_hex(font)));
        assert_eq!(cached_fallback(&cache), None, "other URL");

        std::fs::remove_dir_all(&cache).unwrap();
    }
}
