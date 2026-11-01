import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, JsonSchemaForm, type JsonSchemaFormError, type JsonSchemaObject } from "@flanksource/clicky-ui/components";
import { UiLoader } from "@flanksource/clicky-ui/icons";
import { useDefinitions } from "../definitions/api";
import { StartDefinitionForm } from "../definitions/StartDefinitionForm";
import { fetchStartForm, startRun, StartRunError, type RunTraces, type StartForm, type StartRequest } from "./api";

export interface StartRunFormProps {
  agent?: string;
  probe?: string;
  /** Called with the run the server started. */
  onStarted: (run: RunTraces) => void;
}

function StartRunFields({ form, onStarted, agent, probe }: StartRunFormProps & { form: StartForm }) {
  const queryClient = useQueryClient();
  const [value, setValue] = useState<StartRequest>(() => agent && probe ? {
    ...form.defaults, command: "",
    traces: { [probe]: { enabled: true, target: { seam: "agent", agent } } },
  } : form.defaults);
  const start = useMutation({
    mutationFn: startRun,
    onSuccess: run => {
      void queryClient.invalidateQueries({ queryKey: ["trace-runs"] });
      onStarted(run);
    },
  });
  const fieldErrors: JsonSchemaFormError[] = start.error instanceof StartRunError
    ? start.error.errors.filter(error => error.instancePath !== "")
    : [];
  const formError = start.error instanceof StartRunError
    ? start.error.errors.filter(error => error.instancePath === "").map(error => error.message).join("; ")
    : start.error?.message;

  return (
    <form
      className="space-y-3"
      onSubmit={event => {
        event.preventDefault();
        start.mutate(value);
      }}
    >
      <JsonSchemaForm
        schema={form.schema as JsonSchemaObject}
        value={value}
        onChange={setValue}
        errors={fieldErrors}
        hiddenKeys={["definition"]}
        size="sm"
        idPrefix="trace-start"
        showPreferencesMenu={false}
        persistPreferences={false}
      />
      {formError && <p role="alert" className="text-sm text-destructive">{formError}</p>}
      <Button type="submit" disabled={start.isPending}>
        {start.isPending && <UiLoader className="mr-1 animate-spin" />}
        Start run
      </Button>
    </form>
  );
}

/** Which saved definition a run starts from; empty keeps the captures inline. */
function DefinitionPicker({ value, onChange }: { value: string; onChange: (next: string) => void }) {
  const definitions = useDefinitions();
  return (
    <div className="flex flex-wrap items-center gap-3 text-sm">
      <label className="flex items-center gap-2">
        Definition
        <select className="rounded border border-border bg-background px-2 py-1" value={value} disabled={definitions.isPending} onChange={event => onChange(event.target.value)}>
          <option value="">None: choose the captures below</option>
          {definitions.data?.map(definition => (
            <option key={definition.name} value={definition.name} disabled={!!definition.problem}>
              {definition.name} ({definition.layer}){definition.problem ? ": unreadable" : ""}
            </option>
          ))}
        </select>
      </label>
      {definitions.isPending && <span role="status" className="text-muted-foreground">Loading definitions…</span>}
      {definitions.isError && <span role="alert" className="text-destructive">{definitions.error.message}</span>}
    </div>
  );
}

/**
 * The form that starts a run on the server: a saved trace definition plus the
 * run's command or pid, or a shell command and the captures armed around it.
 */
export function StartRunForm(props: StartRunFormProps) {
  const [definition, setDefinition] = useState("");
  const form = useQuery({ queryKey: ["trace-start-form"], queryFn: fetchStartForm, retry: false, staleTime: Infinity });
  if (form.isPending) return <p role="status">Loading the start form…</p>;
  if (form.isError) return <p role="alert">{form.error.message}</p>;
  const captures = (form.data.schema as JsonSchemaObject).properties?.traces;
  if (props.probe && (!captures || typeof captures !== "object" || !captures.properties?.[props.probe])) {
    return <p role="alert">Probe {props.probe} has no armable trace kind on this server.</p>;
  }
  // Arming one agent probe is an inline start by construction.
  if (props.probe) return <StartRunFields form={form.data} {...props} />;
  return (
    <div className="space-y-3">
      <DefinitionPicker value={definition} onChange={setDefinition} />
      {definition
        ? <StartDefinitionForm key={definition} definition={definition} onStarted={props.onStarted} />
        : <StartRunFields form={form.data} {...props} />}
    </div>
  );
}
