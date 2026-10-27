//go:build windows && amd64 && v02qualification

// Qualification-only control process. No product authority is supplied by it.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/localswim/localswim/internal/assets"
	"github.com/localswim/localswim/internal/contract"
	"github.com/localswim/localswim/internal/transport/local"
)

type controller struct {
	runtime  *local.Runtime
	delivery *delivery
	data     string
	port     int
}

func (c *controller) start() error {
	h, e := assets.Handler()
	if e != nil {
		return e
	}
	r, e := local.Start(context.Background(), local.Options{DataDir: c.data, Port: c.port, Assets: h})
	if e != nil {
		return e
	}
	c.runtime = r
	return nil
}
func (c *controller) close() error {
	c.delivery.release()
	if c.runtime == nil {
		return nil
	}
	e := c.runtime.Close()
	c.runtime = nil
	return e
}
func (c *controller) status() any {
	v, e := local.ReadClient(c.data)
	if e != nil || c.runtime == nil {
		return failure("STATUS_UNAVAILABLE")
	}
	return map[string]any{"ok": true, "ready": true, "origin": v.Origin, "installationId": v.InstallationID, "boardId": v.BoardID, "incarnation": v.Incarnation, "epoch": v.Epoch, "mutationCount": c.delivery.count.Load(), "delayReached": c.delivery.delayReached()}
}
func failure(code string) any { return map[string]any{"ok": false, "error": code} }
func parse(line []byte) (string, bool) {
	if _, e := contract.DecodeJSON(line); e != nil {
		return "", false
	}
	var q struct {
		Op string `json:"op"`
	}
	d := json.NewDecoder(bytes.NewReader(line))
	d.DisallowUnknownFields()
	if d.Decode(&q) != nil || d.Decode(new(any)) != io.EOF || q.Op == "" {
		return "", false
	}
	return q.Op, true
}
func emit(w io.Writer, v any) error {
	b, e := json.Marshal(v)
	if e != nil || len(b) > 2048 {
		return fmt.Errorf("output unavailable")
	}
	b = append(b, '\n')
	_, e = w.Write(b)
	return e
}
func (c *controller) serve(in io.Reader, out io.Writer) error {
	if e := emit(out, c.status()); e != nil {
		return e
	}
	s := bufio.NewScanner(in)
	s.Buffer(make([]byte, 1024), 1024)
	for s.Scan() {
		op, ok := parse(s.Bytes())
		var result any
		stop := false
		terminalFailure := false
		if !ok {
			result = failure("INVALID_REQUEST")
		} else {
			switch op {
			case "status":
				result = c.status()
			case "dropNext", "delayNext":
				if !c.delivery.arm(op) {
					result = failure("FAULT_ALREADY_ARMED")
				} else {
					result = map[string]any{"ok": true}
				}
			case "release":
				c.delivery.release()
				result = map[string]any{"ok": true}
			case "restart":
				if c.close() != nil || c.start() != nil {
					result = failure("RESTART_UNAVAILABLE")
					stop = true
					terminalFailure = true
				} else {
					result = c.status()
				}
			case "stop":
				if c.close() != nil {
					result = failure("CLOSE_UNAVAILABLE")
					terminalFailure = true
				} else {
					result = map[string]any{"ok": true}
				}
				stop = true
			default:
				result = failure("INVALID_REQUEST")
			}
		}
		if e := emit(out, result); e != nil {
			return e
		}
		if stop {
			if terminalFailure {
				return fmt.Errorf("controller unavailable")
			}
			return nil
		}
	}
	return s.Err()
}
func run() int {
	// Product net/http diagnostics must never escape the secret-free controller.
	log.SetOutput(io.Discard)
	f := flag.NewFlagSet("localfixture", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	data := f.String("data", "", "")
	port := f.Int("port", 0, "")
	if f.Parse(os.Args[1:]) != nil || f.NArg() != 0 || !filepath.IsAbs(*data) || filepath.Clean(*data) != *data || *port < 1024 || *port > 65535 {
		_ = emit(os.Stdout, failure("INVALID_ARGUMENTS"))
		return 2
	}
	// Initial invocation must never adopt a prior runtime directory.
	if _, e := os.Lstat(*data); !os.IsNotExist(e) {
		_ = emit(os.Stdout, failure("DATA_NOT_FRESH"))
		return 2
	}
	c := &controller{data: *data, port: *port, delivery: &delivery{}}
	// This unique sentinel is available only in the private handler overlay.
	local.V02QualificationDelivery(c.delivery.wrap)
	if c.start() != nil {
		_ = emit(os.Stdout, failure("START_UNAVAILABLE"))
		return 2
	}
	defer c.close()
	e := c.serve(os.Stdin, os.Stdout)
	ce := c.close()
	if e != nil || ce != nil {
		fmt.Fprintln(os.Stderr, "V02_CONTROL_UNAVAILABLE")
		return 2
	}
	return 0
}
func main() {
	defer func() {
		if recover() != nil {
			fmt.Fprintln(os.Stderr, "V02_CONTROL_UNAVAILABLE")
			os.Exit(2)
		}
	}()
	os.Exit(run())
}
