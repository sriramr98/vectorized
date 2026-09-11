package commands

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/sriramr98/vectorized/config"
	"github.com/sriramr98/vectorized/core"
	"github.com/sriramr98/vectorized/db"
	"github.com/sriramr98/vectorized/db/wal"
	"github.com/sriramr98/vectorized/handlers"
	"github.com/sriramr98/vectorized/transport/tcpserver"
)

var ServerCommand = &cobra.Command{
	Use:   "server",
	Short: "Start the database server",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runServer(cmd)
	},
}

var port int
var configPath string

func init() {
	ServerCommand.Flags().IntVar(&port, "port", 6380, "override the port the server runs on")
	ServerCommand.Flags().StringVar(&configPath, "config_path", "", "the database configuration path")
}

func runServer(cmd *cobra.Command) error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	serverConfig, err := config.LoadConfig(configPath, logger)
	if err != nil {
		return err
	}
	logger.Info("server configuration loaded")
	port, err := resolvePort(cmd, serverConfig)
	if err != nil {
		return err
	}
	logger.Info("server attempting to start", "port", port)
	listenAddress := ":" + strconv.Itoa(port)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store := db.NewMemoryStore()
	walStore, err := wal.NewWal(ctx, logger, serverConfig.WalDirPath)
	if err != nil {
		return err
	}
	defer walStore.Close()

	if err := core.ReplayWal(walStore, store); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		logger.Error("listen failed", "address", listenAddress, "error", err)
		return err
	}
	server := tcpserver.New(listener, handlers.NewPublicTCPHandler(store, walStore, logger), logger)
	logger.Info("server listening", "address", listener.Addr())
	if err := server.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server stopped unexpectedly", "error", err)
		return err
	}
	logger.Info("server stopped")
	return nil
}

// resolvePort returns a flag set port if available else the configured value in config.yaml or default
func resolvePort(cmd *cobra.Command, serverConfig config.ServerConfig) (int, error) {
	if cmd.Flags().Changed("port") {
		return cmd.Flags().GetInt("port")
	}
	return serverConfig.Port, nil
}
