// Package examples embeds the example extensions so `nova docs` can print them
// without the source tree. Only text files are embedded; the built binaries are
// not.
package examples

import "embed"

//go:embed extensions/*/README.md extensions/*/extension.yaml extensions/*/*.go extensions/*/check.sh
var Files embed.FS
