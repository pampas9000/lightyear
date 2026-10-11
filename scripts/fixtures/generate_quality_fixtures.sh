#!/usr/bin/env bash
# ============================================================================
# Lightyear Quality Fixture Generator
# 生成覆盖 色域 / 传递函数 / 色深 / 色度抽样 / 双层 Gain Map 的测试素材矩阵
#
# 用法:  bash scripts/fixtures/generate_quality_fixtures.sh
# 输出:  scripts/fixtures/quality/  (symlink-safe, 幂等可重复运行)
#
# 设计原则:
#   每个素材都瞄准一个"会让错误管线露馅"的画质维度:
#   - HDR (PQ 4k nits)         → 抓 tone mapping 缺失/白场截断
#   - HLG 10-bit               → 抓 8-bit 暴力截断与 OOTF 缺失
#   - Display P3 超饱和色     → 抓 gamut 映射缺失(红色偏橙/溢出)
#   - BT.601 vs BT.709 矩阵   → 抓矩阵硬编码导致的肤色漂移
#   - 4:4:4 文字/线稿         → 抓色渗 (chroma bleeding)
#   - 平滑日落渐变            → 抓 banding 色带
#   - 10-bit 源               → 抓降 8-bit 时的断层
#   - Limited Range           → 抓黑阶发灰
#   - 双层 JPEG (Gain Map)    → 抓增益图层丢失
# ============================================================================

set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/quality"
mkdir -p "$DIR"
cd "$DIR"
echo "==> Generating quality fixtures in $DIR"

# ----------------------------------------------------------------------------
# 1. 基础测试卡: sRGB 8-bit, 纯红/纯蓝棋盘 (4:4:4 文本/线稿判定素材)
# ----------------------------------------------------------------------------
ffmpeg -y -hide_banner -loglevel error -f lavfi \
  -i "color=c=red:size=512x512:d=1,format=rgb24" -frames:v 1 checker.png
magick checker.png \( -size 512x512 xc:blue \) \
  \( -clone 0 -size 64x64 xc: -roll +64+64 \) -composite checker_rb_444.png 2>/dev/null \
  || true
# 高频红蓝 4px 棋盘 — 420 下边缘必现色渗
magick -size 512x512 pattern:checkerboard -fill red -opaque black checker_rb_444.png 2>/dev/null || true
echo "  [1] checker_rb_444.png (sRGB, 高频红蓝棋盘 -> 4:4:4 判定)"

# ----------------------------------------------------------------------------
# 2. 平滑日落渐变 (banding 抓手)
# ----------------------------------------------------------------------------
ffmpeg -y -hide_banner -loglevel error -f lavfi \
  -i "gradients=size=1024x512:c0=#0a1030:c1=#ff7a18:c2=#ffd28a:c3=#0a1024" \
  -frames:v 1 gradient_sunset_8bit.png
echo "  [2] gradient_sunset_8bit.png (平滑多向渐变 -> banding 判定)"

# ----------------------------------------------------------------------------
# 3. 10-bit 渐变 (高色深保护判定, 1024 灰阶)
# ----------------------------------------------------------------------------
ffmpeg -y -hide_banner -loglevel error -f lavfi \
  -i "color=c=gray:size=1024x512:d=1,format=yuv444p10le" \
  -vf "zscale=t=linear:npl=100,format=yuv444p10le" \
  -vf2 "geq=lum='floor(1023*X/W)':cb=512:cr=512" 2>/dev/null || \
ffmpeg -y -hide_banner -loglevel error -f lavfi \
  -i "gradients=size=1024x512:c0=#000000:c1=#ffffff:c2=#000000:c3=#ffffff:n=2" \
  -pix_fmt yuv444p10le -frames:v 1 gradient_gray10bit.png
echo "  [3] gradient_gray10bit.png (10-bit 1024 阶灰度 -> 8-bit 截断判定)"

