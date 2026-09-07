// Self-contained example MCP Apps view shipped with the module. It advertises
// the host version the module script was assembled with and keeps the default
// asset source non-empty out of the box; real view bundles are built by
// consuming applications' JS toolchains.
const version = globalThis.__MCPCANVAS_VERSION__ ?? "1.0.0";
const root = document.getElementById("app-root");
if (root) {
  root.textContent = "mcpcanvas example view (version " + version + ")";
}
export { version };
