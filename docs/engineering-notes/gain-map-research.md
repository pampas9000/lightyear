# HDR Gain Map 直通调研（研究笔记）

> **2026-10 更新**：本笔记写于自研 Rust 引擎时期，下面几处已过时，以此为准：
>
> - **libvips 8.17+ 已支持 UltraHDR JPEG**：`uhdrload` 把 gain map 读进 `gainmap-data` 及 `gainmap-*` 元数据，`thumbnail` 按同样比例缩放它（实测 192×256 → 48×64），`uhdrsave` 能写回。Lightyear 换用 libvips 后，UltraHDR JPEG → JPEG 已保留 gain map。
> - libvips 仍**不**读写 AVIF/HEIC 的 gain map（`tmap`、Apple `auxC`），也不保留 HEIF/AVIF/PNG 的 CICP；因此 PQ/HLG 源图被明确拒绝，现状见[HDR 与已知限制](/compute/limitations#hdr)。
> - 第三节里的 `compute-engine` / `compute-server-adapter`、Y4M 管线均已删除。以后的 HDR 通道应作为 `compute-imageops` 旁的一条容器层路径，元数据对齐 libvips 的 `gainmap-data`，libvips 支持 AVIF gain map 后整体删除。
>
> 勘误（2026-10 初）：初稿中"`cjxl` 会自动识别 UltraHDR JPEG 的 MPF/XMP 并把 Gain Map 封装进 JXL（`jhgm`）"的结论**不成立**——libjxl 没有该能力（上游 issue #4588），带 MPF 的 JPEG 按普通 JPEG 重新打包会静默丢失 Gain Map。正确解法见下文"针对 JXL 目标"一节。

## 问题

到底应该选择 ImageMagick/ffmpeg/libvips 还是自己写代码？它们支持 HDR Gain Map 透传吗？比如 HEIC (HDR Gain Map) -> AVIF (HDR Gain Map)

---

这是一个非常深刻且直击现代 HDR 图像管线核心的问题。

你在文档和现状分析里已经抓住了最本质的矛盾：**“传递轴 HDR（Single-layer PQ/HLG）”与“双层 Gain Map HDR（Base SDR + Aux Gain Map + Metadata）”是两套完全异构的系统。**

如果引入通用工具链（libvips / ImageMagick / FFmpeg），在**完全不碰像素（即不将 HDR Gain Map 合成烘焙进像素）的前提下，能不能尽量降低质量损失并保留 Gain Map？**

答案直接说：
**单纯用 vips / ImageMagick / FFmpeg，目前几乎“全军覆没”，无法做到优雅保留或直通 Gain Map。** 甚至它们在处理此类图像时，会比你现在的管线更加危险。

---

### 一、 为什么 vips / ImageMagick / FFmpeg 在 Gain Map 直通上做不到？

#### 1. libvips
- **调研时（8.16 及以前）**：单图像流模型，HEIC 只读主图像，AVIF 不解 gain map，UltraHDR JPEG 当普通 JPEG，`heifsave`/`jxlsave` 无法写 gain map——gain map 被静默丢弃。
- **8.17 起**：新增 `uhdrload`/`uhdrsave`（libultrahdr），UltraHDR JPEG 的 gain map 作为 `gainmap-data` 随图像流转，缩放时同步处理。HEIF/AVIF/JXL 的 gain map 仍不支持。
- **结论**：JPEG → JPEG 可以交给 libvips；其他容器的 gain map 仍需要专用工具链。

#### 2. ImageMagick (v7)
- **现状**：ImageMagick 支持读取多图层/多帧（`image[0]`, `image[1]`），但在 Gain Map 上是灾难：
  - 读取带 Gain Map 的 HEIC：某些版本会把 Gain Map 读作“第二帧”，但丢失所有私有 metadata（Apple XMP / ISO 21496-1）。
  - 编码输出：**无法写出符合 Apple/ISO/Google 规范的 Gain Map 容器**。你把两帧存进去，它只会存成一张多帧动画或多页图片，OS（iOS/Android/macOS）无法识别出 HDR 效果。
  - **严重陷阱**：在某些色彩操作下，它甚至会因为读到了带有增益元数据的 XMP，但只改了主图尺寸，导致元数据与像素错位。

#### 3. FFmpeg
- **现状**：FFmpeg 强在视频流和基色/传递函数（BT.2020 PQ/HLG）。
- **解码/编码行为**：
  - FFmpeg 是视频流容器思维。HEIC/AVIF 在 FFmpeg 中走 `hevc` / `av1` parser，它根本不理会 ISOBMFF 中针对静止图像定义的 `auxC` / `tmap` 项。
  - 即使你把 secondary item 提取出来作为视频 stream 1，FFmpeg 的 muxer 目前也**完全没有能力封装 ISO 21496-1 或 Apple HEIC 格式的 Gain Map item 关系**。

---

### 二、 如果要“完全不碰像素，保留 Gain Map”，正确的工业解法是什么？

既然不能做像素级的 tone-mapping，又要保留 HDR Gain Map，**这根本不是图像解码器（Image Processing）的任务，而是容器解复用与复用（Demuxing & Remuxing）的任务。**

在工业界，目前只有以下专用链路能够做到“尽量无损或接近直通”保留 Gain Map：

#### 方案 A：专用 CLI / 库拼接（最靠谱的管道化做法）

1. **针对 AVIF 目标：`libavif` 原生工具链（`avifgainmaputil`）**
   - Google / AOM 在 libavif 源码树里专门提供了一个工具：`avifgainmaputil`（支持 `swap` / `extract` / `combine`）。
   - **机制**：
     - 如果源是 Ultra HDR JPEG，提取基础图与增益图。
     - 用 `avifgainmaputil combine -b base.jpg -g gainmap.jpg -m metadata.xml out.avif`。
     - 它会在 AVIF 容器中规范写入 `tmap` box、`altr`（alternate group）以及遵循 ISO 21496-1 的 metadata。
     - **保真度**：基础图可以走原样 JPEG 压缩或 AV1 高质量压缩，Gain Map 是灰度图，完全零色彩损失，只保留空间增益。

2. **针对 JXL 目标：`libjxl` 的 `jhgm` (JPEG XL Gain Map)**
   - JXL 文件格式规范 ISO/IEC 18181-2 定义了 Gain Map 的官方封装 `jhgm` box（libjxl ≥ 0.11 实现；元数据为 ISO 21496-1 二进制 blob）。
   - **box 结构**（`lib/include/jxl/gain_map.h` + `lib/extras/gain_map.cc`）：
     `version(1B) | metadata_size(2B) | ISO 21496-1 元数据 | color_encoding | 可选压缩 ICC | gain map 的 JXL 裸码流`。
     gain map 本身是**一条独立的 JXL 码流**——不是额外通道，也不是原样嵌入的 JPEG 字节。因此“Gain Map JXL”在本质上必然是**两次图像编码、一个文件**（base 可走 JPEG 无损重构，成本极低；gain map 必须单独编码一次）。
   - **纠错（2026-10 源码核查）**：不存在“`cjxl` 自动识别 UltraHDR JPEG 的 MPF/XMP 并封装为 `jhgm`”这条路径。
     - libjxl main（0.12 之后）全库没有 MPF / `hdrgm` / UltraHDR 的任何解析代码；`jhgm` 仅在**输入本身就是 JXL**时被读入 `metadata.jhgm` 并原样写回（`lib/extras/dec/jxl.cc` ↔ `lib/extras/enc/jxl.cc`），属于 round-trip 透传。
     - `cjxl input_ultrahdr.jpg output.jxl` 只保留 SDR base，Gain Map 被静默丢弃（上游 issue #4588 仍开放）。验证：`jxlinfo output.jxl` 中不会出现 `Gain map (jhgm) box`。
   - **正确解法**（无现成 CLI，需要 libjxl C API）：
     1. 解析 UltraHDR JPEG：拆出 base JPEG、gain map JPEG、元数据（较新的 UltraHDR 会在 gain map 图的 APP2 中携带 ISO 21496-1 二进制 blob，可直接取用；仅有 XMP `hdrgm:` 的旧文件需做字段等价转换）。
     2. gain map 解码后单独编码为**裸 JXL 码流**（CLI 近似做法：`cjxl gainmap.png gainmap.jxl --container=0`）。
     3. `JxlGainMapWriteBundle` 生成 box 内容 → `JxlEncoderAddBox(enc, "jhgm", ...)`；base 用 `JxlEncoderAddJPEGFrame`（无损重构，近乎零成本）或普通 frame。输出必须走容器（box 无法放进裸码流）。
   - **HEIC/AVIF $\rightarrow$ JXL**：同样没有一步式 CLI。需要用 ISOBMFF 解析器提取增益图平面与 ISO 元数据后，按上面第 2、3 步组装 `jhgm` box（不是 `JxlEncoderAddImageFrame` 额外通道）。

3. **针对 HEIC（Apple 生态）**
   - Apple 的 Gain Map（`urn:com:apple:photo:2020:aux:hdrgainmap`）深度绑定在 iOS/macOS 的 `ImageIO.framework`（`CGImageSource` / `CGImageDestination`）。
   - Linux/Rust 服务端环境下：`libheif` 近期版本开始有辅助图像 API，但写出兼容 iOS 的 Apple 私有 HDR HEIC 非常繁琐，必须完整镜像 Apple 的 XMP 字典（`HDRGainMapVersion`, `HDRGainMapHeadroom` 等）。

---

### 三、 implementation 里的设计与细节该如何落地？

如果你的目标是**“在 lightyear 架构下，以最低失真、且只在存在调用方时才算完成的标准来落地”**，必须把这套方案切分成具体的 implementation 细节：

#### 1. 架构定位修正：Gain Map 属于“容器管道（L2/L3）”，绝不属于“像素管道（L1）”
现在的 `compute-engine` 核心是基于像素的（`apply_pipeline_with_transfer`），如果走 RGB 线性光浮点，Gain Map 的概念就彻底碎了（因为被强行烘焙成了 PQ/HLG）。
因此，必须在 `compute-server-adapter` 和 `compute-worker` 的 **L2 直通层** 构建 Gain Map 逻辑：

```text
               ┌── [JPG -> JXL] ──> 无 CLI 直通：需自建 jhgm box（见 §二-2）
               │
源图像输入 ─────┼── [带 Gain Map 的 AVIF/HEIC] ──┐
               │                                │
               └── [需要转码/需要编辑]            ▼
                                          容器分流器 (Demuxer)
                                          ┌──────────────┴──────────────┐
                                          ▼                             ▼
                                     Primary Item                  Gain Map Item
                                     (SDR Base)                 (8/10-bit Grayscale)
                                          │                             │
                                  常规转码/编辑/降采样            同步做相同尺寸降采样
                                          │                             │
                                          └──────────────┬──────────────┘
                                                         ▼
                                                容器重组 (Remuxer)
                                       (AVIF tmap / JXL jhgm / Apple auxC)
```

#### 2. 具体 implementation 文件与细节改动

* **第一步：容器层探测（`compute-engine::color::metadata`）**
  - 在目前的 `probe_alpha_channel` 旁边，复活并规范化：
    ```rust
    pub struct GainMapDescriptor {
        pub format: GainMapFormat, // Apple, UltraHDR, Iso21496_1
        pub metadata_bytes: Vec<u8>,
        pub item_id: u32,
    }
    ```
  - 解析 ISOBMFF 的 `iref`（item reference）：
    - 匹配 `auxl` 或 `cdsc` 引用，且 `auxC` 的 URN 为 `urn:com:apple:photo:2020:aux:hdrgainmap` 或 `urn:iso:std:iso:ts:21496:-1`。
    - 对 JPEG，探测 APP2（MPF 扩展标记）与 XMP 中的 `hdrgm:` / `Container:Directory`。

* **第二步：解码层双流提取（`compute-server-adapter::transcode::decode`）**
  - **切断 Y4M 假设**：Y4M 是单流，容不下 Gain Map。
  - 如果目标不是直通（例如要把 HEIC Gain Map 转为 AVIF Gain Map）：
    - `heif-dec` 无法单命令导出 Gain Map。需要引入或封装精炼的 CLI（或基于 `libavif` 的 `avifgainmaputil`，以及基于 `libheif` 的轻量解包工具），将输入拆为两份 raw 数据：
      `base.y4m`（基础 SDR）+ `gainmap.y4m`（单通道灰度增益图）+ `gainmap_metadata.json`。

* **第三步：有操作时的保真联动（Resize / 旋转）**
  - **降分辨率（Downsampling）**：
    - 这是最容易翻车的地方！**主图缩小了，Gain Map 必须按相同比例缩小**。
    - 主图在 **Linear RGB** 空间做 Lanczos3 降采样。
    - **Gain Map 必须在它的增益域（通常是 Log 空间或 Normalized Linear）做双线性/Lanczos 插值**，绝对不能直接在非线性伽马下粗暴 resize。
  - **旋转（Rotation）**：
    - 两者必须施加完全相同的 EXIF/正交旋转变换。

* **第四步：编码与容器回写（`compute-server-adapter::transcode::encode`）**
  - **产物是 AVIF**：
    丢弃单流 `avifenc --stdin`，调用 `avifgainmaputil combine` 或直接使用 `libavif` 带有 `--gain-map` 参数的编译版本。
  - **产物是 JXL**：
    - 不存在“让 `cjxl` 处理原始 JPEG 就自动封装 `jhgm`”的路径（纠错见 §二-2）。任何源都需要显式组装：gain map 单独编码为裸 JXL 码流 → `JxlGainMapWriteBundle` → `JxlEncoderAddBox("jhgm", ...)`。
    - base 若来自 JPEG，用 `JxlEncoderAddJPEGFrame` 走无损重构；否则常规编码。产物用 `jxlinfo` 检查 `jhgm` box 是否落盘。

### 四、 总结与决策建议

1. **千万别指望 ImageMagick/vips/ffmpeg**：它们不是为多层元数据绑定图像（Gain Map HDR）设计的，盲目引入不仅保不住 Gain Map，还会引入极大的动态库体积和内存损耗，且最终依然是“静默丢失”。
2. **坚持 lightyear 既有的决定**：在没有端到端需求前不实现 Gain Map 处理。如果你们业务目前**没有端到端输出 Gain Map 的硬性指标**（即消费端并不严格依赖手机屏幕点亮 HDR 高光），那么维持现状——“将 SDR 基础图以极高保真度（10-bit / Blue noise / 444 / 正确 CICP）透传”，是工程收益最高、最不容易出 Bug 的方案。
3. **如果要支持，优先只做 `JPEG -> JXL`（保留 Gain Map）**：
   - 这**不是零代码**：上游没有 CLI 路径，必须在 `compute-server-adapter` 解析 UltraHDR（base / gain map / 元数据），并用 libjxl C API 组装 `jhgm`（两次图像编码、一个文件，详见 §二-2）。
   - 反面警示：**不要**把带 MPF 的 JPEG 当普通 JPEG 走直通——`cjxl` 会静默丢弃 Gain Map，产出“成功但已无 HDR”的文件。
   - 更简单的替代（若不需要双层结构）：base × gain map 合成线性 HDR，单次编码为 PQ/HDR JXL；放弃精确 SDR 回退与向后兼容语义。
