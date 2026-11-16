package agentinstall

import (
	javaagent "github.com/flanksource/telltale/pkg/agentinstall/java"
	"strconv"
)

type Java struct{}

func (Java) Artifact() (Artifact, error) {
	data, err := javaagent.Jar()
	return Artifact{Name: "telltale-agent.jar", Data: data}, err
}
func (Java) Attach(a Attachment) []string {
	launcher := "java"
	classpath := a.Artifact
	if a.JavaHome != "" {
		launcher = a.JavaHome + "/bin/java"
		classpath += ":" + a.JavaHome + "/lib/tools.jar"
	}
	return []string{launcher, "-cp", classpath, "com.flanksource.telltale.Attach", strconv.Itoa(a.PID), a.Artifact, a.Config}
}
