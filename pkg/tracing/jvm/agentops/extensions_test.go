package agentops_test

import (
	"encoding/json"

	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/tracing/jvm/agentops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Built from Extensions.list(): the demo JVM has no extensionsDir, so nothing can be installed in it.
const sourceExtensions = `[{"id":"acme-cache","version":"1.2.0","digest":"3f5a9c0e1b2d4f6a8c0e2b4d6f8a0c2e4b6d8f0a2c4e6b8d0f2a4c6e8b0d2f4a","active":0,"status":"active","operations":["extension.acme-cache.stats","extension.acme-cache.evict"]}]`

const agentCatalog = `[{"name":"jvm.probes.list","title":"jvm.probes.list","description":"List method probe keys","command":"jvm trace probes","paramsSchema":{"type":"object","properties":{},"required":[],"additionalProperties":false},"resultSchema":{"type":"object"},"mutates":false,"scope":"jvm:read"},` +
	`{"name":"jvm.probes.freeze","title":"jvm.probes.freeze","description":"Stop a method probe from recording further events","command":"jvm trace freeze","paramsSchema":{"type":"object","properties":{"key":{"type":"string","title":"key"}},"required":["key"],"additionalProperties":false},"resultSchema":{"type":"object"},"mutates":true,"scope":"jvm:trace"}]`

var _ = Describe("extensions and the catalog", func() {
	It("tabulates the loaded extensions", func() {
		result, err := agentops.DecodeExtensions(json.RawMessage(sourceExtensions))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(agentops.Extensions{{
			ID: "acme-cache", Version: "1.2.0", Status: "active",
			Digest:     "3f5a9c0e1b2d4f6a8c0e2b4d6f8a0c2e4b6d8f0a2c4e6b8d0f2a4c6e8b0d2f4a",
			Operations: []string{"extension.acme-cache.stats", "extension.acme-cache.evict"},
		}}))
		Expect(rows(result)).To(Equal([]map[string]any{{
			"id": "acme-cache", "version": "1.2.0", "status": api.Text{}.Append("active", "text-green-600"),
			"active": 0, "operations": 2, "digest": "3f5a9c0e1b2d",
		}}))
		Expect(plain(result)[0]).To(Equal("Extensions 1 loaded"))
	})

	It("says no extension is loaded on one line", func() {
		result, err := agentops.DecodeExtensions(json.RawMessage(`[]`))
		Expect(err).NotTo(HaveOccurred())
		Expect(plain(result)).To(Equal([]string{"no extensions loaded"}))
	})

	It("acknowledges an install, a replace and an unload on one line each", func() {
		installed, err := agentops.Results[agentops.ExtensionsInstallOp](json.RawMessage(`{"installed":"acme-cache"}`))
		Expect(err).NotTo(HaveOccurred())
		replaced, err := agentops.Results[agentops.ExtensionsReplaceOp](json.RawMessage(`{"installed":"acme-cache"}`))
		Expect(err).NotTo(HaveOccurred())
		unloaded, err := agentops.DecodeExtensionUnload(json.RawMessage(`{"unloaded":"acme-cache"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect([][]string{
			plain(installed.(interface{ Pretty() api.Text })), plain(replaced.(interface{ Pretty() api.Text })), plain(unloaded),
		}).To(Equal([][]string{{"✓ installed extension acme-cache"}, {"✓ replaced extension acme-cache"}, {"✓ unloaded extension acme-cache"}}))
	})

	It("tabulates the catalog's operations, marking those that mutate", func() {
		result, err := agentops.DecodeCatalog(json.RawMessage(agentCatalog))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(result[1].ParamsSchema)).To(Equal(`{"type":"object","properties":{"key":{"type":"string","title":"key"}},"required":["key"],"additionalProperties":false}`))
		Expect(rows(result)).To(Equal([]map[string]any{
			{"name": "jvm.probes.list", "command": "jvm trace probes", "scope": "jvm:read", "mutates": api.Text{}, "description": "List method probe keys"},
			{"name": "jvm.probes.freeze", "command": "jvm trace freeze", "scope": "jvm:trace", "mutates": api.Text{}.Append("mutates", "text-yellow-600"), "description": "Stop a method probe from recording further events"},
		}))
		Expect(plain(result)[0]).To(Equal("Catalog 2 operations · 1 mutates"))
	})

	It("refuses a catalog entry without a name", func() {
		_, err := agentops.DecodeCatalog(json.RawMessage(`[{"title":"x"}]`))
		Expect(err).To(HaveOccurred())
	})
})
