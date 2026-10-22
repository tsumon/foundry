"use strict";
const { test } = require("node:test");
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const zlib = require("node:zlib");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const {
  verifyInput,
  prepareSources,
  download,
} = require("../runtime-sources.cjs");
const { verifyFiles } = require("../windows-provenance.cjs");
const { archiveIdentity } = require("../gitiles-archive.cjs");
const sha = (data) => crypto.createHash("sha256").update(data).digest("hex");
function checksum(h) {
  h.fill(32, 148, 156);
  h.write(
    [...h]
      .reduce((a, b) => a + b, 0)
      .toString(8)
      .padStart(6, "0") + "\0 ",
    148,
    "ascii",
  );
  return h;
}
function member(name = "hello.txt", data = "hello", options = {}) {
  data = Buffer.from(data);
  const h = Buffer.alloc(512);
  h.write(name);
  for (const [offset, width, value] of [
    [100, 8, options.mode ?? 0o100644],
    [108, 8, options.uid ?? 0],
    [116, 8, options.gid ?? 0],
    [124, 12, data.length],
    [136, 12, options.mtime ?? 123],
    [329, 8, 0],
    [337, 8, 0],
  ])
    h.write(value.toString(8).padStart(width - 1, "0") + " ", offset, "ascii");
  h.write(options.type ?? "0", 156, "ascii");
  h.write("ustar\0" + "00", 257, "ascii");
  h.write(options.uname ?? "", 265);
  h.write(options.gname ?? "", 297);
  options.edit?.(h);
  return Buffer.concat([
    checksum(h),
    data,
    Buffer.alloc((512 - (data.length % 512)) % 512),
  ]);
}
function pax(key, value) {
  const body = ` ${key}=${value}\n`;
  let length = Buffer.byteLength(body) + 1;
  while (String(length).length + Buffer.byteLength(body) !== length)
    length = String(length).length + Buffer.byteLength(body);
  return `${length}${body}`;
}
const pack = (...members) =>
  zlib.gzipSync(Buffer.concat([...members, Buffer.alloc(1024)]));
const original = pack(member());
// Independently specified canonical record, not produced by the verifier.
const canonical = {
  format: "gitiles-tar-v1",
  entries: 1,
  fileBytes: 5,
  sha256: sha(
    "foundry-gitiles-tar-v1\n" +
      JSON.stringify([
        "hello.txt",
        "0",
        0o100644,
        0,
        0,
        "",
        "",
        5,
        "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
      ]) +
      "\n",
  ),
};
const pin = {
  file: "fixture.tar.gz",
  bytes: original.length,
  sha256: sha(original),
  canonical,
  maxDownloadBytes: 64 * 1024 * 1024,
};

