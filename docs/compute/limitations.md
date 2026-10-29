# HDR 与已知限制

本页列出当前**明确不支持**或**行为与直觉不同**的地方。改进计划统一记在[路线图](/roadmap)。

## HDR

当前只处理 SDR 管线，HDR 分两类对待：

| 源图 | 例子 | 行为 |
| :--- | :--- | :--- |
| 单层 HDR（传递函数为 PQ 或 HLG） | HDR AVIF/HEIC、带 `cICP` 的 PNG | **拒绝**（`unsupported_input`），`dynamic_range` 为 `keep` 或 `sdr` 都一样 |
| SDR 基础图 + gain map | UltraHDR JPEG（含 Apple/Google 手机照片） | 输出 JPEG 且 `dynamic_range=keep`：保留 gain map，随缩放同步缩放；其他格式或 `sdr`：输出 SDR 基础图 |
| 带 gain map 的 AVIF/HEIC | iPhone HEIC、AVIF `tmap` | libvips 只读主图像：基础图是 SDR 时按 SDR 输出（gain map 丢失）；基础图本身是 PQ/HLG 时按上一行拒绝 |

拒绝 PQ/HLG 的原因：libvips 读取 HEIF/AVIF 与 PNG 时不保留 CICP，PQ/HLG 码值会被当成 sRGB 写出，产物"成功"但颜色完全错误。在有色调映射之前，明确报错比静默出错好。

探测方式是读取容器里第一个 `colr`/`nclx` box（或 PNG `cICP` chunk）的传递函数字段，不解析 item 关系。

### 以后的 HDR 通道

计划在 `compute-imageops` 旁加一条只处理 HDR 的路径，用 libavif / libultrahdr 在容器层处理 gain map，元数据与 libvips 的 `gainmap-data` 对齐；libvips 支持 AVIF gain map 后可整体删除。背景调研见 [HDR Gain Map 调研](/engineering-notes/gain-map-research)。

## 服务端

- **元数据**：`metadata=keep` 保留 EXIF、XMP、IPTC；方向一律烘焙进像素。不支持只保留其中一部分。
- **JPEG 输出**：总是 8-bit；带 alpha 的源铺在白底上。
- **PNG 输出**：位深跟随源图（8 或 16），不能指定。
- **动图**：只处理第一帧。
- **JPEG → JXL 直接转码**：只在"无损、不缩放、`color=keep`"时走 `cjxl`；其他组合是普通的像素编码。只有再加上 `metadata=keep`（默认是 `strip`）才可逐字节还原；UltraHDR 源在 `dynamic_range=sdr` 时也不可还原。
- **AVIF 的 Exif**：服务端 AVIF 写入源图 Exif 时只把方向改为 1，不像 libvips 那样更新尺寸字段，也不会给没有 Exif 的源图补一个。
- **UltraHDR → JXL**：JXL 只显示 SDR 基础图；gain map 只存在于重构数据里，用 `djxl` 还原成 JPEG 后才回来。

## 浏览器沙盒

沙盒用 wasm-vips（与服务端同一个 libvips），差异来自 wasm 构建本身：

| 限制 | 影响 |
| :--- | :--- |
| 需要跨源隔离（`SharedArrayBuffer`） | 全站响应头为 COOP `same-origin` + COEP `credentialless`。Safari 不支持 `credentialless`，沙盒在 Safari 中不可用 |
| 没有 HEVC 编解码器 | HEIC 既不能读也不能写；沙盒不提供 HEIC 输出，文件选择器不接受 HEIC |
| JXL 解码器拒绝带 Exif/XMP box 的文件 | 这类 JXL 无法作为沙盒输入（报 `JxlDecoderProcessInput`） |
| 没有 `cjxl` | JPEG → JXL 无损会重新编码像素，结果无损但不可逐字节还原 |
| AVIF 用 libvips `heifsave` 编码 | 服务端用 libavif。像素一致，但截图类 AVIF 在沙盒里大 13%–40%；不填 `effort` 时沙盒为 speed 5，服务端为 speed 6 |
| 首次使用需要下载 wasm | 核心约 2.0 MB，AVIF/HEIF 模块 1.25 MB，JXL 模块 0.8 MB（gzip） |

以上差异在 `scripts/consistency.ts` 中登记为预期失败；wasm-vips 补上某项后，测试会提示更新代码与本页。
