# 快速开始

## 前置依赖

| 工具 | 用途 |
| :--- | :--- |
| Bun 1.0+ | Web 依赖、文档站、类型生成 |
| Node ≥ 23.6 | 服务端/浏览器一致性测试（TypeScript 类型擦除） |
| Go 1.26+ | API 与 orchestrator |
| Rust nightly | 由 `rust-toolchain.toml` 指定（edition 2024） |
| [`task`](https://taskfile.dev) | 仓库任务入口 |
| libvips ≥ 8.17 + `pkg-config` | worker 处理核心；需带 libheif（AVIF/HEIC）、libjxl、libultrahdr |
| `cjxl` / `djxl`（libjxl 工具） | JPEG → JXL 无损重新打包；测试需要 `djxl` |
| PostgreSQL、Redis、S3 兼容存储 | 运行时依赖 |

macOS（Homebrew 的 vips 已包含 libheif、libjxl、libultrahdr）：

```bash
brew install vips jpeg-xl pkgconf go-task
```

Linux 发行版自带的 libvips 往往低于 8.17，需要从源码构建或使用较新的发行版；`vips --vips-config` 中 `UHDR`、`JXL`、`HEIC/AVIF` 三项应为 `true`。

`task doctor` 会打印各工具版本，缺少任何一项都会报错。

## 首次运行

```bash
task bootstrap                         # bun install + go work sync + cargo fetch
cp .env.example .env                   # 填写 DATABASE_URL、REDIS_URL、S3_*
go run ./server/cmd/migrate            # 建表（API 启动时 AUTO_MIGRATE=true 也会执行）
task dev                               # API + orchestrator + worker + Web
```

打开 `http://localhost:3000`。

::: warning 表结构不兼容时
数据模型改动后自动迁移无法处理已有数据（例如新增的非空列）。开发库可以直接重建：

```bash
go run ./server/cmd/migrate -reset     # 删除全部应用表后重建，数据全部丢失
```

`-reset` 使用 `.env` 中的 `DATABASE_URL`，执行前确认它指向开发库。
:::

## 仓库布局

```text
.
├── server/                     # Go
│   ├── cmd/api/                # HTTP API
│   ├── cmd/worker/             # orchestrator（派发 / 收结果 / 恢复）
│   ├── cmd/migrate/            # 建表；-reset 重建
│   └── internal/
│       ├── api/                # 路由、handlers、中间件、响应封装
│       ├── imageoptions/       # ImageOptions 的 Go 镜像、规范化、产物键
│       ├── models/ schema/     # GORM 模型与迁移
│       ├── services/           # auth / task / job / workflow / orchestrator / storage
│       ├── task/               # 队列契约（v2）
│       └── config/ pkg/s3/     # 配置与 S3 客户端
├── compute/                    # Rust
│   ├── crates/types/           # compute-types：ImageOptions 唯一定义、队列协议
│   ├── crates/imageops/        # compute-imageops：libvips 处理核心
│   └── worker/                 # compute-worker：Redis/S3 消费者
├── web/                        # Nuxt 4（app/ 目录结构），沙盒用 wasm-vips
├── docs/                       # 本文档站（VitePress）；contracts/ 下有 Schema 与 fixtures
├── scripts/                    # 一致性测试与素材
└── Taskfile.yml
```

## 常用命令

```bash
task --list            # 全部任务
task dev               # API + orchestrator + worker + Web
task dev:backend       # 只启动后端三个进程
task check             # Go 编译、cargo check、vue-tsc、契约是否最新
task test              # go test + cargo test + vue-tsc
task test:fixtures     # 全部素材 × 六种格式（release 构建）
task test:consistency  # 服务端与浏览器产物一致性
task gen:types         # 修改 ImageOptions 后重新生成 Schema 与 TS 类型
task docs              # 文档站
```

Go 的数据库测试需要一个可丢弃的库：

```bash
TEST_DATABASE_URL='postgres://postgres@127.0.0.1:5432/lightyear_test?sslmode=disable' go test ./server/...
```

未设置时这些测试会跳过。

## 端口与配置

| 服务 | 默认地址 | 说明 |
| :--- | :--- | :--- |
| API | `http://localhost:8080` | `SERVER_ADDR` 优先于 `PORT` |
| Web | `http://localhost:3000` | Nitro 把 `/api/**` 代理到 API |
| PostgreSQL | `DATABASE_URL` | |
| Redis | `REDIS_URL`，默认 `redis://localhost:6379/0` | 会话、队列、取消标记 |
| S3 / R2 | `S3_ENDPOINT`、`S3_ACCESS_ID`、`S3_ACCESS_KEY`、`S3_BUCKET`、`S3_REGION`（空为 `auto`） | |

API 与 orchestrator 从 `server/.env`（没有则用根目录 `.env`）和环境变量读取配置；worker 读取当前目录的 `.env` 与环境变量，另有 `WORKER_MAX_SOURCE_BYTES`（默认 200 MB）与 `WORKER_MAX_PIXELS`（默认 1 亿）。完整列表见 `.env.example`。

## 下一步

- [架构总览](/architecture/overview) · [请求链路](/architecture/request-flows) · [可靠性设计](/architecture/reliability)
- [ImageOptions](/contracts/image-options) · [处理核心](/compute/imageops) · [已知限制](/compute/limitations)
