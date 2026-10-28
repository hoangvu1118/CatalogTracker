// Command fakesupplier runs a fictional supplier ("Acme Wholesale") that
// serves catalog changes in batches. It can misbehave on purpose (duplicates,
// out-of-order changes, outages) to demonstrate the rules in docs/01-problem.md.
package main

import (
	"log/slog"
	"os"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	logger.Info("fake supplier starting (skeleton)")
}
