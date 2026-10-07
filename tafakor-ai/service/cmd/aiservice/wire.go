package main

import (
	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
	"github.com/tafakorpardazan/tafakor-ai/internal/config"
	"github.com/tafakorpardazan/tafakor-ai/internal/httpapi"
	"github.com/tafakorpardazan/tafakor-ai/internal/provider"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
)

// wire attaches the feature layers (added phase by phase) to the HTTP server.
func wire(srv *httpapi.Server, cfg config.Config, st store.Repository, acc *accounting.Client, ai provider.AIProvider) {
}
