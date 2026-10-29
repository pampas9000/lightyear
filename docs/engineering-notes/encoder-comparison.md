# 编码器对比：libvips 与 libjxl / libavif

2026-10。问题：lightyear 通过 libvips 编码 JXL 和 AVIF，与直接调用 libjxl（`cjxl`）、libavif（`avifenc`）相比，同等画质下的体积有没有损失？结论与当前链路见[转码链路](/compute/pipeline)，据此做的决定见[路线图](/roadmap#已做的决定)。

## 方法

参照 `~/mini/image-qa`：

- **素材**：image-qa 的 12 张图。PNG 源 8 张：照片 `p_night`、`p_clic1`、`commons-nikon-d90`、`i26-latte-art`，16-bit 照片 `dsc01027`，地图 `p_map`，截图 `p_shot2`、`i26w-terminal-nvim-edit`。JPEG 源 4 张：`j_ph1`、`j_ph5`、`j_ani1`、`j_ani2`。PNG 源从 `ref.png` 编码，JPEG 源从 `original.jpg` 编码（解码像素与 `ref.png` 逐位相同）。
- **扫描**：质量参数取 12 个点（Q50–98；JXL 直接调用时用 libjxl 对应的 distance）。用 `avifdec` / `djxl` 解码后，以 `ssimulacra2` 与 `butteraugli_main`（max-norm）对比 `ref.png`。
- **比较**：在相同 SSIMU2（85、90 及 80–92 扫描）和相同 Butteraugli（1.5、1.0 及 0.8–1.6 扫描）处插值体积，取 lightyear ÷ 直接调用的比值，再按 PNG 源、JPEG 源分组算几何平均。比值大于 1 表示 lightyear 更大。
- **配置**：两边固定在同一速度档。AVIF 用 lightyear `effort=5`（aom speed 5）对 `avifenc -s 5`，4:2:0 与 4:4:4 各一组，16-bit 源两边都输出 10-bit。JXL 用 lightyear `effort=5` 对 `cjxl -e 5`，另在两张图上用 `effort=8` 对 `-e 7`（同为 libjxl effort 7）。JPEG 源两边都删除元数据，`cjxl` 关闭 JPEG 直接转码。
- **版本**：libvips 8.18.7、libheif 1.23、aom 3.15.2、libavif 1.4.2、libjxl 0.12.0。

## 结果

### JXL：与 libjxl 相同

PNG 源在每个质量点上都与 `cjxl` **逐字节相同**。JPEG 源与一张 16-bit 源解码出的像素相同，文件只差 1–3 字节的头部。所有比值都是 1.000。libvips 的 `jxlsave` 只是把参数原样交给 libjxl。

### AVIF：照片持平，截图偏大

按 SSIMU2 80–92 扫描的体积比（4:4:4；4:2:0 结论相同）：

| 素材 | 体积比 |
| :--- | ---: |
| 照片、地图、16-bit 照片（6 张 PNG 源） | 0.994–1.019 |
| JPEG 源（4 张） | 0.996–1.004 |
| 截图 `p_shot2` | 1.171 |
| 截图 `i26w-terminal-nvim-edit` | 1.406 |

照片和 JPEG 源的差异在 ±2% 以内，属于噪声。Butteraugli 的 max-norm 对单个像素块敏感，单点比值波动较大（0.90–1.17），但扫描平均仍在 1 附近。两张截图则在所有指标上都明显偏大。

## 截图偏大的原因

libvips 的 `heifsave` 固定给 libheif 的 aom 插件传两个参数，且没有选项可以修改：

- `auto-tiles=TRUE`：按图像大小自动切 tile。
- `enable-intrabc=FALSE`：关闭 intra block copy。它在同一帧内复制已编码的块，对重复的字形和界面元素特别有效。libvips 关掉它，是因为高 effort 时编码会慢约 10 倍（libvips issue #2983、PR #4284）。

用 `heif-enc` 逐个打开这两个参数复现（Q90、4:4:4、speed 5，单位 KB）：

| 设置 | 终端截图 | `p_shot2` |
| :--- | ---: | ---: |
| libheif 默认 | 21.0 | 238 |
| 只开 `auto-tiles` | 28.3 | 282 |
| 只关 intrabc | 38.9 | 345 |
| libvips 的组合 | 39.4 | 346 |
| lightyear | 39.1 | 346 |
| `avifenc -s 5` | 26.3 | 282 |

体积差几乎全部来自关闭 intrabc。在这两张截图上，打开 intrabc 会让编码时间变为 1.2–1.9 倍（终端截图从 0.16 秒增至 0.30 秒），没有出现 issue #2983 那样的 10 倍。照片上 intrabc 基本不起作用，两种设置的体积一致。

## 结论

- **JXL** 经 libvips 没有任何损失，没有理由绕开 libvips。
- **AVIF** 只在截图类内容上有损失，原因是 libvips 固定关闭了 intrabc，而不是 libheif 或 aom 本身。修复方式有两种：给 libvips 加参数（需要上游合入，浏览器端的 wasm-vips 也要跟进），或服务端直接调用 libavif。
- **默认速度档**：libjxl 与 libvips 的 JXL 默认都是 effort 7。AVIF 的默认值不一致：libvips 是 effort 4（aom speed 5），libavif 是 speed 6。当时 lightyear 默认 `effort=5`，对应 JXL effort 5、AVIF speed 5（后已改为不填即用编码器自身默认）。

据此做的决定与后续验证项记在[路线图](/roadmap#已做的决定)。

## 复测：服务端 AVIF 改走 libavif 之后

2026-10-28。服务端 AVIF 改为"libvips 解码与整理像素 → libavif 编码"后，比较三条路径：

- **lightyear**：现行链路，libavif 默认 speed 6。
- **纯 libvips**：同一 worker 设 `WORKER_AVIF_ENCODER=libvips`，`effort=4`，即 `heifsave` effort 3、aom speed 6。
- **avifenc**：`avifenc -s 6`，取 image-qa 库中已有的编码与分数。

### 方法

脚本是 `scripts/avif_encoders.py`，用 `task bench:avif` 运行，复现方式见下文。

- **素材与指标**：与上文相同的 12 张图，SSIMULACRA2 与 Butteraugli，都以 `ref.png` 为参照。解码与打分直接调用 image-qa 的 `iqa.tools.metrics`，用 `avifdec` 解码，所以数字可以与 image-qa 库中的结果直接对比。
- **输入**：PNG 源用 `ref.png`。JPEG 源用 `original.jpg`，与 image-qa 的 avifenc 运行一致。
- **编码**：lightyear 用 `compute-imageops` 的 `process` 示例程序，全部为 4:4:4、删除元数据，Q 取 62、70、75、80、84、87、90、92、95 共 9 个点。两张 4:2:0 JPEG（`j_ph1`、`j_ph5`）另测 4:2:0 输出。
- **比较**：在相同 Q 下逐点配对，取体积比的几何平均，以及 SSIMU2、Butteraugli 差的平均。不在相同画质处插值，原因见结果。
- **解码路径**：在 Q80 下比较 lightyear 与关闭 tile 的 avifenc 是否逐字节相同。

两处调整让三方可比：

- 16-bit 源 `dsc01027` 三方都输出 10-bit。lightyear 的 `bit_depth` 只允许 8 或 10，而 image-qa 库中的 avifenc 结果是 12-bit，所以 avifenc 重新编码。
- 透明图 `commons-nikon-d90` 的 avifenc 不加 `--qalpha 100`。libavif 库的默认与 avifenc 相同，alpha 跟随颜色质量，是有损的。

### 结果

在相同 Q 下逐点比较，取体积比的几何平均与 SSIMU2 差的平均（体积比大于 1 表示比 avifenc 大，ΔSSIMU2 为正表示比 avifenc 好）：

| 素材 | lightyear 体积比 | 纯 libvips 体积比 | ΔSSIMU2（三方） |
| :--- | ---: | ---: | :--- |
| 照片 ×3、地图、16-bit 照片、透明图 | 0.98–1.00 | 1.00 | ±0.05 以内 |
| 截图 `p_shot2` | 0.854 | 1.175 | lightyear −0.16，libvips 0.00 |
| 截图 `i26w-terminal-nvim-edit` | 0.816 | 1.365 | lightyear −0.10，libvips +0.56 |
| JPEG 源，4:4:4 输出（4 张） | 0.99–1.00 | 0.99–1.00 | ±0.04 |
| 4:2:0 JPEG，4:2:0 输出（`j_ph1`、`j_ph5`） | 0.99–1.00 | 0.99–1.00 | −0.12 至 −0.30 |

按 image-qa 的方法在相同 SSIMU2（80–92 扫描）处插值，结论相同。Butteraugli 的插值在只有 9 个点时不可靠：`i26-latte-art` 的 lightyear 输出与关闭 tile 的 avifenc 逐字节相同，插值却给出 0.71，所以上表不用它。

- **照片类三方等价**，差别在 ±2% 的噪声内。
- **截图**：lightyear 最小。纯 libvips 偏大，原因见上文的 intrabc。avifenc 比 lightyear 大 15%–18%，原因见下一节。
- **4:2:0 JPEG 转 4:2:0** 是 avifenc 唯一占优的情况：相同 Q 下 SSIMU2 高 0.1–0.3，文件大 0.3%–1%；按相同 SSIMU2 插值，体积比为 1.014（`j_ph1`）与 0.993（`j_ph5`），在 1% 的噪声内。

### libvips 解码与 avifenc 直接读文件的差别

两者调用的是同一个 libavif 编码函数。区别只在谁把 PNG/JPEG 变成像素，以及 avifenc 命令行自带的默认值。

**编码结果逐字节相同。** 关闭 avifenc 的 tile（`--tilerowslog2 0 --tilecolslog2 0`）后，Q80 下 16 组中有 12 组与 lightyear 逐字节相同：全部 8 张 PNG 源（含 16-bit、灰度、透明），以及 JPEG 采样与输出不同的 4 组（4:2:0 JPEG 转 4:4:4、4:4:4 JPEG 转 4:2:0）。不同的 4 组恰好是 JPEG 采样与输出相同的情况，原因见下文"JPEG 直读 YCbCr"。由 libvips 解码本身没有损失。

**avifenc 命令行的默认值与 libavif 库不同：**

| 项目 | avifenc | libavif 库默认，即 lightyear |
| :--- | :--- | :--- |
| tile | 自动切分；截图大 15%–18%，画质相同 | 不切 |
| JPEG 色度 | 沿用 JPEG 的采样 | `chroma` 规则（未填时 Q ≥ 90 为 4:4:4，否则 4:2:0） |
| 灰度 PNG | 4:0:0 | 转 RGB 后编码 |
| 深色位源 | 12-bit | 10-bit（`bit_depth` 只允许 8 或 10） |
| Exif 方向 | 写成 `irot`/`imir` | 方向烘焙进像素 |

**JPEG 直读 YCbCr。** JPEG 的色度采样与输出一致时（4:2:0 → 4:2:0，或 4:4:4 → 4:4:4），avifenc 直接取 JPEG 解码出的 YCbCr 平面，跳过 YCbCr → RGB → YCbCr 的往返；lightyear 总是经过 RGB。实测影响见上表最后一行，等画质下体积差在 1% 以内；4:4:4 的 `j_ani1`、`j_ani2` 测不出差别。

### 复现

需要 image-qa 的工作目录：图片与 `iqa.sqlite` 已就绪，`uv` 可用。另需 `avifenc`、`avifdec`、`ssimulacra2`、`butteraugli_main`。

```bash
task bench:avif IMAGE_QA=~/mini/image-qa
```

10 核机器上约 5 分钟。共编码并打分 243 次：

- lightyear 两条路径：14 组 × 9 个 Q；
- 两张需要重跑的 avifenc：2 × 9 个 Q。

其余 avifenc 结果直接从 image-qa 库中读取。并发数用 `JOBS` 调整，默认 4。输出写到 `target/avif-encoders/`，包括 `results.json` 和全部编码文件；终端打印两张表，即上面的相同 Q 对比和 Q80 逐字节对比。

**结论：** 服务端 AVIF 已与直接调用 libavif 等效，截图上还因不切 tile 更小。没有必要绕开 libvips 去读 PNG/JPEG。
