package main

import (
	"fmt"
	"strings"
	"time"
)

type httpRecord struct {
	Direction string `json:"direction"`
	RequestID string `json:"requestId"`
	URL       string `json:"url"`
	Route     string `json:"route"`
	Handler   string `json:"handler"`
	Status    int    `json:"status"`
	Error     string `json:"error"`
}

type sqlRecord struct {
	API   string `json:"api"`
	SQL   string `json:"sql"`
	Error string `json:"error"`
}

type jmsRecord struct {
	Direction   string `json:"direction"`
	Destination string `json:"destination"`
	MessageID   string `json:"messageId"`
	Framework   string `json:"framework"`
	Outcome     string `json:"outcome"`
	Error       string `json:"error"`
}

type spanRecord struct {
	TraceID    string    `json:"traceId"`
	SpanID     string    `json:"spanId"`
	ParentID   string    `json:"parentId"`
	Operation  string    `json:"operation"`
	StartedAt  time.Time `json:"startedAt"`
	DurationMs float64   `json:"durationMs"`
	Status     string    `json:"status"`
}

type completedEvidence struct {
	HTTP     []httpRecord `json:"http"`
	SQL      []sqlRecord  `json:"sql"`
	JMS      []jmsRecord  `json:"jms"`
	Mirrored []spanRecord `json:"mirrored"`
	Received []spanRecord `json:"received"`
}

func checkCompleted(evidence completedEvidence) error {
	if err := checkHTTP(evidence.HTTP); err != nil {
		return err
	}
	if err := checkSQL(evidence.SQL); err != nil {
		return err
	}
	if err := checkJMS(evidence.JMS); err != nil {
		return err
	}
	return checkMirror(evidence)
}

func checkHTTP(rows []httpRecord) error {
	directions, matchedRoute := map[string]bool{}, false
	for _, row := range rows {
		if !strings.Contains(row.URL, "/demo/") {
			continue
		}
		if row.Error != "" || row.Status != 200 {
			return fmt.Errorf("demo HTTP %s failed: status=%d error=%s", row.URL, row.Status, row.Error)
		}
		directions[row.Direction] = true
		if row.Direction == "incoming" && strings.Contains(row.Route, "/demo/{") && row.Handler != "" {
			matchedRoute = true
		}
	}
	for _, direction := range []string{"incoming", "outgoing"} {
		if !directions[direction] {
			return fmt.Errorf("no completed %s demo HTTP operation", direction)
		}
	}
	if !matchedRoute {
		return fmt.Errorf("incoming HTTP has no matched Spring route and handler")
	}
	return nil
}

func checkSQL(rows []sqlRecord) error {
	apis := map[string]bool{}
	for _, row := range rows {
		if !strings.Contains(strings.ToUpper(row.SQL), "DEMO_HOLD") {
			continue
		}
		if row.Error != "" {
			return fmt.Errorf("demo %s SQL failed: %s", row.API, row.Error)
		}
		apis[row.API] = true
	}
	for _, api := range []string{"jdbc", "r2dbc"} {
		if !apis[api] {
			return fmt.Errorf("no completed %s demo SQL operation", api)
		}
	}
	return nil
}

func checkJMS(rows []jmsRecord) error {
	produced, consumed := map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		if row.Destination != "demo.orders" {
			continue
		}
		if row.Error != "" || row.Outcome == "error" {
			return fmt.Errorf("demo JMS %s failed: %s", row.Direction, row.Error)
		}
		if row.Outcome != "success" || row.MessageID == "" {
			continue
		}
		if row.Direction == "outgoing" {
			produced[row.MessageID] = true
		} else if row.Direction == "incoming" && row.Framework == "spring-jms" {
			consumed[row.MessageID] = true
		}
	}
	for id := range produced {
		if consumed[id] {
			return nil
		}
	}
	return fmt.Errorf("no demo JMS message links outgoing production to incoming Spring processing")
}

func checkMirror(evidence completedEvidence) error {
	received := make(map[string]spanRecord, len(evidence.Received))
	for _, span := range evidence.Received {
		received[span.TraceID+"/"+span.SpanID] = span
	}
	cycle := false
	for _, span := range evidence.Mirrored {
		key := span.TraceID + "/" + span.SpanID
		if span.TraceID == "" || span.SpanID == "" {
			return fmt.Errorf("mirrored span has no trace/span identity")
		}
		original, found := received[key]
		if !found {
			return fmt.Errorf("original exporter has not delivered mirrored span %s (%s)", key, span.Operation)
		}
		if original != span {
			return fmt.Errorf("original exporter span %s changed during mirroring", key)
		}
		cycle = cycle || span.Operation == "jvm-demo-cycle"
	}
	if !cycle {
		return fmt.Errorf("no mirrored jvm-demo-cycle span from the existing exporter")
	}
	return nil
}
