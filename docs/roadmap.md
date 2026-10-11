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
| 3.1 | 新 crate，`build.rs` 用 pkg-config 定位 libvips（≥ 8.17） | ✅ |
| 3.2 | 解码 + 自动转正、缩放（`inside`/`cover`/`fill`、`enlarge`）、ICC 保留或转 sRGB、元数据保留/去除、六种格式编码 | ✅ |
| 3.3 | 资源上限：解码前按像素数拒绝；`fail_on=error`；拦截 libvips 标记为 untrusted 的加载器（PDF、SVG、OpenEXR、FITS 等），单独放行 JXL | ✅ |
| 3.4 | `cjxl` JPEG→JXL 无损旁路（逐字节可还原，已用 `djxl` 验证）；缺 `cjxl` 时退回像素级无损 | ✅ |
| 3.5 | HDR 保护：PQ/HLG 源图（HEIF/AVIF `nclx`、PNG `cICP`）明确拒绝；UltraHDR JPEG 的 gain map 在 JPEG 输出时保留，`sdr` 或其他格式输出基础 SDR 图 | ✅ |
| 3.6 | 全部素材 × 六种格式扫描（`task test:fixtures`）：只有带 PQ/HLG 标记的 4 个 HDR 素材被拒绝，其余全部成功 | ✅ |
| 3.7 | PQ/HLG 源图转 SDR（色调映射）与 AVIF/HEIC gain map 保留 | ⬜ 归入以后的 HDR 通道 |

实现中确认的 libvips 行为（8.18）：

- `thumbnail` 总是输出 8-bit，所以 8-bit 源图走 `thumbnail`（可在解码时缩小），更高位深的源图走"加载 → 转正 → 缩放 → ICC 转换"，保持 16-bit。
- libvips 读取 HEIF/AVIF 与 PNG 时不保留 CICP，PQ/HLG 像素会被当成 sRGB 写出，因此必须在入口拒绝。
- libjxl 被 libvips 标记为 untrusted，启用 untrusted 拦截后需显式放行 JXL。
- 选项名写错时 libvips 只打 warning 不报错，每个编码选项都需要测试验证效果。


### 第 4 步：数据模型与 Orchestrator（Go）

| # | 项目 | 状态 |
|---|---|---|
| 4.1 | 统一模型：`tasks` / `jobs(operation, options, source_file_id, output_file_id)` / `attempts` / `files`；输出在完成时才建文件记录。单一来源先用 `source_file_id` 列，多输入的 AI 操作出现时再加 | ✅ |
| 4.2 | 删除 AI provider 层、`execution`、`operation`、`transcode` 旧参数包，以及未被引用的 gorm/gen 生成代码 | ✅ |
| 4.3 | 删除 `jobs` stream 与 `outbox_events`：orchestrator 每 500 ms 从数据库领取到期任务，attempt 行保存派发内容并充当 outbox | ✅ |
| 4.4 | 恢复：未发出的 attempt 30 秒后补发；已发出但 10 分钟无 RUNNING 心跳则原样重发；运行中 lease（2 分钟，心跳续期）过期则放弃并重试 | ✅ |
| 4.5 | 取消只用 `cancel:attempt:<id>`，提交后尽力通知，迟到的结果因任务已不在 PROCESSING 而被忽略 | ✅ |
| 4.6 | Task/Workflow API 改收 `options`（严格解析、规范化） | ✅ |
| 4.7 | 同一用户、同一源文件、同一规范化参数直接复用已有产物 | ✅ |
| 4.8 | `cmd/migrate -reset`：删除全部应用表（含旧版本遗留表）后重建，仅限开发 | ✅ |

### 第 5 步：Worker（Rust）

