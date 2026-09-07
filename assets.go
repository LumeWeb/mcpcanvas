package mcpcanvas

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
)

// assets holds the static assets for MCP Apps views: the manifest describing
// the servable views and the per-view, fully self-contained ESM bundles
// under dist/.
//
// Each bundle is self-contained (zero imports): the whole app client plus the
// view's flow logic are inlined, so a ui:// view can be served as a single
// HTML document with no external dependencies and no runtime module loading —
// the sandboxed iframe cannot resolve file imports.
//
// ManifestFile inside it is parseable JSON (see assets/manifest.json) and is
// what EmbedSource reads; DefaultSource reads it through the same embedded FS
// at init, so there is a single source of truth.
//
//go:embed all:assets
var assetsFS embed.FS

// bootstrapAssetsManifest reads, parses, and validates the embedded manifest
// once at init: a malformed shipped manifest is a build-time programming
// error, not a first-use runtime surprise.
func bootstrapAssetsManifest() Manifest {
	data, err := fs.ReadFile(assetsFS, "assets/"+ManifestFile)
	if err != nil {
		panic("mcpcanvas: embedded asset manifest missing: " + err.Error())
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		panic("mcpcanvas: embedded asset manifest is not valid JSON: " + err.Error())
	}
	if err := m.Validate(); err != nil {
		panic("mcpcanvas: embedded asset manifest invalid: " + err.Error())
	}
	return m
}

// defaultManifest is the validated Manifest served by DefaultSource.
var defaultManifest = bootstrapAssetsManifest()

// ManifestFile is the filename EmbedSource reads its Manifest from, relative
// to the source's root FS.
const ManifestFile = "manifest.json"

// EmbedSource is an AssetSource backed by an fs.FS laid out as a
// manifest.json (see ManifestFile and the Manifest type) plus the bundle
// files the manifest references. With the module's own embedded assetsFS it
// serves the default asset set.
type EmbedSource struct {
	root     fs.FS
	manifest Manifest
}

// NewEmbedSource returns an AssetSource over fsys, reading manifest.json
// from its root. An error wraps ErrManifestSchema if the manifest is missing,
// unreadable, unparsable, or fails Validate.
func NewEmbedSource(fsys fs.FS) (*EmbedSource, error) {
	data, err := fs.ReadFile(fsys, ManifestFile)
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %v", ErrManifestSchema, ManifestFile, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrManifestSchema, ManifestFile, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &EmbedSource{root: fsys, manifest: m}, nil
}

// DefaultSource returns an AssetSource over the asset set embedded in this
// module (assets/manifest.json + assets/dist bundles). The embedded manifest
// is validated at package init, so the error return is always nil.
func DefaultSource() (*EmbedSource, error) {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		return nil, fmt.Errorf("mcpcanvas: embed assets root: %w", err)
	}
	return &EmbedSource{root: sub, manifest: defaultManifest}, nil
}

// Manifest returns the source's validated Manifest. The manifest is
// completely parsed and validated at construction time.
func (s *EmbedSource) Manifest(context.Context) (Manifest, error) {
	return s.manifest, nil
}

// Open returns a ReadSeeker over the bundle registered for view, rewound to
// its start. Unknown views wrap ErrMissingBundle; a manifest entry whose file
// is missing from the underlying FS is reported as such.
func (s *EmbedSource) Open(_ context.Context, view string) (io.ReadSeeker, error) {
	bundle, err := s.manifest.Lookup(view)
	if err != nil {
		return nil, err
	}
	f, err := s.root.Open(bundle.File)
	if err != nil {
		return nil, fmt.Errorf("mcpcanvas: open bundle for view %q (%s): %w", view, bundle.File, err)
	}
	seeker, ok := f.(io.Seeker)
	if !ok {
		_ = f.Close()
		return nil, fmt.Errorf("mcpcanvas: bundle file for view %q (%s) is not seekable", view, bundle.File)
	}
	if _, err := seeker.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("mcpcanvas: seek bundle for view %q: %w", view, err)
	}
	return struct {
		io.Reader
		io.Seeker
	}{f, seeker}, nil
}

