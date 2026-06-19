package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tom/ai-dev/frontends/lanctl-go/internal/api"
	"github.com/tom/ai-dev/frontends/lanctl-go/internal/frontend"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8003"
	}

	deployDir := os.Getenv("DEPLOY_DIR")
	if deployDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			log.Fatalf("failed to get working directory: %v", err)
		}
		deployDir = wd
	}

	mux := http.NewServeMux()

	htmlPath := filepath.Join(deployDir, "frontend", "index.html")
	logPath := filepath.Join(deployDir, "frontend", "log.html")
	frontend.Attach(mux, htmlPath, logPath)

	api.RegisterRoutes(mux)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}

	done := make(chan struct{})
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-quit
		log.Printf("caught signal %s, shutting down…", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
		close(done)
	}()

	log.Printf("lanctl listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
	<-done
	fmt.Println("lanctl stopped")
}
