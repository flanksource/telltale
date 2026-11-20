package tracequery

import (
	"context"
	shared "github.com/flanksource/commons-db/query/tracequery"
)

func (s Service) query() shared.Service {
	return shared.Service{Connections: s.Connections, Read: s.load}
}
func (s Service) Sources() ([]Source, error) {
	return (shared.Service{Connections: s.Connections}).Sources()
}
func (s Service) Search(ctx context.Context, r Request) (SearchResult, error) {
	return s.query().Search(ctx, r)
}
func (s Service) Trace(ctx context.Context, r Request, id string) (Detail, error) {
	return s.query().Trace(ctx, r, id)
}
func (s Service) load(ctx context.Context, r Request, ids []string) ([]Span, error) {
	if r.Source == "records" {
		return s.recorded(ctx, r, ids)
	}
	return (shared.Service{Connections: s.Connections}).ReadSpans(ctx, r, ids)
}
