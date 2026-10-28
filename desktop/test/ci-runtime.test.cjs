const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const yaml = require("js-yaml");

function assertBuildPython(job, testCommand) {
  const setupIndex = job.steps.findIndex((step) => step.id === "build-python");
  assert.ok(setupIndex >= 0, "trusted build Python setup required");
  const setup = job.steps[setupIndex];
  assert.equal(setup.uses, "actions/setup-python@6d4fd22b20465ccc0f7354476588e8df7c9ca0e0");
  assert.equal(setup.with["python-version"], "3.14.8");
  assert.equal(setup.with.cache, undefined);
  assert.equal(setup.if, undefined);
  assert.notEqual(setup["continue-on-error"], true);
  const consumers = job.steps.filter((step) => testCommand.test(step.run || ""));
  assert.ok(consumers.length > 0, "desktop test execution required");
  for (const step of consumers) {
    assert.ok(setupIndex < job.steps.indexOf(step), "Python setup must precede tests");
    assert.equal(step.env?.FOUNDRY_BUILD_PYTHON, "${{ steps.build-python.outputs.python-path }}");
    assert.equal(step.if, undefined);
    assert.notEqual(step["continue-on-error"], true);
  }
}

for (const [file, jobID, command] of [
  ["verify.yml", "linux-source-verify", /npm test --prefix desktop/],
  ["desktop.yml", "macos-installer", /npm test --prefix desktop/],
  ["desktop.yml", "windows-installer", /\.\/desktop\/windows-test\.ps1/],
]) {
  const workflow = yaml.load(fs.readFileSync(path.join(__dirname, "../../.github/workflows", file), "utf8"));
  const job = workflow.jobs[jobID];
  test(`${jobID}: desktop tests require pinned Python through its explicit output path`, () => {
    assertBuildPython(job, command);
  });
  for (const [name, mutate] of [
    ["missing setup", (value) => { value.steps = value.steps.filter((step) => step.id !== "build-python"); }],
    ["unpinned action", (value) => { value.steps.find((step) => step.id === "build-python").uses = "actions/setup-python@v7"; }],
    ["wrong Python version", (value) => { value.steps.find((step) => step.id === "build-python").with["python-version"] = "3.12"; }],
    ["ambient Python", (value) => { value.steps.find((step) => command.test(step.run || "")).env.FOUNDRY_BUILD_PYTHON = "python3"; }],
    ["missing path wiring", (value) => { delete value.steps.find((step) => command.test(step.run || "")).env.FOUNDRY_BUILD_PYTHON; }],
    ["late setup", (value) => { const index = value.steps.findIndex((step) => step.id === "build-python"); value.steps.push(...value.steps.splice(index, 1)); }],
    ["conditional setup", (value) => { value.steps.find((step) => step.id === "build-python").if = "false"; }],
    ["ignored setup failure", (value) => { value.steps.find((step) => step.id === "build-python")["continue-on-error"] = true; }],
    ["conditional tests", (value) => { value.steps.find((step) => command.test(step.run || "")).if = "false"; }],
    ["ignored test failure", (value) => { value.steps.find((step) => command.test(step.run || ""))["continue-on-error"] = true; }],
  ]) {
    test(`${jobID}: runtime policy rejects ${name}`, () => {
      assertBuildPython(job, command);
      const changed = structuredClone(job);
      mutate(changed);
      assert.throws(() => assertBuildPython(changed, command), assert.AssertionError);
    });
  }
}

// Read the literal argument arrays passed to the fail-closed native gate.
// This checks executable selection rather than incidental comments or names.
function checkedCommands(source) {
  return Array.from(source.matchAll(/^Checked '(go|node)' @\(([^\n]+)\)$/gm), ([, command, values]) => {
    assert.match(values.replace(/'[^']*'/g, ""), /^[,\s]*$/);
    return { command, args: Array.from(values.matchAll(/'([^']*)'/g), ([, value]) => value) };
  });
}

function assertWindowsCoverage(commands) {
  assert.ok(commands.some(({ command, args }) => command === "node" && args[0] === "--test"
    && args.includes("desktop/test/build-guest-v2.test.cjs")
    && !args.some((arg) => /^--test-(name|skip|only)/.test(arg))), "Checked must execute v2 assembler host tests");
  for (const pkg of ["./internal/dependencybundle", "./internal/frameworkreport", "./internal/domain"]) {
    assert.ok(commands.some(({ command, args }) => command === "go" && args[0] === "test"
      && args.includes("-count=1") && args.includes(pkg)
      && !args.some((arg) => /^-(run|skip|list|c)(=|$)/.test(arg))), `Checked must execute all platform-supported tests in ${pkg}`);
  }
}

const windowsCommands = checkedCommands(fs.readFileSync(path.join(__dirname, "../windows-test.ps1"), "utf8"));
test("Windows native gates execute assembler and new Go package tests", () => {
  assertWindowsCoverage(windowsCommands);
});
for (const target of ["desktop/test/build-guest-v2.test.cjs", "./internal/dependencybundle", "./internal/frameworkreport", "./internal/domain"]) {
  test(`Windows coverage policy rejects omitted ${target}`, () => {
    assertWindowsCoverage(windowsCommands);
    const changed = windowsCommands.map(({ command, args }) => ({ command, args: args.filter((arg) => arg !== target) }));
    assert.throws(() => assertWindowsCoverage(changed), assert.AssertionError);
  });
}
test("Windows coverage policy rejects compile-only Go package selection", () => {
  assertWindowsCoverage(windowsCommands);
  const changed = structuredClone(windowsCommands);
  for (const { command, args } of changed) {
    if (command === "go") args.push("-run", "^$");
  }
  assert.throws(() => assertWindowsCoverage(changed), assert.AssertionError);
});
