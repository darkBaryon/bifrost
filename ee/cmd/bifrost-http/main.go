// Package main 提供 EE 可执行入口，嵌入 UI 并调用 internal/app 完成应用装配。
//
// 合并上游后若上游 main.go 有变化, 以 diff 同步此文件.
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	eeApp "github.com/darkBaryon/bifrost/ee/internal/app"
	bifrost "github.com/maximhq/bifrost/core"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/maximhq/bifrost/transports/bifrost-http/profiling"
	bifrostServer "github.com/maximhq/bifrost/transports/bifrost-http/server"
	_ "go.uber.org/automaxprocs" // Automatically set GOMAXPROCS based on container cgroup limits
)

//go:embed all:ui
var uiContent embed.FS

var Version string

var logger = bifrost.NewDefaultLogger(schemas.LogLevelInfo)
var server *bifrostServer.BifrostHTTPServer

// init initializes command line flags (but does not parse them).
// Flag parsing is deferred to main() to avoid conflicts with test flags.
// It sets up the following flags:
//   - host: Host to bind the server to (default: localhost, can be overridden with BIFROST_HOST env var)
//   - port: Server port (default: 8080)
//   - app-dir: Application data directory (default: current directory)
//   - log-level: Logger level (debug, info, warn, error). Default is info.
//   - log-style: Logger output type (json or pretty). Default is JSON.

func init() {
	if Version == "" {
		Version = "v2.0.0-ee.0"
	}
	// Set default host from environment variable or use localhost
	defaultHost := os.Getenv("BIFROST_HOST")
	if defaultHost == "" {
		defaultHost = bifrostServer.DefaultHost
	}
	defaultLogLevel := strings.ToLower(os.Getenv("LOG_LEVEL"))
	if defaultLogLevel == "" {
		defaultLogLevel = bifrostServer.DefaultLogLevel
	}
	// Initializing server
	server = bifrostServer.NewBifrostHTTPServer(Version, uiContent)
	// Updating server properties from flags
	flag.StringVar(&server.Port, "port", bifrostServer.DefaultPort, "Port to run the server on")
	flag.StringVar(&server.Host, "host", defaultHost, "Host to bind the server to (default: localhost, override with BIFROST_HOST env var)")
	flag.StringVar(&server.AppDir, "app-dir", bifrostServer.DefaultAppDir, "Application data directory (contains config.json and logs)")
	flag.StringVar(&server.LogLevel, "log-level", defaultLogLevel, "Logger level (debug, info, warn, error). Default is info.")
	flag.StringVar(&server.LogOutputStyle, "log-style", bifrostServer.DefaultLogOutputStyle, "Logger output type (json or pretty). Default is JSON.")
}

// main is the entry point of the application.
func main() {
	if len(os.Args) > 1 && os.Args[1] == "identity" {
		if err := eeApp.RecoverAdmin(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	// Parse command line flags
	flag.Parse()

	// Printing version
	versionLine := fmt.Sprintf("║%s%s%s║", strings.Repeat(" ", (61-2-len(Version))/2), Version, strings.Repeat(" ", (61-2-len(Version)+1)/2))
	// Welcome to bifrost!
	fmt.Printf(`
╔═══════════════════════════════════════════════════════════╗
║                                                           ║
║   ██████╗ ██╗███████╗██████╗  ██████╗ ███████╗████████╗   ║
║   ██╔══██╗██║██╔════╝██╔══██╗██╔═══██╗██╔════╝╚══██╔══╝   ║
║   ██████╔╝██║█████╗  ██████╔╝██║   ██║███████╗   ██║      ║
║   ██╔══██╗██║██╔══╝  ██╔══██╗██║   ██║╚════██║   ██║      ║
║   ██████╔╝██║██║     ██║  ██║╚██████╔╝███████║   ██║      ║
║   ╚═════╝ ╚═╝╚═╝     ╚═╝  ╚═╝ ╚═════╝ ╚══════╝   ╚═╝      ║
║                                                           ║
║═══════════════════════════════════════════════════════════║
%s
║═══════════════════════════════════════════════════════════║
║                 The Fastest LLM Gateway                   ║
║═══════════════════════════════════════════════════════════║
║             https://github.com/maximhq/bifrost            ║
╚═══════════════════════════════════════════════════════════╝

`, versionLine)

	// Start profiling
	pprofServer := profiling.Start()

	// Configure logger from flags
	logger.SetOutputType(schemas.LoggerOutputType(server.LogOutputStyle))
	logger.SetLevel(schemas.LogLevel(server.LogLevel))
	// Setting up logger
	lib.SetLogger(logger)
	bifrostServer.SetLogger(logger)
	handlers.SetLogger(logger)

	ctx := context.Background()
	t := time.Now()
	err := eeApp.Bootstrap(ctx, server) // ee: 上游 Bootstrap → EE 业务装配
	if err != nil {
		logger.Error("failed to bootstrap server: %v", err)
		os.Exit(1)
	}
	logger.Info("Time spent in Bifrost server bootstrap %d ms", time.Since(t).Milliseconds())
	err = server.Start()
	if err != nil {
		logger.Error("failed to start server: %v", err)
		os.Exit(1)
	}
	// server.Start() blocks until SIGINT/SIGTERM triggers graceful shutdown, so
	// by here the main server is draining/done. Shut the pprof server down too
	// to let any in-flight profile requests finish instead of being killed.
	if pprofServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := pprofServer.Shutdown(shutdownCtx); err != nil {
			logger.Warn("pprof server shutdown error: %v", err)
		}
	}
	logger.Info("🏁 server stopped")
}
