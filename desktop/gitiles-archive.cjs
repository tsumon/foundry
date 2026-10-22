"use strict";
// Build-time verification only: no extraction and no archive-provided code execution.
const { gunzipSync } = require("node:zlib");
const { createHash } = require("node:crypto");
const { Header } = require("tar");
const utf8 = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true });
const MAX_TAR_BYTES = 256 * 1024 * 1024;
function fail(reason) {
  throw Error("Runtime source integrity failure: Gitiles tar " + reason);
}
function text(field) {
  const zero = field.indexOf(0);
  if (zero >= 0 && field.subarray(zero).some((b) => b !== 0))
    fail("string padding");
  try {
    return utf8.decode(zero < 0 ? field : field.subarray(0, zero));
  } catch {
    fail("UTF-8");
  }
}
function octal(field, empty = false) {
  if (empty && field.every((b) => b === 0)) return 0;
  const value = field.toString("latin1");
  if (!/^[0-7]+[\0 ]*$/.test(value)) fail("numeric field");
  const result = Number.parseInt(value, 8);
  if (!Number.isSafeInteger(result) || result < 0) fail("numeric range");
  return result;
}
function sourcePath(name, type) {
  if (Buffer.byteLength(name, "utf8") > 4096) fail("path bound");
  if (
    !name ||
    /[\x00-\x1f\x7f\\:]/.test(name) ||
    name.startsWith("/") ||
    name !== name.normalize("NFC")
  )
    fail("path");
  if (type === "0" && name.endsWith("/")) fail("file path");
  const identity =
    type === "5" && name.endsWith("/") ? name.slice(0, -1) : name;
  const parts = identity.split("/");
  if (parts.length > 128) fail("path bound");
  if (parts.some((part) => !part || part === "." || part === ".."))
    fail("path alias");
  return identity;
}
function paxFields(data) {
  if (!data.length || data.length > 1024 * 1024) fail("PAX size");
  const fields = Object.create(null);
  for (let offset = 0; offset < data.length;) {
    const space = data.indexOf(32, offset);
    if (space < 0) fail("PAX length");
    const digits = data.toString("latin1", offset, space);
    if (!/^[1-9][0-9]{0,6}$/.test(digits)) fail("PAX length");
    const length = Number(digits),
      end = offset + length;
    if (end > data.length || end <= space + 2 || data[end - 1] !== 10)
      fail("PAX record");
    let record;
    try {
      record = utf8.decode(data.subarray(space + 1, end - 1));
    } catch {
      fail("PAX UTF-8");
    }
    const equal = record.indexOf("="),
      key = record.slice(0, equal),
      value = record.slice(equal + 1);
    if (
      equal < 1 ||
      !["mtime", "path"].includes(key) ||
      Object.hasOwn(fields, key)
    )
      fail("PAX key");
    if (
      key === "mtime" &&
      (value.length > 64 || !/^-?(0|[1-9][0-9]*)(\.[0-9]+)?$/.test(value))
    )
      fail("PAX mtime");
    fields[key] = value;
    offset = end;
  }
  return fields;
}
function archiveIdentity(raw) {
  let tar;
  try {
    tar = gunzipSync(raw, { maxOutputLength: MAX_TAR_BYTES });
  } catch {
    fail("decompression or 256 MiB bound");
  }
  if (tar.length % 512 !== 0) fail("block length");
  const records = [],
    names = new Map();
  let pending,
    fileBytes = 0,
    rawHeaders = 0,
    ended = false;
  for (let offset = 0; offset < tar.length;) {
    const block = tar.subarray(offset, offset + 512);
    if (block.every((b) => b === 0)) {
      if (pending || tar.length - offset < 1024)
        fail("end markers or orphan PAX");
      if (tar.subarray(offset).some((b) => b !== 0)) fail("trailing data");
      ended = true;
      break;
    }
    if (++rawHeaders > 100000) fail("header count bound");
    const type = String.fromCharCode(block[156]);
    if (!["0", "5", "x"].includes(type)) fail("entry type");
    if (
      block.toString("latin1", 257, 265) !== "ustar\0" + "00" ||
      block.subarray(500).some((b) => b !== 0)
    )
      fail("header format");
    const name = text(block.subarray(0, 100)),
      prefix = text(block.subarray(345, 500));
    const rawPath = prefix ? prefix + "/" + name : name;
    const mode = octal(block.subarray(100, 108)),
      uid = octal(block.subarray(108, 116)),
      gid = octal(block.subarray(116, 124));
    const size = octal(block.subarray(124, 136));
    octal(block.subarray(136, 148)); // mtime syntax is checked, but its value is not identity.
    octal(block.subarray(148, 156));
    const uname = text(block.subarray(265, 297)),
      gname = text(block.subarray(297, 329));
    if (
      text(block.subarray(157, 257)) ||
      octal(block.subarray(329, 337), true) ||
      octal(block.subarray(337, 345), true)
    )
      fail("link/device metadata");
    if (type === "5" && size !== 0) fail("directory data");
    if (size > MAX_TAR_BYTES || (type === "x" && size > 1024 * 1024))
      fail("entry size bound");
    // Never give PAX to Header: avoid its directory coercion and extension merging.
    const header = new Header(block);
    if (!header.cksumValid) fail("checksum");
    if (
      header.path !== rawPath ||
      header.mode !== mode ||
      header.uid !== uid ||
      header.gid !== gid ||
      header.size !== size ||
      header.uname !== uname ||
      header.gname !== gname ||
      header.atime !== undefined ||
      header.ctime !== undefined
    )
      fail("header interpretation");
    const start = offset + 512,
      end = start + size,
      next = start + Math.ceil(size / 512) * 512;
    if (next > tar.length) fail("truncated data");
    if (tar.subarray(end, next).some((b) => b !== 0)) fail("body padding");
    const data = tar.subarray(start, end);
    if (type === "x") {
      // Gitiles' fixed PAX wrapper is not a filesystem entry; it may only carry
      // path/mtime. Refuse other wrapper metadata rather than silently discard it.
      if (
        pending ||
        prefix ||
        !/^\.\/PaxHeaders\.X\/[^/\\:\x00-\x1f\x7f]+$/.test(name) ||
        mode !== 0o100644 ||
        uid ||
        gid ||
        uname ||
        gname
      )
        fail("PAX wrapper");
      pending = paxFields(data);
    } else {
      sourcePath(rawPath, type);
      const effectivePath = pending?.path ?? rawPath;
      const identity = sourcePath(effectivePath, type);
      if (names.has(identity)) fail("duplicate path");
      names.set(identity, type);
      records.push([
        effectivePath,
        type,
        mode,
        uid,
        gid,
        uname,
        gname,
        size,
        createHash("sha256").update(data).digest("hex"),
      ]);
      if (type === "0") fileBytes += size;
      pending = undefined;
    }
    offset = next;
  }
  if (!ended || pending || !records.length) fail("missing end or entries");
  for (const name of names.keys()) {
    const parts = name.split("/");
    for (let i = 1; i < parts.length; i++)
      if (names.get(parts.slice(0, i).join("/")) === "0")
        fail("file is parent");
  }
  records.sort((a, b) => Buffer.compare(Buffer.from(a[0]), Buffer.from(b[0])));
  const hash = createHash("sha256").update("foundry-gitiles-tar-v1\n");
  for (const record of records) hash.update(JSON.stringify(record) + "\n");
  return {
    format: "gitiles-tar-v1",
    sha256: hash.digest("hex"),
    entries: records.length,
    fileBytes,
  };
}
module.exports = { archiveIdentity };
