//! `process <input> <options-json> <output>`: runs one conversion and prints
//! `{"mime","width","height"}`, or `{"error"}` with exit code 2. Used by
//! `scripts/consistency.ts` to compare against the browser implementation.

use compute_imageops::{Error, ImageOptions, Limits, process};

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let [input, options, output] = args.as_slice() else {
        eprintln!("usage: process <input> <options-json> <output>");
        std::process::exit(1);
    };
    let input = std::fs::read(input).expect("read input");
    let options: ImageOptions = serde_json::from_str(options).expect("options");
    match process(&input, &options, &Limits::default()) {
        Ok(out) => {
            std::fs::write(output, &out.bytes).expect("write output");
            let summary = serde_json::json!({ "mime": out.mime, "width": out.width, "height": out.height });
            println!("{summary}");
        }
        Err(err) => {
            let kind = match err {
                Error::Invalid(_) => "invalid",
                Error::Unsupported(_) => "unsupported",
                Error::Failed(_) => "failed",
            };
            println!("{}", serde_json::json!({ "error": kind, "message": err.to_string() }));
            std::process::exit(2);
        }
    }
}
