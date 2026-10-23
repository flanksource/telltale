import { useQuery } from "@tanstack/react-query";
import { Button } from "@flanksource/clicky-ui/components";

async function health(): Promise<{ status: string; version: string }> {
  const response = await fetch("/health");
  if (!response.ok) throw new Error(`Health request failed: HTTP ${response.status}`);
  const value: unknown = await response.json();
  if (typeof value !== "object" || value === null || !("status" in value) || value.status !== "healthy" || !("version" in value) || typeof value.version !== "string" || value.version.length === 0) {
    throw new Error("Invalid health response");
  }
  return { status: value.status, version: value.version };
}

export default function Status() {
  const query = useQuery({ queryKey: ["health"], queryFn: health, retry: false });
  if (query.isPending) return <p role="status">Connecting to the backend…</p>;
  if (query.isError) return (
    <div className="space-y-4">
      <p role="alert">{query.error.message}</p>
      <Button onClick={() => void query.refetch()}>Retry connection</Button>
    </div>
  );
  return (
    <div role="status" className="space-y-2">
      <p className="font-medium">Connected</p>
      <p className="text-sm text-muted-foreground">Backend version: <code>{query.data.version}</code></p>
    </div>
  );
}
