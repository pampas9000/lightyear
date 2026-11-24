# 架构总览

Lightyear 是一个图像处理平台：用户上传图片，按一份 [`ImageOptions`](/contracts/image-options)（输出格式、质量、缩放、色彩、元数据等）异步生成产物；浏览器里还有一个用同一个 libvips 在本地处理的沙盒。

```text
web ──HTTP──▶ API（Go） ──▶ PostgreSQL（唯一事实来源） ◀── Orchestrator（Go）
                │                                            │ XADD q:image     ▲ q:results
                └── S3/R2（上传、产物）                       ▼                  │
                                     ▲               image worker（Rust + libvips）
                                     └──────── 下载源图 / 上传产物 ────┘

浏览器 ──Web Worker──▶ wasm-vips（同一个 libvips，AVIF/HEIF、JXL 模块按需加载）
```

## 组件

| 组件 | 位置 | 职责 |
| :--- | :--- | :--- |
| API | `server/cmd/api` | HTTP API（`/api` 供控制台、会话认证；`/v1` 对外、API Key 认证）与 MCP 服务器（`/mcp`，同一套 API Key 与服务）、上传与下载签名、任务与预设持久化 |
| Orchestrator | `server/cmd/worker` | 从数据库领取到期任务并派发、处理 worker 结果、租约恢复 |
| 迁移 | `server/cmd/migrate` | 建表；`-reset` 删除全部应用表后重建（仅开发） |
| image worker | `compute/worker` | 从 `q:image` 收派发、下载源图（URL 源先从外部地址抓取并存入存储）、调用 `compute-imageops`、上传产物、回报结果；**不连数据库** |
| compute-imageops | `compute/crates/imageops` | `ImageOptions` → libvips 的映射与输入防护，见[处理核心](/compute/imageops) |
| compute-types | `compute/crates/types` | `ImageOptions` 唯一定义、队列协议 |
| Web | `web` | Nuxt 4 控制台；业务请求经 Nitro 代理到 API；沙盒在 Web Worker 中运行 wasm-vips |

拆分原则：

- **PostgreSQL 是唯一事实来源**。Redis Stream 只负责投递，丢消息、重复投递都由数据库里的 attempt 与租约兜底。
- **worker 只做计算**，不连数据库，按执行类型分池（目前只有 `q:image`），可独立扩缩容。
- **一份参数**：Web、API、worker、浏览器沙盒都使用 `ImageOptions`；源图信息（色彩、位深、方向、HDR）一律由程序探测。

## 数据模型

模型在 `server/internal/models/models.go`，迁移在 `server/internal/schema`。主键是 UUID v7；数据库不建外键，一致性由应用层保证。

