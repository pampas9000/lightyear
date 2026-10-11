# 色彩管线路线图

本页记录色彩/元数据管线的功能状态与后续规划。**现状规范**见[色彩管线](/compute/color-pipeline)、[元数据与 Alpha](/compute/metadata)；无法兑现的能力单独列在[已知限制](/compute/limitations)。

> 2026-09-28 复核：有真实调用方只是必要条件，不能代替组合验证。仅有函数/测试但未接入生产路径，不算能力已交付。当前缺陷、简化建议、成本和暂缓条件统一记录于[实现审查与取舍](/compute/engineering-review)，本页不再以功能勾选数量衡量成熟度。

## 状态矩阵

| 维度 | 能力 | 状态 | 位置 | 说明 |
| :--- | :--- | :---: | :--- | :--- |
| 色深 | 有效位深与存储容器解耦（`nominal_bit_depth`） | ✅ | `compute-types::transcode::color` | 逻辑位深独立于像素容器 |
| 色深 | PAM 16-bit 裸流 | ✅ | `engine::color::yuv::write_pam_rgba` | `MAXVAL 65535` 大端，供 `cjxl` 高位深输入 |
| 色深 | AVIF 解码 10-bit 保护 | ✅ | `server-adapter::transcode::decode` | 移除 `zenavif` 后统一走官方 `avifdec` → Y4M → `ImageRgba16` |
| 色深 | Y4M 10-bit 序列化（C444/C420/C422） | ✅ | `engine::color::yuv` | 严格 0..1023 采样值域 |
| 色深 | 输出位深约束 | ✅ | `types` + `server-adapter::transcode::encode` | AVIF 公共入口先解析有效 8/10-bit、full-range 与抽样，再选择 Alpha、native 或 Y4M 路径；12/16-bit YUV 仍不支持 |
| 色深 | 高位深 → 8-bit 蓝噪声抖动 | ✅ | `engine::dither` | `dither: bool` 单开关，Blue Noise 64×64 |
| 色彩空间 | sRGB / BT.709 / HLG / PQ 可逆变换 | ✅ | `engine::color::transfer` | 全阶往返测试 |
| 色彩空间 | HLG OOTF 公式 | ⏸️ 未接入 | `engine::color::transfer::hlg_ootf` | 仅函数、导出与测试，完整显示处理暂缓 |
| 色彩空间 | 编辑管线传递函数自适应线性化 | ✅ | `engine::edit::apply_pipeline_with_transfer` | 按源 `TransferCharacteristics` 选择 EOTF |
| 色彩空间 | ACES Filmic 色调映射 | ⚠️ 部分完成 | `engine::tonemap` + adapter | transfer 选择与输出标签已收敛；完整 ICC/广色域到 sRGB 的基色转换仍不支持 |
| 抽样 | 矩阵与色域解耦 | ✅ | `engine::color::yuv` | 按 `MatrixCoefficients` 选用 BT.601/709/2020 |
| 抽样 | Full Range 锁定 | ✅ | `engine::color` + adapter | Y4M `XCOLORRANGE=FULL`，CLI `-r full` |
| 抽样 | 优先无压缩中间流 | ✅ | `server-adapter::transcode` | Y4M/PAM；Alpha、HEIC/cwebp 文件输入允许无损 PNG 中转 |
| 抽样 | 4:2:2 支持 | ✅ | `engine::color::yuv` + adapter | 8/10-bit 平面提取与 Y4M C422 |
| 抽样 | 内容自适应抽样（Auto） | ✅ | `engine::color::chroma` | 色度梯度能量判决，<1ms |
| 抽样 | 原始 YUV 码流零解码直通（Path C） | ⏸️ | — | 前置条件苛刻（无编辑 + 矩阵/Range 一致），保持架构预留 |
| 色域 | CICP 标准三元组 | ✅ | `types::Cicp` | `P/T/M` 输出（如 P3 `12/13/6`） |
| 色域 | 色域转换 | 🚫 已移除 | — | 产品决策：只描述不转换（见 §已删除） |
| 元数据 | ICC 提取与注入 | ✅ | `engine::color::icc` + adapter | JPEG/PNG/WebP/AVIF，受 `ProfilePolicy` 管控 |
| 元数据 | EXIF Orientation | ✅ | `engine::color::metadata` | `kamadak-exif` 读取并烘焙 |
| 元数据 | EXIF 其余标签 / XMP / HDR10 元数据 | ⏸️ | — | 未实现；JPEG→JXL 直通除外 |
| 元数据 | HDR Gain Map | 🚫 已删除 | — | 无消费方的 probe 层按 YAGNI 删除 |
| 管线 | JPEG → JXL 频域直通 | ✅ | `server-adapter::transcode::bypass` | 6 项条件严格匹配，Brunsli 重构 |
| 管线 | 原生 YUV 直通（HEIC/AVIF → AVIF） | ⚠️ Experimental | `server-adapter::transcode::native` | 已支持有效参数门禁；Gain Map、MatrixOnly 和完整工具版本矩阵仍不在范围内 |
| 管线 | 色度上采样双线性重构 | ✅ | `engine::color::yuv` | `yuv` crate（libyuv 同源），替换最近邻 |
| 管线 | Alpha 解码保护 | ✅ | `engine::color::metadata` + decode | `auxC` 探测，带 Alpha 自动改走 16-bit PNG |
| 管线 | 输入格式 magic-byte 探测 | ✅ | `server-adapter::transcode::detect` | `infer` 三级解析，字节优先 |
| 管线 | 工具能力动态探测 | ✅ | `server-adapter::transcode::probe` | `ToolCapabilities`，strict 阻断 / 非 strict 降级 |
| 格式 | HEIC/HEIF 输入解码 | ✅ | `server-adapter::transcode::decode` | `heif-dec` 直出 Y4M，零 C 依赖污染 WASM |
| 格式 | 纯 Rust JXL 编码器 | ⚠️ 生态限制 | — | 编码依赖 `cjxl` / WASM 侧 `@jsquash/jxl` |
| 质检 | 素材审计与测试入口 | ✅ | `scripts/benchmark.ts` | 素材头断言 + workspace 测试；不是全量转码产物画质/性能门禁 |
| Web | WASM 动态加载与本地转码 | ✅ | `web/app/workers` + `compute-wasm` | 动态 `import()`，Worker chunk 按需加载 |
| Web | WASM 广色域保留 | ⚠️ 生态限制 | — | 浏览器编码器无 ICC/CICP 写入能力，见[已知限制](/compute/limitations) |
| 全栈 | Go 参数校验与前端面板同步 | ⚠️ 部分完成 | `server/internal/transcode` + Web | 后端已收敛到当前实际支持范围；Web 新建/重试仍有可合并的归一化逻辑 |
| 动图 | 多帧流式转码 | ⏸️ 暂缓 | — | 当前聚焦静态超高清与 HDR |

