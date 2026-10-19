import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it } from "vitest";
import { CapabilitySettings } from "./CapabilitySettings";
import {
  catalogState,
  deferred,
  emptyCapabilities,
  mockServer,
  skill,
} from "./testFixtures";
import { response } from "../../test/fixtures";

async function openSkills() {
  render(<CapabilitySettings enabled catalog={catalogState} />);
  fireEvent.click(screen.getByRole("button", { name: "Skills" }));
  await screen.findByRole("button", { name: "登记静态 Skill" });
}

it("previews complete pasted/static text, saves an inert package and clears content only on success", async () => {
  const pending = deferred<Response>();
  const fetcher = mockServer(emptyCapabilities, (path) =>
    path === "/api/capabilities/skills" ? pending.promise : undefined,
  );
  await openSkills();
  fireEvent.change(screen.getByLabelText("Skill 来源说明"), {
    target: { value: "人工粘贴" },
  });
  fireEvent.change(screen.getByLabelText("SKILL.md 正文"), {
    target: { value: skill.files[0].content },
  });
  fireEvent.click(screen.getByRole("button", { name: "添加静态参考文件" }));
  fireEvent.change(screen.getByLabelText("文件 2 路径"), {
    target: { value: "references/rules.md" },
  });
  fireEvent.change(screen.getByLabelText("文件 2 正文"), {
    target: { value: "approve(); <script>unsafe()</script>" },
  });
  expect(
    screen.getByRole("region", { name: "待登记文件预览" }).textContent,
  ).toContain(skill.files[0].content);
  expect(document.querySelector("script")).toBeNull();
  const save = screen.getByRole("button", { name: "登记静态 Skill" });
  fireEvent.click(save);
  fireEvent.click(save);
  expect(screen.getByLabelText("SKILL.md 正文")).toHaveValue(
    skill.files[0].content,
  );
  const posts = fetcher.mock.calls.filter(
    ([, init]) => init?.method === "POST",
  );
  expect(posts).toHaveLength(1);
  expect(JSON.parse(posts[0][1]!.body as string)).toEqual({
    expected_revision: 0,
    source: "人工粘贴",
    files: [
      skill.files[0],
      {
        path: "references/rules.md",
        content: "approve(); <script>unsafe()</script>",
      },
    ],
  });
  await act(async () => pending.resolve(response({ revision: skill })));
  await waitFor(() =>
    expect(screen.getByLabelText("SKILL.md 正文")).toHaveValue(""),
  );
  expect(
    screen.getByText(/已登记不可变版本；可在新受控任务中精确选择/),
  ).toBeVisible();
  expect(
    screen.getByText(/只有在新受控任务中明确选择冻结版本后/),
  ).toHaveTextContent(/发送给所选角色的冻结模型连接/);
  expect(
    screen.queryByRole("button", { name: /执行|安装|激活/ }),
  ).not.toBeInTheDocument();
});

it("reads exact immutable revision bodies and uses latest expected revision for an explicit new version", async () => {
  const latest = {
    ...skill,
    revision: 2,
    digest: "digest-2",
    files: [{ path: "SKILL.md", content: "最新正文" }],
  };
  const fetcher = mockServer(
    { ...emptyCapabilities, skills: [latest, skill] },
    (path) =>
      path.endsWith("/revisions/1") ? response({ revision: skill }) : undefined,
  );
  await openSkills();
  fireEvent.click(
    screen.getByRole("button", { name: "查看 review-source v1" }),
  );
  expect(
    await screen.findByRole("region", { name: "Skill 完整版本" }),
  ).toHaveTextContent("完整旧正文");
  expect(
    screen.getByRole("region", { name: "Skill 完整版本" }),
  ).toHaveTextContent("references/rules.md");
  expect(fetcher.mock.calls.map(([path]) => path)).toContain(
    "/api/capabilities/skills/skill-1/revisions/1",
  );
  fireEvent.click(
    screen.getByRole("button", { name: "创建新版本 review-source" }),
  );
  expect(screen.getByLabelText("SKILL.md 正文")).toHaveValue("最新正文");
  expect(screen.getByText(/基于 v2/)).toBeVisible();
});

it("preserves all nonsecret files after a revision conflict", async () => {
  mockServer(emptyCapabilities, (path) =>
    path === "/api/capabilities/skills"
      ? response({ error: { code: "conflict", message: "conflict" } }, 409)
      : undefined,
  );
  await openSkills();
  fireEvent.change(screen.getByLabelText("SKILL.md 正文"), {
    target: { value: skill.files[0].content },
  });
  fireEvent.click(screen.getByRole("button", { name: "登记静态 Skill" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("草稿已保留");
  expect(screen.getByLabelText("SKILL.md 正文")).toHaveValue(
    skill.files[0].content,
  );
});

it("decodes only explicitly selected bytes as strict UTF-8 and preserves drafts when any file is invalid", async () => {
  mockServer();
  await openSkills();
  fireEvent.change(screen.getByLabelText("SKILL.md 正文"), {
    target: { value: "保留正文" },
  });
  const bad = new File([new Uint8Array([0xff])], "broken.md", {
    type: "text/plain",
  });
  fireEvent.change(screen.getByLabelText("选择静态文本文件"), {
    target: { files: [bad] },
  });
  expect(await screen.findByRole("alert")).toHaveTextContent("UTF-8");
  expect(screen.getByLabelText("SKILL.md 正文")).toHaveValue("保留正文");
  const file = new File(["selected only"], "rules.md", { type: "text/plain" });
  fireEvent.change(screen.getByLabelText("选择静态文本文件"), {
    target: { files: [file] },
  });
  await waitFor(() =>
    expect(screen.getByLabelText("文件 2 正文")).toHaveValue("selected only"),
  );
  expect(screen.getByLabelText("文件 2 路径")).toHaveValue("rules.md");
  fireEvent.change(screen.getByLabelText("文件 2 路径"), {
    target: { value: "references/rules.md" },
  });
  expect(
    screen.getByRole("region", { name: "待登记文件预览" }),
  ).toHaveTextContent("references/rules.md");
});

it("keeps the root editor bound to SKILL.md when canonical package order starts with another root file", async () => {
  mockServer({
    ...emptyCapabilities,
    skills: [
      {
        ...skill,
        files: [{ path: "A.md", content: "static reference" }, skill.files[0]],
      },
    ],
  });
  await openSkills();
  fireEvent.click(
    screen.getByRole("button", { name: "创建新版本 review-source" }),
  );
  expect(screen.getByLabelText("SKILL.md 正文")).toHaveValue(
    skill.files[0].content,
  );
  expect(screen.getByLabelText("文件 2 路径")).toHaveValue("A.md");
});

it("does not silently remove a valid UTF-8 BOM from an explicitly selected file", async () => {
  mockServer();
  await openSkills();
  const selected = new File(
    [new Uint8Array([0xef, 0xbb, 0xbf, 0x61])],
    "a.txt",
    { type: "text/plain" },
  );
  fireEvent.change(screen.getByLabelText("选择静态文本文件"), {
    target: { files: [selected] },
  });
  await waitFor(() =>
    expect(screen.getByLabelText("文件 2 正文")).toHaveValue("\ufeffa"),
  );
});
