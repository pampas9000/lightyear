# 色彩管线

`compute-engine` 与 `compute-server-adapter` 共同实现的静态图像处理管线。设计目标是在已支持路径上保持位深、颜色描述、矩阵与透明度一致，优先使用无压缩中间流，必要时使用无损 PNG 中转。

> 2026-09-28 审查发现部分组合尚未兑现上述目标。具体缺陷、验收条件和明确暂缓的能力见[实现审查与取舍](/compute/engineering-review)；本页的算法和模块列表不代表所有组合已经通过保真验证。

## 设计原则

### 1. 优先无压缩中间流

避免不必要的中间编解码，但不为“零中间容器”牺牲 Alpha 或格式兼容性：

- AVIF 解码用 `avifdec` 直出**无压缩 Y4M 裸流**（保留原始 YUV 平面与 10-bit 值域），由 `read_y4m_to_image` 在内存中还原；HEIC 一律用 `heif-dec` 输出 16-bit PNG（其 Y4M 固定为 8-bit 4:2:0 且无 range 标签）
- 编码由引擎内存生成 Y4M 裸流，经 `stdin` 直灌 `avifenc --stdin`；RGB 路径使用 16-bit PAM 裸流
- 带 Alpha 的 AVIF、HEIC 解码与编码、请求 sharp YUV 的 8-bit 4:2:0 AVIF 以及 cwebp 文件输入使用无损 PNG 中转（avifenc 对 Y4M 输入会忽略 `--sharpyuv`）
- PNG 压缩本身不等于画质损失。取消这些中转属于实测瓶颈出现后的优化，不是当前正确性的前提

### 2. 无损中间层的四个隐形陷阱

即使“解码 → 重编码”不做任何编辑，中间表示选错也会造成不可逆损失：

| 陷阱 | 后果 | 本管线的对策 |
| :--- | :--- | :--- |
| YUV → RGB → YUV 往返 | 两次色度插值/下采样，文字与高饱和边缘出现色渗 | YUV 源在 adapter 私有 `YuvLayout` 相等时走原生直通 |
| 位深截断 | 10/16-bit 被硬压到 8-bit，渐变出现色带、HDR 动态范围丢失 | `ImageRgba16` / PAM `MAXVAL 65535` / Y4M `C*_p10` 原生承载 |
| 元数据剥离 | ICC/EXIF/矩阵全部丢失，P3 被当 sRGB 渲染而偏色 | ICC 提取 + 按容器回写；EXIF 方向读取并烘焙 |
| 频域被破坏 | JPEG → JXL 失去 DCT 重建机会 | `cjxl` 频域无损直通（`--lossless_jpeg` 语义） |

### 3. 只描述、不转换色域

引擎**不做基色转换**：`ColorSpace` 用于描述颜色身份、选择 EOTF 和生成标签；不发生编辑或其他变换时保留源采样含义。HDR 色调映射仅处理传递函数与亮度，不自动把 BT.2020/P3 基色变成 sRGB。当前请求中的源描述与输出意图仍有混用，见审查 R1。

## 类型模型

定义于 `compute/crates/types/src/transcode/color.rs`：

| 类型 | 取值 |
| :--- | :--- |
| `ColorPrimaries` | `Srgb` / `DisplayP3` / `Bt2020` / `Unspecified` |
| `TransferCharacteristics` | `Srgb` / `Linear` / `Pq` / `Hlg` / `Bt709` / `Unspecified` |
| `MatrixCoefficients` | `Identity` / `Bt709` / `Bt601` / `Bt2020Ncl` / `Unspecified` |
| `ColorRange` | `Full` / `Limited` |
| `ChromaSubsampling` | `Yuv420` / `Yuv422` / `Yuv444` / `Yuv400` / `Auto` |
| `nominal_bit_depth` | 请求位深，与存储容器解耦；当前 AVIF 公共入口只接受实际支持的 8/10-bit full-range 组合，完整 12/16-bit YUV 暂缓 |
| `ToneMappingOperator` | `None` / `AcesFilmic`（容错反序列化：历史值映射为 `AcesFilmic`） |
| `ProfilePolicy` | `PreserveSource` / `Strip` |

`ColorSpace`（`srgb` / `display_p3` / `bt2020_pq` / `bt2020_hlg` / `linear`）提供常用基色与传递函数组合，存在时优先于旧字段，但不代替输入 metadata 探测。`Cicp::to_arg_string()` 输出 `P/T/M` 三元组供 `avifenc --cicp` 使用；其中矩阵必须与实际采样转换一致，不仅取决于颜色空间名称。

## 传递函数与色调映射

`compute-engine::color::transfer` 提供全部可逆光学公式：

- sRGB（IEC 61966-2-1 分段）、BT.709、HLG（BT.2100）、PQ（ST 2084，绝对亮度 0–10000 nits）
- `eotf_to_linear` / `oetf_from_linear` 按 `TransferCharacteristics` 动态分发，非最近邻 resize 和 blur 使用线性光工作区；grayscale/adjust 等算子并非都走这条路径
- `hlg_ootf` 有独立公式与测试，但未接入生产色调映射管线；完整显示相关 HLG 处理不属于当前承诺
- `eotf_to_sdr_relative` 以 BT.2408 的 203 nits 漫反射白为单一参考刻度

色调映射目前只有 `AcesFilmic` 一条曲线。adapter 的 `OutputColor` 统一决定源 transfer、处理后的 transfer、输出 CICP 和量化位深。完整 ICC/TRC 与 gamut conversion 仍不属于当前支持范围。

