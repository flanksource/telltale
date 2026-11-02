import { ConnectionsPage as ConnectionEditor } from "@flanksource/clicky-ui/connections";
import { useRouter } from "@flanksource/clicky-ui/rpc";

export default function ConnectionsPage({ selected }: { selected?: string }) {
  const { navigate, renderLink } = useRouter();
  return <div className="space-y-4">
    <h1 className="text-2xl font-semibold">Connections</h1>
    <p className="text-sm text-muted-foreground">Manage workspace connections. Connections from shared and application configuration are read-only here.</p>
    {renderLink({ to: "/query", children: "Query a connection", className: "text-sm underline underline-offset-4" })}
    <ConnectionEditor
      apiBase="/api/v1"
      selected={selected}
      onSelect={id => navigate(id ? `/connections/${encodeURIComponent(id)}` : "/connections")}
      connectionHref={id => `/connections/${encodeURIComponent(id)}`}
      readOnlyHint="Edit the connection in its source configuration file."
    />
  </div>;
}
