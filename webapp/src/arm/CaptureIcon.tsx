import { FlanksourceIcon } from "@flanksource/clicky-ui/connections";

const KIND_ICONS: Record<string, string> = {
  http: "http", http_proxy: "http", http_top: "http", sql: "postgres", pg_proxy: "postgres", sql_top: "sql",
  sql_deadlock: "sqlserver", sql_plancache: "sqlserver", sql_xevent: "sqlserver",
  jvm_jfr: "java", jvm_trace: "java", jvm_notifications: "java", jvm_ognl: "java", jvm_redefine: "java",
  jms: "activemq", jms_top: "activemq", exec: "shell", llm: "ai", logs: "logs", spans: "opentelemetry",
  entity: "database", task: "console",
};

const PROVIDER_ICONS: Record<string, string> = { k8s: "k8s", bigquery: "gcp-bigquery" };

export function CaptureIcon({ kind, provider, className }: { kind: string; provider?: string; className?: string }) {
  return <FlanksourceIcon name={provider ? (PROVIDER_ICONS[provider] ?? provider) : (KIND_ICONS[kind] ?? "database")} className={className} />;
}
