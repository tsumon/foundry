import type { C4Operation } from "../c4";
import { mission } from "./fixtures";

export const c4Operation: C4Operation = {
  schema_version: 1,
  id: "baseline-1",
  kind: "baseline",
  mission_id: mission.id,
  epoch: 0,
  plan_revision: 0,
  base_commit: "base",
  base_digest: "base-digest",
  profile_digest: "profile",
  material_digest: "material",
  container_name: "owned",
  container_id: "b".repeat(64),
  owner: "owner",
  state: "finished",
  prepared_at: "today",
  approval: { digest: "material", revision: 2, at: "today" },
  dispatch: { revision: 3, at: "today" },
  created: { revision: 4, at: "today" },
  started: { revision: 5, at: "today" },
  result: {
    class: "passed",
    exit_code: 0,
    tests: 1,
    output: "SYNTHETIC",
    truncated: false,
    revision: 6,
    at: "today",
  },
  cleanup: [
    {
      state: "confirmed",
      container_id: "b".repeat(64),
      owner: "owner",
      revision: 7,
      at: "today",
    },
  ],
};
