import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ConnectionDiagnostic } from "./ConnectionDiagnostic";

it("shows a static identity sub-check without revealing claims or adding controls", () => {
  render(
    <ConnectionDiagnostic
      diagnostic={{
        version: 1,
        stage: "identity_validation",
        code: "auth_invalid",
        identity_check: "audience",
        credential_commit: "not_committed",
        cleanup: "completed",
      }}
    />,
  );
  const view = screen.getByLabelText("连接失败诊断");
  expect(view).toHaveTextContent("身份检查项");
  expect(view).toHaveTextContent("令牌受众");
  expect(view).toHaveTextContent("未提交");
  expect(view).toHaveTextContent("已完成");
  expect(screen.queryByRole("button")).toBeNull();
});

it.each(["SYNTHETIC-PRIVATE-CHECK", "", null, 1, "__proto__"])(
  "fails closed for unknown identity check %s",
  (identity_check) => {
    render(
      <ConnectionDiagnostic
        diagnostic={{
          version: 1,
          stage: "identity_validation",
          code: "auth_invalid",
          identity_check,
          credential_commit: "not_committed",
          cleanup: "completed",
        }}
      />,
    );
    const view = screen.getByLabelText("连接失败诊断");
    expect(view).toHaveTextContent("阶段未记录");
    expect(view).not.toHaveTextContent("SYNTHETIC-PRIVATE-CHECK");
  },
);

it("rejects an identity check attached to a different stage", () => {
  render(
    <ConnectionDiagnostic
      diagnostic={{
        version: 1,
        stage: "token_exchange",
        code: "auth_invalid",
        identity_check: "nonce",
        credential_commit: "not_committed",
        cleanup: "completed",
      }}
    />,
  );
  expect(screen.getByLabelText("连接失败诊断")).toHaveTextContent("阶段未记录");
});

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
  expect(view).toHaveTextContent("授权信息未通过安全校验");
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
  expect(screen.getByLabelText("连接失败诊断")).toHaveTextContent("阶段未记录");
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

it.each(["callback_validation", "oidc_discovery", "identity_validation"])(
  "does not attribute local %s failures to a provider rejection",
  (stage) => {
    render(
      <ConnectionDiagnostic
        diagnostic={{
          version: 1,
          stage,
          code: "auth_invalid",
          credential_commit: "not_committed",
          cleanup: "completed",
        }}
      />,
    );
    const view = screen.getByLabelText("连接失败诊断");
    expect(view).toHaveTextContent("授权信息未通过安全校验");
    expect(view).not.toHaveTextContent("提供方拒绝了授权凭据");
  },
);

it("describes a consent decline without claiming account ineligibility", () => {
  render(
    <ConnectionDiagnostic
      diagnostic={{
        version: 1,
        stage: "browser_authorization",
        code: "entitlement_denied",
        credential_commit: "not_committed",
        cleanup: "not_required",
      }}
    />,
  );
  const view = screen.getByLabelText("连接失败诊断");
  expect(view).toHaveTextContent("这次连接未获所需授权");
  expect(view).not.toHaveTextContent("当前账户不符合这次连接要求");
  expect(view).toHaveTextContent("未提交");
});

it("describes post-commit cleanup failure without denying the recorded commit", () => {
  render(
    <ConnectionDiagnostic
      diagnostic={{
        version: 1,
        stage: "credential_commit",
        code: "storage_error",
        credential_commit: "committed",
        cleanup: "unknown",
      }}
    />,
  );
  const view = screen.getByLabelText("连接失败诊断");
  expect(view).toHaveTextContent("本地凭据处理遇到错误");
  expect(view).not.toHaveTextContent("本地凭据存储未能完成");
  expect(view).toHaveTextContent("已提交");
  expect(view).toHaveTextContent("未知");
});
