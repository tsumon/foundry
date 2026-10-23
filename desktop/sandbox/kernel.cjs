"use strict";
const zlib = require("node:zlib");
// Build-time format conversion only, after the caller verifies the release
// archive digest. Layout: Linux v6.18 drivers/firmware/efi/libstub/zboot-header.S.
// The resulting digest is separately pinned in the installed VM manifest.
function arm64Kernel(input) {
  if (!Buffer.isBuffer(input) || input.length < 72 || input.length > 64 * 1024 * 1024 || input.toString("ascii", 0, 2) !== "MZ" || input.toString("ascii", 4, 8) !== "zimg" || input.toString("ascii", 24, 29) !== "gzip\0") throw Error("Unsupported ARM64 zboot image");
  const pe = input.readUInt32LE(60), start = input.readUInt32LE(8), size = input.readUInt32LE(12);
  if (pe < 64 || pe + 6 > start || input.toString("ascii", pe, pe + 4) !== "PE\0\0" || input.readUInt16LE(pe + 4) !== 0xaa64 || size < 18 || start + size > input.length) throw Error("Invalid ARM64 zboot payload");
  const image = zlib.gunzipSync(input.subarray(start, start + size), { maxOutputLength: 128 * 1024 * 1024 });
  if (image.length < 64 || image.readUInt32LE(56) !== 0x644d5241 || image.readBigUInt64LE(16) < BigInt(image.length) || image.readBigUInt64LE(16) > 128n * 1024n * 1024n) throw Error("Invalid direct-boot ARM64 Image");
  return image;
}
module.exports = { arm64Kernel };
