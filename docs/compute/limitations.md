# 已知限制

本页汇总当前实现明确不支持或降级的能力，以及背后的原因。无消费方、无实现路径的功能不在此列（相关代码已删除，见[色彩路线图](/compute/color-roadmap)）。

> 2026-09-28：[实现审查与取舍](/compute/engineering-review) 单独列出需要修复的实现错误、简化建议和暂缓项。下列产品边界不能用来掩盖已接收请求却静默输出错误结果的缺陷。

## 仍需完善的实现边界

| 问题 | 当前影响 | 审查编号 |
| :--- | :--- | :--- |
| 源颜色与输出意图混用 | 像素路径已共享输出颜色决策，修复 tone-mapped JXL 标签并拒绝冲突 CICP；完整 ICC/TRC 和广色域到 sRGB 转换仍未支持 | R1/R7 |
| 多 Worker 执行所有权 | PEL heartbeat 已续租，单槽 Worker 每次仅认领一个任务；旧 attempt 与新 attempt 仍可能写同一产物对象 | R5 |
| CLI I/O 与资源边界 | 双向管道、有界诊断和统一 deadline 已修复；输入/像素/工作内存预算及同步算子取消仍需完善 | R6 |
| 参数往返和校验 | Rust/Go 已拒绝当前能力之外的 AVIF 组合；Web 新建/重试仍有重复归一化逻辑，需继续合并 | R8 |

本轮已修复的 R2-R4 边界详见下节；这些剩余项不属于“因成本高而暂不支持”，应通过实现或验证继续收敛。

## 本轮已修复的边界

- RGB/RGBA/Gray/Gray+Alpha 的 16-bit YUV、resize、PAM 和 Alpha 判断不再无意降为 8-bit 或丢透明。
- Y4M 对 p12/p16/未知布局明确拒绝；CICP Identity (`M=0`) 会继续传递，不回退为 BT.709。
- AVIF Alpha、native、Y4M 路径共用有效 depth/chroma 解析；legacy `engine_params` 仍受支持，显式 `tone_mapping=none` 不会阻断 native。
- CLI stdin 与 stdout/stderr 并发处理，使用统一 deadline 和有界诊断缓存；超时后会等待子进程退出。
- Worker heartbeat 通过 Redis `XCLAIM` 续租计算流 PEL。
- Web `"none"` 不再误判为 ACES；未知 tone mapping 不再静默变成 ACES。

## 浏览器 WASM 沙盒

| 限制 | 说明 |
| :--- | :--- |
| 无法写入 ICC / CICP | `image`/`ravif` 的 AVIF 编码器没有 ICC 接口；`@jsquash/jxl@1.3.0` 的 `EncodeOptions` 无色域字段；JXL 的颜色编码位于码流头部，无法像容器 box 那样事后注入 |
| 不做色域转换 | 沙盒按源图描述解析后**原样输出像素**；广色域源图不带色彩标识，UI 提示“可能偏饱和，保留请用服务端” |
| `heic` 目标不可用 | 格式字符串可解析，但引擎编码直接返回错误 |
| `jxl` 目标依赖 JS 编码器 | `compute-engine` 无纯 Rust JXL 编码器，Worker 走 `@jsquash/jxl` |

## 服务端管线

| 限制 | 说明 |
| :--- | :--- |
| EXIF / XMP 不完整保留 | 仅读取并烘焙 Orientation；相机参数、GPS、XMP 在“解码 → 重编码”路径丢失（JPEG → JXL 频域直通除外） |
| HDR 母带元数据未实现 | Mastering Display / CLLI 不做保留或写入 |
| Gain Map 不支持 | Apple / Google HDR Gain Map 探测代码已删除（无消费方，见[色彩路线图](/compute/color-roadmap)）；双层 JPEG 仅解码主图。**注意**：原生 YUV 直通目前没有 Gain Map 门禁（Phase A 未实施），带增益图的 AVIF 走直通会丢辅助图元 |
| 颜色管理不是完整 CMS | 像素处理、AVIF CICP 与 JXL 标签共享输出决策；已知 nclx transfer 优先于请求默认值。任意 ICC/TRC、广色域转换和未覆盖的标签组合仍见 R1/R7 |
| 广色域 tone mapping 输出受限 | AVIF/JXL 可保留基色并改为 sRGB transfer；JPEG/PNG/WebP/HEIC 尚不能生成相应新 profile，当前明确拒绝此类请求 |
| 不做色域转换 | 产品定位为转码/压缩工具，`ColorSpace` 仅描述源图；广色域通过 ICC/CICP 透传，不重写像素 |
| 无纯 Rust JXL 编码器 | 编码依赖 `cjxl`（缺失时无法输出 JXL）；`jxl-oxide` 只用于解码 |
| 动图暂缓 | GIF / APNG / 动图 WebP / 动图 AVIF 的多帧时间轴支持未实现 |
| Path C 进阶直通暂缓 | 仅当平面布局完全一致时直通；`MatrixOnly` 仍回退像素路径；原始码流零解码直通未实现 |
| 外部 CLI 依赖 | 精确位深/ICC/频域直通依赖相应 CLI；AVIF 的 10-bit、ICC、非默认颜色编码和 WebP ICC 在缺工具时直接失败，不能静默丢弃。工具可用性按进程缓存，安装后需重启 Worker |
| 完整 12/16-bit YUV 未支持 | 当前平面像素路径只实现 8/10-bit；其他布局应拒绝或明确回退，当前误读问题见 R3 |
| 完整显示相关 HLG 未接入 | `hlg_ootf` 有函数和测试，但生产管线没有调用，不能据此宣称完整 OOTF 支持 |

## 接口与工程

| 限制 | 说明 |
| :--- | :--- |
| HTTP API 无版本号 | 路由固定 `/api`；部分历史端点字段大小写不一致（详见 [HTTP 契约](/contracts/http-api)） |
| 队列协议无 Schema 生成 | `queue.go` 与 `protocol.rs` 为双侧手写镜像，只能靠成对审查与测试保证一致 |
| e2e 测试依赖宿主 CLI | 本机缺少 CLI 时相关 e2e 用例会跳过（打印 skip），CI 应显式安装工具链 |
| 活跃 Worker 统计依赖 `transcoder:compute:group` | 该消费组由 Rust Worker 首次消费时创建；服务端不会主动创建，缺失时统计恒为 0 |

## 刻意不做的事

- **色域转换**：不提供 P3/BT.2020 ↔ sRGB 像素映射
- **HDR → HDR 合成**：不做 Gain Map 合成或 torch 重建
- **多帧/时间轴**：不处理动图与视频时间维度
- **为无消费方的功能保留代码**：状态判定标准是“存在调用方”，不是“存在实现”

完整 Gain Map、色域转换、多帧、C-ABI 扩展、WASM HDR 对等、超大图分块及任意 metadata 保留暂不投入。各项成本、当前替代边界和重新启动条件见[明确暂缓的能力](/compute/engineering-review#明确暂缓的能力)。输入安全检查、准确标签、拒绝不支持的请求不在暂缓范围内。
