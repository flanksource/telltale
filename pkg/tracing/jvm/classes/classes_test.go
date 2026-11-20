package classes_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/flanksource/telltale/pkg/tracing/jvm/classes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestClasses(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "JVM class operation results") }

const (
	idleLoader = "18a70f16"
	appLoader  = "14dad5dc"

	agentClasses = `{"total":679,"offset":0,"limit":3,"platformExcluded":3549,"classes":[{"name":"Idle","loader":"18a70f16"},{"name":"com.flanksource.telltale.Agent","loader":"14dad5dc"},{"name":"com.flanksource.telltale.Agent$$Lambda/0x00000e00011c9f40","loader":"14dad5dc"}]}`

	agentLoaders = `[{"hash":"14dad5dc","className":"jdk.internal.loader.ClassLoaders$AppClassLoader","label":"JVM classpath + java agents","description":"com.sun, Jackson, com.flanksource, telltale-diag","kind":"container","classCount":1937},{"hash":"18a70f16","className":"com.sun.tools.javac.launcher.MemoryClassLoader","label":"MemoryClassLoader","description":"Idle","kind":"unknown","classCount":1},{"hash":"0","className":"","label":"JDK bootstrap","description":"java.lang, java.util, jdk.internal, sun.nio","kind":"jdk","classCount":2241},{"hash":"~compiled","className":"jdk.internal.reflect.DelegatingClassLoader","label":"Reflection accessors (12 loaders)","description":"jdk.internal.reflect","kind":"jdk","classCount":12,"virtual":true,"loaderCount":12}]`

	idleSource = "public class Idle {\n    static void check(long l) {\n        if (l < 0L) {\n            throw new IllegalStateException(\"negative \" + l);\n        }\n    }\n}\n"

	agentSource = `{"class":"Idle","loader":"18a70f16","source":"public class Idle {\n    static void check(long l) {\n        if (l < 0L) {\n            throw new IllegalStateException(\"negative \" + l);\n        }\n    }\n}\n","methods":[{"name":"check","descriptor":"(J)V","line":3,"endLine":4}],"sourceLineNumbers":[0,0,14,14,0,0,0,0],"lineRows":{"14":3},"digest":"sha256:8ee7b08b76749dc3475e5bcad8a829a7f261d1d70d9abc5d4b7945e6356d84d2","decompiler":"cfr-0.152","durationMs":147,"cached":false}`

	// The first 12 bytes of Idle's live class file: magic, version 0.69 (Java 25), then the constant pool count.
	agentBytecode = `{"bytecode":"yv66vgAAAEUARQoA"}`

	agentCompiled     = `{"success":true,"classes":["Idle"],"diagnostics":[],"classpathEntries":2}`
	agentCompileError = `{"success":false,"classes":[],"diagnostics":[{"kind":"ERROR","line":3,"column":57,"code":"compiler.err.expected","message":"';' expected"},{"kind":"ERROR","line":-1,"column":-1,"code":"compiler.err.cant.resolve","message":"in the generated stand-in for Idle: cannot find symbol"}],"classpathEntries":2}`

	originalDigest = "sha256:8ee7b08b76749dc3475e5bcad8a829a7f261d1d70d9abc5d4b7945e6356d84d2"
	patchedDigest  = "sha256:1c0e9b85c1656afd46b860a0e2bcfb1ae676eed8993f49fcc861862203f0c963"
	leaseExpiresAt = int64(1794804977512)
	leaseUntil     = "2026-11-16T04:56:17Z"

	agentPatched       = `{"success":true,"classes":["Idle"],"diagnostics":[],"classpathEntries":2,"method":"(J)V","class":"Idle","loader":"18a70f16","digest":"` + patchedDigest + `","originalDigest":"` + originalDigest + `","handedDigest":"` + patchedDigest + `","patched":true,"leaseExpiresAt":1794804977512,"applied":true}`
	agentPatchRejected = `{"success":false,"classes":[],"diagnostics":[{"kind":"ERROR","line":3,"column":57,"code":"compiler.err.expected","message":"';' expected"}],"classpathEntries":2,"method":"(J)V","applied":false}`

	agentLease    = `{"class":"Idle","loader":"18a70f16","patched":true,"leaseExpiresAt":1794804977512}`
	agentRestored = `{"class":"Idle","loader":"18a70f16","patched":false,"restored":["Idle"],"digest":"` + originalDigest + `","probesClosed":[{"key":"agentops-args","closed":true,"generation":1794804880129,"writeSeq":144,"capacity":4096}]}`
	agentReleased = `{"class":"Idle","loader":"18a70f16","digest":"` + patchedDigest + `","originalDigest":"` + originalDigest + `","patched":true,"leaseExpiresAt":1794804977512,"restored":[],"released":"agentops-args","heldBy":["agentops-fixture"],"probesClosed":[]}`

	agentPatches = `[{"class":"Idle","loader":"18a70f16","digest":"` + patchedDigest + `","originalDigest":"` + originalDigest + `","patched":true,"leaseExpiresAt":1794804977512},{"class":"example.Gone","loader":"5e1f00d","patched":false,"error":"no longer loaded: class not loaded by loader 5e1f00d: example.Gone"}]`
)

