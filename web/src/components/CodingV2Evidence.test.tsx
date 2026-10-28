import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { C4Verification } from "./C4Verification";
import { c4Mission, c4Operation } from "../test/c4Fixtures";
import { profileV2, evidenceV2 } from "../test/codingV2Fixtures";
import { c4Passed, c4OperationLabel } from "../c4";
it.each([0, 1, 2])(
  "renders the fixed language %i contract and protected digests without Go claims",
  (index) => {
    const p = profileV2(index),
      m = {
        ...c4Mission,
        spec: { ...c4Mission.spec, c4: p },
        c4_profile_binding: {
          revision: c4Mission.revision,
          digest: c4Operation.profile_digest,
          profile: p,
        },
      };
    render(<C4Verification mission={m} />);
    expect(screen.getByText(p.v2.execution_contract_digest)).toBeVisible();
    expect(screen.getByText(/纯语言依赖/)).toBeVisible();
    expect(screen.queryByText(/Go 1.27.3/)).not.toBeInTheDocument();
    expect(screen.getByText(p.v2.runner_id)).toBeVisible();
  },
);
it.each(["infrastructure_failed", "timeout"])(
  "keeps zero-case %s visible as infrastructure evidence, never a completed passing test",
  (kind) => {
    const base = evidenceV2(),
      p = base.spec.c4,
      o = {
        ...base.c4_operations[0],
        result: {
          ...c4Operation.result!,
          class: kind,
          tests: 0,
          exit_code: 1,
          output: "synthetic compile failure",
        },
      },
      m = { ...base, c4_operations: [o] };
    render(<C4Verification mission={m} />);
    expect(screen.getByText(/基础设施失败|超时/)).toBeVisible();
    expect(screen.getByText("synthetic compile failure")).toBeVisible();
    expect(c4Passed(o)).toBe(false);
    expect(c4OperationLabel(m, o)).not.toContain("验证通过");
    expect(screen.getByText(/完成测试用例数/)).toBeVisible();
  },
);
it("distinguishes a genuine completed assertion failure from a zero-case compile failure", () => {
  const base = evidenceV2(),
    p = base.spec.c4,
    o = {
      ...base.c4_operations[0],
      result: {
        ...c4Operation.result!,
        class: "test_failed",
        tests: 2,
        exit_code: 1,
      },
    },
    m = base;
  expect(c4OperationLabel(m, o)).toContain("测试断言失败");
});
