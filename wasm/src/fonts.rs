//! `font-plan` and `font-install`: the host downloads, rhwp checks and caches.
//!
//! rhwp picks fonts from a pinned ledger and downloads them itself, but its HTTP
//! client cannot start a thread inside wasm. So the engine runs rhwp's font
//! supply offline, reports the URLs it missed, and the host fetches them and
//! hands each one back to `font-install`. Some fonts are only asked for once
//! another is present, so the host repeats plan and install until the plan is
//! empty.

use std::path::Path;

use rhwp::document_core::DocumentCore;
use rhwp::renderer::native_webfonts;
use serde_json::{json, Value};

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
    let missing = native_webfonts::why_hwp_take_missing();
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
    let cache = std::env::var("RHWP_WEBFONT_CACHE").unwrap_or_else(|_| crate::WEBFONT_CACHE.into());
    native_webfonts::why_hwp_install(url, &bytes, Path::new(&cache))
        .map_err(|e| format!("install {url}: {e}"))?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::plan_json;

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
}
