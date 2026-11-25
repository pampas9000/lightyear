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
- **一份参数规范 `ImageOptions`**：Web、API、MCP、worker、浏览器共用，只描述输出意图，源图信息由程序探测。规范化与产物键（hash(源文件 ID + 规范化参数)）只在 Go 侧计算，worker 收到的已是规范化参数。
- **前期只做异步**：`POST /v1/tasks` 返回 202，下游轮询或接收 webhook。
- **服务端与浏览器用同一个 libvips**：worker 用 Rust + [`rs-vips`](https://crates.io/crates/rs-vips)，浏览器用 wasm-vips。两端各有一份很薄的 `ImageOptions → libvips` 映射，用同一批素材做一致性测试。

## 已做的决定

| 日期 | 决定 | 依据 |
|---|---|---|
| 2026-10 | 处理核心换成 libvips，退役自研 engine / CLI 编排 | 自研部分重写了 libvips 已有能力，且是缺陷集中处 |
| 2026-10 | worker 保留 Rust，绑定用 `rs-vips` | `libvips` crate 2.3.0 未生成 `jxlsave`、`uhdrload` 等操作；`rs-vips` 0.7（基于 libvips 8.18.1）冒烟测试通过：UltraHDR 缩略图保留 gain map、JXL、10-bit AVIF |
| 2026-10 | 浏览器用 wasm-vips（0.0.19：核心 2.0 MB，AVIF/HEIF 1.25 MB，JXL 0.8 MB，gzip） | 与服务端同一个 libvips；需要跨源隔离，全站启用 COOP/COEP（`credentialless`），客户端路由进入沙盒时才是隔离状态 |
| 2026-10 | 线上无运行：不做灰度、不写数据迁移，直接重建表 | 只有本地开发 |
| 2026-10 | 前期只做异步任务；CDN 式取图、AI 能力以后再加 | 负担小，结构已预留 |
| 2026-10 | Rust 工具链保持 nightly | 项目决定 |
| 2026-10 | `dynamic_range=keep` 在输出格式无法携带 gain map 时输出 SDR 基础图，而不是报错 | 否则最常见的"手机照片转 AVIF"默认会失败；PQ/HLG 源图仍一律拒绝 |
| 2026-10-12 | 浏览器沙盒暂不支持 HEIC（读写都不做），HEIC 只走服务端 | 官方 wasm-vips 以 `WITH_LIBDE265=OFF` 构建；自编带 libde265 的 wasm-vips 需维护构建且有 HEVC 专利风险；libheif-js 只出 8-bit，ICC/Exif/XMP 要自己从 ISOBMFF 解析透传，与服务端难以一致 |
| 2026-10-27 | 编码平时走 libvips；服务端 AVIF 改为直接调用 libavif。默认速度档跟随实际调用的编码器：走 libvips 用 libvips 默认，走 libavif 用 libavif 默认（speed 6），走 libjxl 用 libjxl 默认（effort 7）；用户未填 `effort` 时不传 | [编码器对比](/engineering-notes/encoder-comparison)：JXL 经 libvips 与 `cjxl` 逐字节相同，没有理由绕开；AVIF 照片与 `avifenc` 持平，但 libvips `heifsave` 固定关闭 intrabc，截图大 13%–40%。默认值不自造映射，与所用库的文档一致 |
| 2026-11-13 | 压缩能力不进 `ImageOptions`，而是在 `image.convert` 上与 `options` 并列加可选 `target`（`max_bytes` / `min_ssimu2`）；格式必须由用户指定，不做多格式择优，按 `Accept` 协商留给以后的 CDN 式取图；搜索放在 `compute-imageops` 之外的新 crate（`compute-optimize`），结果回传 `resolved_options` | `ImageOptions` 保持单轮确定性指令，浏览器沙盒与 `test:consistency` 不受影响；最终产物必须与 `process(源图, resolved_options)` 逐字节相同 |
| 2026-11-13 | 一次任务支持"一份文件多种输出"（`outputs` 数组），每份输出对应一个普通 `image.convert` job，预设 `Workflow` 同步保存整组输出 | 复用现有产物复用、单 job 重试、取消、`PARTIALLY_FAILED` 聚合与并行 worker；单源图重复解码（主要是 HEIC）初期可接受，需要时再做单次解码多输出合并 |
| 2026-11-13 | `target` 规则：给了 `quality`、`distance` 或 `lossless: true` 时不搜索、只检查；`format: png` 带 `target` 报 400；`max_bytes` 与 `min_ssimu2` 同在且都能满足时取刚达标的最小体积，冲突时体积优先；达不到目标时交付最小结果并标 `target_met: false`；`larger_than_source` 无条件按字节对比打标，不退回原图 | 避免隐式退回原图泄露默认 `strip` 的元数据或违背格式/缩放意图；保证与 `ratio`、`saved_ratio` 口径一致 |
| 2026-11-13 | 队列协议在 `v2` 上新增可选字段（`target`、`resolved_options`、`target_met`、`achieved`、`encode_rounds`、`compute_ms`）；第 1 期搜索任务共用 `q:image`，`Attempt` 记队列名、编码轮数与耗时 | 尚无线上运行，可直接在 `v2` 上向后兼容扩字段；`Attempt` 已按次记录队列，第 2 期做 `min_ssimu2` 或出现队头阻塞时只需按配置改投独立队列 |
| 2026-11-18 | 计量单位是"处理单位"，一次成功处理按实际 `encode_rounds` 计（普通转换为 1）；创建时按估算预占（体积目标搜索 7、JXL 搜索 9、其余 1，给了 `quality`/`distance`/`lossless` 的目标只检查，按 1），完成后按实际轮数结算 | 编码是主要成本，搜索的成本随轮数变化，固定按 8 算会多收或少收；预占让并发提交和长时间运行的搜索也受上限约束 |
| 2026-11-18 | 复用已有产物、失败的 attempt、URL 下载都记录但不计单位；下载的文件计入存储 | 复用和失败没有给用户带来新产物；下载成本主要是存储，已由存储配额覆盖 |
| 2026-11-18 | 用量记成一事件一行的 `usage_records`，`(attempt_id, kind)` 唯一、无外键，暂时永久保留；不做按天汇总表 | 能幂等写入、能对账；按天和按月都是查询时汇总，量大了再加汇总表 |
| 2026-11-18 | 配额只按 `User`，不引入组织 / 项目层级；全局默认值加按用户覆盖列（存储 10 GiB、每月 10000 单位，本地开发同样启用），月份按 UTC 自然月，job 算在完成的月份 | 目前没有多人协作的需求，加层级前先看实际用法 |
| 2026-11-18 | 超出配额返回 `403 error.quota.exceeded`（`details` 带 `quota`、`limit`、`used`、`reserved`、`requested`），限流返回 `429 error.rate_limit.exceeded` + `Retry-After`；已接受的任务照常跑完 | 配额不会因等待而恢复，限流等几秒即可，客户端需要区分；中途停掉已接受的任务对用户更糟 |
| 2026-11-18 | 限流按用户（不是按 key）、按请求次数，只限 `/v1` 的写入口；Redis 只计数，不可用时放行 | 防止一个用户换 key 绕过；用量的事实来源是 Postgres，限流失效不影响计量和配额 |
| 2026-11-18 | 存储配额是软限制：上传按"已用 + 申报大小"检查，产物不检查，已满后拒绝新的上传与任务 | 产物大小在处理前未知；中途因存储失败的任务比略超配额更难处理 |
| 2026-11-18 | MCP 只支持 API Key（`Authorization: Bearer`），OAuth 另立 9.12 | 主流 Agent / Harness 都能给 MCP 服务器配置请求头，不支持的是少数；API Key 已有 scope、吊销与只存哈希，OAuth 要做授权服务器、同意页与客户端注册，等有需求再做 |
| 2026-11-18 | MCP 用官方 Go SDK，挂在 API 进程的 `/mcp`，工具直接调用 task / upload 服务与 `/v1` 的错误映射，不另起进程、不加业务逻辑 | SDK 负责协议版本协商与 JSON-RPC；鉴权用它的 `RequireBearerToken` 接 apikey 服务；与 `/v1` 共用限流桶、配额与幂等键，只是另一个入口 |
| 2026-11-18 | MCP 无状态、只回 JSON（不用 SSE、不做进度通知与 tasks 扩展）；`get_task` 的 `wait_seconds`（0–30，默认 0）在服务端每秒读一次库，任务结束或等满即返回 | 多实例不需要会话粘滞；30 秒低于常见客户端的工具调用超时；等待的代价只是每秒一次按主键查询 |
| 2026-11-18 | MCP 工具结果用 `structuredContent` + `outputSchema`（即 `/v1` 的表示）并附 JSON 文本；`options` 的 Schema 嵌入生成的 ImageOptions JSON Schema（Go 侧嵌入副本，`task gen:types` 同步、`task check` 检查），输入 Schema 去掉可为 `null` 的类型数组与 Rust 整数 `format` | 参数规范只有一份；单一类型方言（如 Gemini 函数声明）的客户端也能用 |
| 2026-11-18 | MCP 不提供 resources 与 prompts；错误作为工具结果（`isError` + `/v1` 的 `code`、`message`、`details`，限流的等待秒数放在 `details.retry_after`），不用 JSON-RPC 错误 | 三个工具覆盖整个流程；工具结果里的错误 Agent 能读到并自行处理，JSON-RPC 错误多半只显示给用户 |
| 2026-11-18 | MCP 没有上传 complete 这一步：`submit_task` 遇到 `UPLOADING` 的文件时从存储确认对象（分片须从 1 连续且总大小等于申报值）再完成 | 少一次工具调用，Agent 也不必记录分片 ETag；对象不完整时返回 `error.param.invalid`，补传后重提即可 |
| 2026-11-23 | 把 outbox 从 `attempts` 拆成独立的 `outbox_messages`：首次派发和 `StartTimeout` 重发都在事务中写一条消息，由每秒运行的投递循环补发；`attempts` 删掉 `dispatched_at`，保留 `payload` 作为该次执行派发内容的记录（取代 4.3 中"attempt 行充当 outbox"） | 一个 attempt 只有一个 `dispatched_at`，表示不了"又要发一次但还没发出"，所以重发不经过 outbox，一次失败就要再等 10 分钟；拆开后两类发送有同样的保证，发送记录可查，未发送消息的扫描与 attempts 的历史量无关，以后别的消息（如 webhook）可以复用同一条路径 |

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
| 1.4 | 确认无误后删除备份分支 `backup/pre-reset-4f4b318` 与对应 stash | ✅ |

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
| 3.4 | `cjxl` JPEG→JXL 无损旁路（逐字节可还原，已用 `djxl` 验证）；`cjxl` 改为 worker 必装，启动时检查；`metadata=strip` 也走旁路（删元数据、不带重构数据）；UltraHDR 源也走旁路（gain map 在重构数据中保留，`dynamic_range=sdr` 时不带）；服务端表单提示会走哪条路径 | ✅ |
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
| 7.2 | 开发环境依赖改为 libvips ≥ 8.17（含 libjxl、libheif、libultrahdr）+ `cjxl`/`djxl` + Node ≥ 23.6；去掉 `avifenc`/`avifdec`/`heif-enc`/`heif-dec`/`cwebp`、`wasm-pack`；`task doctor` 检查版本，缺失即失败 | ✅ |
| 7.3 | Taskfile / `package.json`：去掉 wasm 与 gorm/gen 任务，`build:server` 产出 `migrate` | ✅ |
| 7.4 | 删除只读文件头的 `scripts/benchmark.ts`；产物级检查由 `compute-imageops` 测试、`test:fixtures`、`test:consistency` 承担；`cjxl`/`djxl` 缺失时测试失败而不是跳过 | ✅ |
| 7.5 | 重写素材生成脚本（magick + vips + avifenc）：根源是 `checker_rb_444.png` 生成成了灰度图，连带 JPEG/AVIF/WebP/JXL/HEIC 衍生素材都没有色度；删除与新管线无关或名不副实的素材（limited range、矩阵、"4000 nits" 白图、损坏的双层 JPEG 等），HDR 拒绝新增 HLG 素材 | ✅ |

### 第 8 步：文档

| # | 项目 | 状态 |
|---|---|---|
| 8.1 | 删除随旧实现作废的页面：`codec-engines`、`color-pipeline`、`color-roadmap`、`engineering-review`、`metadata`、`gain-map`、`contracts/wasm`、`fidelity-benchmark` 与 4 篇引擎迁移笔记；删除 Web 端 193 个不再使用的翻译条目 | ✅ |
| 8.2 | 新写：`ImageOptions` 契约、[处理核心](/compute/imageops)、[HDR 与已知限制](/compute/limitations)、[素材与测试](/quality/fixtures)；`dynamic_range` 的说明改为与实现一致 | ✅ |
| 8.3 | 重写：架构总览、请求链路、可靠性、契约总览、队列契约、HTTP API、快速开始、首页，以及根目录、`compute`、`server`、`web` 的 README | ✅ |
| 8.4 | 更新 `gain-map-research`：libvips 8.17+ 已支持 UltraHDR JPEG（实测 gain map 随缩放同步缩放） | ✅ |
| 8.5 | 核对文档时发现并修复：Redis 丢失流或消费组（`NOGROUP`）后，worker 与 orchestrator 不会重建，只会一直报错；现在两侧都会重建（已在本地用清空 Redis 验证） | ✅ |
| 8.6 | 任务不存在时，`GET /api/tasks/:id` 与重试/取消统一返回 `404 error.task.not_found`（原为 `ok` 与 `error.param.invalid`）；补齐 de/fr/ja/zh-Hant 缺失的 35 条任务详情页翻译 | ✅ |

### 第 9 步：对外 API 与 MCP（只做异步）

| # | 项目 | 状态 |
|---|---|---|
| 9.1 | `/v1`：独立路由组，统一信封与 snake_case；上传、`POST /v1/tasks` → 202 + `Location` + `Retry-After`、列表、取消、重试；`GET /v1/tasks/:id` 支持弱 ETag / `If-None-Match` → 304。请求体沿用第 11 步的 `options` / `outputs` / `target`，job 结果带 `resolved_options` 与 `compression` | ✅ |
| 9.2 | API Key + scopes（只存哈希）：`api_keys` 表存 SHA-256 与前缀，明文只在创建时返回一次；`tasks:read` / `tasks:write`；`/api/api-keys` 管理接口与设置页 | ✅ |
| 9.3 | `Idempotency-Key` 请求头：复用按 owner 的幂等键与指纹，重放返回 202 + `Idempotent-Replayed: true`，内容不同返回 409 | ✅ |
| 9.4 | webhook：HMAC 签名、指数退避、`webhook_deliveries` | ⬜ |
| 9.5 | URL 作为源：worker 拉取后存成普通 `File`，之后的重试、保留期、压缩统计沿用现有逻辑。只允许 `http` / `https`；在 HTTP 客户端的 resolver 里按白名单检查 IP（默认只放行全局可路由的公网单播地址，另放行部署配置 `WORKER_FETCH_ALLOW_CIDRS` 里的网段，默认为空，非空时启动打 warning），连接只用检查过的地址，所以 DNS rebinding 无效；重定向最多 3 跳，每跳同样检查；沿用大小上限与取消检查，加连接与读取超时。错误码先分"地址被拒绝"（不可重试）与"下载失败"（可重试）。私有桶的图用预签名 URL，不做专门支持。暂不做：按内容哈希复用 URL 源产物、域名白名单、存储连接器与私有网络接入（有需求再加）。MCP 以它为主要输入方式，见 9.8 | ✅ |
| 9.6 | 预签名下载 URL：已完成作业的输出带 `download_url` 与 `download_expires_at`（1 小时），ETag 按 30 分钟时间段变化 | ✅ |
| 9.7 | 限流与用量：`/v1` 写请求按用户走 Redis 令牌桶（`RATE_LIMIT_WRITE_PER_MINUTE` 默认 60、`RATE_LIMIT_WRITE_BURST` 默认 30），超出返回 `429 error.rate_limit.exceeded` + `Retry-After`，Redis 不可用时放行；只限上传申请、创建、取消、重试与两个删除，上传后续步骤与读请求不限，`/api` 不限。用量记在 `usage_records`（一事件一行，`(attempt_id, kind)` 唯一，无外键、删除任务不删用量）：成功处理按实际 `encode_rounds` 计单位，复用产物、失败、URL 下载各记一行但不计单位。`GET /v1/usage`、`GET /api/usage` 与控制台用量页按 UTC 月汇总。只记产物像素，源图像素要改协议，暂不记 | ✅ |
| 9.8 | MCP：远程 Streamable HTTP，官方 Go SDK，挂在 API 进程的 `/mcp`，无状态、只回 JSON；认证用 `/v1` 的 API Key 与 scope（OAuth 见 9.12）。工具 `create_upload`（返回预签名 PUT / 分片 URL，二进制不经工具参数；没有 complete，`submit_task` 从存储确认上传完成）、`submit_task`（URL 源、`outputs`、`target`、`idempotency_key`）、`get_task(wait_seconds ≤ 30)`（下载链接与 `compression`，不返回图片内容）。结果是 `/v1` 的表示（`structuredContent` + JSON 文本，`options` 的 Schema 嵌入生成的 ImageOptions Schema），错误是带 `/v1` 错误码的工具结果；与 `/v1` 共用限流桶、配额与幂等键。见 [MCP 契约](/contracts/mcp) | ✅ |
| 9.9 | OpenAPI 描述 `/v1`，文档与 SDK 由它生成；对外承诺的错误码单独成表 | ⬜ |
| 9.10 | 删除接口：`DELETE /v1/tasks/:id`、`DELETE /v1/files/:id`（`tasks:write`）及 `/api` 对应端点，控制台任务详情页可删除。有未结束 job 的任务返回 409 而不是自动取消：删除不可撤销，取消只是尽力而为，调用方应显式先取消。产物按 job 引用计数，仍被其他任务复用的保留并列在 `retained_outputs`；源文件不随任务删除 | ✅ |
| 9.11 | 简化上传：小文件单请求上传，可选"上传并提交任务"一步完成。现有 init → PUT → complete 三步是按 Uppy 设计的 | ⬜ |
| 9.12 | MCP 的 OAuth 2.1：按 MCP 授权规范提供 Protected Resource Metadata 与授权服务器（候选 fosite），客户端注册优先 Client ID Metadata Documents，动态客户端注册按客户端支持情况再定；token 映射到同样的 `tasks:read` / `tasks:write`，API Key 继续保留。等出现只支持 OAuth 的重要客户端再做 | ⬜ |

### 第 10 步：编码器直连与默认值

背景见[编码器对比](/engineering-notes/encoder-comparison)与[转码链路](/compute/pipeline)。

| # | 项目 | 状态 |
|---|---|---|
| 10.1 | 已知问题：经 libvips `heifsave` 编码的截图类 AVIF 比 `avifenc` 大 13%–40%（同等 SSIMU2），原因是它固定 `enable-intrabc=FALSE`、`auto-tiles=TRUE` 且无选项可改；照片不受影响。服务端已改走 libavif，浏览器沙盒仍有此问题 | — |
| 10.2 | 验证 libavif 的最坏编码时间：intrabc 在 speed 0–2 时开销大（2560×1392 截图 speed 1 为 16 秒，关闭 intrabc 为 4 秒，10 线程）；默认 speed 6 低于 1 秒，照片不受影响。决定不加限制，保持 libavif 默认，只有显式 `effort` 9–10 会进入这几档 | ✅ |
| 10.3 | 服务端 AVIF 改走 libavif（C 包装层 `avif_shim.c`，编译期经 pkg-config 链接），默认 speed 6；位深、色度、ICC、Exif/XMP、alpha、缩放后的像素仍由 libvips 准备；测试读回 ICC、Exif、方向与无损像素 | ✅ |
| 10.4 | `effort` 未填时不传给编码器（JXL e7、AVIF s6、其余为 libvips 默认）：Rust/Go 规范化、fixtures、Schema、Web 表单（显示"默认"，可恢复）、契约文档同步修改，产物键前缀升到 `image-options/v2` | ✅ |
| 10.5 | 浏览器沙盒仍用 wasm-vips 编码 AVIF：`task test:consistency` 不比较体积，确认像素差仍在阈值内，并在[已知限制](/compute/limitations#浏览器沙盒)登记两端 AVIF 体积不同。两个独立的有损 AV1 编码在 Q60 下彼此平均差约 2（各自与源图差约 2.8，无色偏），有损 AVIF 用例的阈值放宽到 3；新增无损 AVIF（要求完全相同）与 alpha AVIF 用例，共 24 组 | ✅ |
| 10.6 | 决定 `cjxl` 旁路是否传入用户显式给出的 `effort`（目前不传，即 cjxl 默认 e7） | ⬜ |
| 10.7 | worker 级开关 `WORKER_AVIF_ENCODER=libavif\|libvips`（默认 libavif），用于回退与对比；不进 `ImageOptions`，不开放给用户：实测 libavif 在照片上与 libvips 持平、截图上更小，JXL 两条路径逐字节相同，用户可选只会多出更差或相同的选项。同时修正 libavif 输出的矩阵为 BT.601（之前写"未指定"）；与 `avifenc` 的差别只剩它默认自动切 tile（截图大 15%–18%，画质相同） | ✅ |
| 10.8 | 三方复测（[编码器对比](/engineering-notes/encoder-comparison#复测-服务端-avif-改走-libavif-之后)）：服务端 AVIF 与直接调用 libavif 等效，截图比 `avifenc` 小 15%–18%（它默认切 tile）。顺带更正：libavif 默认 alpha 跟随颜色质量（有损），此前文档误写为无损 | ✅ |
| 10.9 | `bit_depth` 开放 12（AVIF/HEIC）并开放给 JXL（8 或 16）：Schema 改为枚举，Rust/Go 校验按格式区分，fixtures、表单、沙盒、文档同步；未填时的默认不变，产物键前缀不变。JPEG → JXL 的 `cjxl` 旁路只在未指定或指定 8 时使用 | ✅ |
| 10.10 | JXL 的 10/12-bit：libvips 8.18 的 `jxlsave bitdepth` 写出的像素偏暗（10-bit 暗 64 倍，12-bit 暗 16 倍），暂不开放。可选做法：等 libvips 修复，或像 AVIF 一样加 libjxl 的 C 包装层（浏览器沙盒无法同步） | ⬜ |
| 10.11 | 浮点源（scRGB）转整数位深时截断 0–1 以外的值，且不参考内嵌 ICC；需要色调映射或按 ICC 转换时再处理，与 HDR 通道一并考虑 | ⬜ |

### 第 11 步：压缩目标与多输出

规则摘要：格式必须指定；不搜索的 job（无 `target`，或给了 `quality` / `distance` / `lossless: true`）沿用 `image-options/v2` 共用产物，需要搜索的 job 用新前缀（哈希源文件 + 规范化参数 + `target` + 搜索算法版本）；搜索产物必须与 `process(源图, resolved_options)` 逐字节相同。

| # | 项目 | 状态 |
|---|---|---|
| 11.1 | **第 0 期（随第 9 步 `/v1`）**：job 返回 `compression`（`source_bytes`、`output_bytes`、`ratio = output/input`、`saved_ratio = (input-output)/input`、`larger_than_source`）与 `resolved_options`（未搜索时可从 `options` 回落）；`larger_than_source` 一律按 `output_bytes > source_bytes` 计算；任务列表接口补齐输出文件预加载或冗余字节数 | ✅ |
| 11.2 | **第 0 期（下载文件名）**：`GetPresignedURL` 签入 `ResponseContentDisposition`（取 `file.Name`），避免跨域 `<a download>` 被浏览器忽略或直链缺少友好文件名 | ✅ |
| 11.3 | **第 1 期（多输出与预设）**：`CreateTask` 支持 `options` / `outputs` / `workflow_id` 三选一；`outputs[]` 每项含可选 `name`（合法字符校验且最终文件名后缀在任务内不撞名）、`options`、可选 `target: { max_bytes }`；限制输出份数与"文件数 × 输出数"总 job 数；job 记录 `output_name` 与 `output_index`，单输出仍写为 `a.avif`，多输出写为 `a.<name\|序号>.avif`；`Workflow` 改为保存一组输出；幂等指纹计入 `outputs` 与 `target` | ✅ |
| 11.4 | **第 1 期（规范化、键与复用）**：Go 侧先判断是否需要搜索（有 `target` 且未给 `quality` / `distance` / `lossless`），搜索时不填默认 `quality`；`format: png` 带 `target` 报 400；需要搜索的 job 用新产物键前缀；`reuseOutput` 命中时复制 `resolved_options`（不创建 `Attempt`），并按当前 job 的 `target` 与源文件重算 `target_met`、`achieved` 与 `compression` | ✅ |
| 11.5 | **第 1 期（重试与 Web 控制台）**：不带参数时可批量原样重跑；带新 `options` 或 `target` 时仅允许针对单个 job；Web 第一期只做单输出加可选 `target`（启用体积目标时 `quality` 显示为"自动"），修正任务详情页（不重复累加原图体积、格式卡与数量区分文件数与输出数）并同步六种语言翻译 | ✅ |
| 11.6 | **第 1 期（模型与队列协议 v2）**：`models.go`、`schema.go`、`overview.md`、`http-api.md` 增加 `target`、`resolved_options`、`target_met`、`achieved`、`output_name`、`output_index` 与 `Attempt` 上的 `encode_rounds` / `compute_ms`；`protocol.rs`、`queue.go`、`fixtures/{dispatch,results}-v2.json` 与 `queue.md` 在 `v2` 上新增对应可选字段；第 1 期共用 `q:image` | ✅ |
| 11.7 | **第 1 期（搜索实现 `compute-optimize`）**：`compute-imageops` 拆出并公开"解码、缩放与色彩转换并落进内存"（防止关闭缓存后每轮重复解码）与"编码"原语，`process` 签名和行为不变；新 crate `compute-optimize` 单向依赖 `compute-imageops`，二分 `quality`（1–100，约 7 轮）或 JXL `distance`（0.1–25，精度 0.05，约 9 轮），从所有试过的结果里挑最优，达不到时交付最小结果并标 `target_met: false`；未指定 `chroma` 时沿用 `Q ≥ 90` 切换 4:4:4 规则并把胜出轮次实际 `chroma` 写进 `resolved_options`；JPEG→JXL 满足旁路条件（无缩放、`color=keep`、位深 ≤ 8）时先试 `cjxl` 无损旁路，命中则 `resolved_options` 记 `lossless: true`；每轮之间检查取消标记（`AtomicBool`）并上报进度；测试验证搜索产物与 `process(源图, resolved_options)` 逐字节相同 | ✅ |
| 11.8 | **第 2 期（`min_ssimu2` 与队列隔离）**：评估 Rust 端 SSIMULACRA2 实现的准确度、速度与内存，定下 16 位、广色域、alpha 处理方式与像素上限；参照图取"缩放与色彩转换之后、编码之前"的内存图像；双目标都能满足时取刚达 `min_ssimu2` 的最小体积，冲突时体积优先；`reuseOutput` 在只检查 `min_ssimu2` 时仅复用已带 `achieved.ssimu2` 的记录；搜索任务切分独立队列与 worker 池 | ⬜ |
| 11.9 | **第 3 期（多输出体验增强）**：Web 控制台多输出编辑器；必要时让 worker 一次解码源图输出多份产物（主要减少 HEIC 重复解码） | ⬜ |

### 第 12 步：对外运行的前提

第 9 步解决"别人怎么调用"，这一步解决"很多人同时调用时不出事"。12.1–12.3 应在 `/v1` 对第一个外部用户开放之前完成。

| # | 项目 | 状态 |
|---|---|---|
| 12.1 | 按用户公平派发：每个用户同时 `PROCESSING` 的 job 数有上限（`DISPATCH_MAX_PROCESSING_PER_USER`，默认 8），领取时在用户之间轮询；按用户的部分索引与 advisory lock 保证多实例下不超限、不重复派发。交互与批量优先级留作后续 | ✅ |
| 12.2 | 单个 attempt 的最长执行时间：从 `started_at` 起算，普通 10 分钟、搜索任务 30 分钟（`EXECUTION_LIMIT`/`TARGET_EXECUTION_LIMIT`）；心跳续租不越过上限，到期由恢复循环按 `execution_timeout` 重试 | ✅ |
| 12.3 | 存储生命周期：`SOURCE_RETENTION_DAYS` / `OUTPUT_RETENTION_DAYS`（默认 30 天）与按用户覆盖字段（暂无界面）；orchestrator 里的 `services/lifecycle` 每 10 分钟把到期文件标为 `EXPIRED` 后删对象，失败重试，跳过未结束 job 的源文件；复用与秒传只选剩余保留期过半的文件。R2 生命周期规则只写了文档。已知遗留：显式重试已完成的 job 时旧产物不再被引用，要等到期才删；清理由 orchestrator 运行（12.22），多个 orchestrator 实例之间没有互斥（可并发，只是重复劳动） | ✅ |
| 12.4 | 配额：按用户的存储（`QUOTA_STORAGE_BYTES`，默认 10 GiB）与每月处理单位（`QUOTA_MONTHLY_UNITS`，默认 10000），`users` 上可按用户覆盖（暂无界面），`0` 为不限。创建任务与重试时按"已结算 + 未结束 job 预占 + 本次预占"检查（搜索预占 7、JXL 搜索 9、其余 1，完成后按实际轮数结算），按用户 advisory lock 串行；上传 `init` 按申报大小检查，存储为软限制。超出返回 `403 error.quota.exceeded`，已接受的任务照常跑完。单任务 job 数沿用全局常量 `MaxItems` 100、`MaxOutputs` 10、`MaxTotalJobs` 200（`400 error.param.invalid`）。不引入组织层级 | ✅ |
| 12.5 | 可观测性：指标（各队列积压、排队等待时间、处理耗时分位、各错误码比例、按用户用量）、追踪与告警。目前只有日志（限流、配额拒绝、用量写入失败都有结构化日志）；按用户用量直接从 `usage_records` 汇总，不另做指标。有了派发延迟与积压指标后，再决定派发是否需要多实例并行（目前多实例只靠 advisory lock 保证不重复，吞吐不随实例数增加） | ⬜ |
| 12.6 | 生产部署：部署方式、staging 环境、Postgres 备份、密钥轮换；worker 按队列积压扩缩容（见"以后"的自动扩缩容）；生产环境启动时校验 S3 等必需配置，缺失或仍是开发默认值时拒绝启动。目前只有本地开发 | ⬜ |
| 12.7 | 审计与数据删除：API Key 的使用记录、按用户删除全部数据、对外的数据保留说明 | ⬜ |
| 12.8 | 清理：`JWT_SECRET` 配置与 JWT 中间件仍在代码中但未使用，对外前删除或接入 | ⬜ |
| 12.9 | 取消与执行上限要真正停下计算：每个 job 的计算放在 worker 自身启动的子进程里，看到取消标记后杀掉整个进程组并等它回收完，再读下一条派发；原先被放弃的 libvips/libavif 编码会在后台跑完（2026-11 端到端验证中一个 effort 10 的大图 AVIF 被判超时后又在同一个 worker 上跑了 3 分 20 秒）。没有用 libvips kill 标志，因为它停不下 libavif/aom 等编码器本身。每个 job 多约 70 ms 的进程启动；如有需要可预先启动下一个子进程 | ✅ |
| 12.10 | 500 响应不回显内部错误：所有 500 经 `response.RespondInternal` 返回，错误只写日志，响应只带 `details.request_id`（`requestid` 中间件，响应头 `X-Request-ID`） | ✅ |
| 12.11 | OAuth 不再按邮箱合并账号：只按 `(provider, provider_account_id)` 查绑定；没有绑定且邮箱已被任何用户占用时拒绝（不合并、不建绑定），前端提示用原来的方式登录。GitHub 只取 `/user/emails` 里已验证的邮箱，Google 要求 `email_verified`，service 再统一检查。回调的所有失败都重定向到 `FRONTEND_URL?auth_error=<code>`。已知遗留：没有手动绑定第三方账号的入口；密码注册不验证邮箱（因此邮箱被占用时只能拒绝，不能合并）；`user_oauth_accounts` 的唯一索引含 `user_id`，`(provider, provider_account_id)` 本身在库里不唯一，靠"只为新用户建绑定"保证 | ✅ |
| 12.12 | 会话 ID 不再交给页面脚本：登录/注册响应去掉 `token`，`/api` 只认 HttpOnly 的 `session_id` cookie（不再接受 `Authorization: Bearer <session_id>`），前端删掉 `auth_token` cookie。`/api` 上所有 POST/PUT/PATCH/DELETE（含 `/api/login`、`/api/register`、`/api/logout`）要求 `Origin` 与规范化后的 `FRONTEND_URL` 严格相等，缺少即拒绝（`403 error.auth.origin_rejected`）；`/v1`、`/mcp` 不受影响。Nitro 代理原样转发 `Origin`，并以 `redirect: "manual"` 把 API 的重定向交给浏览器 | ✅ |
| 12.13 | 积压不等于丢失：outbox 记下每次发送的 stream ID；`StartTimeout` 到期时用 `XRANGE` 和 `XINFO GROUPS` 的 `last-delivered-id`（按 `(ms, seq)` 数值比较）判断派发是否仍在排队，仍在排队只续租不重发；被 `MAXLEN` 裁掉、已投递却没 `RUNNING`、查不到的照旧重发；Redis 出错时只续租。积压超过 10 万条时被裁掉的派发仍会在下一个 `StartTimeout` 才重发 | ✅ |
| 12.14 | 同一 attempt 的副本不覆盖已发布的产物：`COMPLETED` 提交后写 `cancel:attempt:<id>`，未开始的副本跳过、运行中的最多约 3 秒被杀掉且不上传。剩余窗口：副本在标记写入前已传完，或在队列里等了超过标记的 24 小时。不做条件写。`FAILED` 后不写标记，失败 attempt 的迟到副本仍可能留下一个没人引用的产物对象 | ✅ |
| 12.15 | 派发吞吐：一批取满（20）且有领取时立刻取下一批，否则等 500 毫秒 | ✅ |
| 12.16 | 结果消费：`XREADGROUP` 每次取 20 条、单协程顺序处理，`XAUTOCLAIM` 每 15 秒最多一次；`RUNNING` 心跳只锁 job 行，不再锁 task | ✅ |
| 12.17 | 优雅退出：API 收到 SIGINT/SIGTERM 后 `ShutdownWithContext`（10 秒），后台清理随之停止；worker 只设退出标志、不打断进行中的 `NOACK` 读取（读取阻塞 1.5 秒），正在跑的 job 有 `WORKER_SHUTDOWN_GRACE_SECS`（默认 30 秒）宽限期，超时被杀并回报可重试的 `worker_shutdown`，照常计一次尝试。API 与 orchestrator 的后台协程没有等待退出，进行中的事务随 ctx 取消回滚 | ✅ |
| 12.18 | `AUTO_MIGRATE` 默认关闭，迁移只经 `task migrate`（`cmd/migrate`）显式执行 | ✅ |
| 12.19 | 上线前需确认 Upstash 支持 `XINFO GROUPS` 并返回 `last-delivered-id`（12.13 依赖它；本地只用 Redis 8 验证过）。不支持时 `Waiting` 会一直报错，恢复循环只续租不重发，丢失的派发就不会再发出 | ⬜ |
| 12.20 | Job 状态转换只有一份实现：领取、心跳、完成、复用、重试、失败、取消、手动重试都经 `jobstate.Run`/`Tx`，转换检查起始状态与 task 锁，写回前用 `jobstate.Check` 校验字段规则，状态变过的 task 在提交前聚合一次；测试结束时对库里全部 job 跑 `Check` | ✅ |
| 12.21 | 文件删除只有一套模型：`files` 只剩 `UPLOADING`/`FETCHING`/`UPLOADED`/`EXPIRED`/`DELETED`，删除文件、草稿、任务产物与中止上传都经 `services/lifecycle`（先改为 `DELETED` 再删对象，失败重试）；未完成的上传删除后立即中止分片，签名 URL 过期后再中止一次并删对象；上传端点只接受 `UPLOADING`，`complete` 不再复活已删除的文件。过期上传与 7 天草稿并入 lifecycle 每小时一次、分批处理，删除 `storage/cleanup.go`。没有为旧状态（`DELETING`/`ABORTING`/`ABORTED`/`FAILED`）写数据迁移，库里遗留的这类行不会再被处理 | ✅ |
| 12.22 | 周期任务移到 orchestrator：存储清理（`lifecycle.Service.Run`，每 10 分钟 `Sweep`、每小时 `SweepUploads`）由 `cmd/worker`（orchestrator）启动，接 orchestrator 的信号 ctx，收到 SIGINT/SIGTERM 等本轮结束后退出；API 只处理请求，`cmd/api` 有测试保证它不引入 `lifecycle`/`orchestrator` 包。S3 客户端经 `bootstrap.OpenStorage` 共用初始化，API 与 orchestrator 缺 `S3_BUCKET`、`S3_ACCESS_ID` 或 `S3_ACCESS_KEY` 时都在连接数据库前启动失败并列出缺的变量。已知遗留：多个 orchestrator 实例各跑一份清理（见 12.3） | ✅ |
| 12.23 | `/v1` 的响应格式在对外开放前定下来：沿用现在与 `/api` 相同的 envelope（`code`、`message`、`data`/`details`），还是改成直接返回资源 JSON、错误用 RFC 9457（`application/problem+json`）；`/api` 不变。开放后再改就要做版本迁移。**待决定** | ⬜ |
| 12.24 | 控制台首页的存储统计用 `usage` 的上限与口径：`task.Service.GetStats` 的 `storage_used` 改用 `usage.StorageUsed`（`UPLOADED` 加 `UPLOADING` 的声明大小，与存储配额检查一致），`storage_limit` 取 `usage.Quotas.LimitsFor`（全局 `QUOTA_STORAGE_BYTES`，按用户 `storage_quota_bytes` 覆盖），不限时为 `null`；首页卡片不限时显示「已用 · 不限」、不画进度条，"% used"/"free" 文案进了 i18n | ✅ |
| 12.25 | `/api` handler 小修：workflow 与文件下载地址的 404 改为 `error.workflow.not_found`（新增）与 `error.file.not_found`；会话、API Key（与已不用的 JWT）中间件把用户解析成 `uuid.UUID` 后用 `middleware.SetUserID` 存入，handler、`/v1`、限流统一用 `middleware.UserID` 取值，`ownerFromContext` 与各处字符串解析删除，会话里的 `user_id` 不是合法 UUID 时按未登录返回 401；不再比较错误字符串：唯一约束冲突统一由 `pkg/dberr.IsUniqueViolation` 按 SQLSTATE 23505 判断（注册、OAuth 绑定、任务幂等键共用），`GetTask` 只用 `errors.Is`，Redis 的 `NOGROUP`/`BUSYGROUP` 改用 go-redis 的 `HasErrorPrefix`（只匹配 Redis 返回的错误）。没开 GORM 的 `TranslateError`。前端错误码文案只在 `useApi.ts` 的英文兜底表里，补了两个 not_found，`web/i18n/locales` 无需改动 | ✅ |
| 12.26 | Nitro 代理的后端地址从运行时配置读取：`/api/**` 的路由规则代理（构建时固定）换成服务端路由 `web/server/routes/api/[...path].ts`，用 h3 `proxyRequest` 转发到 `runtimeConfig.apiBase`（`NUXT_API_BASE`，默认 `http://localhost:8080`，只取 origin，改了不用重新构建）；照旧原样转发 `Origin`（API 的 Origin 检查依赖它）与 Cookie/Set-Cookie，`fetchOptions.redirect: "manual"` 让 OAuth 回调的 303 交给浏览器；拼出的目标不在该 origin 的 `/api` 下时返回 400。已在浏览器里经代理注册、登录、刷新后取 `/api/me` 与统计，用 curl 验证了 Origin 拒绝、OAuth 回调 303 不被跟随与 `session_id` 透传。已知遗留：前端没有测试工具，代理没有自动化测试 | ✅ |
| 12.27 | 前端规则与大页面拆分：`.agent/rules/code-rules.md` 要求 Temporal 与 TanStack Query，代码里都没用；先定规则（保留 TanStack Query，还是用已安装的 VueUse 做轮询与失焦暂停；是否去掉 Temporal），再拆 `tasks/[id].vue`（约 1400 行）与 `tasks/new.vue`（约 1250 行）。**待决定** | ⬜ |

### 以后（结构已预留，暂不排期）

- **CDN 式取图** `/v1/img/{source}/{options}`：有产物就 302，否则返回原图并投递任务。
- **AI 能力**（放大、生图、P 图）：注册表加一行 + 新 worker 池；付费调用以 `attempt_id` 作幂等键，attempt 上记 `remote_ref` 与用量。
- **任务串联**：输入写 `from_job`，上游完成后才派发。
- **浏览器读取 HEIC**：首选自编 wasm-vips 打开 libde265（仅解码），`heifload` 与服务端行为一致，无需手工透传色彩与元数据；备选 libheif-js + ISOBMFF 解析（仅 8-bit）。
- **HDR / gain map 通道**：libavif / libultrahdr 在容器层处理，元数据对齐 libvips `gainmap-data`；libvips 支持 AVIF gain map 后可整体删除。
- **自动扩缩容**：按各 stream 积压量（KEDA Redis Streams 伸缩器）。
- **分片上传的 SHA-256 校验**：单次 PUT 由存储按签名里的 SHA-256 校验，分片上传（>10 MB）的 `hash` 仍由客户端上报。计划由 worker 首次读取源图时计算并回写，秒传只匹配校验过的文件。
- **跨刷新断点续传**：init 按 `owner_id + hash + UPLOADING` 找回旧的 `uploadId`，经 `list-parts` 续传；目前只支持页面内重试。
