package definition

import "github.com/flanksource/telltale/pkg/tracing/jvmtrace"

type JVMNotifications struct {
	Named
	Agent
	jvmtrace.NotificationOptions
}

func (p *JVMNotifications) options(Located) (any, any, error) {
	return p.NotificationOptions, nil, p.Validate()
}

type JVMExpression struct {
	Named
	Agent
	jvmtrace.ExpressionOptions
}

func (p *JVMExpression) options(Located) (any, any, error) {
	return p.ExpressionOptions, nil, p.Validate()
}

type JVMMethods struct {
	Named
	Agent
	jvmtrace.Options
}

func (p *JVMMethods) options(Located) (any, any, error) { return p.Options, nil, p.Validate() }

type JVMRedefine struct {
	Named
	Agent
	jvmtrace.RedefineOptions
}

func (p *JVMRedefine) options(Located) (any, any, error) {
	return p.RedefineOptions, nil, p.Validate()
}
