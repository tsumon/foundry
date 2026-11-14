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
