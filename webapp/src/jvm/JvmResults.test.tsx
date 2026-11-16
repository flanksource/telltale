import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { JvmResults } from "./JvmResults";

it("shows compiler diagnostics when the native response includes its class output list", () => {
  render(<JvmResults value={{ success: false, classes: [], classpathEntries: 4, diagnostics: [{ kind: "ERROR", line: 5, column: 12, message: "String cannot be converted to double" }] }} />);
  expect(screen.getByText("Compilation failed")).toBeInTheDocument();
  expect(screen.getByRole("alert")).toHaveTextContent("ERROR · 5:12 · String cannot be converted to double");
});
