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

```json
{
  "items": [{ "file_id": "0198…" }, { "file_id": "0198…" }],
  "options": { "format": "avif", "quality": 60, "resize": { "width": 1600 } },
  "idempotency_key": "upload-batch-42"
}
```

- `options` 与 `workflow_id` **二选一**；`options` 按 [ImageOptions](/contracts/image-options) 严格解析并规范化后存入每个 job。
- 每个 item 生成一个 job；文件必须属于当前用户且为 `UPLOADED`，提交后对应草稿被删除。
- 已有相同产物（同一源文件、同一规范化参数）的 job 直接完成。
- `idempotency_key`：同一用户同一 key 的重复请求返回第一次创建的任务；请求内容（`workflow_id`、`options`、文件集合）不同则 `409`。

| 情况 | 响应 |
| :--- | :--- |
| `items` 为空 | `400 error.param.required` |
| 超过 100 个 item | `400 error.param.invalid` |
| `options` 与 `workflow_id` 都给或都没给；`options` 非法；`idempotency_key` 过长 | `400 error.param.invalid` |
| 文件不存在或未上传完成 | `400 error.param.invalid` |
| `workflow_id` 不存在 | `400 error.param.invalid` |
| 同 key 不同内容 | `409 error.param.invalid` |

### 重试

```json
{ "job_ids": ["0198…"], "retry_all": false, "options": { "format": "webp" } }
```

- 都不给：重试所有 `FAILED` 的 job。
- `job_ids`：重试指定的 job；`retry_all: true`：重试所有已结束的 job（完成、失败、取消）。
- `options` 可选，给出时替换这些 job 的参数。
- 有未结束的 job 被点名时返回 `409`；没有可重试的 job 返回 `400`。

### Job 字段

```json
{
  "id": "…", "task_id": "…", "operation": "image.convert",
  "options": { "format": "avif", "quality": 60, "lossless": false, "effort": 5, "color": "keep", "dynamic_range": "keep", "metadata": "strip" },
  "source_file_id": "…", "source_file": { "name": "a.jpg", "size": 123, "width": 4000, "height": 3000 },
  "output_file_id": "…", "output_file": { "name": "a.avif", "size": 45, "mime_type": "image/avif" },
  "status": "COMPLETED", "progress": 100, "attempts": 1, "max_attempts": 3,
  "error_code": "", "error_message": ""
}
```

`options` 总是规范化后的完整形式。`error_code` 取值见[队列契约 §错误码](/contracts/queue#错误码)，另有 `execution_timeout` 与 `cancelled`。

## 预设（Workflows）

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| `POST` | `/api/workflows` | `{name, options}` |
| `GET` | `/api/workflows` | 当前用户的预设 |
| `GET` | `/api/workflows/:id` | 详情 |
| `PUT` | `/api/workflows/:id` | `{name, options}` |
| `DELETE` | `/api/workflows/:id` | 删除 |

`options` 与任务中的规则相同。

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
| `GET` | `/api/files/:id/download-url` | 15 分钟预签名下载 URL（仅限本人文件） |
| `GET` | `/api/drafts/list` | 草稿（游标分页 `?cursor=`） |
| `POST` | `/api/drafts/:id/delete` | 删除草稿及其存储对象 |
| `GET` | `/api/jobs/:id` | 单个 job |

## 已知不一致

- 上传端点直接返回 camelCase 的裸对象（`uploadId`、`fileId`、`fastUpload`），没有使用统一封装（Uppy 的约定）；key 必须以 `uploads/<user_id>/` 开头，否则 `403`。
- `JWT_SECRET` 配置与 JWT 中间件仍在代码中，但没有使用（实际是 session 认证）。
