import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { mission, response } from "../test/fixtures";
import { AppPreview } from "./AppPreview";

it("opens a candidate only on request in an opaque-origin iframe and removes stale previews", async () => {
  const fetcher = vi.fn(async () =>
    response({
      html: "<!doctype html><p>Habit app</p>",
      candidate_digest: "candidate-1",
    }),
  );
  vi.stubGlobal("fetch", fetcher);
  const initial = { ...mission, candidate_digest: "candidate-1" };
  const view = render(<AppPreview mission={initial} />);
  expect(fetcher).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "预览当前应用" }));
  const frame = await screen.findByTitle("应用隔离预览");
  expect(frame).toHaveAttribute("sandbox", "allow-scripts");
  expect(frame).toHaveAttribute("referrerpolicy", "no-referrer");
  expect(frame).toHaveAttribute(
    "src",
    "/api/missions/m1/app-preview/frame?digest=candidate-1",
  );
  expect(frame).not.toHaveAttribute("srcdoc");
  view.rerender(
    <AppPreview mission={{ ...initial, candidate_digest: "candidate-2" }} />,
  );
  expect(screen.queryByTitle("应用隔离预览")).not.toBeInTheDocument();
});

it("discards a response when the candidate changed while it was loading", async () => {
  let finish!: (value: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn(
      () =>
        new Promise<Response>((resolve) => {
          finish = resolve;
        }),
    ),
  );
  const view = render(
    <AppPreview mission={{ ...mission, candidate_digest: "candidate-1" }} />,
  );
  fireEvent.click(screen.getByRole("button", { name: "预览当前应用" }));
  view.rerender(
    <AppPreview mission={{ ...mission, candidate_digest: "candidate-2" }} />,
  );
  await act(async () =>
    finish(response({ html: "old content", candidate_digest: "candidate-1" })),
  );
  expect(screen.queryByTitle("应用隔离预览")).not.toBeInTheDocument();
});

it("supports viewport switching, state reset without changing frame URL, and companion QA acceptance checklist", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({
        html: "<!doctype html><p>App</p>",
        candidate_digest: "cand-1",
      }),
    ),
  );
  const ideaMission = {
    ...mission,
    state: "awaiting_delivery",
    candidate_digest: "cand-1",
    idea_project: {
      schema_version: 2 as const,
      template: "browser_app_v1" as const,
      acceptance: ["Add entry", "Export JSON"],
    },
    reviews: [
      {
        role: "qa" as const,
        passed: true,
        summary: "ok",
        findings: [],
        candidate_digest: "cand-1",
        checks: [
          {
            id: "requirement:0",
            passed: true,
            summary: "Add verified in checks.js",
            paths: ["src/app.js"],
          },
        ],
      },
    ],
  };
  render(<AppPreview mission={ideaMission} />);
  fireEvent.click(screen.getByRole("button", { name: "预览当前应用" }));
  const frame = await screen.findByTitle("应用隔离预览");
  expect(frame).toHaveAttribute(
    "src",
    "/api/missions/m1/app-preview/frame?digest=cand-1",
  );
  fireEvent.click(screen.getByRole("button", { name: "手机 390px" }));
  expect(frame.parentElement).toHaveClass("viewport-mobile");
  fireEvent.click(screen.getByRole("button", { name: "展开高度" }));
  expect(frame).toHaveClass("preview-tall");
  fireEvent.click(screen.getByRole("button", { name: "重置应用状态" }));
  const reloadedFrame = screen.getByTitle("应用隔离预览");
  expect(reloadedFrame).not.toBe(frame);
  expect(reloadedFrame).toHaveAttribute(
    "src",
    "/api/missions/m1/app-preview/frame?digest=cand-1",
  );
  expect(screen.getByText("对照需求验收清单")).toBeVisible();
  expect(screen.getByText(/Add verified in checks\.js/)).toBeVisible();
  const itemCheck = screen.getByRole("checkbox", { name: "Add entry" });
  fireEvent.click(itemCheck);
  expect(itemCheck).toBeChecked();
});
