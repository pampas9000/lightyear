---
# https://vitepress.dev/reference/default-theme-home-page
layout: home

hero:
  name: "Lightyear"
  text: "图像处理平台"
  tagline: Go 编排 + Rust/libvips worker + 浏览器 wasm-vips 沙盒，同一份参数、同一个 libvips。
  actions:
    - theme: brand
      text: 快速开始
      link: /getting-started
    - theme: alt
      text: 架构总览
      link: /architecture/overview

features:
  - title: 一份参数
    details: ImageOptions 只描述输出意图（格式、质量、缩放、色彩、元数据），Web、API、worker、浏览器共用；源图信息由程序探测。
  - title: libvips 处理核心
    details: Rust worker 通过 rs-vips 调用 libvips；10/16-bit、ICC、UltraHDR gain map、JPEG→JXL 无损重新打包；PQ/HLG 源图明确拒绝而不是静默出错。
  - title: 数据库主导的调度
    details: PostgreSQL 是唯一事实来源，Redis Stream 只负责投递；attempt 租约处理丢消息、重复投递与 worker 崩溃。
  - title: 浏览器本地沙盒
    details: wasm-vips 在 Web Worker 中运行，与服务端同一个 libvips；一致性测试逐项比对两端产物。
---

## 浏览器本地沙盒

文件拖入队列、参数编辑、滑块对比、批量下载全部在浏览器本地完成，不上传文件。

![浏览器本地沙盒](/assets/local-wasm-transcoding.avif)

沙盒需要跨源隔离，Safari 暂不可用；浏览器端不支持 HEIC。详见[已知限制](/compute/limitations#浏览器沙盒)。

## 技术栈

| 边界 | 技术 |
| :--- | :--- |
| `server` | Go 1.26、Fiber v3、GORM、PostgreSQL、Redis Streams、AWS SDK for Go v2（S3/R2） |
| `compute` | Rust nightly（edition 2024）、libvips ≥ 8.17（`rs-vips`）、`cjxl`、`redis-rs`、`aws-sdk-s3` |
| `web` | Nuxt 4、Vue 3.5、Tailwind CSS 4、shadcn-nuxt、@nuxtjs/i18n、wasm-vips |

继续阅读：[架构总览](/architecture/overview) · [转码链路](/compute/pipeline) · [ImageOptions](/contracts/image-options) · [处理核心](/compute/imageops) · [路线图](/roadmap)
