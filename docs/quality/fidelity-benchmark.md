# 图像保真度基准与测试规范（Image Fidelity Benchmark Specification）

本文档是 Lightyear 转码系统关于**图像保真度（Fidelity）**的完整评测规范，涵盖四个方面：

1. **评测哲学**：什么算“损失”，我们承诺什么级别的保真；
2. **指标体系**：用什么度量，判读阈值是多少，怎么跑；
3. **系统级验证目标**：需要逐步落实的 8 项能力及其当前边界；
4. **测试素材库**：45 个模拟与真实素材的逐项说明书。

---

> **当前状态（2026-09-28）**：本文包含验收目标，不是所有格式组合已经通过的能力声明。现有 harness 主要检查输入素材头并运行 Rust 测试；感知指标、全量产物矩阵尚未接入。已确认缺陷及不投入的高成本能力见[实现审查与取舍](/compute/engineering-review)，不要用本文的目标值覆盖这些限制。

# 一、评测哲学：三种“损失”与三级目标

## 1.1 图像转码中的三种损失

图像经过“解码 → 处理 → 编码”链路后，损失可以分为三个正交的层次。只测其中一种，必然遗漏另外两类问题：

| 损失类型 | 定义 | 典型来源 | 检验手段 |
| :--- | :--- | :--- | :--- |
| **数学损失** | 像素值发生任何改变 | 有损量化、降位深截断、色度二次采样 | 逐像素 AE、无损往返测试 |
| **感知损失** | 像素变了，但人眼难以察觉 | 变换域量化、去块滤波、平滑 | SSIMULACRA2、Butteraugli、PSNR-HVS |
| **结构性损失** | 元数据/色域/图层级别的信息丢失 | ICC 丢弃、色深截断、矩阵错误 | 容器头断言、元数据嗅探、CICP 校验 |

**关键认知**：一个转码器可能数学上“接近无损”（PSNR 很高），但同时犯下结构性灾难（例如把 10-bit 拍扁成 8-bit，或把 P3 色域裁剪成 sRGB）。因此三类损失必须分开验证，不能互相替代。

## 1.2 Lightyear 的三级验收目标

| 级别 | 承诺内容 | 硬性判定 |
| :--- | :--- | :--- |
| **T0 · 数学无损** | 像素与源完全一致 | AE = 0（逐像素绝对误差为零） |
| **T1 · 视觉无损** | 人眼在 1:1 与 200% 放大下均无法分辨 | SSIMULACRA2 ≥ 90 或 Butteraugli ≤ 1.0 |
| **T2 · 高保真有损** | 高压缩收益下仍保持极高观感质量 | SSIMULACRA2 ≥ 80 或 Butteraugli ≤ 2.0 |

**在声明支持的路径内，结构性约束不应随质量等级放宽**：

- 产物位深、颜色描述、矩阵和 Range 应符合已解析的请求；不支持的组合明确拒绝或降级；
- ICC 按策略处理，EXIF 当前主要处理 Orientation，其他 EXIF/XMP/HDR metadata 不承诺完整保留；
- 优先无压缩 Y4M/PAM，允许 Alpha、HEIC、cwebp 路径使用无损 PNG。中间格式应验证位深/Alpha/metadata，不能把“没有中间 PNG”作为保真的替代指标。

> **适用范围**：以上是服务端受支持路径的验收目标，当前仍有[已知实现缺陷](/compute/limitations)。浏览器 WASM 的 ICC/CICP 与高位深能力更受限，且不做色域转换；不能将服务端的 E-01/F-07 等断言直接视为浏览器能力。完整对等暂缓，见审查 D6。

---

# 二、评测指标体系

## 2.1 指标对照表

