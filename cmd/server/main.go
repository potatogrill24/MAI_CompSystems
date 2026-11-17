package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"mai/compsystems/internal/catalog"
	"mai/compsystems/internal/gateway"
	"mai/compsystems/internal/httpx"
	"mai/compsystems/internal/management"
	"mai/compsystems/internal/notifications"
	"mai/compsystems/internal/orders"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	service := httpx.Env("SERVICE", "management")
	var handler http.Handler
	var err error
	switch service {
	case "calculator":
		handler = management.Calculator()
	case "graphql":
		handler, err = gateway.Routes()
	case "management", "product", "inventory":
		var db *pgxpool.Pool
		db, err = pgxpool.New(ctx, httpx.Env("DATABASE_URL", "postgres://mai:mai@localhost:5432/mai?sslmode=disable"))
		if err != nil {
			return err
		}
		defer db.Close()
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err = db.Ping(c); err != nil {
			return err
		}
		switch service {
		case "management":
			handler, err = management.Routes(ctx, db)
		case "product":
			handler, err = catalog.Products(ctx, db)
		case "inventory":
			handler, err = catalog.Inventory(ctx, db)
		}
	case "order", "notification":
		var client *mongo.Client
		client, err = mongo.Connect(options.Client().ApplyURI(httpx.Env("MONGO_URL", "mongodb://localhost:27017")).SetServerSelectionTimeout(5 * time.Second))
		if err != nil {
			return err
		}
		defer func() {
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = client.Disconnect(c)
		}()
		if err = client.Ping(ctx, nil); err != nil {
			return err
		}
		db := client.Database(service)
		if service == "order" {
			handler, err = orders.Routes(ctx, db)
		} else {
			handler, err = notifications.Routes(ctx, db)
		}
	default:
		return errors.New("unknown SERVICE: " + service)
	}
	if err != nil {
		return err
	}
	defaultPorts := map[string]string{"management": ":8081", "calculator": ":8082", "product": ":8083", "inventory": ":8084", "order": ":8085", "notification": ":8086", "graphql": ":8080"}
	server := &http.Server{Addr: httpx.Env("HTTP_ADDR", defaultPorts[service]), Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 130 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
	done := make(chan error, 1)
	go func() {
		slog.Info("listening", "service", service, "address", server.Addr)
		done <- server.ListenAndServe()
	}()
	select {
	case err = <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return server.Shutdown(c)
	}
	return nil
}
