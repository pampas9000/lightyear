# 编解码器与引擎架构

本文档定义 Lightyear `compute` 转码核心中编解码器（Codec）、命令行工具（CLI）与编排层（Orchestrator）的三层分工模型，并给出支持格式的 Provider 矩阵、直通机制及 Alpha 通道处理规范。

> 本页区分现有路径和设计目标。仍需完善的颜色、资源边界和组合验证问题，见[实现审查与取舍](/compute/engineering-review)；已修复的局部正确性边界以本页和[已知限制](/compute/limitations)为准。

---

## 1. 架构三层模型

在 `compute` 的设计中，编解码能力按照职责清晰划分为三层，避免“黑盒工具”抹平画质与色彩控制力：

```
┌─────────────────────────────────────────────────────────────┐
│                      编排层 (Orchestrator)                   │
│   compute-server-adapter (路由/降级/直通) + compute-engine (色彩/编辑) │
└──────────────┬───────────────────────────────┬──────────────┘
               │ 内存/管道中转 (Y4M / PAM / PNG)  │
┌──────────────▼──────────────┐ ┌──────────────▼──────────────┐
│       CLI / 外部进程层       │ │       原生库 / C-ABI 层      │
│  avifenc, cjxl, heif-enc    │ │  (轻量 Rust crate 或待扩展)   │
│  (强隔离、零污染、原厂最佳实现) │ │  image-rs, ravif, yuv      │
└──────────────┬──────────────┘ └──────────────┬──────────────┘
               │ 动态链接 / 二进制调用           │ 静态嵌入 / FFI
┌──────────────▼───────────────────────────────▼──────────────┐
│                    底层编解码实现 (Codec)                     │
│       libaom / libjxl / libheif (x265) / libwebp / libyuv   │
└─────────────────────────────────────────────────────────────┘
```

1. **底层 Codec（编解码库）**：负责具体的像素压缩算法标准实现（如 `libaom` 负责 AV1、`libjxl` 负责 JPEG XL、`x265` 负责 HEVC）。
2. **执行载体（CLI vs In-process FFI）**：
   - 服务端首选 **CLI 子进程执行**（`avifenc`, `cjxl`, `cwebp`, `heif-enc`, `heif-dec`），减少在 Rust 端维护 C-ABI 绑定与跨平台构建的成本。子进程崩溃通常不会直接终止 Worker，但它们共享宿主权限和资源；当前临时目录、超时与 `kill_on_drop` 不是 OS 安全沙盒，也不意味着免疫底层漏洞或 OOM。
   - 纯 Rust 库作为 **Portable Fallback**（如 `image-rs`, `ravif`），在未安装专用 CLI 或 WASM 运行时生效。
3. **编排层（Orchestrator）**：
   - 由 `compute-server-adapter`（IO、探测、CLI 调度、直通判断）与 `compute-engine`（像素管线、CICP/NCLX 换算、色调映射、采样转换）协同组成。
   - 编排层完全掌控转码路径、色彩保真度策略（Describe, don't convert）与元数据传递，不将管线控制权交给如 ImageMagick / libvips 等第三方重型一体化黑盒。

---

## 2. 静态格式与 Provider 矩阵

目标格式严格收敛为 6 种主流静态图像格式。每个角色的主用（Primary）与备用（Fallback）实现如下：

| 格式 | 解码 (Primary / Fallback) | 编码 (Primary / Fallback) | 核心特性与通道支持 |
| :--- | :--- | :--- | :--- |
| **AVIF** | `avifdec` (CLI) / `compute-engine` | `avifenc` (CLI) / `ravif` (Rust) | 支持 8/10-bit、YUV420/422/444、完整 CICP 注入；Alpha 经 PNG16 转接 |
| **HEIC** | `heif-dec` (CLI) / — | `heif-enc` (CLI) / — | 依赖 `libheif`；PNG 输入可传 ICC/Alpha，输出请求可选 10-bit；并无完整输出 NCLX 参数链路或全版本布局保真保证 |
| **JXL** | `compute-engine` (`jxl-oxide`) | `cjxl` (CLI) / — | JPEG 重构、PAM 裸流和 ICC/命名空间提示；`djxl` 当前主要用于探测/测试，并非实际解码 fallback |
| **WebP** | `compute-engine` (`image`) | `cwebp` (CLI) / `image` (无损 Fallback) | 支持有损/无损、ICC 搬运、透明通道 |
| **PNG** | `compute-engine` (`image`) | `compute-engine` (`image`) | 8/16-bit 完整精度、ICC 保留、透明通道无损传递 |
| **JPEG** | `compute-engine` (`image`) | `compute-engine` (`image`) | 8-bit YCbCr、EXIF Orientation 解析与烘焙 |

