# 转码链路

本页回答三个问题：一张图片从上传到产物经过了什么，每种输出格式由哪个编码器完成，以及为什么这样设计。前半部分面向使用者，后半部分给出开发者需要的细节。参数映射与安全边界见[处理核心](/compute/imageops)，限制见[HDR 与已知限制](/compute/limitations)。

## 一张图片经过的路

```text
你描述一次输出：ImageOptions（格式、质量、缩放、色彩、元数据…）
        │
        ├─ 服务端任务 ─▶ API 规范化参数 ─▶ 已有相同产物？── 是 ─▶ 直接复用
        │                                   │ 否
        │                                   ▼
        │                    worker：下载源图 ─▶ compute-imageops ─▶ 上传产物
        │
        └─ 浏览器沙盒 ─▶ Web Worker：wasm-vips ─▶ 本地下载（文件不离开浏览器）

compute-imageops / vips-ops.ts 内部：
  读文件头（格式、尺寸、HDR）─▶ 解码 ─▶ 转正 ─▶ 缩放 ─▶ 色彩 ─▶ 元数据 ─▶ 编码
                         │                                        └─ AVIF：libavif（仅服务端）
                         └─ JPEG → JXL 无损且不改像素：cjxl 直接转码（仅服务端）
```

1. **只描述一次意图**。你给出想要的结果（例如"AVIF，质量 60，宽不超过 2000"），不需要知道源图是什么格式、几位、带不带 ICC，这些由程序探测。
2. **相同意图不重复计算**。服务端把参数规范化（补齐默认值）后，与源文件一起算出产物键；同一个源文件、同一组规范化参数直接复用已有产物。
3. **一次解码，一次编码**。解码、转正、缩放、色彩转换和元数据都在 libvips 内完成，像素在内存中直接交给编码器，中间不落地、不经过第二种格式。
4. **服务端和浏览器跑同一套逻辑**。沙盒用 wasm-vips，与服务端是同一个 libvips，参数映射逐条对应；只有服务端 AVIF 的最后一步编码换成了 libavif。

服务端任务的调度、心跳与重试见[请求链路](/architecture/request-flows)。

## 每种格式由谁编码

| 输出 | 编码器 | 说明 |
| :--- | :--- | :--- |
| AVIF | libavif → aom | 服务端；像素与元数据仍由 libvips 准备。浏览器沙盒为 libvips `heifsave` → libheif → aom |
| HEIC | libvips `heifsave` → libheif → x265 | 仅服务端 |
| JXL | libvips `jxlsave` → libjxl | |
| JXL（JPEG 源、无损、不缩放、`color=keep`） | `cjxl` 直接转码 DCT 系数 | 仅服务端；见下文 |
| WebP | libvips `webpsave` → libwebp | |
| JPEG | libvips `jpegsave` → mozjpeg | 保留 gain map 时为 `uhdrsave` → libultrahdr |
| PNG | libvips `pngsave` → libpng | |

**不填 `effort` 时，用实际调用的那个编码器自己的默认值**：走 libvips 就是 libvips 的默认，走 libavif 就是 libavif 的默认。填了 `effort`（1–10）时，线性映射到该编码器自己的范围：

| 输出 | 编码器参数 | 不填 `effort` | `effort` 1 → 10 |
| :--- | :--- | :--- | :--- |
| AVIF（服务端） | libavif speed 0–10 | speed 6（libavif / avifenc 默认） | speed 10 → 0 |
| AVIF（浏览器）、HEIC | libvips `effort` 0–9（aom speed = 9 − effort） | effort 4，即 speed 5（libvips 默认） | 0 → 9 |
| JXL | libjxl effort 1–9 | 7（libvips 与 libjxl 的默认相同） | 1 → 9 |
| WebP | libwebp method 0–6 | 4（libvips 默认） | 0 → 6 |
| PNG | zlib 压缩级别 0–9 | 6（libvips 默认） | 0 → 9 |

这样做的好处是"默认"有据可查：与各库文档里写的一致，也与直接用 `avifenc`、`cjxl` 得到的结果一致，而不是 lightyear 自造的一个中间档。

## 为什么统一走 libvips

