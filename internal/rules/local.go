package rules

import (
	"context"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/process"
	"time"
)

func PreviewFile(ctx context.Context, path, workDir string) ([]Preview, error) {
	program, err := Load(path)
	if err != nil {
		return nil, err
	}
	catalog, err := groups.CatalogFile(workDir)
	if err != nil {
		return nil, err
	}
	options, err := (groups.Params{Catalog: catalog, Interval: groups.DefaultInterval.String()}).Options()
	if err != nil {
		return nil, err
	}
	top, err := groups.NewTop(ctx, process.NewSampler(), options)
	if err != nil {
		return nil, err
	}
	defer top.Close()
	if _, err := top.Snapshot(ctx); err != nil {
		return nil, err
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	snapshot, err := top.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return program.Preview(ctx, snapshot)
}
