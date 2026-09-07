package mcpcanvas

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

// Characterization tests for the host-side MCP Apps document shell and the
// manifest-driven asset layer. The view JS behavioral logic is tested by the
// TypeScript runtime's own suite; these tests pin the Go-side seam: the
// shared document shell, the embedded theme, the semver normalize rules, the
// manifest validation and view resolution error paths, and the
// self-containment guard over every embedded bundle.

// fakeBody is a minimal BodyComponent: any component with a Render(ctx, w)
// method (e.g. a templ component) plugs into RenderAppDoc.
type fakeBody struct {
	markup string
	err    error
}

func (f *fakeBody) Render(_ context.Context, w io.Writer) error {
	if f.err != nil {
		return f.err
	}
	_, err := io.WriteString(w, f.markup)
	return err
}

func TestRenderAppDoc(t *testing.T) {
	doc, err := RenderAppDoc("Test", AppThemeCSS, &fakeBody{markup: "<p>hello</p>"}, "/* module */")
	if err != nil {
		t.Fatalf("RenderAppDoc returned error: %v", err)
	}
	for _, want := range []string{
		"<!doctype html>",
		"<title>Test</title>",
		"<script type=\"module\">",
		"/* module */",
		"<p>hello</p>",
		"</body></html>",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("render doc missing %q", want)
		}
	}
}

// TestAppThemeCSSEmbedded pins that the compiled Tailwind theme is embedded
// and inlined into every app document. A missing/empty tailwind.css (theme
// not present before building Go) would leave apps unstyled.
func TestAppThemeCSSEmbedded(t *testing.T) {
	if strings.TrimSpace(AppThemeCSS) == "" {
		t.Fatal("embedded app theme CSS is empty — the theme stylesheet must be present before building Go")
	}
	doc, err := RenderAppDoc("Test", AppThemeCSS, &fakeBody{markup: "<div/>"}, "/* module */")
	if err != nil {
		t.Fatalf("RenderAppDoc returned error: %v", err)
	}
	for _, want := range []string{"<style>", "app-shell", "text-status-ok", "text-status-error"} {
		if !strings.Contains(doc, want) {
			t.Errorf("rendered doc missing %q (theme not inlined?)", want)
		}
	}
}

// TestRenderAppDocBodyError proves a failing body component surfaces as an
// error instead of silently rendering an empty body.
func TestRenderAppDocBodyError(t *testing.T) {
	sentinel := errors.New("boom")
	if _, err := RenderAppDoc("Test", AppThemeCSS, &fakeBody{err: sentinel}, "/* module */"); !errors.Is(err, sentinel) {
		t.Fatalf("RenderAppDoc err = %v, want it to wrap the body error", err)
	}
}

