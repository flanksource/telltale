package agentinstall

import (
	javaagent "github.com/flanksource/telltale/pkg/agentinstall/java"
	"strconv"
)

type Java struct{ Home string }

func (Java) Artifact() (Artifact, error) {
	data, err := javaagent.Jar()
	return Artifact{Name: "telltale-agent.jar", Data: data}, err
}
func (j Java) Attach(a Attachment) []string {
	launcher := "java"
	classpath := a.Artifact
	if j.Home != "" {
		launcher = j.Home + "/bin/java"
		classpath += ":" + j.Home + "/lib/tools.jar"
	}
	return []string{launcher, "-cp", classpath, "com.flanksource.telltale.Attach", strconv.Itoa(a.PID), a.Artifact, a.Config}
}
