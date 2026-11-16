package tracequery

import (
	"github.com/flanksource/commons-db/connection/store"
	shared "github.com/flanksource/commons-db/query/tracequery"
)

type Span = shared.Span
type Request = shared.Request
type DocumentFormat = shared.DocumentFormat
type Source = shared.Source
type Summary = shared.Summary
type SearchResult = shared.SearchResult
type Detail = shared.Detail
type Modes = shared.Modes
type Mode = shared.Mode

const Include = shared.Include
const Exclude = shared.Exclude
const maxSpans = 100000

var ErrTraceNotFound = shared.ErrTraceNotFound

type Service struct {
	DataDir     string
	Connections *store.Store
}
