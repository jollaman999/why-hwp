// Record the versions `why-hwp-engine version` reports.
use std::path::PathBuf;

fn main() {
    println!("cargo:rerun-if-env-changed=WHY_HWP_RHWP_COMMIT");
    println!("cargo:rerun-if-changed=Cargo.toml");
    println!("cargo:rerun-if-changed=Cargo.lock");
    let commit = std::env::var("WHY_HWP_RHWP_COMMIT").unwrap_or_else(|_| "unknown".into());
    println!("cargo:rustc-env=WHY_HWP_RHWP_COMMIT={commit}");

    // rhwp also pulls an older resvg through svg2pdf. Report the one this
    // crate asks for in Cargo.toml, as resolved in Cargo.lock.
    let dir = PathBuf::from(std::env::var("CARGO_MANIFEST_DIR").unwrap());
    let manifest = std::fs::read_to_string(dir.join("Cargo.toml")).unwrap_or_default();
    let wanted = manifest
        .lines()
        .find_map(|l| l.strip_prefix("resvg = { version = \""))
        .and_then(|l| l.split('"').next())
        .map(|v| format!("{v}."))
        .unwrap_or_default();
    let lock = std::fs::read_to_string(dir.join("Cargo.lock")).unwrap_or_default();
    let mut resvg = "unknown";
    let mut lines = lock.lines();
    while let Some(line) = lines.next() {
        if line != "name = \"resvg\"" {
            continue;
        }
        if let Some(version) = lines
            .next()
            .and_then(|l| l.strip_prefix("version = \""))
            .and_then(|l| l.strip_suffix('"'))
        {
            if !wanted.is_empty() && version.starts_with(&wanted) {
                resvg = version;
            }
        }
    }
    println!("cargo:rustc-env=WHY_HWP_RESVG_VERSION={resvg}");
}
