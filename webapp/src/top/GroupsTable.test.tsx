import { fireEvent, render, screen, within } from "@testing-library/react";
import { RouterProvider, useMemoryRouter } from "@flanksource/clicky-ui/rpc";
import { describe, expect, it, vi } from "vitest";
import { GroupsTable } from "./GroupsTable";
import { DEFAULT_VIEW, type GroupsView } from "./groups";
import { othersFrame, sampleFrame } from "./testFrames";

function renderTable(overrides: Partial<GroupsView> = {}, groups = sampleFrame().groups) {
  const onViewChange = vi.fn();
  render(<GroupsTable groups={groups} view={{ ...DEFAULT_VIEW, ...overrides }} onViewChange={onViewChange} />);
  return onViewChange;
}

const cells = (name: RegExp) => within(screen.getByRole("row", { name })).getAllByRole("cell").map(cell => cell.textContent);

describe("GroupsTable", () => {
  it("shows each group's total, and a dash for what is unknown rather than zero", () => {
    renderTable();
    // Group, Procs, CPU %, RSS, Disk read/s, Disk write/s, Net in/s, Net out/s, Sockets, Origin, Owner, Context
    expect(cells(/vitest/)).toEqual(["vitest", "2", "30.0", "300 MiB", "–", "–", "1.5 KiB/s", "–", "–", "", "", ""]);
    expect(cells(/^claude/)).toEqual(["claude 01234567", "5", "12.5", "600 MiB", "2 KiB/s", "0 B/s", "–", "–", "7", "", "", "telltale@main claude:0123456… cmux:workspa…"]);
  });

  it("indents a child under its parent and keeps the tree order", () => {
    renderTable();
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows.map(row => [row.getAttribute("data-depth"), row.querySelector("th, td")?.textContent])).toEqual([
      ["0", "claude 01234567"], ["1", "vitest"], ["1", "chrome"], ["0", "gavel"],
    ]);
    const vitestIndent = parseFloat(within(rows[1]!).getAllByRole("cell")[0]!.querySelector<HTMLElement>("[data-indent]")!.style.paddingLeft);
    expect(vitestIndent).toBeGreaterThan(0);
  });

  it("offers a group's own use, apart from its descendants, as the title of its cell", () => {
    renderTable();
    const claude = within(screen.getByRole("row", { name: /^claude/ })).getAllByRole("cell");
    expect([claude[2]!.title, claude[3]!.title, claude[4]!.title]).toEqual(["self 0.5", "self 100 MiB", "self –"]);
  });

  it("sorts by a column's header, highest first, then flips the direction of the sorted one", () => {
    const onViewChange = renderTable();
    fireEvent.click(screen.getByRole("button", { name: "RSS" }));
    expect(onViewChange).toHaveBeenLastCalledWith({ sort: "rss", dir: "desc" });
    fireEvent.click(screen.getByRole("button", { name: "CPU %" }));
    expect(onViewChange).toHaveBeenLastCalledWith({ dir: "asc" });
    fireEvent.click(screen.getByRole("button", { name: "Group" }));
    expect(onViewChange).toHaveBeenLastCalledWith({ sort: "name", dir: "asc" });
  });

  it("marks the sorted column", () => {
    renderTable({ sort: "rss", dir: "asc" });
    expect(screen.getByRole("columnheader", { name: "RSS" })).toHaveAttribute("aria-sort", "ascending");
    expect(screen.getByRole("columnheader", { name: "CPU %" })).toHaveAttribute("aria-sort", "none");
  });

  it("says when there are no groups, and when the filters leave none", () => {
    renderTable({}, []);
    expect(screen.getByText("No process groups yet.")).toBeInTheDocument();
  });

  it("says when the filters leave no group", () => {
    renderTable({ q: "no-such-tool" });
    expect(screen.getByText("No group matches these filters.")).toBeInTheDocument();
  });

  it("draws a CPU trend per group when given one", () => {
    render(<GroupsTable groups={sampleFrame().groups} view={DEFAULT_VIEW} onViewChange={vi.fn()} trend={{ "claude:1": [1, 5, 3] }} />);
    expect(within(screen.getByRole("row", { name: /^claude/ })).getByRole("img", { name: /CPU trend/ })).toBeInTheDocument();
  });
});

