package main

import (
	"context"
	"errors"
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
	if len(args) > 0 && (args[0] == "serve" || args[0] == replacementCommand) {
		var err error
		if args[0] == "serve" {
			err = serve(args[1:], os.Stdout, os.Stderr)
		} else {
			err = servePreparedReplacement(args[1:])
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(runCLI(args, os.Stdout, os.Stderr))
}

func parseServeArguments(args []string) (root string, port int, background bool, err error) {
	if len(args) > 0 && args[0] == "--background" {
		background = true
		args = args[1:]
	}
	if len(args) < 1 || len(args) > 2 || !filepath.IsAbs(args[0]) {
		return "", 0, false, errors.New("serve requires an absolute DATA_DIRECTORY and an optional PORT from 0 to 65535")
	}
	root = args[0]
	if len(args) == 2 {
		value := args[1]
		if value == "" {
			return "", 0, false, errors.New("serve PORT must be an integer from 0 to 65535")
		}
		for _, digit := range value {
			if digit < '0' || digit > '9' {
				return "", 0, false, errors.New("serve PORT must be an integer from 0 to 65535")
			}
		}
		port, err = strconv.Atoi(value)
		if err != nil || port > 65535 {
			return "", 0, false, errors.New("serve PORT must be an integer from 0 to 65535")
		}
	}
	if background && port == 0 {
		return "", 0, false, errors.New("serve --background requires an explicit nonzero positional PORT")
	}
	return root, port, background, nil
}

func serve(args []string, stdout, stderr io.Writer) (result error) {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(stderr, "Usage: localswim serve [--background] DATA_DIRECTORY [PORT]")
		fmt.Fprintln(stderr, "DATA_DIRECTORY must be absolute. PORT defaults to 0 (an available IPv4 loopback port).")
		fmt.Fprintln(stderr, "--background starts independently without a console on Windows and requires an explicit nonzero PORT.")
		return nil
	}
	root, port, background, err := parseServeArguments(args)
	if err != nil {
		return err
	}
	if background {
		return launchBackground(root, port, stdout)
	}
	unlock, err := v2.LockServiceStartup(root)
	if err != nil {
		return err
	}
	app, err := openService(root)
	if err != nil {
		return errors.Join(err, unlock())
	}
	defer func() { result = errors.Join(result, app.Close()) }()
	listener, err := listenLoopback(port)
	if err != nil {
		return errors.Join(err, unlock())
	}
	defer listener.Close()
	if err := unlock(); err != nil {
		return err
	}
	cleanupRunImages(app.DataRoot())
	fmt.Fprintln(stdout, "http://"+listener.Addr().String())
	return runService(app, listener, app.DataRoot())
}

func openService(root string) (*v2.Service, error) {
	return v2.Open(root, os.Getenv("LOCALSWIM_HUMAN_TOKEN"), os.Getenv("LOCALSWIM_BOT_TOKEN"))
}

func listenLoopback(port int) (*net.TCPListener, error) {
	address := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	listener, err := net.ListenTCP("tcp4", address)
	if err != nil {
		return nil, errors.New("cannot bind the requested loopback listener")
	}
	return listener, nil
}

func runService(app *v2.Service, listener *net.TCPListener, root string) error {
	actualPort := listener.Addr().(*net.TCPAddr).Port
	exits := app.ExitRequests(func(image []byte, id string) (v2.PreparedActivation, error) {
		return prepareNativeReplacement(root, actualPort, image, id)
	})
	stop, release := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer release()
	for {
		exit, err := serveHTTP(app, listener, stop.Done(), exits)
		if err != nil || exit.Replacement == nil {
			if exit.Replacement != nil {
				err = errors.Join(err, exit.Replacement.Abort())
			}
			return err
		}
		listener, err = handoffService(app, root, actualPort, exit)
		if err != nil || listener == nil {
			if listener != nil {
				err = errors.Join(err, listener.Close())
			}
			return err
		}
	}
}

func serveHTTP(app *v2.Service, listener *net.TCPListener, stop <-chan struct{}, exits <-chan v2.ServiceExit) (v2.ServiceExit, error) {
	server := &http.Server{
		Handler: app.Handler(listener.Addr().(*net.TCPAddr)), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second,
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()

	var exit v2.ServiceExit
	var serveErr error
	var joined, explicit bool
	select {
	case <-stop:
	case exit = <-exits:
		explicit = true
	case serveErr = <-finished:
		joined = true
	}
	if explicit {
		app.BeginEventDrain()
	} else {
		app.BeginEventShutdown()
	}
	shutdownErr := server.Shutdown(context.Background())
	if !joined {
		serveErr = <-finished
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	// A native stop and an already admitted lifecycle request can race.
	// Join the HTTP handler and retrieve any committed replacement exactly once.
	if exit.Replacement == nil {
		select {
		case exit = <-exits:
		default:
		}
	}
	return exit, errors.Join(serveErr, shutdownErr)
}
