package groups

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/telltale/pkg/tracing/process"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	acmeSession  = "5f0c3a52-acme-4b1e-9d3a-000000000001"
	acmePrompt   = "Fix the flaky acme login test"
	acmeSurface  = "8E1C7A10-ACME-4C2B-9F00-000000000002"
	acmeTreeJSON = `{"windows":[{"workspaces":[{"title":"✳ acme-api","panes":[{"surfaces":[{"id":"` + acmeSurface + `","ref":"surface:3","title":"⠋ claude · login fix","tty":"ttys004","type":"terminal"}]}]}]}]}`
)

// fakeHome points the claude projects directory at a temporary home holding
// one transcript of acmeSession that opens with acmePrompt.
func fakeHome() {
	ginkgo.GinkgoHelper()
	home := ginkgo.GinkgoT().TempDir()
	ginkgo.GinkgoT().Setenv("HOME", home)
	project := filepath.Join(home, ".claude", "projects", "-Users-acme-api")
	Expect(os.MkdirAll(project, 0o755)).To(Succeed())
	line := `{"type":"user","uuid":"m1","sessionId":"` + acmeSession + `","timestamp":"2026-10-25T12:00:00.000Z","cwd":"/Users/acme/api","message":{"role":"user","content":[{"type":"text","text":"` + acmePrompt + `"}]}}` + "\n"
	Expect(os.WriteFile(filepath.Join(project, acmeSession+".jsonl"), []byte(line), 0o644)).To(Succeed())
}

// fakeCmux points captain at a cmux CLI that prints tree as its tree.
func fakeCmux(tree string) {
	ginkgo.GinkgoHelper()
	dir := ginkgo.GinkgoT().TempDir()
	Expect(os.WriteFile(filepath.Join(dir, "tree.json"), []byte(tree), 0o644)).To(Succeed())
	script := "#!/bin/sh\nexec cat " + filepath.Join(dir, "tree.json") + "\n"
	Expect(os.WriteFile(filepath.Join(dir, "cmux"), []byte(script), 0o755)).To(Succeed())
	ginkgo.GinkgoT().Setenv("CMUX_BIN", filepath.Join(dir, "cmux"))
}

var _ = ginkgo.Describe("names", func() {
	ginkgo.It("names a claude session after its opening prompt, and a cmux surface and workspace after their titles", func() {
		fakeHome()
		fakeCmux(acmeTreeJSON)
		resolver := newNames()
		first := Context{Agent: "claude", Session: acmeSession, Surface: acmeSurface}

		Expect(resolver.fill(&first)).To(Succeed())
		Expect(first.SessionTitle).To(BeEmpty(), "the transcript is read off the tick, so the first fill has no title yet")
		Eventually(func(g Gomega) {
			context := Context{Agent: "claude", Session: acmeSession, Surface: acmeSurface}
			g.Expect(resolver.fill(&context)).To(Succeed())
			g.Expect(context).To(Equal(Context{
				Agent: "claude", Session: acmeSession, SessionTitle: acmePrompt,
				Surface: acmeSurface, SurfaceTitle: "claude · login fix", WorkspaceTitle: "acme-api",
			}))
		}).WithTimeout(5 * time.Second).Should(Succeed())
	})

	ginkgo.It("leaves a session no transcript holds yet, and a closed surface, unnamed", func() {
		fakeHome()
		fakeCmux(`{"windows":[]}`)
		resolver := newNames()
		context := Context{Agent: "claude", Session: "no-transcript-yet", Surface: acmeSurface}
		Expect(resolver.fill(&context)).To(Succeed())

		Consistently(func(g Gomega) {
			context := Context{Agent: "claude", Session: "no-transcript-yet", Surface: acmeSurface}
			g.Expect(resolver.fill(&context)).To(Succeed())
			g.Expect(context.SessionTitle).To(BeEmpty())
			g.Expect(context.SurfaceTitle).To(BeEmpty())
		}).WithTimeout(300 * time.Millisecond).Should(Succeed())
	})

	ginkgo.It("says why cmux's titles could not be read", func() {
		ginkgo.GinkgoT().Setenv("CMUX_BIN", filepath.Join(ginkgo.GinkgoT().TempDir(), "missing-cmux"))
		context := Context{Surface: acmeSurface}

		Expect(newNames().fill(&context)).To(MatchError(ContainSubstring("cmux surfaces")))
	})
})

var _ = ginkgo.Describe("openRollout", func() {
	ginkgo.It("is the thread of the rollout a codex process holds open, the one written last when it runs sub-agents", func() {
		const main, subagent, read = "019a2b3c-4d5e-7f60-8a9b-0ac3e0000001", "019a2b3c-4d5e-7f60-8a9b-0ac3e0000002", "019a2b3c-4d5e-7f60-8a9b-0ac3e0000003"
		sessions := ginkgo.GinkgoT().TempDir()
		rolloutOf := func(thread string, writtenAgo time.Duration) string {
			path := filepath.Join(sessions, "rollout-2026-10-25T12-00-00-"+thread+".jsonl")
			Expect(os.WriteFile(path, []byte("{}\n"), 0o644)).To(Succeed())
			at := time.Now().Add(-writtenAgo)
			Expect(os.Chtimes(path, at, at)).To(Succeed())
			return path
		}
		member := func(pid int, command string) process.Observed {
			return process.Observed{Process: clickyprocess.Process{PID: pid, Command: command}}
		}
		wrapper := member(1000, "node /pnpm/global/node_modules/@openai/codex/bin/codex.js --enable hooks")
		native := member(1001, "/pnpm/store/@openai/codex/vendor/aarch64-apple-darwin/codex/codex --enable hooks")
		reader := member(1002, "captain session show")
		held := map[int][]clickyprocess.Descriptor{
			1000: {{FD: "3r", Type: "REG", Name: "/Users/acme/.codex/config.toml"}},
			1001: {
				{FD: "5r", Type: "REG", Name: filepath.Join(sessions, "notes.jsonl")},
				{FD: "9w", Type: "REG", Name: rolloutOf(subagent, time.Minute)},
				{FD: "41w", Type: "REG", Name: rolloutOf(main, time.Second)},
			},
			1002: {{FD: "7r", Type: "REG", Name: rolloutOf(read, 0)}},
		}

		Expect(openRollout([]process.Observed{wrapper, native, reader}, held)).To(Equal(main))
		Expect(openRollout([]process.Observed{wrapper, reader}, held)).To(BeEmpty(), "a process that is no codex reading a rollout is not its session")
	})
})

var _ = ginkgo.Describe("contexts.anchors", func() {
	ginkgo.It("takes an agent's session from its command line when its environment names none", func(ctx context.Context) {
		fakeHome()
		bare := exec.Command("sleep", "30")
		bare.Env = []string{}
		Expect(bare.Start()).To(Succeed())
		ginkgo.DeferCleanup(func() { _ = bare.Process.Kill(); _ = bare.Wait() })
		agent := clickyprocess.Process{PID: bare.Process.Pid, Command: "/Users/acme/.local/bin/claude --session-id " + acmeSession}

		read := newContexts().anchors(ctx, []clickyprocess.Process{agent})

		Expect(read).To(ConsistOf(And(HaveField("Agent", "claude"), HaveField("Session", acmeSession))))
	})
})
