//! `convert`: rhwp document conversion, following rhwp's own CLI commands
//! (`convert`, `export-hwpx`, `export-pdf`, `export-text`).

use std::path::Path;

use rhwp::document_core::DocumentCore;
use rhwp::renderer::font_environment::FontEnvironment;
use rhwp::renderer::native_webfonts;
use rhwp::renderer::pdf::PdfExportOptions;

use crate::args::Args;
use crate::{Error, Result};

pub fn run(args: &Args) -> Result<()> {
    args.only(&["to", "in", "out"])?;
    let to = args.one("to")?;
    let input = args.one("in")?;
    let output = args.one("out")?;
    if !matches!(to, "hwp" | "hwpx" | "pdf" | "txt") {
        return Err(Error::Usage(format!(
            "--to must be hwp, hwpx, pdf or txt, got {to}"
        )));
    }

    let data = std::fs::read(input).map_err(|e| format!("read {input}: {e}"))?;
    let mut doc = DocumentCore::from_bytes(&data).map_err(|e| format!("open {input}: {e}"))?;
    let bytes = match to {
        "hwp" => to_hwp(&mut doc)?,
        "hwpx" => doc
            .export_hwpx_native()
            .map_err(|e| format!("export HWPX: {e}"))?,
        "pdf" => to_pdf(&mut doc, &data, input)?,
        _ => to_text(&doc)?.into_bytes(),
    };
    std::fs::write(output, bytes).map_err(|e| format!("write {output}: {e}"))?;
    Ok(())
}

/// rhwp `convert`: distribution documents become editable, then HWP 5 is written.
fn to_hwp(doc: &mut DocumentCore) -> Result<Vec<u8>> {
    doc.convert_to_editable_native()
        .map_err(|e| format!("make editable: {e}"))?;
    Ok(doc
        .prepare_hwp_export_snapshot()
        .serialize()
        .map_err(|e| format!("export HWP: {e}"))?)
}

/// rhwp `export-text`: every page's text, each ending in a newline.
fn to_text(doc: &DocumentCore) -> Result<String> {
    let pages = (0..doc.page_count())
        .map(|page| {
            doc.extract_page_text_native(page)
                .map_err(|e| format!("page {} text: {e}", page + 1))
        })
        .collect::<std::result::Result<Vec<_>, _>>()?;
    Ok(join_pages(pages))
}

fn join_pages(pages: Vec<String>) -> String {
    let mut text = String::new();
    for mut page in pages {
        if !page.ends_with('\n') {
            page.push('\n');
        }
        text.push_str(&page);
    }
    text
}

/// rhwp `export-pdf` with the default SVG backend and print profile.
fn to_pdf(doc: &mut DocumentCore, data: &[u8], input: &str) -> Result<Vec<u8>> {
    crate::use_font_cache();
    let mut options = PdfExportOptions::default();
    let unavailable = apply_font_environment(doc, &mut options.font_paths)?;
    if !unavailable.is_empty() {
        eprintln!(
            "why-hwp-engine: warning: fonts not available: {}",
            unavailable.join(", ")
        );
    }
    // Linked pictures next to the document, as rhwp does for non-HML input.
    if !matches!(
        rhwp::parser::detect_format(data),
        rhwp::parser::FileFormat::Hml
    ) {
        if let Some(parent) = Path::new(input).parent() {
            doc.populate_external_images_from_dir(parent);
        }
    }
    let pages: Vec<u32> = (0..doc.page_count()).collect();
    if pages.is_empty() {
        return Err(Error::Failed("the document has no pages".into()));
    }
    Ok(doc
        .render_pages_pdf_native_with_options(&pages, &options)
        .map_err(|e| format!("render PDF: {e}"))?)
}

/// The no-`--font-environment` branch of rhwp's CLI `apply_font_environment`:
/// supply ledger fonts from the cache and map document fonts onto them.
/// Returns the fonts rhwp could not supply.
fn apply_font_environment(
    doc: &mut DocumentCore,
    font_paths: &mut Vec<std::path::PathBuf>,
) -> Result<Vec<String>> {
    let supply = native_webfonts::prepare(&doc.document().doc_info, font_paths)?;
    // Planning state from this run is not needed; drop it.
    native_webfonts::why_hwp_take_missing();
    font_paths.extend(supply.paths.iter().cloned());
    let native = FontEnvironment::from_native_document_substitutions(
        &doc.document().doc_info,
        font_paths,
    )
    .map_err(|e| format!("font environment: {e}"))?;
    let environment = if supply.mappings.is_empty() && supply.glyph_fallbacks.is_empty() {
        native
    } else {
        let failed = |e: &dyn std::fmt::Display| Error::Failed(format!("font environment: {e}"));
        let mut value = serde_json::to_value(native).map_err(|e| failed(&e))?;
        if value.is_null() {
            value = serde_json::json!({"id":"native-webfont-supply", "substitutions":{}, "preserveSourceMetrics":true});
        }
        let object = value
            .as_object_mut()
            .ok_or_else(|| failed(&"not an object"))?;
        let mappings = object
            .entry("substitutions")
            .or_insert_with(|| serde_json::json!({}))
            .as_object_mut()
            .ok_or_else(|| failed(&"substitutions is not an object"))?;
        for (source, target) in &supply.mappings {
            mappings.insert(source.clone(), target.clone().into());
        }
        if let Some(programs) = object
            .get_mut("selectedPrograms")
            .and_then(|v| v.as_object_mut())
        {
            for source in supply.mappings.keys() {
                programs.remove(source);
            }
        }
        if let Some(entries) = object
            .get_mut("documentSubstitutions")
            .and_then(|v| v.as_array_mut())
        {
            entries.retain(|entry| {
                !entry["source"]
                    .as_str()
                    .is_some_and(|source| supply.mappings.contains_key(source))
            });
        }
        if !supply.glyph_fallbacks.is_empty() {
            object.insert(
                "documentGlyphFallbacks".into(),
                supply.glyph_fallbacks.clone().into(),
            );
        }
        use sha2::{Digest, Sha256};
        let identity = serde_json::to_vec(&(&value, &supply.resources)).map_err(|e| failed(&e))?;
        let digest: String = Sha256::digest(identity)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect();
        value["id"] = format!("native-webfont-{}", &digest[..24]).into();
        Some(FontEnvironment::from_json(&value.to_string()).map_err(|e| failed(&e))?)
    };
    doc.set_font_environment(environment)
        .map_err(|e| format!("font environment: {e}"))?;
    Ok(supply.unavailable)
}

#[cfg(test)]
mod tests {
    use super::join_pages;

    #[test]
    fn pages_end_in_newlines() {
        let pages = vec!["one".to_string(), "two\n".to_string(), String::new()];
        assert_eq!(join_pages(pages), "one\ntwo\n\n");
        assert_eq!(join_pages(Vec::new()), "");
    }
}
