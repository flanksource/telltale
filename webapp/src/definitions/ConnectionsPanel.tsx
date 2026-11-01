import { Button, IconButton, JsonSchemaForm, Section, type JsonSchemaObject, type JsonSchemaProperty } from "@flanksource/clicky-ui/components";
import { UiAdd, UiLink, UiTrash } from "@flanksource/clicky-ui/icons";
import type { StartFieldError } from "../traces/api";
import { useConnections, type DefinitionConnection, type InlineConnection } from "./api";
import { errorsUnder, isPlainObject } from "./model";

const credential = (title: string): JsonSchemaProperty => ({
  type: "object",
  title,
  description: "A 1Password reference, resolved where the run starts with the op CLI; a literal value is refused",
  properties: { valueFrom: { type: "object", title: "From", properties: { onePassword: { type: "string", title: "1Password reference", description: "op://<vault>/<item>/<field>" } } } },
});

// spec.connections items are free-form in the CRD: this is the inline
// connection's shape (pkg/definition InlineConnection), which the server
// validates on every edit.
const INLINE_CONNECTION_SCHEMA: JsonSchemaObject = {
  type: "object",
  required: ["name", "type"],
  properties: {
    name: { type: "string", title: "Name", description: "Entries name it as connection://<name>" },
    type: { type: "string", title: "Type", description: "The connection type, such as postgres, prometheus or sql_server" },
    url: { type: "string", title: "URL", description: "May use $(username) and $(password); never a literal user or password" },
    username: credential("Username"),
    password: credential("Password"),
  },
};

const selectClass = "rounded border border-border bg-background px-2 py-1 text-sm";

function ReferencePicker({ index, value, onChange, readOnly }: { index: number; value: string; onChange: (next: string) => void; readOnly: boolean }) {
  const catalog = useConnections();
  const known = catalog.data?.some(connection => connection.reference === value);
  return (
    <div className="flex flex-wrap items-center gap-2">
      <select aria-label={`Connection ${index + 1}`} className={selectClass} value={value} disabled={readOnly} onChange={event => onChange(event.target.value)}>
        {value === "" && <option value="">Choose a stored connection…</option>}
        {value !== "" && !known && <option value={value}>{value}{catalog.data ? " (not in the connection store)" : ""}</option>}
        {catalog.data?.map(connection => <option key={connection.reference} value={connection.reference}>{connection.reference} ({connection.type})</option>)}
      </select>
      {catalog.isPending && <span role="status" className="text-xs text-muted-foreground">Loading connections…</span>}
      {catalog.isError && <span role="alert" className="text-xs text-destructive">{catalog.error.message}</span>}
    </div>
  );
}

export interface ConnectionsPanelProps {
  connections: DefinitionConnection[] | undefined;
  onChange: (next: DefinitionConnection[]) => void;
  errors: StartFieldError[];
  readOnly: boolean;
}

/** The definition's default connections: stored connection references, or inline connections with referenced credentials. */
export function ConnectionsPanel({ connections = [], onChange, errors, readOnly }: ConnectionsPanelProps) {
  const set = (index: number, next: DefinitionConnection) => onChange(connections.map((current, at) => (at === index ? next : current)));
  return (
    <Section title="Connections" icon={UiLink} summary={`${connections.length} default${connections.length === 1 ? "" : "s"}`} defaultOpen>
      <div className="space-y-3">
        <p className="text-sm text-muted-foreground">An entry that names no connection takes the one of its type here; two of one type that an entry needs are refused.</p>
        {connections.length > 0 && (
          <ul className="space-y-2">
            {connections.map((connection, index) => {
              const pointer = `/spec/connections/${index}`;
              const here = errorsUnder(errors, pointer);
              // A default is unnamed until filled in: its position is its identity.
              return (
                <li key={pointer} className="space-y-2 rounded-md border border-border p-3">
                  <div className="flex items-center gap-2 text-sm">
                    <span className="font-medium">{typeof connection === "string" ? "Stored connection" : "Inline connection"}</span>
                    {!readOnly && <IconButton icon={UiTrash} label={`Remove connection ${index + 1}`} className="ml-auto" onClick={() => onChange(connections.filter((_, at) => at !== index))} />}
                  </div>
                  {typeof connection === "string" && (
                    <>
                      <ReferencePicker index={index} value={connection} onChange={next => set(index, next)} readOnly={readOnly} />
                      {here.map(error => <p key={error.instancePath + error.message} role="alert" className="text-sm text-destructive">{error.message}</p>)}
                    </>
                  )}
                  {isPlainObject(connection) && (
                    <JsonSchemaForm
                      schema={INLINE_CONNECTION_SCHEMA}
                      value={connection}
                      onChange={next => set(index, { ...connection, ...next } as InlineConnection)}
                      readOnly={readOnly}
                      instancePath={pointer}
                      errors={here}
                      idPrefix={`connection-${index}`}
                      size="sm"
                      showPreferencesMenu={false}
                      persistPreferences={false}
                      applyDefaults={false}
                    />
                  )}
                  {typeof connection !== "string" && !isPlainObject(connection) && <p role="alert" className="text-sm text-destructive">{pointer} is {JSON.stringify(connection)}: a connection is a connection:// reference or a mapping. Fix it in the YAML tab.</p>}
                </li>
              );
            })}
          </ul>
        )}
        {!readOnly && (
          <div className="flex flex-wrap gap-2">
            <Button type="button" size="sm" variant="outline" onClick={() => onChange([...connections, ""])}><UiAdd className="mr-1" />Add stored connection</Button>
            <Button type="button" size="sm" variant="outline" onClick={() => onChange([...connections, { name: "", type: "" }])}><UiAdd className="mr-1" />Add inline connection</Button>
          </div>
        )}
      </div>
    </Section>
  );
}
