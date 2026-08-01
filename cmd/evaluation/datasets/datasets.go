package datasets

import _ "embed"

// Smoke is a builtin dataset in YAML format.
//
//go:embed smoke.yml
var Smoke []byte