*注：WASM 解码主要走 portable engine，编码为 portable engine 或 `@jsquash/jxl` 等独立路径；不与服务端所有颜色/位深能力对等。上表不豁免审查 R1-R4/R7 的已知缺陷。*

---

## 3. Alpha 通道处理规范

Alpha 通道是图像转码中最易发生隐式色彩破坏或通道丢弃的边界。基于各工具底层的实际支持约束，我们明确以下处理规范：

### 3.1 Y4M 格式的通道限制

Y4M（YUV4MPEG2）作为视频/平面流的中间传输格式：
- **规范标准**：只定义了一个带 Alpha 的标签 `C444alpha`，其规格严格限定为 **8-bit YUV 4:4:4 + 8-bit Alpha**。
- **高位深与抽样限制**：不存在任何被广泛支持的 `C444alpha10` 或 4:2:0/4:2:2 带 Alpha 标签。libavif 在遇到非 8-bit 4:4:4 的 Y4M 请求写 Alpha 时，会直接警告并丢弃 Alpha 通道；ffmpeg 的 y4m muxer 甚至完全不支持 YUVA。
- **heif-dec 的 Y4M 局限**：libheif 内部的 `Y4MEncoder` 声明 `supports_alpha() = false` 且固定输出 8-bit 4:2:0。如果让带透明通道的 HEIC 输出 Y4M，底层会自动将图像在**棋盘格背景**上进行合成扁平化，造成永久性画质破坏。

### 3.2 规范实施：统一通过 PNG16 传递 Alpha

目标是让需要保留 Alpha 的 AVIF/HEIC 避开不承载透明度的 Y4M 路径，使用保位深的无损 PNG 中转：

```
[源图像 (带 Alpha)] 
       │
       ▼
   解码阶段 (avifdec -d 16 / heif-dec 到 .png)
       │
       ▼ (内存中保全 16-bit RGBA 像素与无损 Alpha)
   处理阶段 (compute-engine 像素调整 / 色彩标记)
       │
       ▼ (落盘或管道传递 16-bit PNG)
   编码阶段 (avifenc / heif-enc 读取 PNG)
       │
       ▼
[目标图像 (AVIF / HEIC 保全 Alpha)]
```

- **编码器支持**：`avifenc`（支持 `--qalpha` 独立量化）和 `heif-enc`（原生解析 PNG Alpha）均完整支持读取带透明通道的 PNG。
- **当前实现**：RGBA 与灰度 Alpha 均会触发 PNG 路径；AVIF 在分支选择前统一解析有效 depth/chroma，随后再选择 PNG Alpha 或 Y4M。中间 PNG 位深由实际像素类型决定，并非无条件 16-bit。
- **不扩展特例**：`C444alpha` 优化暂缓；没有测得 PNG 中转成为瓶颈前，不增加另一套透明路径。

---

## 4. 原生 YUV 直通（Native Passthrough）

YUV 直通旨在跨容器转码时，跳过“YUV → RGB 重建 → YUV 重新分解”的重采样损失。

### 4.1 标记为 Experimental

原生 YUV 直通当前被正式标记为 **`experimental`**。由于各底层 CLI 工具对色度对齐、容器 Header 的解析容差存在细微差异，该机制默认仅在通过严苛白名单校验时触发。

### 4.2 白名单约束

当前实现检查以下主要条件；它还不是完整的无损保留门禁：

1. **格式对**：严格限定在有限集合：
   - `AVIF -> AVIF`
   - `HEIC -> AVIF`
2. **无编辑操作**：`ops.is_empty()`（无缩放、无裁剪、无旋转、无水印）。
3. **无色彩重映射**：`tone_mapping == None` 且未请求矩阵/色彩空间重投影。
4. **无 Alpha 通道**：`!probe_alpha_channel()`（带透明通道必须退回 §3 的 PNG16 管道）。
5. **布局相等判断**：adapter 内部的 `YuvLayout` 直接比较源端与目标端的 `ChromaSubsampling`、位深、矩阵和 range。未实现的 RGB/Gray/CMYK 协商及 `Handoff` 抽象已移除。

**未实施或不完整：** Gain Map 没有探测/门禁；矩阵转换没有独立直通路径；完整外部工具版本矩阵尚未验证。当前已读取 `color` 与 legacy `engine_params` 的有效 depth/chroma，显式 `"none"` 不再阻断 native，且会先检查目标编码器可用性。布局相等只说明已比较的平面参数相等，不代表所有请求和辅助数据已被保留。

Gain Map 风险见[已知限制](/compute/limitations)。AVIF/HEIC→HEIC 的 Y4M 直通、MatrixOnly 和原始码流直通暂不扩展，条件见[暂缓项 D4](/compute/engineering-review)。
