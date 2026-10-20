import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ConnectionDiagnostic } from "./ConnectionDiagnostic";

it("renders only static labels for a bounded recorded token failure", () => {
  render(
    <ConnectionDiagnostic
      diagnostic={{
        version: 1,
        stage: "token_exchange",
        code: "auth_invalid",
        http_status: 403,
        provider_code: "invalid_grant",
        credential_commit: "not_committed",
        cleanup: "completed",
      }}
    />,
  );

  const view = screen.getByLabelText("连接失败诊断");
  expect(view).toHaveTextContent("令牌交换");
  expect(view).toHaveTextContent("提供方拒绝了授权凭据");
  expect(view).toHaveTextContent("HTTP 403");
  expect(view).toHaveTextContent("invalid_grant");
  expect(view).toHaveTextContent("未提交");
  expect(view).toHaveTextContent("已完成");
  expect(view).toHaveTextContent("当前尝试不会重试");
  expect(view).toHaveTextContent("远端授权状态仍未验证");
  expect(screen.queryByRole("button")).not.toBeInTheDocument();
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
});

it("uses a conservative historical fallback when no diagnostic was recorded", () => {
  render(<ConnectionDiagnostic />);
  expect(screen.getByLabelText("连接失败诊断")).toHaveTextContent(
    "阶段未记录",
  );
  expect(screen.getByText(/较早的连接回执没有保存诊断阶段/)).toBeVisible();
});

it("does not reflect malformed or unknown diagnostic values", () => {
  render(
    <ConnectionDiagnostic
      diagnostic={{
        version: 99,
        stage: "SYNTHETIC-PRIVATE-STAGE",
        code: "SYNTHETIC-PRIVATE-CODE",
        http_status: 999,
        provider_code: "SYNTHETIC-PRIVATE-PROVIDER",
        credential_commit: "SYNTHETIC-PRIVATE-CREDENTIAL",
        cleanup: "SYNTHETIC-PRIVATE-CLEANUP",
      }}
    />,
  );
  const view = screen.getByLabelText("连接失败诊断");
  expect(view).toHaveTextContent("阶段未记录");
  expect(view).not.toHaveTextContent("SYNTHETIC-PRIVATE");
  expect(view).not.toHaveTextContent("999");
});

it("renders bounded post-commit failure and cleanup evidence", () => {
  render(
    <ConnectionDiagnostic
      diagnostic={{
        version: 1,
        stage: "session",
        code: "transport_unknown",
        credential_commit: "committed",
        cleanup: "completed",
      }}
    />,
  );
  const view = screen.getByLabelText("连接失败诊断");
  expect(view).toHaveTextContent("连接会话");
  expect(view).toHaveTextContent("已提交");
  expect(view).toHaveTextContent("已完成");
  expect(view).not.toHaveTextContent("阶段未记录");
});
