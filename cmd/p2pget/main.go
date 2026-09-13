// Command p2pget is a BitTorrent v1 client: point it at a .torrent file and it downloads (and optionally seeds)
// the file it describes. This file is deliberately thin - flags in, an Options struct, one call into
// internal/download - with no logic of its own worth unit-testing.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/asmanya/p2p-file-distribution/internal/download"
	"github.com/asmanya/p2p-file-distribution/internal/metainfo"
	"github.com/asmanya/p2p-file-distribution/internal/tracker"
)

const version = "1.0.1"

func main() {
	torrentPath := flag.String("torrent", "", "path to the .torrent file (required)")
	outDir := flag.String("out", ".", "output directory")
	maxPeers := flag.Int("max-peers", 50, "maximum concurrent peer connections")
	port := flag.Int("port", 6881, "listening port for incoming connections")
	seed := flag.Bool("seed", false, "keep seeding after the download completes")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("p2pget version " + version)
		return
	}

	if *torrentPath == "" {
		fmt.Fprintln(os.Stderr, "p2pget: -torrent is required")
		flag.Usage()
		os.Exit(2)
	}

	level, err := parseLogLevel(*logLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, "p2pget:", err)
		os.Exit(2)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	err = run(*torrentPath, *outDir, *port, *maxPeers, *seed)
	switch {
	case err == nil:
		return
	case errors.Is(err, context.Canceled):
		// The user asked for this via Ctrl+C (withShutdown cancels the context it hands to Download) - a clean
		// shutdown they requested, not a failure, so it gets a plain message and a success exit code, not the
		// "fatal" treatment below.
		slog.Info("p2pget: stopped")
	default:
		slog.Error("p2pget: fatal", "error", err)
		os.Exit(1)
	}
}

func run(torrentPath, outDir string, port, maxPeers int, seed bool) error {
	tor, err := metainfo.ParseFile(torrentPath)
	if err != nil {
		return fmt.Errorf("parse torrent: %w", err)
	}

	peerID, err := tracker.GeneratePeerID()
	if err != nil {
		return fmt.Errorf("generate peer id: %w", err)
	}

	outputPath := outDir + "/" + tor.Name

	tc := tracker.NewClient()
	opts := download.Options{Port: port, MaxPeers: maxPeers, Seed: seed}
	return download.Download(withShutdown(), tor, tc, peerID, outputPath, opts)
}

// withShutdown returns a context that's cancelled on the first SIGINT/SIGTERM, giving Download a chance to shut
// down cleanly (stop the tracker, sync the file, close every connection) instead of the process just dying
// mid-write. A second signal forces an immediate exit - if the graceful shutdown is taking a few seconds, a user
// pressing Ctrl+C again shouldn't be ignored.
func withShutdown() context.Context {
	ctx, cancel := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Warn("p2pget: shutting down (press Ctrl+C again to force exit)")
		cancel()
		<-sigCh
		slog.Error("p2pget: forced exit")
		os.Exit(1)
	}()

	return ctx
}

func parseLogLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid -log-level %q (want debug, info, warn, or error)", s)
	}
}
