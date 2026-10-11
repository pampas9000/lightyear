# 输入格式探测迁移记录：扩展名嗅探 → Magic Bytes（Magic-byte Input Detection）

本文档记录 `compute-server-adapter` 输入格式探测从「扩展名 / payload 声明」迁移到「文件头 magic bytes」的完整方案与落地状态，是「计算模块可用库评估」落地清单的第 1 项。

---

## 一、背景与问题

迁移前 `transcode::detect` 只依据 `input_format`（payload 声明）和路径扩展名判定 JPEG / AVIF / HEIC。真实失败场景：

| 场景 | 失败表现 |
| :--- | :--- |
| worker 按 S3 key 落盘，key 无扩展名（`input.tmp`） | JPEG→JXL 无损直通不触发，退化为解码+重编码 |
| 客户端误标扩展名或漏传 `input_format` | `avifdec` / `heif-dec` 选错：AVIF 丢 10-bit，HEIC 直接报工具缺失 |
| strict 模式 | 基于错误扩展名误判 `ToolCapabilities::require`，误报失败 |
| `build_cjxl_args` 的 JPEG 重建决策 | 同样被扩展名污染 |

## 二、进度对照矩阵（Status Matrix）

| 维度 | 项目 | 状态 | 对应文件 / 模块 | 说明 |
| :--- | :--- | :---: | :--- | :--- |
| 依赖 | 引入 `infer` 0.22（MIT，纯 Rust） | ✅ 已完成 | `compute-server-adapter/Cargo.toml` | 仅 native 侧引用，不影响 WASM 引擎 |
| 探测 | `InputKind` / `InputProbe` 类型 | ✅ 已完成 | `transcode/detect.rs` | `is_jpeg` / `is_avif` / `is_heic` 谓词 |
| 探测 | 三级解析：magic bytes → declared → extension | ✅ 已完成 | `transcode/detect.rs` | 字节权威，后两级仅回退，不劣化任何旧行为 |
| 探测 | 8 KiB 头部异步读取，读失败不报错 | ✅ 已完成 | `InputProbe::from_file` | 每 job 多一次 ≤8 KiB 读，可忽略 |
| 管线 | `run_transcode` 单次探测、全链路传参 | ✅ 已完成 | `transcode.rs` | 直通判定、strict 校验、native 直通、解码共用同一 probe |
| 管线 | 解码路径改用 probe | ✅ 已完成 | `transcode/decode.rs` | `decode_to_image(path, probe, ...)` |
| 管线 | 原生 YUV 直通改用 probe | ✅ 已完成 | `transcode/native.rs` | `try_native_path(path, probe, ...)` |
| 参数 | `build_cjxl_args` 纯函数化（收 `is_jpeg: bool`） | ✅ 已完成 | `transcode/args.rs` | 参数构建不再做文件 I/O，可单测 |
| 测试 | 单元测试（分类 / 回退 / 说谎扩展名） | ✅ 已完成 | `transcode/detect.rs` | 6 个用例 |
| 测试 | e2e：无扩展名 JPEG→JXL 直通 + djxl 回解 | ✅ 已完成 | `transcode/tests.rs` | 锁死本次核心回归 |
| 回退 | 保留 declared / extension 回退链 | ✅ 已完成 | `transcode/detect.rs` | `infer::is_heif` 不穷尽，必须有兜底 |

## 三、解析规则

```
magic bytes  ──(命中)──▶ InputKind
     │ 未命中
     ▼
payload declared Format ──(可映射)──▶ InputKind
     │ 未命中
     ▼
路径扩展名 ──(可识别)──▶ InputKind
     │ 未命中
     ▼
InputKind::Other（行为与迁移前完全一致：走 portable engine）
```

分类顺序显式固定，不依赖 `infer` 内部 matcher 注册顺序：

