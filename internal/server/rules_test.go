package server

import (
	"io"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type disconnectedRulesWriter struct {
	header   http.Header
	statuses []int
	writes   int
}

func (w *disconnectedRulesWriter) Header() http.Header { return w.header }
func (w *disconnectedRulesWriter) WriteHeader(status int) {
	w.statuses = append(w.statuses, status)
}
func (w *disconnectedRulesWriter) Write([]byte) (int, error) {
	if w.writes == 0 {
		w.WriteHeader(http.StatusOK)
	}
	w.writes++
	return 0, io.ErrClosedPipe
}

var _ = Describe("resource rule JSON responses", func() {
	It("does not attempt a second response after the client disconnects", func() {
		writer := &disconnectedRulesWriter{header: http.Header{}}
		writeRulesJSON(writer, map[string]bool{"enabled": true})
		Expect(writer.statuses).To(Equal([]int{http.StatusOK}))
		Expect(writer.writes).To(Equal(1))
	})
	It("reports serialization errors before writing the response body", func() {
		writer := httptest.NewRecorder()
		writeRulesJSON(writer, func() {})
		Expect(writer.Code).To(Equal(http.StatusInternalServerError))
		Expect(writer.Body.String()).To(ContainSubstring("unsupported type"))
	})
})
