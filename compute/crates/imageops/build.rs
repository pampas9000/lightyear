//! rs-vips links `vips`, `glib-2.0` and `gobject-2.0` by name only; pkg-config
//! supplies the search paths (e.g. Homebrew's) and enforces the minimum version.
//! libavif is reached through a small C shim compiled against its header.
fn main() {
    pkg_config::Config::new()
        .atleast_version("8.17")
        .probe("vips")
        .expect("libvips >= 8.17 with pkg-config metadata is required");
    let avif = pkg_config::Config::new()
        .atleast_version("1.1")
        .probe("libavif")
        .expect("libavif >= 1.1 with pkg-config metadata is required");
    cc::Build::new()
        .file("src/avif_shim.c")
        .includes(&avif.include_paths)
        .warnings(true)
        .compile("avif_shim");
    println!("cargo:rerun-if-changed=src/avif_shim.c");
}
