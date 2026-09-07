package mcpcanvas

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
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

// closeTrackingFS wraps the embedded asset root so every Open returns a
// genuinely seekable file that counts its Close calls atomically, letting the
// close-contract test observe OpenView's cleanup without stubbing out the
// real EmbedSource path.
type closeTrackingFS struct {
	inner  fs.FS
	closes *int32
}

func (f closeTrackingFS) Open(name string) (fs.File, error) {
	file, err := f.inner.Open(name)
	if err != nil {
		return nil, err
	}
	data, rerr := io.ReadAll(file)
	if rerr != nil {
		_ = file.Close()
		return nil, rerr
	}
	return &closeTrackingFile{File: file, Reader: bytes.NewReader(data), closes: f.closes}, nil
}

type closeTrackingFile struct {
	fs.File
	*bytes.Reader
	closes *int32
}

func (f *closeTrackingFile) Read(p []byte) (int, error) { return f.Reader.Read(p) }

func (f *closeTrackingFile) Close() error {
	atomic.AddInt32(f.closes, 1)
	return f.File.Close()
}

// TestOpenViewClosesOpenedBundleFile pins the fd-leak fix: when the asset
// source's underlying FS yields closable files, OpenView must close the
// bundle handle exactly once on the success path, must never open (hence
// close) a handle for an unknown view, and must still return the verified
// bundle content after its deferred close fires. It drives the real
// EmbedSource over the embedded asset set and observes the observable close
// side effect rather than stubbing the source out.
func TestOpenViewClosesOpenedBundleFile(t *testing.T) {
	root, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		t.Fatalf("fs.Sub(assets) error: %v", err)
	}
	var closes int32
	src, err := NewEmbedSource(closeTrackingFS{inner: root, closes: &closes})
	if err != nil {
		t.Fatalf("NewEmbedSource() error: %v", err)
	}
	// Building the source read+closed manifest.json through the tracking FS.
	atomic.StoreInt32(&closes, 0)
	ctx := context.Background()

	got, err := OpenView(ctx, src, "example")
	if err != nil {
		t.Fatalf("OpenView(example) error: %v", err)
	}
	if n := atomic.LoadInt32(&closes); n != 1 {
		t.Errorf("close count after successful OpenView = %d, want 1", n)
	}
	if strings.TrimSpace(string(got)) == "" {
		t.Fatal("OpenView returned empty content")
	}

	// The success path must be unchanged: the same bytes DefaultSource serves.
	want, err := OpenView(ctx, mustDefaultSource(t), "example")
	if err != nil {
		t.Fatalf("DefaultSource OpenView(example) error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("OpenView over tracking FS returned different bytes than DefaultSource")
	}

	// An unknown view never reaches src.Open, so no handle is opened or closed.
	if _, err := OpenView(ctx, src, "no-such-view"); !errors.Is(err, ErrMissingBundle) {
		t.Errorf("OpenView(unknown) = %v, want ErrMissingBundle", err)
	}
	if n := atomic.LoadInt32(&closes); n != 1 {
		t.Errorf("close count after unknown-view OpenView = %d, want still 1", n)
	}

	// Read-before-close correctness: the tracking FS re-opens and re-parses
	// the manifest below, so verify the manifest is still serviceable after
	// the closed handles — a fresh OpenView must succeed.
	if _, err := OpenView(ctx, src, "example"); err != nil {
		t.Fatalf("OpenView(example) after prior closes error: %v", err)
	}
}

func mustDefaultSource(t *testing.T) *EmbedSource {
	t.Helper()
	src, err := DefaultSource()
	if err != nil {
		t.Fatalf("DefaultSource() error: %v", err)
	}
	return src
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

// --- Close-once regression coverage on EmbedSource.Open error paths ---
//
// These tests extend the close-tracking approach of
// TestOpenViewClosesOpenedBundleFile to the error paths assets.go is
// careful about: a bundle file that is not an io.Seeker and a bundle file
// whose Seek fails must both be closed exactly once by EmbedSource.Open
// itself (OpenView never gets a handle to close, because Open returns nil
// there). The fakes below yield genuinely closable files whose Close calls
// are counted atomically; nothing is stubbed out with non-closable readers.

// fakeFileStat is a minimal fs.FileInfo for the counting files below. It
// only needs to make the files valid fs.File values; nothing under test
// interprets the metadata.
type fakeFileStat struct {
	name string
	size int64
}

func (s fakeFileStat) Name() string    { return s.name }
func (s fakeFileStat) Size() int64     { return s.size }
func (s fakeFileStat) Mode() fs.FileMode { return 0o444 }
func (s fakeFileStat) ModTime() time.Time { return time.Time{} }
func (s fakeFileStat) IsDir() bool     { return false }
func (s fakeFileStat) Sys() any        { return nil }

// countingFile satisfies fs.File with Read, Close, and Stat — and notably
// NOT io.Seeker — so embedding it pins the not-seekable branch of
// EmbedSource.Open. It is also enough for fs.ReadFile, which only needs
// Read (and Close, which it defers in this package paths via EmbedSource).
type countingFile struct {
	data   []byte
	off    int
	closes *int32
}

func (f *countingFile) Read(p []byte) (int, error) {
	if f.off >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.off:])
	f.off += n
	return n, nil
}

