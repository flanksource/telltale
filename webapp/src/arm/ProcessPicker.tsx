import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Combobox, Field, type ComboboxOption } from "@flanksource/clicky-ui/components";
import { fetchTopProcesses } from "../top/api";

/** A running process on the server's host, searched live by name, command or pid. */
export function ProcessPicker({ id, value, onChange, error, initialSearch = "", helper }: {
  id: string;
  value: number | null;
  onChange: (pid: number | null) => void;
  error?: string | undefined;
  /** The search the list opens with, such as `java` for JVMs. */
  initialSearch?: string;
  helper?: string;
}) {
  const [search, setSearch] = useState(initialSearch);
  const processes = useQuery({
    queryKey: ["arm-processes", search],
    queryFn: ({ signal }) => fetchTopProcesses({ q: search }, signal),
    enabled: search.trim() !== "",
    retry: false,
    staleTime: 5000,
  });
  const options: ComboboxOption[] = (processes.data?.processes ?? []).map(process => ({
    value: String(process.pid),
    label: `${process.name} (${process.pid})`,
    description: process.command,
    trailing: <span className="font-mono text-[11px] text-muted-foreground">ppid {process.ppid}</span>,
  }));
  return (
    <Field label="Process" htmlFor={id} required error={error ?? (processes.isError ? processes.error.message : undefined)} helper={helper}>
      <Combobox
        id={id}
        options={options}
        value={value === null ? "" : String(value)}
        onChange={text => onChange(/^\d+$/.test(text) ? Number(text) : null)}
        onSearch={setSearch}
        onNew={text => (/^\d+$/.test(text) ? { value: text, label: `pid ${text}` } : null)}
        loading={processes.isFetching}
        invalid={!!error}
        placeholder="Search name, command or pid…"
      />
    </Field>
  );
}
