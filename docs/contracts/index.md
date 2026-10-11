# 契约总览

本文档定义跨组件边界的规范契约与兼容规则。字段级细节见各分页。

## 边界与归属

| 边界 | 文档 | 权威实现 |
| :--- | :--- | :--- |
| `web` → `server` | [HTTP API](/contracts/http-api) | `server/internal/api/handlers`、`internal/transcode/params.go` |
| `server` ↔ `compute-worker` | [Queue](/contracts/queue) | `server/internal/task/queue.go` + `compute/crates/types/src/protocol.rs`（镜像定义） |
| `web` → `compute/bindings/wasm` | [WASM](/contracts/wasm) | `compute/bindings/wasm/src/lib.rs` |
| 媒体领域类型 | 本页 §领域类型 | `compute/crates/types` |

跨语言镜像规则：媒体领域以 `compute-types` 为 Rust 侧权威定义，Go（`internal/transcode`）与 TypeScript（`web/app/lib/types`）手工镜像；线上协议以 `queue.go` 与 `protocol.rs` 双侧镜像为准，改动必须成对提交。

## 序列化与命名规则

- HTTP 路由统一挂在 `/api` 下，**当前没有路径版本号**（旧文档中的 `/api/v1` 从未实现）
- JSON 字段以 `snake_case` 为主；历史端点存在例外（见 HTTP 分页的“已知不一致”）
- 主键为 UUID v7，JSON 中为字符串
- 时间戳为 RFC 3339（Go `time.Time` 默认序列化）
- **枚举大小写分两层**：HTTP/数据库状态值为**大写**（`PENDING`、`PROCESSING`、`COMPLETED`、`FAILED`、`PARTIALLY_FAILED`、`CANCELLED`）；格式与色彩枚举为**小写**（`jpeg`、`srgb`、`aces_filmic`）
- 大文件不内嵌 JSON：走对象存储引用或预签名句柄
- 可选字段只能新增，不得改变既有语义；破坏性变更需要新字段名或新端点

## 领域类型（compute-types）

`Format`（`compute/crates/types/src/format.rs`）：`jxl`、`avif`、`webp`、`jpeg`、`heic`、`heif`、`png`、`mp4`、`mov`、`mkv`、`flv`。

> 注意：`jxl`/`heic`/`mp4` 等已在类型中保留，但并非所有编码路径都可用；能力矩阵见 [WASM 契约](/contracts/wasm) 与[已知限制](/compute/limitations)。

转码参数 `Params`（`compute/crates/types/src/transcode/params.rs`，Go 镜像 `server/internal/transcode/params.go`）：

```jsonc
{
  "engine": "libavif:avif",          // 必填（AVIF/JXL 有校验），如 libavif:avif / libjxl:jxl
  "engine_params": { /* 编码器专属参数 */ },
  "color": { /* ColorEncoding，见下 */ },
  "profile_policy": "preserve_source", // preserve_source | strip
  "strict": false,                    // 缺少宿主 CLI 时是否直接失败
  "strategy": null                    // 预留
}
```

`ColorEncoding` 字段与取值：

| 字段 | 取值 | 说明 |
| :--- | :--- | :--- |
| `color_space` | `srgb` / `display_p3` / `bt2020_pq` / `bt2020_hlg` / `linear` | 成对描述源图基色 + 传递函数，优先于下列独立字段 |
| `primaries` | `srgb` / `display_p3` / `bt2020` / `unspecified` | 兼容旧工作流的回落字段 |
| `transfer` | `srgb` / `linear` / `pq` / `hlg` / `bt709` / `unspecified` | 同上 |
| `matrix` | `identity` / `bt709` / `bt601` / `bt2020_ncl` / `unspecified` | YUV 矩阵 |
| `range` | `full` / `limited` | 量化范围（服务端输出锁定 full） |
| `nominal_bit_depth` | 8–16 | 逻辑位深，决定输出位深 |
| `chroma` | `yuv420` / `yuv422` / `yuv444` / `yuv400` / `auto` | `auto` 触发内容自适应抽样 |
| `tone_mapping` | `none` / `aces_filmic` | HDR→SDR 映射 |
| `dither` | bool | 高位深降 8-bit 时启用蓝噪声抖动 |

## 兼容规则

### `v1` 内允许（向后兼容）

- 新增可选字段（HTTP、队列、WASM 参数）
- 新增响应元数据字段、扩展 `details`
- 新增内部实现路径（不影响对外语义）

### 需要新版本/新字段名（破坏性）

- 重命名/删除字段，把可选变必填，改变字段含义
- 修改枚举字符串值（HTTP 状态、格式串、色彩串）
- 修改 WASM 函数签名、参数顺序、返回值所有权

### `Format` 枚举演进

仓库内新增 `Format` 变体时可同步更新所有消费者；但对外暴露新的格式字符串（HTTP/队列/WASM）属于破坏性变更，需按版本化契约处理。

## 契约现状

| 契约 | 状态 |
| :--- | :--- |
| HTTP API（认证/任务/工作流/上传/下载/草稿） | 已实现 |
| Redis Streams 队列（含 attempt_id、心跳、取消） | 已实现 |
| WASM 导出（6 个函数） | 已实现（`heic`/`jxl` 编码有生态限制） |
| 队列协议 JSON Schema 生成 | 未实现（当前为双侧手写镜像） |
| API 路径版本化 | 未实现（`/api` 无版本） |