function renderLive(initialPath = "/top", overrides: Partial<GroupsView> = {}) {
  function Harness() {
    const router = useMemoryRouter(initialPath);
    return (
      <RouterProvider adapter={router}>
        <output data-testid="path">{router.pathname}</output>
        <GroupsTable groups={othersFrame().groups} view={{ ...DEFAULT_VIEW, showOther: true, ...overrides }} onViewChange={vi.fn()} linkGroups />
      </RouterProvider>
    );
  }
  render(<Harness />);
}

describe("GroupsTable origin, scope and the other processes tree", () => {
  it("shows what started a group and who owns it, in the Origin and Owner columns", () => {
    renderLive();
    // Group, Procs, CPU %, RSS, Disk read/s, Disk write/s, Net in/s, Net out/s, Sockets, Origin, Owner, Context
    const service = cells(/^postgresql@17/);
    expect(service.slice(-3)).toEqual(["brew:postgresql@17", "moshe (user)", ""]);
    expect(screen.getByRole("columnheader", { name: "Origin" })).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "Owner" })).toBeInTheDocument();
  });

  it("starts the synthetic other parents collapsed, and expands one to its programs, indented under it", () => {
    renderLive();
    expect(screen.getAllByRole("row").slice(1).map(row => [row.getAttribute("data-depth"), within(row).getAllByRole("cell")[0]!.textContent])).toEqual([
      ["0", "▸other"], ["0", "postgresql@17"], ["0", "▸system"],
    ]);
    fireEvent.click(screen.getByRole("button", { name: "Expand other" }));
    expect(screen.getAllByRole("row").slice(1).map(row => [row.getAttribute("data-depth"), within(row).getAllByRole("cell")[0]!.textContent])).toEqual([
      ["0", "▾other"], ["1", "Slack"], ["0", "postgresql@17"], ["0", "▸system"],
    ]);
    fireEvent.click(screen.getByRole("button", { name: "Collapse other" }));
    expect(screen.queryByRole("row", { name: /^Slack/ })).not.toBeInTheDocument();
  });

  it("shows a program that matches the text filter though its parent is collapsed", () => {
    renderLive("/top", { q: "slack" });
    expect(screen.getAllByRole("row").slice(1).map(row => [row.getAttribute("data-depth"), within(row).getAllByRole("cell")[0]!.textContent])).toEqual([
      ["0", "▾other"], ["1", "Slack"],
    ]);
  });

  it("filters by scope, keeping the synthetic parent of the scope's programs", () => {
    renderLive("/top", { scope: "system" });
    expect(screen.getAllByRole("row").slice(1).map(row => within(row).getAllByRole("cell")[0]!.textContent)).toEqual(["▸system"]);
  });

  it("links a program's group to its name (encoded), a service's group to its pid, and the synthetic parents to nothing", () => {
    renderLive("/top", { q: "" });
    fireEvent.click(screen.getByRole("button", { name: "Expand system" }));
    expect(screen.getByRole("link", { name: "com.apple.WebKit.Networking" })).toHaveAttribute("href", "/top/com.apple.WebKit.Networking");
    expect(screen.getByRole("link", { name: "postgresql@17" })).toHaveAttribute("href", "/top/900");
    expect(screen.queryByRole("link", { name: "system" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "other" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("link", { name: "postgresql@17" }));
    expect(screen.getByTestId("path")).toHaveTextContent("/top/900");
  });

  it("does not link groups unless asked to (recorded groups' processes may be gone)", () => {
    renderTable();
    expect(screen.queryAllByRole("link")).toEqual([]);
  });
});
