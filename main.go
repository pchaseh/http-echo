// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"golang.org/x/sys/unix"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hashicorp/http-echo/version"
)

var (
	listenFlag  = flag.String("listen", ":5678", "address and port to listen")
	textFlag    = flag.String("text", "", "text to put on the webpage")
	versionFlag = flag.Bool("version", false, "display version information")
	statusFlag  = flag.Int("status-code", 200, "http response code, e.g.: 200")
	transparentFlag = flag.Bool("transparent", false, "set the IP_TRANSPARENT option on the listening socket")

	// stdoutW and stderrW are for overriding in test.
	stdoutW = os.Stdout
	stderrW = os.Stderr
)

type listenerOpts struct {
	transparent bool
}

func createListener(addr string, opts listenerOpts) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var sockErr error

			if opts.transparent {
				err := c.Control(func(fd uintptr) {
					sockErr = unix.SetsockoptInt(int(fd), unix.SOL_IP, unix.IP_TRANSPARENT, 1)
				})
				if err != nil {
					return err
				}
			}
			return sockErr
		},
	}
	return lc.Listen(context.Background(), "tcp", addr)
}


func main() {
	flag.Parse()

	// Asking for the version?
	if *versionFlag {
		fmt.Fprintln(stdoutW, version.HumanVersion)
		os.Exit(0)
	}

	// Get text to echo from env var or flag
	echoText := os.Getenv("ECHO_TEXT")
	if *textFlag != "" {
		echoText = *textFlag
	}

	// Validation
	if echoText == "" {
		fmt.Fprintln(stderrW, "Missing -text option or ECHO_TEXT env var!")
		os.Exit(127)
	}

	args := flag.Args()
	if len(args) > 0 {
		fmt.Fprintln(stderrW, "Too many arguments!")
		os.Exit(127)
	}

	// Flag gets printed as a page
	mux := http.NewServeMux()
	mux.HandleFunc("/", httpLog(stdoutW, withAppHeaders(*statusFlag, httpEcho(echoText))))

	// Health endpoint
	mux.HandleFunc("/health", withAppHeaders(200, httpHealth()))

	server := &http.Server{
		Addr:    *listenFlag,
		Handler: mux,
	}
	serverCh := make(chan struct{})

	listenOpts := listenerOpts {
		transparent: *transparentFlag,
	}
	listener, err := createListener(*listenFlag, listenOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create listener: %v\n", err)
		os.Exit(1)
	}

	go func() {
		log.Printf("[INFO] server is listening on %s\n", *listenFlag)
		if err := server.Serve(listener); err != http.ErrServerClosed {
			log.Fatalf("[ERR] server exited with: %s", err)
		}
		close(serverCh)
	}()

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM)

	// Wait for interrupt
	<-signalCh

	log.Printf("[INFO] received interrupt, shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("[ERR] failed to shutdown server: %s", err)
	}

	// If we got this far, it was an interrupt, so don't exit cleanly
	os.Exit(2)
}

func httpEcho(v string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, v)
	}
}

func httpHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"ok"}`)
	}
}
