// SPDX-License-Identifier: MIT

package goscheme

import "path/filepath"

// dirOf is the directory part of a path, which becomes a library search
// directory for a script being loaded.
func dirOf(path string) string { return filepath.Dir(path) }
