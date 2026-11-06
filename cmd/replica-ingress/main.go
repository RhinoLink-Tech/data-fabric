package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/replicarouting"
)

type ingressConfig struct {
	DeploymentID string `json:"deployment_id"`
	replicarouting.Upstreams
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8082", "private listener behind HTTPS termination")
	config := flag.String("config", "", "path to the ingress JSON configuration")
	flag.Parse()
	if err := run(*listen, *config); err != nil {
		log.Fatal(err)
	}
}

func run(address, filename string) error {
	cfg, err := loadConfig(filename)
	if err != nil {
		return err
	}
	ring, err := replicarouting.ParseKeyRing(cfg.DeploymentID, os.Getenv("DATA_FABRIC_ROUTING_KEYS"))
	if err != nil {
		return err
	}
	ingress, err := replicarouting.NewIngress(ring, cfg.Upstreams)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{
		Handler:           ingress,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	reload := make(chan os.Signal, 1)
	signal.Notify(reload, syscall.SIGHUP)
	defer signal.Stop(reload)
	errs := make(chan error, 1)
	go func() { errs <- server.Serve(listener) }()
	log.Printf("Replica ingress listening on %s", listener.Addr())

	for {
		select {
		case err := <-errs:
			return err
		case <-reload:
			reloadConfig(ingress, filename, cfg.DeploymentID)
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdown); err != nil {
				_ = server.Close()
				return err
			}
			return nil
		}
	}
}

func reloadConfig(ingress *replicarouting.Ingress, filename, deployment string) {
	cfg, err := loadConfig(filename)
	if err == nil && cfg.DeploymentID != deployment {
		err = errors.New("cannot change routing deployment ID on reload")
	}
	if err == nil {
		err = ingress.Reload(cfg.Upstreams)
	}
	if err != nil {
		log.Printf("Routing reload rejected: %v", err)
		return
	}
	log.Print("Reloaded routing upstreams")
}

func loadConfig(filename string) (ingressConfig, error) {
	var cfg ingressConfig
	file, err := os.Open(filename)
	if err != nil {
		return cfg, err
	}
	defer file.Close()
	const maxConfig = 64 << 10
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxConfig {
		return cfg, errors.New("ingress config must be a regular JSON file no larger than 64 KiB")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxConfig+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, errors.New("invalid ingress configuration")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return cfg, errors.New("ingress config must contain one JSON object")
	}
	return cfg, nil
}
