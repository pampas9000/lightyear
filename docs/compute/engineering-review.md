# 实现审查与取舍

审查日期：2026-09-28。范围：当前工作区的 `compute-engine`、`compute-server-adapter`、Rust Worker，以及直接影响转码语义的 Go 参数校验、任务恢复和 Web 参数表单。不是整个产品的安全审计，也不是所有格式、平台和显示设备的画质认证。

源码简称：`engine/` 指 `compute/crates/engine/src/`，`transcode/` 指 `compute/crates/server-adapter/src/transcode/`，`worker/` 指 `compute/worker/src/`；`worker.rs`、`handler.rs` 均位于后者。

**本轮已开始按优先级修复功能代码。** 下文状态以当前工作区为准：
“已修复”表示已有代码和针对性回归；“部分修复”表示只完成低风险边界，
仍有集成验证或更大范围能力未完成；“暂缓”不是现有支持承诺。

### 当前修复状态

| 项目 | 状态 | 已完成内容 | 仍未完成 |
| :--- | :--- | :--- | :--- |
| R1 颜色决策 | 部分修复 | HDR 源 nclx 优先用于像素 transfer；tone mapping 输出改为 sRGB transfer | 完整 ICC/广色域到 sRGB 的色域转换仍暂缓 |
| R2 精度与 Alpha | 已修复边界 | RGB/RGBA/Gray/Gray+Alpha 的 16-bit YUV、resize、PAM 和 Alpha 判断覆盖补齐 | 全部格式组合的产物级回归仍需继续扩展 |
| R3 Y4M/Identity | 已修复边界 | 未支持的 p12/p16/未知布局拒绝；CICP M=0 正确映射为 Identity/GBR | 外部 decoder 的全部 Identity 组合仍需部署矩阵验证 |
| R4 AVIF 参数一致性 | 已修复边界 | Alpha 路径也先解析 depth/chroma；native 读取 color/legacy 参数；显式 `None` 不阻断 native | 缺少 avifenc 时的降级策略仍只覆盖已声明限制 |
| R5 Worker 所有权 | 部分修复 | heartbeat 现在通过 `XCLAIM` 续期 PEL idle 时间 | attempt 隔离的 S3 产物提交尚未实现 |
| R6 CLI I/O | 部分修复 | stdin 与 stdout/stderr 并发排空；共用 deadline；诊断缓冲有上限 | 下载、解码和像素工作集的统一资源预算仍需接入 |
| R7 元数据 | 部分修复 | `strip` 仍可读取 nclx 作为源描述；JXL 默认继承可识别的源 nclx，`strip` 会禁用 JPEG bypass | ICC/TRC、tone mapping 后的 profile 策略及完整组合仍需回归 |
| R8 参数校验 | 已修复边界 | adapter 公共入口验证；AVIF 只接受实际支持的 8/10-bit full range；Web `"none"` 归一化修正 | Web 新建与重试仍有可合并的表单工具函数 |
| R9 组合测试 | 进行中 | 新增 engine、adapter、CLI 管道回归 | 固定工具链下的完整产物门禁尚未完成 |

## 结论与边界

### 2026-09-29 补充优化

- 像素路径新增 adapter 私有的 `OutputColor`，编辑、tone mapping、位深决策、AVIF CICP 和 JXL 输出标签共享结果。PQ + ACES 的 JXL 输出现在保留 BT.2020 基色并标注 sRGB transfer；冲突的显式 AVIF CICP 直接拒绝，native 路径也执行检查。
- tone mapping 后不再复用源 ICC。尚不能生成正确输出 profile 的广色域 SDR JPEG/PNG/WebP/HEIC 请求会报错，不宣称已经完成 gamut conversion。
- 集中 RGB/RGBA/Gray/Gray+Alpha 的 16-bit 判断，修复灰度 blur 和 WASM 原始像素导出的降位深遗漏。PNG/HEIC 不再在编码前把明确的高位深请求先量化到 8-bit。
- ICC 以字节持有，仅需要路径的 CLI 按需落盘；工具可用性按进程缓存，安装工具后需重启 Worker。`djxl` 仅在测试中探测。
- 删除共享类型中的 `NativeRepresentation`、`ColorModel` 和 `Handoff`，改为 adapter 内部的具体 YUV 布局比较，不扩展表示协商框架。
- `XAUTOCLAIM COUNT 1` 与 Worker 的串行执行及单任务续租一致。attempt 隔离的 S3 产物、统一资源预算、CPU 强制取消、完整 ICC/TRC 和全部动态参数归一化仍未完成。

