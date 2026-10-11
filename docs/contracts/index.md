# 契约总览

跨组件边界的格式与规则。每份契约都有一个权威定义，另一侧要么由它生成，要么用同一批 fixtures 做测试。

| 边界 | 文档 | 权威定义 | 另一侧如何保持一致 |
| :--- | :--- | :--- | :--- |
| 图像处理参数 | [ImageOptions](/contracts/image-options) | `compute/crates/types/src/image_options.rs` | JSON Schema 与 TS 类型由它生成（`task gen:types`，`task check` 检查）；Go 镜像与 Rust 跑同一份 fixtures |
| `web` → API | [HTTP API](/contracts/http-api) | `server/internal/api` | Web 端 `app/lib/types` 手写 |
| orchestrator ↔ worker | [队列（Redis Streams）](/contracts/queue) | `protocol.rs` 与 `queue.go` 双侧镜像 | 双侧对 `fixtures/{dispatch,results}-v2.json` 做往返测试 |
| 服务端 ↔ 浏览器处理结果 | [处理核心](/compute/imageops) | `compute-imageops` | `web/app/lib/vips-ops.ts` 由 `task test:consistency` 比对产物 |

## 通用规则

- JSON 字段用 `snake_case`（上传端点是已知例外，见 HTTP API）。
- 主键为 UUID v7，JSON 中是字符串；时间为 RFC 3339。
- 状态值大写（`PENDING`、`COMPLETED`、`PARTIALLY_FAILED`…）；格式、色彩等参数枚举小写（`avif`、`srgb`）。
- 大文件不进 JSON：一律是存储 key 或预签名 URL。

## 兼容规则

目前没有线上部署，契约可以直接修改，但修改必须在同一个提交里覆盖所有使用方与 fixtures。有了外部调用方之后：

- 只允许新增可选字段；
- 改名、删除字段、改变含义或枚举值需要新版本（HTTP 路径版本、队列 `v`、`ImageOptions` 产物键前缀中的版本）；
- `ImageOptions` 的默认值或规范化规则改变时，必须提升产物键前缀的版本号，否则旧产物会被错误复用。
