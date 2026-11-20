// Run through telltale trace run to observe changing CPU/RAM, HTTP events, and a child exiting.
package main

import (
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"
)

func main() {
	duration := flag.Duration("duration", 30*time.Second, "Duration of the local workload")
	child := flag.Bool("child", false, "Run the short-lived child workload")
	flag.Parse()
	if *child {
		time.Sleep(2 * time.Second)
		return
	}
	if err := run(*duration); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(duration time.Duration) (err error) {
	if duration <= 0 {
		return fmt.Errorf("duration must be positive")
	}
	proxy, err := url.Parse(os.Getenv("HTTP_PROXY"))
	if err != nil || proxy.Scheme == "" || proxy.Host == "" {
		return fmt.Errorf("run this workload through telltale trace run with HTTP proxy capture")
	}
	if proxy.Hostname() != "127.0.0.1" && proxy.Hostname() != "localhost" && proxy.Hostname() != "::1" {
		return fmt.Errorf("the demo requires Telltale's localhost HTTP proxy")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("recorded local workload")) }))
	defer server.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxy)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	child := exec.Command(executable, "--child")
	if err := child.Start(); err != nil {
		return err
	}
	defer func() {
		if child != nil {
			err = errors.Join(err, child.Wait())
		}
	}()
	started := time.Now()
	var held [][]byte
	for time.Since(started) < duration {
		if child != nil && time.Since(started) > 3*time.Second {
			if err := child.Wait(); err != nil {
				return err
			}
			child = nil
		}
		block := make([]byte, 2<<20)
		for i := range block {
			block[i] = byte(i)
		}
		held = append(held, block)
		busyUntil := time.Now().Add(80 * time.Millisecond)
		for time.Now().Before(busyUntil) {
			sum := sha256.Sum256(block)
			runtime.KeepAlive(sum)
		}
		response, err := client.Get(server.URL + "/orders")
		if err != nil {
			return err
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		closeErr := response.Body.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("local request: status %d", response.StatusCode)
		}
		if len(held) >= 32 {
			held = nil
			runtime.GC()
		}
		time.Sleep(400 * time.Millisecond)
	}
	runtime.KeepAlive(held)
	return nil
}
