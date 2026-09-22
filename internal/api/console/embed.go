package console

import "embed"

// Dist holds the built ProviderApi Console (Vite output in dist/).
//
//go:embed all:dist
var Dist embed.FS
