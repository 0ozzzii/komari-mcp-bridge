package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/app"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/diagnostics"
	"github.com/0ozzzii/komari-mcp-bridge/bridge/internal/terminal"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var version = "development"
var commit = "unknown"

func value(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
func duration(name string, fallback time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		log.Fatalf("invalid %s", name)
	}
	return d
}
func integer(name string, fallback, low, high int) int {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < low || n > high {
		log.Fatalf("invalid %s", name)
	}
	return n
}
func main() { os.Exit(run()) }
func run() int {
	if len(os.Args) > 1 && os.Args[1] == "diagnostics" {
		flags := flag.NewFlagSet("diagnostics", flag.ContinueOnError)
		state := flags.String("state-dir", value("BRIDGE_STATE_DIR", "./bridge-data"), "Bridge state directory; never reads credential configuration")
		output := flags.String("output", "komari-diagnostics-"+time.Now().UTC().Format("20060102T150405Z")+".zip", "New metadata archive; existing files are never overwritten")
		if err := flags.Parse(os.Args[2:]); err != nil || flags.NArg() != 0 {
			return 2
		}
		if err := diagnostics.Export(*state, *output, version, commit); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("Diagnostic metadata saved: %s (no credentials or terminal output)\n", *output)
		return 0
	}
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("komari-mcp %s (%s)\n", version, commit)
		return 0
	}
	if len(os.Args) > 1 && os.Args[1] == "stdio" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := app.RunStdio(ctx, value("BRIDGE_MCP_URL", "http://127.0.0.1:8967/mcp"), os.Getenv("KOMARI_MCP_CALLER_KEY")); err != nil {
			log.Printf("stdio adapter: %v", err)
			return 1
		}
		return 0
	}
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: komari-mcp [serve|stdio|diagnostics|--version]; see bridge/README.md")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	control := value("BRIDGE_CONTROL_ADDRESS", "127.0.0.1:8968")
	host, _, err := net.SplitHostPort(control)
	if err != nil {
		log.Fatal("invalid BRIDGE_CONTROL_ADDRESS")
	}
	ip := net.ParseIP(host)
	if (ip == nil || !ip.IsLoopback()) && os.Getenv("BRIDGE_ALLOW_INTERNAL_CONTROL") != "1" {
		log.Fatal("control address must be loopback; use BRIDGE_ALLOW_INTERNAL_CONTROL=1 only for a private container network")
	}
	a, err := app.New(ctx, app.Config{BaseURL: os.Getenv("KOMARI_BASE_URL"), APIKey: os.Getenv("KOMARI_API_KEY"), ControlToken: os.Getenv("BRIDGE_CONTROL_TOKEN"), StateDir: value("BRIDGE_STATE_DIR", "./bridge-data"), Terminal: terminal.Options{ReadyTimeout: duration("CONNECTION_READY_TIMEOUT", 60*time.Second), IdleTimeout: duration("SESSION_IDLE_TIMEOUT", 30*time.Minute), Retention: duration("OUTPUT_RETENTION", 24*time.Hour), MaxSessions: integer("SESSION_LIMIT", 64, 1, 64), BufferBytes: integer("OUTPUT_BUFFER_LIMIT", 2<<20, 32768, 8<<20)}})
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()
	listen := value("BRIDGE_BIND_ADDRESS", "127.0.0.1:8967")
	servers := []*http.Server{{Addr: listen, Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}, {Addr: control, Handler: a.ControlHandler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}}
	failures := make(chan error, 2)
	for _, server := range servers {
		go func() {
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				failures <- err
			}
		}()
	}
	log.Printf("MCP %s; private control %s; %s", strings.TrimSpace(listen), control, a.Description())
	exitCode := 0
	select {
	case <-ctx.Done():
	case err := <-failures:
		log.Printf("listener failed: %v", err)
		exitCode = 1
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, server := range servers {
		server.Shutdown(shutdown)
	}
	return exitCode
}
