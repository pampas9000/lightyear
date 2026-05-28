# Transcoder Architecture And Design

## 目标

`transcoder` 是一个多语言 Monorepo，用于承载同一套转码能力在不同运行时中的实现与交付：

- `server` 负责对外 API、任务创建、任务编排与系统集成
- `compute` 负责转码核心能力，包括纯 Rust 编解码、WASM 导出、FFI 导出与任务执行 Worker
- `web` 负责前端体验，基于 Nuxt 提供控制台、上传、任务提交与结果展示
- `docs` 负责沉淀跨模块的架构、边界与协作规范


这个仓库的设计目标不是把所有逻辑堆在一个语言里，而是把“产品入口”和“计算核心”解耦：

- 面向用户的接入层在 `web`
- 面向业务系统的协调层在 `server`
- 面向性能与跨平台复用的核心层在 `compute`

## 顶层结构

当前推荐的顶层职责划分如下：

- `server/`
  - Go 服务
  - 负责 HTTP API、任务创建、任务状态查询、队列交互、存储集成
- `compute/`
  - Rust 计算域
  - 负责跨运行时复用的转码能力与执行策略
- `web/`
  - Nuxt 前端
  - 负责用户界面、任务发起、结果查询、浏览器侧能力接入
- `docs/`
  - 架构与规范文档
- `package.json`
  - Node workspace 入口与根级脚本入口
- `Cargo.toml`
  - Rust workspace 入口
- `go.work`
  - Go workspace 入口

## 目录命名规范

仓库目录遵循以下命名原则：

- 顶层目录使用业务边界命名：`server`、`compute`、`web`
- 可执行单元使用单数名词：`worker`、`wasm`、`ffi`、`api`
- 集合目录使用复数名词：`crates`、`bindings`、`components`、`pages`
- Rust crate 统一使用 `compute-*` 前缀，避免命名漂移
- 避免使用语义不清的目录名，如 `libs`、`ext`、`common`、`misc`

## Compute 结构

`compute` 是仓库的核心域，负责真正的媒体处理能力。它被拆成三层：

### 1. `compute/crates`

这一层放置纯 Rust 能力与领域模型，是可复用的内部核心库。

- `compute/crates/types`
  - crate 名：`compute-types`
  - 负责跨模块共享的类型定义，如任务结构、格式枚举、编码参数
  - 是 `server`、`worker`、`wasm`、`ffi` 之间的核心契约来源

- `compute/crates/engine`
  - crate 名：`compute-engine`
  - 负责纯 Rust、可移植的编解码逻辑
  - 优先承载可以在浏览器、服务端、原生端复用的处理能力
  - 不应该依赖服务端专属基础设施

- `compute/crates/server-adapter`
  - crate 名：`compute-server-adapter`
  - 负责服务端专属适配能力，例如 FFmpeg 绑定、系统库接入、硬件加速接入
  - 只应承载无法在 WASM 或通用运行时中复用的能力

### 2. `compute/bindings`

这一层负责把 `compute` 能力导出到不同宿主环境，保持“薄绑定、厚核心”。

- `compute/bindings/wasm`
  - crate 名：`compute-wasm`
  - 负责导出浏览器可调用的 WASM 接口
  - 面向 Web 场景，比如浏览器本地预处理、轻量级本地转码、预览转换

- `compute/bindings/ffi`
  - crate 名：`compute-ffi`
  - 负责导出 C ABI 兼容接口
  - 面向 Flutter、SwiftUI、桌面端或其他需要原生链接的宿主

### 3. `compute/worker`

- crate 名：`compute-worker`
- 负责实际消费任务并执行转码策略
- 它不定义核心编码算法，而是编排：
  - 优先使用 `compute-engine`
  - 必要时使用 `compute-server-adapter`
  - 兜底时可调用外部命令或系统能力

## Server 结构

`server` 是系统协调层，不负责核心转码算法，只负责业务与系统边界。

推荐职责如下：

- `server/cmd/api`
  - 服务入口
  - 负责启动 API 进程、装配依赖、初始化配置与基础设施

- `server/internal/api/handlers`
  - HTTP 处理函数，负责请求解析、权限校验、调用 Service 层并返回标准响应

