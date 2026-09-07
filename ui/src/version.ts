// Shared runtime version. The host renderer prefixes each app module with a
// `window.__MCPCANVAS_VERSION__ = "<version>"` assignment, sourced from the
// consuming product's build version (stamped at build time). Apps advertise
// this as their version during the ui/initialize handshake instead of carrying
// a hardcoded per-app version. When running outside the rendered doc (e.g.
// vitest), fall back to a dev marker.
export const APP_VERSION: string =
  (globalThis as { __MCPCANVAS_VERSION__?: string }).__MCPCANVAS_VERSION__ ?? "dev";
