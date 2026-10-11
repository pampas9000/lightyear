# 可靠性设计

分布式队列的重复投递、Worker 崩溃、节点扩缩容都会造成同一作业被多次处理。系统在 Redis Streams 之上叠加了：**事务 Outbox**、**原子抢占**、**Attempt ID 幂等**、**Attempt 产物隔离**、**数据库与 PEL 双租约**、**PEL 自动认领**。

这些机制提供 at-least-once 消息投递和业务结果幂等，不宣称本地执行 exactly-once；外部付费 Provider 也不宣称 exactly-once 提交。

## 1. 原子抢占与 Attempt ID

- API 创建 `Job` 时状态为 `PENDING`，并在同一事务写入 Outbox。
- Orchestrator 派发前锁定父任务和 Job，创建持久化 `JobAttempt`，每个 generation 生成 UUID 作为 `attempt_id`：

```sql
UPDATE jobs
SET status = 'PROCESSING', attempt_id = $1, updated_at = now()
WHERE id = $2 AND status = 'PENDING';
```

- 受影响行数为 0 表示已被其他实例处理，直接跳过并 `XACK`。
- `ComputePayload` 与 `ResultPayload` 都携带 `attempt_id`（见[队列契约](/contracts/queue)）。

## 2. 结果幂等校验

Orchestrator 收到结果时先比对结果中的 `attempt_id` 与数据库当前值：

- 不一致（旧尝试姗姗来迟）→ 丢弃并 `XACK`，防止历史结果覆盖最新状态。
- 一致 → 更新 `Job`/`File`，锁定父 `Task` 行重算聚合状态。
- 心跳（`PROCESSING`）同样校验 `attempt_id`，只用于续租，不改业务状态。

Worker 只能写入 `attempts/<attempt-id>/...`。Orchestrator 只接受当前 attempt manifest 中精确匹配的 artifact，旧 attempt 的结果和路径不会更新文件引用。

## 3. 租约超时恢复（Stale Recovery）

Orchestrator 内常驻 `runStaleJobRecovery`，每 **30 秒**扫描一次：

| 场景 | 判定 | 动作 |
| :--- | :--- | :--- |
| `PENDING` 长时间未派发 | 超过 **2 分钟** | 重新写入任务流 |
| `PROCESSING` 排队未开始且 lease 过期 | 超过 **5 分钟** | 重发同一 attempt，不增加重试次数 |
| `PROCESSING` 已开始但 lease 过期 | lease 到期 | 取消旧 attempt，生成新 `attempt_id`，`retry_count + 1` |
| 重试耗尽 | `retry_count >= max_retries` | 标记 `FAILED`（`error_code=max_retries_exceeded`），产物文件标记 `FAILED` |

Worker 心跳同时续 Redis PEL 所有权和数据库 lease；结果流心跳只用于更新进度和服务端租约。

## 4. PEL 自动认领（XAUTOCLAIM）

- Orchestrator 与 Worker 启动时用 `hostname + pid + random` 生成唯一消费者名，支持随时扩缩容。
- 消费循环**先 `XAUTOCLAIM` 再 `XREADGROUP`**：
  - Go Orchestrator：认领空闲超过 **30 秒**的 PEL 消息
  - Rust Worker：认领空闲超过 **60 秒**的 PEL 消息
- 崩溃节点遗留的未 `XACK` 消息会被存活节点接管，不会死锁。

Worker 心跳通过 Lua 校验 lock token 与当前 PEL consumer，并用 `XCLAIM` 将消息续回当前 consumer；旧 consumer 无法续租或完成。即使重复投递，同一 attempt 也只有一个 Redis lock 持有者。

## 5. 失败重试与取消

- Worker 结果携带 `retryable`；Orchestrator 对可重试失败自动重新入队，`retry_count` 默认上限 3。
- 错误分类（`compute/worker/src/error.rs`）：`invalid_request`、`source_unavailable`、`encode_failed`、`storage_failed`、`internal_error`。
- 新任务取消使用 `transcoder:cancel:attempt:<attempt-id>`，旧协议仍兼容 job key。这样重试不会被上一 attempt 的取消标记误伤。
- Worker 在执行前、心跳与完成前检查取消，并每 3 秒轮询以放弃正在等待的工作；外部 Provider 取消不会伪造远端未提交事实。

## 6. 外部 Provider

生图、放大和编辑图的 Provider 提交由 `ProviderInvocation` 记录：

- `(job_id, generation)` 唯一；
- 提交前先把 `PREPARED` 原子切换为 `SUBMITTING`；
- 超时或连接断开进入 `UNKNOWN`，必须通过 Provider `Lookup` 对账；
- 只有权威确认“未接受”才能回到 `PREPARED`；
- 已提交或未知状态不会被自动 retry/manual retry 绕过。

## 7. 配套的运维细节

- 流长度近似裁剪（`MAXLEN ~ 100000`），避免无限增长。
- S3 上传支持 SHA-256 秒传；未完成分片与陈旧草稿由 API 侧每小时清理，清理前会保护所有旧字段和 generalized input/output 引用。
- Worker 出错时结果仍会写回；Redis 发布失败时保留 attempt lock、缓存结果和 PEL，恢复后继续完成。
