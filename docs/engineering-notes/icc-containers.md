# ICC 容器读写迁移记录：手写解析 → `img-parts`（ICC Container I/O）

本文档记录 `compute-engine` 的 ICC Profile 提取/注入从手写字节扫描迁移到 `img-parts` 0.4 的方案与落地状态，是「计算模块可用库评估」落地清单的第 2 项（第 1 项见 [输入格式探测](./input-detection)）。

---

## 一、背景

迁移前 `engine/src/color/icc.rs` 手写实现了：

- JPEG `APP2` 分片扫描与拼接（含 `ICC_PROFILE\0` seq/total 校验）
- PNG `iCCP` 解析（zlib 解压）与注入（自算 CRC-32、miniz 压缩）
- WebP `ICCP` RIFF 块扫描
- AVIF/HEIC `colr` box 递归查找

其中 JPEG/PNG/WebP 三块都是成熟容器问题，`img-parts` 0.4（MIT/Apache，1M 下载/月，10.8M 累计，纯 Rust，`bytes`+`crc32fast`+`miniz_oxide` 依赖）已覆盖并有结构化 API。

## 二、进度对照矩阵（Status Matrix）

| 维度 | 项目 | 状态 | 对应位置 | 说明 |
| :--- | :--- | :---: | :--- | :--- |
| 依赖 | 引入 `img-parts` 0.4，移除直接依赖 `miniz_oxide` | ✅ 已完成 | `compute/crates/engine/Cargo.toml` | miniz 由 img-parts 传递引入 |
| 提取 | JPEG/PNG/WebP 统一走 `DynImage::from_bytes` + `ImageICC::icc_profile` | ✅ 已完成 | `color/icc.rs::extract_icc_from_memory` | 删除 3 个手写扫描函数约 120 行 |
| 提取 | AVIF/HEIC `colr` 保留本地实现 | ✅ 已完成 | `color/icc.rs::extract_icc_avif` | img-parts 不覆盖 ISOBMFF，待 `mp4parse` 迁移处理 |
| 注入 | JPEG：结构化重建 + 分段 `APP2` 写在 `SOS` 之前 | ✅ 已完成 | `color/icc.rs::inject_icc_into_jpeg` | 不再字节拼接，自动替换已有 ICC |
| 注入 | PNG：`set_icc_profile` 插在 `IHDR` 之后 | ✅ 已完成 | `color/icc.rs::inject_icc_into_png` | 移除手写 CRC-32 与 zlib 压缩 |
| 注入 | 非法/退化输入原样返回，不 panic、不产出损坏文件 | ✅ 已完成 | 两处 injection | 解析失败、空 profile、超大 profile、无 `SOS`、无 `IHDR`/`IEND` 全部早退 |
| 分类 | ICC colorant 识别（sRGB/P3/AdobeRGB/ProPhoto）保留手写 | ✅ 已完成 | `classify_icc_color_space` | 非容器问题，无主流库等价物 |
| 测试 | 单测覆盖 roundtrip / 多分片 / 替换 / 非目标格式 / 退化流 | ✅ 已完成 | `color/icc.rs` tests | 共 8 个用例 |
| 测试 | 真实相机/浏览器 fixture 回归 | ✅ 已完成 | `test_classify_icc_color_space`、server-adapter e2e | WebKit P3 PNG、P3 JPEG/PNG/AVIF 注入链路全绿 |
| 兼容 | WASM 引擎构建 | ✅ 已完成 | `compute-wasm` | `cargo check --target wasm32-unknown-unknown` 通过 |

## 三、关键设计决策

1. **提取统一入口 `DynImage`**
   `DynImage::from_bytes` 按文件签名分派，未知格式返回 `Ok(None)`；配合 AVIF/HEIC 前缀判断（`ftyp`/`meta`）分流到本地 `colr` 解析，`extract_icc_from_memory` 收敛为 10 行。

2. **JPEG 注入不用库的 `set_icc_profile`**
   `img-parts` 的实现固定 `segments.insert(3, ..)`：当 JPEG 头部段数 < 4 时，
   - 段数 < 3 → `Vec::insert` 越界 panic；
   - 段数 == 3 且 `SOS` 位于下标 2 → ICC 会落到 `SOS` 之后，产出损坏文件。
   本实现改为：先 `retain` 掉既有 ICC 段，定位 `SOS`，把分片段按顺序插在其前（无 `SOS` 则原样返回）。分片大小沿用 ICC 规范上限 `65519` 字节，profile 超过 `65519 × 255` 时不可寻址，直接原样返回。

3. **PNG 注入复用库的 `set_icc_profile`**
   它会把 `iCCP` 插在下标 1（即 `IHDR` 之后），与规范一致，并负责 zlib 压缩；调用前校验 `IHDR` 在首位且存在 `IEND`，避免在残缺流上重建。

4. **重建而非字节拼接的取舍**
   - 收益：自动替换重复/过期的 ICC、CRC 重算、结构校验；
   - 代价：紧跟在 `EOI`/`IEND` 之后的尾随垃圾字节会在重建时丢弃。本仓库两个注入点的输入都是自家 `image` 编码器产物，不存在该情况。

## 四、行为差异（相对迁移前）

| 场景 | 迁移前 | 迁移后 |
| :--- | :--- | :--- |
| 已带 ICC 的 JPEG/PNG 再注入 | 追加第二份 profile（重复段） | 替换，只保留最新一份 |
| JPEG `APP2` 分片 count 不完整 | 校验 `total` 不匹配返回 `None` | 按 seqno 排序拼接（更宽松） |
| 流结构损坏/截断 | 扫描到多少算多少 | 解析失败 → 视为无 ICC（更严格） |
| PNG `iCCP` 写入 | profile 名 `ICC Profile`，zlib level 6 | profile 名 `icc`，zlib level 10（体积更小） |
| 非图像/空 profile | 原样返回 | 原样返回（不变） |

## 五、回归验证点

- `cargo test -p compute-engine`：43 passed（新增 8 个 ICC 单测，含两段大 profile 的分片 roundtrip、二次注入替换、退化流不 panic）。
- `cargo test --workspace`：43 + 33 + 12 + 1 全绿；CLI e2e 中 ICC 链路真实执行：
  - `test_e2e_icc_injection_avif_and_jxl`（真实 Display P3 profile 经 JPEG 提取 → AVIF `colr` 校验）
  - `test_e2e_p3_wide_gamut_preserved_avif_and_jxl`（WebKit fixture 走真实 CLI 编码）
  - `test_e2e_webp_preserves_icc`（PNG 注入 → cwebp `-metadata icc` → WebP 提取比对）
- `cargo check -p compute-wasm --target wasm32-unknown-unknown`：通过（引擎保持 WASM 可用）。
- `cargo clippy -p compute-engine`：新增代码零告警（`icc.rs` 仅剩迁移前已有的 `chunks_exact` 提示）。

## 六、后续

- 下一项：`fast_image_resize` 替换 `edit.rs` 的 `image::imageops` 重采样（linear f32 premultiplied 工作区可直接复用）。
- 之后：`kamadak-exif`（orientation）、`moxcms`（ICC/CICP 转换）、`mp4parse`（ISOBMFF `colr`/`auxC`，届时可移除本地 `extract_icc_avif`）。