## 编辑管线

`compute-engine::edit::apply_pipeline_with_transfer`：

1. 按源传递函数把像素转入线性光
2. 转为**预乘 Alpha 的线性 f32 工作区**（透明边缘不产生脏边）
3. 依次应用算子；卷积重采样由 `fast_image_resize 6.1` 执行（`use_alpha(false)`，F32x4 SIMD）
4. 按目标传递函数逆变换回编码值

算子（`ImageEditOp`）：`resize`（约束框等比缩放，`nearest`/`triangle`/`catmull-rom`/`gaussian`/`lanczos3`）、`rotate`、`flip`、`grayscale`、`blur`、`adjust`。`nearest` 走纯拷贝快路径，不做线性光往返。

## 输出管线

`OutputColorSpec` + `apply_output_color_pipeline` 只做两步：**传递轴映射（色调映射）+ 位深量化**。

- adapter 在处理像素前解析 `OutputColor`，编码器复用该结果；显式 `engine_params.cicp` 与实际输出决策冲突时拒绝请求，不能单独改标签。
- tone mapping 后不再携带源 ICC；JXL 可用 BT.2020 基色 + sRGB transfer 的显式描述。需要新广色域 SDR profile 的 JPEG/PNG/WebP/HEIC 请求当前报错，不能冒充标准 sRGB。
- 位深：目标是在编码前确定实际输出位深；AVIF 公共入口已在 Alpha、native 与 Y4M 分支前统一解析 8/10-bit 有效值，灰度与灰度 Alpha 也保持 16-bit 工作表示
- 抖动：`dither: bool`（固定 Blue Noise 64×64）在 10/16-bit → 8-bit 时启用，消除渐变断层
- 抽样：`ChromaSubsampling::Auto` 按空间色度梯度能量自动决策（UI/文字/线条 → 4:4:4，连续色调 → 4:2:0）
- 色域：不改写像素；ICC 按容器能力回写（见[元数据](/compute/metadata)）
- 默认标注：无源 ICC 时继承可识别的源 nclx 描述；JXL 的可表示组合通过相同输出决策生成提示。PNG/JPEG/WebP 不会因为目标格式而自动完成 HDR→SDR 转换，完整 ICC/TRC 组合仍见审查 R1/R7。

## Y4M / PAM 裸流

`compute-engine::color::yuv`：

- 平面提取：4:4:4 / 4:2:0（2×2 盒式下采样）/ 4:2:2，各自提供 8-bit 与 10-bit 版本
- 10-bit 严格写入 `0..1023` 采样值域（16-bit 小端字、高 6 位为零），避免编码器溢出烧白
- `write_y4m_c444(_p10)` / `write_y4m_c420(_p10)` / `write_y4m_c422(_p10)` 统一标定 `XCOLORRANGE=FULL`
- `read_y4m_to_image` 支持 8/10-bit、C444/C420/C422，色度上采样使用 `yuv` crate（libyuv 同源）的**双线性**实现，并支持 GBR/Identity 矩阵与 limited range
- Y4M parser 对 p12/p16/其他未知布局会明确拒绝；Identity metadata 会在 native/source 链路中保留。底层函数支持不等于所有外部 decoder 版本组合都已验证
- `write_pam_rgba` 输出 `MAXVAL 65535` 大端 PAM，作为 `cjxl` 高位深输入管道

## 原生 YUV 直通

adapter 内部的 `YuvLayout` 只做当前有执行路径的布局相等判断：

- 当输入输出平面布局、矩阵、抽样、range、位深完全一致（仅 AVIF → AVIF；HEIC 因 `heif-dec` 的 Y4M 输出会截断位深且缺 range 标签而排除），且无编辑、无色调映射、无 Alpha 时，`transcode/native.rs` 把解码器产出的 Y4M 直接管道送入 `avifenc --stdin`，全程不接触 RGB
- CICP 由 `resolve_native_cicp` 从源 `nclx` 搬运（而不是默认 BT.709），避免反解 BT.601/BT.2020 内容时偏色
- 编辑会回退到像素路径；显式 `"none"` 不会阻断 native，legacy `engine_params` 会参与目标布局解析，缺少 `avifenc` 会在选择路径前失败或按能力明确降级。矩阵转换没有独立直通路径，Gain Map 门禁也未实现。

## 输入识别与工具链

- `transcode/detect.rs` 用 `infer` 按 **magic bytes → declared format → 扩展名** 三级判定 `Jpeg`/`Avif`/`Heic`/`Jxl`/`Webp`/`Png`，字节优先，后两级仅回退
- `ToolCapabilities::cached()` 按进程缓存 `cjxl`、`avifenc`、`avifdec`、`heif-enc`、`heif-dec`、`cwebp` 的可用性，安装工具后需重启 Worker；`djxl` 仅用于测试探测。严格模式要求对应工具，非严格模式也只在能够表达输出颜色时允许 portable fallback。
- JPEG → JXL 直通条件（`transcode/bypass.rs`）：输入确为 JPEG、目标 JXL、无编辑、`cjxl` 可用、无色调映射、且 `lossless` 或 `jpeg_reconstruction` 为真

## 验证

- `cargo test -p compute-engine` / `-p compute-server-adapter` 覆盖传递函数往返、Y4M 序列化、ICC 往返、编辑语义与 CLI 端到端
- `bun run benchmark` 对 45 个素材做容器头断言并运行 workspace 测试，详见[保真度基准](/quality/fidelity-benchmark)
