import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, JsonSchemaForm, Modal, type JsonSchemaFormError, type JsonSchemaObject } from "@flanksource/clicky-ui/components";
import { UiLoader, UiPlay } from "@flanksource/clicky-ui/icons";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { fetchStartForm, startRun, StartRunError, type RunExecution, type RunTraces } from "../traces/api";

/** What one run of a saved definition adds to it. */
export const EXECUTION_FIELDS = ["command", "pid", "duration", "every"] as const;

/** The start form's execution fields, or the ones it lacks. */
export function executionSchema(form: Record<string, unknown>): { schema: JsonSchemaObject } | { missing: string[] } {
  const properties = (form as JsonSchemaObject).properties ?? {};
  const missing = EXECUTION_FIELDS.filter(field => !properties[field]);
  if (missing.length) return { missing };
  return { schema: { type: "object", properties: Object.fromEntries(EXECUTION_FIELDS.map(field => [field, properties[field]!])) } };
}

/** value without the fields left blank, which the run would read as set. */
const filled = (value: Record<string, unknown>): RunExecution => Object.fromEntries(Object.entries(value).filter(([, field]) => field !== undefined && field !== "")) as RunExecution;

export interface StartDefinitionFormProps {
  definition: string;
  onStarted: (run: RunTraces) => void;
}

/** Starts a run of a saved definition: only the command or pid, the duration and how often its checkpoints read. */
export function StartDefinitionForm({ definition, onStarted }: StartDefinitionFormProps) {
  const queryClient = useQueryClient();
  const form = useQuery({ queryKey: ["trace-start-form"], queryFn: fetchStartForm, retry: false, staleTime: Infinity });
  const [value, setValue] = useState<Record<string, unknown>>({});
  const start = useMutation({
    mutationFn: () => startRun({ definition, ...filled(value) }),
    onSuccess: run => {
      void queryClient.invalidateQueries({ queryKey: ["trace-runs"] });
      onStarted(run);
    },
  });
  if (form.isPending) return <p role="status">Loading the start form…</p>;
  if (form.isError) return <p role="alert">{form.error.message}</p>;
  const execution = executionSchema(form.data.schema as Record<string, unknown>);
  if ("missing" in execution) return <p role="alert">The server's start form has no {execution.missing.join(", ")} field, so a definition cannot be started here.</p>;
  const fieldErrors: JsonSchemaFormError[] = start.error instanceof StartRunError ? start.error.errors.filter(error => error.instancePath !== "") : [];
  const formError = start.error instanceof StartRunError
    ? start.error.errors.filter(error => error.instancePath === "").map(error => error.message).join("; ")
    : start.error?.message;
  return (
    <form className="space-y-3" onSubmit={event => { event.preventDefault(); start.mutate(); }}>
      <JsonSchemaForm schema={execution.schema} value={value} onChange={setValue} errors={fieldErrors} size="sm" idPrefix={`start-${definition}`} showPreferencesMenu={false} persistPreferences={false} applyDefaults={false} />
      {formError && <p role="alert" className="text-sm text-destructive">{formError}</p>}
      <Button type="submit" disabled={start.isPending}>
        {start.isPending ? <UiLoader className="mr-1 animate-spin" /> : <UiPlay className="mr-1" />}
        Start run
      </Button>
    </form>
  );
}

/** The Start dialog of a saved definition; a started run opens on the traces page. */
export function StartDefinitionDialog({ definition, open, onClose, unsaved }: { definition: string; open: boolean; onClose: () => void; unsaved: boolean }) {
  const { navigate } = useRouter();
  return (
    <Modal open={open} onClose={onClose} title={`Start ${definition}`} size="md">
      <div className="space-y-3">
        <p className="text-sm text-muted-foreground">The run traces what the saved definition names; add what is about this run alone.</p>
        {unsaved && <p role="note" className="text-sm text-muted-foreground">This starts the saved file: your unsaved edits are not in it.</p>}
        <StartDefinitionForm definition={definition} onStarted={run => navigate(`/?run=${encodeURIComponent(run.runId)}`)} />
      </div>
    </Modal>
  );
}
