//! `track-count`: the number of tracked changes in an HWP document, for the
//! HWP files why-hwp's own readers cannot open (HWP 3.0, distribution
//! documents). The count is that of `why-hwp info` for an HWP file: the
//! `HWPTAG_TRACK_CHANGE` records of DocInfo.

use rhwp::model::document::Document;
use rhwp::parser::FileFormat;

use crate::args::Args;
use crate::{Error, Result};

pub fn run(args: &Args) -> Result<()> {
    args.only(&["in"])?;
    let input = args.one("in")?;
    let data = std::fs::read(input).map_err(|e| format!("read {input}: {e}"))?;
    let format = format_name(rhwp::parser::detect_format(&data))?;
    let doc = rhwp::parser::parse_document(&data).map_err(|e| format!("open {input}: {e}"))?;
    println!(
        "{}",
        serde_json::json!({ "format": format, "trackChanges": count(&doc) })
    );
    Ok(())
}

/// The formats whose tracked changes the patched rhwp reads into its model.
/// HWPX and HWPML change marks are not read, so their count would be 0
/// whatever the document holds.
fn format_name(format: FileFormat) -> Result<&'static str> {
    match format {
        FileFormat::Hwp => Ok("hwp"),
        FileFormat::Hwp3 => Ok("hwp3"),
        other => Err(Error::Failed(format!(
            "tracked changes are counted in HWP files only, not in {other:?}"
        ))),
    }
}

/// Every TRACK_CHANGE record, read or not (`DocInfo::track_changes` keeps a
/// record it cannot read as `None`).
fn count(doc: &Document) -> usize {
    doc.doc_info.track_changes.len()
}

#[cfg(test)]
mod tests {
    use super::*;
    use rhwp::model::document::{TrackChange, TrackChangeKind};

    #[test]
    fn counts_every_record() {
        let change = TrackChange {
            kind: TrackChangeKind::Insert,
            date: [2026, 1, 2, 3, 4],
            author_id: 1,
            properties: 0,
            shape_id: None,
            hide: false,
        };
        let mut doc = Document::default();
        assert_eq!(count(&doc), 0);
        doc.doc_info.track_changes = vec![Some(change.clone()), None, Some(change)];
        assert_eq!(count(&doc), 3);
    }

    #[test]
    fn counts_hwp_only() {
        assert_eq!(format_name(FileFormat::Hwp).ok(), Some("hwp"));
        assert_eq!(format_name(FileFormat::Hwp3).ok(), Some("hwp3"));
        for format in [FileFormat::Hwpx, FileFormat::Hml, FileFormat::Unknown] {
            match format_name(format) {
                Err(Error::Failed(message)) => assert!(
                    message.ends_with(&format!("not in {format:?}")),
                    "{message}"
                ),
                _ => panic!("{format:?} was counted"),
            }
        }
    }
}