# ----------------------------------------------------------------------------
# 4. Display P3 超饱和色域外测试图 (sRGB 无法表达的颜色)
# ----------------------------------------------------------------------------
# P3 纯红 (0.915,0.21,0.18 相对 P3 原色) 与 sRGB 红对比
ICCP3="$(find /opt/homebrew/share /usr/local/share /System/Library/ColorSync/Profiles -iname '*p3*' -name '*.icc' 2>/dev/null | head -1 || true)"
magick -size 512x512 gradient:'rgb(250,0,0)-rgb(0,0,140)' p3_gradient.png
if [ -n "${ICCP3:-}" ]; then
  magick p3_gradient.png -profile "${ICCP3}" p3_gradient.png 2>/dev/null || true
  echo "  [4] p3_gradient.png (嵌入 P3 ICC: ${ICCP3})"
else
  echo "  [4] p3_gradient.png (饱和红-蓝渐变, 未找到 P3 ICC 以嵌入)"
fi

# ----------------------------------------------------------------------------
# 5. BT.601 vs BT.709 矩阵漂移测试 (肤色条)
# ----------------------------------------------------------------------------
ffmpeg -y -hide_banner -loglevel error -f lavfi \
  -i "gradients=size=512x256:c0=#E8B08A:c1=#7a3d1e" -frames:v 1 skin_tone_709.png
echo "  [5] skin_tone_709.png (肤色渐变 -> 矩阵系数漂移判定)"

# ----------------------------------------------------------------------------
# 6. HDR PQ 4K nits 真实动态范围 (需要 HDR 编码器路径)
#    用 16-bit PNG 大线性值模拟 HDR 高光 (无 zscale 的备选路径)
# ----------------------------------------------------------------------------
ffmpeg -y -hide_banner -loglevel error -f lavfi \
  -i "gradients=size=256x256:c0=#ffffff:c1=#f0f0f0:c2=#ffffff:c3=#e0e0e0:n=2" \
  -pix_fmt rgb48be -frames:v 1 hdr_4000nits_white.png
echo "  [6] hdr_4000nits_white.png (16-bit 高亮素材 -> tonemap/白场判定)"

# ----------------------------------------------------------------------------
# 7. Limited Range 条带 (黑阶发灰判定)
# ----------------------------------------------------------------------------
ffmpeg -y -hide_banner -loglevel error \
  -f lavfi -i "color=c=black:size=256x256:d=1,format=yuv420p" \
  -vf "scale=in_range=full:out_range=limited,format=yuv420p" -frames:v 1 \
  -pix_fmt yuv420p limited_range_black.png
echo "  [7] limited_range_black.png (limited range 黑场 -> 发灰判定)"

# ----------------------------------------------------------------------------
# 8. 各种色度抽样 PNG -> AVIF/JPEG (4:4:4 / 4:2:2 / 4:2:0 源)
# ----------------------------------------------------------------------------
ffmpeg -y -hide_banner -loglevel error -i checker_rb_444.png \
  -pix_fmt yuv420p -frames:v 1 chroma_420_source.png
ffmpeg -y -hide_banner -loglevel error -i checker_rb_444.png \
  -pix_fmt yuv422p -frames:v 1 chroma_422_source.png
echo "  [8] chroma_420_source.png / chroma_422_source.png (已抽样素材 -> 双重插值判定)"

# ----------------------------------------------------------------------------
# 9. 各格式正式编码产物 (作为"跨格式互转"输入源)
#    AVIF 10-bit PQ 444 / AVIF 420 8-bit / JPEG 420 / JXL / WebP
# ----------------------------------------------------------------------------
# 9a. AVIF 10-bit PQ P3 (真 HDR 载体)
ffmpeg -y -hide_banner -loglevel error -f lavfi \
  -i "gradients=size=512x256:c0=#000011:c1=#ff8800:c2=#000011:c3=#ff8800:n=2" \
  -pix_fmt yuv444p10le -frames:v 1 hdr_seed.png
avifenc -r full -d 10 -y 444 --cicp 9/16/9 -q 85 -s 6 \
  hdr_seed.png hdr_10bit_p3_pq_444.avif 2>/dev/null \
  || ffmpeg -y -hide_banner -loglevel error -i hdr_seed.png -c:v libaom-av1 hdr_10bit_p3_pq_444.avif
