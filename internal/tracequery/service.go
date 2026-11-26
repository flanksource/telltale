package tracequery

import (
	"context"

	shared "github.com/flanksource/commons-db/query/tracequery"
)

func (s Service) query(source string) shared.Service {
	service := shared.Service{Connections: s.Connections}
	if source == "records" {
		service.Read = s.recorded
	}
	return service
}
func (s Service) Sources() ([]Source, error) {
	return s.query("").Sources()
}
func (s Service) Search(ctx context.Context, r Request) (SearchResult, error) {
	return s.query(r.Source).Search(ctx, r)
}
func (s Service) Trace(ctx context.Context, r Request, id string) (Detail, error) {
	return s.query(r.Source).Trace(ctx, r, id)
}
