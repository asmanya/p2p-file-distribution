package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/asmanya/p2p-file-distribution/internal/download"
	"github.com/asmanya/p2p-file-distribution/internal/metainfo"
	"github.com/asmanya/p2p-file-distribution/internal/tracker"
)

const version = "0.1.0"

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

	if err := run(*torrentPath, *outDir, *port, *maxPeers, *seed); err != nil {
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
	return download.Download(context.Background(), tor, tc, peerID, outputPath, opts)
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
