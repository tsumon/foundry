const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const yaml = require("js-yaml");

function assertPushPolicy(workflow) {
  // A branches-only filter accepts main and excludes feature branches and all tags.
  // Comparing the parsed mapping also rejects additional tag/path filters.
  assert.deepEqual(workflow.on.push, { branches: ["main"] });
}

function assertPullRequestPolicy(workflow) {
  assert.ok(Object.hasOwn(workflow.on, "pull_request"));
  assert.equal(workflow.on.pull_request, null);
}

function assertConcurrencyPolicy(workflow) {
  assert.deepEqual(workflow.concurrency, {
    group: "${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}",
    "cancel-in-progress": true,
  });
}

for (const file of ["verify.yml", "desktop.yml"]) {
  const workflow = yaml.load(
    fs.readFileSync(path.join(__dirname, "../../.github/workflows", file), "utf8"),
  );

  test(`${file}: main pushes run; feature-branch pushes and release tags do not`, () => {
    assertPushPolicy(workflow);
  });

  test(`${file}: pull requests retain unfiltered update checks`, () => {
    assertPullRequestPolicy(workflow);
    assert.deepEqual(
      Object.keys(workflow.on).sort(),
      file === "desktop.yml"
        ? ["pull_request", "push", "workflow_dispatch"]
        : ["pull_request", "push"],
    );
  });

  test(`${file}: superseded runs cancel within their workflow and PR or ref`, () => {
    assertConcurrencyPolicy(workflow);
  });

  const mutations = [
    ["unrestricted pushes", assertPushPolicy, (value) => { value.on.push = null; }],
    ["feature-branch pushes", assertPushPolicy, (value) => { value.on.push.branches = ["**"]; }],
    ["release tag triggers", assertPushPolicy, (value) => { value.on.push.tags = ["v*"]; }],
    ["removed PR checks", assertPullRequestPolicy, (value) => { delete value.on.pull_request; }],
    ["filtered PR checks", assertPullRequestPolicy, (value) => { value.on.pull_request = { types: ["opened"] }; }],
    ["shared concurrency group", assertConcurrencyPolicy, (value) => {
      value.concurrency.group = "${{ github.event.pull_request.number || github.ref }}";
    }],
    ["cross-workflow cancellation", assertConcurrencyPolicy, (value) => {
      value.concurrency.group = "Verify-${{ github.event.pull_request.number || github.ref }}";
    }],
    ["disabled superseded-run cancellation", assertConcurrencyPolicy, (value) => {
      value.concurrency["cancel-in-progress"] = false;
    }],
  ];
  for (const [name, check, mutate] of mutations) {
    test(`${file}: policy rejects ${name}`, () => {
      check(workflow);
      const changed = structuredClone(workflow);
      mutate(changed);
      assert.throws(() => check(changed), assert.AssertionError);
    });
  }

  if (file === "desktop.yml") {
    test("Desktop manual dispatch retains the explicit runtime source selection input", () => {
      assert.deepEqual(workflow.on.workflow_dispatch, {
        inputs: {
          runtime_sources_run_id: {
            description: "Optional same-repository run containing foundry-runtime-sources; every input is reverified against this checkout",
            required: false,
            default: "",
            type: "string",
          },
        },
      });
    });
  }
}
