# 处理核心 compute-imageops

`compute/crates/imageops` 把一份 [`ImageOptions`](/contracts/image-options) 应用到一张编码后的图片上。解码、转正、缩放、色彩转换、元数据处理与编码全部交给 libvips（通过 [`rs-vips`](https://crates.io/crates/rs-vips) 绑定）；本 crate 只做两件事：把参数映射到 libvips 选项，以及拦下 libvips 会处理错的输入。

浏览器沙盒里的 `web/app/lib/vips-ops.ts` 是它的逐条对应版本，运行在 wasm-vips 上；两者的差异见[已知限制](/compute/limitations#浏览器沙盒)，一致性由 `task test:consistency` 检查。整条链路与设计理由见[转码链路](/compute/pipeline)。

## 处理流程

```text
process(input, options, limits)
 1. 规范化参数（填默认值，非法参数 → Invalid）
 2. 只读文件头：识别格式、宽高；超过像素上限 → Unsupported
 3. HDR 探测：HEIF/AVIF nclx 或 PNG cICP 的传递函数为 PQ/HLG → Unsupported
 4. 记录是否带 gain map（UltraHDR JPEG）
 5. 旁路：JPEG（含 UltraHDR）→ JXL 无损且不缩放、不转色 → cjxl 直接转码 DCT 系数；
    保留元数据时附带重构数据（可逐字节还原）
 6. 解码 + 转正 + 缩放 + 色彩
      8-bit 源   → thumbnail（一次调用，JPEG/WebP 可在解码时缩小）
      更高位深源 → 加载 → autorot → resize/裁剪 → icc_transform(depth 16)
 7. 编码（见下表）；AVIF 先按 libvips 保存器的规则整理成 RGB(A)，再交给 libavif
```

| 错误 | 含义 | worker 错误码 |
| :--- | :--- | :--- |
| `Invalid` | 参数本身非法 | `invalid_request` |
| `Unsupported` | 输入无法按要求处理：未知或损坏、过大、PQ/HLG、被拦截的加载器 | `unsupported_input` |
| `Failed` | libvips 或 cjxl 在受支持的请求上失败 | `processing_failed` |

三者都不可重试；只有存储失败（`storage_failed`）会自动重试。

## 参数映射

| 参数 | libvips |
| :--- | :--- |
| `resize.fit = inside` | `thumbnail size=down`（`enlarge` 时 `both`） |
| `resize.fit = cover` | `crop=centre`；高位深路径为缩放后居中 `extract_area` |
| `resize.fit = fill` | `size=force`；高位深路径为 `resize` + `vscale` |
| `color = keep` | 不转换像素，保留源 ICC |
| `color = srgb` | `output_profile=srgb`，无 ICC 的源按 sRGB 处理 |
| `metadata` | `keep` 标志：总是 ICC；`keep` 时加 EXIF/XMP/IPTC；保留 gain map 时加 gainmap |
| `effort` 1–10 | 线性映射到各编码器的范围：AVIF（libavif）speed 10–0，HEIC 0–9，JXL 1–9，WebP 0–6，PNG 压缩级别 0–9；未填时不设置，使用编码器自身的默认 |
| `chroma` | `subsample_mode`：`420` → on，`444` → off，未给 → auto |
| `bit_depth` | AVIF/HEIC `bitdepth`；未给时 8-bit 源输出 8，更高位深源输出 10 |

PNG 输出跟随源位深（16 或 8）。JPEG 输出总是 8-bit，带 alpha 的源铺在白底上；保留 gain map 时改用 `uhdrsave`。

## 安全边界

- **untrusted 加载器**：启动时开启 `vips_block_untrusted_set`，PDF、SVG、OpenEXR、FITS、Matlab 等加载器一律拒绝。libjxl 也被 libvips 标为 untrusted，但 JXL 是支持的格式，所以单独放行它的加载器与保存器。
- **资源上限**：像素数在只读文件头时检查（默认 1 亿，worker 可用 `WORKER_MAX_PIXELS` 调整）；下载大小由 worker 限制（默认 200 MB，`WORKER_MAX_SOURCE_BYTES`）。
- **`fail_on=error`**：截断或损坏的文件报错，而不是输出半张图。
- **缓存**：关闭 libvips 操作缓存，任务之间不共享像素。

## 外部工具

### libavif

AVIF 不用 libvips `heifsave`，因为它固定关闭了 aom 的 intra block copy，截图会大 13%–40%（见[转码链路](/compute/pipeline#为什么服务端-avif-交给-libavif)）。`avif.rs` 复刻 libvips 保存前的整理（CMYK 经 ICC 导入、转 sRGB 或 RGB16、最多一个 alpha、转成 8/16-bit），渲染出像素后经 `avif_shim.c` 调用 libavif：

- 编码器固定为 aom；speed 未填 `effort` 时为 6，否则由 `effort` 线性映射到 10–0；线程数为 CPU 核数。
- `chroma` 未填时沿用 libvips 规则（Q ≥ 90 为 4:4:4，否则 4:2:0）；无损为 4:4:4 + identity 矩阵 + quality 100；alpha 用 libavif 默认，即与颜色相同的质量（有损，与 `avifenc` 默认相同）。
- ICC 只在与 RGB 兼容时写入（灰度源的灰度 ICC 会被丢弃，与 libvips 相同）；没有 ICC 时标记 CICP 1/13（sRGB）；有损时矩阵为 BT.601（与 `avifenc` 相同）。
- `metadata=keep` 时写入源图的 Exif 与 XMP，Exif 方向改为 1。与 libvips 不同，源图没有 Exif 时不会补一个只含分辨率的 Exif。

worker 可以用 `WORKER_AVIF_ENCODER=libvips` 把 AVIF 切回 libvips `heifsave`（默认 `libavif`），用于 libavif 出问题时回退，或对比两种编码器。它是 worker 级配置，不进 `ImageOptions`，也不改变产物键：切换前已生成的产物会继续复用。

### cjxl

`cjxl` 是唯一的外部命令，每台 worker 必装，启动时检查，缺失即退出。JPEG → JXL 无损时它直接转码原 JPEG 的 DCT 系数，不重新编码像素；在 `scripts/fixtures` 的 JPEG 上体积减少 9%–29%。EXIF 方向会写进 JXL 码流头。

- `metadata=keep`：附带重构数据（`jbrd`），用 `djxl` 可逐字节还原原文件（测试会验证）。
- `metadata=strip`：`-x strip=exif|xmp|jumbf` 加 `--allow_jpeg_reconstruction=0`。cjxl 规定删元数据就不能保留重构数据，所以不可逐字节还原，但体积更小。

UltraHDR JPEG 也走这条路：gain map 随重构数据原样保存，`djxl` 还原出的仍是带 gain map 的原文件；但 JXL 本身显示的是 SDR 基础图，不带 `jhgm`。像素路径对 JXL 输出同样只给 SDR 基础图，所以旁路不会损失什么。`dynamic_range=sdr` 要求丢弃 gain map，此时不附带重构数据。

## 实测确认的 libvips 行为（8.18）

- `thumbnail` 总是输出 8-bit，因此高位深源走单独的路径。
- 读取 HEIF/AVIF 与 PNG 时不保留 CICP：PQ/HLG 的码值会被当成 sRGB 写出，所以必须在入口拒绝。
- UltraHDR JPEG（`uhdrload`）的 gain map 存在 `gainmap-data` 中，`thumbnail` 会按同样比例缩放它，`uhdrsave` 能写回。
- 选项名写错时 libvips 只打 warning，不报错（例如 8.18 已把 `import_profile` 改名为 `input_profile`），每个编码选项都需要用测试验证效果。

## 测试

| 命令 | 内容 |
| :--- | :--- |
| `cargo test -p compute-imageops` | 几何、转正、元数据、ICC、位深、alpha、格式、HDR 拒绝、gain map、上限、JPEG→JXL 逐字节还原 |
| `task test:fixtures` | 全部素材 × 六种格式，只有 PQ/HLG 素材应被拒绝 |
| `task test:consistency` | 与浏览器实现比较同一输入的产物 |

素材说明见[素材与测试](/quality/fixtures)。
