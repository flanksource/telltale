import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { SearchForm } from "./SearchForm";
import type { SearchValues } from "./api";

it("keeps common filters visible and retains their values while editing more filters", () => {
  let submitted: SearchValues | undefined;
  function Harness() {
    const [value, setValue] = useState<SearchValues>({
      source: "records",
      service: "checkout",
    });
    return (
      <SearchForm
        form={{
          schema: {
            type: "object",
            properties: {
              service: { type: "string", title: "Service" },
              minDuration: { type: "string", title: "Minimum trace duration" },
            },
          },
          defaults: {},
        }}
        sources={[{ id: "records", name: "Recorded traces", type: "records" }]}
        runs={[]}
        value={value}
        onChange={setValue}
        onSearch={() => {
          submitted = value;
        }}
        pending={false}
        services={[]}
        operations={[]}
      />
    );
  }
  render(<Harness />);
  expect(screen.getByRole("textbox", { name: "Service" })).toHaveValue(
    "checkout",
  );
  expect(
    screen.queryByRole("textbox", { name: "Minimum trace duration" }),
  ).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "More filters" }));
  fireEvent.change(
    screen.getByRole("textbox", { name: "Minimum trace duration" }),
    { target: { value: "100ms" } },
  );
  fireEvent.change(screen.getByRole("textbox", { name: "Service" }), {
    target: { value: "payments" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Find traces" }));
  expect(submitted).toMatchObject({
    source: "records",
    service: "payments",
    minDuration: "100ms",
  });
});

const runForm = {
  schema: {
    type: "object" as const,
    properties: {
      run: { type: "string" as const, title: "Run" },
      index: { type: "string" as const, title: "Index" },
      format: { type: "string" as const, title: "Document format" },
      minDuration: { type: "string" as const, title: "Minimum trace duration" },
    },
  },
  defaults: {},
};

it("offers the receiver run for recorded traces and hides OpenSearch settings", () => {
  let changed: SearchValues | undefined;
  render(
    <SearchForm
      form={runForm}
      sources={[{ id: "records", name: "Recorded traces", type: "records" }]}
      runs={["run-2026-10-29T10-00-00Z", "receiver"]}
      value={{ source: "records" }}
      onChange={(next) => {
        changed = next;
      }}
      onSearch={() => {}}
      pending={false}
      services={[]}
      operations={[]}
    />,
  );
  const run = screen.getByRole("combobox", { name: "Run" });
  expect(
    Array.from(run.querySelectorAll("option")).map((o) => o.textContent),
  ).toEqual(["Every run", "run-2026-10-29T10-00-00Z", "receiver (OTLP receiver)"]);
  fireEvent.change(run, { target: { value: "receiver" } });
  expect(changed).toMatchObject({ source: "records", run: "receiver" });
  fireEvent.click(screen.getByRole("button", { name: "More filters" }));
  expect(
    screen.getByRole("textbox", { name: "Minimum trace duration" }),
  ).toBeInTheDocument();
  for (const name of ["Run", "Index", "Document format"])
    expect(screen.queryByRole("textbox", { name })).not.toBeInTheDocument();
});

it("offers no run for a connection source", () => {
  render(
    <SearchForm
      form={runForm}
      sources={[{ id: "search", name: "search", type: "opensearch" }]}
      runs={["receiver"]}
      value={{ source: "search" }}
      onChange={() => {}}
      onSearch={() => {}}
      pending={false}
      services={[]}
      operations={[]}
    />,
  );
  expect(screen.queryByRole("combobox", { name: "Run" })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "More filters" }));
  expect(screen.getByRole("textbox", { name: "Index" })).toBeInTheDocument();
});
