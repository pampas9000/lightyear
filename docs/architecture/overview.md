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
| API | `server/cmd/api` | HTTP API、会话、上传与下载签名、任务与预设持久化 |
| Orchestrator | `server/cmd/worker` | 从数据库领取到期任务并派发、处理 worker 结果、租约恢复 |
| 迁移 | `server/cmd/migrate` | 建表；`-reset` 删除全部应用表后重建（仅开发） |
| image worker | `compute/worker` | 从 `q:image` 收派发、下载源图、调用 `compute-imageops`、上传产物、回报结果；**不连数据库** |
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
| `users` / `user_oauth_accounts` | 用户、bcrypt 密码哈希、GitHub/Google 绑定 |
| `files` | 源文件与产物（路径、大小、MIME、宽高、SHA-256、状态 `UPLOADING`/`UPLOADED`/…） |
| `draft_files` | 已上传但尚未提交任务的文件 |
| `workflows` | 命名的 `ImageOptions` 预设 |
| `tasks` | 一次提交；含 `idempotency_key` 与请求指纹，状态由 jobs 聚合 |
| `jobs` | 一个源文件 → 一个产物：`operation`（目前只有 `image.convert`）、规范化后的 `options`、`source_file_id`、`output_file_id`、`artifact_key`、重试计数、当前 attempt 与租约 |
| `attempts` | 一次派发：完整的派发负载、产物 key、`dispatched_at`/`started_at`/`finished_at`。先写 attempt 再发消息，因此它同时充当 outbox |

产物文件在任务**完成时**才创建 `files` 记录，路径为 `outputs/<owner>/<job>/<attempt>.<ext>`，按 attempt 隔离。

### 状态

```text
Job:      PENDING ──▶ PROCESSING ──▶ COMPLETED
             ▲            │
             └── 可重试 ───┤──▶ FAILED
                          └──▶ CANCELLED（PENDING 也可直接取消）

Attempt:  QUEUED ──▶ RUNNING ──▶ COMPLETED | FAILED | EXPIRED | CANCELLED
```

`Task` 状态由其 jobs 聚合：`PENDING`、`PROCESSING`、`COMPLETED`、`PARTIALLY_FAILED`、`FAILED`、`CANCELLED`（`server/internal/services/jobstate`）。

### 产物复用

创建任务时，同一用户、同一源文件、同一规范化参数如果已有完成的产物，job 直接标记为完成并指向该产物，不再派发。键的计算见 [ImageOptions §产物键](/contracts/image-options#规范化与产物键)。

## 存储与上传

- S3 兼容存储（R2 友好：path-style、region 默认 `auto`）。
- 上传走分片预签名直传；初始化时带 SHA-256，命中本人已上传的文件则直接复用（秒传）。
- 下载走 15 分钟预签名 URL。
- API 每小时清理过期的分片上传与陈旧草稿；被任务引用的文件不会被清理。

## 扩展点（尚未实现）

- **新操作**（AI 放大、生图、编辑）：在 `operation` 加一个值、在 orchestrator 里映射到新的队列（如 `q:gpu`）、部署对应的 worker 池；表结构不变。多输入的操作出现时再给 `jobs` 加输入列。
- **对外 API 与 MCP**：只做异步，`POST` 返回 202 与任务 ID，下游轮询或接收 webhook。计划见[路线图第 9 步](/roadmap#第-9-步-对外-api-与-mcp-只做异步)。

## 相关文档

[请求链路](/architecture/request-flows) · [可靠性设计](/architecture/reliability) · [队列契约](/contracts/queue) · [HTTP API](/contracts/http-api)