- `server/internal/api/middleware`
  - Fiber 中间件，包括 Auth (Session)、Logger、Recovery、CORS 等

- `server/internal/models`
  - 领域模型定义 (GORM)
  - 采用 UUID v7 作为主键，利用 `json` tag 定义 API 契约

- `server/internal/services`
  - 业务逻辑层，封装复杂的数据库操作与跨模块调用

- `server/internal/config`
  - 基于 Viper 的配置管理，支持环境变量与 `.env` 文件

- `server/internal/query`
  - 由 `gorm-gen` 自动生成的类型安全查询代码

`server` 的核心原则：

- 不直接实现编解码算法
- 不复制 `compute` 中已有的领域模型
- 负责把业务请求转换为任务，并把任务交给执行层
- 负责对外暴露稳定的 API 与状态查询能力
- **无物理外键约束**：数据库层面不建立物理外键，通过应用层逻辑保证数据一致性。

## Web 结构

`web` 是产品入口层，负责交互体验而不是计算核心。

Nuxt 4 默认采用 `app/` 目录作为应用源码根，因此前端运行时代码统一放在 `web/app` 下，而 `public`、`nuxt.config.ts` 和 `package.json` 保持在 `web` 根目录。

推荐职责如下：

- `web/app/pages`
  - 页面与路由
- `web/app/components/ui`
  - 设计系统与基础 UI 组件
- `web/app/components/tasks`
  - 与任务相关的业务组件
- `web/app/composables`
  - 组合式状态与业务逻辑复用
- `web/app/layouts`
  - 页面布局
- `web/app/lib/api`
  - API Client 与请求封装 (对接本地代理 `/api`)

- `web/assets/styles`
  - 全局样式与设计令牌

- `web/app/app.vue`
  - 应用壳层与全局入口

`web` 的核心交互逻辑：

- 通过 Nuxt Nitro Proxy 将 `/api/**` 请求透明转发至 Go 后端 (`localhost:8080`)
- 自身不持有数据库或 Redis 连接，作为纯粹的“薄客户端”运行

`web` 可以通过两条路径接入转码能力：

- 默认路径：调用 `server` API，创建异步任务
- 可选路径：在浏览器中直接调用 `compute/bindings/wasm`，用于本地预处理或轻量转换

## 核心领域模型 (Database Schema)

系统采用 GORM 进行对象关系映射，并在 `server/internal/models` 中定义了以下核心模型。**注意：所有模型之间均无数据库层面的物理外键约束。**

- **User**: 用户模型，包含基本信息、密码哈希。
- **UserOauthAccount**: 第三方登录账户信息（GitHub, Google），关联到 User。
- **File**: 文件模型，记录用户上传的源文件和系统生成的产物文件（路径、大小、MIME 类型）。
- **Workflow (Preset)**: 转码配置预设，代表一种可复用的处理参数模版（例如：将图片转为 `AVIF`，并配置特定的 `params` JSON 负载）。
- **Task**: 任务批处理容器，代表用户的“一次提交动作”。用于追踪一组 Job 的整体状态。
- **Job**: 具体的转码作业。包含状态（`PENDING`, `PROCESSING` 等）、进度、以及**快照化**的执行参数（如 `target_format` 和 `params` JSON 字段）。Job 归属于 Task，并关联输入/输出文件。

## 运行时架构

系统存在三条主要执行路径。

### 1. Web 到 Server 到 Worker 的异步任务流

这是默认主链路，适合服务端处理与长任务处理。

流程如下：