// TestSemverNormalize pins that advertised app versions are always valid
// semver (the ext-apps host rejects non-semver), passing through real build
// versions and falling back to "1.0.0" for un-stamped/non-semver values.
func TestSemverNormalize(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"v0.2.1", "0.2.1"},
		{"0.2.1", "0.2.1"},
		{"1.0.0", "1.0.0"},
		{"v1.2.3-rc.1", "1.2.3-rc.1"},
		{"v1.2.3+build.5", "1.2.3+build.5"},
		{"develop", "1.0.0"},
		{"", "1.0.0"},
		{"abcdef1234567890", "1.0.0"}, // un-stamped dev/commit-ish value
		{"master", "1.0.0"},
	}
	for _, c := range cases {
		if got := semverNormalize(c.in); got != c.want {
			t.Errorf("semverNormalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestVersionGlobalInjectsHandshake proves VersionGlobal emits the
// __MCPCANVAS_VERSION__ handshake global with a non-empty, quoted, normalized
// semver value for both stamped versions and un-stamped fallbacks. The
// caller passes its own app version explicitly; the library owns no global
// version state.
func TestVersionGlobalInjectsHandshake(t *testing.T) {
	const prefix = "window.__MCPCANVAS_VERSION__ = "
	global := VersionGlobal("v0.2.1")
	if !strings.HasPrefix(global, prefix) {
		t.Fatalf("VersionGlobal did not emit the %q prefix: %q", prefix, global)
	}
	rest := strings.TrimPrefix(global, prefix)
	if !strings.HasSuffix(rest, ";") {
		t.Fatalf("version assignment unterminated: %q", global)
	}
	quoted := strings.TrimSuffix(rest, ";")
	if len(quoted) < 3 || quoted[0] != '"' || quoted[len(quoted)-1] != '"' {
		t.Fatalf("version not a quoted string literal: %q", quoted)
	}
	if val := strings.Trim(quoted, `"`); val == "" || semverNormalize(val) != val {
		t.Fatalf("version global %q is not a normalized semver value", quoted)
	}

	// The value must track the caller-passed version with the documented
	// fallback: un-stamped builds normalize to valid semver so the
	// ui/initialize handshake never advertises an invalid version.
	if got := VersionGlobal("develop"); got != prefix+`"1.0.0";` {
		t.Errorf("VersionGlobal with un-stamped version = %q, want %q", got, prefix+`"1.0.0";`)
	}
	if got := VersionGlobal("v2.3.4"); got != prefix+`"2.3.4";` {
		t.Errorf("VersionGlobal with stamped version = %q, want %q", got, prefix+`"2.3.4";`)
	}
}

// TestManifestValidate errors wrap ErrManifestSchema: bad schema version,
// missing compatibility marker, empty fields, malformed hashes, duplicate
// views. A well-formed manifest passes and Lookup resolves known views only.
func TestManifestValidate(t *testing.T) {
	valid := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		Compatibility: "test",
		Bundles: []Bundle{
			{View: "a", File: "a.js", Hash: strings.Repeat("a", 64)},
			{View: "b", File: "b.js", Hash: strings.Repeat("b", 64)},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	if _, err := valid.Lookup("b"); err != nil {
		t.Errorf("Lookup(existing) = %v, want nil", err)
	}
	if _, err := valid.Lookup("missing"); !errors.Is(err, ErrMissingBundle) {
		t.Errorf("Lookup(unknown) = %v, want ErrMissingBundle", err)
	}

	cases := map[string]func(*Manifest){
		"bad schema version":  func(m *Manifest) { m.SchemaVersion = 99 },
		"no compatibility":    func(m *Manifest) { m.Compatibility = "" },
		"empty view":          func(m *Manifest) { m.Bundles[0].View = "" },
		"empty file":          func(m *Manifest) { m.Bundles[0].File = "" },
		"malformed hash":      func(m *Manifest) { m.Bundles[0].Hash = "not-a-hash" },
		"uppercase hash":      func(m *Manifest) { m.Bundles[0].Hash = strings.Repeat("A", 64) },
		"short hash":          func(m *Manifest) { m.Bundles[0].Hash = strings.Repeat("a", 63) },
		"duplicate view name": func(m *Manifest) { m.Bundles[1].View = "a" },
	}
	for name, mutate := range cases {
		m := valid
		mutate(&m)
		if err := m.Validate(); !errors.Is(err, ErrManifestSchema) {
			t.Errorf("%s: Validate() = %v, want ErrManifestSchema", name, err)
		}
	}
}

func TestNewEmbedSource(t *testing.T) {
	ctx := context.Background()
	src, err := NewEmbedSource(fstest.MapFS{
		"manifest.json": &fstest.MapFile{Data: []byte(`{"schemaVersion":1,"compatibility":"test","bundles":[{"view":"v","file":"v.js","hash":"` + strings.Repeat("0", 64) + `"}]}`)},
	})
	if err != nil {
		t.Fatalf("NewEmbedSource() error: %v", err)
	}
	m, err := src.Manifest(ctx)
	if err != nil {
		t.Fatalf("Manifest() error: %v", err)
	}
	if _, err := m.Lookup("v"); err != nil {
		t.Fatalf("Lookup(v) error: %v", err)
	}

	// A missing or unparsable manifest is a manifest schema error.
	for name, fsys := range map[string]fstest.MapFS{
		"missing manifest":    {},
		"unparsable manifest": {"manifest.json": &fstest.MapFile{Data: []byte("not json")}},
		"invalid manifest":    {"manifest.json": &fstest.MapFile{Data: []byte(`{"schemaVersion":7,"compatibility":""}`)}},
	} {
		if _, err := NewEmbedSource(fsys); !errors.Is(err, ErrManifestSchema) {
			t.Errorf("%s: NewEmbedSource = %v, want ErrManifestSchema", name, err)
		}
	}
}

// TestDefaultSourceServesEmbeddedManifest pins that the shipped asset set is
// present, valid, and resolves the example view with a non-empty,
// self-contained bundle.
func TestDefaultSourceServesEmbeddedManifest(t *testing.T) {
	src, err := DefaultSource()
	if err != nil {
		t.Fatalf("DefaultSource() error: %v", err)
	}
	m, err := src.Manifest(context.Background())
	if err != nil {
		t.Fatalf("Manifest() error: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("embedded manifest invalid: %v", err)
	}

	src2, _ := DefaultSource()
	data, err := OpenView(context.Background(), src2, "example")
	if err != nil {
		t.Fatalf("OpenView(example) error: %v", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		t.Fatal("embedded example bundle is empty")
	}
	if bare := BareModuleSpecifiers(string(data)); len(bare) > 0 {
		t.Errorf("embedded example bundle is not inline-module-ready (bare imports the browser cannot resolve: %v)", bare)
	}
}

// fakeSource is a hand-rolled AssetSource (no generated mocks are needed) so
// the OpenView/ModuleJS error paths are driven directly.
type fakeSource struct {
	manifest Manifest
	content  map[string]string // view -> bundle source
}

func (f *fakeSource) Manifest(context.Context) (Manifest, error) {
	return f.manifest, nil
}

// sha256Hex is a test helper that hashes a string the way the asset layer
// verifies bundle contents.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (f *fakeSource) Open(_ context.Context, view string) (io.ReadSeeker, error) {
	src, ok := f.content[view]
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	return strings.NewReader(src), nil
}

func fakeManifest(view, hash string) Manifest {
	return Manifest{
		SchemaVersion: ManifestSchemaVersion,
		Compatibility: "test",
		Bundles:       []Bundle{{View: view, File: view + ".js", Hash: hash}},
	}
}

// TestOpenViewUnknownView pins the unknown-view error path end-to-end through
// OpenView and ModuleJS, plus Open on the embed source.
func TestOpenViewUnknownView(t *testing.T) {
	src, err := DefaultSource()
	if err != nil {
		t.Fatalf("DefaultSource() error: %v", err)
	}
	ctx := context.Background()
	if _, err := src.Open(ctx, "no-such-view"); !errors.Is(err, ErrMissingBundle) {
		t.Errorf("Open(unknown) = %v, want ErrMissingBundle", err)
	}
	fake := &fakeSource{manifest: fakeManifest("v", strings.Repeat("0", 64)), content: map[string]string{"v": "x"}}
	if _, err := OpenView(ctx, fake, "no-such-view"); !errors.Is(err, ErrMissingBundle) {
		t.Errorf("OpenView(unknown) = %v, want ErrMissingBundle", err)
	}
	if _, err := ModuleJS(ctx, fake, "no-such-view", "v1.0.0"); !errors.Is(err, ErrMissingBundle) {
		t.Errorf("ModuleJS(unknown) = %v, want ErrMissingBundle", err)
	}
}

// TestOpenViewHashMismatch pins that tampered bundle content is refused with
// ErrHashMismatch.
func TestOpenViewHashMismatch(t *testing.T) {
	src := &fakeSource{
		manifest: fakeManifest("v", strings.Repeat("0", 64)),
		content:  map[string]string{"v": "actual content"},
	}
	if _, err := OpenView(context.Background(), src, "v"); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("OpenView with tampered content = %v, want ErrHashMismatch", err)
	}
}

// TestModuleJSInjectsVersionGlobal proves ModuleJS (the wrapper the render
// functions actually use) prefixes the verified bundle with the version
// handshake global, so views inherit the host version instead of carrying a
// hardcoded per-view version during the ui/initialize handshake.
func TestModuleJSInjectsVersionGlobal(t *testing.T) {
	content := `import "./local.js"; export const x = 1;`
	src := &fakeSource{
		manifest: fakeManifest("v", sha256Hex(content)),
		content:  map[string]string{"v": content},
	}
	module, err := ModuleJS(context.Background(), src, "v", "v1.2.3")
	if err != nil {
		t.Fatalf("ModuleJS() error: %v", err)
	}
	const prefix = "window.__MCPCANVAS_VERSION__ = "
	if !strings.HasPrefix(module, prefix+`"1.2.3";`) {
		t.Fatalf("ModuleJS did not inject the version global: %q", module)
	}
	if !strings.HasSuffix(module, content) {
		t.Errorf("ModuleJS did not inline the bundle content")
	}
	if strings.Contains(module, "\nimport ") {
		t.Errorf("ModuleJS output is not self-contained")
	}
}

// TestModuleJSEndToEndDoc proves the embedded example bundle flows through
// the shared document shell with the handshake global present (the seam
// render functions rely on).
func TestModuleJSEndToEndDoc(t *testing.T) {
	src, err := DefaultSource()
	if err != nil {
		t.Fatalf("DefaultSource() error: %v", err)
	}
	module, err := ModuleJS(context.Background(), src, "example", "v1.2.3")
	if err != nil {
		t.Fatalf("ModuleJS(example) error: %v", err)
	}
	doc, err := RenderAppDoc("Example", AppThemeCSS, &fakeBody{markup: `<div id="app-root"></div>`}, module)
	if err != nil {
		t.Fatalf("RenderAppDoc error: %v", err)
	}
	for _, want := range []string{"<!doctype html>", "<script type=\"module\">", "<div id=\"app-root\"></div>", "window.__MCPCANVAS_VERSION__"} {
		if !strings.Contains(doc, want) {
			t.Errorf("rendered doc missing %q", want)
		}
	}
}

// TestBareModuleSpecifiers pins the self-containment guard itself: it must
// flag bare package specifiers (including the minified no-space forms), while
// ignoring relative/absolute paths and resolvable URLs.
func TestBareModuleSpecifiers(t *testing.T) {
	good := []string{
		`const a = 1;`,
		`import "./local.js";`,
		`import x from "/abs/mod.js";`,
		`import x from "https://cdn.example/lib.js";`,
	}
	bad := []string{
		`import e from"@uppy/core";`,
		`import t from "@uppy/xhr-upload";`,
		`import "zod";`,
		`import { x } from "@modelcontextprotocol/sdk/client.js";`,
		`import("@uppy/core");`,
		`import ("@uppy/xhr-upload");`,
	}
	for _, s := range good {
		if got := BareModuleSpecifiers(s); len(got) != 0 {
			t.Errorf("BareModuleSpecifiers(%q) = %v, want []", s, got)
		}
	}
	for _, s := range bad {
		if got := BareModuleSpecifiers(s); len(got) == 0 {
			t.Errorf("BareModuleSpecifiers(%q) = [] , want a flagged specifier", s)
		}
	}
}

// TestEveryEmbeddedBundleSelfContained pins that EVERY embedded bundle is
// inline-ready with zero bare module imports. A missing/empty bundled file or
// a residual bare import (e.g. a dependency left external by a bundler
// config) would crash every view that ships it in a browser host.
func TestEveryEmbeddedBundleSelfContained(t *testing.T) {
	src, err := DefaultSource()
	if err != nil {
		t.Fatalf("DefaultSource() error: %v", err)
	}
	m, err := src.Manifest(context.Background())
	if err != nil {
		t.Fatalf("Manifest() error: %v", err)
	}
	views := make([]string, 0, len(m.Bundles))
	for _, b := range m.Bundles {
		views = append(views, b.View)
	}
	sort.Strings(views)
	for _, view := range views {
		data, err := OpenView(context.Background(), src, view)
		if err != nil {
			t.Fatalf("bundle %q failed to open with hash verification: %v", view, err)
		}
		if strings.TrimSpace(string(data)) == "" {
			t.Fatalf("bundle %q is empty", view)
		}
		if bare := BareModuleSpecifiers(string(data)); len(bare) > 0 {
			t.Errorf("bundle %q is not inline-module-ready (bare imports the browser cannot resolve: %v)", view, bare)
		}
	}
}
