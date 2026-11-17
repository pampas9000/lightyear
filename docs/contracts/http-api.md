# HTTP API 契约

权威实现：`server/internal/api`。路由分两组：

- `/api`：Web 控制台使用的内部接口，**没有版本号**，会话认证。本页前半部分。
- `/v1`：对外 API，API Key 认证，独立的路由、表示与错误码，见 [§对外 API /v1](#对外-api-v1)。它与 `/api` 共用任务与上传的业务逻辑（`services/task`、`services/upload`），但不暴露 `/api` 的形状。

## 认证

Fiber session，存储在 Redis。两种等价凭据：

- Cookie `session_id`（HttpOnly、SameSite=Lax、生产环境 Secure、空闲 7 天过期）
- `Authorization: Bearer <session_id>`（登录/注册返回的 `token` 即 session ID）

未认证返回 `401 error.auth.unauthorized`。

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| `POST` | `/api/register` | `{username, email, password}`；成功即登录 |
| `POST` | `/api/login` | `{username, password}` |
| `POST` | `/api/logout` | 销毁会话 |
| `GET` | `/api/auth/:provider` | OAuth 跳转（`github` / `google`，配置后启用） |
| `GET` | `/api/auth/:provider/callback` | OAuth 回调（也接受 `/api/auth/callback/:provider`），成功后重定向到 `FRONTEND_URL` |
| `GET` | `/api/me` | 当前用户 |

`/api` 只认会话：API Key 放进 `Authorization` 会按 session ID 查找而失败（`401`），因此 API Key 不能管理 API Key。

### API Key 管理

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| `GET` | `/api/api-keys` | 当前用户的全部 key（含已吊销），新的在前；另返回可选 `scopes` |
| `POST` | `/api/api-keys` | `{name, scopes?}` → `201 {api_key, key}`。`key` 是明文，**只在这一次返回** |
| `DELETE` | `/api/api-keys/:id` | 吊销（软删除，写 `revoked_at`），立即生效；重复吊销无副作用 |

```json
{ "id": "0198…", "name": "ci", "prefix": "ly_Xk3f9aQ2", "scopes": ["tasks:read", "tasks:write"],
  "last_used_at": null, "revoked_at": null, "created_at": "…", "updated_at": "…" }
```

- `name`：去掉首尾空白后 1–64 字符；`scopes` 省略或为空数组时授予全部 scope，否则只能取 `tasks:read`、`tasks:write`。
- 每个用户最多 20 个未吊销的 key，超出返回 `409 error.api_key.limit_reached`。
- 明文形如 `ly_` + 43 个 base64url 字符（32 字节随机数）；库里只存其 SHA-256（`key_hash`，不出现在任何响应里）和前 11 个字符 `prefix`（用于辨认）。

## 响应封装

```json
{ "success": true, "code": "ok", "message": "Task fetched.", "data": { }, "details": null }
```

失败时 `success=false`、`data=null`，`details` 为机器可读上下文（如 `{"field":"items","reason":"empty"}`）。响应码定义在 `server/internal/api/response/codes.go`：

| 类别 | 码 |
| :--- | :--- |
| 成功 | `ok`、`ok.user.registered`、`ok.user.logged_in`、`ok.job.fetched` |
| 请求与校验 | `error.request.invalid`、`error.body.invalid`、`error.param.invalid`、`error.param.required`、`error.param.malformed` |
| 认证 | `error.auth.unauthorized`、`error.auth.forbidden`、`error.auth.invalid_credentials`、`error.auth.insufficient_scope`（仅 `/v1`） |
| 资源 | `error.job.not_found`、`error.task.not_found`、`error.file.not_found`、`error.api_key.not_found`、`error.user.already_exists` |
| 冲突 | `error.api_key.limit_reached`、`error.idempotency.conflict`（仅 `/v1`）、`error.task.conflict`（仅 `/v1`） |
| 基础设施 | `error.database.unavailable`、`error.queue.unavailable`、`error.internal` |

## 任务

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| `POST` | `/api/tasks` | 创建任务，返回 `201` 与任务 |
| `GET` | `/api/tasks` | 分页列表：`?page=1&count=20&status=FAILED` |
| `GET` | `/api/tasks/stats` | 各状态任务数、存储用量、活跃 worker 数、近 7 天趋势 |
| `GET` | `/api/tasks/:id` | 详情，含 jobs 及其 `source_file`、`output_file` |
| `POST` | `/api/tasks/:id/retry` | 重试 |
| `POST` | `/api/tasks/:id/cancel` | 取消未结束的 job |

### 创建

单输出或兼容预设时可使用 `options`（可选附带 `target`）或 `workflow_id`；需要一次提交生成多份输出或为输出命名时使用 `outputs`：

```json
{
  "items": [{ "file_id": "0198…" }],
  "outputs": [
    { "name": "preview", "options": { "format": "avif", "resize": { "width": 800 } }, "target": { "max_bytes": 102400 } },
    { "name": "fallback", "options": { "format": "webp", "quality": 80 } }
  ],
  "idempotency_key": "upload-batch-42"
}
```

- `outputs`、`options`（可配可选 `target`）、`workflow_id` **三选一**（给出 `target` 时必须同时给出 `options`，不可与 `outputs` 或 `workflow_id` 混用）。
- `outputs` 为 1–10 项数组，每项含必填 `options`、可选 `name`（正则 `[A-Za-z0-9_-]{1,64}`，同任务内不可重复，且不可与未命名项自动分配的 `1..N` 序号碰撞）以及可选 `target`（第 1 期 `{ "max_bytes": <正整数> }`）。
- 每条 `(item, output)` 生成一个 job；`items` 最多 100 个，总 job 数 `len(items) * len(outputs)` 最多 200。文件必须属于当前用户且为 `UPLOADED`，提交后对应草稿被删除。
- `format: "png"` 不支持 `target`。对非 PNG 格式：当 `target` 非空且省略 `quality`/`distance` 且 `lossless: false` 时，worker 在该格式内部二分搜索参数并输出 `resolved_options`（产物复用键前缀 `compression-target/v1`）；若显式指定了 `quality`/`distance` 或 `lossless: true`，则仅做确定性单次编码并检查 `target_met`（产物复用键沿用 `image-options/v2`）。
- 产物文件名：单输出为 `<stem>.<ext>`，多输出为 `<stem>.<name|index+1>.<ext>`（`jpeg` 使用 `jpg`）。
- 已有相同产物（同一源文件、同一规范化参数，搜索任务还含 `target`）的 job 直接完成，并按新 job 的 `target` 重算 `target_met` 与 `achieved`。
- `idempotency_key`：同一用户同一 key 的重复请求返回第一次创建的任务；请求内容（`workflow_id`、规范化输出集合、文件集合）不同则 `409`。

| 情况 | 响应 |
| :--- | :--- |
| `items` 为空 | `400 error.param.required` |
| 超过 100 个 item、超过 10 个 output、或总 job 数超过 200 | `400 error.param.invalid` |
| `outputs` / `options` / `workflow_id` 缺失或冲突；`name` 非法/重名/序号碰撞；`options` 或 `target` 非法（含 `png` 带 `target`）；`idempotency_key` 过长 | `400 error.param.invalid` |
| 文件不存在或未上传完成 | `400 error.param.invalid` |
| `workflow_id` 不存在 | `400 error.param.invalid` |
| 同 key 不同内容 | `409 error.param.invalid` |

### 重试

```json
{ "job_ids": ["0198…"], "retry_all": false, "options": { "format": "webp" }, "target": { "max_bytes": 102400 } }
```

- 都不给：重试所有 `FAILED` 的 job。
- `job_ids`：重试指定的 job；`retry_all: true`：重试所有已结束的 job（完成、失败、取消）。
- `options`、`target` 可选：给出 `options` 时替换参数；`target` 传对象时设置/替换目标，显式传 `null` 时清除目标。替换参数或更改/清除目标时，必须只指定单个 `job_ids`（不允许对批量任务统一替换参数）；保留原参数时不限制单选。
- 若原 job 无 `target` 且非 `lossless`，重试时仅传 `target` 而省略 `options`，会清除原 `options` 中的 `quality`/`distance` 并触发目标搜索；若希望保留原固定 `quality`/`distance` 仅做目标校验，需同时显式传入 `options`。
- 替换参数后也会清除上一次运行的 `resolved_options`、`target_met`、`achieved`。
- 有未结束的 job 被点名时返回 `409`；没有可重试的 job 返回 `400`。

### Job 字段

```json
{
  "id": "…", "task_id": "…", "operation": "image.convert",
  "output_index": 0, "output_name": "preview",
  "options": { "format": "avif", "lossless": false, "resize": { "width": 800, "fit": "inside", "enlarge": false }, "color": "keep", "dynamic_range": "keep", "metadata": "strip" },
  "target": { "max_bytes": 102400 },
  "resolved_options": { "format": "avif", "quality": 72, "lossless": false, "resize": { "width": 800, "fit": "inside", "enlarge": false }, "color": "keep", "dynamic_range": "keep", "metadata": "strip", "chroma": "420" },
  "target_met": true,
  "achieved": { "bytes": 98400 },
  "compression": { "source_bytes": 245760, "output_bytes": 98400, "ratio": 0.400390625, "saved_ratio": 0.599609375, "larger_than_source": false },
  "source_file_id": "…", "source_file": { "name": "a.jpg", "size": 245760, "width": 4000, "height": 3000 },
  "output_file_id": "…", "output_file": { "name": "a.preview.avif", "size": 98400, "mime_type": "image/avif" },
  "status": "COMPLETED", "progress": 100, "attempts": 1, "max_attempts": 3,
  "error_code": "", "error_message": ""
}
```

- 无搜索的普通任务上，`options` 为规范化后的完整形式；有目标搜索时 `options` 省略被搜索的 `quality`/`distance`，任务完成后 `resolved_options` 总是给出实际采用的参数（未搜索时回退为 `options`）。
- `compression` 仅在 job 已完成且同时关联到 `source_file` 与 `output_file`（二者在 `GET /api/tasks`、`GET /api/tasks/:id`、`GET /api/jobs/:id` 上均已预加载）且双方字节数均大于 0 时返回；`larger_than_source = output_bytes > source_bytes` 无条件计算。
- `error_code` 取值见[队列契约 §错误码](/contracts/queue#错误码)，另有 `execution_timeout` 与 `cancelled`。

## 预设（Workflows）

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| `POST` | `/api/workflows` | `{name, outputs}`（也兼容单输出简写 `{name, options, target?}`） |
| `GET` | `/api/workflows` | 当前用户的预设 |
| `GET` | `/api/workflows/:id` | 详情 |
| `PUT` | `/api/workflows/:id` | `{name, outputs}`（或单输出简写） |
| `DELETE` | `/api/workflows/:id` | 删除 |

`outputs` 内的 `name`、`options` 与 `target` 规则同任务创建，保存时统一规范化为 `outputs` 数组。

## 上传（S3 预签名直传）

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| `POST` | `/api/upload/init` | `{filename, type, size, hash}`，见下文 |
| `POST` | `/api/upload/complete` | `{key, parts?}`；分片上传须带 `parts:[{PartNumber, ETag}]`。文件变为 `UPLOADED`，返回 `{fileId, key}`；对已 `UPLOADED` 的文件重复调用直接返回 |
| `GET` | `/api/upload/multipart/:uploadId/list-parts?key=` | 已上传分片 |
| `GET` | `/api/upload/multipart/:uploadId/sign-part/:partNumber?key=` | 重新签一个分片（init 返回的 URL 一小时后过期） |
| `POST` | `/api/upload/multipart/:uploadId/abort` | `{key}`；中止，文件变为 `ABORTED` |

`key` 不以 `uploads/<user_id>/` 开头，或 `uploadId` 与该文件不符时返回 `403`；文件记录不存在返回 `404 error.file.not_found`。

`init` 的 `hash` 是十六进制 SHA-256，可省略。返回值有三种：

- 命中本人已上传的同 `hash` 文件：`{fileId, key, fastUpload: true}`，无需上传。
- `size` 不超过 10 MB：`{fileId, key, url}`。向 `url` 单次 `PUT` 文件内容；`hash` 已签进 URL，内容不符时存储拒绝写入。
- 更大的文件：`{fileId, key, uploadId, partUrls}`。分片大小为 `max(5 MiB, ceil(size / 10000))`，`partUrls[i]` 对应第 `i + 1` 片。

## 文件、草稿与 Job

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| `GET` | `/api/files/:id/download-url` | 15 分钟预签名下载 URL（仅限本人文件；签名包含基于 `file.Name` 的 `ResponseContentDisposition` attachment 头） |
| `GET` | `/api/drafts/list` | 草稿（游标分页 `?cursor=`） |
| `POST` | `/api/drafts/:id/delete` | 删除草稿及其存储对象 |
| `GET` | `/api/jobs/:id` | 单个 job |

## 对外 API /v1

面向调用方的分步教程见[对外 API 接入指南](/guides/api-quickstart)；本节是逐字段的契约。

只做异步：提交返回 `202`，客户端轮询任务（webhook 尚未实现）。所有响应沿用上文的[响应封装](#响应封装)，字段一律 `snake_case`。`/v1` 直接由 API 服务（默认 `:8080`）提供，不经 Web 的 Nitro 代理。

### 认证与 scope

```http
Authorization: Bearer ly_…
```

Key 在 Web 控制台的「API Keys」页面创建（[§API Key 管理](#api-key-管理)）。`/v1` 不接受会话。

| scope | 允许 |
| :--- | :--- |
| `tasks:read` | `GET /v1/tasks`、`GET /v1/tasks/:id` |
| `tasks:write` | 上传（`/v1/uploads/**`）、`POST /v1/tasks`、取消、重试 |

两个 scope 互不包含；写操作的响应里也会带回任务本身。

| 情况 | 响应 |
| :--- | :--- |
| 缺少 `Authorization`、格式不对、key 不存在或已吊销 | `401 error.auth.unauthorized`，带 `WWW-Authenticate: Bearer realm="lightyear"` |
| key 缺少路由要求的 scope | `403 error.auth.insufficient_scope`，`details: {"required_scope": "tasks:write"}` |

`last_used_at` 在认证成功时更新，同一 key 每分钟最多写一次。

### 请求体

`/v1` 严格解析 JSON：未知字段（包括 `/api` 请求体里的 `idempotency_key`）、多余数据、缺失必需的请求体都返回 `400 error.body.invalid`，`details.reason` 给出原因。

### 端点

| 方法 | 路径 | scope | 成功 |
| :--- | :--- | :--- | :--- |
| `POST` | `/v1/uploads` | write | `201`（需上传）或 `200`（秒传）+ `Upload` |
| `POST` | `/v1/uploads/:id/complete` | write | `200` + `File` |
| `GET` | `/v1/uploads/:id/parts` | write | `200 {parts: [{part_number, etag, size}]}` |
| `POST` | `/v1/uploads/:id/parts/:part_number/url` | write | `200 {part_number, url, expires_at}` |
| `POST` | `/v1/uploads/:id/abort` | write | `200` + `File`（`ABORTED`） |
| `POST` | `/v1/tasks` | write | `202` + `Task`，`Location`、`Retry-After` |
| `GET` | `/v1/tasks` | read | `200 {tasks, total, page, limit}` |
| `GET` | `/v1/tasks/:id` | read | `200` + `Task`（含下载链接），`ETag`；或 `304` |
| `POST` | `/v1/tasks/:id/cancel` | write | `200` + `Task` |
| `POST` | `/v1/tasks/:id/retry` | write | `202` + `Task`，`Location`、`Retry-After` |

`:id` 不是 UUID 时返回 `400 error.param.invalid`；不属于当前 key 所有者的任务与上传一律按不存在处理（`404`）。

### 上传

与 `/api/upload` 是同一套 S3 预签名直传逻辑，只是改用文件 ID 寻址并统一封装。

```json
// POST /v1/uploads
{ "filename": "a.jpg", "content_type": "image/jpeg", "size": 245760, "sha256": "9f86d0…" }
```

- `filename`、`size`（正整数）必填；`content_type` 省略时为 `application/octet-stream`；`sha256` 为可选的十六进制 SHA-256。
- 响应 `Upload`：

```json
{ "file": { "id": "0198…", "name": "a.jpg", "status": "UPLOADING", "size": 0, "mime_type": "image/jpeg", "sha256": "9f86d0…", "created_at": "…" },
  "method": "put", "url": "https://…", "expires_at": "…" }
```

| `method` | 客户端要做的 |
| :--- | :--- |
| `none` | 本人已有同 `sha256` 的已上传文件，`file` 即该文件，直接用它的 `id` 提交任务（`200`） |
| `put` | `size` ≤ 10 MB：把文件内容 `PUT` 到 `url`（无需额外请求头；`sha256` 已签进 URL，内容不符时存储拒绝），再调用 `complete` |
| `multipart` | 更大的文件：按 `part_size` 切片，第 `i + 1` 片 `PUT` 到 `part_urls[i]`，记下每片响应的 `ETag` 头，再以 `{"parts": [{"part_number": 1, "etag": "\"…\""}, …]}` 调用 `complete` |

- 上传 URL 在 `expires_at`（1 小时）后失效；分片可用 `POST /v1/uploads/:id/parts/:n/url` 重新签名，用 `GET …/parts` 查询已上传的分片。
- `complete` 后文件为 `UPLOADED`，`size` 与 `mime_type` 以存储实际为准。对已上传的文件重复调用直接返回；分片上传未带 `parts` 返回 `400 error.param.required`。
- 对单次 PUT 的上传调用分片相关端点、对已完成的上传中止，返回 `409 error.param.invalid`。
- 未完成的上传 24 小时后由清理任务回收。

### 创建任务

```http
POST /v1/tasks
Authorization: Bearer ly_…
Idempotency-Key: batch-2026-11-16-001
Content-Type: application/json

{ "items": [{ "file_id": "0198…" }], "options": { "format": "avif" }, "target": { "max_bytes": 102400 } }
```

请求体与 [`POST /api/tasks`](#创建) 相同（`items`；`options`(+`target`)、`outputs`、`workflow_id` 三选一；限额与校验规则一致），只是**没有** `idempotency_key` 字段。

```http
HTTP/1.1 202 Accepted
Location: /v1/tasks/0198…
Retry-After: 2
```

响应体是 `Task`（见下）。已有相同产物的 job 会直接是 `COMPLETED`，此时仍返回 `202`。错误与 `/api` 相同，唯一的区别是幂等冲突的码（见下）。

### 幂等：`Idempotency-Key`

- 请求头 `Idempotency-Key`（去掉首尾空白后最多 255 字节，否则 `400 error.param.invalid`）。不带则每次请求都创建新任务。
- 作用域是 key 的所有者（用户），而不是单个 API Key；与 Web 控制台请求体里的 `idempotency_key` 是同一个命名空间、同一套逻辑。
- 同一 key、相同内容的重复请求返回第一次创建的任务：`202`、相同的 `Location`，并带 `Idempotent-Replayed: true`。“相同内容”指 `workflow_id`、规范化后的输出集合与文件集合相同（`items` 顺序、`options` 的写法差异不影响）。
- 同一 key、内容不同：`409 error.idempotency.conflict`。
- key 永久有效（不过期），随任务一起存在。

### 查询任务与 ETag

`GET /v1/tasks/:id` 返回：

- `ETag: W/"…"`（弱校验器）与 `Cache-Control: private, no-cache`。带 `If-None-Match`（支持多个值与 `*`，按弱比较）且匹配时返回 `304`，无响应体。
- 任务未结束（`PENDING`、`PROCESSING`）时带 `Retry-After: 2`，作为建议的轮询间隔。

ETag 由任务的完整状态（各 job 的状态、进度、参数、结果、文件）计算，任一变化都会换新值。下载链接每次请求都会重新签名，所以不参与计算；但任务含可下载产物时，ETag 额外包含当前 30 分钟时间片：拿到 `304` 时，客户端缓存中的下载链接仍至少有 30 分钟有效期。

`GET /v1/tasks?page=1&limit=20&status=FAILED`：`page` ≥ 1，`limit` 1–100（默认 20），`status` 为任务状态（大小写不敏感），越界返回 `400 error.param.invalid`。按创建时间倒序；列表不带 ETag，也不带下载链接（需要时逐个 `GET`）。

### Task 与 Job

```json
{
  "id": "0198…", "status": "COMPLETED", "idempotency_key": "batch-2026-11-16-001",
  "created_at": "…", "updated_at": "…",
  "jobs": [{
    "id": "0198…", "status": "COMPLETED", "progress": 100,
    "output_index": 0, "output_name": "preview",
    "options": { "format": "avif", "lossless": false, "color": "keep", "dynamic_range": "keep", "metadata": "strip" },
    "target": { "max_bytes": 102400 },
    "resolved_options": { "format": "avif", "quality": 72, "lossless": false, "color": "keep", "dynamic_range": "keep", "metadata": "strip" },
    "target_met": true, "achieved": { "bytes": 98400 },
    "compression": { "source_bytes": 245760, "output_bytes": 98400, "ratio": 0.400390625, "saved_ratio": 0.599609375, "larger_than_source": false },
    "attempts": 1, "max_attempts": 3,
    "source_file": { "id": "…", "name": "a.jpg", "status": "UPLOADED", "size": 245760, "mime_type": "image/jpeg", "width": 4000, "height": 3000, "created_at": "…" },
    "output_file": {
      "id": "…", "name": "a.avif", "status": "UPLOADED", "size": 98400, "mime_type": "image/avif", "created_at": "…",
      "download_url": "https://…&response-content-disposition=attachment%3B%20filename%3Da.avif&…",
      "download_expires_at": "2026-11-16T11:00:00Z"
    },
    "created_at": "…", "updated_at": "…"
  }]
}
```

- `options`、`target`、`resolved_options`、`target_met`、`achieved`、`compression`、`error_code`、`error_message` 的含义与 [`/api` 的 Job 字段](#job-字段)相同；完成的 job 总有 `resolved_options`。
- 与 `/api` 的区别：不返回 `owner_id`、`operation`、`artifact_key`、`next_run_at`、租约与当前 attempt 等内部字段；`source_file`/`output_file` 只含上面列出的字段。
- **下载链接**：`GET /v1/tasks/:id` 中，状态为 `COMPLETED` 且产物已上传的 job，其 `output_file` 带 `download_url` 与 `download_expires_at`（签名时刻 + 1 小时）。链接是存储的预签名 GET，签入了以产物文件名为附件名的 `response-content-disposition`。过期后重新 `GET` 任务即可拿到新链接。创建、取消、重试与列表的响应不带下载链接。

### 取消与重试

- `POST /v1/tasks/:id/cancel`：无请求体，取消所有未结束的 job，返回 `200` 与任务。
- `POST /v1/tasks/:id/retry`：请求体可省略，字段与 [`/api` 的重试](#重试)相同（`job_ids`、`retry_all`、`options`、`target`）。成功返回 `202`、`Location`、`Retry-After`。点名了未结束的 job：`409 error.task.conflict`；没有可重试的 job 或参数不合法：`400 error.param.invalid`。

### 错误码

| 状态 | 码 | 场景 |
| :--- | :--- | :--- |
| `400` | `error.body.invalid` | 请求体不是合法 JSON、含未知字段或缺失 |
| `400` | `error.param.invalid` / `error.param.required` | 参数校验失败（规则同 `/api`），`details` 指出字段 |
| `401` | `error.auth.unauthorized` | 无 key、key 无效或已吊销 |
| `403` | `error.auth.insufficient_scope` | key 缺少 scope |
| `404` | `error.task.not_found` / `error.file.not_found` | 任务 / 上传不存在或不属于该用户 |
| `409` | `error.idempotency.conflict` | `Idempotency-Key` 已用于不同内容的请求 |
| `409` | `error.task.conflict` | 重试点名了未结束的 job |
| `409` | `error.param.invalid` | 上传状态不允许该操作（见上传） |
| `500` | `error.internal` | 其他 |

## 已知不一致

- `/api/upload/*` 直接返回 camelCase 的裸对象（`uploadId`、`fileId`、`fastUpload`），没有使用统一封装（Uppy 的约定）；`/v1/uploads` 没有这个问题。
- `JWT_SECRET` 配置与 JWT 中间件仍在代码中，但没有使用（实际是 session 认证）。