func (f *countingFile) Close() error {
	atomic.AddInt32(f.closes, 1)
	return nil
}

func (f *countingFile) Stat() (fs.FileInfo, error) {
	return fakeFileStat{name: "fake", size: int64(len(f.data))}, nil
}

// seekingCountingFile is a Read+Seek+Close file whose Seek can be forced to
// fail, pinning the seek-failure branch of EmbedSource.Open.
type seekingCountingFile struct {
	*bytes.Reader
	closes  *int32
	seekErr error
}

func (f *seekingCountingFile) Seek(offset int64, whence int) (int64, error) {
	if f.seekErr != nil {
		return 0, f.seekErr
	}
	return f.Reader.Seek(offset, whence)
}

func (f *seekingCountingFile) Close() error {
	atomic.AddInt32(f.closes, 1)
	return nil
}

func (f *seekingCountingFile) Stat() (fs.FileInfo, error) {
	return fakeFileStat{name: "fake", size: int64(f.Reader.Size())}, nil
}

// countingFS is a hand-rolled fs.FS serving manifest.json plus one bundle
// file (v.js), mirroring the layout an EmbedSource consumes. Both files are
// genuinely closable and share one atomic Close counter (including the
// manifest handle NewEmbedSource opens at construction).
type countingFS struct {
	manifestJSON []byte
	bundle       []byte
	// noSeeker serves v.js as a countingFile (no Seek method at all).
	noSeeker bool
	// seekErr, when non-nil, is the error v.js's Seek returns. When both
	// noSeeker and seekErr are unset, v.js is fully seekable.
	seekErr error
	closes  *int32
}

func (f countingFS) Open(name string) (fs.File, error) {
	switch name {
	case ManifestFile:
		if f.manifestJSON == nil {
			return nil, fs.ErrNotExist
		}
		return &countingFile{data: f.manifestJSON, closes: f.closes}, nil
	case "v.js":
		if f.noSeeker {
			return &countingFile{data: f.bundle, closes: f.closes}, nil
		}
		return &seekingCountingFile{
			Reader:  bytes.NewReader(f.bundle),
			closes:  f.closes,
			seekErr: f.seekErr,
		}, nil
	default:
		return nil, fs.ErrNotExist
	}
}

// fakeFSManifest renders a valid manifest.json body for the counting fake FS:
// one bundle view "v" backed by file with the given hex sha256.
func fakeFSManifest(hash, file string) []byte {
	return []byte(fmt.Sprintf(
		`{"schemaVersion":%d,"compatibility":"test","bundles":[{"view":"v","file":%q,"hash":%q}]}`,
		ManifestSchemaVersion, file, hash))
}

