// Command fakesupplier runs a fictional supplier ("Acme Wholesale") that
// serves catalog changes in batches. It can misbehave on purpose (duplicates,
// out-of-order changes, outages) to demonstrate the rules in docs/01-problem.md.
package main

import (
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/supplier/fake"
)

func main() {
	var (
		addr     = flag.String("addr", ":9090", "listen address")
		feedPath = flag.String("feed", "testdata/feeds/acme-story.json", "feed file to serve")
		generate = flag.Bool("generate", false, "serve a generated feed instead of -feed")
		seed     = flag.Uint64("seed", 42, "seed for -generate and -fail-rate")
		products = flag.Int("products", 60, "products in a generated feed")
		batches  = flag.Int("batches", 40, "approximate batches in a generated feed")
		publish  = flag.Int("publish", 0, "batches visible at start (0 = all)")
		grow     = flag.Int("grow", 0, "publish this many more batches after each batch request")
		delay    = flag.Duration("delay", 0, "delay added to every feed request, e.g. 500ms")
		failRate = flag.Float64("fail-rate", 0, "chance (0..1) that a feed request answers 503")
		dump     = flag.Bool("dump", false, "print the feed as JSON and exit")
	)
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	var feed fake.Feed
	if *generate {
		feed = fake.Generate(fake.GenerateOptions{Seed: *seed, Products: *products, Batches: *batches})
	} else {
		var err error
		if feed, err = fake.LoadFile(*feedPath); err != nil {
			log.Error("cannot load feed", "error", err)
			os.Exit(1)
		}
	}

	if *dump {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(feed)
		return
	}

	srv := fake.NewServer(feed, fake.Options{
		Publish: *publish, Grow: *grow, Delay: *delay, FailRate: *failRate, Seed: *seed,
	})
	log.Info("fictional supplier listening", "addr", *addr, "batches", len(feed.Batches),
		"visible", srv.Latest(), "stats", feed.Stats)

	server := &http.Server{Addr: *addr, Handler: srv, ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
