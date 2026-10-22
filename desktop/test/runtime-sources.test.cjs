const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const crypto = require("node:crypto");
const {
  prepareSources,
  verifyInput,
  download,
} = require("../runtime-sources.cjs");
const bytes = Buffer.from("exact unmodified source");
const entry = {
  file: "source.tar.gz",
  bytes: bytes.length,
  sha256: crypto.createHash("sha256").update(bytes).digest("hex"),
  url: "https://example.invalid/source",
};
test("runtime source integrity is byte and size bound", () => {
  assert.doesNotThrow(() => verifyInput(entry, bytes));
  assert.throws(() => verifyInput(entry, Buffer.from("altered")));
  assert.throws(() => verifyInput({ ...entry, file: "../escape" }, bytes));
});
test("local input prepares a fresh complete sidecar without network", async (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "foundry-sources-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const local = path.join(root, "input");
  fs.mkdirSync(local);
  fs.writeFileSync(path.join(local, entry.file), bytes);
  const output = path.join(root, "output");
  await prepareSources({
    output,
    local,
    manifest: { files: [entry] },
    readme: "Source instructions",
  });
  assert.deepEqual(fs.readFileSync(path.join(output, entry.file)), bytes);
  assert.match(
    fs.readFileSync(path.join(output, "SHA256SUMS"), "utf8"),
    new RegExp(entry.sha256),
  );
  await assert.rejects(
    prepareSources({ output, local, manifest: { files: [entry] }, readme: "" }),
  );
});
test("tampered local source fails; no remote fallback or overwrite", async (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "foundry-sources-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const local = path.join(root, "input");
  fs.mkdirSync(local);
  fs.writeFileSync(path.join(local, entry.file), "bad");
  const output = path.join(root, "output");
  await assert.rejects(
    prepareSources({ output, local, manifest: { files: [entry] }, readme: "" }),
    /integrity/,
  );
  assert.equal(fs.existsSync(path.join(output, entry.file)), false);
});
test("invalid and reserved metadata fails before filesystem or transport", async (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "foundry-sources-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  for (const file of [
    "../escape",
    "manifest.json",
    "README.md",
    "SHA256SUMS",
  ]) {
    const output = path.join(root, "output");
    let called = false;
    await assert.rejects(
      prepareSources({
        output,
        manifest: { files: [{ ...entry, file }] },
        readme: "",
        fetchImpl: async () => {
          called = true;
          throw Error("must not fetch");
        },
      }),
    );
    assert.equal(called, false);
    assert.equal(fs.existsSync(output), false);
  }
});
test("transport is allowlisted, one-shot and redirect/size bounded offline", async () => {
  let calls = 0;
  const official = {
    ...entry,
    url: "https://raw.githubusercontent.com/owner/project/commit/source",
  };
  const fake = async (_url, options) => {
    calls++;
    assert.equal(options.redirect, "manual");
    assert.ok(options.signal instanceof AbortSignal);
    return {
      ok: true,
      body: (async function* () {
        yield bytes;
      })(),
    };
  };
  await assert.rejects(download(entry, fake), /origin/);
  assert.equal(calls, 0);
  assert.deepEqual(await download(official, fake), bytes);
  assert.equal(calls, 1);
  await assert.rejects(
    download(official, async () => ({ ok: false, status: 503 })),
    /503/,
  );
  await assert.rejects(
    download(official, async () => ({
      status: 302,
      headers: new Headers({ location: "https://untrusted.invalid/source" }),
    })),
    /origin/,
  );
  let redirects = 0;
  assert.deepEqual(
    await download(
      {
        ...official,
        url: "https://github.com/example/releases/download/1/source",
      },
      async () => {
        if (redirects++ === 0)
          return {
            status: 302,
            headers: new Headers({
              location: "https://release-assets.githubusercontent.com/source",
            }),
          };
        return {
          ok: true,
          body: (async function* () {
            yield bytes;
          })(),
        };
      },
    ),
    bytes,
  );
  assert.equal(redirects, 2);
  await assert.rejects(
    download(official, async () => ({
      ok: true,
      body: (async function* () {
        yield Buffer.alloc(bytes.length + 1);
      })(),
    })),
    /bound/,
  );
});
