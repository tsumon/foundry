import type { C4Operation, C4Profile } from "../c4";
import type { Mission } from "../types";
import { mission } from "./fixtures";

export const c4BaseCommit = "a".repeat(40);
export const c4BaseDigest = "b".repeat(64);
export const c4Profile: C4Profile = {
  schema_version: 1,
  id: "c4_go_v1",
  mode: "synthetic",
  image_id: "sha256:" + "a".repeat(64),
  platform: "linux/arm64",
  docker_path: "/synthetic/docker",
  endpoint: "unix:///synthetic/docker.sock",
  verifier: "/opt/foundry/verify-go-v1",
};

export const c4Operation: C4Operation = {
  schema_version: 1,
  id: "baseline-1",
  kind: "baseline",
  mission_id: mission.id,
  epoch: mission.epoch,
  plan_revision: 0,
  base_commit: c4BaseCommit,
  base_digest: c4BaseDigest,
  profile_digest: "c".repeat(64),
  material_digest: "d".repeat(64),
  container_name: "foundry-c4-baseline-1",
  container_id: "e".repeat(64),
  owner: "owner",
  state: "finished",
  prepared_at: "2026-10-21T00:00:00Z",
  approval: {
    digest: "d".repeat(64),
    revision: 2,
    at: "2026-10-21T00:00:01Z",
  },
  dispatch: { revision: 3, at: "2026-10-21T00:00:02Z" },
  created: { revision: 4, at: "2026-10-21T00:00:03Z" },
  started: { revision: 5, at: "2026-10-21T00:00:04Z" },
  result: {
    class: "passed",
    exit_code: 0,
    tests: 1,
    output: "SYNTHETIC",
    truncated: false,
    revision: 6,
    at: "2026-10-21T00:00:05Z",
  },
  cleanup: [
    {
      state: "confirmed",
      container_id: "e".repeat(64),
      owner: "owner",
      revision: 7,
      at: "2026-10-21T00:00:06Z",
    },
  ],
};

export const c4CandidateOperation: C4Operation = {
  ...c4Operation,
  id: "candidate-1",
  kind: "candidate",
  plan_revision: mission.plan!.revision,
  candidate_generation: { id: "generation-1", digest: "2".repeat(64) },
  candidate_digest: "2".repeat(64),
  material_digest: "3".repeat(64),
  container_name: "foundry-c4-candidate-1",
  approval: { ...c4Operation.approval!, digest: "4".repeat(64) },
};

export const c4Mission: Mission = {
  ...mission,
  base_commit: c4BaseCommit,
  spec: { ...mission.spec, c4: c4Profile },
  repository_import: {
    schema_version: 1,
    inspection_id: "inspection-1",
    inspection_digest: "f".repeat(64),
    import_operation_id: "import-1",
    root: "/tmp/fixture",
    commit: c4BaseCommit,
    base_digest: c4BaseDigest,
    import_spec_digest: "1".repeat(64),
  },
  c4_operations: [c4Operation],
};
