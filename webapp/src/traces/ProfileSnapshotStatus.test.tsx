import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ProfileSnapshotStatus, type ProfileFrame } from "./ProfileSnapshotStatus";

const now = Date.parse("2026-11-14T10:00:20Z");
const frame: ProfileFrame = {
  id: "snapshot-2", source: "java/orders", startedAt: "2026-11-14T10:00:00Z", completedAt: "2026-11-14T10:00:00Z",
  observed: 3, retained: 2, limit: 2,
  summary: { profileId: "profile-2", complete: false, dropped: 4, receivedAt: "2026-11-14T10:00:00Z", pollIntervalMs: 5000 },
};

describe("profile snapshot status", () => {
  it("distinguishes observation loss, intentional limits and stale live snapshots", () => {
    render(<ProfileSnapshotStatus frames={[frame]} live now={now} />);
    expect(screen.getByRole("status")).toHaveTextContent("Incomplete snapshot");
    expect(screen.getByRole("status")).toHaveTextContent("4 observations dropped");
    expect(screen.getByRole("status")).toHaveTextContent("Top limit: showing 2 of 3 active operations");
    expect(screen.getByRole("status")).toHaveTextContent("Snapshot is stale");
  });

  it("does not label an empty completed historical snapshot as stale", () => {
    render(<ProfileSnapshotStatus frames={[{ ...frame, observed: 0, retained: 0, summary: { ...frame.summary, complete: true, dropped: 0 } }]} live={false} now={now} />);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("does not apply Profiles status rules to existing non-profile frames", () => {
    render(<ProfileSnapshotStatus frames={[{ ...frame, summary: undefined }]} live now={now} />);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("reports the newest producer observation even when an older snapshot arrives later", () => {
    render(<ProfileSnapshotStatus frames={[
      { ...frame, id: "latest", tick: 3 },
      { ...frame, id: "late", tick: 2, summary: { ...frame.summary, complete: true, dropped: 0 }, observed: 2 },
    ]} live now={now} />);
    expect(screen.getAllByRole("status")).toHaveLength(1);
    expect(screen.getByRole("status")).toHaveTextContent("Incomplete snapshot");
  });

  it("does not describe observation loss as a configured top limit", () => {
    render(<ProfileSnapshotStatus frames={[{ ...frame, limit: 50 }]} live={false} now={now} />);
    expect(screen.getByRole("status")).toHaveTextContent("4 observations dropped");
    expect(screen.getByRole("status")).not.toHaveTextContent("Top limit");
  });
});
