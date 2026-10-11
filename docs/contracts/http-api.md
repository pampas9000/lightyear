# HTTP API 契约

权威实现：`server/internal/api`（路由、handlers、middleware、响应封装）。所有路由挂在 `/api` 前缀下，**没有版本号**。

## 认证

基于 Fiber session（存储于 Redis），支持两种等价凭据：

- Cookie：`session_id`（HttpOnly、SameSite=Lax、生产环境 Secure、空闲 7 天过期）
- `Authorization: Bearer <session_id>`（登录/注册返回的 `token` 即 session ID）

未认证请求返回 `401` 与 `error.auth.unauthorized`。

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| `POST` | `/api/register` | `{username, email, password}`；成功建会话并返回 `token` 与用户信息 |
| `POST` | `/api/login` | `{username, password}` |
| `POST` | `/api/logout` | 销毁会话 |
| `GET` | `/api/auth/:provider` | OAuth 登录跳转（`github`/`google`） |
| `GET` | `/api/auth/:provider/callback` | OAuth 回调（兼容 `/api/auth/callback/:provider`），成功后重定向 `FRONTEND_URL` |
| `GET` | `/api/me` | 当前用户 |

## 响应封装

除少数历史端点外，统一包裹为：

```json
{
  "success": true,
  "code": "ok",
  "message": "Task fetched.",
  "data": { },
  "details": null
}
```

错误时 `success=false`、`data=null`，`details` 承载机器可读上下文（如 `{"field":"items","reason":"empty"}`）。

### 响应码

成功：`ok`、`ok.job.created`、`ok.job.fetched`、`ok.job.created_partial`、`ok.user.registered`、`ok.user.logged_in`。

失败：

| 类别 | 码 |
| :--- | :--- |
| 请求/校验 | `error.request.invalid`、`error.body.invalid`、`error.param.invalid`、`error.param.required`、`error.param.malformed` |
| 认证 | `error.auth.unauthorized`、`error.auth.forbidden`、`error.auth.invalid_credentials` |
| 资源 | `error.job.not_found`、`error.user.already_exists`、`error.queue.enqueue_failed` |
| 基础设施 | `error.database.unavailable`、`error.queue.unavailable`、`error.internal` |

完整定义：`server/internal/api/response/codes.go`。

## 任务（Tasks）

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| `POST` | `/api/tasks` | 创建任务 |
| `GET` | `/api/tasks` | 分页列表：`?page=1&count=20&status=FAILED` |
| `GET` | `/api/tasks/stats` | 统计（任务数、活跃 Worker 等） |
| `GET` | `/api/tasks/:id` | 任务详情（含 jobs 与文件） |
| `POST` | `/api/tasks/:id/retry` | 重试失败作业，可覆盖参数 |
| `POST` | `/api/tasks/:id/cancel` | 取消任务 |

### 创建任务

新接口支持通用 `jobs`；`items` 是转码兼容写法。一个 job 的 `operation` 可以是 `transcode`、`image.generate`、`image.upscale` 或 `image.edit`。

```json
{
  "workflow_id": "0198...",          // 可选，复用预设参数
  "items": [{ "file_id": "0198...", "target_format": "avif", "params": { } }],
  "target_format": "avif",           // 可选，作为 items 的默认值
  "params": {                        // 可选，作为 items 的默认参数
    "engine": "libavif:avif",
    "engine_params": { "quality": 80 },
    "color": { "color_space": "display_p3", "chroma": "auto", "nominal_bit_depth": 10 }
  },
  "idempotency_key": "..."           // 可选；同 key 不同负载返回 409
}
```

通用写法示例：

```json
{
  "jobs": [{
    "operation": "image.edit",
    "executor": "provider:example",
    "operation_params": {
      "model": "example-edit-v1",
      "prompt": "replace the background"
    },
    "inputs": [
      {"file_id": "0198...", "role": "source"},
      {"file_id": "0198...", "role": "mask"}
    ],
    "target_format": "png",
    "output_count": 1
  }]
}
```