| 指标 | 测量什么 | 推荐工具 / 命令 | 适用场景 | 判读阈值 |
| :--- | :--- | :--- | :--- | :--- |
| **AE**（Absolute Error） | 逐像素绝对误差计数 | `magick compare -metric AE a.png b.png null:` | T0 无损链路终审 | 必须为 **0** |
| **PSNR** | 峰值信噪比 | `magick compare -metric PSNR a.png b.png null:`<br>`ffmpeg -i a.png -i b.png -lavfi psnr -f null -` | 快速回归、粗筛退化 | ≥ 40 dB（8-bit）视为良好 |
| **SSIM** | 结构相似度 | `magick compare -metric SSIM a.png b.png null:`<br>`ffmpeg -i a.png -i b.png -lavfi ssim -f null -` | 结构/边缘保真 | ≥ 0.99 优秀 |
| **SSIMULACRA2** ⚠️ *尚未接入 harness* | 感知相似度（JXL 社区标准） | `ssimulacra2 a.png b.png`（需 libjxl tools 或 `cargo install ssimulacra2`） | T1/T2 有损质量终审 | ≥ 90 视觉无损；≥ 80 高保真；≥ 70 良好 |
| **Butteraugli** ⚠️ *尚未接入 harness* | 心理视觉距离（JXL 官方） | `butteraugli a.png b.png`（libjxl tools） | T1/T2 有损质量终审 | ≤ 1.0 视觉无损；≤ 2.0 高保真；> 3.0 可察觉 |
| **Delta E 2000** | 色彩偏差（CIEDE2000） | `magick compare -metric DE2000 a.png b.png null:`（IM7） | 色彩/肤色/广色域验收 | < 1.0 不可察觉；< 2.0 可接受 |
| **容器头断言** | 位深 / CICP / 抽样 / Range 精确值 | `bun run benchmark`（纯字节解析 PNG/JPEG/WebP/AVIF/HEIC 头）、`avifdec --info out.avif`、`magick identify -verbose` | 结构性验证 | 精确匹配预期（见第三章） |
| **增量流量**（Δ Bitrate） | 相同质量下的体积差异 | 文件大小直接对比 | 压缩效率评估 | 同质量下越小越好 |

## 2.2 指标选用原则

1. **无损链路只认 AE**：T0 级别下 SSIM=1.0 不代表无损（同图不同编码都可能是 1.0），唯一可信的是 AE = 0；
2. **有损链路以感知指标为准**：PSNR/SSIM 对“平滑型”失真不敏感，SSIMULACRA2 与 Butteraugli 才是感知质量终审（⚠️ 二者尚未接入 `bun run benchmark`，当前需手工执行）；
3. **色彩问题用 Delta E**：肤色、广色域映射的验收优先用色彩差异指标，而非亮度主导的 PSNR；
4. **结构性验证永远单独执行**：指标再高也不能豁免容器头断言——位深被截断的图在 PSNR 上可能表现极好。

---

# 三、系统级验证目标与现状

以下目标必须以转码产物验证；不是无条件支持声明，未实现和暂缓部分应显式标注：

| # | 系统目标 | 验证方式 | 对应素材 |
| :- | :--- | :--- | :--- |
| 1 | **中间表示正确**：优先 Y4M/PAM；必要 PNG 中转保持精度、Alpha 与描述 | 路径检查 + 产物回读 + 实测耗时，非“零 PNG”口号 | F-10、F-12、F-19 |
| 2 | **位深符合请求**：不因像素类型分支意外截断；当前 R2-R4 待修复 | 回读产物位深及梯度，不只看容器位深 | F-04、F-10、E-16 |
| 3 | **颜色描述一致**：已支持的 ICC/CICP 与实际像素匹配；不宣称基色转换 | metadata + 代表性像素/渲染对照；R1/R7 待修复 | F-07、E-01、E-11 |
| 4 | **矩阵正确**：按 `MatrixCoefficients` 选用 BT.601/709/2020 公式 | 肤色 Delta E | F-08 |
| 5 | **Range 锁定**：Full/Limited 标定一致，黑阶不发灰 | 最低亮度像素值 + Y4M 标注 | F-09 |
| 6 | **自适应抽样**：UI/文字走 444，照片走 420 | 抽样判决 + 400% 放大色渗检查 | F-01、F-02、F-03 |
| 7 | **请求抖动时生效**：支持的高位深源 → 8-bit 使用蓝噪声，不承诺修复源已有色带 | 直方图 + 视觉色带检查 | F-05、F-04 |
| 8 | **HDR 与元数据边界明确**：ACES 像素/标签一致，ICC 与 Orientation 按支持范围验证；Gain Map/其余 metadata 暂缓 | 产物像素/标签检查；不支持项显式标记 | F-06、E-12~E-19 |

---

# 四、测试素材库

全库共 **45 个素材**，分三类：

