# 重采样迁移记录：`image::imageops` → `fast_image_resize`（Resampling Kernel）

本文档记录 `compute-engine` 编辑管线（`edit.rs`）的重采样从 `image` crate 的 `imageops` 迁移到 `fast_image_resize`（fir）6.1 的方案与落地状态，是「计算模块可用库评估」落地清单的第 3 项。

---

## 一、背景

迁移前 `edit.rs` 的 `Resize` 算子：

- 非最近邻：把图像转成 **premultiplied linear f32 RGBA** 工作区，调用 `DynamicImage::resize`（`image::imageops` 卷积核），再转回原传递函数；
- 最近邻：直接 `img.resize(..., Nearest)`，不做线性化。

`fast_image_resize` 6.1（MIT/Apache，3.8M 下载/月，SIMD：SSE4.1/AVX2/Neon/Wasm SIMD128，支持 U8/U16/F32 像素）在性能与维护度上都是同类首选，且原生支持 F32x4 与 premultiplied 语义。

## 二、进度对照矩阵（Status Matrix）

| 维度 | 项目 | 状态 | 对应位置 | 说明 |
| :--- | :--- | :---: | :--- | :--- |
| 依赖 | 引入 `fast_image_resize` 6.1（`image` feature） | ✅ 已完成 | `compute/crates/engine/Cargo.toml` | `image` feature 提供 `IntoImageView[Mut]` 对 `Rgba32FImage` 的实现 |
| 内核 | 卷积重采样全部改走 fir（F32x4） | ✅ 已完成 | `edit.rs::apply_pipeline_with_transfer` | 直接消费现有 premultiplied linear f32 buffer，无需转换 |
| 语义 | `use_alpha(false)`：buffer 已是 premultiplied | ✅ 已完成 | `edit.rs` | fir 默认 `mul_div_alpha=true`，若不关闭会二次预乘导致边界变暗 |
| 语义 | 保持「约束框」语义（等比缩放） | ✅ 已完成 | `edit.rs::fit_dimensions` | 等价于迁移前 `DynamicImage::resize` 的 `resize_dimensions(.., false)`（该函数为 `pub(crate)`，故本地复刻） |
| 语义 | 匹配框尺寸时不重采样（no-op） | ✅ 已完成 | `edit.rs` | 与 `image::imageops::resize` 的等尺寸快路径一致，避免内核浮点漂移 |
| 语义 | 最近邻保持非线性的纯像素拷贝路径 | ✅ 已完成 | `edit.rs` | `resize_exact(.., Nearest)`，不做传递函数往返 |
| 位深 | 16-bit 输入仍产出 16-bit | ✅ 已完成 | `edit.rs` | `is_16bit` 判定沿用 |
| 测试 | 滤镜映射 / 约束框 / no-op / 16-bit / 预乘 Alpha | ✅ 已完成 | `edit.rs` tests | 新增 6 个用例 |
| 兼容 | WASM 引擎构建 | ✅ 已完成 | `compute-wasm` | `wasm32-unknown-unknown` check 通过 |

## 三、滤镜映射

| 调用方字符串 | 迁移前（image） | 迁移后（fir） |
| :--- | :--- | :--- |
| `nearest` | `FilterType::Nearest` | `ResizeAlg::Nearest`（仍走非线快路径） |
| `triangle` | `FilterType::Triangle` | `ResizeAlg::Convolution(Bilinear)` |
| `catmullrom` / `catmull-rom` | `FilterType::CatmullRom` | `ResizeAlg::Convolution(CatmullRom)` |
| `gaussian` | `FilterType::Gaussian` | `ResizeAlg::Convolution(Gaussian)`（fir 原生支持） |
| `lanczos3`（默认） | `FilterType::Lanczos3` | `ResizeAlg::Convolution(Lanczos3)` |
| 未知名 | `Lanczos3` | `Lanczos3`（行为不变） |

## 四、关键设计决策

1. **直接复用 premultiplied linear f32 工作区**
   `to_premultiplied_linear_f32_with_transfer` 的输出正是 fir `image` feature 支持的 `Rgba32FImage`（`PixelType::F32x4`），因此不需要任何像素拷贝或类型转换，fir 在原工作区上直接做 SIMD 卷积。

2. **必须 `use_alpha(false)`**
   fir 的 `ResizeOptions::mul_div_alpha` 默认为 `true`（对 straight-alpha 输入自动预乘）。我们的 buffer 已预乘，保持默认会导致颜色被乘两次、透明边缘变暗。回归测试 `premultiplied_alpha_prevents_transparent_pixels_from_bleeding` 专门锁定该点。

3. **保留「约束框」语义（重要）**
   迁移前 `DynamicImage::resize` 是**等比缩放**（缩放到能放进 `width × height` 框的最大尺寸），并非强制拉伸到指定尺寸。Web 端的「max constraint box」交互正是建立在这一行为上。因此本次迁移在 `fit_dimensions` 中复刻该语义（`image` 的 `resize_dimensions` 是 `pub(crate)`，无法直接复用），仅替换**内核实现**，不改尺寸语义、不引入宽高比畸变。内部对齐的框（如 16×16 → 8×8）仍精确命中目标尺寸。

4. **最近的最近邻路径保持非线性**
   最近邻无权重混合，线性光往返只会引入传递函数量化误差，故保留 `image` 的纯拷贝快路径。

## 五、行为差异

| 场景 | 迁移前 | 迁移后 |
| :--- | :--- | :--- |
| 输出尺寸 | 等比 fit 到约束框 | 完全一致（`fit_dimensions` 复刻） |
| 匹配尺寸（fit == 源） | `image` 等尺寸快路径返回副本 | 显式 no-op |
| 像素值 | `image::imageops` 卷积核 | fir 卷积核，边缘/权重实现不同，存在 LSB 级差异 |
| 错误处理 | 线性通道失败时静默回退到 `img.resize` | fir 失败返回 `anyhow` 错误（含目标尺寸上下文） |

## 六、回归验证点

- `cargo test -p compute-engine`：49 passed，其中新增：
  - `filter_names_map_to_resize_algorithms`
  - `every_filter_name_resizes_within_the_constraint_box`（全部滤镜名 × 非匹配/匹配框）
  - `fit_dimensions_preserves_aspect_ratio_and_never_collapses`
  - `resize_inside_the_original_box_is_a_no_op`
  - `resize_keeps_16bit_pixels_16bit`
  - `premultiplied_alpha_prevents_transparent_pixels_from_bleeding`
- 既有 `test_linear_light_resize_with_transfer`（线性光 Lanczos3）保持通过。
- `cargo test --workspace`：全绿；`cargo check -p compute-wasm --target wasm32-unknown-unknown` 通过。
- `cargo clippy -p compute-engine`：新增代码零告警。

## 七、后续

- 下一项：`kamadak-exif`（`metadata.rs` orientation 与容器 EXIF 定位）。
- 之后：`moxcms`（ICC/CICP 转换，补上当前刻意省略的 primaries 转换能力）、`mp4parse`（ISOBMFF `colr`/`auxC`，可连带移除本地 `extract_icc_avif`）。
- 备注：fir 的 `Blur` 不在本项范围；`edit.rs` 的高斯模糊仍走 `DynamicImage::blur`，如需 SIMD 可另立一项评估 `imageproc::filter` 或 `libblur`。
