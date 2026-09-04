package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/sriramr98/vectorized/db"
	"github.com/sriramr98/vectorized/handlers"
	"github.com/sriramr98/vectorized/transport/tcpserver"
)

func main() {
	addr := flag.String("addr", ":6380", "TCP listen address")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		logger.Error("listen failed", "address", *addr, "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store := db.NewMemoryStore()
	server := tcpserver.New(listener, handlers.NewPublicTCPHandler(store), logger)

	logger.Info("server listening", "address", listener.Addr())
	if err := server.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server stopped unexpectedly", "error", err)
		os.Exit(1)
	}
	logger.Info("server stopped")
}
