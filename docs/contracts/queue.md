# 队列契约（Redis Streams，v2）

orchestrator 与 worker 之间的消息格式。双侧实现是 `server/internal/task/queue.go` 与 `compute/crates/types/src/protocol.rs`，两边都用 `docs/contracts/fixtures/{dispatch,dispatch-target,dispatch-url,results}-v2.json` 做往返测试，**修改必须成对提交并同时更新 fixtures**。

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
| `options` | **规范化后**的 [`ImageOptions`](/contracts/image-options)，所有默认值已填上；当附带 `target` 触发搜索时，`quality`/`distance` 保持省略 |
| `target` | 可选，压缩目标（目前支持 `max_bytes`）；见 `dispatch-target-v2.json` |
| `source_key` | 源文件在存储桶中的 key |
| `source_url` | 可选，源文件尚未抓取（`FETCHING`）时附带；worker 先下载这个 URL 并写到 `source_key`，再照常处理。见 `dispatch-url-v2.json` 与下文 [URL 源](#url-源) |
| `output_key` | 产物必须写入的 key，按 attempt 唯一 |

同一个 attempt 可能被投递多次（见可靠性设计的"重发同一 attempt"），worker 不需要去重：产物 key 相同，重复执行只是覆盖同一个对象。

## Result（worker → orchestrator）

```json
{ "v": 2, "job_id": "…", "attempt_id": "…", "status": "RUNNING", "progress": 10 }
```

```json
{
  "v": 2, "job_id": "…", "attempt_id": "…", "status": "COMPLETED", "progress": 100,
  "artifact": { "key": "outputs/<owner>/<job>/<attempt>.avif", "size": 98400, "mime": "image/avif", "width": 800, "height": 450 },
  "resolved_options": {
    "format": "avif", "quality": 72, "lossless": false,
    "resize": { "width": 800, "fit": "inside", "enlarge": false },
    "color": "keep", "dynamic_range": "keep", "metadata": "strip", "chroma": "420"
  },
  "target_met": true,
  "achieved": { "bytes": 98400 },
  "encode_rounds": 7,
  "compute_ms": 420
}
```

```json
{
  "v": 2, "job_id": "…", "attempt_id": "…", "status": "FAILED",
  "error": { "code": "unsupported_input", "message": "unsupported input: HDR (PQ/HLG) sources are not supported yet", "retryable": false }
}
```

| `status` | 含义 | 必带与可选字段 |
| :--- | :--- | :--- |
| `RUNNING` | 开始执行与心跳（每 20 秒）；续租约（不超过 attempt 的执行上限），可更新进度 | 抓取完成后可带 `source` |
| `COMPLETED` | 产物已上传 | 必带 `artifact`（`key` 须等于派发 `output_key`）；带 `target` 时附带可选 `resolved_options`（含选中的 `quality`/`distance` 与 `chroma`）、`target_met`、`achieved`（`{bytes}`）、`encode_rounds`、`compute_ms` |
| `FAILED` | 执行失败 | `error`；抓取已完成时带 `source` |

`progress` 为 0–100 的可选整数，只会前进。

### URL 源

派发带 `source_url` 时，worker 抓取成功并把对象写到 `source_key` 后，立即发一条带 `source` 的 `RUNNING`，之后的心跳与最终结果（无论 `COMPLETED` 还是 `FAILED`）也都带上它：

```json
{ "v": 2, "job_id": "…", "attempt_id": "…", "status": "RUNNING", "progress": 0,
  "source": { "key": "uploads/<owner>/<file>.jpg", "size": 482113, "mime": "image/jpeg" } }
```

`source.key` 必须等于 `source_key`，`size` 大于 0，`mime` 由文件头识别（识别不出时用响应声明的 `image/*`，否则为 `application/octet-stream`）。orchestrator 收到后把源文件从 `FETCHING` 改为 `UPLOADED` 并填入大小与类型，此后的重试不再带 `source_url`，直接读已存的对象。同一个 URL 文件同一时间只派发一个 job 去抓取，其余 job 稍后再派发。

抓取的安全限制：

- 只允许 `http` / `https`，不使用代理环境变量，不发送凭据与 `Referer`。
- 连接前检查每个地址，域名在 resolver 里解析后检查，IP 字面量直接检查；只放行全局可路由的公网单播地址，以及 `WORKER_FETCH_ALLOW_CIDRS` 里的网段。连接只用检查过的地址，所以 DNS rebinding 无效。
- 重定向最多 3 跳，每跳同样检查。
- 大小上限为 `WORKER_MAX_SOURCE_BYTES`，按实际读到的字节计算（不信任 `Content-Length`）；连接与读取各有超时（`WORKER_FETCH_CONNECT_TIMEOUT_SECS`、`WORKER_FETCH_READ_TIMEOUT_SECS`）；抓取期间照常检查取消。
- 错误消息不包含 URL 本身，预签名 URL 的签名不会写进结果。

### 错误码

| `code` | 可重试 | 含义 |
| :--- | :--- | :--- |
| `invalid_request` | 否 | 派发内容或参数非法 |
| `unsupported_input` | 否 | 输入无法按要求处理：未知或损坏、过大、PQ/HLG HDR、被拦截的格式 |
| `source_unavailable` | 否 | 源文件不存在或超过下载上限 |
| `processing_failed` | 否 | libvips/cjxl 在受支持的请求上失败 |
| `storage_failed` | 是 | 对象存储读写失败 |
| `url_rejected` | 否 | 源 URL 被拒绝：协议不是 http/https、地址不是公网地址（含重定向目标）、重定向超过 3 次、文件超过源文件大小上限 |
| `fetch_failed` | 视情况 | 下载源 URL 失败：超时、连接失败、5xx、408、429 可重试；其他 4xx 与空响应不可重试 |
| `internal_error` | 否 | 其他错误 |

orchestrator 自己还会写入两种错误码：`execution_timeout`（租约到期或超过执行上限，可重试）与 `cancelled`。

## 取消

`cancel:attempt:<attempt_id>` 存在（24 小时过期）表示该 attempt 应放弃。worker 在开始前与执行中每 3 秒检查一次；发现后不上传、不回报。

## 兼容规则

- 新增字段必须可选（Rust `Option` + `skip_serializing_if`，Go `omitempty`），旧的一方忽略它。
- 改变字段含义、删除字段、改变枚举值都需要提升 `v`，并让两侧同时发布。
- 目前没有线上部署，`v1` 已直接删除；有部署之后再考虑双版本并存。
