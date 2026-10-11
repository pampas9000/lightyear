---
# https://vitepress.dev/reference/default-theme-home-page
layout: home

hero:
  name: "Lightyear"
  text: "Media Transcoding Platform"
  tagline: Go 编排服务 + 纯 Rust 计算核心 + 浏览器 WASM 沙盒的多语言 Monorepo。
  actions:
    - theme: brand
      text: 快速开始
      link: /getting-started
    - theme: alt
      text: 架构总览
      link: /architecture/overview

features:
  - title: Go API 与编排
    details: Fiber v3 + GORM/PostgreSQL 负责 API 与任务持久化；Redis Streams 承载调度、结果回收、租约与幂等恢复。
  - title: Rust 计算核心
    details: 纯 Rust 引擎承担色彩管线、编辑与 Y4M/PAM 裸流；官方 CLI（cjxl / avifenc / avifdec / heif-dec / cwebp）承担宿主编解码。
  - title: WASM 本地沙盒
    details: Web Worker 异步卸载 + 动态加载 WASM，浏览器内完成转码与编辑，零服务端开销。
  - title: 保真度基准
    details: 45 个合成/行业/4K 素材与容器头断言，约束色深、色域、矩阵与抽样行为。
---

## WASM 本地转码沙盒

前端内置完整沙盒：文件拖入队列、参数编辑、滑块对比、批量下载全部在浏览器本地完成，**零服务器成本、100% 隐私**。

![WASM 本地转码沙盒](/assets/local-wasm-transcoding.avif)

> ⚠️ **已知限制（广色域）**：浏览器端编码器栈（`image`/`ravif`、`@jsquash/jxl`）无法写入 ICC/CICP，且 JPEG XL 的颜色编码位于码流头部无法事后注入。沙盒**不做色域转换**：广色域源图按原始像素输出但不带色彩标识，渲染时可能偏饱和；需要保留 ICC 的广色域资产请走服务端。详见[已知限制](/compute/limitations)与[色彩管线路线图](/compute/color-roadmap)。

## 能力概览

- **服务端异步转码**：任务提交 → 队列调度 → Rust Worker 执行 → 结果回收与状态聚合，支持重试、取消、租约超时接管与幂等校验。
- **高质量静态图像管线**：10-bit 原生色深、自适应 4:4:4/4:2:0 抽样、ACES Filmic 色调映射、蓝噪声抖动、ICC/EXIF 方向保留、HEIC/AVIF 原生 YUV 直通。
- **浏览器本地沙盒**：WebP / JPEG / PNG / AVIF 编码，JPEG → JXL 频域无损直通（`@jsquash/jxl`），线性光重采样与几何编辑。

## 技术栈

| 边界 | 技术 |
| :--- | :--- |
| `server` | Go 1.26、Fiber v3、GORM + gorm/gen、PostgreSQL、Redis Streams、AWS SDK for Go v2（S3/R2） |
| `compute` | Rust（edition 2024）、`image`、`fast_image_resize`、`img-parts`、`kamadak-exif`、`yuv`、`jxl-oxide`、`redis-rs`、`aws-sdk-s3` |
| `web` | Nuxt 4、Vue 3.5、Tailwind CSS 4、shadcn-nuxt、@nuxtjs/i18n、Web Worker + WebAssembly |
| 外部工具（服务端可选） | `cjxl` / `djxl`、`avifenc` / `avifdec`、`heif-dec`、`cwebp`（缺失时按严格/降级策略处理） |

继续阅读：[架构总览](/architecture/overview) · [接口契约](/contracts/) · [色彩管线](/compute/color-pipeline) · [保真度基准](/quality/fidelity-benchmark)。
