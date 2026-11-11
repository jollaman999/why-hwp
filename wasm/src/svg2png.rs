//! `svg2png`: draw an SVG with resvg on a white page of the given size.

use std::sync::Arc;

use resvg::tiny_skia::{Color, Pixmap, Transform};
use resvg::usvg::{fontdb, Options, Tree};

use crate::args::Args;
use crate::Result;

pub fn run(args: &Args) -> Result<()> {
    args.only(&["in", "out", "width", "height", "font"])?;
    let input = args.one("in")?;
    let output = args.one("out")?;
    let width = args.positive("width")?;
    let height = args.positive("height")?;

    // Only the fonts given; resvg matches them by their name table, not @font-face.
    let mut fonts = fontdb::Database::new();
    for path in args.all("font") {
        let data = std::fs::read(path).map_err(|e| format!("read {path}: {e}"))?;
        let before = fonts.len();
        fonts.load_font_data(data);
        if fonts.len() == before {
            return Err(format!("{path}: not a font resvg can read").into());
        }
    }

    let svg = std::fs::read(input).map_err(|e| format!("read {input}: {e}"))?;
    let mut options = Options::default();
    if let Some(family) = fonts.faces().next().and_then(|f| f.families.first()) {
        options.font_family = family.0.clone();
    }
    options.fontdb = Arc::new(fonts);
    let tree = Tree::from_data(&svg, &options).map_err(|e| format!("parse {input}: {e}"))?;

    let mut pixmap = Pixmap::new(width, height)
        .ok_or_else(|| format!("cannot make a {width}x{height} image"))?;
    pixmap.fill(Color::WHITE);
    let size = tree.size();
    let scale = Transform::from_scale(
        width as f32 / size.width(),
        height as f32 / size.height(),
    );
    resvg::render(&tree, scale, &mut pixmap.as_mut());
    let png = pixmap
        .encode_png()
        .map_err(|e| format!("encode PNG: {e}"))?;
    std::fs::write(output, png).map_err(|e| format!("write {output}: {e}"))?;
    Ok(())
}
