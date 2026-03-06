package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/fsnotify/fsnotify"
	"github.com/leovanalphen/symfony-impl/internal/config"
	"github.com/leovanalphen/symfony-impl/internal/orchestrator"
	"github.com/leovanalphen/symfony-impl/internal/server"
	"github.com/leovanalphen/symfony-impl/internal/workflow"
)

func main() {
	workflowPath := flag.String("workflow", "./WORKFLOW.md", "path to WORKFLOW.md")
	port := flag.Int("port", 0, "HTTP server port (0 = disabled)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	loader := workflow.NewWorkflowLoader()
	wfDef, err := loader.Load(*workflowPath)
	if err != nil {
		slog.Error("failed to load workflow", "error", err)
		os.Exit(1)
	}
	slog.Info("loaded workflow", "prompt_length", len(wfDef.PromptTemplate))

	cfg := config.New(wfDef.Config)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	orch := orchestrator.New(cfg, wfDef)

	// file watcher
	absPath, aerr := filepath.Abs(*workflowPath)
	if aerr != nil {
		slog.Warn("could not resolve absolute workflow path, falling back to original", "path", *workflowPath, "error", aerr)
		absPath = *workflowPath
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Warn("could not create file watcher", "error", err)
	} else {
		if werr := watcher.Add(absPath); werr != nil {
			slog.Warn("could not watch workflow file", "path", absPath, "error", werr)
		}
		go func() {
			for {
				select {
				case event, ok := <-watcher.Events:
					if !ok {
						return
					}
					if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
						newDef, lerr := loader.Load(*workflowPath)
						if lerr != nil {
							slog.Warn("workflow reload failed, keeping last good config", "error", lerr)
						} else {
							orch.UpdateWorkflow(newDef)
							slog.Info("workflow reloaded")
						}
					}
				case werr, ok := <-watcher.Errors:
					if !ok {
						return
					}
					slog.Warn("watcher error", "error", werr)
				case <-ctx.Done():
					return
				}
			}
		}()
		defer watcher.Close()
	}

	if *port != 0 {
		srv := server.New(orch, *port)
		go func() {
			if serr := srv.Start(); serr != nil {
				slog.Error("server error", "error", serr)
			}
		}()
		slog.Info("HTTP server started", "port", *port)
	}

	go orch.Run(ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	slog.Info("shutting down")
	cancel()
}