package goprof

import "github.com/flanksource/clicky/rpc"

// Schema describes checkpoint parameters with this checkpoint's defaults.
func (p CheckpointParams) Schema() *rpc.OpenAPISchema {
	schema := rpc.SchemaForStruct(p)
	schema.Properties["source"].Enum = []any{SourcePProf, SourceGops, SourceFile}
	schema.Properties["source"].Default = p.Source
	schema.Properties["profile"].Enum = make([]any, len(Profiles))
	for index, profile := range Profiles {
		schema.Properties["profile"].Enum[index] = profile
	}
	schema.Properties["profile"].Default = p.Profile
	schema.Properties["seconds"].Default = p.Seconds
	return schema
}
