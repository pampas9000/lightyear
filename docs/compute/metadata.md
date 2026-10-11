# 元数据与 Alpha

本页描述 ICC、EXIF、XMP/HDR 元数据与 Alpha 平面的当前处理方式。引擎只读取 EXIF 方向，不做通用元数据重写；ICC 的提取与注入按容器能力实现。

## ICC Profile

### 提取（`compute-engine::color::icc::extract_icc_from_memory`）

| 容器 | 位置 | 实现 |
| :--- | :--- | :--- |
| JPEG | `APP2` 分片（`ICC_PROFILE\0` + 序号/总数） | `img-parts` 统一入口 + 分片组装 |
| PNG | `iCCP` chunk（zlib 解压） | `img-parts` |
| WebP | `ICCP` RIFF 块 | `img-parts` |
| AVIF / HEIC | ISOBMFF `colr` box | 本地递归查找（待 `mp4parse` 迁移统一） |

色域分类 `classify_icc_color_space` 识别 sRGB / Display P3 / AdobeRGB / ProPhoto，用于给 `cjxl` 生成 `-x color_space=` 提示。

> 当前分类只检查 XYZ colorants，没有验证 TRC，不能据此认定整个 ICC 与命名空间等价。不同曲线可能被误标，见[实现审查 R7](/compute/engineering-review)。

### 注入

| 容器 | 机制 | 状态 |
| :--- | :--- | :--- |
| AVIF | `avifenc --icc` | 已实现 |
| JXL | `cjxl -x color_space=` 或 `-x icc_pathname=` | 已实现 |
| WebP | 先注入中间 PNG，再 `cwebp -metadata icc`；无 cwebp 的降级路径会丢失 ICC | 已实现 |
| JPEG | 结构化重建 + `APP2` 分片段写在 `SOS` 之前 | 已实现 |
| PNG | `set_icc_profile` 插在 `IHDR` 之后 | 已实现 |

策略由 `ProfilePolicy` 控制：

- `preserve_source`（默认）：保留源 ICC；容器 CICP 写 Unspecified，由 ICC 管辖
- `strip`：剥离 ICC，按声明的色彩空间写标签

这是输出策略的含义，不应改变处理中对源颜色的认识。当前 `strip` 不再阻止读取源 nclx，处理阶段仍可使用源 transfer/matrix；但 tone mapping 后哪些源 ICC 仍然有效、以及 ICC/TRC 与命名色彩空间的等价判断尚未统一，见审查 R1/R7。不能保证每条路径都已完整兑现上述策略。

JPEG → JXL 直通路径由 `cjxl` 原生保留源 ICC，无需提取；`profile_policy=strip` 现在会禁用该 bypass，避免绕过剥离策略。非直通 JXL 编码在没有 ICC 时会继承可识别的源 nclx 提示。

### 已知限制

浏览器端编码器栈（`image`/`ravif`、`@jsquash/jxl`）无法写入 ICC/CICP，且 JPEG XL 的颜色编码位于码流头部、无法事后注入。广色域资产请走服务端；沙盒行为见[已知限制](/compute/limitations)。

## EXIF

- **方向（Orientation）**：`kamadak-exif` 统一读取（JPEG/PNG/WebP/TIFF/HEIF），四个容器一致；读取后**烘焙进像素**，产物不再依赖 EXIF 旋转
- **其余标签（相机参数 / GPS）与 XMP**：当前不保留；「解码 → 重编码」路径会丢失（JPEG → JXL 频域直通路径除外）
- ISOBMFF 的 EXIF 按规范存于 `meta → iinf` 声明的 item + `iloc` 定位；旧版按顶层 `Exif` box 查找的实现已修复（迁移记录见[工程笔记](/engineering-notes/exif-metadata)）

## HDR 元数据与增益图

- HDR 内容仅沿**传递轴**处理（PQ/HLG 色调映射到 SDR），不做基色转换
- HDR10 母带元数据（Mastering Display / CLLI）与 Apple/Google Gain Map 均**未实现**；Gain Map 探测代码已按“无消费方不加功能”的原则整体删除
- 双层 JPEG（MPF/XMP Gain Map）作为普通 JPEG 输入，只解码主图（健壮性回归见 F-18 / E-12 / E-13）
- 未来若要支持 Gain Map，必须按「容器管道」而非「像素管道」设计，调研结论见[工程笔记](/engineering-notes/gain-map-research)

## Alpha 平面

当前采用的 Y4M 平面路径不承载 Alpha，因此**解码侧**与**编码侧**必须分别处理，否则透明会被静默丢弃。有限的 `C444alpha` 特例不在当前支持范围：

1. `probe_alpha_channel` 解析 ISOBMFF `auxC` 盒（兼容 MIAF `urn:mpeg:mpegB:cicp:systems:auxiliary:alpha` 与 Apple 旧版 `urn:mpeg:hevc:2015:auxid:1`）
2. 命中 Alpha 时，**解码**自动改走 `avifdec -d 16` / `heif-dec` 的 16-bit PNG 输出，兼顾透明与位深（有端到端测试 `test_e2e_avif_alpha_survives_the_planar_path`，AVIF → PNG）
3. 原生 YUV 直通要求「无 Alpha」，命中即回退像素路径
4. **编码**侧 AVIF 命中 Alpha 时自动改走 PNG 文件输入 + `--qalpha`，透明完整保留（`test_e2e_avif_alpha_encoding_survives_via_png_path`，规范见[编解码引擎](/compute/codec-engines) §3）

| 输入 → 目标 | Alpha 状态 |
| :--- | :--- |
| AVIF / HEIC 带 Alpha → PNG | 保留（16-bit PNG 解码回退） |
| PNG / WebP 带 Alpha → PNG / WebP | 保留（portable engine / cwebp 中间 PNG） |
| RGBA8/16 源 → AVIF / HEIC | PNG 管道承载 Alpha；AVIF 先解析有效位深/抽样，再进入 PNG 路径 |
| 灰度 Alpha 源 → AVIF | `ImageLumaA8/16` 已纳入透明检测，走 PNG 管道保留 Alpha |
| JPEG → JXL 直通 | JPEG 无 Alpha |

> “AVIF 输出都会丢 Alpha”已经不成立。当前 Alpha 路径已覆盖灰度 Alpha，并在进入 PNG 中转前解析统一的 AVIF 位深/抽样；仍需通过固定工具链补齐更多产物级组合验证。

## 验证

- `cargo test -p compute-engine`：ICC roundtrip / 多分片 / 退化流、EXIF orientation、nclx/auxC 探测
- `cargo test -p compute-server-adapter`：真实 P3 ICC 经 CLI 注入回归、WebP ICC 保留、AVIF Alpha 端到端
- 素材级断言见[保真度基准](/quality/fidelity-benchmark)（F-07、F-11、E-01、E-17 等）
