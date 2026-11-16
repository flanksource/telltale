package tracing

import (
	"context"
	"fmt"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/recordresults"
)

type captureBackendKey struct{}

// CaptureBackend is the atomic writer of a capture with named result tables.
func CaptureBackend(ctx context.Context) (recordstore.BatchAppender, error) {
	backend, ok := ctx.Value(captureBackendKey{}).(recordstore.BatchAppender)
	if !ok {
		return nil, fmt.Errorf("capture backend does not support atomic batches")
	}
	return backend, nil
}

// ResultTable is a named, read-only result of a capture. It is advertised with
// its owning kind, and registered with the same query/export infrastructure.
type ResultTable struct {
	Name   string `json:"name"`
	plugin TracePlugin
}

type resultParams struct{}

func (resultParams) Validate() error {
	return fmt.Errorf("result tables cannot be started independently")
}

type resultReader[R any] struct{ result recordresults.ResultType[R] }

func (r resultReader[R]) Params() resultParams                { return resultParams{} }
func (r resultReader[R]) Schema() recordresults.ResultType[R] { return r.result }
func (resultReader[R]) Handle(dbcontext.Context, resultParams, Emitter[R], Records[R]) error {
	return fmt.Errorf("result tables cannot be started independently")
}

func NewResultTable[R any](name string, result recordresults.ResultType[R]) ResultTable {
	return ResultTable{Name: name, plugin: NewHandler[resultParams, R](resultReader[R]{result: result}, Capabilities{Live: true}, "self")}
}

func (h *Handler[P, R]) WithResults(tables ...ResultTable) *Handler[P, R] {
	h.tables = append(h.tables, tables...)
	return h
}
func (h *Handler[P, R]) ResultTables() []ResultTable { return h.tables }

// ResultOf identifies the capture and named result behind a storage kind.
func (k *Kinds) ResultOf(storage string) (kind, result string) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	for name, plugin := range k.plugins {
		for _, table := range plugin.ResultTables() {
			if storage == name+"_"+table.Name {
				return name, table.Name
			}
		}
	}
	return storage, ""
}

// CaptureRefs returns the primary and all named results of a capture.
func (k *Kinds) CaptureRefs(ref TraceRef) []TraceRef {
	k.mu.RLock()
	defer k.mu.RUnlock()
	refs := []TraceRef{ref}
	if plugin := k.plugins[ref.Kind]; plugin != nil {
		for _, table := range plugin.ResultTables() {
			refs = append(refs, TraceRef{Kind: ref.Kind + "_" + table.Name, Stream: ref.Stream + "-" + table.Name})
		}
	}
	return refs
}

// Schemas includes each kind's primary table and its named result tables.
func (k *Kinds) Schemas() ([]recordstore.KindSchema, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	schemas := []recordstore.KindSchema{}
	for _, name := range k.names() {
		plugin := k.plugins[name]
		schema, err := plugin.Schema(name)
		if err != nil {
			return nil, err
		}
		schemas = append(schemas, schema)
		for _, result := range plugin.ResultTables() {
			schema, err := result.plugin.Schema(name + "_" + result.Name)
			if err != nil {
				return nil, err
			}
			schemas = append(schemas, schema)
		}
	}
	return schemas, nil
}
