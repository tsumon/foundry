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
  for (const target of ["desktop/test/runtime-v2.test.cjs", "desktop/test/packaging-v2.test.cjs", "desktop/test/multilang-inputs.test.cjs", "desktop/test/runner-contracts-v2.test.cjs"]) {
    assert.ok(commands.some(({command,args}) => command === "node" && args[0] === "--test" && args.includes(target)), "Windows must execute " + target);
  }
  assert.ok(commands.some(({command,args}) => command === "go" && args[0] === "test" && args.includes("./internal/vmruntime") && args.some(arg => arg.includes("RuntimeV2"))), "Windows executes v2 host runtime and manifest tests");
  assert.ok(commands.some(({command,args}) => command === "go" && args[0] === "test" && args.includes("./internal/vmguest") && args.some(arg => arg.includes("ChildContractClosed"))), "Windows executes portable v2 child contracts");
  for(const boundary of ['LateInput','Publication','Receive'])assert.ok(commands.some(({command,args})=>command==='go'&&args[0]==='test'&&args.includes('./internal/vmguest')&&args.some(arg=>arg.includes(boundary))), 'Windows executes v2 '+boundary+' boundary regressions');
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
for (const target of ["desktop/test/runtime-v2.test.cjs", "desktop/test/packaging-v2.test.cjs", "desktop/test/multilang-inputs.test.cjs", "desktop/test/runner-contracts-v2.test.cjs", "desktop/test/build-guest-v2.test.cjs", "./internal/dependencybundle", "./internal/frameworkreport", "./internal/domain"]) {
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

function assertNativeV2Wiring(workflow,driver){
 const job=workflow.jobs['windows-installer'],steps=job.steps;
 const installed=steps.find(s=>s.id==='installed-contract'),index=steps.indexOf(installed);
 assert.ok(installed&&!installed.if&&!installed['continue-on-error']);
 assert.match(installed.run,/-StableInstaller /);assert.match(installed.run,/-StableUpgradeFixture /);
 const source=steps.find(s=>s.with?.path==='release-stable-source');assert.equal(source?.with.ref,'ab89b5a88f73755e91aaefa5b73b1dfd5c73fdb0');assert.equal(source.with['persist-credentials'],false);
 assert.equal((driver.match(/native-vm-smoke\.cjs'\) --v2-matrix/g)||[]).length,1);
 assert.equal((driver.match(/\$vmResult = & \$node .*native-vm-smoke\.cjs'\)\s*\n/g)||[]).length,1);
 const upload=steps.find(s=>s.with?.name==='foundry-windows-x64');assert.ok(steps.indexOf(upload)>index);assert.equal(upload.if,undefined);assert.equal(upload['continue-on-error'],undefined);
 for(const row of require('../native-vm-v2.cjs').nativeV2Cases()){const name='native-vm-v2-'+row.profile.replace(/^c4_/,'').replace(/_model_v2$/,'')+'-'+row.case+'.json';assert.ok(upload.with.path.includes(name),'missing gated receipt '+name)}
 const diagnostic=steps.find(s=>s.with?.name==='foundry-windows-native-diagnostics');assert.match(diagnostic.if,/failure\(\)/);assert.ok(steps.indexOf(diagnostic)>index);assert.equal(diagnostic.with['if-no-files-found'],'ignore');
 for(const file of diagnostic.with.path.trim().split(/\r?\n/)){assert.match(file,/\/native-vm-(?:acceptance|v2-(?:node|typescript|python)-(?:workflow|typecheck|setup|discovery|memory|pids|timeout|signal|descendant|held_stream))\.json$/)}
 assert.match(upload.with.path,/upgrade-stable-acceptance\.json/);
 assert.match(driver,/windows-upgrade-smoke\.ps1/);
}
const nativeWorkflow=yaml.load(fs.readFileSync(path.join(__dirname,'../../.github/workflows/desktop.yml'),'utf8'));
const installedDriver=fs.readFileSync(path.join(__dirname,'../windows-installed-smoke.ps1'),'utf8');
test('Windows v2 native matrix and independent stable supplement are mandatory before distribution upload',()=>assertNativeV2Wiring(nativeWorkflow,installedDriver));
for(const [name,mutate] of [
 ['conditional installed gate',v=>v.jobs['windows-installer'].steps.find(s=>s.id==='installed-contract').if='false'],
 ['missing stable source',v=>v.jobs['windows-installer'].steps=v.jobs['windows-installer'].steps.filter(s=>s.with?.path!=='release-stable-source')],
 ['missing v2 receipt',v=>{const s=v.jobs['windows-installer'].steps.find(s=>s.with?.name==='foundry-windows-x64');s.with.path=s.with.path.replace(/^.*native-vm-v2-python-memory\.json.*\n/m,'')}],
 ['ungated distribution artifact',v=>v.jobs['windows-installer'].steps.find(s=>s.with?.name==='foundry-windows-x64').if='always()'],
 ['broad failure upload',v=>v.jobs['windows-installer'].steps.find(s=>s.with?.name==='foundry-windows-native-diagnostics').with.path+='\n${{ runner.temp }}/private-data/**'],
])test('Windows native wiring rejects '+name,()=>{assertNativeV2Wiring(nativeWorkflow,installedDriver);const changed=structuredClone(nativeWorkflow);mutate(changed);assert.throws(()=>assertNativeV2Wiring(changed,installedDriver))});
