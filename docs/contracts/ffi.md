# FFI 契约

原生宿主边界由 `compute/bindings/ffi/src/lib.rs` 定义，当前为**实验性**：只覆盖解码 + 重编码，编辑、参数与 JXL 与 `compute-wasm` 对齐尚未落地。

> `compute/bindings/ffi` 是独立 crate，被显式排除在根 workspace 之外，`cargo build/test --workspace` 不会构建它；需要时执行 `cargo build -p compute-ffi` 或 `task build:compute:ffi`。

## 导出符号

| 符号 | 签名 | 说明 |
| :--- | :--- | :--- |
| `transcoder_version` | `() -> *const c_char` | 返回静态分配的版本字符串，调用方不得释放 |
| `transcoder_process` | `(data: *const c_uchar, len: usize, target_format_code: i32, out_len: *mut usize) -> *mut c_uchar` | 解码 + 重编码；失败返回空指针 |
| `transcoder_free_buffer` | `(ptr: *mut c_uchar, len: usize)` | 释放 `transcoder_process` 返回的堆缓冲 |

## 格式码

| 码 | 格式 |
| :--- | :--- |
| `0` | JPEG |
| `1` | PNG |
| `2` | WebP |
| `3` | AVIF |

其他值返回空指针。当前编码质量固定为 80，不可配置。

## 内存与安全规则

- `data` 必须是 `len` 字节的有效缓冲区
- `out_len` 必须是可写的 `usize` 指针
- `transcoder_process` 成功返回的缓冲必须由 `transcoder_free_buffer` 释放，参数需与返回时一致
- 所有符号保持 C ABI 兼容；Rust 特有类型不得出现在公开 ABI 中

## 演进规则

- 新增符号是兼容变更；修改符号名、签名或内存归属规则是破坏性变更
- 扩展时遵循：不透明句柄提供 create/destroy 成对接口；字节缓冲使用指针 + 长度；错误通过独立查询或结果结构体返回