| 顺序 | 判定 | Kind |
| :---: | :--- | :--- |
| 1 | `infer::image::is_jpeg`（`FF D8 FF`） | `Jpeg` |
| 2 | `infer::image::is_avif`（`ftyp` + brand `avif`/`avis`，含 compatible brands） | `Avif` |
| 3 | `infer::image::is_heif`（brand `heic`/`heix`，或 `mif1`/`msf1` + compatible `heic`） | `Heic` |
| 4 | `infer::image::is_jxl`（box 容器 + `FF 0A` 裸码流） | `Jxl` |
| 5 | `infer::image::is_webp` / `is_png` | `Webp` / `Png` |
| — | 其余 | `Other` |

- `Heic` 与 `Heif` 统一由 `is_heic()` 覆盖（两者的解码路径都是 `heif-dec`）。
- `Jxl` / `Webp` / `Png` 目前无消费者，为后续分流（`jxl-oxide` 快路径、PNG 直出）预留。

## 四、改动清单

| 文件 | 改动 |
| :--- | :--- |
| `Cargo.toml` | 新增 `infer = "0.22"` |
| `transcode/detect.rs` | 重写：`InputKind`、`InputProbe::{from_file, from_bytes}`、`classify` + 单元测试 |
| `transcode.rs` | `run_transcode` 内一次 `InputProbe::from_file`，替换 `detect::is_jpeg/is_heic` 调用并向下传递 |
| `transcode/decode.rs` | 入参 `input_format: Option<Format>` → `probe: InputProbe` |
| `transcode/native.rs` | 同上；`try_native_path` 第二参数改 probe |
| `transcode/args.rs` | `build_cjxl_args(..., input_format: Option<Format>)` → `(..., is_jpeg: bool)`，移除文件 I/O 依赖 |
| `transcode/encode.rs` | stdin PAM 场景传 `false` |
| `transcode/bypass.rs` | 直通场景传 `true` |
| `transcode/tests.rs` | `build_cjxl_args` / `try_native_path` 调用点适配；新增 e2e |

worker 契约不变：`run_transcode` 签名保持 `input_format: Option<Format>`，`payload.input_format` 降级为回退来源。

## 五、回归验证点

已通过：

- `cargo test -p compute-server-adapter`：33 passed（含全部 CLI e2e：cjxl / djxl / avifenc / avifdec / heif-dec / cwebp）。
- `cargo test --workspace`：全绿。
- `cargo clippy -p compute-server-adapter`：新增代码零告警。

新增单测（`detect.rs`）：

1. `classifies_known_headers`：jpeg / png / webp / jxl（裸码流+容器）/ avif / heic / mif1+heic 全格式正例。
2. `magic_bytes_win_over_a_lying_declaration`：声明 JPEG、实为 PNG → 判 Png。
3. `unknown_header_falls_back_to_declared_format`。
4. `unknown_header_without_declaration_is_other`。
5. `file_probe_prefers_bytes_then_declared_then_extension`：无扩展名 `.tmp` + JPEG 字节、`.jpg` + PNG 字节、垃圾字节 + declared、垃圾字节 + 扩展名、文件不存在。

新增 e2e（`tests.rs`）：

- `test_e2e_jpeg_to_jxl_bypass_without_extension`：JPEG 字节落盘为 `no_extension_input.tmp` 且不传 declared，`run_transcode` 直达 JXL 后由 `djxl` 解回，锁死「无扩展名丢失直通」这一核心回归。

## 六、已知边界与后续

- `infer::image::is_heif` 不覆盖 major brand 为 `hevc`/`hevx`、或 `mif1` 但 compatible brands 无 `heic` 的 HEIF。此类输入走 declared / 扩展名回退，行为与迁移前一致，不会劣化；若后续需要穷尽，可在 `classify` 前对 `ftyp` 做本地兜底解析。
- `compute-engine` 内 `is_jxl`（`engine/src/lib.rs`）与 `infer::image::is_jxl` 判定等价但保留重复：引擎需保持 WASM 体积，不引入 `infer`。
- 后续迁移候选（按顺序）：`img-parts`（ICC/EXIF chunk 读写）、`fast_image_resize`（linear f32 重采样）、`kamadak-exif`（orientation 解析）、`moxcms`（ICC/CICP 转换）、`mp4parse`（ISOBMFF nclx/auxC 解析）。
