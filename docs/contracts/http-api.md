# HTTP API 契约

权威实现：`server/internal/api`。所有路由挂在 `/api` 下，**没有版本号**；这是 Web 控制台使用的内部接口。对外的 `/v1` API 与 MCP 计划在[路线图第 9 步](/roadmap#第-9-步-对外-api-与-mcp-只做异步)。

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

## 响应封装

```json
{ "success": true, "code": "ok", "message": "Task fetched.", "data": { }, "details": null }
```

失败时 `success=false`、`data=null`，`details` 为机器可读上下文（如 `{"field":"items","reason":"empty"}`）。响应码定义在 `server/internal/api/response/codes.go`：

| 类别 | 码 |
| :--- | :--- |
| 成功 | `ok`、`ok.user.registered`、`ok.user.logged_in`、`ok.job.fetched` |
| 请求与校验 | `error.request.invalid`、`error.body.invalid`、`error.param.invalid`、`error.param.required`、`error.param.malformed` |
| 认证 | `error.auth.unauthorized`、`error.auth.forbidden`、`error.auth.invalid_credentials` |
| 资源 | `error.job.not_found`、`error.task.not_found`、`error.user.already_exists` |
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
| `POST` | `/api/upload/complete` | `{key, parts?}`；分片上传须带 `parts:[{PartNumber, ETag}]`。文件变为 `UPLOADED`，返回 `{fileId, key}` |
| `GET` | `/api/upload/multipart/:uploadId/list-parts` | 已上传分片 |
| `GET` | `/api/upload/multipart/:uploadId/sign-part/:partNumber` | 重新签一个分片（init 返回的 URL 一小时后过期） |
| `POST` | `/api/upload/multipart/:uploadId/abort` | 中止，文件变为 `ABORTED` |

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

## 已知不一致

- 上传端点直接返回 camelCase 的裸对象（`uploadId`、`fileId`、`fastUpload`），没有使用统一封装（Uppy 的约定）；key 必须以 `uploads/<user_id>/` 开头，否则 `403`。
- `JWT_SECRET` 配置与 JWT 中间件仍在代码中，但没有使用（实际是 session 认证）。