1. 用户在 `web` 发起请求（包括 OAuth 登录或普通注册）。
2. `web` 的 Nitro Server 将请求代理到 Go 后端的 Fiber API。
3. Go 后端 API 通过 GORM 写入任务元数据到 PostgreSQL（GORM models），并将 `job_id` 写入 Redis Stream `transcoder:jobs:stream`。
4. **Go Orchestrator** 守护进程从 `transcoder:jobs:stream` 读取任务，在数据库事务中将 Job 状态更新为 `PROCESSING` 并生成一个全新的 `attempt_id`。随后，它将包含 `job_id`、`attempt_id`、输入/输出 S3 路径和转码参数的 JSON 负载发送到 `transcoder:compute:stream`，然后确认（`XACK`）旧消息。
5. **Rust Compute Worker** 集群消费 `transcoder:compute:stream`。由于 Rust worker 是纯粹的计算器，它并不连接数据库，而是直接下载 S3 输入文件，调用 `compute-engine` 或本地命令行工具执行转码，上传结果文件至 S3，将执行成功或失败的详细结果（包含 `job_id`、`attempt_id`）写入 `transcoder:results:stream`，并在处理完成后确认旧消息。
6. **Go Orchestrator** 消费 `transcoder:results:stream`，在数据库事务中通过校验 `attempt_id` 确保消息的有效性与幂等性，更新 `Job`/`File` 状态并聚合重算父 `Task` 状态，确认结果消息。
7. `web` 通过代理 API 轮询 Go 后端获得实时进度，用户获得产物下载地址或结果预览。

这条链路的特点：

- 适合长耗时任务
- 适合服务端资源调度与后续横向扩容
- 适合需要系统级工具或硬件能力的转码流程

### 2. Browser 到 WASM 的本地处理流

这是浏览器本地执行路径，适合轻量级、隐私敏感或高实时的处理场景。该功能通过 **WASM 本地转码沙盒 (WASM Local Transcoding Sandbox)** 实现，提供了与服务端转码互补的本地无损优化体验。

![WASM 本地转码沙盒](/Users/kazuha/dev/lightyear/assets/local-wasm-transcoding.avif)

#### 架构与实现细节：
1. **多线程 Web Worker 异步卸载**：
   - 浏览器主线程仅负责 UI 渲染和用户交互。
   - 重型的媒体解析、解密和转码全部通过 HTML5 Web Worker (`compute.worker.ts`) 在后台线程中异步执行，彻底杜绝了复杂图像计算时造成的浏览器界面卡顿与卡死（UI Blocking）。
2. **纯 Rust 计算核心的高效交付**：
   - `compute-wasm` 绑定层加载 `compute-engine` 核心，在浏览器中提供高性能的 Rust 原生算法支持。
   - 支持完整的编解码管线，包括 `WebP`、`JPEG`、`PNG`、`AVIF` 和最新一代的 `JXL` (JPEG XL) 格式。
3. **流式批处理队列**：
   - 支持多文件拖拽拖入，构建前端执行队列。
   - 提供队列管理、单任务独立配置、顺序转码调度以及批量打包下载功能。
4. **精细化参数控制与图像编辑管线**：
   - **智能尺寸缩放**：支持锁定/解锁宽高比，预置 *Lanczos3* (高保真锐化)、*Catmull-Rom* (平滑)、*Gaussian* (柔和)、*Triangle* (线性) 和 *Nearest* (快速) 缩放滤波器。
   - **几何变换**：支持 90°/180° 旋转，水平或垂直镜像翻转。
   - **视觉增强调整**：提供亮度、对比度、高斯模糊的精准滑动条调节，并集成一键灰度图转换。
5. **实时性能监控与渐进式对比**：
   - 控制台实时计算并渲染处理耗时（毫秒级精度）、产物大小及存储节省率。
   - 交互界面提供双栏对比 (Side by Side) 以及带物理弹性的滑块对比 (Slider Comparison) 两种模式，方便用户直观校验转码后的图像细节质量。

流程如下：

1. `web` 在浏览器中加载 `compute/bindings/wasm`，并在独立线程中初始化 Web Worker。
2. 用户选择/拖入本地文件，并调整转码与图像处理参数（目标格式、质量、尺寸缩放、旋转、滤镜等）。
3. Web Worker 接收到数据和操作指令 JSON，调用 WASM 导出的 `browser_edit_image` 或 `browser_convert` 接口。
4. WASM 内部的 `compute-engine` 在沙盒内完成加载、解码、管线操作链应用及重编码。
5. Worker 将生成的二进制 Blob 传回主线程，前端生成本地预览 URL 并动态计算压缩效率指标。

这条链路的特点：

- **零服务器负担**：所有的计算都在用户浏览器本地完成，极大减轻了云端计算和存储压力。
- **高安全性与隐私保护**：源文件和处理后的文件无需上传到服务器，适合敏感图像的处理。
- **极速响应**：省去了文件上传与下载的网络开销，转码与编辑操作立等可现。
- **局限性**：不适合依赖系统级大型音视频解码库 (如 FFmpeg) 或重型超大视频转码的场景。


