package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"wallet-service/internal/config"
	"wallet-service/internal/handlers"
	"wallet-service/internal/routes"
	"wallet-service/internal/services"
	"wallet-service/internal/services/chains"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
)

func main() {
	if err := run(); err != nil {
		slog.Error("wallet-service", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	appLog, accessLog, closeLogs, err := openLogs(cfg.App.LogDir)
	if err != nil {
		return err
	}
	defer closeLogs()

	log := slog.New(slog.NewJSONHandler(appLog, &slog.HandlerOptions{Level: cfg.App.SlogLevel()}))
	slog.SetDefault(log)

	registry, err := chains.NewRegistry(cfg.Gates)
	if err != nil {
		return err
	}
	defer registry.Close()

	addresses := services.NewAddressService(registry, log)
	transactions := services.NewTransactionService(registry, log)

	app := fiber.New(fiber.Config{
		AppName:      "wallet-service",
		BodyLimit:    1 << 20,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
		ErrorHandler: routes.ErrorHandler(log),
	})
	app.Use(recover.New())
	app.Use(logger.New(logger.Config{Stream: accessLog, Format: logger.JSONFormat}))

	routes.Register(app, routes.Handlers{
		Addresses:    handlers.NewAddressHandler(addresses),
		Transactions: handlers.NewTransactionHandler(transactions),
	}, cfg.Server.AuthToken)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("wallet-service started",
		"addr", cfg.Server.Addr(),
		"gates", registry.Names(),
		"auth", cfg.Server.AuthToken != "",
	)

	err = app.Listen(cfg.Server.Addr(), fiber.ListenConfig{
		DisableStartupMessage: true,
		GracefulContext:       ctx,
		ShutdownTimeout:       10 * time.Second,
	})
	log.Info("wallet-service stopped")
	return err
}

func openLogs(dir string) (io.Writer, io.Writer, func(), error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, nil, nil, err
	}
	open := func(name string) (*os.File, error) {
		return os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	}

	app, err := open("wallet-service.log")
	if err != nil {
		return nil, nil, nil, err
	}
	access, err := open("access.log")
	if err != nil {
		app.Close()
		return nil, nil, nil, err
	}

	closeAll := func() {
		access.Close()
		app.Close()
	}
	return io.MultiWriter(os.Stdout, app), io.MultiWriter(os.Stdout, access), closeAll, nil
}
