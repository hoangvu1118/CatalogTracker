// Command catalog runs the staff application: it starts catalog updates
// from the supplier and serves the staff page and JSON API.
//
// This is the composition root: the only place that knows about concrete
// implementations (PostgreSQL, HTTP supplier client) and wires them together.
package main

import (
	"log/slog"
	"os"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	logger.Info("catalog application starting (skeleton)")
}
