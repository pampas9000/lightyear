# 请求链路

系统有两条执行路径：服务端异步任务流、浏览器 WASM 本地流。

## 1. 服务端异步任务流（默认主链路）

适合长任务、需要系统级工具或集群扩容的场景。

```text
web ─▶ Nitro 代理 ─▶ Go API ─▶ PostgreSQL + Outbox
                                     │
                        Outbox relay ─▶ transcoder:jobs:stream
                                     │
                        Go Orchestrator（原子抢占 / attempt_id）
                                     │
                          transcoder:compute:stream
                                     │
                          Rust compute-worker ─▶ S3（下载/上传）
                                     │
                           transcoder:results:stream
                                     │
                    Go Orchestrator（幂等校验 / 状态聚合）
                                     │
                     web 轮询 /api/tasks/:id 获取进度与产物
```

具体步骤：

1. 用户上传文件（分片预签名直传 S3）后提交任务；API 在一个事务中写入参数快照、输入/输出关系和 Outbox 事件。
2. Outbox relay 发布 `job_id` 或 executor payload；发布失败会持久化重试，语义是 at-least-once。
3. Orchestrator 消费任务流，在数据库事务中原子条件更新 `PENDING → PROCESSING`，生成 `JobAttempt` 和 `attempt_id`，再通过 Outbox 写入对应 executor stream。
4. Rust Worker 消费 compute 流：从 S3 下载输入、调用 `compute-server-adapter::run_transcode`、上传到 `attempts/<attempt-id>/...`，把带 artifact manifest 的结果写入结果流；执行期间续租 Redis PEL 和数据库 lease。
5. Orchestrator 校验 `attempt_id`、artifact file ID 与 attempt manifest 后才发布 `File.Path`，再锁定父 `Task` 行重算聚合状态。
6. 前端轮询 `/api/tasks/:id`（2 秒间隔）展示进度、错误与下载入口。

这条链路的关键点：

- Worker 是**无状态纯计算器**，不连接数据库；所有状态判定集中在 Go 侧
- 计算负载与结果都携带 `attempt_id`，旧尝试的结果会被丢弃（幂等）
- 失败可自动重试（`retryable` + `max_retries`，默认 3），超时由租约恢复接管；排队未启动的 attempt 会重发同一 attempt，不会无谓消耗重试次数
- 生图、放大、编辑图使用同一 `Job`/`JobInput`/`JobOutput` 模型；当前没有安装具体 AI executor 时会明确拒绝，不会生成无法执行的任务

## 2. 浏览器 WASM 本地流

适合轻量、隐私敏感、高实时场景，零服务端开销。

```text
主线程 UI ──postMessage──▶ Web Worker ──▶ compute-wasm ──▶ compute-engine
    ▲                          │
    └───── Blob / 像素数据 ─────┘
```

1. 前端按需动态 `import()` 加载 Worker 与 WASM（`web/app/workers/compute.worker.ts`、`web/app/composables/useComputeWasm.ts`），避免首屏体积膨胀。
2. 用户拖入文件并调整参数（目标格式、质量、缩放、旋转、滤镜、色彩）。
3. Worker 调用 WASM 导出：`browser_convert` / `browser_convert_advanced` / `browser_edit_image` / `browser_edit_to_raw_rgba` / `browser_decode_raw_pixels` / `browser_get_metadata`。
4. 图像处理全程在 WASM 线性内存中以 TypedArray 流转，不经过 DOM Canvas；主线程只负责渲染与交互。
5. JXL 目标先由 `compute-wasm` 解码为像素，再交给 `@jsquash/jxl` 编码；它只接受像素输入，浏览器端没有 JPEG 无损重构（该能力仅在服务端 `cjxl` 路径中提供）。

与服务端的画质对齐原则（避免“浏览器端二等公民”）：

- **零 Canvas 中转**：Canvas 会强制 8-bit / sRGB / 预乘 Alpha，全链路禁用
- **位深与抖动**：保留 10/16-bit 容器；降 8-bit 时使用蓝噪声 64×64 抖动
- **传递函数感知**：Resize / Blur 依据源 `TransferCharacteristics` 动态线性化
- **自适应抽样**：UI/文字/线条走 4:4:4，连续色调照片走 4:2:0
- **广色域限制**：浏览器编码器无法写入 ICC/CICP，广色域源图**原样输出不带标识的像素**，UI 提示可能偏饱和；保留需求走服务端。详见[已知限制](/compute/limitations)

## 请求生命周期速览

| 阶段 | 责任方 | 产物 |
| :--- | :--- | :--- |
| 采集意图 | `web` | 上传句柄 + 任务请求 DTO |
| 归一化 | `server` | 参数校验、`Job` 持久化、队列负载 |
| 执行 | `compute-worker` | 产物文件 + 状态结果 |
| 分发 | `server` / `web` | 预签名下载 URL + 任务状态 |

跨边界的字段级说明见[接口契约](/contracts/)。
