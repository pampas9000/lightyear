# 队列契约（Redis Streams，v2）

orchestrator 与 worker 之间的消息格式。双侧实现是 `server/internal/task/queue.go` 与 `compute/crates/types/src/protocol.rs`，两边都用 `docs/contracts/fixtures/{dispatch,results}-v2.json` 做往返测试，**修改必须成对提交并同时更新 fixtures**。

Redis 只负责投递；执行所有权、超时与重试都由数据库决定，见[可靠性设计](/architecture/reliability)。

## 流

| 流 | 生产者 | 消费组 | 读取方式 |
| :--- | :--- | :--- | :--- |
| `q:image` | orchestrator | `workers` | `XREADGROUP … NOACK`，收到即确认 |
| `q:results` | worker | `orchestrators` | 处理后 `XACK`；空闲超过 30 秒的未确认消息由其他实例 `XAUTOCLAIM` |

- 每条消息只有一个字段 `payload`，值为下面的 JSON 字符串。
- 写入时近似裁剪：`MAXLEN ~ 100000`。
- 消费组从 `0` 创建，worker 启动前已发出的派发也会被读到；组或流丢失（`NOGROUP`）时双方都会重建。
- 以后的执行类型（如 GPU、外部 provider）各用一个 `q:<class>` 流和自己的 worker 池，结果统一回到 `q:results`。

## Dispatch（orchestrator → worker）

```json
{
  "v": 2,
  "job_id": "0198a000-…-0001",
  "attempt_id": "0198a000-…-0002",
  "op": "image.convert",
  "options": {
    "format": "avif", "quality": 70, "lossless": false, "effort": 5,
    "resize": { "width": 1600, "fit": "inside", "enlarge": false },
    "color": "keep", "dynamic_range": "keep", "metadata": "strip"
  },
  "source_key": "uploads/<owner>/source.png",
  "output_key": "outputs/<owner>/<job>/<attempt>.avif"
}
```

| 字段 | 说明 |
| :--- | :--- |
| `v` | 协议版本，必须为 `2`；worker 丢弃其他版本 |
| `job_id` / `attempt_id` | UUID；结果必须原样带回 |
| `op` | 操作；image worker 只接受 `image.convert` |
| `options` | **规范化后**的 [`ImageOptions`](/contracts/image-options)，所有默认值已填上 |
| `source_key` | 源文件在存储桶中的 key |
| `output_key` | 产物必须写入的 key，按 attempt 唯一 |

同一个 attempt 可能被投递多次（见可靠性设计的"重发同一 attempt"），worker 不需要去重：产物 key 相同，重复执行只是覆盖同一个对象。

## Result（worker → orchestrator）

```json
{ "v": 2, "job_id": "…", "attempt_id": "…", "status": "RUNNING", "progress": 10 }
```

```json
{
  "v": 2, "job_id": "…", "attempt_id": "…", "status": "COMPLETED", "progress": 100,
  "artifact": { "key": "outputs/<owner>/<job>/<attempt>.avif", "size": 1234, "mime": "image/avif", "width": 1600, "height": 900 }
}
```

```json
{
  "v": 2, "job_id": "…", "attempt_id": "…", "status": "FAILED",
  "error": { "code": "unsupported_input", "message": "unsupported input: HDR (PQ/HLG) sources are not supported yet", "retryable": false }
}
```

| `status` | 含义 | 必带字段 |
| :--- | :--- | :--- |
| `RUNNING` | 开始执行与心跳（每 20 秒）；续租约，可更新进度 | — |
| `COMPLETED` | 产物已上传 | `artifact`，其 `key` 必须等于派发的 `output_key` |
| `FAILED` | 执行失败 | `error` |

`progress` 为 0–100 的可选整数，只会前进。

### 错误码

| `code` | 可重试 | 含义 |
| :--- | :--- | :--- |
| `invalid_request` | 否 | 派发内容或参数非法 |
| `unsupported_input` | 否 | 输入无法按要求处理：未知或损坏、过大、PQ/HLG HDR、被拦截的格式 |
| `source_unavailable` | 否 | 源文件不存在或超过下载上限 |
| `processing_failed` | 否 | libvips/cjxl 在受支持的请求上失败 |
| `storage_failed` | 是 | 对象存储读写失败 |
| `internal_error` | 否 | 其他错误 |

orchestrator 自己还会写入两种错误码：`execution_timeout`（租约到期，可重试）与 `cancelled`。

## 取消

`cancel:attempt:<attempt_id>` 存在（24 小时过期）表示该 attempt 应放弃。worker 在开始前与执行中每 3 秒检查一次；发现后不上传、不回报。

## 兼容规则

- 新增字段必须可选（Rust `Option` + `skip_serializing_if`，Go `omitempty`），旧的一方忽略它。
- 改变字段含义、删除字段、改变枚举值都需要提升 `v`，并让两侧同时发布。
- 目前没有线上部署，`v1` 已直接删除；有部署之后再考虑双版本并存。
