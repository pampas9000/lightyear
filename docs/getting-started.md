# 快速开始

## 前置依赖

- Bun 1.0+
- Go 1.26+
- Rust stable（edition 2024，`rust-toolchain.toml` 已固定）
- [`task`](https://taskfile.dev)（Taskfile runner，Monorepo 主入口）
- `wasm-pack`（构建浏览器 WASM 包）
- 服务端媒体工具（可选，缺失时按严格/降级策略处理）：`cjxl`、`avifenc`、`avifdec`、`heif-dec`、`cwebp`

## 仓库布局

```text
.
├── server/                     # Go API + 编排器
│   ├── cmd/api/                # HTTP API 进程入口
│   ├── cmd/worker/             # Orchestrator 守护进程（调度 / 结果回收 / 租约恢复）
│   ├── cmd/gen/                # gorm/gen 代码生成
│   ├── internal/api/           # 路由、handlers、middleware、响应封装
│   ├── internal/services/      # auth / job / task / workflow / storage 业务逻辑
│   ├── internal/task/          # Redis Streams 队列契约
│   ├── internal/transcode/     # 转码参数模型与校验
│   ├── internal/models/        # GORM 实体
│   ├── internal/query/         # gorm/gen 生成代码
│   ├── internal/config/        # koanf 配置加载
│   └── internal/pkg/s3/        # S3/R2 客户端
├── compute/                    # Rust 计算工作区
│   ├── crates/types/           # compute-types：共享领域类型与线上协议
│   ├── crates/engine/          # compute-engine：可移植编解码 / 色彩 / 编辑核心
│   ├── crates/server-adapter/  # compute-server-adapter：run_transcode 宿主管线
│   ├── bindings/wasm/          # compute-wasm：浏览器导出
│   └── worker/                 # compute-worker：Redis/S3 消费者
├── web/                        # Nuxt 4 前端（app/ 目录结构）
├── docs/                       # 本文档站（VitePress）
├── scripts/                    # benchmark 与画质素材
├── Cargo.toml                  # Rust workspace
├── go.work                     # Go workspace
├── package.json                # bun workspace 与根脚本
└── Taskfile.yml                # Monorepo 任务入口
```

## 常用命令

以根目录 `Taskfile.yml` 为准：

```bash
task --list           # 查看全部任务
task bootstrap        # bun install + go work sync + cargo fetch
task dev              # api + orchestrator + worker + web
task dev:backend      # api + orchestrator + worker
task web              # Nuxt 开发服务器（3000）
task api              # Go HTTP API（默认 8080）
task orchestrator     # Go 调度/回收守护进程
task worker           # Rust compute-worker
task check            # server 编译 / cargo check / vue-tsc
task test             # go test + cargo test + vue-tsc
task build:all        # server 二进制 + cargo release + wasm + web
```

直接工具链回退（适合单生态调试）：

```bash
go run ./server/cmd/api            # API
go run ./server/cmd/worker         # Orchestrator
cargo run -p compute-worker        # Rust worker
cargo test --workspace             # Rust 测试
go test ./server/...               # Go 测试
cd web && bun run dev              # 前端
bun run build:compute:wasm         # 构建 WASM 到 web/public/wasm
bun run benchmark                  # 画质素材 + 容器头断言 + cargo test
bun run docs:dev                   # 文档站
```

## 端口与依赖服务

| 服务 | 默认地址 | 说明 |
| :--- | :--- | :--- |
| Go API | `http://localhost:8080` | `SERVER_ADDR` > `PORT` > `[::]:8080`；Taskfile 提示里的 8000 是过期文案 |
| Nuxt Web | `http://localhost:3000` | Nitro 将 `/api/**` 代理到 `http://localhost:8080/api/**` |
| PostgreSQL | `localhost:5432` | `DATABASE_URL` 默认 `transcoder` 库 |
| Redis | `localhost:6379` | `REDIS_URL` 默认 `redis://localhost:6379/0` |
| S3 / R2 | `S3_ENDPOINT` | 需 `S3_ACCESS_ID` / `S3_ACCESS_KEY` / `S3_BUCKET` |

服务端配置从 `server/.env`（或仓库根 `.env`）与环境变量读取，完整列表见 [server README](https://github.com/pampas9000/lightyear/blob/main/server/README.md) 与[接口契约](/contracts/)。

## 下一步

- [架构总览](/architecture/overview)：边界、组件与数据模型
- [请求链路](/architecture/request-flows)：三条执行路径
- [可靠性设计](/architecture/reliability)：attempt_id / 租约 / XAUTOCLAIM
- [接口契约](/contracts/)：HTTP、队列、WASM