**一个引擎，结果可预期。** 六种输出格式、缩放、色彩管理、元数据都由同一个库处理。不需要在多个命令行工具之间传中间文件，也就没有"这一步丢了 ICC""那一步把 16-bit 截成 8-bit"这类在工具之间发生的问题。lightyear 之前的自研引擎就是在这些衔接处出错最多，所以整体换成了 libvips（见[路线图](/roadmap#已做的决定)）。

**浏览器里看到的就是服务端会给的。** 沙盒用 wasm-vips，与服务端是同一个 libvips、同一套参数映射。你在浏览器里试出满意的参数，提交到服务端后得到同样的结果。`task test:consistency` 用 24 组素材检查两端的格式、尺寸、通道、位深、ICC、Exif、gain map 和像素差。

**压缩效率不打折。** 我们用 SSIMULACRA2 与 Butteraugli 在 12 张图上比较了 lightyear 与直接调用 libjxl / libavif（方法与数据见[编码器对比](/engineering-notes/encoder-comparison)）：

- **JXL**：PNG 源与 `cjxl` 在相同 effort 与 distance 下逐字节相同；JPEG 源解码出的像素相同，文件只差 1–3 字节的头部。
- **AVIF**：照片、插画、16-bit 源和 JPEG 源在相同 SSIMU2 下体积是 `avifenc` 的 0.99–1.01 倍。
- **例外是截图类 AVIF**：经 libvips 编码时体积比 `avifenc` 大 13%–40%。原因是 libvips 的 `heifsave` 固定关闭了 aom 的 intra block copy（这个工具对重复的文字和界面元素特别有效），且没有选项可以打开。照片不受影响。

## 为什么服务端 AVIF 交给 libavif

上面的例外是 libvips 唯一有实测损失的地方，所以服务端 AVIF 的最后一步编码改为直接调用 libavif：

- **libvips 仍负责前面所有步骤**：解码、转正、缩放、色彩转换，以及把像素整理成 8/16-bit RGB(A)（CMYK 经 ICC 转换、灰度扩成 RGB），与 libvips 自己的保存器做法相同。ICC、Exif、XMP 也从 libvips 取出后交给 libavif。
- **libavif 只做编码**，并使用它自己的默认值（speed 6、alpha 与颜色同质量、不切 tile；无 ICC 时标记为 sRGB，矩阵与 `avifenc` 相同为 BT.601）。像素由 libvips 准备不影响体积：关掉 tile 的 `avifenc` 与 lightyear 的输出体积逐字节相同。`avifenc` 默认自动切 tile，所以直接用它压截图反而大 15%–18%，画质（SSIMU2）相同。
- **可以切回 libvips**：worker 设置 `WORKER_AVIF_ENCODER=libvips` 后，AVIF 改用 `heifsave`，用于 libavif 出问题时回退或做对比。这是部署配置，不是请求参数。
- **浏览器沙盒不变**：wasm-vips 里没有单独的 libavif，沙盒继续用 libvips `heifsave`。两端 AVIF 的像素在一致性测试的阈值内，但文件体积不同。

代价是 libavif 在最慢的几档上会更慢：intra block copy 在 speed 0–2 时开销很大，在一张 3.5 MP 的截图上 speed 1 约 16 秒（关闭时约 4 秒）。默认的 speed 6 不到 1 秒，只有显式把 `effort` 调到 9–10 才会进入这几档。

**安全边界集中在一处。** libvips 的 untrusted 加载器拦截、像素上限、`fail_on=error`、关闭操作缓存都只需要配一次（见[安全边界](/compute/imageops#安全边界)）。每多一个直接调用的编码库，就多一套输入校验和资源上限要维护。

## 为什么 JPEG → JXL 要绕开 libvips

JPEG 转 JXL 有一种 libvips 做不到的方式：`cjxl` 可以直接把原 JPEG 的 DCT 系数搬进 JXL，不解码成像素再重新压缩。这样做：

- **真正无损**：像素与原 JPEG 完全相同，而不是"解码后的像素无损"。
- **可逐字节还原**：`metadata=keep` 时附带重构数据，用 `djxl` 能还原出与原文件一模一样的 JPEG（测试会验证）。
- **体积更小**：在 `scripts/fixtures` 的 JPEG 上比原文件小 9%–29%。

所以只有同时满足"源是 JPEG、`lossless=true`、不缩放、`color=keep`"时才走 `cjxl`。任何一项不满足，就意味着要改像素，这时回到 libvips。浏览器端没有 `cjxl`，同样的请求会重新编码像素：结果仍然无损，但不能逐字节还原（见[浏览器沙盒限制](/compute/limitations#浏览器沙盒)）。

## 对使用者意味着什么

- **默认参数就是各编码器推荐的参数**。不填 `effort` 时使用编码器自己的默认；想要更小的文件就调高 `effort`，代价是编码更慢。
- **同一组参数在哪里跑结果都一样**：浏览器沙盒里预览的效果，就是服务端任务的结果（HEIC 等沙盒不支持的格式除外）。
- **重复提交不会重复计算**：同一源文件、同一组参数直接返回已有产物。
- **元数据默认删除，方向烘焙进像素**：输出不会因为查看器不认 Exif 方向而歪掉；需要保留时设 `metadata=keep`。
- **JPEG 转 JXL 无损是真正的无损**，并且在 `metadata=keep` 时可以还原回原文件。
- **服务端 AVIF 与 `avifenc` 一样高效**，截图也是；浏览器沙盒里截图转 AVIF 仍会偏大，最终以服务端结果为准。

## 开发者须知

- 改 `compute/crates/imageops` 时要同步改 `web/app/lib/vips-ops.ts`，并运行 `task test:consistency`。AVIF 编码（`avif.rs` 与 `avif_shim.c`）只在服务端，浏览器端保持 `heifsave`。
- 服务端编译需要 libavif 的头文件与 pkg-config 信息；C 包装层让 Rust 不依赖 libavif 的结构体布局。
- 改默认值或规范化规则时要提升产物键前缀 `image-options/vN`，否则会复用旧产物。
- libvips 对未知的选项名只打 warning。新增编码选项时，要在测试里读回产物确认它生效。
