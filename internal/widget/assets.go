package widget

import _ "embed"

// DeparturesWASM is checked in so normal server builds do not require TinyGo.
// Artifact revision: Rust Transitous v6 Chrono-free ASCII wall times (4).
//
//go:embed departures.wasm
var DeparturesWASM []byte
