# EXIF 元数据迁移记录：手写 TIFF/容器解析 → `kamadak-exif`（EXIF Metadata）

本文档记录 `compute-engine` 的 EXIF orientation 读取从手写「容器定位 + TIFF IFD 遍历」迁移到 `kamadak-exif` 0.6 的方案与落地状态，是「计算模块可用库评估」落地清单的第 4 项。

---

## 一、背景与发现

迁移前 `metadata.rs` 手写实现了 5 个容器定位函数（JPEG `APP1`、PNG `eXIf`、WebP `EXIF`、ISOBMFF `Exif` box）加一个 TIFF IFD0 遍历，用来取 orientation（tag `0x0112`）。

**迁移过程中确认了一个真实缺陷**：旧实现的 ISOBMFF 分支按「名为 `Exif` 的 box」查找，而真实 AVIF/HEIC（含 iPhone HEIC 与 libavif/avifenc 输出）按规范把 EXIF 存为 **item**：由 `meta → iinf`（`infe` item_type=`Exif`）声明、`meta → iloc` 定位到 `mdat`/文件偏移。经核对 `scripts/fixtures/external/aom_seine_*.avif`：`ftyp` 兼容 brand 含 `mif1`，EXIF 位于 `infe` item（item_id=4）+ `iloc`(offset=645, len=950)，**顶层并不存在 `Exif` box**。因此旧实现从真实 AVIF/HEIC 中永远读不到 orientation，手机竖拍图在 HEIC 路径上会静默丢旋转。

`kamadak-exif` 0.6.1（BSD-2，3.1M 下载/月，13.7M 累计，15 年历史的纯 Rust 事实标准）按规范实现了 `iinf`/`iloc` 查找与 `Exif\0\0`/4 字节偏移处理，同时覆盖 JPEG/PNG/WebP/TIFF/HEIF，本次迁移同时完成简化与修复。

## 二、进度对照矩阵（Status Matrix）

| 维度 | 项目 | 状态 | 对应位置 | 说明 |
| :--- | :--- | :---: | :--- | :--- |
| 依赖 | 引入 `kamadak-exif` 0.6（BSD-2，纯 Rust） | ✅ 已完成 | `compute/crates/engine/Cargo.toml` | 支持 wasm32，无 C 依赖 |
| 提取 | orientation 统一走 `Reader::read_from_container` | ✅ 已完成 | `metadata.rs::extract_exif_orientation` | `Cursor` 提供 `BufRead + Seek` |
| 提取 | HEIF/AVIF 规范支持（`iinf`/`iloc` item） | ✅ 已完成 | 同上 | **修复**旧实现的顶层 `Exif` box 假设 |
| 提取 | 仍返回 `Option<u8>` 且过滤 `1..=8` | ✅ 已完成 | 同上 | 调用方 API 不变 |
| 清理 | 删除 5 个容器定位函数 + TIFF IFD 遍历（约 135 行） | ✅ 已完成 | `metadata.rs` | 仅保留 ISOBMFF 盒子工具供 nclx/auxC 使用 |
| 保留 | `probe_nclx` / `probe_alpha_channel` 手写实现 | ✅ 已完成 | `metadata.rs` | 待 `mp4parse` 迁移统一处理 |
| 测试 | 规范化的最小 HEIF fixture（`iinf`/`iloc`） | ✅ 已完成 | `metadata.rs` tests | 替代旧的顶层 `Exif` box 合成件 |
| 测试 | 真实 JPEG fixture orientation | ✅ 已完成 | 同上 | `webkit_iceland_p3.jpg` → `Some(1)` |
| 兼容 | WASM 引擎构建 | ✅ 已完成 | `compute-wasm` | `wasm32-unknown-unknown` check 通过 |

## 三、关键设计决策

1. **`read_from_container` 而非 `parse_exif`**
   `read_from_container` 自带容器嗅探（TIFF/JPEG/PNG/ISOBMFF/WebP），一次调用即可覆盖全部输入类型；`Cursor::new(data)` 同时满足 `BufRead + Seek`，无需临时文件。

2. **保持 `Option<u8>` 契约**
   `kamadak-exif` 返回 `Field`/`Value`；本函数只取 `Tag::Orientation`（`In::PRIMARY`）的无符号值与 `1..=8` 过滤，语义与迁移前完全一致，调用方（`apply_exif_orientation`、worker）无需改动。

3. **ISOBMFF 盒子工具保留**
   `find_nclx`/`find_box_payload`/`parse_colr` 仍服务于 CICP matrix 与 alpha `auxC` 探测，等 `mp4parse` 迁移时一并替换。

4. **测试 fixture 规范化**
   旧的 `isobmff_with_colr_and_exif` 合成件（顶层 `Exif` box）在真实文件里不存在，已拆分为：
   - `isobmff_with_colr`：仅 `ftyp + colr`，服务 nclx/alpha 负例；
   - `heif_with_exif`：规范最小 HEIF（`ftyp` 含 `mif1` 兼容 brand → `meta`(v0) → `iinf`(`infe` v2, item_type `Exif`) + `iloc`(绝对偏移) → 末尾 item 数据），与手机 HEIC 结构一致。

## 四、行为差异

| 场景 | 迁移前 | 迁移后 |
| :--- | :--- | :--- |
| 真实 AVIF/HEIC（item 式 EXIF） | **读不到 orientation（静默丢失旋转）** | 按规范读取，正确返回 |
| 顶层 `Exif` box（非规范合成件） | 可识别 | 不再识别（非规范结构） |
| JPEG/PNG/WebP | 手写扫描 | 库扫描，语义一致 |
| EXIF 数据 > 64 KiB（ISOBMFF） | 无限制 | `kamadak-exif` 上限 `MAX_EXIF_SIZE = 65535`，超限返回 `None` |
| 损坏/截断输入 | 尽力扫描 | 返回 `None`（不 panic） |

## 五、回归验证点

- `cargo test -p compute-engine`：50 passed，其中 metadata 相关：
  - `reads_jpeg_orientation` / `reads_png_orientation` / `reads_webp_orientation`
  - `reads_heif_orientation`（规范 `iinf`/`iloc` fixture）
  - `reads_real_jpeg_fixture_orientation`（WebKit P3 JPEG → `Some(1)`）
  - `ignores_upright_and_invalid_orientation`（含非图像与截断 JPEG）
  - `reads_nclx_matrix` / `ignores_unknown_matrix_and_non_isobmff` / `detects_alpha_*`（未受影响的路径）
- `cargo test --workspace`：全绿（含 server-adapter 的 HEIC/AVIF CLI e2e）。
- `cargo check -p compute-wasm --target wasm32-unknown-unknown`：通过。
- `cargo clippy -p compute-engine`：告警总数 31 → 30，新增代码零告警。

## 六、后续

- 下一项：`moxcms`（ICC/CICP 转换，补上当前刻意省略的 primaries 转换能力）。
- 之后：`mp4parse`（ISOBMFF `colr`/`auxC`，届时 `metadata.rs` 的手写盒子工具与 `icc.rs::extract_icc_avif` 可一并移除）。
