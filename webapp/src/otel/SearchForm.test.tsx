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

it("hides run and OpenSearch settings for the receiver source", () => {
  render(
    <SearchForm
      form={{
        schema: {
          type: "object",
          properties: {
            run: { type: "string", title: "Run" },
            index: { type: "string", title: "Index" },
            format: { type: "string", title: "Document format" },
            minDuration: { type: "string", title: "Minimum trace duration" },
          },
        },
        defaults: {},
      }}
      sources={[
        { id: "receiver", name: "Received traces", type: "recordstore" },
      ]}
      value={{ source: "receiver" }}
      onChange={() => {}}
      onSearch={() => {}}
      pending={false}
      services={[]}
      operations={[]}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "More filters" }));
  expect(
    screen.getByRole("textbox", { name: "Minimum trace duration" }),
  ).toBeInTheDocument();
  for (const name of ["Run", "Index", "Document format"]) {
    expect(screen.queryByRole("textbox", { name })).not.toBeInTheDocument();
  }
});
