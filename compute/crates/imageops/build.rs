//! rs-vips links `vips`, `glib-2.0` and `gobject-2.0` by name only; pkg-config
//! supplies the search paths (e.g. Homebrew's) and enforces the minimum version.
fn main() {
    pkg_config::Config::new()
        .atleast_version("8.17")
        .probe("vips")
        .expect("libvips >= 8.17 with pkg-config metadata is required");
}
