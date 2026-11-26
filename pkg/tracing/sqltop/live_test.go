package sqltop

import (
	"context"
	"encoding/json"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	. "github.com/onsi/gomega"
)

func prepareLiveCapture(ctx dbcontext.Context, params Params, out *recorder) (dbcontext.Context, func() error, error) {
	env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"sql_top": Kind()})
	raw, err := json.Marshal(params)
	if err != nil {
		return ctx, nil, err
	}
	managed, err := env.Runtime.Start(ctx, tracing.StartRequest{Kind: "sql_top", Params: raw})
	if err != nil {
		return ctx, nil, err
	}
	info := managed.Session().Snapshot()
	err = env.Results.Backend.Scan(ctx, info.Events.Stream, 0, func(_ int64, row recordstore.Row) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var sample Sample
		if err := json.Unmarshal(data, &sample); err != nil {
			return err
		}
		out.rows = append(out.rows, sample)
		return nil
	})
	if err != nil {
		return ctx, nil, err
	}
	Expect(env.Read("sql_top_active", info.Events.Stream+"-active", query.PageRequest{Limit: 100}, nil)).To(HaveLen(len(out.rows)))
	stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := managed.Stop(stop); err != nil {
		return ctx, nil, err
	}
	err = env.Results.Backend.Scan(ctx, info.Events.Stream+"-frames", 0, func(_ int64, row recordstore.Row) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var frame sampleframe.Frame
		if err := json.Unmarshal(data, &frame); err != nil {
			return err
		}
		out.frames = append(out.frames, frame)
		return nil
	})
	if err != nil {
		return ctx, nil, err
	}
	out.report = tracingtest.Summary(managed.Session().Snapshot()).Report
	return ctx, func() error {
		stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := managed.Stop(stop)
		return err
	}, nil
}
