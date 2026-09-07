# AGENTS.md

This file provides development guidelines and architectural documentation for
the mcpcanvas project.

## Common Commands

### Building
```bash
# Build all packages
go build -v ./...
```

### Testing
```bash
# Run all tests with race detection and coverage
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...

# Run tests for the root package only
go test -v -race .

# View coverage report
go tool cover -func=coverage.out
```

### Code Generation

```bash
# Generate mocks for interfaces (uses .mockery.yaml; mockery is
# pre-installed at $HOME/go/bin/mockery — run it with no arguments, never
# reinstall it). Add an entry under `interfaces` in .mockery.yaml only when a
# test genuinely requires a generated mock.
mockery
```

### Dependency Management
```bash
# Download dependencies
go mod download

# Verify dependencies
go mod verify

# Tidy dependencies
go mod tidy
```

## Project Overview

mcpcanvas is the host-side Go runtime for MCP Apps: it renders the shared,
self-contained HTML document shell for ui:// app views and serves their
bundled ESM assets behind a small manifest-driven interface. The package is a
generic library — stdlib only, no CLI or product-specific screens; product
views live in consuming applications, which supply their own manifests and
asset sources.

## Architecture

### Package Structure
- **Module path**: `go.lumeweb.com/mcpcanvas`
- **Root package** (`mcpcanvas`): all Go functionality; `doc.go` holds the
  package documentation
- **`ui/`**: the TypeScript app runtime consumed by app views (not part of
  the Go build)
- **`mocks/`**: generated mocks (mockery, testify templates), when needed
- **`assets/`**: embedded default asset set (`manifest.json` + `dist/`
  bundles) backing `DefaultSource`
- **`css/`**: embedded shared theme stylesheet (`AppThemeCSS`)

### Design
- **Document rendering** (`render.go`): `RenderAppDoc` takes the theme
  stylesheet explicitly (no package-global reads at render time; the embedded
  `AppThemeCSS` var is the default callers pass) and fills a fixed
  text/template shell (doctype, head, inline `<style>`, inline
  `<script type="module">`). `text/template` is used deliberately — the
  module script and CSS contain characters `html/template` would escape.
  The body is rendered by the caller through the structural `BodyComponent`
  interface, so templ components plug in without the library importing templ.
- **Manifest** (`manifest.go`): `Manifest`/`Bundle` describe the available
  views; `Validate` enforces the schema (schema version, required fields,
  64-char lowercase hex sha256 hashes, unique view names) and returns
  sentinel errors (`ErrManifestSchema`, `ErrMissingBundle`).
- **Assets** (`assets.go`): `AssetSource` resolves a view to bytes;
  `OpenView` reads via `io.ReadSeeker` and verifies the recorded sha256
  (`ErrHashMismatch`). `EmbedSource` works over any `fs.FS` holding a
  `manifest.json`; `DefaultSource` serves the module's embedded `assets/`.
  `ModuleJS` assembles the inline module script (version handshake global +
  bundle); `VersionGlobal(version)` emits `window.__MCPCANVAS_VERSION__`
  with strict semver normalization (fallback `1.0.0`) — the caller passes
  its own app build version; the library holds no version global of its
  own. `BareModuleSpecifiers`
  flags bare package imports a browser cannot resolve in an inline module.

### Error Model
- Validation failures return `ErrManifestSchema` (wrapped) from
  `Manifest.Validate`
- View lookup returns `ErrMissingBundle` (wrapped) for unknown views
- Content hash verification returns `ErrHashMismatch` (wrapped)

### Testing Conventions
- Tests live in the root package and cover the public seam plus the
  self-containment guard for embedded bundles
- Do not add a README/board copyright header to source files; attribution
  lives only in the LICENSE file
- Generated mocks live in `mocks/` (mockery, testify templates)
