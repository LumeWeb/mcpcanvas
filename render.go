package mcpcanvas

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"text/template"
)

// AppThemeCSS is the default shared visual theme for ui:// MCP Apps
// documents, compiled from a Tailwind input stylesheet at build time. It is
// the default value for the css parameter of RenderAppDoc and the single
// source of the app identity: the @theme tokens (dark surface, accent,
// status palette) plus the component classes the bodies and JS bundles
// reference. The output is tree-shaken to exactly the utilities used across
// views, so it stays small. Consumers that build their own product theme
// pass their own stylesheet instead; the library never assigns or reads
// AppThemeCSS implicitly.
//
// Every view is served as a single self-contained document to a sandboxed
// iframe, so the stylesheet is inlined (no network request, no runtime JIT);
// the compiler only pins the class surface at build time.
//
//go:embed css/tailwind.css
var AppThemeCSS string

// mcpAppDocTmpl is the text/template for the complete, self-contained ui://
// MCP App document shell. The body markup is rendered into a buffer by the
// BodyComponent (which writes via its own io.Writer), so the template only
// owns the static chrome (doctype, head, style, script wrapper). The
// {{.CSS}}, {{.Title}}, and {{.ModuleJS}} slots are filled at render time.
//
// text/template (not html/template) is used because the module script and CSS
// contain characters html/template would escape (> in JS, " in CSS), breaking
// the inline content. The values are developer-controlled (never user input),
// so XSS escaping is not needed.
const mcpAppDocTmpl = `<!doctype html><html lang="en"><head><meta charset="utf-8"/><meta name="viewport" content="width=device-width, initial-scale=1"/><title>{{.Title}}</title><style>{{.CSS}}</style></head><body>{{.Body}}<script type="module">{{.ModuleJS}}</script></body></html>`

// mcpAppDocData is the data model for mcpAppDocTmpl.
type mcpAppDocData struct {
	Title    string
	CSS      string
	Body     string
	ModuleJS string
}

// appDocTmpl is parsed once at init; the template is static so a parse failure
// is a programming error, not a runtime condition.
var appDocTmpl = template.Must(template.New("mcpapp").Parse(mcpAppDocTmpl))

// BodyComponent is the structural interface for the rendered <body> of an app
// document. It matches templ.Component exactly, so templ components satisfy
// it without this package importing templ: the library is stdlib-only and the
// body markup stays entirely the caller's concern.
type BodyComponent interface {
	Render(ctx context.Context, w io.Writer) error
}

// RenderAppDoc renders a complete, self-contained ui:// MCP App document.
// It is the single shared shell for every MCP App view: doctype, <head> with
// the inline theme stylesheet, the app's <body> (rendered by the
// BodyComponent), and the app's ESM <script> module.
//
// The css parameter is the stylesheet inlined into the <head>; it is explicit
// so no package-global state is read at render time. Callers wanting the
// default embedded theme pass the AppThemeCSS variable; consumers with their
// own product-built theme pass that instead.
//
// The body component owns the <body> markup; the head and module script are
// assembled by a text/template because a templ component treats
// <script>/<style> content as raw text and does not evaluate expressions
// inside them, and because the shell is identical across views. Served
// verbatim so the sandboxed iframe needs no network request.
//
// An error is returned if the body component fails to render or the template
// cannot execute; the module script content is inlined unescaped, so callers
// should only pass developer-controlled values, never user input.
func RenderAppDoc(title string, css string, body BodyComponent, moduleJS string) (string, error) {
	ctx := context.Background()

	// Render the body component into a buffer first; it writes via its own
	// io.Writer, so it must complete before the template fills the {{.Body}}
	// slot.
	var bodyBuf bytes.Buffer
	if body != nil {
		if err := body.Render(ctx, &bodyBuf); err != nil {
			return "", fmt.Errorf("mcpcanvas: render document body: %w", err)
		}
	}

	var b bytes.Buffer
	if err := appDocTmpl.Execute(&b, mcpAppDocData{
		Title:    title,
		CSS:      css,
		Body:     bodyBuf.String(),
		ModuleJS: moduleJS,
	}); err != nil {
		// appDocTmpl is parsed at init (template.Must); Execute on a
		// well-formed template with string fields cannot fail.
		return "", fmt.Errorf("mcpcanvas: render document template: %w", err)
	}
	return b.String(), nil
}