| 类别 | 目录 | 数量 | 用途定位 |
| :--- | :--- | :---: | :--- |
| **A. 合成故障探测器** | `scripts/fixtures/quality/` | 19 | 针对性构造的极限工况，专抓特定管线缺陷 |
| **B. 行业权威参考素材** | `scripts/fixtures/external/` | 19 | WebKit / AOMedia / Google 官方发布的公认基准 |
| **C. 超高清真实摄影基准** | `scripts/fixtures/hd_benchmark/` | 7 | 4K 与柯达原版无损图，肉眼 1:1 画质验收 |

## 4.1 A 类：合成故障探测器（Synthetic Fault Discriminators）

> 设计哲学：每个素材都不是普通照片，而是专门让某种错误实现立刻暴露的“陷阱图”。除 F-19（由 `avifenc` 直接生成的 Alpha 夹具）外，全部由 `bash scripts/fixtures/generate_quality_fixtures.sh` 幂等生成。

**色度抽样（Chroma Subsampling）**

| # | 素材 | 规格 | 测什么 | 露馅现象 | 通过标准 |
| :- | :--- | :--- | :--- | :--- | :--- |
| F-01 | `checker_rb_444.png` | PNG 8-bit 512×512 | **4:4:4 色渗判决**：自适应抽样是否触发 444 | 降为 420 后交界出现**紫色毛刺** | 判决返回 `Yuv444`；产物头 4:4:4；400% 放大无紫边 |
| F-02 | `chroma_420_source.png` | PNG 8-bit（420 源） | **4:2:0 双重插值损耗** | 二次滤波致色度模糊平方级增长 | 差异仅来自单次量化，无“二次涂抹” |
| F-03 | `chroma_422_source.png` | PNG 8-bit（422 源） | **4:2:2 全链路**：Y4M `C422` 序列化与重构 | 垂直色度被错误压缩产生横向色带 | 往返后垂直色度逐行独立、无错位 |

**色深与量化（Bit Depth & Quantization）**

| # | 素材 | 规格 | 测什么 | 露馅现象 | 通过标准 |
| :- | :--- | :--- | :--- | :--- | :--- |
| F-04 | `gradient_gray10bit.png` | PNG 10-bit 1024 阶 | **10-bit 原生色深保护** | 截断 8-bit 丢 3/4 阶度，强烈阶梯 | 10-bit 保 1024 阶；8-bit+抖动无可见色带；采样 $0..1023$ |
| F-05 | `gradient_sunset_8bit.png` | PNG 8-bit 1024×512 | **平滑渐变回归** | 编码增加色带 | 与源比较不增加明显条带；不能用 8-bit 源证明高位深抖动有效或恢复已丢精度 |
| F-06 | `hdr_4000nits_white.png` | PNG 16-bit 256×256 | **16-bit 高光与色调映射** | 硬截断致高光死白 | ACES Filmic 映射后高光层次平滑保留 |

**色彩空间与色域（Color Space & Gamut）**

| # | 素材 | 规格 | 测什么 | 露馅现象 | 通过标准 |
| :- | :--- | :--- | :--- | :--- | :--- |
| F-07 | `p3_gradient.png` | PNG 嵌 P3 ICC 512×512 | **广色域 ICC 透传**（不转换像素） | 丢失 ICC 致 P3 数值被当作 sRGB 渲染而过饱和 | 像素未被改写；ICC 完整注入输出容器（JPEG APP2 / PNG iCCP / WebP ICCP / AVIF --icc） |
| F-08 | `skin_tone_709.png` | PNG 8-bit 512×256 | **矩阵系数正确性**（709 vs 601） | 肤色偏黄、发土、暗沉 | 肤色自然，$\Delta E < 1.0$ |
| F-09 | `limited_range_black.png` | PNG 8-bit 256×256 | **Range 标定** | Limited 黑点被当作 Full，黑场发灰 | 黑色纯净；Y4M `XCOLORRANGE=FULL`；CLI `-r full` |

**容器与格式载体（Container Carriers）**

