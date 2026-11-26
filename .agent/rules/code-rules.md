---
trigger: always_on
---

## Go
- Type Safety: Dont use `any` if possible.
- Use modern Go
  - `interface{}` -> `any`
- Logging: Use `slog`
- Fiber V3: the parameter name of c.Locals("<param_name>") must be snake case.

## TypeScript
- Type Safety: Don't use `any` if possible.
- Use Modern TypeScript
  - Use the Temporal API for every date and time. `Date` is not allowed anywhere (no `new Date`, `Date.now`, `Date.parse`). In `web/`, `task check:web` fails on any `Date` word in `app/` and `server/`; format through `useTime()` (`web/app/composables/useTime.ts`), which uses the i18n locale and the viewer's time zone.
  - Load Temporal on demand: use the native `globalThis.Temporal` when it exists; only when it does not, dynamically import the polyfill once, before anything uses it.

## Web
- Use shadcn components when possible.
- Type Safety for Backend APIs Responses.
- Server data: Use TanStack Query (`@tanstack/vue-query`) for fetching, polling, caching and invalidation after mutations.
  - Keys live in `web/app/lib/query-keys.ts`; queries and mutations live in `web/app/composables/queries/`.
  - Every query a page renders is prefetched with `prefetchOnServer`, and pages render its loading and error through `renderState`, so the server HTML and the hydrated cache agree.
- Virtual Scroll: Use TanStack Virtual only for lists that can exceed about 500 rows. Paginated lists do not need it.


## Rust
-