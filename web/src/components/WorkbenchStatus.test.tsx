import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { normalizeBuildMetadata } from "../buildMetadata";
import { WorkbenchStatus } from "./WorkbenchStatus";

it("shows unavailable build fields explicitly instead of inventing an identity", () => {
  render(
    <WorkbenchStatus
      loading={false}
      error=""
      snapshotReceived={false}
      stream="未连接"
      lastDataReceivedAt={null}
      build={normalizeBuildMetadata(undefined, "")}
    />,
  );
  const status = screen.getByLabelText("工作台状态");
  expect(status).toHaveTextContent(/前端版本\s*不可用/);
  expect(status).toHaveTextContent(/构建时间\s*不可用/);
  expect(status).toHaveTextContent(/最近收到任务数据\s*尚未收到/);
});