保留现有 `types / engine / server-adapter / worker / wasm` 分层。服务端 CLI 与浏览器 WASM 是真实的不同消费方；线性光重采样、预乘 Alpha、源 ICC 搬运、JPEG→JXL 重构和有条件的 YUV 平面直通都有实际价值，不属于应该删除的架构。

主要问题是**同一输入与请求在不同路径上被解释成不同操作**。优先统一源颜色描述、输出决策、像素表示和参数优先级，补足跨阶段回归；不要为了这些问题再引入通用编解码插件系统或自动管线规划器。

成本分级：低 = 局部判断、校验或解析修改；中 = 多路径共用规则及回归；高 = 新编解码/容器栈、显示相关色彩模型或跨平台运行时。以下不按未经测量的工期估算排期。

## 需要修正的实现

### R1 · P1：源颜色描述与输出意图混用

**证据：已复现，成本中。**

- `transcode/pipeline.rs::output_spec` 从请求 `ColorEncoding` 取源 transfer；输出 AVIF/JXL 时仍用该 transfer 编码。`transcode/color.rs::resolve_output_cicp` 却在 ACES 开启后写 sRGB 标签。PQ/HLG + ACES → AVIF 的像素和标签不一致。
- 源 `nclx=9/16/9`、没有 `color` 时能继承 HDR；仅传抽样、位深、抖动等控制项构成 `color` 对象后，默认 `transfer=Srgb` 又覆盖了源 PQ。Web“保持原观感”实际会发送这样的对象，不能只测试 `params=None`。
- Web“转为标准 SDR”把 `color_space` 设为 `srgb`，而引擎把它当源描述，不能据此正确解释实际 PQ/HLG 输入。
- 色调映射不等于基色转换。当前没有 BT.2020/P3 → sRGB 基色转换，却把 ACES 输出 primaries 改成 sRGB，不能宣称已经得到标准 sRGB。

**当前修复：** adapter 已优先使用源 nclx 的 HDR transfer，并让编辑、tone mapping 和 AVIF CICP 共享该判断；tone mapping 只改变 transfer 标签，不伪装成 primaries 转换。无法兑现的广色域→标准 sRGB 请求仍应拒绝或明确限制；完整色域转换见暂缓项 D1。

**验收：** PQ/HLG × AVIF/PNG/WebP/JXL，覆盖无 `color`、只有位深/抽样、保留、SDR 四种请求；同时断言解码后的像素、transfer、primaries 和 ICC，不只检查文件存在。

### R2 · P1：像素类型分支造成精度与 Alpha 丢失

**证据：已复现，成本中。**

- `engine/color/yuv.rs::rgb_to_yuv420_planar_10bit` 只专门处理 `ImageRgba16`，`ImageRgb16` 落入 `to_rgba8()` 分支。等价 RGB16 与 RGBA16 得到不同精度的 10-bit 输出。
- `ImageLuma16` 在 10-bit YUV 路径、线性光 resize 等处理中也会先降为 8-bit。
- `transcode/encode.rs::encode_avif` 的透明检测只覆盖 RGBA8/16；灰度 Alpha PNG 解码得到 `ImageLumaA16` 时被当成无 Alpha，走 Y4M 后透明度丢失。

**当前修复：** 复用 `image::DynamicImage::to_rgba16` 作为现有支持类型的统一 16-bit 工作表示，并集中补齐 Alpha/16-bit 判断；没有新增通用像素格式系统。不支持的浮点/HDR 类型仍需显式边界。

**验收：** RGB/RGBA/Gray/Gray+Alpha × 8/16-bit，覆盖无编辑、resize、blur、YUV420/422/444 和编码；等价像素表示必须得到同等精度，非不透明 Alpha 不能消失。