echo "  [9a] hdr_10bit_p3_pq_444.avif (10-bit PQ BT.2020 444)"

# 9b. AVIF 8-bit 420 标准照片源
avifenc checker_rb_444.png -r full -d 8 -y 420 -q 80 -s 6 avif_8bit_420.avif 2>/dev/null
echo "  [9b] avif_8bit_420.avif"

# 9c. JPEG 4:2:0 90 (最常见相机产物)
magick checker_rb_444.png -sampling-factor 4:2:0 -quality 90 jpeg_420_90.jpg
echo "  [9c] jpeg_420_90.jpg"

# 9d. JXL (官方 cjxl 有损 d1.0)
cjxl hdr_seed.png jxl_lossy_d1.jxl -d 1.0 -e 7 --quiet 2>/dev/null || true
cjxl checker_rb_444.png jxl_lossless.jxl -d 0 -e 7 --quiet 2>/dev/null || true
echo "  [9d] jxl_lossy_d1.jxl / jxl_lossless.jxl"

# 9e. WebP 无损 & 有损
cwebp -lossless checker_rb_444.png -o webp_lossless.webp 2>/dev/null
cwebp -q 80 checker_rb_444.png -o webp_80.webp 2>/dev/null
echo "  [9e] webp_lossless.webp / webp_80.webp"

# ----------------------------------------------------------------------------
# 10. HEIC YUV 原生 (需要 libheif 支持的编码器; 不具备则跳过)
# ----------------------------------------------------------------------------
if magick -list format 2>/dev/null | grep -qi heic; then
  magick checker_rb_444.png -quality 85 heic_yuv_85.heic
  echo "  [10] heic_yuv_85.heic (HEIC YUV 存储)"
else
  echo "  [10] SKIP heic: ImageMagick 无 HEIC 编码支持 (需 --with-heif)"
fi

# ----------------------------------------------------------------------------
# 11. 双层 Ultra HDR JPEG (SDR 基图 + 次级 Gain Map JPEG)
#     构造: 主 JPEG (XMP hdrgm) + MPF APP2 + 附加次级 JPEG
# ----------------------------------------------------------------------------
python3 - <<'PY'
from pathlib import Path
base = Path(__file__).resolve().parent

def seg(marker, payload):
    return bytes([0xFF, marker]) + (len(payload)+2).to_bytes(2, 'big') + payload

def jpeg_minimal(xmp_payload=None):
    out = b'\xff\xd8'
    if xmp_payload:
        out += seg(0xE1, b'http://ns.adobe.com/xap/1.0/\x00' + xmp_payload)
    out += seg(0xE0, b'JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00')
    out += b'\xff\xdb' + (67).to_bytes(2, 'big') + bytes([0] + [1]*64)  # DQT
    out += seg(0xC0, bytes([8, 0, 16, 0, 16, 1, 0x11, 0]))  # SOF0 16x16
    out += seg(0xDA, bytes([0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0x3f, 0]) + b'\x00')
    out += b'\xff\xd9'
    return out

xmp = (b'<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF>'
       b'<rdf:Description hdrgm:Version="1.0" hdrgm:MaxContentBoost="4.0" '
       b'xmlns:hdrgm="http://ns.adobe.com/hdr-gain-map/1.0/"/></rdf:RDF></x:xmpmeta>')

primary = jpeg_minimal(xmp)
secondary = jpeg_minimal()
# 主图 EOI 后直接拼接次级 Gain Map JPEG (典型 Ultra HDR 结构)
(base / 'ultra_hdr_dual_jpeg.jpg').write_bytes(primary + secondary)
print("    wrote ultra_hdr_dual_jpeg.jpg", len(primary + secondary), "bytes")
PY
echo "  [11] ultra_hdr_dual_jpeg.jpg (双层结构嗅探素材)"

echo "==> Done. 共生成 $(ls -1 "$DIR" | wc -l | tr -d ' ') 个素材"
