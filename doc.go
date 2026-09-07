// Package mcpcanvas provides the host-side Go runtime for rendering MCP Apps
// documents and serving their bundled assets.
//
// The package answers two problems for hosts that serve MCP Apps as
// self-contained ui:// documents in sandboxed iframes:
//
//   - Document rendering: a fixed, self-contained HTML shell (inline theme
//     CSS plus an inline ESM module script) with the app's body markup
//     rendered by the caller. Any component with a
//     `Render(ctx context.Context, w io.Writer) error` method (for example a
//     templ component) satisfies BodyComponent structurally; the library has
//     no templ dependency of its own. The theme stylesheet is passed
//     explicitly to RenderAppDoc (the library reads no package-global state
//     at render time); the embedded AppThemeCSS variable is the default
//     theme callers pass, and consumers may supply their own product-built
//     stylesheet instead.
//
//   - Asset serving: a small manifest-driven asset layer. A Manifest declares
//     the bundled, self-contained ESM views (view name, file, sha256 hash); an
//     AssetSource resolves a view to its bytes. The embed-FS-backed
//     EmbedSource ships a default asset set with this module; ModuleJS and
//     VersionGlobal assemble the inline module script for a view, including
//     the host version handshake global. The consuming application passes
//     its own build version (semver normalization happens inside the
//     library); no package-global version state is read or written.
//
// Document rendering:
//
//	doc, err := mcpcanvas.RenderAppDoc("My App", mcpcanvas.AppThemeCSS, bodyComponent, moduleJS)
//
// Module assembly from the default embedded asset source:
//
//	src, err := mcpcanvas.DefaultSource()
//	moduleJS, err := mcpcanvas.ModuleJS(ctx, src, "example", appVersion)
//
// The package is stdlib-only and holds no product-specific screens, routing,
// or API details; product views live in consuming applications, which supply
// their own manifests and asset sources.
package mcpcanvas
