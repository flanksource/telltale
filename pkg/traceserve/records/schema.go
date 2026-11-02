package records

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/commons-db/query/schema"
	"github.com/flanksource/telltale/pkg/traces/read"
)

type queryOptions struct {
	Where string `json:"where,omitempty" clicky:"title=Row predicate,desc=CEL expression evaluated with a row binding"`
	From  string `json:"from,omitempty" clicky:"title=From,desc=Inclusive row timestamp in RFC3339 format"`
	Until string `json:"until,omitempty" clicky:"title=Until,desc=Exclusive row timestamp in RFC3339 format"`
}

func serveSchema(w http.ResponseWriter, r *http.Request, refs []read.Ref) {
	if len(refs) == 0 {
		http.Error(w, "no captures selected", http.StatusBadRequest)
		return
	}
	profile, err := refs[0].Runtime.Results.Registry.Get(r.Context(), "traces/"+refs[0].Kind)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	instance, err := schema.ProfileInstance(profile)
	if err != nil {
		http.Error(w, fmt.Sprintf("result profile schema: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/schema+json")
	if err := json.NewEncoder(w).Encode(map[string]any{"optionsSchema": rpc.SchemaForStruct(queryOptions{}), "columns": instance["x-clicky-columns"]}); err != nil {
		panic(http.ErrAbortHandler)
	}
}
