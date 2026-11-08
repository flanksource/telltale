package processcapture

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	clickyprocess "github.com/flanksource/clicky/process"
	"maps"
	"net"
	"slices"
	"strings"
	"time"
)

const (
	Files             = "files"
	Sockets           = "sockets"
	ConnectionIn      = "connection_in"
	ConnectionOut     = "connection_out"
	ConnectionUnknown = "connection_unknown"
)

type FacetEvent struct {
	Process         string                   `json:"process"`
	At              time.Time                `json:"observedAt"`
	Tick            int64                    `json:"tick"`
	Facet           string                   `json:"facet"`
	Entity          string                   `json:"entity"`
	Operation       string                   `json:"operation"`
	DirectionSource string                   `json:"directionSource,omitempty"`
	Reason          string                   `json:"reason,omitempty"`
	Resource        clickyprocess.Descriptor `json:"resource"`
}

type FacetObservation struct {
	Process     string
	At          time.Time
	Tick        int64
	Complete    bool
	Exited      bool
	Descriptors []clickyprocess.Descriptor
	Enabled     []string
}

type facetResource struct {
	facet, entity, direction string
	descriptor               clickyprocess.Descriptor
}

// FacetState is an immutable observation checkpoint. Diff returns its successor;
// the collector adopts it only after the observation's storage transaction commits.
type FacetState struct {
	resources map[string]map[string]facetResource
	listeners map[string][]clickyprocess.Descriptor
}

func NewFacetState() *FacetState {
	return &FacetState{resources: map[string]map[string]facetResource{}, listeners: map[string][]clickyprocess.Descriptor{}}
}

func (s *FacetState) Diff(o FacetObservation) ([]FacetEvent, *FacetState) {
	if !o.Complete && !o.Exited {
		return nil, s
	}
	next := s.clone()
	return next.diff(o), next
}

func (s *FacetState) clone() *FacetState {
	return &FacetState{resources: maps.Clone(s.resources), listeners: maps.Clone(s.listeners)}
}

func (s *FacetState) diff(o FacetObservation) []FacetEvent {
	if !o.Complete && !o.Exited {
		return nil
	}
	previous := s.resources[o.Process]
	peers := map[string]facetResource{}
	for _, old := range previous {
		if strings.HasPrefix(old.facet, "connection_") {
			peers[old.descriptor.Protocol+"/"+old.descriptor.RemoteAddr] = old
		}
	}
	current := map[string]facetResource{}
	if !o.Exited {
		listeners := slices.Clone(s.listeners[o.Process])
		for _, d := range o.Descriptors {
			if d.IsListening() && !slices.Contains(listeners, d) {
				listeners = append(listeners, d)
			}
		}
		s.listeners[o.Process] = listeners
		for _, d := range o.Descriptors {
			resource, ok := s.classify(o.Process, d, listeners)
			if !ok {
				continue
			}
			selection := resource.facet
			if strings.HasPrefix(selection, "connection_") {
				selection = "connections"
			}
			if o.Enabled != nil && !slices.Contains(o.Enabled, selection) {
				continue
			}
			// Retain the first inferred direction for an active peer. A later
			// polling observation cannot establish who initiated that connection.
			if strings.HasPrefix(resource.facet, "connection_") {
				if old, ok := peers[d.Protocol+"/"+d.RemoteAddr]; ok {
					resource = old
				}
			}
			current[resource.facet+"/"+resource.entity] = resource
		}
	}
	events := []FacetEvent{}
	for key, old := range previous {
		if _, ok := current[key]; !ok {
			events = append(events, facetEvent(o, old, "removed"))
		}
	}
	for key, value := range current {
		if _, ok := previous[key]; !ok {
			events = append(events, facetEvent(o, value, "added"))
		}
	}
	slices.SortFunc(events, func(a, b FacetEvent) int {
		if n := cmp.Compare(a.Operation, b.Operation); n != 0 {
			return -n
		}
		if n := cmp.Compare(a.Facet, b.Facet); n != 0 {
			return n
		}
		return cmp.Compare(a.Entity, b.Entity)
	})
	if o.Exited {
		delete(s.resources, o.Process)
		delete(s.listeners, o.Process)
	} else {
		s.resources[o.Process] = current
	}
	return events
}

func (s *FacetState) classify(process string, d clickyprocess.Descriptor, listeners []clickyprocess.Descriptor) (facetResource, bool) {
	facet, direction := Files, ""
	if d.IsSocket() {
		switch {
		case d.IsListening():
			facet = Sockets
		case d.Protocol == "TCP" && d.State == "ESTABLISHED" && d.RemoteAddr != "":
			facet, direction = ConnectionUnknown, "unknown"
			for _, listener := range listeners {
				if receiving(listener, d) {
					facet, direction = ConnectionIn, "inferred_listener"
					break
				}
			}
			if facet == ConnectionUnknown {
				if _, observed := s.resources[process]; observed {
					facet, direction = ConnectionOut, "inferred_no_listener"
				}
			}
			d = clickyprocess.Descriptor{Protocol: d.Protocol, RemoteAddr: d.RemoteAddr}
		default:
			return facetResource{}, false
		}
	}
	encoded, err := json.Marshal(d)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return facetResource{facet: facet, entity: hex.EncodeToString(sum[:]), direction: direction, descriptor: d}, true
}

func receiving(listener, connection clickyprocess.Descriptor) bool {
	if listener.Protocol != connection.Protocol {
		return false
	}
	host, port, err := net.SplitHostPort(listener.LocalAddr)
	if err != nil {
		return listener.LocalAddr == connection.LocalAddr
	}
	localHost, localPort, err := net.SplitHostPort(connection.LocalAddr)
	return err == nil && port == localPort && (host == localHost || host == "*" || host == "0.0.0.0" || host == "::")
}

func facetEvent(o FacetObservation, r facetResource, operation string) FacetEvent {
	event := FacetEvent{Process: o.Process, At: o.At, Tick: o.Tick, Facet: r.facet, Entity: r.entity, Operation: operation, DirectionSource: r.direction, Resource: r.descriptor}
	if o.Exited {
		event.Reason = "observed_exit"
	}
	return event
}

func (s *FacetState) counts(o FacetObservation) map[string]any {
	counts := map[string]int{}
	for _, resource := range s.resources[o.Process] {
		counts[resource.facet]++
	}
	values := map[string]any{}
	for facet, metric := range map[string]string{Files: "openFiles", Sockets: "receivingSockets", ConnectionIn: "connectionsIn", ConnectionOut: "connectionsOut", ConnectionUnknown: "connectionsUnknown"} {
		selected := facet
		if strings.HasPrefix(facet, "connection_") {
			selected = "connections"
		}
		if o.Enabled == nil || slices.Contains(o.Enabled, selected) {
			values[metric] = counts[facet]
		}
	}
	return values
}
