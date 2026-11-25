# HTTP API 契约

权威实现：`server/internal/api`。路由分两组：

- `/api`：Web 控制台使用的内部接口，**没有版本号**，会话认证。本页前半部分。
- `/v1`：对外 API，API Key 认证，独立的路由、表示与错误码，见 [§对外 API /v1](#对外-api-v1)。它与 `/api` 共用任务与上传的业务逻辑（`services/task`、`services/upload`），但不暴露 `/api` 的形状。

## 认证

Fiber session，存储在 Redis。唯一的凭据是 Cookie `session_id`（HttpOnly、SameSite=Lax、生产环境 Secure、空闲 7 天过期）。登录/注册响应只返回 `{user}`，不含 session ID；`/api` 不读 `Authorization` 头。

未认证返回 `401 error.auth.unauthorized`。

`/api` 上的 `POST`、`PUT`、`PATCH`、`DELETE`（包括下表的登录、注册、登出）要求 `Origin` 头与 `FRONTEND_URL` 规范化后的 `scheme://host[:port]`（小写，去掉路径与默认端口）严格相等；不等或缺少时返回 `403 error.auth.origin_rejected`。`GET` 不检查。`/v1` 与 `/mcp` 不做此检查。控制台经 Nitro 代理访问 `/api`，代理原样转发浏览器的 `Origin`，因此本地开发时 `FRONTEND_URL` 要等于控制台地址（`http://localhost:3000`）。

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| `POST` | `/api/register` | `{username, email, password}`；成功即登录 |
| `POST` | `/api/login` | `{username, password}` |
| `POST` | `/api/logout` | 销毁会话 |
| `GET` | `/api/auth/:provider` | OAuth 跳转（`github` / `google`，配置后启用） |
| `GET` | `/api/auth/:provider/callback` | OAuth 回调（也接受 `/api/auth/callback/:provider`），成功后重定向到 `FRONTEND_URL`，失败重定向到 `FRONTEND_URL?auth_error=<code>` |
| `GET` | `/api/me` | 当前用户 |

OAuth 只按 `(provider, provider_account_id)` 找已绑定的用户，从不按邮箱关联已有账号。首次登录时，提供方必须给出已验证的邮箱（GitHub 取 `/user/emails` 中已验证的，优先 primary；Google 要求 `email_verified`），且该邮箱没有被任何用户使用，才会新建用户并绑定。

| `auth_error` | 含义 |
| :--- | :--- |
| `invalid_state` | `state` 与会话中保存的不符或已用过 |
| `email_taken` | 邮箱已属于另一个账号（例如密码注册的）；不合并、不建绑定，请用原来的方式登录 |
| `email_unverified` | 提供方没有给出已验证的邮箱 |
| `oauth_failed` | 不支持的提供方、换取 token 失败等其他错误 |
| `session_failed` | 会话读取或保存失败 |

`/api` 只认会话 cookie：带 API Key 的 `Authorization` 头会被忽略（`401`），因此 API Key 不能管理 API Key。

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

`/api` 与 `/v1` 的所有 JSON 响应都是这个信封，五个字段始终出现：

- 成功时 `success=true`、`code` 为 `ok`；没有数据可返回时 `data` 为 `null`。
- 失败时 `success=false`、`data=null`，`code` 是区分错误的 `error.*` 码。`details` 为机器可读上下文（如 `{"field":"items","reason":"empty"}`），没有时为 `null`。
- `message` 只给人看，措辞可能变化；调用方按 `code` 判断。
- 框架层的错误也走信封：未知路由 `404 error.route.not_found`，路径存在但方法不对 `405 error.route.method_not_allowed`（带 `Allow` 头），请求体超过 4 MB `413 error.body.too_large`，处理中的 panic 与其他未处理错误 `500 error.internal`。
- 不带响应体的只有重定向（OAuth 登录与回调）、`304`（`GET /v1/tasks/:id` 的条件请求）与 `204`。

`500 error.internal` 不带内部错误内容（SQL、存储响应、panic 只写进服务端日志），`details` 只有 `request_id`；每个响应都带 `X-Request-ID` 头，排查时按它查日志。

`/mcp` 不用这个信封：它是 JSON-RPC，工具错误的格式见 [MCP 契约](/contracts/mcp)。

响应里的时间都是 RFC 3339 的 UTC 时间（以 `Z` 结尾）。响应码定义在 `server/internal/api/response/codes.go`：

| 类别 | 码 |
| :--- | :--- |
| 成功 | `ok`（所有成功响应都用它） |
| 请求与校验 | `error.request.invalid`、`error.body.invalid`、`error.body.too_large`、`error.param.invalid`、`error.param.required`、`error.param.malformed` |
| 路由 | `error.route.not_found`、`error.route.method_not_allowed` |
| 认证 | `error.auth.unauthorized`、`error.auth.forbidden`、`error.auth.invalid_credentials`、`error.auth.origin_rejected`（仅 `/api`）、`error.auth.insufficient_scope`（仅 `/v1`） |
| 资源 | `error.job.not_found`、`error.task.not_found`、`error.file.not_found`、`error.workflow.not_found`、`error.api_key.not_found`、`error.user.already_exists`、`error.file.gone`（文件已过期或已删除） |
| 冲突 | `error.api_key.limit_reached`、`error.idempotency.conflict`（仅 `/v1`）、`error.task.conflict`、`error.file.conflict` |
| 限额 | `error.quota.exceeded`（见[用量与配额](#用量与配额)）、`error.rate_limit.exceeded`（仅 `/v1`，见[限流](#限流)） |
| 基础设施 | `error.database.unavailable`、`error.queue.unavailable`、`error.internal` |

## 任务

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| `POST` | `/api/tasks` | 创建任务，返回 `201` 与任务 |
| `GET` | `/api/tasks` | 分页列表：`?page=1&count=20&status=FAILED` |
| `GET` | `/api/tasks/stats` | 各状态任务数、存储用量与上限（与[用量与配额](#用量与配额)的存储配额同口径；`storage_limit` 不限时为 `null`）、活跃 worker 数、近 7 天趋势 |
| `GET` | `/api/tasks/:id` | 详情，含 jobs 及其 `source_file`、`output_file` |
| `POST` | `/api/tasks/:id/retry` | 重试 |
| `POST` | `/api/tasks/:id/cancel` | 取消未结束的 job |
| `DELETE` | `/api/tasks/:id` | 删除已结束的任务及只属于它的产物，见[删除](#删除) |

### 创建

单输出或兼容预设时可使用 `options`（可选附带 `target`）或 `workflow_id`；需要一次提交生成多份输出或为输出命名时使用 `outputs`。`items` 的每一项给已上传的文件 `file_id`，或给一个源图 `url`（见 [URL 源](#url-源)）：

```json
{
  "items": [{ "file_id": "0198…" }, { "url": "https://images.example.com/cat.jpg" }],
  "outputs": [
    { "name": "preview", "options": { "format": "avif", "resize": { "width": 800 } }, "target": { "max_bytes": 102400 } },
    { "name": "fallback", "options": { "format": "webp", "quality": 80 } }
  ],
  "idempotency_key": "upload-batch-42"
}
```

- `outputs`、`options`（可配可选 `target`）、`workflow_id` **三选一**（给出 `target` 时必须同时给出 `options`，不可与 `outputs` 或 `workflow_id` 混用）。
- `outputs` 为 1–10 项数组，每项含必填 `options`、可选 `name`（正则 `[A-Za-z0-9_-]{1,64}`，同任务内不可重复，且不可与未命名项自动分配的 `1..N` 序号碰撞）以及可选 `target`（第 1 期 `{ "max_bytes": <正整数> }`）。
- 每条 `(item, output)` 生成一个 job；`items` 最多 100 个，总 job 数 `len(items) * len(outputs)` 最多 200。每个 item 必须**恰好**给出 `file_id` 与 `url` 之一。`file_id` 指向的文件必须属于当前用户且为 `UPLOADED`，提交后对应草稿被删除。
- `format: "png"` 不支持 `target`。对非 PNG 格式：当 `target` 非空且省略 `quality`/`distance` 且 `lossless: false` 时，worker 在该格式内部二分搜索参数并输出 `resolved_options`（产物复用键前缀 `compression-target/v1`）；若显式指定了 `quality`/`distance` 或 `lossless: true`，则仅做确定性单次编码并检查 `target_met`（产物复用键沿用 `image-options/v2`）。
- 产物文件名：单输出为 `<stem>.<ext>`，多输出为 `<stem>.<name|index+1>.<ext>`（`jpeg` 使用 `jpg`）。
- 已有相同产物（同一用户、同一源文件、同一规范化参数，搜索任务还含 `target`）的 job 直接完成，并按新 job 的 `target` 重算 `target_met` 与 `achieved`。只复用状态为 `UPLOADED` 且剩余保留期不少于其总保留期一半的产物（见[存储保留期](#存储保留期)），否则重新生成。
- `idempotency_key`：同一用户同一 key 的重复请求返回第一次创建的任务；请求内容（`workflow_id`、规范化输出集合、文件与 URL 集合）不同则 `409`。

#### URL 源

`{ "url": "…" }` 让 worker 从这个地址下载源图，不必先上传：

- 创建时只检查格式：绝对的 `http`/`https` URL，有主机名，不带用户名密码，首尾无空白，最多 2048 字节。不发起网络请求，所以地址不可达要到执行时才知道。
- 每个 URL item 新建一个状态为 `FETCHING` 的源文件，`source_url` 记录原地址；`name` 取 URL 路径的最后一段（没有则为 `image`）。同一个 URL 出现两次就是两个源文件。
- worker 第一次执行时下载并存入存储，之后文件变为 `UPLOADED`，填入 `size` 与 `mime_type`，按普通上传文件计算保留期；之后的重试直接读存好的文件，不再下载。下载失败时文件保持 `FETCHING`，重试会重新下载；一直没下载成功的文件从创建时起按源文件保留期过期（见[存储保留期](#存储保留期)），之后重试返回 `409 error.file.gone`。
- 只允许公网地址：指向内网、本机、保留地址（含经重定向到达的）会以 `url_rejected` 失败，不可重试；超时、连接失败、远端 5xx 以 `fetch_failed` 失败并自动重试，远端 4xx 不重试。重定向最多 3 跳；文件大小上限与上传相同。完整规则见[队列契约 §URL 源](/contracts/queue#url-源)。
- 不发送 Cookie、凭据或 `Referer`。私有存储桶的图请传预签名 URL；URL 在任务执行前过期会导致 `fetch_failed`（403，不重试）。
- URL 源不复用已有产物：同一个 URL、同样的参数再提交一次，会重新下载并处理。
- URL 源不是上传：对它调用 `complete` 返回 `404`。

| 情况 | 响应 |
| :--- | :--- |
| `items` 为空；某个 item 没有或同时给出 `file_id` 与 `url` | `400 error.param.required` |
| `url` 不是合法的 `http`/`https` URL（见 [URL 源](#url-源)） | `400 error.param.invalid` |
| 超过 100 个 item、超过 10 个 output、或总 job 数超过 200 | `400 error.param.invalid` |
| `outputs` / `options` / `workflow_id` 缺失或冲突；`name` 非法/重名/序号碰撞；`options` 或 `target` 非法（含 `png` 带 `target`）；`idempotency_key` 过长 | `400 error.param.invalid` |
| 文件不存在或未上传完成 | `400 error.param.invalid` |
| `workflow_id` 不存在 | `400 error.param.invalid` |
| 同 key 不同内容 | `409 error.param.invalid` |
| 超出每月处理量或存储已满（见[用量与配额](#用量与配额)） | `403 error.quota.exceeded`，什么都不创建 |

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
- 源文件已过期或已被删除时返回 `409 error.file.gone`：重新上传后创建新任务。
- 重试同样要过配额检查，超出时返回 `403 error.quota.exceeded`，job 保持原状。

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
- 产物过期或被删除后 job 记录保留：`status` 仍是 `COMPLETED`，`output_file.status` 变为 `EXPIRED` 或 `DELETED`，不再能下载。

### 删除

`DELETE /api/tasks/:id`（及 [`DELETE /v1/tasks/:id`](#删除-1)）删除任务、它的 job 与 attempt 记录，以及**只被这个任务使用**的产物：

```json
{ "id": "0198…", "deleted_outputs": ["0198…"], "retained_outputs": ["0198…"] }
```

- 任务还有未结束（`PENDING`、`PROCESSING`）的 job 时返回 `409 error.task.conflict`，不会自动取消；先调用取消，再删除。
- 产物按引用计数处理：同一产物可能被别的任务复用（见[创建](#创建)），只有当最后一个指向它的 job 被删除时才删除存储对象。仍被其他任务使用的产物列在 `retained_outputs`，它们按自己的保留期过期。
- 源文件不随任务删除（同一源文件常被多个任务使用），需要时用 `DELETE /api/files/:id`。
- 不存在或不属于当前用户：`404 error.task.not_found`。

`DELETE /api/files/:id`（及 `DELETE /v1/files/:id`）删除本人的一个文件，源文件或产物均可，返回文件本身：

| 文件状态 | 结果 |
| :--- | :--- |
| `UPLOADED` 或 `FETCHING`，没有未结束的 job 读取它 | `200`，状态变为 `DELETED`，存储对象（如已下载）随后删除；草稿一并删除 |
| `UPLOADED` 或 `FETCHING`，是未结束 job 的源文件 | `409 error.file.conflict` |
| `UPLOADING` | `200`，状态变为 `DELETED`；分片上传立即中止，已签出的上传 URL 过期（最多 1 小时）后删除对象和记录 |
| `EXPIRED`、`DELETED` | `200`，原样返回（幂等） |
| 不存在或不属于当前用户（含已彻底删除的文件） | `404 error.file.not_found` |

先在事务里改状态，再删除存储对象；对象删除失败不影响响应，由[存储保留期](#存储保留期)的清理任务重试。被删除的文件若仍有 job 指向它，记录保留并显示 `DELETED`；否则对象删除后记录也删除。

### 存储保留期

源文件与产物各有保留期，从上传完成（`uploaded_at`）算起，默认均为 30 天，由环境变量 `SOURCE_RETENTION_DAYS`、`OUTPUT_RETENTION_DAYS` 配置（`0` 表示永久保留）。用户表上的 `source_retention_days`、`output_retention_days` 可按用户覆盖（预留，暂无界面）。被任意 job 产出的文件按产物计，其余按源文件计。

文件变为 `UPLOADED` 时（上传 `complete`、worker 产出产物或下载完 URL 源）在同一事务里写入 `expires_at`，所以 `complete` 的响应已带到期时间。还没下载成功的 URL 源（`FETCHING`）从创建时间（`created_at`）算起，按源文件保留期到期；下载成功后改从 `uploaded_at` 重新计算。

orchestrator 每 10 分钟运行一次清理：

1. 为仍没有到期时间的 `UPLOADED` 与 `FETCHING` 文件补写 `expires_at`（例如上线前已有的文件、所有 job 在下载前就已结束的 URL 源）。
2. 到期的文件（`UPLOADED` 或 `FETCHING`）改为 `EXPIRED`，再删除存储对象（`FETCHING` 文件可能留有下载到一半的对象）。仍被未结束 job 读取的文件跳过，等 job 结束后再处理。
3. 重试此前删除失败的对象（`EXPIRED`/`DELETED` 且尚未清除的文件），以及签名 URL 已过期的已删除上传的对象。

另外每小时一次：

- 上传窗口（24 小时）过了仍是 `UPLOADING` 的文件：存储里已有对象（上传完成但没调用 `complete`）则改为 `UPLOADED` 并写入 `expires_at`，否则改为 `DELETED` 并清除；存储暂时不可用时留到下次。
- 7 天前加入、尚未提交的草稿：文件没有任何 job 引用则一并删除，否则只移除草稿。

到期后：

- job 的 `output_file.status` 为 `EXPIRED`，`/v1` 不再给出 `download_url`；`GET /api/files/:id/download-url` 返回 `410 error.file.gone`，`details: {"status": "EXPIRED"}`（被删除的文件同理，`status` 为 `DELETED`）。
- 产物复用与秒传（同 SHA-256 上传）只使用剩余保留期不少于一半的文件，所以复用不会让对象活过它自己的保留期，也不会把即将过期的文件交给新任务。
- 用过期源文件重试返回 `409 error.file.gone`。

### 用量与配额

**计量单位**是"处理单位"：一次成功的处理按实际编码轮数（attempt 的 `encode_rounds`）计，普通转换是 1 个单位，按体积目标搜索通常要 5～9 个单位。下面这些不计单位，但仍有记录：

- 复用已有产物的 job（见[产物复用](/architecture/overview#产物复用)）：0 个单位。
- 失败的 attempt：0 个单位，记下 `compute_ms`。
- URL 源的下载：记下载字节数；下载下来的文件和上传的一样占存储。

每个事件在 `usage_records` 里记一行，同一个 attempt 的同一类事件只记一次，所以队列重投、结果重放不会重复计量。删除任务或文件不会删除用量记录。月份按 UTC 自然月，job 算在它完成的那个月。

**配额**按用户计，有两项：

| 配额 | 默认 | 配置 | 检查 |
| :--- | :--- | :--- | :--- |
| 每月处理量 | 10,000 单位 | `QUOTA_MONTHLY_UNITS` | 创建任务与重试 |
| 存储 | 10 GiB | `QUOTA_STORAGE_BYTES` | 上传 `init`；创建任务与重试时只看存储是否已满 |

`0` 表示不限。`users` 表上的 `monthly_unit_quota`、`storage_quota_bytes` 可按用户覆盖（`NULL` 用全局默认，暂无界面）。

- **每月处理量**：本月已结算的单位，加上所有未结束 job 预占的单位，再加上这次请求要预占的单位，超过上限就拒绝。预占按 job 估算：按体积目标搜索的 job 预占 7 个单位（JXL 是 9 个），其余 1 个；全部复用已有产物的任务不预占，因此不会被拒绝。job 完成后按实际轮数结算，预占随之释放。同一用户的配额检查串行执行，并发提交不会一起越过上限。
- **存储**：已上传完成（`UPLOADED`）的文件大小，加上上传中（`UPLOADING`）文件申报的大小。上传 `init` 按"已用 + 本次申报大小 > 上限"拒绝；秒传不占新空间，不检查。存储是软限制：任务的产物可能让用量略超上限，超过后再上传或创建任务都会被拒绝，直到删除文件或文件过期。
- 已经接受的任务总会跑完，配额只在入口检查。

超出时返回 `403`：

```json
{
  "success": false,
  "code": "error.quota.exceeded",
  "message": "Monthly processing quota exceeded.",
  "data": null,
  "details": { "quota": "monthly_units", "limit": 10000, "used": 9990, "reserved": 7, "requested": 7 }
}
```

`quota` 是 `monthly_units` 或 `storage`（单位是字节）；`used` 是已结算的用量（存储为当前占用），`reserved` 是其他未结束 job 已预占的单位，`requested` 是这次请求要用的量。

`GET /api/usage?month=YYYY-MM`（`/v1` 为 `GET /v1/usage`，需要 `tasks:read`）返回某月的用量，`month` 省略时为当月，格式不对返回 `400 error.param.invalid`：

```json
{
  "month": "2026-11",
  "period_start": "2026-11-01T00:00:00Z",
  "period_end": "2026-12-01T00:00:00Z",
  "units": { "used": 277, "reserved": 7, "limit": 10000 },
  "storage": { "used_bytes": 859832320, "limit_bytes": 10737418240 },
  "totals": {
    "outputs": 121, "reused_outputs": 16, "failed_attempts": 11, "encode_rounds": 277,
    "compute_ms": 64000, "output_bytes": 19200000, "output_pixels": 320000000, "fetched_bytes": 42000000
  },
  "daily": [{ "date": "2026-11-01", "units": 21, "outputs": 9, "reused_outputs": 0, "failed_attempts": 0 }]
}
```

- `limit`、`limit_bytes` 不限时为 `null`。`reserved` 只在当月有值。`storage` 总是当前占用，与 `month` 无关。
- `outputs` 是本月成功处理的产物数（不含复用），`output_pixels` 是产物的像素数（源图像素暂不记录）。
- `daily` 只列有用量的日期（UTC），按日期升序。

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
| `POST` | `/api/upload/complete` | `{key, parts?}`；分片上传须带 `parts: [{part_number, etag}]`。文件变为 `UPLOADED`，`data` 为 `{file_id, key, location}`；对已 `UPLOADED` 的文件重复调用直接返回；存储里还没有该对象时返回 `409`；文件已删除时返回 `404` |
| `GET` | `/api/upload/multipart/:uploadId/list-parts?key=` | 已上传分片：`data` 为 `{parts: [{part_number, size, etag}]}` |
| `GET` | `/api/upload/multipart/:uploadId/sign-part/:partNumber?key=` | 重新签一个分片（init 返回的 URL 一小时后过期）：`data` 为 `{part_number, url, expires_at}` |
| `POST` | `/api/upload/multipart/:uploadId/abort` | `{key}`；中止并删除，文件变为 `DELETED`（同[删除](#删除)中的 `UPLOADING`），`data` 为 `null` |

`key` 不以 `uploads/<user_id>/` 开头，或 `uploadId` 与该文件不符时返回 `403`；文件记录不存在或已不是上传中（已删除、已过期）返回 `404 error.file.not_found`。

这些接口与其他 `/api` 一样返回[响应封装](#响应封装)，字段为 `snake_case`；控制台自己把 `data` 转成 Uppy 需要的形状。

`init` 的 `hash` 是十六进制 SHA-256，可省略。`data` 有三种：

- 命中本人已上传的同 `hash` 文件：`{file_id, key, fast_upload: true}`，无需上传。
- `size` 不超过 10 MB：`{file_id, key, url}`。向 `url` 单次 `PUT` 文件内容；`hash` 已签进 URL，内容不符时存储拒绝写入。
- 更大的文件：`{file_id, key, upload_id, part_urls}`。分片大小为 `max(5 MiB, ceil(size / 10000))`，`part_urls[i]` 对应第 `i + 1` 片。

除秒传外，`size` 加上已占用的存储超过配额时返回 `403 error.quota.exceeded`，见[用量与配额](#用量与配额)。

## 文件、草稿与 Job

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| `GET` | `/api/files/:id/download-url` | 15 分钟预签名下载 URL（仅限本人文件；签名包含基于 `file.Name` 的 `ResponseContentDisposition` attachment 头）；文件已过期或已删除时 `410 error.file.gone` |
| `DELETE` | `/api/files/:id` | 删除本人的源文件或产物，见[删除](#删除) |
| `GET` | `/api/drafts/list` | 草稿（游标分页 `?cursor=`） |
| `POST` | `/api/drafts/:id/delete` | 删除草稿；文件没有任何 job 引用时一并删除（同[删除](#删除)），否则只移除草稿。草稿不存在时 `404 error.file.not_found` |
| `GET` | `/api/jobs/:id` | 单个 job |

## 对外 API /v1

面向调用方的分步教程见[对外 API 接入指南](/guides/api-quickstart)；本节是逐字段的契约。

只做异步：提交返回 `202`，客户端轮询任务（webhook 尚未实现）。所有响应沿用上文的[响应封装](#响应封装)，字段一律 `snake_case`。`/v1` 直接由 API 服务（默认 `:8080`）提供，不经 Web 的 Nitro 代理。同一个进程在 `/mcp` 以 MCP 工具提供同样的能力，见 [MCP 契约](/contracts/mcp)。

### 认证与 scope

```http
Authorization: Bearer ly_…
```

Key 在 Web 控制台的「API Keys」页面创建（[§API Key 管理](#api-key-管理)）。`/v1` 不接受会话。

| scope | 允许 |
| :--- | :--- |
| `tasks:read` | `GET /v1/tasks`、`GET /v1/tasks/:id`、`GET /v1/usage` |
| `tasks:write` | 上传（`/v1/uploads/**`）、`POST /v1/tasks`、取消、重试、删除（`DELETE /v1/tasks/:id`、`DELETE /v1/files/:id`） |

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
| `POST` | `/v1/uploads/:id/abort` | write | `200` + `File`（`DELETED`） |
| `POST` | `/v1/tasks` | write | `202` + `Task`，`Location`、`Retry-After` |
| `GET` | `/v1/tasks` | read | `200 {tasks, total, page, limit}` |
| `GET` | `/v1/tasks/:id` | read | `200` + `Task`（含下载链接），`ETag`；或 `304` |
| `POST` | `/v1/tasks/:id/cancel` | write | `200` + `Task` |
| `POST` | `/v1/tasks/:id/retry` | write | `202` + `Task`，`Location`、`Retry-After` |
| `DELETE` | `/v1/tasks/:id` | write | `200 {id, deleted_outputs, retained_outputs}` |
| `DELETE` | `/v1/files/:id` | write | `200` + `File` |
| `GET` | `/v1/usage` | read | `200` + 用量（见[用量与配额](#用量与配额)） |

`:id` 不是 UUID 时返回 `400 error.param.invalid`；不属于当前 key 所有者的任务、上传与文件一律按不存在处理（`404`）。

### 上传

与 `/api/upload` 是同一套 S3 预签名直传逻辑，只是改用文件 ID 寻址并统一封装。

```json
// POST /v1/uploads
{ "filename": "a.jpg", "content_type": "image/jpeg", "size": 245760, "sha256": "9f86d0…" }
```

- `filename`、`size`（正整数）必填；`content_type` 省略时为 `application/octet-stream`；`sha256` 为可选的十六进制 SHA-256。
- 响应 `Upload`：

```json
{ "file": { "id": "0198…", "name": "a.jpg", "status": "UPLOADING", "size": 245760, "mime_type": "image/jpeg", "sha256": "9f86d0…", "created_at": "…" },
  "method": "put", "url": "https://…", "expires_at": "…" }
```

| `method` | 客户端要做的 |
| :--- | :--- |
| `none` | 本人已有同 `sha256` 的已上传文件，`file` 即该文件，直接用它的 `id` 提交任务（`200`） |
| `put` | `size` ≤ 10 MB：把文件内容 `PUT` 到 `url`（无需额外请求头；`sha256` 已签进 URL，内容不符时存储拒绝），再调用 `complete` |
| `multipart` | 更大的文件：按 `part_size` 切片，第 `i + 1` 片 `PUT` 到 `part_urls[i]`，记下每片响应的 `ETag` 头，再以 `{"parts": [{"part_number": 1, "etag": "\"…\""}, …]}` 调用 `complete` |

- 上传 URL 在 `expires_at`（1 小时）后失效；分片可用 `POST /v1/uploads/:id/parts/:n/url` 重新签名，用 `GET …/parts` 查询已上传的分片。
- `complete` 后文件为 `UPLOADED`，`size` 与 `mime_type` 以存储实际为准。对已上传的文件重复调用直接返回；分片上传未带 `parts` 返回 `400 error.param.required`；存储里还没有该对象（`PUT` 未完成或被存储拒绝，例如 `sha256` 不符）返回 `409 error.param.invalid`，文件仍为 `UPLOADING`，可以重新 `PUT` 后再调用。
- 对单次 PUT 的上传调用分片相关端点、对已完成的上传中止，返回 `409 error.param.invalid`。
- 中止即删除：文件变为 `DELETED`，之后的 `complete`、分片端点和再次中止返回 `404 error.file.not_found`。
- 未完成的上传 24 小时后由清理任务回收（见[存储保留期](#存储保留期)）。
- 上传中的文件按申报的 `size` 计入存储；申报后会超出存储配额时返回 `403 error.quota.exceeded`（秒传不检查），见[用量与配额](#用量与配额)。

### 创建任务

```http
POST /v1/tasks
Authorization: Bearer ly_…
Idempotency-Key: batch-2026-11-16-001
Content-Type: application/json

{ "items": [{ "file_id": "0198…" }], "options": { "format": "avif" }, "target": { "max_bytes": 102400 } }
```

请求体与 [`POST /api/tasks`](#创建) 相同（`items` 的每项为 `file_id` 或 [`url`](#url-源)；`options`(+`target`)、`outputs`、`workflow_id` 三选一；限额与校验规则一致），只是**没有** `idempotency_key` 字段。

```json
{ "items": [{ "url": "https://images.example.com/cat.jpg" }], "options": { "format": "webp" } }
```

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
- 与 `/api` 的区别：不返回 `owner_id`、`operation`、`artifact_key`、`next_run_at`、租约与当前 attempt 等内部字段；`source_file`/`output_file` 只含上面列出的字段。URL 源的 `source_file` 另带 `source_url`，下载前 `status` 为 `FETCHING`、`size` 为 `0`。
- **下载链接**：`GET /v1/tasks/:id` 中，状态为 `COMPLETED` 且产物已上传的 job，其 `output_file` 带 `download_url` 与 `download_expires_at`（签名时刻 + 1 小时）。链接是存储的预签名 GET，签入了以产物文件名为附件名的 `response-content-disposition`。过期后重新 `GET` 任务即可拿到新链接。创建、取消、重试与列表的响应不带下载链接。
- **保留期**：`source_file`、`output_file` 带 `expires_at`，即存储清理删除该文件的时间；保留期为 `0` 时省略。产物过期或被删除后，job 仍是 `COMPLETED`，`output_file.status` 为 `EXPIRED` 或 `DELETED`，不带 `download_url`。规则见[存储保留期](#存储保留期)。

### 取消与重试

- `POST /v1/tasks/:id/cancel`：无请求体，取消所有未结束的 job，返回 `200` 与任务。
- `POST /v1/tasks/:id/retry`：请求体可省略，字段与 [`/api` 的重试](#重试)相同（`job_ids`、`retry_all`、`options`、`target`）。成功返回 `202`、`Location`、`Retry-After`。点名了未结束的 job：`409 error.task.conflict`；没有可重试的 job 或参数不合法：`400 error.param.invalid`；源文件已过期或已删除：`409 error.file.gone`。

### 删除

`DELETE /v1/tasks/:id` 与 `DELETE /v1/files/:id` 无请求体，需要 `tasks:write`，行为与 [`/api` 的删除](#删除)相同：任务有未结束的 job 时 `409 error.task.conflict`（先取消）；产物按引用计数删除，仍被其他任务复用的列在 `retained_outputs`；文件被未结束的 job 读取时 `409 error.file.conflict`。

### 限流

`/v1` 的写请求按用户限流（同一用户的所有 key 共用额度），用令牌桶：默认每分钟 60 次、最多连续 30 次，由 `RATE_LIMIT_WRITE_PER_MINUTE`、`RATE_LIMIT_WRITE_BURST` 配置，`RATE_LIMIT_WRITE_PER_MINUTE=0` 关闭。

- 计入限流的只有 `POST /v1/uploads`、`POST /v1/tasks`、取消、重试、`DELETE /v1/tasks/:id`、`DELETE /v1/files/:id`。上传的后续步骤（`complete`、分片、`abort`）与所有读请求不计。
- 超出时返回 `429 error.rate_limit.exceeded`，带 `Retry-After`（整数秒），`details: {"retry_after": 2}`。等待后原样重试即可；创建任务时带 `Idempotency-Key`。
- 计数放在 Redis，只用来限流。Redis 不可用时放行请求，并在日志里告警。
- `/api`（Web 控制台）不限流，但同样检查配额。

### 错误码

| 状态 | 码 | 场景 |
| :--- | :--- | :--- |
| `400` | `error.body.invalid` | 请求体不是合法 JSON、含未知字段或缺失 |
| `400` | `error.param.invalid` / `error.param.required` | 参数校验失败（规则同 `/api`），`details` 指出字段 |
| `401` | `error.auth.unauthorized` | 无 key、key 无效或已吊销 |
| `403` | `error.auth.insufficient_scope` | key 缺少 scope |
| `403` | `error.quota.exceeded` | 超出每月处理量或存储配额，见[用量与配额](#用量与配额) |
| `404` | `error.task.not_found` / `error.file.not_found` | 任务 / 上传 / 文件不存在或不属于该用户 |
| `409` | `error.idempotency.conflict` | `Idempotency-Key` 已用于不同内容的请求 |
| `409` | `error.task.conflict` | 重试点名了未结束的 job；删除仍有未结束 job 的任务 |
| `409` | `error.file.conflict` | 删除仍被未结束 job 读取的文件 |
| `409` | `error.file.gone` | 重试时源文件已过期或已删除 |
| `409` | `error.param.invalid` | 上传状态不允许该操作（见上传） |
| `429` | `error.rate_limit.exceeded` | 写请求过于频繁，按 `Retry-After` 等待，见[限流](#限流) |
| `500` | `error.internal` | 其他 |

## 已知不一致

- `JWT_SECRET` 配置与 JWT 中间件仍在代码中，但没有使用（实际是 session 认证）。
