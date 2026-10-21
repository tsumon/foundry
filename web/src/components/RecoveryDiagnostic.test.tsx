import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { MissionViews } from "./MissionViews";
import { mission, response } from "../test/fixtures";
const diagnostic = {
  schema_version: 1,
  mission_id: mission.id,
  revision: mission.revision,
  epoch: mission.epoch,
  checked_at: "2026-10-21T10:00:00Z",
  stale: false,
  checks: [
    {
      id: "candidate_integrity",
      status: "blocked",
      code: "candidate_invalid",
      message: "candidate bytes differ",
      records: [],
    },
  ],
  next_actions: ["inspect_records"],
};
it("marks previous facts stale when a same-revision recheck starts and fails without retrying", async () => {
  let reject!: (cause: Error) => void;
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(
      response({
        diagnostic: {
          ...diagnostic,
          checks: [
            {
              id: "candidate",
              status: "verified",
              code: "candidate_verified",
              message: "candidate bytes verified",
              records: [],
            },
          ],
          next_actions: ["delivery_decision", "new_task"],
        },
      }),
    )
    .mockImplementationOnce(
      () =>
        new Promise<Response>((_, fail) => {
          reject = fail;
        }),
    );
  vi.stubGlobal("fetch", fetcher);
  render(<MissionViews mission={mission} view="总览" />);
  fireEvent.click(screen.getByRole("button", { name: "核对恢复状态" }));
  expect(await screen.findByText("candidate bytes verified")).toBeVisible();
  expect(
    screen.getByRole("link", { name: /返回原决策面板核对/ }),
  ).toBeVisible();
  expect(screen.getByText(/需要新的任务/)).toBeVisible();

  fireEvent.click(screen.getByRole("button", { name: "核对恢复状态" }));
  expect.soft(screen.queryByText(/已过期/)).toBeVisible();
  expect
    .soft(screen.queryByRole("link", { name: /返回原决策面板核对/ }))
    .not.toBeInTheDocument();
  expect.soft(screen.queryByText(/需要新的任务/)).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "正在核对…" })).toBeDisabled();

  await act(async () => reject(new Error("diagnostic unavailable")));
  expect(screen.getByRole("alert")).toHaveTextContent("diagnostic unavailable");
  expect(screen.getByText("candidate bytes verified")).toBeVisible();
  expect(screen.getByText(/已过期/)).toBeVisible();
  expect(
    screen.queryByRole("link", { name: /返回原决策面板核对/ }),
  ).not.toBeInTheDocument();
  expect(screen.queryByText(/需要新的任务/)).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "核对恢复状态" })).toBeEnabled();
  expect(fetcher).toHaveBeenCalledTimes(2);
});
it("diagnoses only on click, renders the envelope, and invalidates after revision changes", async () => {
  const fetcher = vi.fn(async () => response({ diagnostic }));
  vi.stubGlobal("fetch", fetcher);
  const ui = render(<MissionViews mission={mission} view="总览" />);
  expect(fetcher).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "核对恢复状态" }));
  expect(await screen.findByText(/candidate bytes differ/)).toBeVisible();
  expect(fetcher.mock.calls).toHaveLength(1);
  ui.rerender(
    <MissionViews
      mission={{ ...mission, revision: mission.revision + 1 }}
      view="总览"
    />,
  );
  expect(screen.getByText(/已过期/)).toBeVisible();
  expect(fetcher.mock.calls).toHaveLength(1);
});
it("discards a late diagnostic across mission epochs", async () => {
  let resolve!: (r: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn(
      () =>
        new Promise((r) => {
          resolve = r;
        }),
    ),
  );
  const ui = render(<MissionViews mission={mission} view="总览" />);
  fireEvent.click(screen.getByRole("button", { name: "核对恢复状态" }));
  ui.rerender(
    <MissionViews
      mission={{ ...mission, epoch: mission.epoch + 1 }}
      view="总览"
    />,
  );
  await act(async () => resolve(response({ diagnostic })));
  expect(screen.queryByText(/candidate bytes differ/)).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "核对恢复状态" })).toBeEnabled();
});
it("does not revive an earlier request when navigation returns to the original revision", async () => {
  let resolve!: (r: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn(
      () =>
        new Promise((r) => {
          resolve = r;
        }),
    ),
  );
  const ui = render(<MissionViews mission={mission} view="总览" />);
  fireEvent.click(screen.getByRole("button", { name: "核对恢复状态" }));
  ui.rerender(
    <MissionViews mission={{ ...mission, id: "other" }} view="总览" />,
  );
  ui.rerender(<MissionViews mission={mission} view="总览" />);
  await act(async () => resolve(response({ diagnostic })));
  expect(screen.queryByText(/candidate bytes differ/)).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "核对恢复状态" })).toBeEnabled();
});
