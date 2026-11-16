package snapshot

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSnapshot(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "JVM snapshot") }

var _ = Describe("support archive", func() {
	It("retains evidence and masks secrets when a collector fails", func() {
		result, err := collect(context.Background(), Options{}, func(_ context.Context, op string) (json.RawMessage, error) {
			if op == "jvm.threads.dump" {
				return nil, errors.New("thread dump denied")
			}
			return json.RawMessage(`{"password":"snapshot-private-value","java.version":"17"}`), nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Manifest.Complete).To(BeFalse())
		data, err := result.Zip()
		Expect(err).NotTo(HaveOccurred())
		archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		Expect(err).NotTo(HaveOccurred())
		Expect(archive.File).NotTo(BeEmpty())
		for _, file := range archive.File {
			stream, err := file.Open()
			Expect(err).NotTo(HaveOccurred())
			content, err := io.ReadAll(stream)
			Expect(err).NotTo(HaveOccurred())
			Expect(stream.Close()).To(Succeed())
			for _, collector := range result.Manifest.Collectors {
				if collector.File == file.Name {
					Expect(collector.SHA256).To(Equal(fmt.Sprintf("%x", sha256.Sum256(content))))
				}
			}
		}
		for _, content := range result.Files {
			Expect(string(content)).NotTo(ContainSubstring("snapshot-private-value"))
		}
		Expect(string(result.Files["summary.md"])).To(ContainSubstring("thread dump denied"))
	})
	It("rejects invalid recording durations", func() {
		Expect((Options{JFR: "-1s"}).Validate()).NotTo(Succeed())
		Expect((Options{JFR: "bogus"}).Validate()).NotTo(Succeed())
	})
	It("masks quoted assignments and embedded URLs without losing integer precision", func() {
		data, err := sanitized([]byte(`{"arguments":["-Dexample.password=\"private with spaces\""],"environment":{"JAVA_TOOL_OPTIONS":"-Dauth.token=private-token"},"url":"https://user:private-pass@example.com/path","counter":9007199254740993}`), false)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).NotTo(ContainSubstring("private"))
		Expect(string(data)).To(ContainSubstring("9007199254740993"))
	})
	It("does not invoke collectors after cancellation", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		called := false
		result, err := collect(ctx, Options{HeapHistogram: true}, func(context.Context, string) (json.RawMessage, error) { called = true; return nil, nil })
		Expect(err).NotTo(HaveOccurred())
		Expect(called).To(BeFalse())
		Expect(result.Manifest.Complete).To(BeFalse())
	})
	It("masks JMX tabular system-property values", func() {
		data, err := sanitized([]byte(`{"SystemProperties":[{"key":"example.password","value":"private-value"}]}`), false)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).NotTo(ContainSubstring("private-value"))
	})
})
