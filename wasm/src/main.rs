//! why-hwp-engine: rhwp and resvg as one WASI command.
//!
//! The host mounts /in (read), /out (write), /fonts (rhwp font cache) and /dl
//! (downloaded font originals). Exit codes: 0 success, 1 failure with one line
//! on stderr, 2 usage.

mod args;
mod convert;
mod fonts;
mod svg2png;

use std::process::ExitCode;

use args::Args;

/// Where rhwp's webfont cache lives inside /fonts, matching `~/.cache/rhwp`.
pub const WEBFONT_CACHE: &str = "/fonts/webfonts";

const USAGE: &str = "usage: why-hwp-engine <command> [options]
  convert --to hwp|hwpx|pdf|txt --in <file> --out <file>
  font-plan --in <file>
  font-install --url <url> --file <file>
  svg2png --in <svg> --out <png> --width <px> --height <px> [--font <file> ...]
  version";

/// A failed command: either bad arguments (exit 2) or a runtime error (exit 1).
pub enum Error {
    Usage(String),
    Failed(String),
}

impl From<String> for Error {
    fn from(message: String) -> Self {
        Error::Failed(message)
    }
}

pub type Result<T> = std::result::Result<T, Error>;

fn main() -> ExitCode {
    let argv: Vec<String> = std::env::args().skip(1).collect();
    let Some((command, rest)) = argv.split_first() else {
        eprintln!("{USAGE}");
        return ExitCode::from(2);
    };
    let result = Args::parse(rest).and_then(|args| match command.as_str() {
        "convert" => convert::run(&args),
        "font-plan" => fonts::plan(&args),
        "font-install" => fonts::install(&args),
        "svg2png" => svg2png::run(&args),
        "version" => {
            args.done()?;
            println!(
                "why-hwp-engine {} rhwp {} resvg {}",
                env!("CARGO_PKG_VERSION"),
                env!("WHY_HWP_RHWP_COMMIT"),
                env!("WHY_HWP_RESVG_VERSION")
            );
            Ok(())
        }
        other => Err(Error::Usage(format!("unknown command: {other}"))),
    });
    match result {
        Ok(()) => ExitCode::SUCCESS,
        Err(Error::Usage(message)) => {
            eprintln!("why-hwp-engine: {message}\n{USAGE}");
            ExitCode::from(2)
        }
        Err(Error::Failed(message)) => {
            eprintln!("why-hwp-engine: {}", one_line(&message));
            ExitCode::from(1)
        }
    }
}

fn one_line(message: &str) -> String {
    message.split_whitespace().collect::<Vec<_>>().join(" ")
}

/// rhwp reads its webfont cache location and offline switch from the environment.
pub fn use_font_cache() {
    if std::env::var_os("RHWP_WEBFONT_CACHE").is_none() {
        std::env::set_var("RHWP_WEBFONT_CACHE", WEBFONT_CACHE);
    }
    std::env::set_var("RHWP_WEBFONT_OFFLINE", "1");
}
