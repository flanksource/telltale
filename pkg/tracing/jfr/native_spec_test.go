package jfr_test

import (
	"context"
	"github.com/flanksource/telltale/pkg/tracing/jfr"
	"github.com/flanksource/telltale/pkg/tracing/jfr/decode"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os/exec"
	"path/filepath"
	"time"
)

var _ = Describe("native JVM recording", func() {
	It("starts, dumps and stops an owned recording on a real JVM", func() {
		dir := GinkgoT().TempDir()
		output, err := exec.Command("javac", "-d", dir, "testdata/gen/Workload.java").CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		child := exec.Command("java", "-cp", dir, "jfrfixture.Workload", "60000")
		Expect(child.Start()).To(Succeed())
		DeferCleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		recorder := jfr.NativeRecorder{Dir: filepath.Join(dir, "agent captures")}
		_, err = recorder.Start(ctx, jfr.RecordOptions{PID: child.Process.Pid, Engine: jfr.EngineJFR, Settings: "missing-fixture-settings"}, "telltale-invalid-fixture")
		Expect(err).To(HaveOccurred(), "jcmd must report an unavailable recording configuration")
		recording, err := recorder.Start(ctx, jfr.RecordOptions{PID: child.Process.Pid, Engine: jfr.EngineJFR, Settings: "profile"}, "telltale-native-fixture")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			stop, cancelStop := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancelStop()
			Expect(recording.Stop(stop)).To(Succeed())
		})
		time.Sleep(time.Second)
		data, err := recording.Dump(ctx)
		Expect(err).NotTo(HaveOccurred())
		profile, err := decode.Decode(data, decode.DecodeOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(profile.Samples).NotTo(BeEmpty())
	})
})
