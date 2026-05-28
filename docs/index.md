---
# https://vitepress.dev/reference/default-theme-home-page
layout: home

hero:
  name: "Lightyear"
  text: "Media Transcoding Platform"
  tagline: A premium monorepo featuring a Go orchestration backend, pure Rust compute engine, and interactive WASM browser playground.
  actions:
    - theme: brand
      text: Architecture Design
      link: /architecture
    - theme: alt
      text: API & WASM Contracts
      link: /contracts

features:
  - title: "Go API & Orchestration"
    details: Powered by Fiber v3, Ent ORM, and Redis Streams. Highly reliable task persistence, user sessions, OAuth, and consumer stream orchestration.
  - title: "Rust Compute Core"
    details: High-performance portable encoding/decoding engine supporting WebP, JPEG, PNG, AVIF, and JPEG XL (JXL) in pure Rust.
  - title: "WASM Local Sandbox"
    details: Zero-server-overhead transcoding offloaded to HTML5 Web Workers, preventing main-thread lag with detailed performance dashboards.
  - title: "Draggable Quality Slider"
    details: Interactive side-by-side and draggable comparison views allowing users to inspect compression savings and quality metrics live.
---

## WASM Local Transcoding Sandbox Playground

The frontend application includes a fully featured WASM Sandbox allowing users to edit, transform, and optimize images directly in the browser with **zero server costs** and **100% privacy**.

![WASM Local Transcoding Sandbox](/Users/kazuha/dev/lightyear/assets/local-wasm-transcoding.avif)

### Core Technology Stack

- **Orchestration Server**: Go 1.22+, Fiber v3, Ent ORM, PostgreSQL, Redis Streams (PEL, XAUTOCLAIM).
- **Compute Worker**: Rust stable, SeaORM, AWS SDK S3, Redis Streams.
- **Compute Engine**: Portable Rust codecs (`image`, `zune-jpegxl`, `zenavif`), WebAssembly bindings (`wasm-bindgen`, `wasm-pack`).
- **Web Console**: Vue 3.5, Nuxt 4, Vite, Tailwind CSS 4, shadcn-vue.