| # | 素材 | 规格 | 测什么 | 露馅现象 | 通过标准 |
| :- | :--- | :--- | :--- | :--- | :--- |
| F-10 | `hdr_10bit_p3_pq_444.avif` | AVIF 10-bit CICP 9/16/9 | **真 HDR 容器互转** | `to_rgba8()` 把 HDR 压成 SDR | `avifdec` 直出 Y4M；`ImageRgba16` 原生；CICP 传递 |
| F-11 | `avif_8bit_420.avif` | AVIF 8-bit YUV420 | **标准 AVIF 基线**（回归锚点） | 格式不认、颜色发暗、Alpha 异常 | 解码正确、色彩不偏、Alpha 通路正确 |
| F-12 | `jpeg_420_90.jpg` | JPEG 8-bit 4:2:0 Q90 | **JPEG→JXL 重构直通** | 意外走像素级重编 | 回构 JPEG 字节一致；记录实际耗时和大小，不硬编码体积收益 |
| F-13 | `jxl_lossy_d1.jxl` | JXL VarDCT d=1.0 | **JXL 有损解码** | 解码器不支持 VarDCT 或色彩漂移 | 解码成功、无漂移、无伪影 |
| F-14 | `jxl_lossless.jxl` | JXL d=0.0 | **JXL 无损往返** | 任何微小偏差（AE>0） | 逐像素 AE = 0 |
| F-15 | `webp_lossless.webp` | WebP 无损 | **黄金锚点**：整条管线的参考标准 | 解码后像素任何变化 | 逐像素 AE = 0 |
| F-16 | `webp_80.webp` | WebP 有损 Q80 | **WebP 有损基线** | 无法解码或再转码失真加剧 | 解码正确、可转 AVIF/JXL |
| F-17 | `heic_yuv_85.heic` | HEIC 8-bit YUV | **HEIC 原生 YUV 解码** | 强制 8-bit RGBA 致偏色 | `heif-dec` 直出 Y4M；矩阵无偏差 |
| F-18 | `ultra_hdr_dual_jpeg.jpg` | 双层 JPEG（MPF+XMP） | **双层容器解码健壮性**（Gain Map 能力已移除） | 只读主图或解析越界 | 主图正常解码输出，无越界/崩溃 |
| F-19 | `avif_8bit_420_alpha.avif` | AVIF 8-bit YUV420 + Alpha | **Alpha 平面探测与解码回退** | Alpha 被静默丢弃成不透明 | `probe_alpha_channel` 命中；解码改走 16-bit PNG；半透明值保留 |

> 另有辅助中间产物：`checker.png`（棋盘生成中间态）、`hdr_seed.png`（编码种子源）。

## 4.2 B 类：行业权威参考素材（Industry Reference Fixtures）

> 全部来源于官方仓库（WebKit / AOMediaCodec / Google libultrahdr），未经二次加工。

**WebKit 官方 Color Gamut 系列（11 个）**

| # | 素材 | 规格 | 测什么 | 露馅现象 | 通过标准 |
| :- | :--- | :--- | :--- | :--- | :--- |
| E-01 | `webkit_logo_p3.png` | PNG 1000×1000 | **P3 描述与细节保留**：不做 P3→sRGB 基色转换 | 标签/处理错误造成细节丢失 | 验证 P3 profile/标签与代表性像素，并在匹配色彩管理的显示条件下检查 Logo |
| E-02 | `webkit_logo_srgb.png` | PNG 1000×1000 | Logo 测试 sRGB 对照组 | — | 作为 E-01 基准对照组 |
| E-03 | `webkit_rose_srgb.jpg` | JPEG 1000×1000 | **红玫瑰 sRGB 基线** | 红色阶度丢失、发暗 | 花瓣层次保留、色相正确 |
| E-04 | `webkit_rose_adobergb.jpg` | JPEG 1000×1000 | **红玫瑰 Adobe RGB**：源描述与细节保留 | profile 错误或花瓣层次丢失 | 源颜色描述不被错误替换；结合产物像素和显示检查 |
| E-05 | `webkit_rose_prophoto.jpg` | JPEG 1000×1000 | **红玫瑰 ProPhoto**：极限广色域压力 | 过度去饱和或硬裁剪 | 纹理保留，不“变粉色” |
| E-06 | `webkit_shoes_srgb.jpg` | JPEG 600×400 Q99 | **高饱和橙鞋 sRGB**：人眼敏感橙黄系 | 饱和度/反差变化 | 与原图一致 |
| E-07 | `webkit_shoes_adobergb.jpg` | JPEG 600×400 Q99 | **高饱和橙鞋 Adobe RGB** | 色域截断痕迹 | 无截断、观感一致 |
| E-08 | `webkit_sunset_p3.jpg` | JPEG 397×600 | **日落 P3**：渐变+广色域高光双重压力 | 天空色带或高光偏色 | 过渡连续、高光色相正确 |
| E-09 | `webkit_sunset_srgb.jpg` | JPEG 397×600 | 日落 sRGB 对照组 | — | 作为 E-08 基准对照组 |
| E-10 | `webkit_iceland_p3.jpg` | JPEG 600×400 | **冰岛风光**：天空/水面/植被多色相联合 | 整体偏色或局部溢出 | 观感一致、无偏色溢出 |
| E-11 | `webkit_flowers_prophoto.jpg` | JPEG 1200×795 | **ProPhoto 红花**：广色域源描述与细节保留 | 错误颜色解释或纹理丢失 | profile 与像素含义一致；不作为已支持 gamut mapping 的证据 |

