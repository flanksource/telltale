package definition

import (
	"errors"
	"slices"

	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/pkg/tracing/httptop"
	"github.com/flanksource/telltale/pkg/tracing/jms"
	"github.com/flanksource/telltale/pkg/tracing/sqltop"
	"github.com/flanksource/telltale/pkg/tracing/target"
)

type JVMSQLProbe struct{ SQLProbe }

func (p *JVMSQLProbe) options(Located) (any, any, error) {
	options := p.Options
	if len(options.Connections) == 0 {
		options.Connections = []string{"*"}
	}
	if slices.Contains(options.Connections, "self") {
		return nil, nil, errors.New("connections self is Go-only; use * for JVM SQL")
	}
	return options, nil, options.Validate()
}

func (JVMSQLProbe) JSONSchema() map[string]any {
	schema := rpc.SchemaForStruct(SQLProbe{})
	schema.Required = slices.DeleteFunc(schema.Required, func(name string) bool { return name == "connections" })
	schema.Properties["connections"].Default = []string{"*"}
	schema.Properties["connections"].Description = "JVM connection identities to observe; * selects every observed connection"
	return map[string]any{"type": schema.Type, "properties": schema.Properties, "required": schema.Required}
}

type HTTPTopProbe struct {
	Named
	Agent
	httptop.Options
}

func (p *HTTPTopProbe) options(Located) (any, any, error) {
	return p.Options, nil, p.Validate()
}

type JMSProbe struct {
	Named
	Agent
	jms.Options
}

func (p *JMSProbe) options(Located) (any, any, error) {
	return p.Options, nil, p.Validate()
}

type JVMTop struct {
	Named
	Agent
	sqltop.Options
}

func (p *JVMTop) options(Located) (any, any, error) {
	return p.Options, nil, (sqltop.Params{Target: target.Target{Seam: target.Agent}, Options: p.Options}).Validate()
}
