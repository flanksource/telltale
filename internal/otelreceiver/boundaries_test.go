package otelreceiver

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var _ = Describe("receiver boundaries", func() {
	It("enforces API scope and revocation in both transports", func() {
		receiver, store := testReceiver(DefaultOptions())
		tokens := agentclient.NewTokenStore(store.options.WorkDir)
		api, err := tokens.Mint("exporter", apitoken.ScopeAPI)
		Expect(err).NotTo(HaveOccurred())
		agent, err := tokens.Mint("agent", apitoken.ScopeAgent)
		Expect(err).NotTo(HaveOccurred())
		connection, err := grpc.NewClient(serveGRPC(receiver, true), grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(connection.Close)
		client := ptraceotlp.NewGRPCClient(connection)
		body, err := exampleRequest().MarshalJSON()
		Expect(err).NotTo(HaveOccurred())
		for _, test := range []struct {
			token string
			http  int
			grpc  codes.Code
		}{
			{"", http.StatusUnauthorized, codes.Unauthenticated},
			{agent.Secret.Value(), http.StatusForbidden, codes.PermissionDenied},
			{api.Secret.Value(), http.StatusOK, codes.OK},
		} {
			post := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(body))
			post.Header.Set("Content-Type", jsonType)
			post.Header.Set("Authorization", "Bearer "+test.token)
			response := httptest.NewRecorder()
			receiver.HTTPHandler(true).ServeHTTP(response, post)
			Expect(response.Code).To(Equal(test.http), response.Body.String())
			ctx, cancel := context.WithTimeout(metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+test.token), 5*time.Second)
			_, err := client.Export(ctx, exampleRequest())
			cancel()
			Expect(status.Code(err)).To(Equal(test.grpc))
		}
		record, err := tokens.Lookup(context.Background(), api.ID)
		Expect(err).NotTo(HaveOccurred())
		now := time.Now()
		record.RevokedAt = &now
		encoded, err := json.Marshal(record)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(store.options.WorkDir, ".telltale", "agent-tokens", api.ID+".json"), encoded, 0600)).To(Succeed())
		ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+api.Secret.Value())
		_, err = client.Export(ctx, exampleRequest())
		Expect(status.Code(err)).To(Equal(codes.Unauthenticated))
		Expect(status.Code(receiver.authorize(ctx, "Bearer "+api.Secret.Value()))).To(Equal(codes.Unauthenticated))
	})
	It("bounds compressed input and span count without writing any rows", func() {
		options := DefaultOptions()
		options.MaxRequestBytes = 1024
		options.MaxSpans = 1
		receiver, store := testReceiver(options)
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		_, err := writer.Write([]byte(strings.Repeat(" ", options.MaxRequestBytes+1)))
		Expect(err).NotTo(HaveOccurred())
		Expect(writer.Close()).To(Succeed())
		post := httptest.NewRequest(http.MethodPost, "/v1/traces", &compressed)
		post.Header.Set("Content-Type", jsonType)
		post.Header.Set("Content-Encoding", "gzip")
		response := httptest.NewRecorder()
		receiver.HTTPHandler(false).ServeHTTP(response, post)
		Expect(response.Code).To(Equal(http.StatusRequestEntityTooLarge), response.Body.String())
		request := exampleRequest()
		request.Traces().ResourceSpans().At(0).ScopeSpans().At(0).Spans().AppendEmpty()
		_, err = receiver.Export(context.Background(), request)
		Expect(status.Code(err)).To(Equal(codes.ResourceExhausted))
		spans, err := store.Read(context.Background(), ReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(spans).To(BeEmpty())
	})
	It("returns retryable overload and drains admission before stopping", func() {
		options := DefaultOptions()
		options.MaxInflight = 1
		receiver, _ := testReceiver(options)
		Expect(receiver.acquire()).To(Succeed())
		post := httptest.NewRequest(http.MethodPost, "/v1/traces", strings.NewReader("{}"))
		post.Header.Set("Content-Type", jsonType)
		response := httptest.NewRecorder()
		receiver.HTTPHandler(false).ServeHTTP(response, post)
		Expect(response.Code).To(Equal(http.StatusServiceUnavailable))
		Expect(response.Header().Get("Retry-After")).To(Equal("1"))
		_, err := receiver.Export(context.Background(), ptraceotlp.NewExportRequest())
		Expect(status.Code(err)).To(Equal(codes.Unavailable))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(receiver.Stop(ctx)).To(MatchError(context.Canceled))
		receiver.release()
		Expect(receiver.Stop(context.Background())).To(Succeed())
		Expect(status.Code(receiver.acquire())).To(Equal(codes.Unavailable))
	})
})
