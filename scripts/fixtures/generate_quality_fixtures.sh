#!/usr/bin/env bash
# Regenerates scripts/fixtures/quality/. The files are committed; run this only
# to change them. Each one targets a behaviour of compute-imageops (see
# docs/quality/fixtures.md); the other folders hold third-party samples.
#
# Needs: magick (synthesis), vips (encoding), avifenc (CICP-tagged HDR AVIF,
# which libvips cannot write).
#
#   bash scripts/fixtures/generate_quality_fixtures.sh

set -euo pipefail

for tool in magick vips avifenc; do
  command -v "$tool" >/dev/null || { echo "missing: $tool" >&2; exit 1; }
done

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/quality"
mkdir -p "$DIR"
cd "$DIR"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# 8-bit sRGB, 4 px red/blue checker: 4:2:0 visibly smears it, 4:4:4 does not.
magick -size 8x8 xc:red -fill blue -draw "rectangle 4,0 7,3" -draw "rectangle 0,4 3,7" \
  -write mpr:tile +delete -size 512x512 tile:mpr:tile PNG24:checker_rb_444.png

# 8-bit RGBA smooth gradient: banding and alpha.
magick -size 1024x512 gradient:'#0a1030-#ff7a18' -alpha set -channel A -evaluate set 90% +channel \
  PNG32:gradient_sunset_8bit.png

# 16-bit gray ramp: depth must survive (16-bit PNG, 10-bit AVIF).
magick -size 1024x512 -depth 16 gradient:black-white -colorspace sRGB -type TrueColor \
  PNG48:gradient_gray16bit.png

# 16-bit saturated gradient tagged with Display P3: ICC keep vs sRGB conversion.
P3_ICC="$(find /System/Library/ColorSync/Profiles /opt/homebrew/share /usr/share -iname '*display*p3*.ic[cm]' 2>/dev/null | head -1 || true)"
magick -size 512x512 -depth 16 gradient:'rgb(250,0,0)-rgb(0,0,140)' -type TrueColor "$TMP/p3.png"
if [ -n "$P3_ICC" ]; then
  magick "$TMP/p3.png" -profile "$P3_ICC" PNG48:p3_gradient.png
else
  echo "no Display P3 profile found; p3_gradient.png left without ICC" >&2
  cp "$TMP/p3.png" p3_gradient.png
fi

# Encoded sources for cross-format conversion.
magick gradient_sunset_8bit.png -background white -flatten -sampling-factor 4:2:0 -quality 90 jpeg_420_90.jpg
vips copy checker_rb_444.png 'avif_8bit_420.avif[Q=80,subsample-mode=on]'
vips copy checker_rb_444.png 'heic_yuv_85.heic[Q=85,compression=hevc]'
vips copy checker_rb_444.png 'webp_80.webp[Q=80]'
vips copy checker_rb_444.png 'webp_lossless.webp[lossless]'
vips copy checker_rb_444.png 'jxl_lossless.jxl[lossless]'
# keep=none: wasm-vips cannot read JPEG XL files that carry an Exif box.
vips copy gradient_gray16bit.png 'jxl_lossy_16bit.jxl[distance=1,keep=none]'
magick -size 8x8 xc:'rgba(255,0,0,0.5)' PNG32:"$TMP/alpha.png"
vips copy "$TMP/alpha.png" 'avif_8bit_420_alpha.avif[Q=80,subsample-mode=on]'

# CICP-tagged HDR (BT.2020 primaries): must be rejected, not written as sRGB.
magick -size 512x256 -depth 16 gradient:'#000011-#ff8800' -type TrueColor PNG48:"$TMP/hdr.png"
avifenc -q 85 -s 6 -d 10 -y 444 --cicp 9/16/9 "$TMP/hdr.png" hdr_pq_10bit.avif >/dev/null
avifenc -q 85 -s 6 -d 10 -y 444 --cicp 9/18/9 "$TMP/hdr.png" hdr_hlg_10bit.avif >/dev/null

echo "wrote $(ls -1 | wc -l | tr -d ' ') fixtures to $DIR"
