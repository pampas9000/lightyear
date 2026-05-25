use serde::{Deserialize, Serialize};
use validator::Validate;

// LibjxlParams represents the configuration options for the JPEG XL encoder (cjxl).
#[derive(Debug, Clone, Serialize, Deserialize, Validate, PartialEq, Default)]
#[validate(schema(function = "validate_libjxl_params"))]
pub struct LibjxlParams {
    #[serde(skip_serializing_if = "Option::is_none")]
    #[validate(range(min = 0.0, max = 15.0))]
    pub distance: Option<f64>,

    #[serde(skip_serializing_if = "Option::is_none")]
    #[validate(range(min = 0, max = 100))]
    pub quality: Option<i32>,

    #[serde(skip_serializing_if = "Option::is_none")]
    #[validate(range(min = 1, max = 10))]
    pub effort: Option<i32>,

    #[serde(skip_serializing_if = "Option::is_none")]
    pub progressive: Option<bool>,
}

fn validate_libjxl_params(params: &LibjxlParams) -> Result<(), validator::ValidationError> {
    if params.distance.is_some() && params.quality.is_some() {
        let mut err = validator::ValidationError::new("distance_quality_exclusive");
        err.message = Some("distance and quality are mutually exclusive".into());
        return Err(err);
    }
    Ok(())
}
