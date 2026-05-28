# Web App

`web` 是这个 Monorepo 的前端应用，负责提供基于浏览器的转码产品界面、任务提交入口、任务状态展示，以及后续对 `server` API 和 `compute` WebAssembly 能力的接入。

## 技术栈

- `Nuxt 4`
- `Vite`
- `Vue 3.5+`
- `Tailwind CSS 4`
- `shadcn-nuxt`
- `TypeScript`

## 职责边界

`web` 负责：

- 用户界面与交互流程
- 调用 `server` 暴露的 API
- 展示任务创建、排队、执行、完成等状态
- 在需要浏览器端本地处理时，接入 `compute/bindings/wasm` 产物

`web` 不负责：

- 任务调度与创建策略
- 后端鉴权与持久化
- 原生 FFI 能力导出
- 后台 Worker 执行

## 目录约定

当前前端目录采用 Nuxt 4 的 `app/` 目录结构：

- `app/app.vue`：应用壳层入口
- `app/pages/`：页面路由
- `app/layouts/`：布局层
- `app/components/ui/`：`shadcn-nuxt` 或设计系统基础组件
- `app/components/tasks/`：任务相关业务组件
- `app/composables/`：组合式逻辑
- `app/lib/api/`：API Client、请求封装、DTO 映射
- `app/assets/styles/main.css`：全局样式资源，同时承载 Tailwind 4 的 `@theme` 变量与自定义 utility
- `public/`：静态资源
- `nuxt.config.ts`：Nuxt 4 配置入口，定义 `srcDir: 'app'`、模块与 Vite 插件
- `package.json`：前端子项目依赖与脚本

当前项目使用 Tailwind 4 的 CSS-first 方式，不再维护单独的 `tailwind.config.cjs`。

推荐约定：

- 页面放在 `app/pages/`
- 可复用业务组件按领域拆到 `app/components/*`
- 通用 UI 原子组件只放在 `app/components/ui/`
- 与后端交互的请求代码统一放在 `app/lib/api/`
- Tailwind 4 主题、设计令牌与自定义 utility 统一放在 `app/assets/styles/main.css`
- 不要把业务请求直接散落在页面组件里

## 本地开发

在仓库根目录执行时，推荐优先使用根级 `justfile`：

- 安装依赖：`just bootstrap` 或 `bun install`
- 启动前端：`just dev-web` 或 `bun run dev:web`
- 类型检查：`just check-web` 或 `cd web && bunx nuxt typecheck`
- 生产构建：`just build-web` 或 `bun run build:web`

如果你只想在 `web` 目录内操作，也可以使用：

- 启动开发：`bun run dev`
- 生产构建：`bun run build`
- 预览构建：`bun run preview`

## 与其他模块的关系

- `server`：提供 API、任务创建与编排能力
- `compute`：提供转码核心能力
- `compute/bindings/wasm`：为浏览器场景提供可集成的 WebAssembly 模块
- `compute/bindings/ffi`：为 Flutter、SwiftUI 等原生端提供绑定，不直接由 `web` 使用

系统核心流向：
- **云端异步任务流**：用户在 `web` 发起转码，调用 `server` API 创建任务存入数据库与 Redis，通过 Go Orchestrator 派发至 Rust Worker，最后 `web` 轮询展示结果。
- **本地 WASM 转码沙盒**：现已完整接入 `compute/bindings/wasm`。用户可在浏览器内通过纯 Rust 编译的 WASM 核心，在本地安全、快速地完成多种格式 of 转码与图像编辑操作，无需任何服务端计算开销。

#### WASM 本地转码沙盒 (WASM Local Transcoding Sandbox)

![WASM 本地转码沙盒](/Users/kazuha/dev/lightyear/assets/local-wasm-transcoding.avif)

功能亮点：
1. **异步卸载 (Web Worker)**：采用 HTML5 Web Worker (`compute.worker.ts`) 承载重型图像计算，避免阻塞浏览器 UI 主线程，保证极致流畅的操作响应。
2. **流式批处理队列**：支持多文件拖拽拖入队列、批量参数调整、按需独立配置以及一键打包下载。
3. **高保真预览与滑块对比**：集成双栏对比 (Side by Side) 与带阻尼物理效果的拖拽滑块 (Slider) 实时对比优化前后的图像品质。
4. **精细图像编辑管线**：
   - **格式转换**：支持 WebP, JPEG, PNG, AVIF 以及 JPEG XL (JXL)。
   - **高保真缩放**：支持锁定/解锁宽高比，搭载 Lanczos3, Catmull-Rom, Gaussian, Triangle 和 Nearest 滤波器。
   - **几何与色彩变换**：支持旋转、镜像翻转，以及亮度、对比度、高斯模糊微调，并内置一键灰度处理。
5. **多维指标面板**：提供毫秒级转码速度耗时、最终体积大小和存储节省比例的实时看板。

## UI 与样式规范

- 优先使用 `Tailwind CSS 4` 编写样式
- 全局设计令牌、`@theme` 映射与自定义 utility 统一维护在 `app/assets/styles/main.css`
- 当前项目采用 Tailwind 4 的 CSS-first 配置，不再维护 `tailwind.config.cjs`
- `app/components/ui/` 只承载基础组件，不承载具体业务逻辑
- 业务组件优先保持无副作用，数据请求由页面或 `app/composables/` 协调
- 页面应同时兼顾桌面端与移动端

## 环境变量

当接入真实 API 后，建议将前端环境变量统一收敛到 Nuxt 运行时配置中，例如：

- API Base URL
- 上传限制
- 任务轮询间隔
- 实验性 WASM 开关

在变量稳定前，不建议把它们散落到组件内部。

- `app/lib/api/`：API Client、请求封装、通过 Nuxt Nitro Proxy 对接后端

...

## 数据流向与代理 (Proxy)

本应用采用 **薄客户端 (Thin Client)** 架构：

- 前端不持有数据库或 Redis 连接
- 所有业务请求通过 Nuxt Nitro Server 代理：
  - 前端调用 `/api/**`
  - Nitro 透明转发至本地 Go 后端 (`localhost:8080/api/**`)
- 这样做可以确保所有任务创建逻辑收口在 Go 后端

## 已完成特性

- [x] 配置 Nuxt Nitro 代理与跨域透明转发
- [x] 基于 Ent ORM + Fiber v3 实现任务创建与动态任务列表展示
- [x] 完整接入 `compute/bindings/wasm` 并设计了高阶 WASM 图像转码与编辑沙盒
- [x] 使用 Web Worker 异步卸载转码进程，保障 UI 交互高信噪比与流畅度

## 维护原则

如果你要继续扩展这个前端，请保持下面几条原则：

- 目录命名统一使用小写 kebab-case
- 组件按职责划分，不要把页面、请求、状态、样式耦合在一起
- 优先在根目录脚本中暴露常用操作，保持 Monorepo 使用体验一致
- 前端只关心 UI、交互和 API/WASM 接入边界，不侵入后端或 Worker 实现细节