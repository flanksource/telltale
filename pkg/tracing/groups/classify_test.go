package groups_test

import (
	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	"github.com/flanksource/telltale/pkg/tracing/process"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func observed(pid, ppid int, command string) process.Observed {
	return process.Observed{Process: clickyprocess.Process{PID: pid, PPID: ppid, Command: command, Status: "sleeping"}}
}

// shape is a group as the specs compare it: its tree position, rule, target
// and members.
type shape struct {
	Key, Parent, Name, Target string
	PIDs                      []int
}

func shapes(classified []groups.Group) []shape {
	result := make([]shape, 0, len(classified))
	for _, group := range classified {
		pids := make([]int, 0, len(group.Members))
		for _, member := range group.Members {
			pids = append(pids, member.PID)
		}
		result = append(result, shape{Key: group.Key, Parent: group.ParentKey, Name: group.Rule.Name, Target: group.Target, PIDs: pids})
	}
	return result
}

var _ = Describe("Classify", func() {
	var catalog groups.Catalog

	BeforeEach(func() {
		var err error
		catalog, err = groups.LoadCatalog()
		Expect(err).NotTo(HaveOccurred())
	})
	It("keeps Go build drivers and compiler helpers in independent job groups", func() {
		listing := []process.Observed{
			observed(100, 1, "go build ./cmd/acme"),
			observed(101, 100, "/toolchain/pkg/tool/darwin_arm64/compile -p acme"),
			observed(200, 1, "go -C /work/other build ./..."),
			observed(201, 200, "/toolchain/pkg/tool/darwin_arm64/link -o acme"),
		}
		Expect(shapes(groups.Classify(listing, groups.ClassifyOptions{Catalog: catalog}))).To(Equal([]shape{
			{Key: "100", Name: "go-build", Target: "./cmd/acme", PIDs: []int{100, 101}},
			{Key: "200", Name: "go-build", Target: "/work/other ./...", PIDs: []int{200, 201}},
		}))
	})

	It("nests each tool's group under the nearest group above it, keeping helpers in their tool's group", func() {
		listing := []process.Observed{
			observed(1, 0, "/sbin/launchd"),
			observed(715, 1, "/Applications/cmux.app/Contents/MacOS/cmux"),
			observed(800, 715, "/bin/zsh -l"),
			observed(900, 800, "/Users/acme/.local/bin/claude --session-id abc-123 --settings /tmp/s.json"),
			observed(901, 900, "/Applications/cmux.app/Contents/Resources/bin/cmux hooks claude inbox-wait"),
			observed(902, 900, "/bin/zsh -c gavel test"),
			observed(903, 902, "gavel test ./..."),
			observed(904, 903, "go test -count=1 ./pkg/..."),
			observed(905, 904, "/tmp/go-build1/b001/pkg.test -test.v"),
			observed(1100, 900, "agent-browser open https://example.com --session demo"),
			observed(1101, 1100, "/Applications/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing --headless"),
			observed(1102, 1101, "/Applications/Google Chrome for Testing.app/Contents/Frameworks/Google Chrome for Testing Helper (Renderer).app/x --type=renderer"),
		}

		origins := map[string]groups.Origin{"1": {Owner: "root", Scope: groups.ScopeSystem}}

		Expect(shapes(groups.Classify(listing, groups.ClassifyOptions{Catalog: catalog, Origins: origins}))).To(Equal([]shape{
			{Key: "715", Name: "cmux", PIDs: []int{715, 800}},
			{Key: "900", Parent: "715", Name: "claude", Target: "abc-123", PIDs: []int{900, 901, 902}},
			{Key: "903", Parent: "900", Name: "gavel", Target: "test ./...", PIDs: []int{903}},
			{Key: "904", Parent: "903", Name: "go-test", Target: "./pkg/...", PIDs: []int{904, 905}},
			{Key: "1100", Parent: "900", Name: "agent-browser", Target: "demo", PIDs: []int{1100}},
			{Key: "1101", Parent: "1100", Name: "chrome", PIDs: []int{1101, 1102}},
			{Key: "other:system", Name: "system", PIDs: []int{}},
			{Key: "other:system:launchd", Parent: "other:system", Name: "launchd", PIDs: []int{1}},
		}))
	})

	It("starts a group for a brew service no rule matches, which its own processes join", func() {
		listing := []process.Observed{
			observed(12679, 1, "/opt/homebrew/opt/postgresql@17/bin/postgres -D /opt/homebrew/var/postgresql@17"),
			observed(12700, 12679, "postgres: checkpointer"),
			observed(12701, 12679, "postgres: walwriter"),
		}
		origins := map[string]groups.Origin{"12679": {Owner: "acme", Scope: groups.ScopeUser, Source: groups.SourceBrewService, Service: "postgresql@17"}}

		classified := groups.Classify(listing, groups.ClassifyOptions{Catalog: catalog, Origins: origins})

		Expect(shapes(classified)).To(Equal([]shape{{Key: "12679", Name: "postgresql@17", PIDs: []int{12679, 12700, 12701}}}))
		Expect(classified[0].Rule.Kind).To(Equal(groups.KindService))
		Expect(classified[0].Origin.Source).To(Equal(groups.SourceBrewService))
	})

	It("groups the processes of each app bundle in an Applications folder under applications, its helpers and the processes it runs with it", func() {
		listing := []process.Observed{
			observed(724, 1, "/Applications/Acme Chat.app/Contents/MacOS/Acme Chat"),
			observed(730, 724, "/Applications/Acme Chat.app/Contents/Frameworks/Acme Chat Helper (Renderer).app/Contents/MacOS/Acme Chat Helper (Renderer) --type=renderer"),
			observed(731, 724, "/bin/sh -c open"),
			observed(812, 1, "/Applications/Acme Chat.app/Contents/Frameworks/Acme Chat Helper.app/Contents/MacOS/Acme Chat Helper --type=utility"),
			observed(900, 1, "/System/Applications/Calendar.app/Contents/MacOS/Calendar"),
			observed(950, 1, "/Users/acme/Applications/Setapp/Paste.app/Contents/MacOS/Paste"),
			observed(960, 950, "/Applications/Acme Notes.app/Contents/MacOS/Acme Notes"),
		}

		classified := groups.Classify(listing, groups.ClassifyOptions{Catalog: catalog})

		Expect(shapes(classified)).To(Equal([]shape{
			{Key: "apps", Name: "applications", PIDs: []int{}},
			{Key: "app:/Applications/Acme Chat.app", Parent: "apps", Name: "Acme Chat", PIDs: []int{724, 730, 731, 812}},
			{Key: "app:/System/Applications/Calendar.app", Parent: "apps", Name: "Calendar", PIDs: []int{900}},
			{Key: "app:/Users/acme/Applications/Setapp/Paste.app", Parent: "apps", Name: "Paste", PIDs: []int{950}},
			{Key: "app:/Applications/Acme Notes.app", Parent: "app:/Users/acme/Applications/Setapp/Paste.app", Name: "Acme Notes", PIDs: []int{960}},
		}))
		Expect(classified[1].Rule.Kind).To(Equal(groups.KindApp))
	})

	It("groups the processes no tool holds under their scope by program, users' and the system's apart", func() {
		listing := []process.Observed{
			observed(300, 1, "/usr/libexec/logd"),
			observed(301, 1, "/usr/sbin/mDNSResponder"),
			observed(400, 1, "/usr/libexec/logd --user"),
			observed(401, 1, "node /srv/app.js"),
			observed(402, 1, "node /srv/worker.js"),
		}
		origins := map[string]groups.Origin{
			"300": {Owner: "root", Scope: groups.ScopeSystem, Source: groups.SourceApple},
			"301": {Owner: "_mdnsresponder", Scope: groups.ScopeSystem, Source: groups.SourceApple},
			"400": {Owner: "acme", Scope: groups.ScopeUser},
			"401": {Owner: "acme", Scope: groups.ScopeUser},
			"402": {Owner: "acme", Scope: groups.ScopeUser},
		}

		Expect(shapes(groups.Classify(listing, groups.ClassifyOptions{Catalog: catalog, Origins: origins}))).To(Equal([]shape{
			{Key: "other:system", Name: "system", PIDs: []int{}},
			{Key: "other:system:logd", Parent: "other:system", Name: "logd", PIDs: []int{300}},
			{Key: "other:system:mDNSResponder", Parent: "other:system", Name: "mDNSResponder", PIDs: []int{301}},
			{Key: "other:user", Name: "other", PIDs: []int{}},
			{Key: "other:user:logd", Parent: "other:user", Name: "logd", PIDs: []int{400}},
			{Key: "other:user:node", Parent: "other:user", Name: "node", PIDs: []int{401, 402}},
		}))
	})

	It("groups a codex wrapper with its native binary, leaving its app's server to the app", func() {
		listing := []process.Observed{
			observed(1000, 1, "node /pnpm/global/node_modules/@openai/codex/bin/codex.js --enable hooks"),
			observed(1001, 1000, "/pnpm/store/@openai/codex/vendor/aarch64-apple-darwin/codex/codex --enable hooks"),
			observed(1002, 1, "/Applications/Codex.app/Contents/Resources/codex app-server"),
		}

		Expect(shapes(groups.Classify(listing, groups.ClassifyOptions{Catalog: catalog}))).To(Equal([]shape{
			{Key: "1000", Name: "codex", PIDs: []int{1000, 1001}},
			{Key: "apps", Name: "applications", PIDs: []int{}},
			{Key: "app:/Applications/Codex.app", Parent: "apps", Name: "Codex", PIDs: []int{1002}},
		}))
	})

	It("groups the telltale reading the host with what it runs, under the group it runs in", func() {
		listing := []process.Observed{
			observed(900, 1, "/Users/acme/.local/bin/claude --session-id abc-123"),
			observed(910, 900, "/bin/zsh -c telltale top"),
			observed(911, 910, ".bin/telltale top"),
			observed(912, 911, "nettop -P -L 0 -J bytes_in,bytes_out"),
		}
		self := 911

		classified := groups.Classify(listing, groups.ClassifyOptions{Catalog: catalog, Self: self})

		Expect(shapes(classified)).To(Equal([]shape{
			{Key: "900", Name: "claude", Target: "abc-123", PIDs: []int{900, 910}},
			{Key: "911", Parent: "900", Name: groups.SelfGroup, PIDs: []int{911, 912}},
		}))
		Expect(classified[1].Rule.Kind).To(Equal(groups.KindTool))
	})

	It("starts a new group for a process whose parent is not listed", func() {
		listing := []process.Observed{
			observed(2000, 1999, "go test ./a"),
			observed(2001, 2000, "/tmp/b001/a.test"),
			observed(2002, 1999, "/tmp/b002/b.test"),
		}

		Expect(shapes(groups.Classify(listing, groups.ClassifyOptions{Catalog: catalog}))).To(Equal([]shape{
			{Key: "2000", Name: "go-test", Target: "./a", PIDs: []int{2000, 2001}},
			{Key: "2002", Name: "go-test", PIDs: []int{2002}},
		}))
	})
})
