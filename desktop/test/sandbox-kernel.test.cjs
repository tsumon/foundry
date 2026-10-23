"use strict";
const test = require("node:test"), assert = require("node:assert/strict"), zlib = require("node:zlib");
const { arm64Kernel } = require("../sandbox/kernel.cjs");
function fixture(change = () => {}) {
  const image = Buffer.alloc(4096); image.writeUInt32LE(0x644d5241, 56); image.writeBigUInt64LE(4096n, 16); change(image);
  const gzip = zlib.gzipSync(image), header = Buffer.alloc(256);
  header.write("MZ"); header.write("zimg", 4); header.writeUInt32LE(header.length, 8); header.writeUInt32LE(gzip.length, 12); header.write("gzip", 24);
  header.writeUInt32LE(64, 60); header.write("PE\0\0", 64); header.writeUInt16LE(0xaa64, 68);
  return { image, wrapped: Buffer.concat([header, gzip]) };
}
test("extracts declared gzip payload from ARM64 EFI zboot into a direct-boot Image", () => {
  const { image, wrapped } = fixture(); assert.deepEqual(arm64Kernel(wrapped), image);
});
test("rejects incorrect machine, payload bounds, compression and missing Image header", () => {
  for (const mutate of [b => b.writeUInt16LE(0x8664, 68), b => b.writeUInt32LE(8, 8), b => b.writeUInt32LE(0xffffffff, 12), b => b.write("zstd", 24), b => b.write("ABCD", 4), b => b.fill(0, 64, 68)]) {
    const { wrapped } = fixture(); mutate(wrapped); assert.throws(() => arm64Kernel(wrapped));
  }
  const { wrapped } = fixture(); wrapped[wrapped.length - 1] ^= 1; assert.throws(() => arm64Kernel(wrapped));
  assert.throws(() => arm64Kernel(Buffer.alloc(64)));
});
test("rejects valid gzip with an invalid decompressed Image header or declared size", () => {
  for (const change of [b => b.writeUInt32LE(0, 56), b => b.writeBigUInt64LE(0n, 16), b => b.writeBigUInt64LE(129n * 1024n * 1024n, 16)]) {
    assert.throws(() => arm64Kernel(fixture(change).wrapped), /direct-boot/);
  }
});