// TestEmbedSourceClosesNonSeekableBundleFile pins the not-seekable error
// path: when the underlying FS yields a closable file without io.Seeker,
// EmbedSource.Open must close that handle exactly once itself (the returned
// reader is nil, so OpenView has nothing to close), must report the failure,
// and no half-built wrapper may leak — neither the reader nor the bundle
// bytes nor an unclosed handle.
func TestEmbedSourceClosesNonSeekableBundleFile(t *testing.T) {
	var closes int32
	fsys := countingFS{
		manifestJSON: fakeFSManifest(strings.Repeat("a", 64), "v.js"),
		bundle:       []byte("export const x = 42;"),
		noSeeker:     true,
		closes:       &closes,
	}
	src, err := NewEmbedSource(fsys)
	if err != nil {
		t.Fatalf("NewEmbedSource() error: %v", err)
	}
	ctx := context.Background()

	// Reset: NewEmbedSource opened (and fs.ReadFile closed) manifest.json
	// through the counting FS; only the Open calls below are counted.
	atomic.StoreInt32(&closes, 0)

	if got, err := src.Open(ctx, "v"); err == nil {
		t.Error("Open(v) succeeded over a non-seekable file, want error")
	} else if !strings.Contains(err.Error(), "not seekable") {
		t.Errorf("Open(v) error = %v, want it to mention not seekable", err)
	} else if got != nil {
		// No-escape pin: even on error nothing must leak for the caller to
		// (fail to) close.
		t.Errorf("Open(v) returned a non-nil reader %#v alongside the error — unclosed wrapper leaked", got)
	}
	if n := atomic.LoadInt32(&closes); n != 1 {
		t.Errorf("close count after failed Open = %d, want exactly 1", n)
	}

	// End-to-end through the OpenView wrapper, from a fresh handle.
	atomic.StoreInt32(&closes, 0)
	data, err := OpenView(ctx, src, "v")
	if err == nil {
		t.Error("OpenView(v) succeeded over a non-seekable file, want error")
	} else if !strings.Contains(err.Error(), "not seekable") {
		t.Errorf("OpenView(v) error = %v, want it to mention not seekable", err)
	} else if data != nil {
		t.Errorf("OpenView returned %d bytes alongside the error — content must not escape a failed open", len(data))
	}
	if n := atomic.LoadInt32(&closes); n != 1 {
		t.Errorf("close count after failed OpenView = %d, want exactly 1", n)
	}
}

// TestEmbedSourceClosesWhenSeekFails pins the seek-failure error path: when
// the bundle file implements io.Seeker but its Seek fails, EmbedSource.Open
// must close the handle exactly once and surface the seek error (wrapped, so
// callers can errors.Is it). The failing handle must not escape.
func TestEmbedSourceClosesWhenSeekFails(t *testing.T) {
	seekBroken := errors.New("seek: failpoint")
	var closes int32
	fsys := countingFS{
		manifestJSON: fakeFSManifest(strings.Repeat("a", 64), "v.js"),
		bundle:       []byte("export const x = 42;"),
		seekErr:      seekBroken,
		closes:       &closes,
	}
	src, err := NewEmbedSource(fsys)
	if err != nil {
		t.Fatalf("NewEmbedSource() error: %v", err)
	}
	ctx := context.Background()

	atomic.StoreInt32(&closes, 0) // ignore the construction-time manifest open

	if got, err := src.Open(ctx, "v"); !errors.Is(err, seekBroken) {
		t.Errorf("Open(v) error = %v, want it to wrap the seek failure %v", err, seekBroken)
	} else if got != nil {
		t.Errorf("Open(v) returned a non-nil reader %#v alongside the error — unclosed wrapper leaked", got)
	}
	if n := atomic.LoadInt32(&closes); n != 1 {
		t.Errorf("close count after failed Open = %d, want exactly 1", n)
	}

	atomic.StoreInt32(&closes, 0)
	data, err := OpenView(ctx, src, "v")
	if !errors.Is(err, seekBroken) {
		t.Errorf("OpenView(v) error = %v, want it to wrap the seek failure %v", err, seekBroken)
	} else if data != nil {
		t.Errorf("OpenView returned %d bytes alongside the error", len(data))
	}
	if n := atomic.LoadInt32(&closes); n != 1 {
		t.Errorf("close count after failed OpenView = %d, want exactly 1", n)
	}
}

// TestOpenViewClosesVerifiedBundleFileExactlyOnce extends the success-path
// close coverage to the hand-built-asset-set shape: OpenView over a fake FS
// with a hash-verifying manifest must return the verified bundle bytes while
// closing the opened handle exactly once — a future regression where the
// embed source (or the wrapper) also closed the file internally would push
// the count to 2 and fail here.
func TestOpenViewClosesVerifiedBundleFileExactlyOnce(t *testing.T) {
	const content = "console.log('close-once');"
	var closes int32
	fsys := countingFS{
		manifestJSON: fakeFSManifest(sha256Hex(content), "v.js"),
		bundle:       []byte(content),
		closes:       &closes,
	}
	src, err := NewEmbedSource(fsys)
	if err != nil {
		t.Fatalf("NewEmbedSource() error: %v", err)
	}
	ctx := context.Background()

	atomic.StoreInt32(&closes, 0) // ignore the construction-time manifest open

	got, err := OpenView(ctx, src, "v")
	if err != nil {
		t.Fatalf("OpenView(v) error: %v", err)
	}
	if string(got) != content {
		t.Errorf("OpenView(v) = %q, want the verified bundle content %q", got, content)
	}
	if n := atomic.LoadInt32(&closes); n != 1 {
		t.Errorf("close count after successful OpenView = %d, want exactly 1 (double-close regression?)", n)
	}
}
