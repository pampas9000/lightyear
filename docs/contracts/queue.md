# 队列契约（Redis Streams）

服务端与 Worker 之间通过 Redis Streams 通信。权威实现是双侧镜像是 `server/internal/task/queue.go` 与 `compute/crates/types/src/protocol.rs`，**改动必须成对提交**。

## 流与消费者组

| 流 | 生产者 | 消费者（组） | 内容 |
| :--- | :--- | :--- | :--- |
| `transcoder:jobs:stream` | PostgreSQL Outbox relay | Go Orchestrator（`transcoder:jobs:group`） | `{ "job_id": "<uuid>" }` |
| `transcoder:compute:stream` | Orchestrator Outbox relay | Installed executor（默认 Rust Worker） | `{ "payload": "<execution payload JSON>" }` |
| `transcoder:results:stream` | Rust Worker | Go Orchestrator（`transcoder:results:group`） | `{ "payload": "<ResultPayload JSON>" }` |

- 流长度近似裁剪：`MAXLEN ~ 100000`
- 消费循环先 `XAUTOCLAIM` 再 `XREADGROUP`：Orchestrator 认领空闲 >30s 的消息，Worker 认领 >60s 的消息
- 消费者名：`hostname + pid + random`，支持动态扩缩容
- 转码 Worker 协议版本：`schema_version = "1.0"`；通用执行器协议版本：`"1.1"`

## ComputePayload（server → worker）

当前 Rust Worker 继续消费兼容的 `ComputePayload`。生图、放大、编辑图使用下面的通用执行负载，由对应 executor 消费：

```json
{
  "schema_version": "1.1",
  "job_id": "0198...",
  "attempt_id": "0198...",
  "generation": 0,
  "operation": "image.upscale",
  "executor": "provider:example",
  "operation_params": {"model": "example-upscale-v1", "scale": 4},
  "inputs": [{"file_id": "0198...", "role": "source", "path": "uploads/source.png"}],
  "outputs": [{"file_id": "0198...", "role": "result", "path": "attempts/0198/result.png"}],
  "target_format": "png"
}
```

```json
{
  "schema_version": "1.0",
  "job_id": "0198...",
  "attempt_id": "0198...",
  "input_path": "s3://bucket/uploads/.../input.jpg",
  "input_format": "jpeg",
  "output_path": "s3://bucket/outputs/.../result.avif",
  "target_format": "avif",
  "params": {
    "engine": "libavif:avif",
    "engine_params": { "quality": 80, "speed": 6 },
    "color": { "chroma": "auto", "nominal_bit_depth": 10 },
    "profile_policy": "preserve_source",
    "strict": false
  },
  "ops": [
    { "type": "resize", "width": 1920, "height": 1080, "filter": "lanczos3" }
  ]
}
```

| 字段 | 类型 | 必填 | 说明 |
| :--- | :--- | :--- | :--- |
| `schema_version` | string | 是 | 当前 `"1.0"` |
| `job_id` | string | 是 | 作业 ID |
| `attempt_id` | string | 是 | 本次尝试的幂等标识（见[可靠性设计](/architecture/reliability)） |
| `input_path` / `output_path` | string | 是 | `s3://bucket/key` |
| `input_format` | string | 否 | `Format` 小写串；Worker 以字节探测为准，此字段仅作回退 |
| `target_format` | string | 是 | `Format` 小写串 |
| `params` | object | 否 | 见[契约总览](/contracts/#领域类型-compute-types) |
| `ops` | array | 否 | 编辑算子，见 [WASM 契约](/contracts/wasm#编辑算子-ops-json) |

## ResultPayload（worker → server）

```json
{
  "schema_version": "1.0",
  "job_id": "0198...",
  "attempt_id": "0198...",
  "status": "COMPLETED",
  "error_message": null,
  "error_code": null,
  "retryable": null,
  "progress": 100,
  "artifacts": [{
    "file_id": "0198...",
    "path": "attempts/0198/result.avif",
    "size": 512345,
    "mime_type": "image/avif",
    "width": 1920,
    "height": 1080
  }],
  "metadata": {
    "output_size_bytes": 512345,
    "width": 1920,
    "height": 1080,
    "mime_type": "image/avif"
  }
}
```

| 字段 | 说明 |
| :--- | :--- |
| `status` | `COMPLETED` / `PROCESSING`（心跳） / `FAILED` |
| `error_code` | 稳定错误码，见下 |
| `retryable` | 失败是否可重试；Orchestrator 据此自动重排 |
| `progress` | 可选进度（当前心跳为 0，完成为 100） |
| `metadata` | 产物元数据；`output_size_bytes`/`width`/`height`/`mime_type` 会被 Orchestrator 写回 `File` |

### 心跳（续租）

Worker 执行期间每 30 秒发送一条 `PROCESSING` 结果，携带相同的 `job_id`/`attempt_id`。它只用于刷新租约，不改变业务状态。

### 错误分类

| `error_code` | 含义 |
| :--- | :--- |
| `invalid_request` | 负载或参数非法，不可重试 |
| `unsupported_format` | 目标格式无可用编码路径 |
| `decode_failed` / `encode_failed` | 编解码失败 |
| `source_unavailable` | 输入下载失败 |
| `storage_failed` | 产物上传失败 |
| `internal_error` | 其他内部错误 |

## 取消与幂等

- 取消标记：新协议使用 `transcoder:cancel:attempt:<attempt_id>`；旧 Worker 仍可读取 `transcoder:cancel:job:<job_id>` / `transcoder:cancel:task:<task_id>`
- attempt output path 必须匹配服务端持久化 manifest；结果中的 file ID/path 不匹配时会被拒绝
- `attempt_id` 不匹配的结果一律丢弃并 `XACK`；重复投递因此天然幂等
- 消息处理完成后 `XACK`；投毒消息也会被 `XACK`，避免阻塞流

## 兼容规则

- 新增字段必须可选（Rust 侧 `Option` + `skip_serializing_if`，Go 侧 `omitempty`）
- 不得改变既有字段语义或大小写；`status` 为大写字符串，格式/色彩枚举为小写
- 修改 `schema_version` 需要双侧同步发布