### 3. Native App 到 FFI 的原生集成流

这是面向移动端和原生客户端的复用路径。

流程如下：

1. Flutter、SwiftUI 或桌面端集成 `compute/bindings/ffi`
2. 宿主应用通过 C ABI 调用 Rust 能力
3. `compute-engine` 承担通用转码逻辑
4. 宿主应用拿到处理结果并自行组织 UI 与状态管理

这条链路的特点：

- 让转码核心脱离 Web/Server 单一宿主
- 适合移动端离线处理与本地体验优化
- 绑定层应保持稳定、薄、可版本化

## Worker 执行策略

`compute/worker` 是执行层，不是协议层，也不是产品入口层。

Worker 的职责包括：

- 从任务源接收任务
- 识别目标格式与执行环境
- 选择最合适的处理路径
- 统一产出结果与错误
- 记录任务执行状态

推荐的策略优先级：

1. 优先 `compute-engine`
   - 适用于纯 Rust、可移植、无需外部进程的格式
2. 其次 `compute-server-adapter`
   - 适用于服务端专属原生库、FFmpeg 绑定、系统资源接入
3. 最后 shell 或外部命令兜底
   - 适用于复杂媒体容器、实验性能力或临时接入路径

这个分层确保：

- 可移植能力尽量沉到核心库
- 服务端特定能力不污染通用库
- Worker 只负责编排，不把所有实现塞进一个入口文件

### 任务处理幂等性与并发控制

为了防止多个 Worker 节点并发处理同一个 Job，以及应对 Redis 消息重复投递、XACK 失败、Worker 崩溃重启等场景，系统在应用层与 Redis Streams 层实现了**尝试标识（Attempt ID）**、**租约超时自动接管（Stale Recovery）**以及**唯一消费者名与自动认领（XAUTOCLAIM）**机制：

1. **原子抢占 (Atomic Claim) 与尝试标识 (Attempt ID)**：
   - Go API 插入 Job 时的初始状态为 `PENDING`。
   - 当 Go Orchestrator 准备调度任务时，在数据库事务中执行原子条件更新：
     ```sql
     UPDATE jobs SET status = 'PROCESSING', attempt_id = $1, updated_at = now()
     WHERE id = $2 AND status = 'PENDING';
     ```
   - 每次抢占都会生成一个唯一的 UUID 作为 `attempt_id`。如果受影响行数为 0，说明已被处理，直接跳过并 ACK。
   - Rust Worker 接受的计算负载以及写入的转码结果都会携带该 `attempt_id`。

2. **结果接收的幂等性校验**：
   - 当 Go Orchestrator 从 `transcoder:results:stream` 收到计算结果时，首先在数据库事务中比对结果中的 `attempt_id` 与 DB 中当前最新的 `attempt_id` 是否一致。
   - 若不匹配（例如旧的尝试执行缓慢，在重试启动后才返回结果），Orchestrator 直接丢弃该结果并 ACK，避免历史脏数据覆盖最新状态，保证最终一致性。

3. **租约超时检测与恢复机制 (Stale Recovery)**：
   - Go Orchestrator 后台运行一个常驻的 Stale Checker，定期扫描数据库中处于 `PROCESSING` 状态且 `updated_at` 超过 5 分钟未更新的 Jobs。
   - 如果发现超时 Job，开启事务并生成一个**全新的 `attempt_id`**，将 `updated_at` 置为当前时间，重新向 `transcoder:compute:stream` 投递计算请求。

4. **动态扩容下的唯一 Consumer Name 与 PEL 自动认领 (XAUTOCLAIM)**：
   - Go Orchestrator 和 Rust Worker 每次启动时，均以 `hostname + pid + random` 规则生成唯一的消费者标识，支持节点随时动态扩缩容。
   - 在消费循环中，消费者优先调用 `XAUTOCLAIM` 命令拉取积压在 PEL（Pending Entries List）中且空闲超过 10 秒的流消息，自动接管已崩溃节点未完成的任务，确保系统不会出现死锁。


## 模块边界

为了保持可维护性，仓库中的边界需要明确。

### `web` 与 `server`