**AOMedia HDR 与元数据系列（6 个）**

| # | 素材 | 规格 | 测什么 | 露馅现象 | 通过标准 |
| :- | :--- | :--- | :--- | :--- | :--- |
| E-12 | `aom_apple_gainmap_new.jpg` | JPEG 384×512 | **Apple 双层容器解码健壮性**（Gain Map 能力已移除） | 解析越界/崩溃 | 主图正常解码输出 |
| E-13 | `aom_apple_gainmap_old.jpg` | JPEG 384×512 | **Apple 旧版双层容器解码健壮性** | 解析越界/崩溃 | 主图正常解码输出 |
| E-14 | `aom_seine_hdr_gainmap_srgb.avif` | AVIF 400×300 | **带 Gain Map 输入的主图解码** | 解析失败/崩溃 | 当前仅验证主图可处理；Gain Map 识别、提取与保留未实现，不计为通过 |
| E-15 | `aom_seine_hdr_rec2020.avif` | AVIF 400×300 | **Rec.2020 HDR 载体** | CICP 读错或位深压缩 | CICP 正确、位深保持 |
| E-16 | `aom_weld_16bit.png` | PNG 16-bit 1024×684 | **16-bit 工业高光** + 降位深抖动 | 高光硬截断或出色带 | 位深不损；降 8-bit 无截断无色带 |
| E-17 | `aom_paris_icc_exif_xmp.png` | PNG（ICC+EXIF+XMP） | **复合元数据解析健壮性** | 解析越界、互相污染 | ICC 按策略处理且可读回；EXIF 方向可读；⚠️ EXIF 其余标签与 XMP 保留尚未实现 |

**Google AOSP 系列（2 个）**

| # | 素材 | 规格 | 测什么 | 露馅现象 | 通过标准 |
| :- | :--- | :--- | :--- | :--- | :--- |
| E-18 | `google_minnie_yuv_icc.jpg` | JPEG 320×240 | **原生 YUV + ICC 协同** | 色彩不准或 ICC 丢失 | 色彩准确、ICC 按策略处理 |
| E-19 | `google_apple_gainmap_new.jpg` | JPEG 384×512 | **双层 JPEG 主图解码健壮性** | 解析失败/崩溃 | 主图可处理；双层互操作与 HDR 重建暂缓 |

## 4.3 C 类：超高清真实摄影基准（Ultra-HD Corpus）

> 官方轻量素材（300~400px）适合 CI 与单元测试，但无法用于肉眼 1:1 验收。本类提供真实分辨率下的微观细节，用于判断有损压缩是否“抹平细节”。