`executor` 必须是服务端已安装并启用的执行器。当前默认只安装本地 `compute` 转码执行器，因此未配置 AI Provider 时，AI job 返回 `422 error.param.invalid`（details 表示 executor unavailable），不会进入队列。

规则与错误：

- `items` 为空 → `400 error.param.required`
- 输入文件不存在或未完成上传 → `400 error.param.invalid`
- `workflow_id` 不存在 → `400 error.param.invalid`
- 未安装的 `executor` → `422 error.param.invalid`（details 标识 executor unavailable）
- 相同 `idempotency_key` 但请求指纹不同 → `409 error.param.invalid`

### 重试任务

```json
{
  "target_format": "webp",
  "params": { "engine": "libwebp:webp", "engine_params": { "quality": 75 } },
  "job_ids": ["0198..."],
  "retry_all": false
}
```

未指定 `job_ids` 且 `retry_all=false` 时返回 `400`（无失败作业可重试）。

## 工作流（Workflows）

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| `POST` | `/api/workflows` | 创建预设 |
| `GET` | `/api/workflows` | 当前用户预设列表 |
| `GET` | `/api/workflows/:id` | 预设详情 |
| `PUT` | `/api/workflows/:id` | 更新 |
| `DELETE` | `/api/workflows/:id` | 删除 |

> ⚠️ 已知不一致：工作流请求体使用 `targetFormat`（camelCase），而任务接口使用 `target_format`（snake_case）。

## 上传（S3 分片预签名）

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| `POST` | `/api/upload/multipart/init` | `{filename, type, hash}`；`hash` 命中已上传文件时返回 `fastUpload: true` 秒传 |
| `GET` | `/api/upload/multipart/:uploadId/list-parts` | 已上传分片 |
| `GET` | `/api/upload/multipart/:uploadId/sign-part/:partNumber` | 分片预签名 URL |
| `POST` | `/api/upload/multipart/:uploadId/complete` | `{key, parts:[{PartNumber, ETag}]}` 合并并置文件为 `UPLOADED` |
| `POST` | `/api/upload/multipart/:uploadId/abort` | 中止分片上传并置文件为 `ABORTED` |

> ⚠️ 已知不一致：该组端点直接返回裸 `fiber.Map`（`uploadId`/`fileId`/`fastUpload` 等 camelCase），未使用统一响应封装。路径必须以前缀 `uploads/<user_id>/` 开头，否则 `403`。

## 文件与草稿

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| `GET` | `/api/files/:id/download-url` | 15 分钟预签名下载 URL（仅限本人文件） |
| `GET` | `/api/drafts/list` | 草稿列表（游标分页 `?cursor=`） |
| `POST` | `/api/drafts/:id/delete` | 删除草稿（含存储对象清理） |

## 作业（Jobs）

仅保留 `GET /api/jobs/:id`。`POST /api/jobs` 已从路由移除（统一由任务接口创建），handler 代码仍存在但不可达。

作业详情返回当前 attempt 的状态与 generalized inputs/outputs；下载引用只在对应 attempt 的 artifact manifest 校验通过后更新。

## 参数校验规则

`transcode.Params` 的校验由 `server/internal/transcode/validation.go` 执行：

- 提供 `color` 时按结构体 tag 校验所有枚举与数值范围（`color_space`、`primaries`、`transfer`、`matrix`、`range`、`nominal_bit_depth`、`chroma`、`tone_mapping`、`dither`）
- `AVIF` 要求 `engine == "libavif:avif"`，并校验 `engine_params`（quality/alpha_quality 0–100、speed 0–10、depth 8–12、yuv 444/420/422/auto 等）
- `JXL` 要求 `engine == "libjxl:jxl"`，`distance` 与 `quality` 互斥
- 其他格式暂不做 `engine_params` 级校验；`profile_policy` 与 `strict` 由下游执行层消费

## 契约演进

新增可选字段属于兼容变更；重命名、改枚举值或改变必填性属于破坏性变更。迁移前请先更新本页与 [契约总览](/contracts/)。
