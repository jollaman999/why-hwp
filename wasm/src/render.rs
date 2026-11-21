//! `render`: draw document pages as PNG images the way `convert --to pdf`
//! draws them.
//!
//! PDF export turns each page into SVG with the print profile and has usvg
//! read it with rhwp's font database, fallback face choice, synthetic bold
//! and italic before svg2pdf writes it out. This command takes the same SVG
//! through the same steps and hands the tree to resvg instead of svg2pdf, so
//! line breaks, page breaks and the font chosen for each character match the
//! PDF. rhwp's PDF stage uses usvg 0.45, so the tree is drawn with the resvg
//! of that version, which rhwp's svg2pdf already pulls in.

use std::sync::Arc;

use rhwp::document_core::DocumentCore;
use rhwp::paint::RenderProfile;
use rhwp::renderer::font_paths;
use rhwp::renderer::pdf::{self, PdfExportOptions};
use rhwp_resvg::tiny_skia::{Color, Pixmap, Transform};
use rhwp_resvg::usvg;

use crate::args::Args;
use crate::{Error, Result};

/// The resolution of rhwp's page SVG, whose sizes are CSS pixels.
const SVG_DPI: f32 = 96.0;

/// Limits of rhwp's fallback face index (`PDF_FONT_FALLBACK_INDEX_MAX_CHARS`
/// and `PDF_FONT_FALLBACK_INDEX_MAX_FACE_SLOTS` in its pdf.rs).
const FALLBACK_MAX_CHARS: usize = 4_096;
const FALLBACK_MAX_FACE_SLOTS: usize = 262_144;

/// The largest image drawn, as for `svg2png` (`MaxSide` and `MaxArea` in the
/// Go package internal/svg2png), so a high dpi fails instead of running the
/// engine out of memory.
const MAX_SIDE: u32 = 20_000;
const MAX_AREA: u64 = 120_000_000;

pub fn run(args: &Args) -> Result<()> {
    args.only(&["in", "out", "dpi", "page"])?;
    let input = args.one("in")?;
    let output = args.one("out")?;
    let dpi = args.positive("dpi")?;

    let data = std::fs::read(input).map_err(|e| format!("read {input}: {e}"))?;
    let mut doc = DocumentCore::from_bytes(&data).map_err(|e| format!("open {input}: {e}"))?;
    let pages = select_pages(&args.all("page"), doc.page_count())?;
    let export = crate::convert::pdf_options(&mut doc, &data, input)?;
    let options = svg_options(&export);

    let mut images = Vec::with_capacity(pages.len());
    for page in pages {
        let svg = doc
            .render_page_svg_layer_with_profile_native(page - 1, RenderProfile::Print)
            .map_err(|e| format!("page {page} SVG: {e}"))?;
        let (png, width, height) =
            draw(&svg, &export, &options, dpi).map_err(|e| format!("page {page}: {e}"))?;
        let path = format!("{output}/{page}.png");
        std::fs::write(&path, png).map_err(|e| format!("write {path}: {e}"))?;
        images.push(serde_json::json!({ "page": page, "width": width, "height": height }));
    }
    println!("{}", serde_json::json!({ "pages": doc.page_count(), "images": images }));
    Ok(())
}

/// The 1-based pages to draw: those given, in order, or every page when none
/// is given.
fn select_pages(given: &[&str], count: u32) -> Result<Vec<u32>> {
    if count == 0 {
        return Err(Error::Failed("the document has no pages".into()));
    }
    if given.is_empty() {
        return Ok((1..=count).collect());
    }
    given
        .iter()
        .map(|value| match value.parse::<u32>() {
            Ok(page) if page > 0 && page <= count => Ok(page),
            Ok(page) if page > count => Err(Error::Failed(format!(
                "page {page} does not exist; the document has {count} page{}",
                if count == 1 { "" } else { "s" }
            ))),
            _ => Err(Error::Usage(format!(
                "--page must be a positive whole number, got {value}"
            ))),
        })
        .collect()
}

/// The usvg options rhwp's PDF stage parses page SVG with
/// (`svgs_to_pdf_annotations_impl` and `create_fontdb` in its pdf.rs).
fn svg_options(export: &PdfExportOptions) -> usvg::Options<'static> {
    let mut fontdb = usvg::fontdb::Database::new();
    fontdb.load_system_fonts();
    font_paths::load_into_fontdb(&mut fontdb, &export.font_paths);
    fontdb.set_serif_family(export.fallback_serif.as_str());
    fontdb.set_sans_serif_family(export.fallback_sans.as_str());
    fontdb.set_monospace_family(export.fallback_mono.as_str());

    let mut options = usvg::Options::default();
    options.fontdb = Arc::new(fontdb);
    options.font_resolver.select_fallback =
        pdf::pdf_font_fallback_selector_with_limits(FALLBACK_MAX_CHARS, FALLBACK_MAX_FACE_SLOTS);
    options.image_href_resolver = image_href_resolver();
    options
}