test("Gitiles timestamp-only regeneration returns the actual transport receipt", () => {
  const fresh = pack(
    member("./PaxHeaders.X/hello.txt", pax("mtime", "999.0123"), { type: "x" }),
    member("hello.txt", "hello", { mtime: 999 }),
  );
  assert.notEqual(sha(fresh), pin.sha256);
  assert.deepEqual(verifyInput(pin, fresh), {
    file: pin.file,
    bytes: fresh.length,
    sha256: sha(fresh),
  });
  assert.doesNotThrow(() => verifyInput(pin, original));
});
test("canonical pins bind content and every non-time filesystem metadata field", () => {
  for (const changed of [
    member("other.txt"),
    member("hello.txt", "jello"),
    ...[
      { mode: 0o100755 },
      { uid: 1 },
      { gid: 1 },
      { uname: "owner" },
      { gname: "group" },
    ].map((o) => member("hello.txt", "hello", o)),
    member("hello.txt/", "", { type: "5", mode: 0o40755 }),
  ])
    assert.throws(() => verifyInput(pin, pack(changed)), /integrity/);
  for (const change of [
    { entries: 2 },
    { fileBytes: 6 },
    { sha256: "a".repeat(64) },
  ])
    assert.throws(
      () =>
        verifyInput(
          { ...pin, canonical: { ...canonical, ...change } },
          original,
        ),
      /expected.*observed/,
    );
});
test("strict tar admission refuses aliases, extensions, type tricks and malformed header fields", () => {
  const bad = [
    ...[
      "../x",
      "/x",
      "a/../x",
      "a//x",
      "./x",
      "a\\x",
      "C:x",
      "hello.txt/",
      "a/./x",
    ].map((n) => member(n)),
    ...["1", "2", "g", "L", "K", "7", "\0"].map((type) =>
      member("hello.txt", "hello", { type }),
    ),
    member("dir/", "hidden", { type: "5" }),
    member("hello.txt", "hello", { edit: (h) => h.write("link", 157) }),
    member("hello.txt", "hello", { edit: (h) => h.write("1", 335) }),
    member("hello.txt", "hello", { edit: (h) => h.write("gnu", 257) }),
    member("hello.txt", "hello", { edit: (h) => (h[500] = 1) }),
    member("hello.txt", "hello", { edit: (h) => (h[105] = 57) }),
    member("hello.txt", "hello", { edit: (h) => (h[0] = 255) }),
    member("hello.txt", "hello", {
      edit: (h) => {
        h[1] = 0;
        h[2] = 120;
      },
    }),
  ];
  for (const tarMember of bad)
    assert.throws(() => verifyInput(pin, pack(tarMember)), /Gitiles tar/);
  assert.throws(
    () => verifyInput(pin, pack(member(), member())),
    /duplicate|integrity/,
  );
  const corrupt = member();
  corrupt[148] ^= 1;
  assert.throws(() => verifyInput(pin, pack(corrupt)), /checksum|integrity/);
  const padding = member();
  padding[520] = 1;
  assert.throws(() => verifyInput(pin, pack(padding)), /padding|integrity/);
  assert.throws(
    () =>
      verifyInput(
        pin,
        zlib.gzipSync(Buffer.concat([member(), Buffer.alloc(512)])),
      ),
    /end|integrity/,
  );
  assert.throws(
    () =>
      verifyInput(
        pin,
        zlib.gzipSync(Buffer.concat([member(), Buffer.alloc(1024), member()])),
      ),
    /trailing|integrity/,
  );
});
test("local PAX paths apply exactly once with strict UTF8 and length/key grammar", () => {
  const px = (data) => member("./PaxHeaders.X/hello.txt", data, { type: "x" });
  assert.doesNotThrow(() =>
    verifyInput(
      pin,
      pack(
        px(pax("path", "hello.txt") + pax("mtime", "123.0")),
        member("placeholder"),
      ),
    ),
  );
  for (const data of [
    pax("path", "../hello.txt"),
    pax("path", "/hello.txt"),
    pax("path", "a\\hello.txt"),
    pax("size", "5"),
    pax("uid", "0"),
    pax("mtime", "NaN"),
    pax("mtime", "1") + pax("mtime", "2"),
    "99 mtime=1\n",
    "011 mtime=1\n",
    "11 mtime=1!",
    Buffer.from([0xff]),
    "",
  ])
    assert.throws(
      () => verifyInput(pin, pack(px(data), member())),
      /integrity/,
    );
  assert.throws(
    () =>
      verifyInput(
        pin,
        pack(px(pax("mtime", "1")), px(pax("mtime", "2")), member()),
      ),
    /integrity/,
  );
  assert.throws(
    () => verifyInput(pin, pack(px(pax("mtime", "1")))),
    /integrity/,
  );
  assert.throws(
    () =>
      verifyInput(pin, pack(px(pax("path", "hello.txt")), member(), member())),
    /integrity/,
  );
});
test("source identity sorts bytewise, includes directories, and rejects file-parent and directory aliases", () => {
  const records = [
    ["A", "0", 0o100644, 0, 0, "", "", 5, sha("hello")],
    ["dir/", "5", 0o40755, 0, 0, "", "", 0, sha("")],
    ["dir/z", "0", 0o100644, 0, 0, "", "", 5, sha("hello")],
    ["a", "0", 0o100644, 0, 0, "", "", 5, sha("hello")],
  ];
  // UTF-8 bytes order A, a, dir/, dir/z (not locale-sensitive collation).
  const expected = {
    format: "gitiles-tar-v1",
    entries: 4,
    fileBytes: 15,
    sha256: sha(
      "foundry-gitiles-tar-v1\n" +
        [records[0], records[3], records[1], records[2]]
          .map((r) => JSON.stringify(r) + "\n")
          .join(""),
    ),
  };
  const members = [
    member("dir/z"),
    member("a"),
    member("dir/", "", { type: "5", mode: 0o40755 }),
    member("A"),
  ];
  assert.deepEqual(archiveIdentity(pack(...members)), expected);
  assert.deepEqual(archiveIdentity(pack(...members.reverse())), expected);
  for (const members of [
    [member("a"), member("a/b")],
    [member("a/b"), member("a")],
    [member("a"), member("a/", "", { type: "5" })],
    [member("dir", "", { type: "5" }), member("dir/", "", { type: "5" })],
  ])
    assert.throws(
      () => archiveIdentity(pack(...members)),
      /file is parent|duplicate path/,
    );
});
test("USTAR prefix is decoded completely and cannot hide alternate time extensions", () => {
  const prefixed = member("hello.txt", "hello", {
    edit: (h) => h.write("dir", 345),
  });
  assert.deepEqual(archiveIdentity(pack(prefixed)), {
    ...canonical,
    sha256: sha(
      "foundry-gitiles-tar-v1\n" +
        JSON.stringify([
          "dir/hello.txt",
          "0",
          0o100644,
          0,
          0,
          "",
          "",
          5,
          sha("hello"),
        ]) +
        "\n",
    ),
  });
  for (const edit of [
    (h) => h.write("00000000123 ", 476),
    (h) => {
      h.write("dir", 345);
      h[490] = 1;
    },
    (h) => (h[108] = 0x80),
    (h) => h.write("0000000x", 100),
    (h) => h.write("../", 345),
  ])
    assert.throws(
      () => archiveIdentity(pack(member("hello.txt", "hello", { edit }))),
      /integrity/,
    );
});
test("PAX path byte length and depth are bounded before ancestor processing", () => {
  for (const name of [
    "a".repeat(4097),
    "é".repeat(2049),
    Array(129).fill("a").join("/"),
  ]) {
    const extended = member("./PaxHeaders.X/path", pax("path", name), {
      type: "x",
    });
    assert.throws(
      () => archiveIdentity(pack(extended, member())),
      /path bound/,
    );
  }
});
test("decompression and canonical metadata are bounded before any filesystem/network output", async (t) => {
  const bomb = zlib.gzipSync(Buffer.alloc(256 * 1024 * 1024 + 1));
  assert.throws(() => verifyInput(pin, bomb), /integrity/);
  const root = fs.mkdtempSync(
    path.join(os.tmpdir(), "foundry-canonical-invalid-"),
  );
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  for (const change of [
    { canonical: {} },
    { canonical: { ...canonical, format: "other" } },
    { canonical: { ...canonical, entries: 0 } },
    { canonical: { ...canonical, fileBytes: -1 } },
    { canonical: { ...canonical, sha256: "bad" } },
    { maxDownloadBytes: Infinity },
    { maxDownloadBytes: 0 },
    { maxDownloadBytes: 65 * 1024 * 1024 },
    { encoding: "base64" },
  ]) {
    const output = path.join(root, "out");
    await assert.rejects(
      prepareSources({
        output,
        manifest: { files: [{ ...pin, ...change }] },
        readme: "",
        fetchImpl: () => assert.fail("must not fetch"),
      }),
      /metadata/,
    );
    assert.equal(fs.existsSync(output), false);
  }
});
test("canonical source transport uses a finite cap independent of old compressed length", async () => {
  const fresh = pack(member("hello.txt", "hello", { mtime: 999 }));
  const entry = { ...pin, url: "https://chromium.googlesource.com/example" };
  const result = await download(entry, async () => ({
    ok: true,
    body: (async function* () {
      yield fresh;
    })(),
  }));
  assert.deepEqual(result, fresh);
  await assert.rejects(
    download({ ...entry, maxDownloadBytes: original.length }, async () => ({
      ok: true,
      body: (async function* () {
        yield Buffer.alloc(original.length + 1);
      })(),
    })),
    /bound/,
  );
});
test("saved receipts and shipping checks use actual bytes but never trust a forged output manifest", async (t) => {
  const root = fs.mkdtempSync(
    path.join(os.tmpdir(), "foundry-canonical-receipt-"),
  );
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const local = path.join(root, "in"),
    output = path.join(root, "out");
  fs.mkdirSync(local);
  const fresh = pack(
    member("./PaxHeaders.X/hello.txt", pax("mtime", "999.12"), { type: "x" }),
    member("hello.txt", "hello", { mtime: 999 }),
  );
  fs.writeFileSync(path.join(local, pin.file), fresh);
  await prepareSources({
    output,
    local,
    manifest: { files: [pin] },
    readme: "source",
  });
  const receipt = JSON.parse(
    fs.readFileSync(path.join(output, "manifest.json")),
  ).files[0];
  assert.equal(receipt.bytes, fresh.length);
  assert.equal(receipt.sha256, sha(fresh));
  assert.deepEqual(receipt.originalTransport, {
    bytes: pin.bytes,
    sha256: pin.sha256,
  });
  assert.equal(
    fs.readFileSync(path.join(output, "SHA256SUMS"), "utf8"),
    sha(fresh) + "  " + pin.file + "\n",
  );
  assert.deepEqual(verifyFiles(output, [pin]), [
    { file: pin.file, bytes: fresh.length, sha256: sha(fresh) },
  ]);
  const evil = pack(member("hello.txt", "evil!"));
  fs.writeFileSync(path.join(output, pin.file), evil);
  fs.writeFileSync(
    path.join(output, "manifest.json"),
    JSON.stringify({
      files: [{ ...pin, bytes: evil.length, sha256: sha(evil) }],
    }),
  );
  assert.throws(() => verifyFiles(output, [pin]), /integrity/);
});
