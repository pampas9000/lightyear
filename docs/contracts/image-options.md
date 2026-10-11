# ImageOptions 契约

`ImageOptions` 描述一次图像任务的**输出意图**，由 Web、API、MCP、worker 和浏览器沙盒共用。源图的色彩空间、位深、方向、HDR 等一律由程序探测，不出现在参数里。

| 位置 | 作用 |
| :--- | :--- |
| `compute/crates/types/src/image_options.rs` | 唯一定义（Rust），含跨字段校验与规范化 |
| `docs/contracts/schemas/image-options.schema.json` | 由 Rust 类型生成的 JSON Schema |
| `docs/contracts/fixtures/image-options.json` | Rust 与 Go 共同运行的用例：合法输入的规范化结果、必须拒绝的输入 |
| `server/internal/imageoptions` | Go 侧解码、校验、规范化与产物键 |
| `web/app/lib/types/image-options.ts` | 由 Schema 生成的 TS 类型 |

修改规则时同时更新 Rust 类型与 fixtures，然后执行 `task gen:types`；`task check` 会检查 Schema 与 TS 类型是否为最新。

## 字段

| 字段 | 取值 | 默认 | 说明 |
| :--- | :--- | :--- | :--- |
| `format` | `avif` `jxl` `webp` `jpeg` `png` `heic` | 必填 | |
| `quality` | 1–100 | AVIF/HEIC 60，JXL/WebP 80，JPEG 82 | 不能与 `lossless` 同用；PNG 没有该项 |
| `lossless` | bool | `false` | JPEG 不可用 |
| `effort` | 1–10 | 5 | 1 最快，10 体积最小；JPEG 没有该项 |
| `resize` | `{width?, height?, fit, enlarge}` | 不缩放 | 宽高 1–16384，至少给一个；`fit` 为 `inside`（默认）/`cover`/`fill`，后两者需同时给宽高；`enlarge` 默认 `false` |
| `color` | `keep` `srgb` | `keep` | `keep` 保留源 ICC、不转换像素；`srgb` 转换并标记为 sRGB |
| `dynamic_range` | `keep` `sdr` | `keep` | HDR 无法保留时 `keep` 会明确报错 |
| `metadata` | `keep` `strip` | `strip` | EXIF 与 XMP；ICC 由 `color` 决定。方向一律烘焙进像素 |
| `chroma` | `420` `444` | 自动 | 仅 AVIF/HEIC/JPEG，不能与 `lossless` 同用 |
| `bit_depth` | 8 `10` | 跟随源图 | 仅 AVIF/HEIC |

未知字段、默认字段写成 `null` 都会被拒绝；可选字段（`quality`、`effort`、`resize`、`chroma`、`bit_depth`）写 `null` 等同于未填写。

## 规范化与产物键

规范化会先校验，再把所有默认值显式填上，因此"省略默认值"和"显式写出默认值"得到相同结果。存库和派发给 worker 的都是规范化后的参数。

产物键只在 Go 侧计算：

```text
sha256("image-options/v1\n" + 源标识 + "\n" + 规范化参数的 JSON)
```

源标识必须指向内容本身：目前用源文件 ID（相同内容上传时会合并为同一文件记录）；客户端上报但未经校验的哈希不能使用。

同一用户、同一源图、同一意图的任务据此复用已有产物。规范化规则或默认值发生变化时，需要提升前缀中的版本号。