| # | 项目 | 状态 |
|---|---|---|
| 5.1 | 派发/结果协议 v2，Go 与 Rust 共用 `docs/contracts/fixtures/{dispatch,results}-v2.json` | ✅ |
| 5.2 | `XREADGROUP … NOACK` 收到即确认；删除 Lua 锁、XCLAIM 心跳、XAUTOCLAIM、结果缓存 | ✅ |
| 5.3 | 调用 `compute-imageops`，产物写入 attempt 专属的 key | ✅ |
| 5.4 | 下载大小上限（默认 200 MB）与像素上限可通过环境变量配置；每 3 秒检查取消、每 20 秒心跳 | ✅ |
| 5.5 | 修复：Redis 默认 500 ms 响应超时会让阻塞读不断超时重连，改为 30 秒；`BUSYGROUP` 改按错误码判断（否则 worker 重启即退出） | ✅ |
| 5.6 | 端到端验证（本地 Postgres + 内存版 Redis/S3，真实 orchestrator 与 worker）：缩放、HDR 拒绝、16-bit→10-bit AVIF、worker 宕机期间派发、JPEG→JXL 逐字节还原 | ✅ |

### 第 6 步：Web

| # | 项目 | 状态 |
|---|---|---|
| 6.1 | `ImageOptionsForm` 取代 `TranscodeSettings`；新建任务页与重试抽屉共用；不再让用户填源图色彩参数，也不再编造源图探测结果 | ✅ |
| 6.2 | 任务列表、详情、错误诊断改用 `options`、`source_file`、`attempts`；新增 `unsupported_input`、`processing_failed`、`execution_timeout` 的诊断文案 | ✅ |
| 6.3 | 沙盒改用 wasm-vips（Web Worker、首次使用时加载）；全站 COOP `same-origin` + COEP `credentialless`，保证客户端路由进入沙盒时仍是跨源隔离状态 | ✅ |
| 6.4 | `lib/vips-ops.ts` 与 `compute-imageops` 逐条对应；在浏览器中验证 AVIF/JXL/WebP/JPEG/PNG 编码与缩放 | ✅ |
| 6.5 | 删除 `compute-wasm`、`@jsquash/jxl`、wasm-pack 构建任务 | ✅ |
| 6.6 | 已知限制：Safari 不支持 COEP `credentialless`，页面无法跨源隔离，沙盒会提示不可用；浏览器端没有 cjxl，JPEG→JXL 无损会重新编码像素；wasm-vips 没有 HEVC，HEIC 既不能读也不能写（沙盒已不提供 HEIC 输出）；wasm-vips 的 JXL 解码器读不了带 Exif/XMP box 的文件 | — |
| 6.7 | 服务端与浏览器的自动化一致性测试 `task test:consistency`：22 组素材 × 参数，比较格式、尺寸、通道、位深、ICC/Exif/gain map 与像素平均差；上述浏览器缺口作为预期失败登记，wasm-vips 补上后测试会提示更新 | ✅ |

### 第 7 步：删除旧代码与工具链

| # | 项目 | 状态 |
|---|---|---|
| 7.1 | 删除 `compute-engine`、`compute-server-adapter`、`compute-wasm` 与 `compute-types` 旧参数类型；工作区只剩 `types`、`imageops`、`worker` | ✅ |
| 7.2 | 开发环境依赖改为 libvips ≥ 8.17（含 libjxl、libheif、libultrahdr）+ `cjxl`；去掉 `avifenc`/`avifdec`/`heif-enc`/`heif-dec`/`cwebp`、`wasm-pack`（`task doctor` 已更新，文档待第 8 步） | 🚧 |
| 7.3 | Taskfile / `package.json`：去掉 wasm 与 gorm/gen 任务，`build:server` 产出 `migrate` | ✅ |
| 7.4 | 删除只读文件头的 `scripts/benchmark.ts`；产物级检查由 `compute-imageops` 测试、`test:fixtures`、`test:consistency` 承担；`cjxl`/`djxl` 缺失时测试失败而不是跳过 | ✅ |
| 7.5 | 重写素材生成脚本（magick + vips + avifenc）：根源是 `checker_rb_444.png` 生成成了灰度图，连带 JPEG/AVIF/WebP/JXL/HEIC 衍生素材都没有色度；删除与新管线无关或名不副实的素材（limited range、矩阵、"4000 nits" 白图、损坏的双层 JPEG 等），HDR 拒绝新增 HLG 素材 | ✅ |

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
