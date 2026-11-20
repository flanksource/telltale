package otelreceiver

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/credentials/apitoken"
	"github.com/flanksource/telltale/pkg/datadir"
	"github.com/flanksource/telltale/pkg/otlp/profiles"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/collector/pdata/pprofile/pprofileotlp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func javaTopRequest(name string) pprofileotlp.ExportRequest {
	GinkgoHelper()
	data, err := os.ReadFile("../../pkg/otlp/profiles/testdata/java-http-top-" + name + ".pb")
	Expect(err).NotTo(HaveOccurred())
	request := pprofileotlp.NewExportRequest()
	Expect(request.UnmarshalProto(data)).To(Succeed())
	return request
}

var _ = Describe("Profiles protocol boundaries", func() {
	It("reports invalid operation attributes as a permanent input error", func() {
		receiver, store := testReceiver(DefaultOptions())
		snapshot := topSnapshot(1)
		delete(snapshot.Rows[0].Attributes, "startedAt")
		data, err := profiles.Encode([]profiles.Snapshot{snapshot})
		Expect(err).NotTo(HaveOccurred())
		request := pprofileotlp.NewExportRequest()
		Expect(request.UnmarshalProto(data)).To(Succeed())
		_, err = receiver.ingestProfiles(context.Background(), request)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		stored, err := store.ReadProfiles(context.Background(), ProfileReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(BeEmpty())
	})
	DescribeTable("accepts Java active, updated and empty profiles using authenticated protobuf, JSON and gRPC", func(format string) {
		receiver, store := testReceiver(DefaultOptions())
		tokens := agentclient.NewTokenStore([]datadir.ConfigDir{{Name: datadir.ConfigGlobal, Dir: store.options.DataDir}})
		api, err := tokens.Mint("profile-exporter", apitoken.ScopeAPI)
		Expect(err).NotTo(HaveOccurred())
		connection, err := grpc.NewClient(serveGRPC(receiver, true), grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(connection.Close)
		client := pprofileotlp.NewGRPCClient(connection)
		for _, name := range []string{"active", "updated", "empty"} {
			request := javaTopRequest(name)
			if format == "grpc" {
				ctx, cancel := context.WithTimeout(metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+api.Secret.Value()), 5*time.Second)
				_, err = client.Export(ctx, request)
				cancel()
				Expect(err).NotTo(HaveOccurred())
				continue
			}
			var data []byte
			if format == jsonType {
				data, err = request.MarshalJSON()
			} else {
				data, err = request.MarshalProto()
			}
			Expect(err).NotTo(HaveOccurred())
			var compressed bytes.Buffer
			writer := gzip.NewWriter(&compressed)
			_, err = writer.Write(data)
			Expect(err).NotTo(HaveOccurred())
			Expect(writer.Close()).To(Succeed())
			post := httptest.NewRequest(http.MethodPost, "/v1/profiles", &compressed)
			post.Header.Set("Content-Type", format)
			post.Header.Set("Content-Encoding", "gzip")
			post.Header.Set("Authorization", "Bearer "+api.Secret.Value())
			post.Header.Set("OTLP-Profiles-Development-Version", profiles.DevelopmentVersion)
			response := httptest.NewRecorder()
			receiver.HTTPHandler(true).ServeHTTP(response, post)
			Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
			Expect(response.Header().Get("OTLP-Profiles-Development-Version")).To(Equal(profiles.DevelopmentVersion))
		}
		stored, err := store.ReadProfiles(context.Background(), ProfileReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(HaveLen(3))
		Expect(stored[0].Rows[0].Attributes).To(HaveKeyWithValue("threadId", int64(9007199254740993)))
		Expect(stored[1].Rows[0].ElapsedNS).To(Equal(int64(500000000)))
		Expect(stored[2].Rows).To(BeEmpty())
	}, Entry("HTTP protobuf + gzip", protobufType), Entry("HTTP JSON + gzip", jsonType), Entry("gRPC", "grpc"))
	It("rejects missing and agent-scoped gRPC credentials before writing", func() {
		receiver, store := testReceiver(DefaultOptions())
		tokens := agentclient.NewTokenStore([]datadir.ConfigDir{{Name: datadir.ConfigGlobal, Dir: store.options.DataDir}})
		agent, err := tokens.Mint("agent", apitoken.ScopeAgent)
		Expect(err).NotTo(HaveOccurred())
		connection, err := grpc.NewClient(serveGRPC(receiver, true), grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(connection.Close)
		client := pprofileotlp.NewGRPCClient(connection)
		_, err = client.Export(context.Background(), javaTopRequest("active"))
		Expect(status.Code(err)).To(Equal(codes.Unauthenticated))
		ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+agent.Secret.Value())
		_, err = client.Export(ctx, javaTopRequest("active"))
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
		stored, err := store.ReadProfiles(context.Background(), ProfileReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(BeEmpty())
	})
})
