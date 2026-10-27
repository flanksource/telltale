import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, JsonSchemaForm, type JsonSchemaFormError, type JsonSchemaObject } from "@flanksource/clicky-ui/components";
import { UiLoader } from "@flanksource/clicky-ui/icons";
import { fetchStartForm, startRun, StartRunError, type RunTraces, type StartForm, type StartRequest } from "./api";

export interface StartRunFormProps {
  /** Called with the run the server started. */
  onStarted: (run: RunTraces) => void;
}

function StartRunFields({ form, onStarted }: StartRunFormProps & { form: StartForm }) {
  const queryClient = useQueryClient();
  const [value, setValue] = useState<StartRequest>(form.defaults);
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
        size="sm"
        idPrefix="trace-start"
        showPreferencesMenu={false}
        persistPreferences={false}
      />
      {formError && <p role="alert" className="text-sm text-red-700 dark:text-red-400">{formError}</p>}
      <Button type="submit" disabled={start.isPending}>
        {start.isPending && <UiLoader className="mr-1 animate-spin" />}
        Start run
      </Button>
    </form>
  );
}

/** The form that starts a run on the server: a shell command and the captures armed around it. */
export function StartRunForm({ onStarted }: StartRunFormProps) {
  const form = useQuery({ queryKey: ["trace-start-form"], queryFn: fetchStartForm, retry: false, staleTime: Infinity });
  if (form.isPending) return <p role="status">Loading the start form…</p>;
  if (form.isError) return <p role="alert">{form.error.message}</p>;
  return <StartRunFields form={form.data} onStarted={onStarted} />;
}
