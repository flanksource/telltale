package groups_test

import (
	"os"

	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ParseLaunchctlPrint", func() {
	It("reads the running services of a domain by pid, leaving out those not running and the disabled list", func() {
		file, err := os.Open("testdata/launchctl-print-gui.txt")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(file.Close)

		Expect(groups.ParseLaunchctlPrint(file, groups.DomainUser)).To(Equal(groups.Jobs{
			3613:  {Label: "application.com.example.Launcher.1583758.95743951", Domain: groups.DomainUser},
			59483: {Label: "com.apple.syncdefaultsd", Domain: groups.DomainUser},
			12679: {Label: "homebrew.mxcl.postgresql@17", Domain: groups.DomainUser},
			3432:  {Label: "com.acme.HealthMonitor", Domain: groups.DomainUser},
		}))
	})
})

var _ = Describe("OriginOf", func() {
	jobs := groups.Jobs{
		12679: {Label: "homebrew.mxcl.postgresql@17", Domain: groups.DomainUser},
		3613:  {Label: "application.com.example.Launcher.1583758.95743951", Domain: groups.DomainUser},
		437:   {Label: "com.apple.runningboardd", Domain: groups.DomainSystem},
		3432:  {Label: "com.acme.HealthMonitor", Domain: groups.DomainUser},
	}
	brew := []string{"/opt/homebrew"}
	uids := map[string]int{"acme": 501, "root": 0, "_corespeechd": 262}
	process := func(pid, ppid int, user, command string) clickyprocess.Process {
		return clickyprocess.Process{PID: pid, PPID: ppid, UID: uids[user], User: user, Command: command}
	}

	DescribeTable("names who runs a process and what started it",
		func(p clickyprocess.Process, expected groups.Origin) {
			Expect(groups.OriginOf(p, jobs, brew)).To(Equal(expected))
		},
		Entry("a brew service", process(12679, 1, "acme", "/opt/homebrew/opt/postgresql@17/bin/postgres -D /opt/homebrew/var/postgresql@17"),
			groups.Origin{Owner: "acme", Scope: groups.ScopeUser, Source: groups.SourceBrewService, Domain: groups.DomainUser, Label: "homebrew.mxcl.postgresql@17", Service: "postgresql@17"}),
		Entry("an app launchd started", process(3613, 1, "acme", "/Applications/Launcher.app/Contents/MacOS/Launcher"),
			groups.Origin{Owner: "acme", Scope: groups.ScopeUser, Source: groups.SourceApp, Domain: groups.DomainUser, Label: "application.com.example.Launcher.1583758.95743951", Service: "com.example.Launcher"}),
		Entry("an Apple system daemon", process(437, 1, "root", "/usr/libexec/runningboardd"),
			groups.Origin{Owner: "root", Scope: groups.ScopeSystem, Source: groups.SourceApple, Domain: groups.DomainSystem, Label: "com.apple.runningboardd", Service: "com.apple.runningboardd"}),
		Entry("a third-party agent", process(3432, 1, "acme", "/Applications/Acme.app/Contents/MacOS/HealthMonitor"),
			groups.Origin{Owner: "acme", Scope: groups.ScopeUser, Source: groups.SourceLaunchd, Domain: groups.DomainUser, Label: "com.acme.HealthMonitor", Service: "com.acme.HealthMonitor"}),
		Entry("a role account's Apple daemon launchd hides", process(377, 1, "_corespeechd", "/System/Library/PrivateFrameworks/CoreSpeech.framework/corespeechd_system"),
			groups.Origin{Owner: "_corespeechd", Scope: groups.ScopeSystem, Source: groups.SourceApple}),
		Entry("launchd itself", process(1, 0, "root", "/sbin/launchd"),
			groups.Origin{Owner: "root", Scope: groups.ScopeSystem, Source: groups.SourceApple}),
		Entry("a brew program a shell runs", process(5000, 4999, "acme", "/opt/homebrew/bin/jq ."),
			groups.Origin{Owner: "acme", Scope: groups.ScopeUser, Source: groups.SourceBrew}),
		Entry("a program a shell runs", process(5001, 4999, "acme", "/bin/zsh -l"),
			groups.Origin{Owner: "acme", Scope: groups.ScopeUser}),
	)
})
