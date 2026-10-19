import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { mission, response } from "../test/fixtures";
import { ControlledTask } from "./ControlledTask";

const controlled = {
  ...mission,
  state: "planning",
  tool_mode: "controlled_read_v1" as const,
  workspace_mode: "generations_controlled_read_v1" as const,
  task_bindings: {
    git: {
      repository_id: "repo",
      repository_revision: 2,
      observation_id: "obs",
      root: "/very/long/source/root",
      base_commit: "a".repeat(40),
    },
    skills: [
      {
        id: "skill",
        revision: 3,
        digest: "b".repeat(64),
        name: "Review source",
        role: "researcher" as const,
      },
    ],
    policy_version: "repo_read_v1",
    tools: ["repo.list", "repo.read", "repo.search"],
    budget: {
      max_turns: 4,
      max_calls: 3,
      max_argument_bytes: 8192,
      max_result_bytes: 65536,
      deadline_seconds: 5,
      max_total_result_bytes: 196608,
    },
  },
};

it("shows exact frozen source, Skill destination, fixed tools and current grant without calling it approval", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      response({
        read_grants: [
          {
            id: "grant",
            mission_id: mission.id,
            epoch: 1,
            session_id: "session",
            role: "researcher",
            required_state: "planning",
            binding_digest: "c".repeat(64),
            authority_digest: "d".repeat(64),
            created_at: "2026-10-18T00:00:00Z",
          },
        ],
      }),
    ),
  );
  render(<ControlledTask mission={controlled} />);
  expect(screen.getByText("/very/long/source/root")).toBeVisible();
  expect(screen.getByText(/Review source · v3 · Researcher/)).toBeVisible();
  expect(screen.getByText(/fixture-model/)).toBeVisible();
  expect(screen.getByText(/repo.list/)).toBeVisible();
  expect(screen.getByText(/单次参数 8192 bytes/)).toBeVisible();
  expect(await screen.findByText(/当前 epoch\/状态有记录/)).toBeVisible();
  expect(screen.queryByText(/已批准/)).not.toBeInTheDocument();
});

it("treats missing controlled bindings as an integrity error", () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(response({ read_grants: [] })),
  );
  render(
    <ControlledTask mission={{ ...controlled, task_bindings: undefined }} />,
  );
  expect(screen.getByRole("alert")).toHaveTextContent(/缺少冻结绑定/);
});
