import { expect, it, vi } from "vitest";
import { capabilityApi } from "./capabilityApi";
import { APIError } from "./api";
import { response } from "./test/fixtures";
import {
  emptyCapabilities,
  operation,
  skill,
} from "./components/capabilities/testFixtures";

it("reads direct snapshots through the existing same-origin session without bootstrap or mutation", async () => {
  const fetcher = vi.fn().mockResolvedValue(response(emptyCapabilities));
  vi.stubGlobal("fetch", fetcher);
  await expect(capabilityApi.snapshot()).resolves.toEqual(emptyCapabilities);
  expect(fetcher).toHaveBeenCalledWith("/api/capabilities", {
    credentials: "same-origin",
    headers: {},
  });
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it("URL-encodes record IDs and preserves exact discovery/revision envelopes without starting extra requests", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(response({ operation }))
    .mockResolvedValueOnce(response({ revision: skill }));
  vi.stubGlobal("fetch", fetcher);
  await expect(capabilityApi.discovery("request /?#")).resolves.toEqual({
    operation,
  });
  await expect(capabilityApi.skillRevision("skill /?#", 3)).resolves.toEqual({
    revision: skill,
  });
  expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
    "/api/capabilities/discoveries/request%20%2F%3F%23",
    "/api/capabilities/skills/skill%20%2F%3F%23/revisions/3",
  ]);
  expect(
    fetcher.mock.calls.every(([, init]) => init.method === undefined),
  ).toBe(true);
});

it("sends explicit Skill update CAS and does not retry conflict responses", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValue(
      response(
        { error: { code: "conflict", message: "safe backend message" } },
        409,
      ),
    );
  vi.stubGlobal("fetch", fetcher);
  await expect(
    capabilityApi.saveSkill({
      id: "skill-1",
      expected_revision: 3,
      source: "manual",
      files: [{ path: "SKILL.md", content: "inert" }],
    }),
  ).rejects.toBeInstanceOf(APIError);
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(fetcher.mock.calls[0]).toEqual([
    "/api/capabilities/skills",
    {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        id: "skill-1",
        expected_revision: 3,
        source: "manual",
        files: [{ path: "SKILL.md", content: "inert" }],
      }),
    },
  ]);
});