- 通过 HTTP API 通信
- 传递的是业务请求与任务状态
- `web` 不直接依赖服务端内部实现细节

### `server` 与 `worker`

- 通过任务模型、队列消息与任务状态存储协作
- `server` 负责创建任务
- `worker` 负责执行任务

### `worker` 与 `compute/crates`

- 通过 Rust crate 直接调用
- `worker` 不复制算法，只编排算法与适配器

### `web` 与 `compute/bindings/wasm`

- 通过 WASM 导出的 JS 友好接口交互
- 绑定层不应包含复杂业务规则

### Native App 与 `compute/bindings/ffi`

- 通过 C ABI 交互
- FFI 层只负责边界适配，不负责 UI 与产品流程

## 共享契约

跨模块的共享契约以 `compute-types` 为中心。

该层至少应承载：

- 任务标识
- 输入输出格式
- 编码参数
- 任务状态
- 错误分类
- 可扩展元数据

约束如下：

- 新增跨边界字段时，优先先改 `compute-types`
- `server` 与 `web` 的外部协议应尽量映射到这一层的稳定模型
- 不要在多个语言里各自维护语义相同但命名不同的结构

## 构建与交付边界

不同模块的产物不同：

- `server`
  - 交付 Go 二进制服务
- `compute-worker`
  - 交付 Rust Worker 二进制
- `compute-wasm`
  - 交付浏览器可消费的 WASM 包
- `compute-ffi`
  - 交付原生平台可链接的动态库或静态库
- `web`
  - 交付 Nuxt 构建产物

因此，Monorepo 的核心价值不是“统一生成一个产物”，而是：

- 统一源码边界
- 统一核心模型
- 统一任务编排方式
- 统一跨语言协作规范

## 演进原则

未来扩展时，建议遵循以下原则：

- 新的可移植编解码能力优先进入 `compute-engine`
- 新的宿主导出能力进入 `compute/bindings`
- 新的服务端系统集成进入 `compute-server-adapter` 或 `server/internal/*`
- 新的用户功能入口进入 `web`
- 新的跨边界数据结构优先收敛到 `compute-types`

避免的方向：

- 在 `server` 中直接实现核心转码逻辑
- 在 `web` 中复制媒体格式领域模型
- 在 `bindings` 中写复杂业务逻辑
- 在 `worker` 中混杂协议定义、业务流程和底层实现
- 使用语义含糊的目录名破坏边界

## 当前架构结论

这个仓库的最终边界可以总结为一句话：

- `web` 是产品入口
- `server` 是业务协调层
- `compute` 是能力核心
- `worker` 是执行器
- `wasm` 和 `ffi` 是分发面
- `types` 是共享契约中心

只要后续继续沿着这条边界演进，仓库就能同时支持：

- 浏览器内转码
- 服务端异步任务转码
- 原生移动端或桌面端集成
- 多语言协作下的长期演进
## Detailed Request Flow

The following steps describe the typical lifecycle of a transcoding task from the user interface to completion:

1. **01 Collect intent in web**: The UI captures the input source, target format, and execution mode, then packages that intent as an API-friendly task request (Output: request DTO).
2. **02 Normalize in server**: The Go API validates the payload, persists lifecycle state, and produces a queue-safe task envelope instead of pushing raw UI state downstream (Output: task envelope).
3. **03 Execute in compute-worker**: The worker resolves artifacts, maps transport values into compute-types, then selects compute-engine, compute-server-adapter, or a fallback path (Output: artifact + status).
4. **04 Distribute results cleanly**: The web console polls status through the API, while browser and native hosts can consume compute-wasm or compute-ffi without inheriting server-only concerns (Output: URLs + metadata).



## Design Principles

- **No physical foreign key constraints**: We use application-level logic to ensure data integrity. This avoids locking issues and facilitates horizontal scaling/sharding.
- **UUID v7 as Primary Keys**: All models use UUID v7 to provide time-ordered unique identifiers, which are more efficient for database indexing than random UUIDs.
- **Stateless API with Session Cache**: The API layer is stateless, but uses Redis for session management to support seamless authentication across components.
- **Strong Typing at Boundaries**: Using `gorm-gen` for type-safe database queries and `compute-types` for cross-language consistency.
