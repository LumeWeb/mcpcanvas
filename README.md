# mcpcanvas

Go package providing the host-side runtime for MCP Apps: a self-contained
document shell renderer plus a manifest-driven asset layer for bundled,
self-contained ESM app views served to sandboxed iframes.

## Usage

```go
import "go.lumeweb.com/mcpcanvas"
```

Render a complete ui:// document (inline theme CSS + app body + inline module
script). The theme stylesheet is passed explicitly — use the embedded default
`AppThemeCSS` or your own product-built CSS. Any component with a
`Render(ctx, w io.Writer) error` method (e.g. a templ component) works as the
body:

```go
doc, err := mcpcanvas.RenderAppDoc("My App", mcpcanvas.AppThemeCSS, bodyComponent, moduleJS)
```

Assemble the inline module script for a view from the default embedded asset
source (includes the `window.__MCPCANVAS_VERSION__` handshake global):

```go
src, err := mcpcanvas.DefaultSource()
moduleJS, err := mcpcanvas.ModuleJS(ctx, src, "example", appVersion)
```

Custom asset sources implement the `AssetSource` interface and describe their
bundles with a `Manifest` (view name, file, sha256 hash), validated with
`Manifest.Validate`. Sentinel errors: `ErrManifestSchema`, `ErrHashMismatch`,
`ErrMissingBundle`.

## API

| Member | Description |
|--------|-------------|
| `RenderAppDoc(title, css, body, moduleJS)` | Render a self-contained HTML document with the given stylesheet |
| `AppThemeCSS` | Embedded default theme stylesheet (pass to `RenderAppDoc`) |
| `BodyComponent` | Structural interface for body components (templ-compatible) |
| `DefaultSource()` | Embed-FS-backed `AssetSource` for the bundled assets |
| `NewEmbedSource(fsys)` | `AssetSource` over any `fs.FS` with a `manifest.json` |
| `ModuleJS(ctx, src, view, version)` | Version handshake global + bundle source for a view |
| `VersionGlobal(version)` | `window.__MCPCANVAS_VERSION__` assignment (normalized semver) |
| `BareModuleSpecifiers(src)` | Bare package imports a browser cannot resolve inline |
| `OpenView(ctx, src, view)` | Verified bundle bytes for a view |

Pass your application's own build version (`version`) to `ModuleJS` /
`VersionGlobal`; the library reads no package-global version state, and
non-semver values normalize to `1.0.0`.

## Development

```sh
go build ./...
go test -race ./...
mockery
```

## License

MIT — see [LICENSE](LICENSE).