/// rhwp's `pdf_image_href_resolver`: pictures that rhwp embeds as SVG (WMF,
/// EMF) are read as SVG with the page's options, so bitmaps inside them stay.
fn image_href_resolver() -> usvg::ImageHrefResolver<'static> {
    let default_data = usvg::ImageHrefResolver::default_data_resolver();
    usvg::ImageHrefResolver {
        resolve_data: Box::new(move |mime, data, opts| match mime {
            "image/svg+xml" => match usvg::Tree::from_data(&data, opts) {
                Ok(tree) => Some(usvg::ImageKind::SVG(tree)),
                Err(e) => {
                    eprintln!("why-hwp-engine: warning: skipping an SVG picture: {e}");
                    None
                }
            },
            _ => default_data(mime, data, opts),
        }),
        resolve_string: usvg::ImageHrefResolver::default_string_resolver(),
    }
}

/// One page SVG as PNG at `dpi`, with the steps rhwp's PDF stage takes
/// between the SVG and svg2pdf. Returns the PNG and its size in pixels.
fn draw(
    svg: &str,
    export: &PdfExportOptions,
    options: &usvg::Options<'_>,
    dpi: u32,
) -> std::result::Result<(Vec<u8>, u32, u32), String> {
    let svg = pdf::apply_pdf_font_options(svg, export);
    let (svg, _) = pdf::prepare_svg_with_synthetic_bold(&svg, options)?;
    let (tree, _) = pdf::parse_svg_with_synthetic_italic(&svg, options)
        .map_err(|e| format!("parse SVG: {e}"))?;
    let size = tree.size();
    let (width, height) = pixel_size(size.width(), size.height(), dpi);
    check_size(width, height)?;
    let mut pixmap = Pixmap::new(width, height)
        .ok_or_else(|| format!("cannot make a {width}x{height} image"))?;
    pixmap.fill(Color::WHITE);
    let scale = Transform::from_scale(width as f32 / size.width(), height as f32 / size.height());
    rhwp_resvg::render(&tree, scale, &mut pixmap.as_mut());
    let png = pixmap
        .encode_png()
        .map_err(|e| format!("encode PNG: {e}"))?;
    Ok((png, width, height))
}

/// The pixel size of a page `width` x `height` CSS pixels drawn at `dpi`,
/// at least one pixel each way.
fn pixel_size(width: f32, height: f32, dpi: u32) -> (u32, u32) {
    let at = |length: f32| ((length * dpi as f32 / SVG_DPI).round() as u32).max(1);
    (at(width), at(height))
}

/// Fails when a `width` x `height` image is over MAX_SIDE or MAX_AREA.
fn check_size(width: u32, height: u32) -> std::result::Result<(), String> {
    if width > MAX_SIDE || height > MAX_SIDE || width as u64 * height as u64 > MAX_AREA {
        return Err(format!(
            "{width}x{height} px is over the limit of {MAX_SIDE} px per side and {MAX_AREA} pixels; lower the dpi"
        ));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::{check_size, pixel_size, select_pages};
    use crate::Error;

    fn message<T: std::fmt::Debug>(result: crate::Result<T>) -> (bool, String) {
        match result {
            Err(Error::Usage(message)) => (true, message),
            Err(Error::Failed(message)) => (false, message),
            Ok(value) => panic!("expected an error, got {value:?}"),
        }
    }

    #[test]
    fn pages_default_to_all() {
        assert_eq!(select_pages(&[], 3).ok(), Some(vec![1, 2, 3]));
        assert_eq!(select_pages(&["3", "1"], 3).ok(), Some(vec![3, 1]));
    }

    #[test]
    fn pages_outside_the_document_fail() {
        assert_eq!(
            message(select_pages(&["4"], 3)),
            (false, "page 4 does not exist; the document has 3 pages".into())
        );
        assert_eq!(
            message(select_pages(&["2"], 1)),
            (false, "page 2 does not exist; the document has 1 page".into())
        );
        assert_eq!(
            message(select_pages(&["0"], 3)),
            (true, "--page must be a positive whole number, got 0".into())
        );
        assert_eq!(
            message(select_pages(&[], 0)),
            (false, "the document has no pages".into())
        );
    }

    #[test]
    fn pixels_follow_dpi() {
        // A4 in CSS pixels.
        assert_eq!(pixel_size(793.7, 1122.5, 96), (794, 1123));
        assert_eq!(pixel_size(793.7, 1122.5, 150), (1240, 1754));
        assert_eq!(pixel_size(793.7, 1122.5, 300), (2480, 3508));
        assert_eq!(pixel_size(0.1, 0.1, 72), (1, 1));
    }

    #[test]
    fn size_limits() {
        assert!(check_size(20_000, 6_000).is_ok());
        assert!(check_size(10_000, 12_000).is_ok());
        assert!(check_size(20_001, 1).is_err());
        assert!(check_size(1, 20_001).is_err());
        assert_eq!(
            check_size(11_000, 11_000),
            Err("11000x11000 px is over the limit of 20000 px per side and 120000000 pixels; lower the dpi".into())
        );
    }
}