| # | 素材 | 分辨率 / 大小 | 测什么 | 露馅现象 | 通过标准 |
| :- | :--- | :--- | :--- | :--- | :--- |
| H-01 | `portrait_ultra_hd_4k.jpg` | 3840×4801 / 4.8 MB | **发丝/睫毛/毛孔微观细节** | 发丝黏连发虚、肤色偏色 | 1:1 根根分离；肤色无偏；4K 完整保留 |
| H-02 | `landscape_mountains_4k.jpg` | 3840×2563 / 4.0 MB | **天空 banding 与岩石锐度双重压力** | 天空色带 或 岩石被涂抹 | 天空无色带；岩石清晰；无块效应 |
| H-03 | `architecture_lines_4k.jpg` | 3840×2563 / 2.0 MB | **高对比细线条彩边/振铃/摩尔纹** | 线条边缘彩色伪影 | 400% 放大无彩边、无振铃 |
| H-04 | `kodim04_girl_hair_2k.png` | 512×768 无损 | **经典红发肖像**（全球统一标尺） | 红色偏移、发丝层次丢失 | 红色无偏；SSIMULACRA2 符合预期 |
| H-05 | `kodim23_eagle_feathers_2k.png` | 768×512 无损 | **羽毛双向高频分叉** | 羽毛被“油画化”（smearing） | 无涂抹感、分叉清晰 |
| H-06 | `kodim01_buildings_2k.png` | 768×512 无损 | **周期性纹理**（砖墙/窗格）拍频 | 周期性伪影或拍频图案 | 纹理均匀、无周期性伪影 |
| H-07 | `kodim03_hat_fabric_2k.png` | 768×512 无损 | **低反差毛毡纤维与反光** | 纤维被量化压没“平滑成一片” | 纤维保留、无反光过曝 |

---

# 五、自动化执行与 CI 集成

## 5.1 一键运行

```bash
# 素材头信息审计 + 实测断言 + 运行引擎保真套件
bun run benchmark

# 幂等重建合成素材
bash scripts/fixtures/generate_quality_fixtures.sh
```

`bun run benchmark` **实际执行**（无硬编码断言，任一 FAIL 即退出非零）：

1. 逐个解析素材的容器头，打印真实值：格式、尺寸、位深、ICC / EXIF 方向 / CICP；
2. 对可测量的性质做断言（16-bit 素材位深 > 8、P3 素材带 ICC、HDR AVIF 为 10-bit 且 CICP 前缀为 9）；
3. 无自省能力的一律记为 `SKIP` 并在结尾列出（当前为 JXL 头解析）；**被断言的 fixture 缺失则记为 FAIL 并退出非零**，不会静默降级为 SKIP；
4. 调用 `cargo test --workspace` 并将真实结果计入成败。

**尚未接入**：SSIMULACRA2 / Butteraugli 等感知指标计算 —— 上表阈值目前是目标值，不是自动门禁。

**还不能证明**：全量素材已经被逐一转码、所有预设都符合请求、HDR 标签与像素对应、性能有稳定上限。CLI 条件测试的提前返回目前也不能通过汇总的“passed”数量识别。发布门禁应固定工具链并记录 skip，先补审查 R1-R8 的小型产物回归。

## 5.2 CI 回归建议分级

| 阶段 | 执行内容 | 耗时预算 |
| :--- | :--- | :--- |
| **每次提交** | `cargo test --workspace`（单元测试含合成素材断言） | 秒级 |
| **每日回归** | `bun run benchmark` + 合成素材逐项断言 | 分钟级 |
| **发布前验收** | 全量 45 素材 + 感知指标（SSIMULACRA2 / Butteraugli）+ 手工 1:1 检查 | 人工执行 |

## 5.3 新增素材的规范

1. **合成素材**：加入 `generate_quality_fixtures.sh`，保证幂等可重建，并在第四章 A 类表格注册（编号、露馅现象、通过标准三要素缺一不可）；
2. **外部素材**：仅收录官方仓库原始文件，不改名不加工，在 B 类表格注册来源；
3. **HD 素材**：选择具备明确高频细节特征的真实素材，在 C 类表格注册分辨率与测试焦点。

---

# 六、手工验收流程

1. **零损失校验**：F-14 / F-15 / F-12 → 逐像素 AE 必须为 0；
   ```bash
   magick compare -metric AE jxl_lossless.jxl decoded.png null:
   ```
2. **色域校验**：E-01（隐藏 Logo）、F-07（P3 渐变）→ 检查 Logo 可辨识性与色相；
3. **色深/抖动校验**：F-04、F-05、E-16、H-02 → 检查色带与高光层次；
4. **色度校验**：F-01、F-02、F-03、H-03 → 400% 放大检查色渗与彩边；
5. **细节校验**：H-01、H-05、H-07 → 1:1 检查发丝/羽毛/纤维；
6. **结构校验**：ICC/Orientation 按已支持路径检查；F-18、E-12/E-13/E-14/E-19 当前仅验证主图解码健壮性，不能视作增益图保留测试。
