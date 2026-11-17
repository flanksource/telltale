package agentinstall

import (
	"strconv"

	javaagent "github.com/flanksource/telltale/pkg/agentinstall/java"
)

type Java struct{}

func (Java) Artifact() (Artifact, error) {
	data, err := javaagent.Jar()
	return Artifact{Name: "telltale-agent.jar", Data: data}, err
}

// Attach is the helper JVM that attaches the agent to a.PID. It runs without
// the JVM option variables: a container's JAVA_TOOL_OPTIONS loads its
// javaagents (a jmx-exporter, an OpenTelemetry agent) into every JVM, and one
// that binds a port the target already holds kills the helper.
func (Java) Attach(a Attachment) []string {
	launcher := "java"
	classpath := a.Artifact
	if a.JavaHome != "" {
		launcher = a.JavaHome + "/bin/java"
		classpath += ":" + a.JavaHome + "/lib/tools.jar"
	}
	return []string{"env", "-u", "JAVA_TOOL_OPTIONS", "-u", "_JAVA_OPTIONS", "-u", "JDK_JAVA_OPTIONS",
		launcher, "-cp", classpath, "com.flanksource.telltale.Attach", strconv.Itoa(a.PID), a.Artifact, a.Config}
}
