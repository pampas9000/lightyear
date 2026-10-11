# 处理核心 compute-imageops

`compute/crates/imageops` 把一份 [`ImageOptions`](/contracts/image-options) 应用到一张编码后的图片上。解码、转正、缩放、色彩转换、元数据处理与编码全部交给 libvips（通过 [`rs-vips`](https://crates.io/crates/rs-vips) 绑定）；本 crate 只做两件事：把参数映射到 libvips 选项，以及拦下 libvips 会处理错的输入。

浏览器沙盒里的 `web/app/lib/vips-ops.ts` 是它的逐条对应版本，运行在 wasm-vips 上；两者的差异见[已知限制](/compute/limitations#浏览器沙盒)，一致性由 `task test:consistency` 检查。

## 处理流程

```text
process(input, options, limits)
 1. 规范化参数（填默认值，非法参数 → Invalid）
 2. 只读文件头：识别格式、宽高；超过像素上限 → Unsupported
 3. HDR 探测：HEIF/AVIF nclx 或 PNG cICP 的传递函数为 PQ/HLG → Unsupported
 4. 记录是否带 gain map（UltraHDR JPEG）
 5. 旁路：JPEG → JXL 无损且不缩放、不转色、保留元数据 → cjxl 重新打包
 6. 解码 + 转正 + 缩放 + 色彩
      8-bit 源   → thumbnail（一次调用，JPEG/WebP 可在解码时缩小）
      更高位深源 → 加载 → autorot → resize/裁剪 → icc_transform(depth 16)
 7. 编码（见下表）
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
| `effort` 1–10 | 线性映射到各编码器的范围：AVIF/HEIC 0–9，JXL 1–9，WebP 0–6，PNG 压缩级别 0–9 |
| `chroma` | `subsample_mode`：`420` → on，`444` → off，未给 → auto |
| `bit_depth` | AVIF/HEIC `bitdepth`；未给时 8-bit 源输出 8，更高位深源输出 10 |

PNG 输出跟随源位深（16 或 8）。JPEG 输出总是 8-bit，带 alpha 的源铺在白底上；保留 gain map 时改用 `uhdrsave`。

## 安全边界

- **untrusted 加载器**：启动时开启 `vips_block_untrusted_set`，PDF、SVG、OpenEXR、FITS、Matlab 等加载器一律拒绝。libjxl 也被 libvips 标为 untrusted，但 JXL 是支持的格式，所以单独放行它的加载器与保存器。
- **资源上限**：像素数在只读文件头时检查（默认 1 亿，worker 可用 `WORKER_MAX_PIXELS` 调整）；下载大小由 worker 限制（默认 200 MB，`WORKER_MAX_SOURCE_BYTES`）。
- **`fail_on=error`**：截断或损坏的文件报错，而不是输出半张图。
- **缓存**：关闭 libvips 操作缓存，任务之间不共享像素。

## 外部工具

唯一的外部工具是 `cjxl`：JPEG → JXL 无损时把原 JPEG 的 DCT 系数重新打包，用 `djxl` 可逐字节还原原文件（测试会验证）。缺少 `cjxl` 时退回像素级无损编码，结果仍然无损但不可逐字节还原。

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
