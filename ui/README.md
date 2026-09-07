# @lumeweb/mcpcanvas

Generic frontend runtime for host-rendered MCP Apps. This package owns the
MCP Apps bridge/runtime: connect, the boot/mount contract, the loader,
flow/link state machines, and DOM status helpers. It stays free of
product-specific screens, routing, and API details; product screens live in
consuming products.

## No build step

There is deliberately **no tsdown/esbuild build step** in this package and no
build output. Consumers bundle the TypeScript runtime themselves by importing
`@lumeweb/mcpcanvas` (which resolves to `./src/index.ts`). Reasons:

- Each MCP App is served alone in a sandboxed `ui://` iframe and cannot resolve
  imports, so every app bundle must be fully self-contained — the consuming
  product's bundler inlines the ext-apps client (and its MCP SDK + zod deps)
  into each app bundle anyway, at its own granularity.
- A shared pre-built bundle would pin a bundler output format and duplicate
  dependency copies; bundling from source lets each consumer tree-shake and
  control target/browser baselines.

`@modelcontextprotocol/ext-apps` and `robot3` are runtime imports and are
declared as `dependencies` with exact pinned versions, so bundling consumers
resolve the exact runtime versions this runtime was tested against (no build
runs here anyway).

## Version handshake

Apps do not carry a hardcoded per-app version. The host renderer prefixes each
app module with `window.__MCPCANVAS_VERSION__ = "<version>"` (sourced from the
consuming product's build version). `APP_VERSION` reads that global and is
advertised as the App version during the `ui/initialize` handshake, falling
back to `"dev"` when running outside a rendered document (e.g. vitest).

## Development

```sh
pnpm install
pnpm exec vitest run
pnpm exec tsc --noEmit
```
