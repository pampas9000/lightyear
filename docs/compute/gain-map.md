# HDR Gain Map 规范与演进规划

本文档记录 HDR Gain Map（增益图）的核心工业规范、数学原理、Lightyear `compute` 当前的设计决策及后续阶段规划。

---

## 1. 规范背景与工业标准

HDR 增益图旨在解决传统单一曝光在 SDR 与 HDR 屏幕之间的显示矛盾。它在保留一张标准 SDR 基底图像的同时，附带一张低分辨率/压缩的增益图与元数据，使得 HDR 屏幕能够按需“提亮”高光，而 SDR 屏幕直接展示基底图像，实现完美向后兼容。

### 1.1 核心标准演变

1. **Apple Adaptive HDR (HEIC / AVIF / JPEG)**：
   - 苹果生态率先广泛使用的私有方案，在 HEIC/AVIF 中将增益图存储为辅助图元（Auxiliary Image），在 JPEG 中存储在 App14 / MPF 块中。
   - 具备单通道（灰度增益）与三通道（RGB 色度增益）两种模式。
2. **Adobe / Google Ultra HDR (JPEG_R / v1)**：
   - Android 14+ 引入的格式，基于 JPEG Container + MPF，在第二个图像段存放增益图，辅以 XMP 元数据描述增益参数。
3. **ISO 21496-1 (统一国际标准)**：
   - 整合 Apple、Adobe 与 Google 规范的国际标准，统一了元数据字段定义（`hdrCapacityMin/Max`, `gainMapMin/Max`, `gamma`, `offsetSdr/Hdr` 等）。
   - AVIF (libavif 1.2+) 与 JXL 社区已逐步收敛至 ISO 21496-1 标准。

---

## 2. 核心原理与数学应用模型

### 2.1 增益图的表示：单通道 vs 三通道

- **单通道（Monochrome Gain Map）**：
  - 增益图仅记录亮度增益标量 $G$。
  - 优点：体积极小（通常仅占原图 5%~10% 数据量）。
  - 缺点：无法校正高饱和色彩（如霓虹灯、夕阳高光）的色彩偏折。
- **三通道（RGB Chromatic Gain Map）**：
  - 增益图记录 $(G_R, G_G, G_B)$ 独立的色彩增益向量。
  - 传输形态通常复用标准视频/图像编码管道（如 YCbCr 4:2:0），在解码重建为 RGB 后再施加计算。
  - 优点：能够独立调整 RGB 峰值通道，彻底防止色相偏移。

### 2.2 恢复数学模型（Application Math）

增益图的物理意义是**在线性光（Linear RGB）空间**中的乘性缩放。任何色调映射或色彩混合操作均遵循以下公式（以单通道简化）：

$$
I_{\text{HDR, linear}} = (I_{\text{SDR, linear}} + \text{offset}_{\text{SDR}}) \times 2^{G_{\text{weighted}}} - \text{offset}_{\text{HDR}}
$$

其中：
- $G_{\text{normalized}} = \frac{I_{\text{gainmap}} - \text{gainMapMin}}{\text{gainMapMax} - \text{gainMapMin}}$
- $G_{\text{weighted}} = G_{\text{normalized}}^{\gamma} \times W(\text{DisplayBoost})$
- $W$ 为根据当前屏幕 HDR 峰值亮度自适应插值的权重系数（0.0 代表纯 SDR 展现，1.0 代表完整 HDR 展现）。

---

## 3. Lightyear 现阶段设计决策

### 3.1 暂不介入转码数学计算

经过对技术栈和工程复杂度的评估，我们在当前版本做出如下明确决策：
- **不进行像素级重构计算**：不在自研 Rust `compute-engine` 中手写单/三通道 Gain Map 拆解、插值与混合算法，避免过早引入不稳定的色彩模型。
- **避免黑盒工具破坏**：诸如 ImageMagick / libvips 在处理带 Gain Map 的图片时，往往将其作为普通图元扁平化或完全丢弃附属元数据，无法正确保持 ISO 21496-1 契约。
- **防御性阻断与安全降级（Phase A 规划，未实施）**：
  - **L0 容器探测**：探测输入文件是否存在 Gain Map 辅助图元（如 HEIC `auxC` / AVIF `tmap` / JPEG MPF）。
  - **白名单门禁**：一旦源文件包含 Gain Map，**禁止触发任何原生 YUV 直通**（Native Passthrough），防止辅助图元脱节丢失。
  - **安全基底导出**：如果用户请求转为标准格式，默认将基底 SDR 内容作为主图像提取并正确标注色彩配置文件，保证在所有 SDR 屏幕上色彩准确。

  > **当前实现状态**：探测与门禁代码已按“无消费方不加功能”（YAGNI）原则整体删除（见[色彩路线图 §已删除](/compute/color-roadmap)）。带 Gain Map 的输入按主图（基底）处理；**Phase A 落地前，原生 YUV 直通不会识别增益图，辅助图元会随直通路径丢失**。对外契约是“不支持 Gain Map”，调用方不应期待双层内容跨容器保留。

---

## 4. 后续演进路线（Roadmap）

HDR Gain Map 的完整支持将分阶段逐步推进：

| 阶段 | 目标与范围 | 实施方式 | 依赖前提 |
| :--- | :--- | :--- | :--- |
| **Phase A（未实施）** | 容器探测与门禁隔离 | 容器解析层识别 Gain Map，YUV 直通阻断，SDR 安全基底回退 | 现有 `compute-server-adapter` 管道 |
| **Phase B** | 专用工具容器搬运 | 利用官方独立 CLI（如 `avifgainmaputil` / `cjxl`）保留跨容器 Gain Map | 确定部署环境中存在成熟工具链 |
| **Phase C (长期)** | 原生 C-ABI 深度接入 | 引入轻量 `libavif` / `libheif` / `libjxl` FFI 绑定，直接读写 Aux Image 与 ISO 21496-1 元数据 | 转码核心启动 in-process FFI 架构升级 |