| 表 | 说明 |
| :--- | :--- |
| `users` / `user_oauth_accounts` | 用户、bcrypt 密码哈希、GitHub/Google 绑定；可空的 `source_retention_days`/`output_retention_days` 按用户覆盖存储保留期，`storage_quota_bytes`/`monthly_unit_quota` 按用户覆盖配额（均预留，暂无界面） |
| `files` | 源文件与产物（路径、大小、MIME、宽高、SHA-256）。状态：上传中 `UPLOADING`、URL 源等待 worker 抓取 `FETCHING`、可用 `UPLOADED`、保留期到期 `EXPIRED`、被删除 `DELETED`（用户删除、中止的上传、过期未完成的上传、陈旧草稿）。只有 `services/lifecycle` 把文件改为 `EXPIRED`/`DELETED`。`expires_at` 是保留期到期时间，`purged_at` 是存储对象删除完成的时间；URL 源的 `source_url` 记录原地址，抓取后保留 |
| `draft_files` | 已上传但尚未提交任务的文件 |
| `workflows` | 命名的多输出预设（`outputs` 数组，每项含 `ImageOptions` 与可选压缩 `target`） |
| `tasks` | 一次提交；含 `idempotency_key` 与请求指纹，状态由 jobs 聚合 |
| `jobs` | 一个源文件 + 一份输出规格 → 一个产物：`operation`（目前只有 `image.convert`）、`output_index`/`output_name`、规范化后的 `options`、可选 `target`、实际执行的 `resolved_options`、`target_met`、`achieved`、`source_file_id`、`output_file_id`、`artifact_key`、重试计数、当前 attempt 与租约 |
| `api_keys` | `/v1` 与 `/mcp` 的 API Key：所属用户 `owner_id`、`name`、用于辨认的 `prefix`、`key_hash`（明文的 SHA-256，唯一索引；明文只在创建时返回一次）、`scopes`（`text[]`：`tasks:read`/`tasks:write`）、`last_used_at`、`revoked_at`（吊销为软删除） |
| `attempts` | job 的一次执行：派发内容（`payload`，重发时原样使用）、产物 key、`started_at`/`finished_at`、可选 `encode_rounds`/`compute_ms`、`error_code` |
| `outbox_messages` | 待投递的队列消息，与需要它的状态变更在同一事务中写入，提交后发送：`queue`、`payload`、`attempt_id`（派发消息）、`available_at`、`tries`、`last_error`、`sent_at`、`stream_id`（发送成功时 `XADD` 返回的条目 ID）。一个 attempt 的首次派发和每次重发各一行；已发送的保留 7 天 |
| `usage_records` | 用量流水，一个事件一行：`kind` 为 `process`（成功处理，`units` = 编码轮数）、`reuse`（复用产物，0 单位）、`failed`（失败的 attempt，0 单位）、`fetch`（URL 源下载，记 `input_bytes`），另记 `owner_id`、`attempt_id`、`job_id`、`task_id`、`encode_rounds`、`compute_ms`、`output_bytes`、`output_pixels`。`(attempt_id, kind)` 唯一，重放不重复计量；不随任务或文件删除，按月配额与 `GET /api/usage` 都从这里汇总。规则见[用量与配额](/contracts/http-api#用量与配额) |

产物文件在任务**完成时**才创建 `files` 记录，路径为 `outputs/<owner>/<job>/<attempt>.<ext>`，按 attempt 隔离。源文件路径为 `uploads/<owner>/<uuid>.<ext>`；URL 源在创建任务时就建好 `FETCHING` 的记录和路径，worker 抓取后写入该路径并回报，orchestrator 再把它改为 `UPLOADED`。

`EXPIRED`/`DELETED` 的文件仍有 job 指向时保留记录，job 照常显示为完成，只是产物不可下载；没有 job 指向时，存储对象删除后记录也删除。

### 状态

```text
Job:      PENDING ──▶ PROCESSING ──▶ COMPLETED
             ▲            │
             └── 可重试 ───┤──▶ FAILED
                          └──▶ CANCELLED（PENDING 也可直接取消）

Attempt:  QUEUED ──▶ RUNNING ──▶ COMPLETED | FAILED | EXPIRED | CANCELLED
```

`Task` 状态由其 jobs 聚合：`PENDING`、`PROCESSING`、`COMPLETED`、`PARTIALLY_FAILED`、`FAILED`、`CANCELLED`（`server/internal/services/jobstate`）。

Job 的每种转换只在 `server/internal/services/jobstate` 实现一次：领取 `Claim`、推迟 `Postpone`、心跳 `Heartbeat`、完成 `Complete`、复用即完成 `Reuse`、自动重试 `Requeue`、失败 `Fail`、取消 `Cancel`、手动重试 `Reset`。调用方在 `jobstate.Run` 的事务里先加锁（先 task 后 job；心跳只锁 job），再调用转换。转换检查起始状态和锁，写回全部状态列前用 `jobstate.Check` 校验字段规则；事务提交前，每个状态变过的 task 聚合一次。字段规则：

- `current_attempt_id`：`PENDING` 为空，`PROCESSING` 为在跑的 attempt，终态为最后一个 attempt（复用完成或从未领取则为空）。
- `lease_expires_at` 仅 `PROCESSING` 非空；`next_run_at` 仅 `PENDING` 非空。
- `output_file_id`、`resolved_options`、`target_met`、`achieved` 只在 `COMPLETED` 时可以非空，且 `COMPLETED` 的 `progress` 为 100、没有错误码。
- `FAILED` 必有错误码，`CANCELLED` 的错误码为 `cancelled`；自动重试回到 `PENDING` 时保留上次的错误。

### 产物复用

创建任务时，同一用户、同一源文件、同一规范化参数如果已有完成的产物（状态为 `UPLOADED`，且剩余保留期不少于其总保留期的一半），job 直接标记为完成并指向该产物，不再派发（带参数搜索的 `target` 请求额外将 `target` 计入复用键 `compression-target/v1`；普通或仅校验 `target` 的请求沿用 `image-options/v2`，并按新 job 的 `target` 重算 `target_met` 与 `achieved`）。键的计算见 [ImageOptions §产物键](/contracts/image-options#规范化与产物键)。

## 存储与上传

- S3 兼容存储（R2 友好：path-style、region 默认 `auto`）。
- 上传走预签名直传：不超过 10 MB 的文件单次 PUT，SHA-256 签进 URL，由存储校验内容；更大的文件走分片，初始化时一次签好全部分片。初始化时带 SHA-256，命中本人已上传的文件则直接复用（秒传）。分片上传的 SHA-256 由客户端上报，存储不校验。
- 下载走 15 分钟预签名 URL（签名带入 `ResponseContentDisposition`，使用文件名称作为附件下载文件名）。
- 上传、补签分片、完成只接受 `UPLOADING` 的文件；完成用带 `status = 'UPLOADING'` 条件的更新落库，所以已删除的上传不会被完成接口改回 `UPLOADED`。中止上传即删除文件（见下节）。
- 上传窗口（24 小时）过了仍是 `UPLOADING` 的文件，以及 7 天前加入、尚未提交的草稿，由 lifecycle 每小时清理（`SweepUploads`）：对象已在存储里但没调用完成接口的上传补记为 `UPLOADED` 并写入 `expires_at`，其余删除；被任何 job 引用的文件只移除草稿。

### 保留期与删除

代码在 `server/internal/services/lifecycle`。源文件与产物各有保留期（`SOURCE_RETENTION_DAYS`、`OUTPUT_RETENTION_DAYS`，默认 30 天，`0` 为永久），从 `uploaded_at` 算起，用户可单独覆盖。被任意 job 产出的文件算产物，其余算源文件。文件变为 `UPLOADED` 时（上传完成、orchestrator 记录产物）在同一事务里写入 `expires_at`（`lifecycle.Schedule`）；还没下载的 URL 源（`FETCHING`）从 `created_at` 起按源文件保留期计算，下载成功后改从 `uploaded_at` 重算。API 进程每 10 分钟清理一次（`Sweep`）：给仍没有 `expires_at` 的文件补写，把到期的文件改为 `EXPIRED` 并删除对象，再重试此前删除失败的对象；每小时另清理一次未完成的上传与陈旧草稿（`SweepUploads`，见上节）。每一步每轮最多处理 `BatchSize`（500）行。用户删除任务、文件或草稿、中止上传（[HTTP API §删除](/contracts/http-api#删除)）走同一套删除：文件改为 `DELETED` 只经 `markDeleted` 一处。

未完成的上传被删除时，浏览器可能还拿着有效的签名 URL，`PUT` 或分片会在删除之后落地。所以删除时把 `upload_expires_at` 提前到最多 `UploadURLExpiry`（1 小时，与签名有效期相同）之后，立即中止分片上传，但到 `upload_expires_at` 之后才删除对象和记录，届时再中止一次分片上传（S3 文档说明正在传的分片可能在中止后仍然成功；`NoSuchUpload` 视为已中止）。

不变式：

- **先改库，后删对象。** 事务里先把文件改为 `EXPIRED`/`DELETED`，此后它不会再被复用、秒传、签名下载或用于重试；提交后才删对象。对象删除失败时 `purged_at` 留空，下一轮清理重试（对象不存在视为成功）。数据库因此不会声称一个已不存在的对象可用；反过来，对象可能比记录多活一会儿。
- **不删正在用的文件。** 到期或删除前对文件加行锁（`FOR UPDATE`），再检查是否有未结束（`PENDING`/`PROCESSING`）的 job 读取它：到期时跳过、等 job 结束；用户删除时返回 `409`。创建任务与重试对源文件加 `FOR SHARE` 并要求 `UPLOADED`，复用产物同样加 `FOR SHARE` 并复查状态，因此不会在检查之后冒出新的读者。加锁顺序与创建任务一致：先草稿后文件。
- **产物引用计数。** 一个产物可能被多个任务的 job 复用。删除任务时，只有不再有任何 job 指向的产物才删除；仍被引用的产物保留，按自己的保留期过期。
- **复用不延长寿命。** 复用与秒传只选剩余保留期不少于一半的文件，所以任何对象都不会活过它的保留期，按对象年龄配置的存储生命周期规则也就不会删掉仍在用的文件。

清理在多实例下可并发运行：每一步都重新加锁、复查状态，重复删除对象无害。

#### 可选：R2 生命周期规则兜底

应用的清理是主机制；可以在存储桶上再配一条按对象年龄删除的生命周期规则作兜底，用来回收应用漏掉的对象（例如被取消的 attempt 已上传但没有记录的产物、清理长期失败留下的对象）。这只是部署建议，仓库不包含也不会修改任何云端配置。

- 前缀：`uploads/` 与 `outputs/`（桶里只有这两类对象）。
- 年龄：**必须大于所有生效保留期中的最大值**，包括按用户覆盖的值，再留出余量，例如保留期 30 天时设为 45 天。规则按对象创建时间计算，而应用按 `uploaded_at` 计算，二者相差不到一次上传的时长。
- 有用户设置了永久保留（`0`）或更长保留期时，不要配置这条规则，或者把它限制在不含这些用户的前缀上（前缀的第二段是用户 ID）。
- 规则删除的对象，数据库仍可能记为 `UPLOADED`（直到清理把它标为 `EXPIRED`）；这正是年龄要大于保留期的原因。
- 未完成的分片上传由 lifecycle 中止；R2 也可以加一条「中止未完成的分片上传」规则（例如 2 天）作兜底。

## 扩展点（尚未实现）

- **新操作**（AI 放大、生图、编辑）：在 `operation` 加一个值、在 orchestrator 里映射到新的队列（如 `q:gpu`）、部署对应的 worker 池；表结构不变。多输入的操作出现时再给 `jobs` 加输入列。
- **webhook**：对外的 `/v1` 已提供 API Key 认证、异步提交（`202` + `Location`）、ETag 轮询、`Idempotency-Key`、URL 源与结果下载链接（见 [HTTP API §对外 API](/contracts/http-api#对外-api-v1)），`/mcp` 以三个 MCP 工具提供同样的能力（见 [MCP](/contracts/mcp)）；webhook 尚未实现，计划见[路线图第 9 步](/roadmap#第-9-步-对外-api-与-mcp-只做异步)。

## 相关文档

[请求链路](/architecture/request-flows) · [可靠性设计](/architecture/reliability) · [队列契约](/contracts/queue) · [HTTP API](/contracts/http-api)
