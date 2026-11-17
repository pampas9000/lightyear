# 端到端验证记录（2026-11）

2026-11-17 在本机对 `main`（`ac0f4e3`）做的一次真实链路验证：API、orchestrator、Rust worker、Postgres、Redis Stream 与对象存储（garage）一起跑，覆盖最近合入的四块改动：压缩目标搜索与多输出、`/v1` API、按用户公平派发与执行上限、存储生命周期与删除接口。此前它们只有数据库层的单元测试。

这是一次性的验证记录，不是现状说明。发现的问题已修复的见下文，未修复的记在 [roadmap](/roadmap) 12.9、12.10。验证之后的修复见文末[后续](#后续)。

## 环境

| 组件 | 配置 |
| :--- | :--- |
| Postgres 18 | Docker `pg`，一次性库 `lightyear_e2e`（`AUTO_MIGRATE=true` 建表），验证后删除 |
| Redis 8 | Docker `redis`，逻辑库 9，验证后清空 |
| 对象存储 | garage v1.1.0（`s3_region = "us-east-1"`），一次性桶 `lightyear-e2e` 与专用 key，验证后删除 |
| 进程 | `server/cmd/api`、`server/cmd/worker`（orchestrator）、`compute-worker`（release）各一，公平派发场景另起第二个 worker |
| 工具链 | libvips 8.18.7、libavif 1.4.2、cjxl/djxl 0.13.0、Go 1.27.4、Rust nightly 1.102 |

所有进程从一个没有 `.env` 的目录启动、变量全部在命令行显式给出。原因：Go 配置读取 `./.env` 与 `../.env`，worker 的 `dotenvy::dotenv()` 会**逐级向上**查找 `.env`，在仓库子目录里启动会读到根目录指向云服务的 `.env`（命令行变量优先，但没设置的变量会从文件补上）。

素材：`webkit_shoes_srgb.jpg`（600×400，441 KB）、`landscape_mountains_4k.jpg`（3840×2563，3.9 MB），以及由后者放大两倍得到的 `big.jpg`（7680×5126，11.6 MB，用于分片上传与慢任务）。

## 结果

| # | 场景 | 结果 | 关键证据 |
| :--- | :--- | :--- | :--- |
| 1 | 建用户、建 API Key、单次 PUT 与分片上传 | 通过（修复 1 个问题后） | `/api/register` + `/api/login` 建两个用户，`/api/api-keys` 建读写 key 与只读 key。修复前单次 PUT 被 garage 拒绝（`AuthorizationHeaderMalformed … unexpected scope: …/auto/s3/…`），见问题 1。修复后 441 KB 文件 `method=put`，按指南不带额外请求头 PUT 返回 200，`complete` 后 `UPLOADED`；故意给错 `sha256` 时 garage 返回 `InvalidDigest`，签名里的校验和生效。11.6 MB 文件 `method=multipart`，`part_size` 5 MiB × 3 片，`complete` 后 garage 里对象的 SHA-256 与源文件一致 |
| 2 | 普通转码、多输出、`target.max_bytes`；202 + `Location`、ETag/304、`Idempotency-Key`、下载链接 | 通过 | 普通 AVIF：`202`、`Location: /v1/tasks/<id>`、`Retry-After: 2`；完成后 `GET` 带弱 ETag，带 `If-None-Match` 得 `304`；下载 `Content-Disposition: attachment; filename=shoes.avif`，400×267 AVIF。同一 key 同请求重放：`202` + `Idempotent-Replayed: true`、同一任务 ID；同 key 不同请求：`409 error.idempotency.conflict`，库里只有一个任务。多输出（2 源 × `thumb` webp 320 / `full` avif `max_bytes 512000`）：4 个产物名为 `shoes.thumb.webp`、`shoes.full.avif`、`mountains.thumb.webp`、`mountains.full.avif`，下载后格式、尺寸与声明大小全部一致。搜索任务 webp `max_bytes 204800`：产物 201 210 字节，`resolved_options.quality = 19`，`target_met = true`，`compression.output_bytes` 与下载大小一致 |
| 3 | 同源同参数再提交 | 通过 | 普通、搜索、以及写法不同但规范化后相同的参数（显式 `quality: 60`、`fit: inside`）各提交一次，均立即 `COMPLETED`、`attempts = 0`、`output_file.id` 与首次相同，`attempts` 表无新增。复用只在同一 `owner_id` 内 |
| 4 | 公平派发 | 通过 | alice 一次提交 20 个慢 job（2 张大图 × 10 个质量档的 AVIF effort 6），2 秒后 bob 提交 1 个。每秒采样：alice 的 `PROCESSING` 最大为 8；bob 的 job 在提交后 1 秒内派发，在 21 个 attempt 中第 9 个开始执行（只排在 alice 第一轮已入队的 8 个之后），提交后约 35 秒完成，此时 alice 还剩 14 个 |
| 5 | 执行上限 | 通过，但发现问题 3 | `EXECUTION_LIMIT=5s` 重启 orchestrator，提交大图 AVIF effort 10。三个 attempt 依次 `RUNNING` → 约 8–20 秒后 `EXPIRED`（取决于 15 秒的恢复周期），每次写入 `cancel:attempt:<id>`（TTL 24 小时），worker 日志均为 "was cancelled while running"，没有产物上传；job 最终 `FAILED`、`error_code = execution_timeout`、`error_message = attempt exceeded the execution time limit`、`attempts = 3/3` |
| 6 | worker 中途被杀 | 通过 | 只留一个 worker，提交 3840×2563 → AVIF effort 10，attempt `RUNNING`（最后一次心跳 15:46:00，租约到 15:48:00）后 `kill -9` worker，5 秒后重启。消息因 `NOACK` 不会重投；15:48:02 恢复循环把 attempt 置 `EXPIRED`、job 回到 `PENDING`（`execution_timeout`），5 秒后新 attempt 在重启的 worker 上 `RUNNING`，15:48:35 `COMPLETED`，`attempts = 2`，存储里该 job 只有一个产物对象 |
| 7 | 取消、删除 | 通过 | 运行中的任务：`DELETE /v1/tasks/:id` → `409 error.task.conflict`；删除其源文件 → `409 error.file.conflict`；`cancel` 后 job 与 attempt 均 `CANCELLED`，worker 日志 "cancelled while running"；之后删除得 `200`，再 `GET` 得 `404`。删除已完成的多输出任务：`deleted_outputs` 4 个，garage 里 4 个对象全部消失。删除首个普通任务：其产物被后来两个任务复用，出现在 `retained_outputs`，对象仍在，复用它的任务仍能下载 |
| 8 | 存储生命周期 | 通过 | 保留期最小单位是天，直接改 `files.uploaded_at`/`expires_at` 后重启 API 触发一次清理。剩余保留期不足一半（上传 20 天、30 天到期）的产物不再复用、同 `sha256` 的源文件不再秒传。把一个产物和一个源文件的 `expires_at` 改到过去：清理日志 `expired=2 purged=2`，两行变为 `EXPIRED` 且 `purged_at` 已写，garage 里两个对象消失；`GET /v1/tasks/:id` 中该产物 `status = EXPIRED`、无 `download_url`；`GET /api/files/:id/download-url` → `410 error.file.gone`（`details.status = EXPIRED`）；同参数再提交生成新产物；重试源文件已过期的失败任务 → `409 error.file.gone`；用它新建任务 → `400 error.param.invalid` |
| 9 | scope 与吊销 | 通过 | 只读 key：`POST /v1/tasks`、`POST /v1/uploads`、`DELETE /v1/tasks/:id` 均 `403 error.auth.insufficient_scope`、`details.required_scope = tasks:write`，`GET` 正常。bob 的 key 读 alice 的任务 → `404`。会话 cookie 访问 `/v1` → `401`。吊销后同一 key → `401 error.auth.unauthorized`，带 `WWW-Authenticate: Bearer realm="lightyear"` |

## 发现的问题

1. **Go 没有读取 `S3_REGION`（已修复，`3cd2243`）。** `config.Load` 读了其他 `S3_*` 变量，唯独没把 `S3_REGION` 写进 `S3Config.Region`，客户端总按 `auto` 签名。R2 接受 `auto`，所以之前没暴露；garage、MinIO 等按区域校验的存储拒绝所有预签名 URL。补了 `config_test.go`。
2. **`complete` 在对象不存在时返回 500（已修复，`7c48a2a`）。** PUT 没完成或被存储拒绝（例如 `sha256` 不符）后调用 `complete`，`HeadObject` 的 404 变成 `500 error.internal`，`details.reason` 原样回显 S3 响应。现在 `/v1` 与 `/api` 都返回 `409 error.param.invalid`，文件保持 `UPLOADING`，可以重新 PUT；契约已更新，补了测试。
3. **取消与执行上限不会停止正在进行的编码（未修复，roadmap 12.9）。** worker 看到取消标记后不再等待结果、立即去取下一条消息，但 `spawn_blocking` 里的 libvips 编码会跑完再被丢弃（代码注释里写明了这一点，文档里没有）。场景 5 的 job 在 15:42:06 判为 `FAILED`，被放弃的编码又跑到 15:45:28；场景 7 取消的任务同样如此。峰值时一个 worker 上同时有 3 个被放弃的编码（约 870% CPU），新 job 因此变慢，在执行上限较紧时可能连锁超时。修复需要设计决定，见 roadmap。
4. **500 响应回显内部错误（未修复，roadmap 12.10）。** 由问题 2 发现：`internalError` 把 `err.Error()` 放进 `details.reason`，可能包含 S3 响应或 SQL 错误。

## 其他观察（不算问题）

- **新文件的 `expires_at` 最长 10 分钟后才写入**：清理每 10 分钟运行一次时才排期，期间 `GET` 返回的 `expires_at` 为 `null`。交接说明里已提到。
- **并发上限把"已派发未执行"也算在内**：bob 的 job 立即派发，但仍排在 Redis Stream 里 alice 第一轮的 8 条消息之后（约 35 秒）。这是设计如此（上限保证的是"不排在整批之后"），积压时一个用户最多让别人多等一轮。
- **体积目标搜索在预算内取最高质量**：`shoes.full.avif`（`max_bytes 512000`）选了 `quality 100`、4:4:4，产物 463 KB，比 441 KB 的源 JPEG 还大，`compression.larger_than_source = true` 如实标出。
- **时间字段时区不统一**：同一响应里有的时间是 `Z`，有的是 `+08:00`（例如 `download_expires_at`、`revoked_at`）。都是合法的 RFC 3339，客户端按标准解析不受影响。
- **`task doctor` 在本机失败**：Homebrew 的 libarchive 是 keg-only，`pkg-config` 解析 vips 的依赖时找不到 `libarchive.pc`，`--atleast-version` 因此失败（vips 实际是 8.18.7）。把 `$(brew --prefix libarchive)/lib/pkgconfig` 加进 `PKG_CONFIG_PATH` 即可，属于本机环境。

## 修复后的检查

- `go test ./server/...`：全部通过（`TEST_DATABASE_URL` 指向一次性库 `lightyear_e2e_test`）。
- `task check`：通过（这个 worktree 需先在 `web/` 下 `bunx nuxt prepare` 生成 `.nuxt`，否则 `check:web` 因缺少 Nuxt 生成的类型报错）。
- 两处修复都先确认新测试在修复前失败、修复后通过，再用真实链路复测：修复后 garage 接受预签名 URL；对被拒绝的上传调用 `complete` 得 `409`。
- `task test:fixtures`、`task test:consistency` 未跑：修复没有改动图像处理行为。

## 清理

停止全部进程；删除 `lightyear_e2e`、`lightyear_e2e_test` 两个库，清空 Redis 逻辑库 9，删除 garage 桶 `lightyear-e2e` 与它的 key。

## 后续

验证后又修复了四处（同一分支）：

- 问题 3（roadmap 12.9）：每个 job 的计算放进 worker 启动的子进程，取消或超出执行上限时杀掉整个进程组，回收完再取下一条派发。
- 问题 4（roadmap 12.10）：500 响应不再带内部错误，只带 `details.request_id`，错误写进日志。
- 新文件的 `expires_at`：上传 `complete` 与 orchestrator 记录产物时在同一事务里写入，不再等 10 分钟一次的清理。
- 时区：API 进程以 UTC 为本地时区，响应里的时间都以 `Z` 结尾。修复前同一个 API Key 在创建响应里是 `+08:00`、在列表里是 `Z`。
