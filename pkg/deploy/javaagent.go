package deploy

import (
	"encoding/json"
	"fmt"
)

// KindJavaAgent is a JVM that pre-loads the agent (an init container or
// JAVA_TOOL_OPTIONS). It is registered so a deployer naming it fails with
// what is missing rather than as an unknown kind.
const KindJavaAgent = "javaagent"

func init() {
	RegisterJavaContainer(KindJavaAgent, func(json.RawMessage) (JavaContainer, error) {
		return nil, fmt.Errorf("kind %s is not implemented yet: use %s to attach the agent at runtime or %s to deploy the WAR", KindJavaAgent, KindAttach, KindTomcat)
	})
}
