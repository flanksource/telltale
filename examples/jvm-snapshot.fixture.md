---
cwd: ..
timeout: 15m
---

# JVM support bundle acceptance

Build and live checks run sequentially to keep the binary and embedded assets consistent.

### command: Snapshot acceptance

```yaml
timeout: 1200
```

```bash
set -e
make build
./sdk/java/gradlew -p sdk/java test --tests com.flanksource.telltale.SettingsTest --tests com.flanksource.telltale.diag.jvm.ResourceSettingsTest --tests com.flanksource.telltale.CatalogSnapshotTest
go test ./internal/jvm/snapshot -count=1
go test ./internal/cli ./internal/server -ginkgo.focus='JVM command tree|HTTP routing'
pnpm exec vitest run webapp/src/jvm/JvmSnapshot.test.tsx packages/api/test/agents.spec.ts
make lint
```

- cel: exitCode == 0
