# 可靠性设计

目标：消息会丢、会重复，worker 会崩溃，但任务状态始终正确，且每个 job 最多发布一个产物。

做法只有一条：**执行所有权只看数据库**。Redis Stream 只负责投递；谁在执行、是否超时、结果算不算数，都由 PostgreSQL 中 job 的当前 attempt 与租约决定。系统保证投递至少一次、结果幂等，不宣称执行恰好一次——同一 attempt 可能被执行多次，但产物按 attempt 隔离，重复执行无害。

## Attempt：派发记录兼 outbox

orchestrator 领取 job 时，在同一个事务中：

1. 锁定 task 与 job（固定顺序 task → job，避免死锁），确认 job 仍为 `PENDING` 且已到 `next_run_at`；
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

worker 每 20 秒发一次 `RUNNING` 心跳，所以 2 分钟的租约能容忍几次心跳丢失。

## 重试

- worker 的错误带 `retryable`。目前只有 `storage_failed`（对象存储读写失败）和租约超时可重试；参数非法、输入不支持、处理失败都直接失败。
- 每个 job 最多 3 次 attempt（`max_attempts`）。重试时 job 回到 `PENDING`，`next_run_at` 按 5 秒起、每次翻倍、上限 5 分钟的间隔推迟。
- 用户可在任务页对失败或取消的 job 手动重试，并可换一份参数。

## 取消

1. API 在事务中把未结束的 job 置为 `CANCELLED`，当前 attempt 置为 `CANCELLED`，然后重新聚合 task。
2. 提交后尽力写入 `cancel:attempt:<attempt_id>`（24 小时过期）。
3. worker 在开始前检查一次，执行中每 3 秒检查一次，发现后放弃执行，不上传、不回报。
4. 即使取消标记没写成功或 worker 没看到，迟到的结果也会因 job 已不是 `PROCESSING` 被忽略。

取消标记按 attempt 而不是 job 设置，因此重试出的新 attempt 不会被旧的取消标记误伤。

## worker 侧

- `XREADGROUP … NOACK`：收到即确认，Redis 中没有待确认列表需要认领；消息丢失由数据库租约兜底。
- 回报结果失败时，worker 会重连并持续重试最多 90 秒；仍失败则放弃，由租约超时处理。
- worker 本身无状态，可以随时增减实例；多个实例同属消费组 `workers`，每条派发只交给其中一个。

## Redis 侧

- `q:image` 与 `q:results` 写入时近似裁剪为 10 万条。
- `q:results` 由 orchestrator 在处理后 `XACK`；某个 orchestrator 崩溃后，其他实例会认领空闲超过 30 秒的未确认结果。
- Redis 数据全部丢失时：未完成的 job 由恢复流程按上表重新派发，已完成的 job 不受影响。
