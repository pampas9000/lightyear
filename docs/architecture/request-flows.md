# 请求链路

两条执行路径：服务端异步任务、浏览器本地沙盒。两者使用同一份 `ImageOptions` 和同一个 libvips。

## 服务端异步任务

```text
web ─▶ Nitro 代理 ─▶ API ─────────────▶ PostgreSQL（task + jobs，PENDING）
                                              │ 每 500 ms 领取到期 job
                                        Orchestrator ── 写 attempt（QUEUED）──▶ XADD q:image
                                                                                   │
                                                          image worker ◀──────────┘ XREADGROUP NOACK
                                                            │  RUNNING 心跳（每 20 秒）
                                                            │  下载源图 → compute-imageops → 上传产物
                                                            ▼
                                        Orchestrator ◀── q:results（COMPLETED / FAILED）
                                              │ 校验 attempt 与产物 key，建产物文件，聚合 task
web 每 2 秒轮询 GET /api/tasks/:id ◀──────────┘
```

1. **上传**：浏览器把文件分片直传 S3，完成后文件为 `UPLOADED`，并出现在草稿列表。
2. **提交**：`POST /api/tasks` 带 `items`（文件 ID）和 `options` 或 `workflow_id`。API 在一个事务中：严格解析并规范化参数、锁定源文件、删除对应草稿、按源文件逐个创建 job（`PENDING`）；已有相同产物的 job 直接完成。
3. **派发**：orchestrator 每 500 ms 查询到期的 `PENDING` job（每批 20 个），逐个在事务中锁定 task 与 job、校验源文件仍存在、创建 attempt 并把 job 置为 `PROCESSING`；提交后 `XADD q:image`，成功则记录 `dispatched_at`。
4. **执行**：worker 以 `NOACK` 读取，收到即视为确认。先检查取消标记，再发 `RUNNING`，然后下载源图、调用 `compute-imageops`、把产物写到派发里给定的 `output_key`，最后发 `COMPLETED` 或 `FAILED`。执行期间每 20 秒发一次心跳、每 3 秒检查一次取消。
5. **收结果**：orchestrator 从 `q:results` 读取（消费组 `orchestrators`，处理后 `XACK`）。只接受 job 当前 attempt 的结果；`COMPLETED` 时校验产物 key 与 attempt 记录一致，再创建产物文件、完成 job、重新聚合 task。
6. **展示**：前端轮询任务详情，显示进度、错误诊断与下载入口；下载时再申请预签名 URL。

失败、超时、取消与消息丢失的处理见[可靠性设计](/architecture/reliability)。

## 浏览器沙盒

```text
页面 ──postMessage──▶ Web Worker（vips.worker.ts）──▶ wasm-vips
  ▲                         │  lib/vips-ops.ts：与 compute-imageops 逐条对应
  └──── 产物字节（transfer）──┘
```

1. 首次处理时才创建 Web Worker 并加载 wasm-vips 核心与 AVIF/HEIF、JXL 模块。
2. Worker 启动时与服务端一样拦截 untrusted 加载器（放行 JXL）、关闭缓存。
3. 读入文件后先 `probe`（格式、宽高），再按当前 `ImageOptions` 调用 `process`；产物以 transferable 方式回传，不复制。
4. 页面需要跨源隔离（`SharedArrayBuffer`），全站响应头为 COOP `same-origin` + COEP `credentialless`。

浏览器与服务端的差异（HEIC、带 Exif 的 JXL、cjxl、Safari）见[已知限制](/compute/limitations#浏览器沙盒)。
