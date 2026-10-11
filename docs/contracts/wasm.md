# WASM 契约

浏览器边界由 `compute/bindings/wasm/src/lib.rs` 定义（`wasm-bindgen`，构建产物输出到 `web/public/wasm`）。

## 导出函数

| 函数 | 签名 | 说明 |
| :--- | :--- | :--- |
| `browser_convert` | `(data: &[u8], format: &str, quality: u8) -> Result<Vec<u8>, JsValue>` | 基础转换，等价于带空色彩参数的 advanced 版本 |
| `browser_convert_advanced` | `(data, format, quality, color_json: Option<String>) -> Result<Vec<u8>, JsValue>` | 带 `ColorEncoding` 输出管线（色调映射、抖动、位深） |
| `browser_edit_image` | `(data, ops_json: &str, format, quality, color_json) -> Result<Vec<u8>, JsValue>` | 应用编辑链后编码 |
| `browser_edit_to_raw_rgba` | `(data, ops_json, color_json) -> Result<JsValue, JsValue>` | 应用编辑链后导出裸 RGBA（供 `@jsquash/jxl` 等外部编码器） |
| `browser_decode_raw_pixels` | `(data) -> Result<JsValue, JsValue>` | 解码为裸像素，返回 `{width, height, is_16bit, optimal_chroma, pixels}` |
| `browser_get_metadata` | `(data) -> Result<String, JsValue>` | 返回 JSON 字符串 `{width, height, format, size}` |

`browser_decode_raw_pixels` 的 `pixels` 是真实 `Uint8Array`（16-bit 时按 u16 小端展开），可通过 transferable 零拷贝回传主线程。

## 目标格式字符串

`jxl`、`avif`、`webp`、`heic`、`jfif`/`jpg`/`jpeg`（归一化为 JPEG）、`png`。

## 编辑算子（`ops_json`）

`ops_json` 是 `ImageEditOp` 数组（`serde` 标签 `type`，snake_case）：

```jsonc
[
  { "type": "resize", "width": 1920, "height": 1080, "filter": "lanczos3" },
  { "type": "rotate", "degree": 90 },
  { "type": "flip", "direction": "h" },
  { "type": "grayscale" },
  { "type": "blur", "sigma": 1.5 },
  { "type": "adjust", "brightness": 0.1, "contrast": 0.2 }
]
```

- `filter`：`nearest` / `triangle` / `catmull-rom` / `gaussian` / `lanczos3`（默认）
- `degree`：90 / 180 / 270
- `direction`：`h` / `v`

## 色彩 JSON（`color_json`）

可选，反序列化为 `ColorEncoding`（字段见[契约总览](/contracts/#领域类型-compute-types)）。语义：描述**源图**色彩空间，用于编辑时的传递函数线性化与输出管线；**不做基色/色域转换**。输出管线固定目标传递为 sRGB。

## 能力矩阵

| 目标格式 | 编码路径 | 状态 |
| :--- | :--- | :--- |
| `webp` / `jpeg` / `png` / `avif` | `compute-engine`（`image` / `ravif`） | 可用 |
| `jxl` | Web Worker 走 `@jsquash/jxl`；`compute-engine` 内编码显式不可用 | 可用（经 JS 侧编码器） |
| `heic` | 解析成功但编码返回错误 | 不可用 |

已知限制：浏览器端编码器无法写入 ICC/CICP，广色域源图按原始像素输出但不带标识。详见[已知限制](/compute/limitations)。

## 兼容规则

- 现有 6 个函数签名保持稳定；新增能力以新函数形式添加
- 修改格式字符串集合或算子 JSON schema 属于破坏性变更
- 修改返回值的所有权/二进制编码方式属于破坏性变更
- `quality` 有效范围 `1..=100`
