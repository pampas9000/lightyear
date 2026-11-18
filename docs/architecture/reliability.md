# 可靠性设计

目标：消息会丢、会重复，worker 会崩溃，但任务状态始终正确，且每个 job 最多发布一个产物。

做法只有一条：**执行所有权只看数据库**。Redis Stream 只负责投递；谁在执行、是否超时、结果算不算数，都由 PostgreSQL 中 job 的当前 attempt 与租约决定。系统保证投递至少一次、结果幂等，不宣称执行恰好一次——同一 attempt 可能被执行多次，但产物按 attempt 隔离，重复执行无害。

## 派发顺序与并发上限

orchestrator 每 500 毫秒取一批（最多 20 个）到期 job，在用户之间公平分配：

- **并发上限**：每个用户同时处于 `PROCESSING`（已派发排队中或执行中）的 job 数不超过 `DISPATCH_MAX_PROCESSING_PER_USER`（默认 8）。到上限的用户本轮跳过，有 job 结束后才再领取，不影响其他用户。
- **轮询**：每个用户最多取"剩余名额"个到期 job，按 `next_run_at, id` 排序；然后所有用户的第 1 个排在任何人的第 2 个之前，同一轮次内按到期时间先后。一个用户的大批量积压不会让其他用户的第一个 job 多等一轮以上。
- **索引**：查询先用 `idx_jobs_pending_owner`（`(owner_id, next_run_at, id) WHERE status = 'PENDING'`）跳跃扫描出有待处理 job 的用户，再按用户取到期 job；`PROCESSING` 计数走 `idx_jobs_processing_owner`。代价随"有积压的用户数 × 名额"增长，与积压总量无关（50 万积压 + 2000 个用户约 8 毫秒）。两个部分索引在 `schema.Migrate` 中创建。

## Attempt：派发记录兼 outbox

orchestrator 领取 job 时，在同一个事务中：

1. 取该用户的事务级 advisory lock（`lightyear:dispatch:<owner_id>`），再锁定 task 与 job（固定顺序 task → job，避免死锁），确认 job 仍为 `PENDING` 且已到 `next_run_at`，并重新统计该用户的 `PROCESSING` 数，已到上限则放弃。advisory lock 让多个 orchestrator 实例对同一用户的领取串行，上限不会被并发领取突破；只有领取会取这个锁，且总是最先取，不会形成死锁；
2. 创建 attempt（`QUEUED`），保存完整派发负载与产物 key `outputs/<owner>/<job>/<attempt>.<ext>`；
3. job 置为 `PROCESSING`，`current_attempt_id` 指向新 attempt，`attempts + 1`，租约设为 `StartTimeout`（10 分钟）。

提交后才 `XADD q:image`。发送失败时 attempt 保持未发送状态，由恢复流程补发——attempt 行本身就是 outbox，不需要单独的 outbox 表。

## 结果只认当前 attempt

orchestrator 处理每条结果时锁定 job，只在 job 为 `PROCESSING` 且 `current_attempt_id` 等于结果中的 `attempt_id` 时生效，否则忽略。因此：

- 重试或取消之后才到达的旧结果不会覆盖新状态；
- `COMPLETED` 必须带产物，且产物 key 与 attempt 记录的 key 完全一致，否则视为无效结果丢弃；
- 重复的同一结果在第一次生效后 job 已不再是 `PROCESSING`，第二次自然被忽略。

## 恢复

orchestrator 每 15 秒运行一次恢复：

| 情况 | 判定 | 处理 |
| :--- | :--- | :--- |
| attempt 从未发出 | `QUEUED`、无 `dispatched_at`、创建超过 30 秒 | 补发同一 attempt |
| 已发出但没有 worker 接手 | 租约（`StartTimeout` 10 分钟）到期时 attempt 仍为 `QUEUED` | 原样重发同一 attempt，不消耗重试次数，租约再延 10 分钟 |
| worker 执行中失联 | `RUNNING` 后租约（每次心跳续到 2 分钟后）到期 | attempt 置 `EXPIRED` 并发取消标记；按 `execution_timeout`（可重试）进入重试 |
| 执行超过上限 | 租约到达执行上限（见下） | 同上，错误信息为 `attempt exceeded the execution time limit` |

