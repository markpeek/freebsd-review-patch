// Package reviewskill supplies the shared review instructions to the binary.
package reviewskill

import _ "embed"

// Guidance is also read directly by the installed skill.
//
//go:embed references/review-guidance.md
var Guidance string