### R3 · P1：未支持的布局被当成支持的布局读取

**证据：已复现，成本低至中。**

- `engine/color/yuv.rs::parse_y4m_header` 仅识别 `p10`，其他位深被视为 8-bit。合法 `C444p12` 被成功解析并错误解码成 RGBA8，而不是报告不支持；未知色度标签也会落入 444。
- `engine/color/metadata.rs::Nclx::matrix_coefficients` 把 `matrix=0` 返回为 `None`。下游于是回退到 BT.709，尽管 Y4M 解码器本身已有 GBR/Identity 支持。native 标签解析也会把源 M=0 改成 M=1。

**当前修复：** Y4M 只接受当前实现的 8-bit 和 p10 420/422/444 白名单；p12/p16/未知布局直接报错；CICP M=0 传递为 Identity。完整 12/16-bit YUV 支持仍暂缓，但**拒绝误读不能暂缓**。

**验收：** `C444p12`、`C420p16`、mono、未知标签不能冒充 8-bit 444；GBR 测试必须经过实际 source metadata → adapter → decoder/tag 链路，而不只是直接给底层函数传 `Identity`。

### R4 · P1：AVIF 三条编码路径不遵守同一组参数

**证据：已复现，成本中。**

- `transcode/native.rs::target_representation` 不读取 `engine_params.depth/yuv`，`finish` 又清除这两个参数。8-bit 420 源请求 10-bit 444，实际仍输出 8-bit 420。
- `transcode/encode.rs::encode_avif` 在 Alpha 分支提前返回，跳过普通 Y4M 路径的 `resolve_effective_yuv` 和位深解析。RGBA16 请求 `color.nominal_bit_depth=10, chroma=yuv420`，本机实际输出 12-bit 444。
- native 门禁把 `Some(ToneMappingOperator::None)` 也视作需要 tone mapping；“未填写”与显式 `"none"` 行为不同。它还在确认目标是否适用前解码 Y4M，且未先要求 `avifenc` 可用，可能重复解码或绕开原本应走的 fallback。

**当前修复：** AVIF 编码先得到有效的 depth/chroma，再决定 native、PNG Alpha 或 Y4M 路径；native 只在源和目标布局一致时启用。参数解析错误不再被 `unwrap_or_default()` 静默吞掉，目标格式和工具可用性检查也提前。

**验收：** 同一请求分别带/不带 Alpha、带/不带编辑、使用 `color`/兼容的 `engine_params`、省略/显式 `"none"`，输出参数一致；缺少 `avifenc` 时严格模式报错，非严格模式按已声明能力降级。

### R5 · P1：长任务回收与产物写入缺少一致的所有权约束

**证据：调用链检查，尚未做多 Worker + Redis/S3 集成复现；成本中。**

`worker.rs::poll_entries` 认领空闲超过 60 秒的计算流 PEL 消息；`spawn_heartbeat` 只向结果流发布心跳，没有更新该 PEL 条目的 idle 时间。Go 结果消费者更新的是数据库 `updated_at`。因此，另一 Worker 可以认领仍在正常运行的长任务，并以同一 `attempt_id` 再执行一次。

另外，`handler.rs` 在发布结果前上传 `payload.output_path`，`server/cmd/worker/main.go::runStaleJobRecovery` 的新 attempt 继续使用原 output path。数据库拒绝旧 attempt 的结果，并不能撤销它对同一存储对象的覆盖。终态检查仍有价值，但不是存储写入的隔离措施。

**当前修复：** Worker heartbeat 除发布 PROCESSING 外，还用 `XCLAIM` 将当前 compute stream entry 的 PEL idle 时间归零，和 `XAUTOCLAIM` 的回收规则一致。attempt 隔离的 S3 产物仍未实现，因为它必须同时修改公开输出路径、当前 attempt 提交和失败产物清理；本轮不以改后缀的方式冒险实现。

