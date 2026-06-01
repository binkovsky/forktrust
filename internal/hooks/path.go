package hooks

import (
	"github.com/binkovsky/forktrust/internal/pathsafe"
)

// Thin shims that route hooks' path-safety calls through the shared
// pathsafe package. Keeping the names secureJoin/withinRoot avoids touching
// every call site in hooks.go (which existed before v0.6.2's extract).
//
// Tests for the underlying behavior live in internal/pathsafe/.

func secureJoin(root, rel string) (string, error) { return pathsafe.SafeJoin(root, rel) }
func withinRoot(root, p string) bool              { return pathsafe.WithinRoot(root, p) }
