# 架构总览

Lightyear 是一个多语言 Monorepo，承载同一套转码能力在不同运行时中的实现与交付。设计目标是把**产品入口**、**业务协调**与**计算核心**解耦：

- `web` 是产品入口：界面、上传、任务提交与结果展示
- `server` 是业务协调层：HTTP API、任务创建、调度、状态聚合与系统集成
- `compute` 是能力核心：可移植编解码、宿主管线、WASM 导出与 Worker 执行
- `docs` 是跨模块的架构、契约与工程记录

## 顶层结构

```text
web ──HTTP──▶ server ──Outbox──▶ Redis Streams ──▶ executor
                │                                      ├── compute-worker ─▶ compute-server-adapter
                ├── PostgreSQL                          └── future AI provider worker
                └── S3/R2（上传 / attempt 隔离产物）

浏览器 ──Web Worker──▶ compute-wasm ──▶ compute-engine
```

### 运行组件

| 组件 | 位置 | 职责 |
| :--- | :--- | :--- |
| API 服务 | `server/cmd/api` | HTTP API、上传/下载签名、任务与工作流持久化、会话 |
| Orchestrator | `server/cmd/worker` | 消费任务流、原子抢占、派发执行负载、回收结果、聚合状态、租约恢复；外部 Provider 走持久化 invocation ledger |
| compute-worker | `compute/worker` | 消费计算流、下载输入、调用 `run_transcode`、上传产物、回写结果；**不连数据库** |
| compute-server-adapter | `compute/crates/server-adapter` | `run_transcode` 管线：工具探测、magic-byte 输入识别、JPEG→JXL 直通、原生 YUV 直通、解码/编辑/色彩/编码 |
| compute-engine | `compute/crates/engine` | 可移植核心：编解码封装、色彩与传递函数、编辑管线、Y4M/PAM 序列化、抖动/色调映射、ICC/EXIF 解析 |
| compute-types | `compute/crates/types` | 跨语言共享类型与 Redis Streams 线上协议（`protocol.rs`） |
| compute-wasm | `compute/bindings/wasm` | 浏览器导出（`wasm-bindgen`） |
| Web 控制台 | `web` | Nuxt 4 薄客户端 + 代理，及 WASM 本地沙盒 |

## 技术栈

- **server**：Go 1.26、Fiber v3、GORM + gorm/gen、PostgreSQL、Redis Streams、AWS SDK for Go v2
- **compute**：Rust（edition 2024），核心库依赖 `image`、`fast_image_resize`、`img-parts`、`kamadak-exif`、`yuv`、`jxl-oxide`；worker 使用 `redis-rs` 与 `aws-sdk-s3`
- **web**：Nuxt 4、Vue 3.5、Vite、Tailwind CSS 4（CSS-first，无 `tailwind.config`）、shadcn-nuxt、@nuxtjs/i18n（6 个 locale）、Uppy 上传、`@jsquash/jxl`
- **外部 CLI**（`compute-server-adapter` 探测后使用）：`cjxl`、`avifenc`、`avifdec`、`heif-dec`、`cwebp`；`djxl` 仅用于探测与测试

## 数据模型

所有模型定义在 `server/internal/models/models.go`，由 `server/internal/schema` 统一迁移。API 默认启动时执行；生产环境可关闭 `AUTO_MIGRATE`，发布前运行 `go run ./server/cmd/migrate`。

| 模型 | 说明 |
| :--- | :--- |
| `User` | 用户与密码哈希（bcrypt） |
| `UserOauthAccount` | GitHub / Google 第三方账号绑定 |
| `File` | 源文件与产物文件（路径、大小、MIME、状态、SHA-256）；状态为字符串：`UPLOADING`/`UPLOADED`/`GENERATING`/`FAILED`/`ABORTING`/`ABORTED`/`DELETING` |
| `DraftFile` | 上传草稿（与 `File` 一对一，控制队列中的可见性） |
| `Workflow` | 可复用的转码预设（`target_format` + `params`） |
| `Task` | 一次提交的批处理容器；含 `idempotency_key` 与 `request_fingerprint` |
| `Job` | 单个执行意图；含操作类型、executor、快照参数、`progress`、`retry_count`/`max_retries`、当前 `attempt_id` |
| `JobInput` / `JobOutput` | 有序、多角色输入与逻辑输出；支持 source/mask/reference 和多产物 |
| `JobAttempt` | 一次 generation 的派发负载、输出 manifest、执行状态；产物路径按 attempt 隔离 |
| `OutboxEvent` | 与业务事务同提交的 Redis Stream 事件；relay 采用 at-least-once |
| `ProviderInvocation` | 外部付费 API 的提交账本；记录幂等键、远端任务、未知提交状态与用量 |

设计约定：

- **主键 UUID v7**（`Base.BeforeCreate`，时间有序，利于索引）
- **无物理外键**：数据库层不建 FK，一致性由应用层保证（迁移时 `DisableForeignKeyConstraintWhenMigrating`）
- **参数快照**：任务创建时把操作参数 JSON 快照进 `Job.OperationParams`，转码保留 `Job.Params` 兼容旧数据
- **可靠性边界**：本地执行通过 attempt/artifact 校验避免旧尝试覆盖新结果；外部付费调用不能宣称 exactly-once，未知提交必须先 reconcile

### 状态机

`Job`（PostgreSQL enum `job_status`）：

```text
PENDING ──▶ PROCESSING ──▶ COMPLETED
   │            │
   │            ├──▶ FAILED（可重试，max_retries 默认 3）
   └────────────┴──▶ CANCELLED
```

`PROCESSING` 同时受 `generation`、`JobAttempt` 和显式 lease 保护；队列重复投递不会创建第二次有效本地执行。未安装的 executor 不会接受任务，API 返回 `422`。

`Task` 由子 Job 聚合而来：`PENDING` / `PROCESSING` / `COMPLETED` / `PARTIALLY_FAILED` / `FAILED` / `CANCELLED`。聚合规则见 `server/internal/services/task/aggregation.go`。

## 存储与上传

- 对象存储经 AWS SDK v2 接入 S3 兼容服务（R2 友好：`UsePathStyle`、region 默认 `auto`）
- 上传走**分片预签名**：`/api/upload/multipart/*`，支持 SHA-256 秒传（命中已上传文件则直接建草稿）
- 下载走 15 分钟预签名 URL：`GET /api/files/:id/download-url`
- 后台清理任务每小时回收过期分片上传与陈旧草稿（`services/storage/cleanup.go`）

## 边界原则

- `server` 不实现编解码算法，也不复制 `compute-types` 领域模型
- `compute-worker` 只做编排（S3/Redis/结果），算法全部下沉到 adapter/engine
- `web` 不持有数据库或 Redis 连接，所有业务请求经 Nitro 代理到 Go API
- `bindings` 保持薄：只做边界适配，不写业务规则
- 跨边界新增字段优先落到 `compute-types`

## 相关文档

- [请求链路](/architecture/request-flows) · [可靠性设计](/architecture/reliability)
- [接口契约](/contracts/) · [色彩管线](/compute/color-pipeline) · [已知限制](/compute/limitations)