**验收：** 两个 Worker，单任务运行超过 60 秒仍只执行一次；崩溃后可恢复；过期 attempt 晚上传/晚回报不能改变当前产物。Redis idle 语义参见 [XAUTOCLAIM 官方说明](https://redis.io/docs/latest/commands/xautoclaim/)。

### R6 · P2：CLI 已有超时，但 I/O、内存与取消边界仍不完整

**证据：管道阻塞已复现，其余为代码检查；成本低至中。**

- `transcode/process.rs::run_with_stdin` 先完整写 stdin，再读取 stdout/stderr。子进程先写满输出管道、再读取输入时，双方会互相等待。1 MiB stdin + 1 MiB stderr 的小型复现即会卡住。
- 输入写入和后续等待各给 600 秒，不是一个贯穿全过程的 600 秒 deadline。`read_all` 无界缓存全部输出，最后截断错误文字到 4096 字节不能限制此前的内存占用。
- Worker 的 `tokio::select!` 可取消正在等待的 CLI/IO，但同步 Rust decode/edit/encode 不会因为收到取消标记就被抢占。临时目录是文件组织手段，不是 OS 安全沙盒。
- `worker/storage.rs` 下载时 `body.collect()`，之后 metadata/decode 又多次读整文件；worker/engine 未统一限制解码尺寸、输出尺寸及工作内存预算。

**当前修复：** CLI stdin 与 stdout/stderr 已并发处理，共用一个 deadline，诊断流持续排空但只保留有界内容。下载/解码/编辑的统一资源预算仍未接入；同步工作移到 blocking pool 也不等于获得强制取消能力。

**验收：** 大 stderr + 大 stdin、早退、写入超时、取消和进程退出均不挂起；超限输入被拒绝；内存峰值和取消延迟有明确测试边界。完整分块流水线、跨平台硬隔离见 D7，不要求这一轮实现。

### R7 · P2：元数据策略与处理后的像素状态未对齐

**证据：strip 丢失 nclx 已复现，其余为调用链检查；成本中。**

- `transcode/profile.rs::load_source_profile` 在 `strip` 时连源 nclx 也不读取。输出不携带 ICC，不应意味着处理中不知道源 transfer。
- PNG/JPEG/WebP/HEIC 编码路径会重新带上源 ICC，没有统一判断 tone mapping 是否已改变其 transfer；AVIF 则另写了一套丢 ICC 规则。
- `classify_icc_color_space` 仅比较 XYZ colorants，没有校验 TRC 就用标准 `cjxl color_space` 替换完整 ICC。同基色、不同曲线不应被当成同一个 profile。
- JPEG→JXL bypass 不检查 `profile_policy=strip`，也没有生成剥离 ICC 的参数；“原始 JPEG 可重构”与“剥离原始元数据”需要明确冲突规则。

**当前修复：** 源 nclx 的读取已与 ICC 是否 strip 解耦，strip 不再抹掉处理阶段需要的 HDR 描述；非直通 JXL 已在无 ICC/显式提示时继承可识别的源 nclx，JPEG bypass 也会在 `strip` 时关闭。ICC/TRC 识别和 tone mapping 后的 profile 策略仍需单独回归，无法可靠识别的 ICC 不应替换为命名色彩空间。

**验收：** preserve/strip × 有/无编辑 × 有/无 tone mapping；默认 JXL 的 PQ/HLG 标注；同基色不同 TRC；JPEG 重构与 strip 冲突必须明确报错或明确选择的行为。

### R8 · P2：表单归一化与后端校验会静默改变请求

**证据：代码检查；成本低至中。**

- `web/app/pages/tasks/new.vue` 和 `TaskRetryDrawer.vue` 的 `normalizeLoadedColorParams` 都使用 `legacy?.tone_mapping ? 'aces_filmic' : 'none'`；字符串 `"none"` 为真，保存后再载入会打开 ACES。
- Rust `ToneMappingOperator` 把任何未知字符串映射成 ACES，而不是只兼容已知历史别名。WASM 颜色 JSON 解析失败则直接忽略配置。
- Go 接受 `nominal_bit_depth=8..16` 的所有整数、limited range、yuv400；Rust 部分路径最终只输出 8/10-bit、固定 full range，400 则稍后报错。接收参数不等于实现参数。
- `Params::validate_for_format`、各 encoder 的 `unwrap_or_default()` 与表单默认值并非同一个入口，调用 adapter 的其他消费者可能跳过 worker 校验。

**当前修复：** adapter 公共入口和 worker 参数入口均会执行格式校验；AVIF 当前只接受已实现的 8/10-bit full-range 能力，tone mapping 只兼容已知历史值，解析失败不再静默回退。Web 新建与重试的归一化仍建议后续抽为共享纯函数。

**验收：** 保存→载入→重试的 `"none"`、缺失值、旧别名保持含义；拼写错误、非法位深和不支持组合在开始转码前报错。

### R9 · P2：测试与文档需从“模块存在”转向“组合满足契约”

**证据：现有测试与 benchmark 检查；成本低至中。**

当前 `scripts/benchmark.ts` 主要审计**输入素材头**并执行 workspace 测试，不是全部素材、全部预设的转码产物画质门禁。部分 E2E 缺 CLI/fixture 时提前返回；一些测试只断言输出存在、尺寸或位深，未验证颜色值与标签的对应关系。

`hlg_ootf` 目前只有定义、导出和测试，没有接入生产色调映射链路。“公式已实现”不能写成“完整 HLG 显示处理已支持”。同理，PNG Alpha 中转已经使用，就不能再把“全程禁止 PNG 中间容器”写作当前硬承诺。

**近期做法：** 固定一套 CLI 测试环境，显式统计 pass/skip/fail；先把 R1-R8 的最小回归做成必须执行的产物检查。感知指标先挑一个接入小素材集，不同时搭建多个大而全评测系统。

**验收：** 工具链缺失不能令必测发布任务显示“通过”；实际检查产物位深、抽样、Alpha、ICC/CICP 和代表性像素；目标阈值、人工验收与自动门禁分开标注。

## 值得简化的设计

以下保留初始收缩建议；YUV 布局、ICC 临时文件和工具探测已在 2026-09-29 按本页补充记录优化，其余仍需核对真实消费方和兼容需求。

| 对象 | 当前复杂度来源 | 建议 |
| :--- | :--- | :--- |
| 颜色与编码参数 | `color_space`、旧 primaries/transfer、`color` 与 `engine_params` 的重复 depth/chroma/CICP 各自决策 | 保留兼容输入，统一入口解析为私有的有效配置；不要让各 encoder 再解释一次 |
| `NativeRepresentation` / `Handoff` | 生产调用仅使用 YUV 和 `Identical`；`MatrixOnly` 仅分类、无执行路径，Gray/CMYK 没有生产消费方 | 当前需求可收缩为 adapter 内部 YUV 布局及相等判定；不扩建通用表示协商框架 |
| 可选 tone mapping | 缺字段、`None`、`Some(None)` 在不同门禁中含义不同 | 边界统一为一个禁用状态，保留必要旧载荷兼容 |
| 重复表单逻辑 | 新任务、重试和设置组件维护相近预设、迁移及序列化代码，已经出现相同错误 | 一个颜色参数类型、一组预设、一套纯函数；不要再复制一套 UI 状态转换 |
| 元数据读取与临时 ICC | 探测、decode、profile 多次整文件读取；ICC 写出后又读回供 portable encoder/分类使用 | 复用一次源探测和 ICC 字节，CLI-only 落盘；暂不建立跨任务缓存服务 |
| 每任务工具探测 | 每个任务启动多次 `--version`，`djxl` 主要供测试使用 | 固定部署中按 worker 生命周期探测，提供显式刷新/错误诊断；不做动态 provider 发现框架 |
| ISOBMFF 手写遍历 | ICC、nclx、auxC 各自遍历，尺寸和深度检查不一致 | 统一小型有界遍历器及畸形输入测试；无证据不直接重写为全功能容器库 |
| 未接入能力 | HLG OOTF、非主用 tone-map helper、实验性 FFI 等容易被文档算成支持 | 明确实验/未接入，冻结或收缩导出；有具体消费方后再接入，不为勾选功能增加维护面 |

**不建议简化掉：** Alpha 检查、准确的源 metadata、输入资源限制、严格错误、attempt 校验，以及必要的兼容迁移。它们是现有输入和运行模型的约束，不是“没有消费方的功能”。

## 明确暂缓的能力

| 编号 | 暂不投入 | 成本与原因 | 现在采用的边界 | 重新考虑的条件 |
| :--- | :--- | :--- | :--- | :--- |
| D1 | 完整 ICC 色域转换、感知 gamut mapping、显示相关 HDR 模型 | 高；任意 TRC/LUT、白点、亮度标尺及验证矩阵远超修标签 | 只保留可正确描述的源颜色；不能做到的标准 sRGB 转换拒绝/限制。R1/R7 仍需修复 | 产品明确要求广色域资产统一交付标准 sRGB，且有参考 CMS 与验收素材 |
| D2 | Gain Map 合成、跨容器双层保留 | 高；主图、辅助图、metadata 关联和不同格式工具能力均需验证 | 明确不支持双层保留；当前可能只保留主图。轻量提示/拒绝不等于实现合成，不能宣称门禁已存在 | 有真实 HDR 双层交付需求与经过验证的官方工具路径 |
| D3 | 动图、视频、多帧时间轴 | 高；帧时长、循环、帧间依赖及增量资源模型不同 | 当前服务面向静态单图，不承诺动画保留 | 有真实批量需求并可安排时间轴测试与资源预算 |
| D4 | 原始码流零解码直通、MatrixOnly、Y4M Alpha 特例 | 中至高；组合多、收益尚未测量 | 只保留已验证的 identical YUV 优化；Alpha 使用无损 PNG 中转 | benchmark 证明中间转换/PNG 已是主要瓶颈，且优化不降低正确性 |
| D5 | 服务端全面切换 in-process C-ABI、自研 codec、扩展桌面 FFI | 高；ABI、构建、进程崩溃影响和跨平台维护 | 保留 CLI adapter 和 portable engine；实验性 FFI 不算受支持产品入口 | 有原生消费方或实测进程开销不能接受 |
| D6 | WASM 完整 HDR/广色域及与服务端所有参数对等 | 高；需要维护编码器构建、绑定、metadata 与高位深通路 | 浏览器能力显式受限；不支持的控制不应伪装生效。输入校验和准确提示仍应做 | 本地隐私/离线场景要求并愿意承担独立 WASM 工具链维护 |
| D7 | 超大图完整 tile/stream 管线、跨平台硬资源隔离与强制中断所有 Rust 算子 | 高；需要重构算子生命周期及进程/平台控制 | 先做尺寸/内存上限、有界 I/O 和部署资源限制；不能把临时目录称为安全沙盒 | 大图规模或不可信多租户场景已明确，现有预算限制不足 |
| D8 | 任意 EXIF/XMP/HDR 母带与任意多 item 容器的完整保留 | 中至高；存在隐私策略、结构关联及格式差异 | 先兑现 Orientation、已支持 ICC/CICP；不承诺其他标签完整保留。当前错误标注仍需修复 | 有具体相机/归档/专业 HDR 需求及对应 roundtrip 验证 |
| D9 | 完整 12/16-bit YUV、CMYK 和任意浮点工作流 | 中至高；每个算子和编码器都需要扩展与回归 | 支持集之外拒绝或明确降级；修复 R3 的误读不依赖这些新能力 | 真实素材占比和交付要求证明需要，而不只是类型枚举中已有名称 |

暂缓不意味着可以静默丢弃数据后承诺“保真”。是否拒绝、仅导出主图或明确降级，必须成为调用方能理解的行为。

## 实施顺序

1. **已完成第一轮止损：** R2/R3 的精度、Alpha、布局错误；R4 的请求被忽略；R8 的 `"none"` 变 ACES；R6 的 CLI 管道互锁。
2. **已完成部分颜色收敛：** R1/R7 现在共享源 HDR transfer 和输出判断，但完整 gamut conversion、ICC/TRC 全组合仍不宣称支持。
3. **已完成租约低风险修复：** R5 heartbeat 续期 PEL；attempt 产物隔离保留为暂缓。
4. **下一步：** 固定工具链下补产物级组合回归，统一 Web 新建/重试归一化，并接入输入尺寸、输出尺寸和工作内存上限。
5. **简化原则：** 继续复用现有 `image`、`tokio`、Redis Streams 和 validator；没有明确收益不新增通用 codec/颜色框架。

## 核验记录

2026-09-28 的初始审查在当前工作区执行 `rtk cargo test --workspace`：102 项通过；仓库外临时 harness 复现了 12 项异常行为。下表是修复前的历史记录，不是当前结果。

本轮当前工作区已验证：`cargo check -p compute-engine -p compute-server-adapter -p compute-types -p compute-worker` 通过；`compute-engine` 54 项测试通过；`compute-types` 12 项测试通过；adapter 的 CLI 管道、Alpha 和参数单测通过。依赖外部工具的 ICC 端到端用例在默认测试线程栈上会溢出，提高 `RUST_MIN_STACK` 后通过，仍需单独降低该测试的栈占用或调整测试配置。

| 复现 | 实际结果 |
| :--- | :--- |
| 仅增加 depth/chroma/dither 的 `color` 对象 | 已知 PQ 源输出标签从 `9/16/1` 变成 `1/13/1` |
| PQ + ACES → AVIF 像素处理与标签 | 像素仍匹配 PQ 编码结果，标签却为 sRGB |
| 等价 RGB16/RGBA16 → 10-bit 420 | 前者 Y 为 `[156,156]`，后者为 `[156,157]` |
| 等价 Gray16/RGB16 → 10-bit 422 | 前者 Y 为 `[156,156]`，后者为 `[156,157]` |
| Gray16 resize | 输出变为 RGBA8 |
| Gray+Alpha16 PNG → AVIF | 输出没有 Alpha 平面 |
| RGBA16 请求 10-bit 420 → AVIF | 实际 12-bit 444，Alpha 保留 |
| AVIF 8-bit 420 源显式请求 engine depth=10/yuv=444 | native 输出仍为 8-bit 420 |
| `C444p12` 中性样本读取 | 被探测成 8-bit，得到错误 RGBA `[0,82,0,255]` |
| 源 nclx M=0 | native 标签解析成 M=1 |
| HDR 源 + `profile_policy=strip` | 源 nclx 从可读取变为 `None` |
| 子进程先写 1 MiB stderr 再读 1 MiB stdin | 顺序 I/O 在 2 秒外层 watchdog 下仍未完成 |

未完成的验证：多 Worker/Redis/S3 故障注入、完整 HDR 显示/感知指标评估、WASM 浏览器端到端、全部 HEIC/JXL 组合与部署版本矩阵。不得把本页当成这些测试已经通过。

## 已修复与本次文档修订

- 先前发现的 YUV422 像素循环内重复整图转换已移到循环外，不再列为当前缺陷；R2 是不同的像素类型覆盖问题。
- CLI 已有 watchdog、`kill_on_drop` 与 Worker 取消轮询，不再写成“没有超时/取消”；R6 是剩余的具体边界。
- Gain Map 已明确不支持，AVIF 的 RGBA Alpha PNG 路径已经存在；不能再笼统说“所有 AVIF 输出都丢 Alpha”。
- 本轮同步修订色彩管线、能力路线图、已知限制、编解码架构、元数据说明、可靠性说明和质量基准，区分现状、缺陷、验证目标与暂缓事项。
- 本轮代码修复了 16-bit RGB/Gray YUV 降级、16-bit Gray resize 降级、Gray+Alpha AVIF 丢 Alpha、未知 Y4M 布局误读、Identity 矩阵丢失、AVIF Alpha 分支跳过参数、native 忽略 depth/chroma、CLI 双向管道互锁、Web `"none"` 误判和 Worker PEL 租约不续期。
- 本轮没有实现 attempt 隔离的最终 S3 产物、完整 ICC/gamut conversion、12/16-bit YUV、超大图 tile/硬隔离和完整 WASM 对等；这些边界仍按 D1/D6/D7/D9 记录。
