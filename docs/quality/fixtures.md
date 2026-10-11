# 素材与测试

图像处理的正确性靠三层测试保证，全部使用 `scripts/fixtures/` 下的素材：

| 命令 | 范围 | 判定 |
| :--- | :--- | :--- |
| `cargo test -p compute-imageops` | 逐项行为（几何、转正、ICC、位深、alpha、元数据、gain map、HDR 拒绝、上限、JPEG→JXL 还原） | 读回产物，检查头信息与像素属性 |
| `task test:fixtures` | 全部素材 × 六种输出格式 | 只有带 PQ/HLG 标记的 4 个素材被拒绝，其余必须成功 |
| `task test:consistency` | 服务端与浏览器实现，22 组素材 × 参数 | 格式、尺寸、通道、位深、ICC/Exif/gain map 一致，像素平均差 ≤ 1.5（0–255）；浏览器已知缺口按预期失败处理 |

测试依赖的工具缺失时直接失败，不跳过：`cjxl`/`djxl`（JPEG→JXL 逐字节还原）、Node ≥ 23.6（一致性测试）。

## 合成素材 `quality/`

由 `scripts/fixtures/generate_quality_fixtures.sh` 生成（需要 `magick`、`vips`、`avifenc`），文件已提交，只有修改素材时才需要重新运行。

| 素材 | 规格 | 用途 |
| :--- | :--- | :--- |
| `checker_rb_444.png` | 512² 8-bit RGB，4 px 红蓝棋盘 | 4:2:0 会把色块边缘抹开，4:4:4 不会；多数编码素材的来源 |
| `gradient_sunset_8bit.png` | 1024×512 8-bit RGBA 渐变 | 色带、alpha |
| `gradient_gray16bit.png` | 1024×512 16-bit 灰阶 | 位深保持（16-bit PNG、10-bit AVIF） |
| `p3_gradient.png` | 512² 16-bit，Display P3 ICC | ICC 保留与转 sRGB |
| `jpeg_420_90.jpg` | 1024×512 JPEG 4:2:0 Q90 | JPEG 输入、色度抽样、JPEG→JXL 重新打包 |
| `avif_8bit_420.avif` · `heic_yuv_85.heic` · `webp_80.webp` · `webp_lossless.webp` · `jxl_lossless.jxl` | 各格式编码的棋盘 | 各解码器输入 |
| `jxl_lossy_16bit.jxl` | 16-bit JXL | 高位深 JXL 输入 |
| `avif_8bit_420_alpha.avif` | 8×8 半透明 | alpha 保留与 JPEG 铺白底 |
| `hdr_pq_10bit.avif` · `hdr_hlg_10bit.avif` | 10-bit，CICP 9/16/9 与 9/18/9 | 必须被拒绝 |

## 外部素材 `external/`

来自 WebKit、AOMedia（libavif）与 Google（libultrahdr）仓库的原始文件，未改名、未加工。

| 素材 | 用途 |
| :--- | :--- |
| `webkit_*_{srgb,p3,adobergb,prophoto}.{jpg,png}` | 广色域 ICC 的保留与转换；同一画面的 sRGB 版本作对照 |
| `aom_apple_gainmap_{new,old}.jpg` · `google_apple_gainmap_new.jpg` | UltraHDR / Apple gain map JPEG：保留、缩放与 `sdr` 丢弃 |
| `aom_seine_hdr_rec2020.avif` | BT.2020 PQ，必须被拒绝 |
| `aom_seine_hdr_gainmap_srgb.avif` | PQ 基础图（sRGB 基色）+ SDR gain map，必须被拒绝 |
| `aom_weld_16bit.png` | 16-bit 真实照片 |
| `aom_paris_icc_exif_xmp.png` | ICC + EXIF + XMP 同时存在 |
| `google_minnie_yuv_icc.jpg` | 带 ICC 的 JPEG |

## 高分辨率素材 `hd_benchmark/`

4K 照片（人像、风景、建筑线条）与 Kodak 测试集的 2K 版本，用于缩放与 `fixture_sweep` 的真实尺寸路径，也适合人工 1:1 对比有损编码的细节损失。
