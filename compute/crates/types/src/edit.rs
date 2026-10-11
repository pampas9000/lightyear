use serde::{Deserialize, Serialize};

fn default_filter() -> String {
    "lanczos3".to_string()
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum ImageEditOp {
    Resize {
        width: u32,
        height: u32,
        #[serde(default = "default_filter")]
        filter: String,
    },
    Rotate {
        degree: u32,
    },
    Flip {
        direction: String,
    },
    Grayscale,
    Blur {
        sigma: f32,
    },
    Adjust {
        #[serde(default)]
        brightness: i32,
        #[serde(default)]
        contrast: f32,
    },
}