// semverRaw matches a bare semver core (MAJOR.MINOR.PATCH with optional
// leading "v", optional -prerelease/+build suffix). Anything else (e.g.
// "develop", commit hashes) is not valid for the MCP ui/initialize
// handshake.
var semverRaw = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)$`)

// semverNormalize coerces the raw build version into a value the MCP ext-apps
// host accepts as a valid semver app version. A version already shaped as
// MAJOR.MINOR.PATCH (optionally "v"-prefixed) passes through (v stripped); any
// any non-semver value (e.g. "develop" in un-stamped dev builds) falls back to
// "1.0.0" so the handshake never advertises an invalid version and leaves the
// app inert.
func semverNormalize(raw string) string {
	if m := semverRaw.FindStringSubmatch(raw); m != nil {
		return m[1]
	}
	return "1.0.0"
}

// VersionGlobal returns a module-scope assignment that exposes the caller's
// app build version to the bundled app: pass the consuming application's own
// version (for example an ldflags-stamped build version; non-semver values
// such as "develop" are accepted). The app advertises this as its version
// during the ui/initialize handshake, so apps inherit the host binary's
// version instead of carrying a hardcoded per-view version. The value is
// normalized to valid semver here so the host never rejects the handshake on
// an un-stamped or otherwise un-normalized version.
func VersionGlobal(version string) string {
	return "window.__MCPCANVAS_VERSION__ = " + strconv.Quote(semverNormalize(version)) + ";"
}

// digestHex returns the lowercase hex sha256 of r, leaving r's read offset
// wherever it ends (callers that need to re-read should seek back first).
func digestHex(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("mcpcanvas: hash bundle: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// OpenView returns the verified bundle bytes for view from src: it resolves
// the view through src.Manifest (unknown views wrap ErrMissingBundle), reads
// the content via src.Open, and verifies its sha256 against the manifest
// hash (mismatches wrap ErrHashMismatch).
func OpenView(ctx context.Context, src AssetSource, view string) ([]byte, error) {
	manifest, err := src.Manifest(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcpcanvas: load manifest for view %q: %w", view, err)
	}
	bundle, err := manifest.Lookup(view)
	if err != nil {
		return nil, err
	}
	r, err := src.Open(ctx, view)
	if err != nil {
		return nil, fmt.Errorf("mcpcanvas: open bundle for view %q: %w", view, err)
	}
	got, err := digestHex(r)
	if err != nil {
		return nil, err
	}
	if got != bundle.Hash {
		return nil, fmt.Errorf("%w: view %q: manifest %s, content %s",
			ErrHashMismatch, view, bundle.Hash, got)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("mcpcanvas: rewind bundle for view %q: %w", view, err)
	}
	return io.ReadAll(r)
}

// ModuleJS returns the inline module script for a view: the verified bundle
// source (via OpenView) prefixed with the version handshake global. Use it
// with RenderAppDoc so the version handshake is always present. The version
// parameter is the caller's own app build version (see VersionGlobal).
// Unknown views wrap ErrMissingBundle; content/hash problems wrap
// ErrHashMismatch.
func ModuleJS(ctx context.Context, src AssetSource, view string, version string) (string, error) {
	data, err := OpenView(ctx, src, view)
	if err != nil {
		return "", err
	}
	return VersionGlobal(version) + string(data), nil
}

// fromSpecifierRe matches the module-specifier string in `import ... from
// "spec"`, side-effect `import "spec"`, and dynamic `import("spec")` forms.
// Minified bundles drop the space around the keyword/specifier, so both
// `from "x"` and `from"x"` (and `import"x"` / `import("x")`) must match.
var fromSpecifierRe = regexp.MustCompile(`from\s*["']([^"']+)["']|(?:^|[;)\]}])import\s*["']([^"']+)["']|(?:^|[;)\]}])import\s*\(\s*["']([^"']+)["']`)

// BareModuleSpecifiers returns any module specifiers in an inline-ready
// bundle that the browser cannot resolve on its own: bare package specifiers
// (e.g. "zod") that do NOT start with ".", "/", or a URL scheme. A
// sandboxed iframe serves each view as a single inline <script type="module">
// with no importer and no node_modules, so any such specifier throws "Failed
// to resolve module specifier ..." and kills the app at load time. Use it as
// a build/test-time self-containment guard over every shipped bundle.
func BareModuleSpecifiers(src string) []string {
	seen := map[string]bool{}
	for _, m := range fromSpecifierRe.FindAllStringSubmatch(src, -1) {
		spec := m[1]
		if spec == "" {
			spec = m[2]
		}
		if spec == "" {
			spec = m[3]
		}
		if spec == "" {
			continue
		}
		if len(spec) > 0 && (spec[0] == '.' || spec[0] == '/') {
			continue
		}
		if isResolvableURL(spec) {
			continue
		}
		seen[spec] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// isResolvableURL reports whether a specifier is an absolute URL the browser
// can fetch directly (e.g. https://... or //cdn...), which is fine inline.
func isResolvableURL(spec string) bool {
	for _, p := range []string{"https://", "http://", "//", "data:", "blob:"} {
		if len(spec) >= len(p) && spec[:len(p)] == p {
			return true
		}
	}
	return false
}