worker 每 20 秒发一次 `RUNNING` 心跳，所以 2 分钟的租约能容忍几次心跳丢失。

### 执行上限

每个 attempt 从第一次 `RUNNING`（`started_at`）起有绝对上限：普通 job 为 `EXECUTION_LIMIT`（默认 10 分钟），带 `target` 的搜索 job 因为要多轮编码，为 `TARGET_EXECUTION_LIMIT`（默认 30 分钟）。

上限只在处理心跳时生效：心跳把租约续到 `min(现在 + 2 分钟, started_at + 上限)`，绝不越过上限。超过上限后租约自然到期，由恢复循环按上表"worker 执行中失联"同一路径处理（置 `EXPIRED`、写 `cancel:attempt:<id>`、`execution_timeout` 重试）。这样只有一处做状态转换和取消通知；心跳停止时恢复循环本来就要兜底，而心跳仍在时上限也已体现在租约里。代价是最多晚一个恢复周期（15 秒）才发出取消。worker 每 3 秒检查取消标记，看到后按下文"取消"第 3 步停下：计算进程被杀掉，CPU 随即释放，不上传、不回报。

## 重试

- worker 的错误带 `retryable`。目前可重试的有 `storage_failed`（对象存储读写失败）、URL 源下载时的临时错误（`fetch_failed` 中的超时、连接失败、5xx、408、429）和租约超时；参数非法、输入不支持、处理失败、URL 被拒绝（`url_rejected`）都直接失败。URL 源抓取成功后即回报并记入数据库，之后的重试读存好的源文件，不再抓取。
- 每个 job 最多 3 次 attempt（`max_attempts`）。重试时 job 回到 `PENDING`，`next_run_at` 按 5 秒起、每次翻倍、上限 5 分钟的间隔推迟。
- 用户可在任务页对失败或取消的 job 手动重试，并可换一份参数。

## 取消

1. API 在事务中把未结束的 job 置为 `CANCELLED`，当前 attempt 置为 `CANCELLED`，然后重新聚合 task。
2. 提交后尽力写入 `cancel:attempt:<attempt_id>`（24 小时过期）。
3. worker 在开始前检查一次，执行中每 3 秒检查一次。发现后：下载或上传中就直接放弃；计算中则对计算子进程所在的进程组发 `SIGKILL`（连同 `cjxl`），等它被回收、删掉它的临时目录后才返回。不上传、不回报，回收完之前也不读下一条派发，所以被放弃的编码不会和下一个 job 叠在一起占 CPU。从写入标记到计算停止最多约 3 秒。
4. 即使取消标记没写成功或 worker 没看到，迟到的结果也会因 job 已不是 `PROCESSING` 被忽略。

取消标记按 attempt 而不是 job 设置，因此重试出的新 attempt 不会被旧的取消标记误伤。

## worker 侧

- `XREADGROUP … NOACK`：收到即确认，Redis 中没有待确认列表需要认领；消息丢失由数据库租约兜底。
- 回报结果失败时，worker 会重连并持续重试最多 90 秒；仍失败则放弃，由租约超时处理。
- worker 本身无状态，可以随时增减实例；多个实例同属消费组 `workers`，每条派发只交给其中一个。
- 每个 job 的计算（`compute-optimize`，含搜索的多轮编码）在子进程里进行：worker 以 `compute` 参数重新启动自身，经 stdin 传入参数与源图，经 stdout 收回进度与产物；子进程有独立的进程组和 `TMPDIR`。原因是 libvips 的 kill 标志只在像素管线的 tile 之间生效，而 libavif/aom、libheif、libjxl、libwebp 的编码一旦开始就无法中途停止，只能杀进程。附带的效果：libvips 或编码器崩溃只让这一个 attempt 以 `processing_failed` 失败，worker 本身不退出；每个 job 结束后内存归还系统。代价是每个 job 多一次进程启动与 libvips 初始化，在 Apple Silicon 上约 70 ms。

## Redis 侧

- `q:image` 与 `q:results` 写入时近似裁剪为 10 万条。
- `q:results` 由 orchestrator 在处理后 `XACK`；某个 orchestrator 崩溃后，其他实例会认领空闲超过 30 秒的未确认结果。
- Redis 数据全部丢失时：未完成的 job 由恢复流程按上表重新派发，已完成的 job 不受影响。
