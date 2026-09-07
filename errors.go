package mcpcanvas

import "errors"

// ErrManifestSchema is returned when a Manifest does not conform to the
// expected structure: an unsupported schema version, missing required fields,
// malformed hashes, or duplicate view names.
var ErrManifestSchema = errors.New("mcpcanvas: invalid asset manifest")

// ErrHashMismatch is returned when a bundle's content does not match the
// sha256 hash recorded for it in the manifest.
var ErrHashMismatch = errors.New("mcpcanvas: bundle hash mismatch")

// ErrMissingBundle is returned when a view has no bundle registered in the
// manifest.
var ErrMissingBundle = errors.New("mcpcanvas: bundle not found for view")
