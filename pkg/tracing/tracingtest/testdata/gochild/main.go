// gochild is the Go process the trace kinds' specs observe: it exposes what a
// flag asks for, announces each on stdout as "<name> <value>", then "ready",
// and runs until it is killed or its stdin closes. Each "open <path>" line on
// stdin opens one more file.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/flanksource/commons-db/models"
	"github.com/flanksource/commons-db/observability"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/query/providers/sqlite"
	telltale "github.com/flanksource/telltale/sdk"
	_ "github.com/flanksource/telltale/sdk/commonsdb"
	"github.com/google/gops/agent"
)

func main() {
	serve := flag.Bool("pprof", false, "serve net/http/pprof")
	gops := flag.String("gops", "", "run a gops agent, keeping its port file in this dir")
	cpuProfile := flag.String("cpuprofile", "", "burn CPU into this CPU profile, then exit")
	hold := flag.String("hold", "", "hold this file open")
	listen := flag.Bool("listen", false, "listen on a TCP port and dial it")
	agentDir := flag.String("agent-config", "", "start a telltale agent in this config directory")
	flag.Parse()

	if *cpuProfile != "" {
		must(profileCPU(*cpuProfile))
		return
	}
	go parkedForever(make(chan struct{}))
	if *agentDir != "" {
		capture, err := telltale.Start(telltale.Options{ConfigDir: *agentDir, Listen: "tcp"})
		must(err)
		defer capture.Close()
		go func() {
			for {
				burnCPU(time.Second)
			}
		}()
	}
	var held []any
	if *serve {
		listener := mustListen()
		go func() { must(http.Serve(listener, nil)) }()
		announce("pprof", listener.Addr().String())
	}
	if *gops != "" {
		must(agent.Listen(agent.Options{Addr: "127.0.0.1:0", ConfigDir: *gops}))
		port, err := os.ReadFile(filepath.Join(*gops, fmt.Sprint(os.Getpid())))
		must(err)
		announce("gops", "127.0.0.1:"+string(port))
	}
	if *hold != "" {
		file, err := os.Create(*hold)
		must(err)
		held = append(held, file)
	}
	if *listen {
		held = append(held, connectToSelf()...)
	}
	announce("ready", fmt.Sprint(len(held)))
	commands(held)
}

// commands opens the file each "open <path>" line names until stdin closes.
func commands(held []any) {
	logger := slog.New(telltale.SlogHandler(slog.NewTextHandler(os.Stderr, nil))).With("logger", "child")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if address, ok := strings.CutPrefix(scanner.Text(), "http "); ok {
			client := http.Client{Transport: observability.ApplyHTTPObservability(context.Background(), "http", nil, nil)}
			request, err := http.NewRequest(http.MethodGet, address, nil)
			must(err)
			request.Header.Set("Authorization", "Bearer authorization-secret")
			request.Header.Set("Cookie", "session=cookie-secret")
			response, err := client.Do(request)
			must(err)
			must(response.Body.Close())
			announce("http", "done")
			continue
		}
		if scanner.Text() == "sql" {
			db, err := sql.Open("sqlite", ":memory:")
			must(err)
			_, err = sqlite.ReadSQLPage(context.Background(), db, models.ConnectionTypeSQLite, sqlite.SQLPageRequest{Connection: "parity", Query: "SELECT 1", Page: query.PageRequest{Limit: 10}})
			must(err)
			must(db.Close())
			announce("sql", "done")
			continue
		}
		if message, ok := strings.CutPrefix(scanner.Text(), "log "); ok {
			logger.Info(message)
			continue
		}
		if path, ok := strings.CutPrefix(scanner.Text(), "open "); ok {
			file, err := os.Create(path)
			must(err)
			held = append(held, file)
			announce("opened", path)
		}
	}
}

// connectToSelf listens on a port and holds a connection dialed to it.
func connectToSelf() []any {
	listener := mustListen()
	accepted := make(chan net.Conn)
	go func() {
		conn, err := listener.Accept()
		must(err)
		accepted <- conn
	}()
	dialed, err := net.Dial("tcp", listener.Addr().String())
	must(err)
	announce("listen", listener.Addr().String())
	return []any{listener, dialed, <-accepted}
}

func profileCPU(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := pprof.StartCPUProfile(file); err != nil {
		return err
	}
	burnCPU(500 * time.Millisecond)
	pprof.StopCPUProfile()
	return nil
}

//go:noinline
func burnCPU(d time.Duration) {
	sum := 0
	for end := time.Now().Add(d); time.Now().Before(end); {
		for i := range 10000 {
			sum += i * i
		}
	}
	fmt.Fprintln(os.Stderr, sum)
}

//go:noinline
func parkedForever(never chan struct{}) { <-never }

func mustListen() net.Listener {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	return listener
}

func announce(name, value string) { fmt.Printf("%s %s\n", name, value) }

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
