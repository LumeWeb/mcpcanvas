package mcpcanvas

import (
	"context"
	"fmt"
	"io"
	"regexp"
)

// ManifestSchemaVersion is the only schema version recognized by
// Manifest.Validate. Bump it (and Validate) when the manifest shape evolves
// incompatibly.
const ManifestSchemaVersion = 1

// Manifest describes the set of bundled, self-contained ESM app views an
// AssetSource can serve. It is typically deserialized from a manifest.json
// shipped alongside the bundles.
type Manifest struct {
	// SchemaVersion is the manifest schema version. Validate only accepts
	// ManifestSchemaVersion.
	SchemaVersion int `json:"schemaVersion"`
	// Compatibility is a free-form marker describing which host/runtime
	// generation the bundles were built against, so a host can refuse a
	// manifest it does not understand before loading anything.
	Compatibility string `json:"compatibility"`
	// Bundles is the view table: one entry per servable view.
	Bundles []Bundle `json:"bundles"`
}

// Bundle registers one view: where its bytes live and how to verify them.
type Bundle struct {
	// View is the caller-facing name the view is resolved by (e.g. the
	// MCP ui:// resource name). Unique within a manifest.
	View string `json:"view"`
	// File is the bundle path, interpreted relative to the asset source
	// root (for EmbedSource: the embedded FS root the source was
	// constructed with).
	File string `json:"file"`
	// Hash is the lowercase hex sha256 of the bundle content, verified
	// when the bundle bytes are read.
	Hash string `json:"hash"`
}

// bundleHashRe matches a lowercase hex sha256 digest.
var bundleHashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Validate checks the manifest structure. It returns an error wrapping
// ErrManifestSchema if the schema version is unsupported, any required field
// is missing, a hash is not a 64-char lowercase hex digest, or a view name is
// duplicated. A manifest with zero bundles is structurally valid (it simply
// serves nothing).
func (m Manifest) Validate() error {
	if m.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("%w: unsupported schema version %d (want %d)",
			ErrManifestSchema, m.SchemaVersion, ManifestSchemaVersion)
	}
	if m.Compatibility == "" {
		return fmt.Errorf("%w: missing compatibility marker", ErrManifestSchema)
	}
	seen := make(map[string]bool, len(m.Bundles))
	for i, b := range m.Bundles {
		if b.View == "" {
			return fmt.Errorf("%w: bundle %d: empty view name", ErrManifestSchema, i)
		}
		if seen[b.View] {
			return fmt.Errorf("%w: duplicate view name %q", ErrManifestSchema, b.View)
		}
		seen[b.View] = true
		if b.File == "" {
			return fmt.Errorf("%w: bundle %q: empty file path", ErrManifestSchema, b.View)
		}
		if !bundleHashRe.MatchString(b.Hash) {
			return fmt.Errorf("%w: bundle %q: hash %q is not a 64-char lowercase hex sha256",
				ErrManifestSchema, b.View, b.Hash)
		}
	}
	return nil
}

// Lookup returns the Bundle registered for view, or an error wrapping
// ErrMissingBundle if the manifest does not list it.
func (m Manifest) Lookup(view string) (Bundle, error) {
	for _, b := range m.Bundles {
		if b.View == view {
			return b, nil
		}
	}
	return Bundle{}, fmt.Errorf("%w: %q", ErrMissingBundle, view)
}

// AssetSource resolves an asset manifest to bundle bytes. Open returns a
// ReadSeeker over the content registered for view in Manifest; an unknown
// view is an error wrapping ErrMissingBundle, and implementations should keep
// the returned Seeker positioned at the start of the content.
type AssetSource interface {
	Manifest(context.Context) (Manifest, error)
	Open(context.Context, string) (io.ReadSeeker, error)
}
