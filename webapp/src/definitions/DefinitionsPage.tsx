import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button, Modal } from "@flanksource/clicky-ui/components";
import { Badge, DataTable, type DataTableColumn } from "@flanksource/clicky-ui/data";
import { UiFilePlus, UiWarningTriangle } from "@flanksource/clicky-ui/icons";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { definitionKeys, useDefinition, useDefinitions, useDefinitionSchema, type DefinitionLayer, type DefinitionSchema, type DefinitionSummary, type StoredDefinition } from "./api";
import { DefinitionEditor, LayerBadge } from "./DefinitionEditor";
import { emptyDefinition } from "./model";

type DefinitionRow = DefinitionSummary & Record<string, unknown>;

function useSaved() {
  const queryClient = useQueryClient();
  const { navigate, pathname } = useRouter();
  return (saved: StoredDefinition) => {
    queryClient.setQueryData(definitionKeys.one(saved.name), saved);
    void queryClient.invalidateQueries({ queryKey: definitionKeys.list });
    const target = `/definitions/${encodeURIComponent(saved.name)}`;
    if (pathname !== target) navigate(target);
  };
}

function NewDefinitionDialog({ open, onClose, onCreate }: { open: boolean; onClose: () => void; onCreate: (name: string, layer: DefinitionLayer, schema: DefinitionSchema) => void }) {
  const schema = useDefinitionSchema();
  const [name, setName] = useState("");
  const writable = schema.data?.layers.filter(layer => layer.writable) ?? [];
  const [layer, setLayer] = useState<DefinitionLayer | "">("");
  const chosen = layer || writable[0]?.name;
  const input = "w-full rounded border border-border bg-background px-2 py-1 text-sm";
  return (
    <Modal open={open} onClose={onClose} title="New trace definition" size="sm">
      {schema.isPending && <p role="status">Loading the definition schema…</p>}
      {schema.isError && <p role="alert" className="text-sm text-destructive">{schema.error.message}</p>}
      {schema.data && (
        <form className="space-y-3" onSubmit={event => { event.preventDefault(); if (chosen) onCreate(name.trim(), chosen, schema.data); }}>
          <div className="space-y-1 text-sm">
            <label htmlFor="new-definition-name">Name</label>
            <input id="new-definition-name" aria-describedby="new-definition-name-help" className={input} value={name} onChange={event => setName(event.target.value)} placeholder="checkout" autoFocus />
            <p id="new-definition-name-help" className="text-xs text-muted-foreground">The file name, and the prefix of each capture's default name.</p>
          </div>
          <div className="space-y-1 text-sm">
            <label htmlFor="new-definition-layer">Save to</label>
            <select id="new-definition-layer" className={input} value={chosen ?? ""} onChange={event => setLayer(event.target.value as DefinitionLayer)}>
              {writable.map(candidate => <option key={candidate.name} value={candidate.name}>{candidate.name} ({candidate.dir})</option>)}
            </select>
          </div>
          {writable.length === 0 && <p role="alert" className="text-sm text-destructive">The server offers no writable layer to save a definition to.</p>}
          <Button type="submit" disabled={!name.trim() || !chosen}>Create</Button>
        </form>
      )}
    </Modal>
  );
}

function DefinitionList() {
  const definitions = useDefinitions();
  const { renderLink } = useRouter();
  const onSaved = useSaved();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [creating, setCreating] = useState<{ name: string; layer: DefinitionLayer; schema: DefinitionSchema } | null>(null);
  if (creating) {
    return (
      <div className="space-y-4">
        <Button type="button" variant="link" className="px-0" onClick={() => setCreating(null)}>Back to definitions (discards this new definition)</Button>
        <DefinitionEditor schema={creating.schema} source={{ name: creating.name, layer: creating.layer, resourceVersion: "", definition: emptyDefinition(creating.name), entries: [] }} onSaved={onSaved} />
      </div>
    );
  }
  const columns: DataTableColumn<DefinitionRow>[] = [
    { key: "name", label: "Name", render: (_, row) => renderLink({ to: `/definitions/${encodeURIComponent(row.name)}`, className: "font-medium text-primary underline-offset-4 hover:underline", children: row.name }) },
    { key: "layer", label: "Layer", render: (_, row) => <LayerBadge layer={row.layer} /> },
    {
      key: "description", label: "Description", grow: true, minWidth: 200,
      render: (_, row) => row.problem
        ? <Badge tone="danger" icon={UiWarningTriangle} wrap>{row.problem}</Badge>
        : <span className="text-muted-foreground">{row.description}</span>,
    },
    ...(["proxy", "instrument", "collect"] as const).map((section): DataTableColumn<DefinitionRow> => ({
      key: section, label: section[0]!.toUpperCase() + section.slice(1), align: "right",
      render: (_, row) => <span aria-label={`${section} captures`}>{row.counts[section]}</span>,
    })),
  ];
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-2xl font-semibold">Trace definitions</h1>
        <Button type="button" className="ml-auto" onClick={() => setDialogOpen(true)}><UiFilePlus className="mr-1" />New definition</Button>
      </div>
      <p className="text-sm text-muted-foreground">What telltale traces, kept apart from any one run of it: proxies, instrumentation and collectors. A run names a definition and adds its command or pid and duration.</p>
      {definitions.isError && (
        <div className="space-y-2">
          <p role="alert" className="text-sm text-destructive">{definitions.error.message}</p>
          <Button type="button" variant="outline" onClick={() => void definitions.refetch()}>Retry</Button>
        </div>
      )}
      {!definitions.isError && (
        <DataTable
          data={(definitions.data ?? []) as DefinitionRow[]}
          columns={columns}
          getRowId={row => `${row.layer}:${row.name}`}
          loading={definitions.isPending}
          emptyMessage="No trace definitions yet. Create one, or save a TraceDefinition YAML file under .telltale/traces/."
          showFullscreenControl={false}
        />
      )}
      <NewDefinitionDialog
        open={dialogOpen}
        onClose={() => setDialogOpen(false)}
        onCreate={(name, layer, schema) => { setDialogOpen(false); setCreating({ name, layer, schema }); }}
      />
    </div>
  );
}

function DefinitionPage({ name }: { name: string }) {
  const definition = useDefinition(name);
  const schema = useDefinitionSchema();
  const queryClient = useQueryClient();
  const { navigate, renderLink } = useRouter();
  const onSaved = useSaved();
  const [reloads, setReloads] = useState(0);
  const back = renderLink({ to: "/definitions", className: "text-sm underline underline-offset-4", children: "All definitions" });
  if (definition.isPending || schema.isPending) return <div className="space-y-3">{back}<p role="status">Loading trace definition {name}…</p></div>;
  const failed = definition.error ?? schema.error;
  if (failed) return <div className="space-y-3">{back}<p role="alert" className="text-sm text-destructive">{failed.message}</p></div>;
  const stored = definition.data!;
  return (
    <div className="space-y-3">
      {back}
      <DefinitionEditor
        key={`${stored.layer}:${stored.resourceVersion}:${reloads}`}
        schema={schema.data!}
        source={stored}
        onSaved={onSaved}
        onReload={() => void definition.refetch().then(() => setReloads(count => count + 1))}
        onDeleted={() => {
          queryClient.removeQueries({ queryKey: definitionKeys.one(name) });
          void queryClient.invalidateQueries({ queryKey: definitionKeys.list });
          navigate("/definitions");
        }}
      />
    </div>
  );
}

/** The trace definitions the server's config dirs hold, or one of them in the editor. */
export default function DefinitionsPage({ name }: { name?: string }) {
  return name ? <DefinitionPage name={name} /> : <DefinitionList />;
}
