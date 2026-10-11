# Web

Nuxt 4 frontend for `lightyear`: the product UI, the thin API client and proxy, and the in-browser sandbox running libvips through wasm-vips.

Contracts and behavior live in the docs site: [Getting started](../docs/getting-started.md), [HTTP API](../docs/contracts/http-api.md), [ImageOptions](../docs/contracts/image-options.md), [Known limitations](../docs/compute/limitations.md).

## Stack

- Nuxt 4 (`srcDir: app`) + Vue 3.5 + Vite
- Tailwind CSS 4 (CSS-first, no `tailwind.config`)
- shadcn-nuxt + reka-ui primitives
- @nuxtjs/i18n with 6 locales (`en-US` default, `zh-Hans`, `zh-Hant`, `ja-JP`, `de-DE`, `fr-FR`)
- Uppy (S3 multipart uploads), wasm-vips, VueUse

## Responsibilities

- user interface, upload flow, task submission and status views
- API client with auth token handling and error-code mapping
- transparent proxying of `/api/**` to the Go backend
- local image processing with wasm-vips in a Web Worker

Out of scope: scheduling, persistence, auth decisions.

## Directory layout

```text
web/
├── app/
│   ├── app.vue                 # shell (auth modal, toaster, user fetch)
│   ├── pages/                  # index (sandbox), overview, tasks/*
│   ├── components/             # ImageOptionsForm, TaskRetryDrawer, JobErrorDialog, AuthModal, ui/*
│   ├── composables/            # useApi, useAuth, useVips
│   ├── lib/                    # image-options helpers, vips-ops (browser twin of compute-imageops)
│   ├── lib/types/              # API DTOs; image-options.ts is generated (task gen:types)
│   ├── workers/vips.worker.ts
│   └── assets/styles/main.css  # Tailwind 4 @theme tokens
├── i18n/locales/               # 6 locale files
└── nuxt.config.ts
```

## Integration surface

- **API**: `useApi` creates a `$fetch` client with `baseURL: /api`; the Nitro route rule proxies `/api/**` to `http://localhost:8080/api/**`. Auth uses `auth_token` cookies plus `Authorization: Bearer` and opens the auth modal on 401.
- **Sandbox**: `useVips` starts `vips.worker.ts` on first use; the worker loads wasm-vips with the AVIF/HEIF and JXL modules (Vite `?url` imports), blocks untrusted loaders like the server, and runs `lib/vips-ops.ts`. Results come back as transferable `Uint8Array`s. wasm-vips needs `SharedArrayBuffer`, so `nuxt.config.ts` sends COOP `same-origin` + COEP `credentialless` on every route (client-side navigation into the sandbox must already be isolated).
- **Options UI**: `ImageOptionsForm.vue` edits an `ImageOptions` and hides fields that do not apply; the new-task page, workflow presets, the retry drawer and the sandbox (without HEIC) all use it. `scripts/consistency.ts` checks that `vips-ops.ts` produces the same artifacts as `compute-imageops`.

## Development

From the repo root:

```bash
task bootstrap       # bun install
task web             # or: bun run dev:web   (:3000)
task check:web       # or: cd web && bunx vue-tsc -b --noEmit
task build:web       # Nuxt bundle
task gen:types       # after changing ImageOptions in compute-types
```

## Current status

- [x] Nitro proxy to the Go API (`/api/**`)
- [x] Auth flows (password + OAuth) and session handling
- [x] Task creation, listing, polling (`2s`), retry and cancel
- [x] Multipart uploads with fast-upload support, drafts and workflow presets
- [x] Sandbox on wasm-vips: queue, per-file options, comparison views, batch download
- [x] i18n across six locales

### Known gaps

- No runtime config/env variables are consumed yet; API base URL is fixed through the Nitro proxy
- `app/lib/api/` does not exist — the client lives in `composables/useApi.ts`
- No frontend test tooling (typecheck only)
- The sandbox is unavailable in Safari (no COEP `credentialless`) and cannot read or write HEIC (see [Known limitations](../docs/compute/limitations.md))
