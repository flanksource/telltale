package telltale

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type panicBlobSink struct{ failure chan error }

func (s panicBlobSink) TryEmit(any) bool                       { return true }
func (s panicBlobSink) Blob(string, string, []byte, any) error { panic("blob broke") }
func (s panicBlobSink) Report(any)                             {}
func (s panicBlobSink) Warn(string)                            {}
func (s panicBlobSink) Fail(err error)                         { s.failure <- err }

var _ = Describe("Go profile probe", func() {
	It("recovers a CPU sender panic and releases the profile", func() {
		sink := panicBlobSink{failure: make(chan error, 1)}
		disarm, err := (goProfileProbe{}).Arm(context.Background(), json.RawMessage(`{"profile":"cpu","seconds":1}`), sink)
		Expect(err).NotTo(HaveOccurred())
		var failure error
		Eventually(sink.failure).WithTimeout(3 * time.Second).Should(Receive(&failure))
		Expect(errors.Is(failure, ErrProbePanic)).To(BeTrue())
		Expect(errors.Is(disarm(), ErrProbePanic)).To(BeTrue())
	})
	It("keeps block profiling armed until the last session releases it", func() {
		first, err := (goProfileProbe{}).Arm(context.Background(), json.RawMessage(`{"profile":"block"}`), nil)
		Expect(err).NotTo(HaveOccurred())
		second, err := (goProfileProbe{}).Arm(context.Background(), json.RawMessage(`{"profile":"block"}`), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(first()).To(Succeed())
		Expect(blockSessions).To(Equal(1))
		Expect(second()).To(Succeed())
		Expect(blockSessions).To(Equal(0))
	})

	It("restores the previous mutex profile rate", func() {
		original := runtime.SetMutexProfileFraction(7)
		defer runtime.SetMutexProfileFraction(original)
		disarm, err := (goProfileProbe{}).Arm(context.Background(), json.RawMessage(`{"profile":"mutex"}`), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(runtime.SetMutexProfileFraction(-1)).To(Equal(1))
		Expect(disarm()).To(Succeed())
		Expect(runtime.SetMutexProfileFraction(-1)).To(Equal(7))
	})
})
