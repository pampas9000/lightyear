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
  - Use the Temporal API for every date and time. `Date` is not allowed anywhere (no `new Date`, `Date.now`, `Date.parse`).
  - Load Temporal on demand: use the native `globalThis.Temporal` when it exists; only when it does not, dynamically import the polyfill once, before anything uses it.

## Web
- Use shadcn components when possible.
- Type Safety for Backend APIs Responses.
- Server data: Use TanStack Query (`@tanstack/vue-query`) for fetching, polling, caching and invalidation after mutations.
- Virtual Scroll: Use TanStack Virtual only for lists that can exceed about 500 rows. Paginated lists do not need it.


## Rust
-