func decode(op, raw string) any {
	GinkgoHelper()
	value, err := classes.Results[op](json.RawMessage(raw))
	Expect(err).NotTo(HaveOccurred())
	return value
}

func lines(text interface{ String() string }) []string {
	return strings.Split(strings.TrimRight(text.String(), "\n"), "\n")
}

var _ = Describe("class operation results", func() {
	It("registers a decoder for every class operation", func() {
		Expect(classes.Results).To(HaveLen(9))
		for _, op := range []string{classes.ClassesOp, classes.LoadersOp, classes.SourceOp, classes.BytecodeOp, classes.CompileOp, classes.PatchOp, classes.RestoreOp, classes.LeaseOp, classes.PatchesOp} {
			Expect(classes.Results).To(HaveKey(op))
		}
	})

	It("refuses what is not the operation's result rather than reading it as empty", func() {
		refused := map[string][]string{
			classes.ClassesOp:  {`{}`, `[]`, `{"total":1,"offset":0,"limit":3,"platformExcluded":0,"classes":null}`},
			classes.LoadersOp:  {`{}`, `null`, `[{"label":"no hash"}]`},
			classes.SourceOp:   {`{}`, `{"class":"Idle","loader":"18a70f16","source":"","decompiler":"cfr-0.152"}`},
			classes.BytecodeOp: {`{}`, `{"bytecode":"AAAA"}`, `{"bytecode":"not base64"}`},
			classes.CompileOp:  {`{}`, `{"success":true}`},
			classes.PatchOp:    {`{}`, `{"success":true,"classes":[],"diagnostics":[],"applied":true}`},
			classes.RestoreOp:  {`{}`, `{"class":"Idle","loader":"18a70f16","patched":false}`},
			classes.LeaseOp:    {`{}`, `{"class":"Idle","loader":"18a70f16"}`},
			classes.PatchesOp:  {`{}`, `null`, `[{"patched":true}]`},
		}
		for op, raws := range refused {
			for _, raw := range raws {
				_, err := classes.Results[op](json.RawMessage(raw))
				Expect(err).To(MatchError(ContainSubstring(op)), op+" "+raw)
			}
		}
	})

	It("pages loaded classes under a heading that says what the page leaves out", func() {
		page := decode(classes.ClassesOp, agentClasses).(classes.ClassPage)
		Expect(page).To(Equal(classes.ClassPage{Total: 679, Limit: 3, PlatformExcluded: 3549, Classes: []classes.Class{
			{Name: "Idle", Loader: idleLoader},
			{Name: "com.flanksource.telltale.Agent", Loader: appLoader},
			{Name: "com.flanksource.telltale.Agent$$Lambda/0x00000e00011c9f40", Loader: appLoader},
		}}))
		Expect(lines(page.Pretty())[0]).To(Equal("Classes 1-3 of 679 · 3549 platform classes hidden"))
		Expect(page.Classes[0].Row()).To(Equal(map[string]any{"name": "Idle", "loader": idleLoader}))
	})

	It("tabulates loaders, keeping a merged family's loader count", func() {
		loaders := decode(classes.LoadersOp, agentLoaders).(classes.Loaders)
		Expect(loaders).To(HaveLen(4))
		Expect(loaders[3]).To(Equal(classes.Loader{Hash: "~compiled", ClassName: "jdk.internal.reflect.DelegatingClassLoader", Label: "Reflection accessors (12 loaders)", Description: "jdk.internal.reflect", Kind: "jdk", ClassCount: 12, Virtual: true, LoaderCount: 12}))
		Expect(lines(loaders.Pretty())[0]).To(Equal("Class loaders 4 loaders · 4191 classes"))
		Expect(loaders[1].Row()).To(Equal(map[string]any{"hash": idleLoader, "label": "MemoryClassLoader", "kind": "unknown", "classCount": 1, "className": "com.sun.tools.javac.launcher.MemoryClassLoader", "description": "Idle"}))
	})

	It("prints decompiled source under one header line", func() {
		source := decode(classes.SourceOp, agentSource).(classes.Source)
		Expect(source).To(Equal(classes.Source{
			Class: "Idle", Loader: idleLoader, Source: idleSource,
			Methods:           []classes.SourceMethod{{Name: "check", Descriptor: "(J)V", Line: 3, EndLine: 4}},
			SourceLineNumbers: []int{0, 0, 14, 14, 0, 0, 0, 0}, LineRows: map[int]int{14: 3},
			Digest: originalDigest, Decompiler: "cfr-0.152", DurationMs: 147,
		}))
		Expect(source.Pretty().String()).To(Equal("Idle · loader 18a70f16 · cfr-0.152 · 147ms · 1 method\n" + idleSource))
	})

	It("dumps bytecode in hex under its class file version", func() {
		bytecode := decode(classes.BytecodeOp, agentBytecode).(classes.Bytecode)
		Expect(bytecode.Bytecode).To(Equal([]byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 0x45, 0, 0x45, 0x0a, 0}))
		Expect(lines(bytecode.Pretty())).To(Equal([]string{
			"Bytecode 12B · class file 69.0 (Java 25)",
			"00000000  ca fe ba be 00 00 00 45  00 45 0a 00              |.......E.E..|",
		}))
	})

	It("acknowledges a clean compile in one line and lists each diagnostic on its own", func() {
		compiled := decode(classes.CompileOp, agentCompiled).(classes.Compile)
		Expect(compiled).To(Equal(classes.Compile{Success: true, Classes: []string{"Idle"}, Diagnostics: []classes.Diagnostic{}, ClasspathEntries: 2}))
		Expect(compiled.Pretty().String()).To(Equal("✓ compiled Idle · 2 classpath entries"))

		failed := decode(classes.CompileOp, agentCompileError).(classes.Compile)
		Expect(lines(failed.Pretty())).To(Equal([]string{
			"✗ compile failed · 2 errors · 2 classpath entries",
			"ERROR 3:57 ';' expected (compiler.err.expected)",
			"ERROR in the generated stand-in for Idle: cannot find symbol (compiler.err.cant.resolve)",
		}))
	})

	It("acknowledges an applied patch with its method, loader and lease in one line", func() {
		patch := decode(classes.PatchOp, agentPatched).(classes.Patch)
		Expect(patch).To(Equal(classes.Patch{
			Compile: classes.Compile{Success: true, Classes: []string{"Idle"}, Diagnostics: []classes.Diagnostic{}, ClasspathEntries: 2},
			Method:  "(J)V", Applied: true,
			PatchStatus: classes.PatchStatus{Class: "Idle", Loader: idleLoader, Digest: patchedDigest, OriginalDigest: originalDigest, HandedDigest: patchedDigest, Patched: true, LeaseExpiresAt: leaseExpiresAt},
		}))
		Expect(patch.Pretty().String()).To(Equal("✓ patched Idle (J)V · loader 18a70f16 · lease until " + leaseUntil + " · sha256:1c0e9b85"))

		rejected := decode(classes.PatchOp, agentPatchRejected).(classes.Patch)
		Expect(lines(rejected.Pretty())).To(Equal([]string{
			"✗ not patched (J)V: compile failed · 1 error · 2 classpath entries",
			"ERROR 3:57 ';' expected (compiler.err.expected)",
		}))
	})

	It("reports a failed compile or a patch that was not applied as a failure, so the command exits non-zero", func() {
		failures := func(results ...interface{ Failure() error }) []string {
			var messages []string
			for _, result := range results {
				if err := result.Failure(); err != nil {
					messages = append(messages, err.Error())
				}
			}
			return messages
		}
		Expect(failures(
			decode(classes.CompileOp, agentCompiled).(classes.Compile),
			decode(classes.CompileOp, agentCompileError).(classes.Compile),
			decode(classes.PatchOp, agentPatched).(classes.Patch),
			decode(classes.PatchOp, agentPatchRejected).(classes.Patch),
			classes.Patch{Applied: true, PatchStatus: classes.PatchStatus{Class: "Idle", Error: "the patched bytes are identical to the loaded class"}},
		)).To(Equal([]string{
			"compile failed with 2 errors",
			"(J)V was not patched: compile failed with 1 error",
			"Idle: the patched bytes are identical to the loaded class",
		}))
	})

	It("acknowledges a lease renewal in one line", func() {
		lease := decode(classes.LeaseOp, agentLease).(classes.Lease)
		Expect(lease).To(Equal(classes.Lease{Class: "Idle", Loader: idleLoader, Patched: true, LeaseExpiresAt: leaseExpiresAt}))
		Expect(lease.Pretty().String()).To(Equal("✓ Idle lease until " + leaseUntil + " · loader 18a70f16"))
	})

	It("acknowledges a restore, or says which probes still hold the patch", func() {
		restored := decode(classes.RestoreOp, agentRestored).(classes.Restore)
		Expect(restored).To(Equal(classes.Restore{
			PatchStatus:  classes.PatchStatus{Class: "Idle", Loader: idleLoader, Digest: originalDigest},
			Restored:     []string{"Idle"},
			ProbesClosed: []map[string]any{{"key": "agentops-args", "closed": true, "generation": float64(1794804880129), "writeSeq": float64(144), "capacity": float64(4096)}},
		}))
		Expect(restored.Pretty().String()).To(Equal("✓ restored Idle · loader 18a70f16 · closed probe agentops-args"))

		held := decode(classes.RestoreOp, agentReleased).(classes.Restore)
		Expect(held.HeldBy).To(Equal([]string{"agentops-fixture"}))
		Expect(held.Pretty().String()).To(Equal("✓ released probe agentops-args · Idle still patched, held by agentops-fixture"))
	})

	It("tabulates patches, or says there are none", func() {
		patches := decode(classes.PatchesOp, agentPatches).(classes.Patches)
		Expect(patches[1]).To(Equal(classes.PatchStatus{Class: "example.Gone", Loader: "5e1f00d", Error: "no longer loaded: class not loaded by loader 5e1f00d: example.Gone"}))
		Expect(lines(patches.Pretty())[0]).To(Equal("Patches 2 classes · 1 patched"))
		Expect(classes.PatchRow(patches[0]).Row()).To(Equal(map[string]any{"class": "Idle", "loader": idleLoader, "patched": "patched", "lease": leaseUntil, "digest": "sha256:1c0e9b85", "error": ""}))

		none := decode(classes.PatchesOp, `[]`).(classes.Patches)
		Expect(none.Pretty().String()).To(Equal("no active patches"))
	})
})
