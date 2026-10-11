# 改进路线图

本页是架构改造与已知问题的**唯一跟踪处**。其他规范页只描述现状，不再携带审查进度。状态：✅ 已完成 · 🚧 进行中 · ⬜ 未开始。

## 目标架构

```text
API / MCP ─▶ API 服务 ─▶ Postgres（唯一事实来源）◀── Orchestrator（领任务 / 派发 / 收结果 / 恢复）
                                                     │ XADD q:<class>          ▲ q:results
                                                     ▼                         │
                     image worker（Rust + libvips）· 以后：gpu / provider worker

浏览器 ──Web Worker──▶ wasm-vips（同一个 libvips，按需加载 AVIF/HEIF、JXL 模块）
```

- **Postgres 是唯一事实来源**；Redis Stream 只负责投递，worker 不连数据库，按执行类型分池、独立扩缩容。
- **执行所有权只看数据库 lease**：worker 收到即 ACK；没收到 RUNNING 心跳就原样重发同一 attempt，lease 过期就新建 attempt。产物按 attempt 隔离，重复执行无害。
- **一份参数规范 `ImageOptions`**：Web、API、MCP、worker、浏览器共用，只描述输出意图，源图信息由程序探测。规范化与产物键（hash(源 sha256 + 规范化参数)）只在 Go 侧计算，worker 收到的已是规范化参数。
- **前期只做异步**：`POST /v1/tasks` 返回 202，下游轮询或接收 webhook。
- **服务端与浏览器用同一个 libvips**：worker 用 Rust + [`rs-vips`](https://crates.io/crates/rs-vips)，浏览器用 wasm-vips。两端各有一份很薄的 `ImageOptions → libvips` 映射，用同一批素材做一致性测试。

## 已做的决定

| 日期 | 决定 | 依据 |
|---|---|---|
| 2026-10 | 处理核心换成 libvips，退役自研 engine / CLI 编排 | 自研部分重写了 libvips 已有能力，且是缺陷集中处 |
| 2026-10 | worker 保留 Rust，绑定用 `rs-vips` | `libvips` crate 2.3.0 未生成 `jxlsave`、`uhdrload` 等操作；`rs-vips` 0.7（基于 libvips 8.18.1）冒烟测试通过：UltraHDR 缩略图保留 gain map、JXL、10-bit AVIF |
| 2026-10 | 浏览器用 wasm-vips（0.0.19：核心 2.0 MB，AVIF/HEIF 1.25 MB，JXL 0.8 MB，gzip） | 与服务端同一个 libvips；需要跨源隔离，只在沙盒页启用 COOP/COEP（`credentialless`） |
| 2026-10 | 线上无运行：不做灰度、不写数据迁移，直接重建表 | 只有本地开发 |
| 2026-10 | 前期只做异步任务；CDN 式取图、AI 能力以后再加 | 负担小，结构已预留 |
| 2026-10 | Rust 工具链保持 nightly | 项目决定 |

## 执行步骤

### 已完成：P0 止损（旧 Rust 管线退役前）

| # | 项目 | 状态 |
|---|---|---|
| 0.1 | `engine_params.icc_path` / `icc_pathname` 可读取 worker 任意文件：Rust 不再反序列化，Go 入库前只保留已知字段 | ✅ |
| 0.2 | HEIC 解码改为 `heif-dec` 16-bit PNG | ✅ |
| 0.3 | 原生 YUV 直通排除 HEIC | ✅ |
| 0.4 | 沙盒文件选择器移除 AVIF | ✅ |
| 0.5 | 删除浏览器端无效的 JPEG→JXL "直通"及文案 | ✅ |
| 0.6 | 沙盒 WebP 只能无损：已隐藏质量滑块 | ✅ |
| 0.7 | `sharp_yuv` 按构建探测，不支持时去掉参数而不是让任务失败 | ✅ |
| 0.8 | 删除无法编译、无消费方的 `compute-ffi` | ✅ |

### 第 1 步：整理工作区

| # | 项目 | 状态 |
|---|---|---|
| 1.1 | 按主题拆分提交当前工作区（P0 修复单独一个提交） | ✅ |
| 1.2 | `web/tsconfig.tsbuildinfo` 移出版本控制 | ✅ |
| 1.3 | 工具链保持 nightly（`rust-toolchain.toml` 不改），快速开始文档同步更正 | ✅ |
| 1.4 | 确认无误后删除备份分支 `backup/pre-reset-4f4b318` 与对应 stash | ⬜ |

### 第 2 步：参数规范 `ImageOptions`

契约见 [ImageOptions](/contracts/image-options)。

| # | 项目 | 状态 |
|---|---|---|
| 2.1 | 字段与默认值：`format`、`quality`、`lossless`、`effort`、`resize{width,height,fit,enlarge}`、`color`、`dynamic_range`、`metadata`、`chroma`、`bit_depth`（平铺，不嵌套 `advanced`） | ✅ |
| 2.2 | Rust 类型为唯一定义，生成 JSON Schema；TS 类型由 Schema 生成（`task gen:types`），`task check` 检查是否最新 | ✅ |
| 2.3 | Go 类型与校验，与 Rust 跑同一份 fixtures（34 个用例） | ✅ |
| 2.4 | Go 侧规范化与产物键 | ✅ |

### 第 3 步：处理核心 `compute-imageops`（Rust + rs-vips）

| # | 项目 | 状态 |
|---|---|---|
| 3.1 | 新 crate，`build.rs` 用 pkg-config 定位 libvips | ⬜ |
| 3.2 | 解码 + 自动转正、缩放（`thumbnail`）、ICC 保留或转 sRGB、元数据保留/去除、各格式编码 | ⬜ |
| 3.3 | 资源上限：最大像素数、`fail-on`、禁用 untrusted 加载器 | ⬜ |
| 3.4 | 保留 `cjxl` JPEG→JXL 无损旁路（唯一保留的外部 CLI） | ⬜ |
| 3.5 | HDR 保护：libvips 会丢 HDR AVIF 的 CICP，HDR 输入在 `dynamic_range: keep` 下明确报错，`sdr` 下输出 SDR | ⬜ |
| 3.6 | 用 `scripts/fixtures` 素材验证产物：尺寸、位深、色彩标签、ICC、像素差异 | ⬜ |

### 第 4 步：数据模型与 Orchestrator（Go）

| # | 项目 | 状态 |
|---|---|---|
| 4.1 | 统一模型：`tasks` / `jobs(op, options, inputs jsonb)` / `attempts` / `files`，删除旧字段与预分配 JobOutput；直接重建表 | ⬜ |
| 4.2 | 删除 AI provider 层（`providers`、`ProviderInvocation`、Generate/Upscale/Edit、ExecutionPayload 1.1、单条目 executor 注册表） | ⬜ |
| 4.3 | 删除 `jobs` stream 与 `outbox_events`：用 `SKIP LOCKED` 领任务，attempt 行充当 outbox | ⬜ |
| 4.4 | 恢复规则：派出后无 RUNNING 心跳则重发同一 attempt；lease 过期则新建 attempt | ⬜ |
| 4.5 | 取消只保留 `cancel:attempt:<id>` | ⬜ |
| 4.6 | 删除 Go `transcode` 包里的旧参数（`engine`、`engine_params`、`color`、`profile_policy`、`strategy`），改用 `ImageOptions` | ⬜ |
| 4.7 | 产物键命中时任务直接完成 | ⬜ |

### 第 5 步：Worker（Rust）

| # | 项目 | 状态 |
|---|---|---|
| 5.1 | 派发协议 v2：`{job_id, attempt_id, op, options, inputs, output_prefix, deadline}`；结果 `{attempt_id, status, progress, artifacts, error{code,retryable}}`，用 fixtures 做双端契约测试 | ⬜ |
| 5.2 | 消费 `q:image`，收到即 ACK；删除 Lua 锁、XCLAIM 心跳、XAUTOCLAIM | ⬜ |
| 5.3 | 调用 `compute-imageops`，产物写入 `attempts/<id>/` | ⬜ |
| 5.4 | 下载大小上限，按 attempt 检查取消 | ⬜ |

### 第 6 步：Web

| # | 项目 | 状态 |
|---|---|---|
| 6.1 | 参数面板直接编辑 `ImageOptions`；新建任务页与重试抽屉共用一份；不再让用户填源图色彩参数 | ⬜ |
| 6.2 | 任务详情按 `ImageOptions` 展示参数 | ⬜ |
| 6.3 | 沙盒改用 wasm-vips：Web Worker、模块按需加载、沙盒页 COOP/COEP（`credentialless`） | ⬜ |
| 6.4 | 沙盒的 `ImageOptions → wasm-vips` 映射，与服务端做一致性测试 | ⬜ |
| 6.5 | 删除 `compute-wasm` 调用、`@jsquash/jxl` 依赖及 wasm-pack 构建任务 | ⬜ |

### 第 7 步：删除旧代码与工具链

| # | 项目 | 状态 |
|---|---|---|
| 7.1 | 删除 `compute-engine`、`compute-server-adapter`、`compute-wasm`，以及 `compute-types` 里的旧参数类型（`Params`、`LibavifParams`、`LibjxlParams`、`ColorEncoding` 等） | ⬜ |
| 7.2 | 开发环境依赖改为 libvips ≥ 8.17（含 libjxl、libheif、libultrahdr）+ `cjxl`；去掉 `avifenc`/`avifdec`/`heif-enc`/`heif-dec`/`cwebp` | ⬜ |
| 7.3 | Taskfile / `package.json`：去掉 wasm 构建，更新 bootstrap 与检查任务，修正过期端口文案 | ⬜ |
| 7.4 | `scripts/benchmark.ts` 改为产物级一致性测试；缺工具算失败 | ⬜ |

### 第 8 步：文档

| # | 项目 | 状态 |
|---|---|---|
| 8.1 | 删除随旧实现作废的页面：`codec-engines`、`color-pipeline`、`color-roadmap`、`engineering-review`、`limitations`、`metadata`、`contracts/wasm` | ⬜ |
| 8.2 | 新写：`ImageOptions` 契约、`compute-imageops` 设计、HDR 与已知限制 | ⬜ |
| 8.3 | 重写：架构总览、请求链路、可靠性、队列契约、README、快速开始 | ⬜ |
| 8.4 | 更新 `gain-map-research`：libvips 8.17+ 已支持 UltraHDR JPEG | ⬜ |

### 第 9 步：对外 API 与 MCP（只做异步）

| # | 项目 | 状态 |
|---|---|---|
| 9.1 | `/v1`：`POST /v1/tasks` → 202 + `Location` + `Retry-After`；`GET` 支持 ETag/304 | ⬜ |
| 9.2 | API Key + scopes（只存哈希） | ⬜ |
| 9.3 | `Idempotency-Key` | ⬜ |
| 9.4 | webhook：HMAC 签名、指数退避、`webhook_deliveries` | ⬜ |
| 9.5 | URL 作为源：由 worker 拉取并防 SSRF | ⬜ |
| 9.6 | 预签名下载 URL | ⬜ |
| 9.7 | 令牌桶限流、attempt 用量、`usage_daily` | ⬜ |
| 9.8 | MCP：`upload` / `submit_task` / `get_task(wait_seconds)` | ⬜ |

### 以后（结构已预留，暂不排期）

- **CDN 式取图** `/v1/img/{source}/{options}`：有产物就 302，否则返回原图并投递任务。
- **AI 能力**（放大、生图、P 图）：注册表加一行 + 新 worker 池；付费调用以 `attempt_id` 作幂等键，attempt 上记 `remote_ref` 与用量。
- **任务串联**：输入写 `from_job`，上游完成后才派发。
- **HDR / gain map 通道**：libavif / libultrahdr 在容器层处理，元数据对齐 libvips `gainmap-data`；libvips 支持 AVIF gain map 后可整体删除。
- **自动扩缩容**：按各 stream 积压量（KEDA Redis Streams 伸缩器）。
