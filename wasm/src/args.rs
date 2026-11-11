//! `--name value` option parsing shared by every command.

use crate::{Error, Result};

pub struct Args {
    pairs: Vec<(String, String)>,
}

impl Args {
    pub fn parse(argv: &[String]) -> Result<Args> {
        let mut pairs = Vec::new();
        let mut rest = argv.iter();
        while let Some(arg) = rest.next() {
            let Some(name) = arg.strip_prefix("--").filter(|n| !n.is_empty()) else {
                return Err(Error::Usage(format!("unexpected argument: {arg}")));
            };
            let Some(value) = rest.next() else {
                return Err(Error::Usage(format!("--{name} needs a value")));
            };
            pairs.push((name.to_string(), value.clone()));
        }
        Ok(Args { pairs })
    }

    /// Fail unless every option given is one of `known`.
    pub fn only(&self, known: &[&str]) -> Result<()> {
        match self.pairs.iter().find(|(n, _)| !known.contains(&n.as_str())) {
            Some((name, _)) => Err(Error::Usage(format!("unknown option: --{name}"))),
            None => Ok(()),
        }
    }

    /// Fail if any option was given.
    pub fn done(&self) -> Result<()> {
        self.only(&[])
    }

    /// The single value of a required option.
    pub fn one(&self, name: &str) -> Result<&str> {
        match self.all(name)[..] {
            [value] => Ok(value),
            [] => Err(Error::Usage(format!("--{name} is required"))),
            _ => Err(Error::Usage(format!("--{name} given more than once"))),
        }
    }

    pub fn all(&self, name: &str) -> Vec<&str> {
        self.pairs
            .iter()
            .filter(|(n, _)| n == name)
            .map(|(_, v)| v.as_str())
            .collect()
    }

    /// A required option that must be a positive whole number.
    pub fn positive(&self, name: &str) -> Result<u32> {
        let value = self.one(name)?;
        match value.parse::<u32>() {
            Ok(n) if n > 0 => Ok(n),
            _ => Err(Error::Usage(format!(
                "--{name} must be a positive whole number, got {value}"
            ))),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn parse(argv: &[&str]) -> Result<Args> {
        Args::parse(&argv.iter().map(|s| s.to_string()).collect::<Vec<_>>())
    }

    fn usage<T>(result: Result<T>) -> String {
        match result {
            Err(Error::Usage(message)) => message,
            Err(Error::Failed(message)) => panic!("expected usage error, got failure: {message}"),
            Ok(_) => panic!("expected usage error, got success"),
        }
    }

    #[test]
    fn reads_values_and_repeats() {
        let args = parse(&["--in", "/in/a.svg", "--font", "/in/1.ttf", "--font", "/in/2.ttf"])
            .ok()
            .unwrap();
        assert_eq!(args.one("in").ok(), Some("/in/a.svg"));
        assert_eq!(args.all("font"), ["/in/1.ttf", "/in/2.ttf"]);
        assert_eq!(usage(args.one("font")), "--font given more than once");
        assert_eq!(usage(args.one("out")), "--out is required");
        assert!(args.only(&["in", "font"]).is_ok());
        assert_eq!(usage(args.only(&["in"])), "unknown option: --font");
    }

    #[test]
    fn rejects_bad_shapes() {
        assert_eq!(usage(parse(&["/in/a.hwp"])), "unexpected argument: /in/a.hwp");
        assert_eq!(usage(parse(&["--to"])), "--to needs a value");
        assert_eq!(usage(parse(&["--", "x"])), "unexpected argument: --");
    }

    #[test]
    fn positive_numbers() {
        let args = parse(&["--width", "640", "--height", "0", "--dpi", "x"]).ok().unwrap();
        assert_eq!(args.positive("width").ok(), Some(640));
        assert_eq!(
            usage(args.positive("height")),
            "--height must be a positive whole number, got 0"
        );
        assert_eq!(
            usage(args.positive("dpi")),
            "--dpi must be a positive whole number, got x"
        );
    }
}
