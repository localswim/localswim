package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/localswim/localswim/internal/v2"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "serve" {
		if err := serve(args[1:], os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(runCLI(args, os.Stdout, os.Stderr))
}

func serve(args []string, stdout, stderr io.Writer) (result error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("data", "", "V2 data directory")
	port := fs.Int("port", 0, "IPv4 loopback port (0 selects an available port)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *root == "" || !filepath.IsAbs(*root) || *port < 0 || *port > 65535 || fs.NArg() != 0 {
		return errors.New("serve requires --data with an absolute directory and accepts --port from 0 to 65535")
	}
	app, err := v2.Open(*root, os.Getenv("LOCALSWIM_HUMAN_TOKEN"), os.Getenv("LOCALSWIM_BOT_TOKEN"))
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, app.Close()) }()
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)))
	if err != nil {
		return errors.New("cannot bind the requested loopback listener")
	}
	defer listener.Close()
	server := &http.Server{Handler: app.Handler(listener.Addr().String()), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
	stop, release := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer release()
	drained := make(chan error, 1)
	go func() {
		<-stop.Done()
		drained <- server.Shutdown(context.Background())
	}()
	fmt.Fprintln(stdout, "http://"+listener.Addr().String())
	err = server.Serve(listener)
	release()
	shutdownErr := <-drained
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return errors.Join(err, shutdownErr)
}