本轮已闭合 R2-R4 的主要跨路径边界：RGB/RGBA/灰度/灰度 Alpha 的 16-bit 工作表示、Alpha 检测、Identity metadata、未知 Y4M 布局拒绝，以及 AVIF Alpha/native/Y4M 的有效参数解析已接入实际入口。完整 12/16-bit YUV、所有外部工具版本组合和产物级组合门禁仍不宣称已完成。

## 已删除（Surface Narrowing）

以下功能曾有代码但无调用方，已按“不加无消费方功能”原则移除：

- **色域转换**：`engine::color::gamut` 模块、6 个 3×3 基色矩阵、`ProfilePolicy::ConvertToTarget`（前后端同步删除）
- **HDR Gain Map**：`gain_map` 模块、`GainMapMetadata`、`SourceProfile.gain_map`、Worker 探测调用
- **XYB 感知色彩模型**：`linear_rgb_to_xyb` 及其 LMS 常量（JXL 编码走 `cjxl`，引擎自持实现是负资产）
- **零引用类型**：`MetadataBundle`、`ContentLightLevel`、`MasteringDisplayMetadata`、`PixelFormat`、`ChannelLayout`、`SampleStorage`、`AlphaMode`
- **枚举降维**：`ToneMappingOperator` 收敛为 `{None, AcesFilmic}`；`DitherAlgorithm` 改为 `dither: bool`
- **旧生态依赖**：`zenavif`、`zenpixels`、`zune-jpegxl`、`zune-core` 全部剔除

历史落库参数仍可解码：`reinhard`/`hable` → `AcesFilmic`，`blue_noise`/`floyd_steinberg`/`bayer` → `true`。

## 已有实现（不代表所有组合通过）

- 4:2:2 色度抽样与 Y4M C422 封装打通（8/10-bit）
- HEIC 原生服务端解码（`heif-dec` 直出 Y4M，WASM 保持纯 Rust）
- 色调映射收敛为 `ToneMappingOperator::{None, AcesFilmic}`
- 抖动收敛为 `dither: bool`（固定 Blue Noise）
- 源 ICC 二进制提取与 CLI 注入通道（AVIF/ JXL/WebP/JPEG/PNG）
- AVIF 输出 Alpha（PNG16 中转 + `--qalpha`，端到端测试覆盖）
- 内容自适应抽样判决
- HLG OOTF 公式与单元测试，尚未接入生产链路
- Go 校验、Web 类型和面板已有实现；后端支持范围已收敛，Web 新建/重试的共享归一化仍可简化

## 后续顺序

1. **P1 颜色契约**：补齐 ICC/TRC、JXL 与 tone mapping 的组合回归；完整色域转换仍单独暂缓（R1/R7）。
2. **P1 多 Worker 可靠性**：实现 attempt 级产物隔离，并完成 Redis/S3 故障注入验证（R5）。
3. **P2 运行边界**：接入输入尺寸、输出尺寸和工作内存预算；同步 Rust 算子仍不承诺强制中断（R6）。
4. **P2 参数与验证**：共享 Web 新建/重试归一化，补必须运行的产物回归，再接入一个感知指标（R8/R9）。
5. **随修复简化**：归并重复源探测与参数解释；不另建 provider 框架或完整管线规划器。

## 暂不排期

完整 Gain Map、色域转换/显示相关 HDR、动图视频、原始码流/MatrixOnly 扩展、全面 C-ABI、WASM HDR 对等、超大图分块、任意 metadata 和完整 12/16-bit YUV 暂不投入。各项成本、替代边界和重启条件见[明确暂缓的能力](/compute/engineering-review#明确暂缓的能力)。

消费组可观测性与错误处理可随 Worker 维护完善；它们不替代 R5 的执行所有权修复。暂缓能力不应继续作为 UI/文档中的已实现承诺。
