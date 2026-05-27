# Root task runner for the transcoder monorepo.
#
# Top-level layout:
# - server: Go API and task orchestration
# - compute: Rust workspace for engine, bindings, and worker
# - web: Nuxt frontend

set shell := ["sh", "-eu", "-c"]

help: list

list:
    @just --list

bootstrap: bootstrap-web bootstrap-server bootstrap-compute

bootstrap-web:
    bun install

bootstrap-server:
    go work sync

bootstrap-compute:
    cargo fetch --workspace

bootstrap-wasm:
    @wasm-pack --version >/dev/null 2>&1 || (echo "wasm-pack is required for WASM packaging recipes." && exit 1)

doctor:
    bun --version
    go version
    cargo --version
    just --version
    @wasm-pack --version || echo "wasm-pack not installed; only required for build-compute-wasm."

dev:
    @echo "Run one component per terminal:"
    @echo "  just dev-web"
    @echo "  just dev-server"
    @echo "  just dev-worker"

dev-web:
    cd web && bun run dev

dev-server:
    go run ./server/cmd/api/main.go

dev-orchestrator:
    go run ./server/cmd/worker/main.go

dev-worker:
    cargo run -p compute-worker

build: build-web build-server build-compute

build-all: build build-compute-wasm

build-web:
    cd web && bun run build

build-server:
    go build ./server/...

build-compute:
    cargo build --workspace --release

build-compute-wasm: bootstrap-wasm
    wasm-pack build compute/bindings/wasm --target web

build-compute-ffi:
    cargo build -p compute-ffi --release

build-worker:
    cargo build -p compute-worker --release

fmt: fmt-server fmt-compute

fmt-server:
    go fmt ./server/...

fmt-compute:
    cargo fmt --all

check: check-web check-server check-compute

check-web:
    cd web && bunx nuxi prepare
    cd web && bunx vue-tsc -b --noEmit

check-server:
    go test ./server/...

check-compute:
    cargo check --workspace

test: test-web test-server test-compute

# The web app does not have a dedicated test runner yet, so Nuxt prepare plus vue-tsc is the current smoke test.
test-web:
    cd web && bunx nuxi prepare
    cd web && bunx vue-tsc -b --noEmit

test-server:
    go test ./server/...

test-compute:
    cargo test --workspace

clean: clean-web clean-server clean-compute

clean-web:
    bun -e "const fs=require('fs'); for (const p of ['web/.nuxt','web/.output','web/.nitro','web/dist','web/.cache']) fs.rmSync(p,{recursive:true,force:true});"

clean-server:
    go clean -cache -testcache

clean-compute:
    cargo clean
    bun -e "const fs=require('fs'); for (const p of ['compute/bindings/wasm/pkg']) fs.rmSync(p,{recursive:true,force:true});"

reset: clean
    bun -e "const fs=require('fs'); for (const p of ['node_modules','web/node_modules']) fs.rmSync(p,{recursive:true,force:true});"
