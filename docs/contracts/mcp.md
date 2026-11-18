# MCP 契约

API 服务在 `/mcp` 提供一个远程 [MCP](https://modelcontextprotocol.io) 服务器，供 Agent 直接调用。它是 `/v1` 之外的第二个入口，不是另一套业务：认证、scope、上传、任务创建、幂等、限流、配额和派发都走 `/v1` 的同一套服务，结果是 `/v1` 的同一种表示，错误码也相同。接入步骤见[接入指南 §通过 MCP 接入](/guides/api-quickstart#通过-mcp-接入)。

实现：`server/internal/api/mcpserver`，基于官方 Go SDK（`github.com/modelcontextprotocol/go-sdk`）。

## 传输

| 项目 | 约定 |
| :--- | :--- |
| 地址 | `POST /mcp`（与 `/v1` 同一个进程、同一个端口） |
| 传输 | Streamable HTTP，**无状态**：不发 `Mcp-Session-Id`，每个请求独立认证、独立处理 |
| 响应 | 一律 `application/json`，不用 SSE；`GET /mcp`（服务端推送流）返回 `405` |
| 协议版本 | `2024-11-05` 至 `2026-07-28`，按客户端请求协商 |
| 能力 | 只有 tools。没有 resources、prompts、通知与进度消息 |

初始化结果的 `instructions` 用三步说明了调用流程（取得源 → `submit_task` → `get_task`），并说明图片字节不经过工具参数和结果。

## 认证

只支持 API Key，与 `/v1` 是同一种 key（见 [HTTP API §认证与 scope](/contracts/http-api#认证与-scope)）：

```http
Authorization: Bearer ly_…
```

- 每个请求都要带 key，`initialize` 与 `tools/list` 也一样。没带、key 不存在或已吊销时返回 HTTP `401` 与 `WWW-Authenticate: Bearer realm="lightyear"`。
- 不支持 OAuth。没有 `/.well-known/oauth-protected-resource`（`404`），所以收到 `401` 后尝试 OAuth 发现的客户端会以认证失败结束；在客户端配置里直接写上面的请求头。OAuth 2.1 在[路线图 9.12](/roadmap#第-9-步-对外-api-与-mcp-只做异步)。
- scope 在调用工具时检查：缺少时工具返回 `error.auth.insufficient_scope`（见下文[错误](#错误)），而不是 HTTP `403`。`tools/list` 列出全部工具，不按 key 的 scope 过滤。

## 工具

| 工具 | 所需 scope | 对应的 `/v1` | 限流 | 注解 |
| :--- | :--- | :--- | :--- | :--- |
| `create_upload` | `tasks:write` | `POST /v1/uploads` | 计入 | 非只读、非破坏性、封闭世界 |
| `submit_task` | `tasks:write` | `POST /v1/tasks`（含 `complete`，见下） | 计入 | 非只读、非破坏性、开放世界（会下载 URL 源） |
| `get_task` | `tasks:read` | `GET /v1/tasks/:id` | 不计入 | 只读、幂等 |

每个工具都声明 `inputSchema` 与 `outputSchema`。成功的结果同时带：

- `structuredContent`：`/v1` 的表示本身（不带 `/v1` 的 `{success, code, message, data}` 信封），符合 `outputSchema`；
- 一条 `text` 内容：同一个对象的 JSON 文本，给不读 `structuredContent` 的客户端。

`options` 的 Schema 不在 MCP 里另写：输入与输出 Schema 都把 [ImageOptions 的 JSON Schema](/contracts/image-options) 放在根上的 `$defs.ImageOptions`（其余定义一起提到根上），用 `$ref` 引用。为了让把 Schema 转成单一类型方言的客户端（如 Gemini 的函数声明）能用：

- 输入 Schema 里不出现类型数组：`ImageOptions` 中可为 `null` 的字段（`quality`、`effort`、`bit_depth`、`resize.width`、`resize.height`）在输入里只写非空类型，不填即为默认，与 `null` 含义相同；
- 去掉了 schemars 从 Rust 整数类型推出的 `format`（`uint8`、`uint32`），范围由 `minimum` / `maximum` 表达。

输出 Schema 保留可为 `null` 的字段，因为结果里确实会出现 `null`。

### `create_upload`

申请一次上传。参数与 `POST /v1/uploads` 的请求体相同：

| 参数 | 说明 |
| :--- | :--- |
| `filename` | 必填，1–255 字符，决定产物文件名 |
| `size` | 必填，字节数，≥ 1 |
| `content_type` | 可选，默认 `application/octet-stream` |
| `sha256` | 可选，64 位十六进制；提供时存储校验上传内容，并可命中秒传 |

结果是 `/v1` 上传响应的 `data`：`file`、`method`（`none` / `put` / `multipart`）、`url` 或 `part_size` 与 `part_urls`、`expires_at`。字节由调用方自己用 HTTP 客户端发到这些预签名 URL，**不经过 MCP**：

- `put`：把整个文件 `PUT` 到 `url`；
- `multipart`：按 `part_size` 切片，依次 `PUT` 到 `part_urls`（第 n 片用第 n 个 URL），不需要记录 ETag；
- `none`：已有相同 `sha256` 的文件，`file` 就是它，直接提交。

MCP 没有 `complete` 这一步：`submit_task` 遇到仍是 `UPLOADING` 的文件时，从存储确认对象已上传并完成它（分片上传要求第 1 到 n 片连续、总大小等于申报的 `size`），然后才创建任务。对象还不完整时返回 `error.param.invalid`，`details` 为 `{"field": "items", "reason": "upload not finished"}`，文件保持 `UPLOADING`，补传后再提交即可。

### `submit_task`

创建任务。参数与 `POST /v1/tasks` 的请求体相同，只是幂等键是参数而不是请求头：

| 参数 | 说明 |
| :--- | :--- |
| `items` | 必填，1–100 项，每项恰好一个：`{"file_id": "…"}`（`create_upload` 返回的 `file.id`）或 `{"url": "https://…"}`（最长 2048 字符，规则见 [URL 源](/contracts/http-api#url-源)） |
| `options` | 一份输出的 `ImageOptions`，`format` 必填 |
| `target` | 可选，配合 `options`：`{"max_bytes": n}`，规则见[创建任务](/contracts/http-api#创建任务) |
| `outputs` | 1–10 份输出，每份 `{name?, options, target?}`；与 `options` / `target` 二选一 |
| `idempotency_key` | 可选，1–255 字符，语义与 `Idempotency-Key` 请求头相同（见下） |

结果是创建出的任务（与 `GET /v1/tasks/:id` 的 `data` 相同），通常是 `PENDING`。

### `get_task`

| 参数 | 说明 |
| :--- | :--- |
| `task_id` | 必填 |
| `wait_seconds` | 可选，0–30，默认 0。大于 0 时，任务未结束就每秒重新读取一次，直到进入终态（`COMPLETED`、`PARTIALLY_FAILED`、`FAILED`、`CANCELLED`）或等满为止，然后返回当时的状态 |

结果与 `GET /v1/tasks/:id` 的 `data` 相同：已完成的 job 带 `output_file.download_url`（有效 1 小时，见 `download_expires_at`）与 `compression`，不返回图片内容。等满仍未结束不是错误，再调用一次即可。客户端的工具调用超时要大于 `wait_seconds`。

## 与 `/v1` 共用的规则

- **限流**：`create_upload` 与 `submit_task` 各消耗一次 `/v1` 写请求令牌，与 `/v1` 共用同一个按用户的令牌桶（见 [HTTP API §限流](/contracts/http-api#限流)）；`get_task` 不限。Redis 不可用时放行。
- **配额**：同 `/v1`，`create_upload` 按申报大小检查存储，`submit_task` 检查每月处理单位。
- **幂等**：`idempotency_key` 与 `/v1` 的 `Idempotency-Key` 是同一张表、同一个按用户的命名空间，指纹相同。重放返回第一次创建的任务（MCP 结果里没有 `Idempotent-Replayed` 标记）；同一个键用于不同的请求返回 `error.idempotency.conflict`。
- **派发与执行**：任务进入同一个 orchestrator，按用户公平派发与执行上限照常生效。

## 错误

工具执行中的错误以工具结果返回：`isError: true`，一条 `text` 内容，内容是下面的 JSON，**不带** `structuredContent`（所以不受 `outputSchema` 约束）：

```json
{"code": "error.quota.exceeded", "message": "…", "details": {"quota": "monthly_units", "limit": 10000, "used": 9998, "reserved": 2, "requested": 1}}
```

`code`、`message` 与 `details` 与 `/v1` 同一情形下的响应一致（`/v1` 的完整错误码表见 [HTTP API §错误码](/contracts/http-api#错误码)），只是没有 HTTP 状态码：

| `code` | 情形 | `details` |
| :--- | :--- | :--- |
| `error.body.invalid` | 参数不符合 `inputSchema`（包括 `ImageOptions` 的取值范围） | `reason`：校验器给出的原因 |
| `error.param.invalid` | 参数合法但语义不成立：`png` 带 `target`、文件不存在或不属于你、URL 不允许、上传未完成、超过 job 数上限等 | 视情形，如 `field`、`reason` |
| `error.auth.insufficient_scope` | key 缺少工具所需的 scope | `required_scope` |
| `error.quota.exceeded` | 存储或每月处理单位不足 | `quota`、`limit`、`used`、`reserved`、`requested` |
| `error.rate_limit.exceeded` | 写请求令牌用完 | `retry_after`：建议等待的秒数（`/v1` 用 `Retry-After` 头表达） |
| `error.idempotency.conflict` | `idempotency_key` 已用于不同的请求 | — |
| `error.task.not_found` | 任务不存在或不属于这个 key 的账户 | — |
| `error.internal` | 服务端错误；原因只写日志 | `request_id`：与响应头 `X-Request-ID` 相同 |

`message` 沿用 `/v1` 的文本，偶尔会提到 `/v1` 的叫法（例如 `Idempotency-Key`），以 `code` 为准。

协议层面的错误不是工具结果，而是 JSON-RPC 错误或 HTTP 状态码：未知工具、畸形 JSON-RPC、不支持的协议版本，以及认证失败（HTTP `401`）。
