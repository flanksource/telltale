package release_test

import (
 "os"
 "path/filepath"
 . "github.com/onsi/ginkgo/v2"
 . "github.com/onsi/gomega"
)

func writeAgentFixture(directory string){
 GinkgoHelper()
 Expect(os.MkdirAll(filepath.Join(directory,"pkg/agentinstall/java/assets"),0755)).To(Succeed())
 Expect(os.WriteFile(filepath.Join(directory,"pkg/agentinstall/java/assets/telltale-agent.jar"),[]byte("verified-agent-fixture"),0644)).To(Succeed())
}
