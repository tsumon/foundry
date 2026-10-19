#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { constants } from 'node:fs';
import {
  chmod,
  lstat,
  mkdir,
  open,
  readFile,
  realpath,
  rm,
  writeFile,
} from 'node:fs/promises';
import { basename, dirname, isAbsolute, join, posix, relative, resolve, sep } from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const MAX_MANIFEST_BYTES = 1024 * 1024;
const MAX_MANIFEST_ENTRIES = 10_000;
const MAX_PATH_BYTES = 1024;
const MAX_ERROR_LENGTH = 500;
const RESERVED_SEGMENTS = new Set([
  '.foundry',
  '.git',
  '.superpowers',
  'bin',
  'dist',
  'node_modules',
]);

class ExportError extends Error {}

function fail(message) {
  throw new ExportError(message);
}

async function lstatOrNull(path) {
  try {
    return await lstat(path);
  } catch (error) {
    if (error?.code === 'ENOENT') return null;
    throw error;
  }
}

function isWithin(parent, child) {
  const pathFromParent = relative(parent, child);
  return pathFromParent === '' || (!pathFromParent.startsWith(`..${sep}`) && pathFromParent !== '..' && !isAbsolute(pathFromParent));
}

function parseManifest(contents) {
  if (contents.length > MAX_MANIFEST_BYTES) fail('manifest exceeds the 1 MiB limit');

  let text;
  try {
    text = new TextDecoder('utf-8', { fatal: true }).decode(contents);
  } catch {
    fail('manifest is not valid UTF-8');
  }

  const paths = [];
  const exactPaths = new Set();
  const portablePaths = new Set();
  for (const rawLine of text.split(/\r?\n/u)) {
    if (rawLine === '') continue;
    const path = rawLine;
    if (Buffer.byteLength(path) > MAX_PATH_BYTES) fail('manifest path exceeds the 1024-byte limit');
    if (path.includes('\\') || posix.isAbsolute(path)) fail('manifest paths must be relative POSIX paths');
    if (/\p{Cc}/u.test(path)) fail('manifest paths must not contain control characters');

    const segments = path.split('/');
    if (segments.some((segment) => segment === '' || segment === '.' || segment === '..')) {
      fail('manifest paths must not contain empty, dot, or traversal segments');
    }
    if (segments.some((segment) => RESERVED_SEGMENTS.has(segment.toLowerCase()))) {
      fail('manifest path enters a reserved directory');
    }
    if (exactPaths.has(path)) fail('manifest contains a duplicate path');

    const portablePath = path.normalize('NFC').toLowerCase();
    if (portablePaths.has(portablePath)) fail('manifest contains paths that collide on macOS');
    exactPaths.add(path);
    portablePaths.add(portablePath);
    paths.push(path);
    if (paths.length > MAX_MANIFEST_ENTRIES) fail('manifest exceeds the 10000-entry limit');
  }

  if (paths.length === 0) fail('manifest contains no files');
  return paths.sort((left, right) => Buffer.from(left).compare(Buffer.from(right)));
}

function sameFile(before, after) {
  return before.dev === after.dev
    && before.ino === after.ino
    && before.mode === after.mode
    && before.size === after.size
    && before.mtimeNs === after.mtimeNs;
}

async function readSourceEntry(sourceRoot, path) {
  const segments = path.split('/');
  let current = sourceRoot;
  for (let index = 0; index < segments.length; index += 1) {
    current = join(current, segments[index]);
    const entry = await lstatOrNull(current);
    if (!entry) fail(`manifest entry does not exist: ${path}`);
    if (entry.isSymbolicLink()) fail(`manifest entry crosses a symbolic link: ${path}`);
    if (index < segments.length - 1 && !entry.isDirectory()) {
      fail(`manifest path parent is not a directory: ${path}`);
    }
    if (index === segments.length - 1 && !entry.isFile()) {
      fail(`manifest entry is not a regular file: ${path}`);
    }
  }

  let handle;
  try {
    handle = await open(current, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const before = await handle.stat({ bigint: true });
    if (!before.isFile()) fail(`manifest entry is not a regular file: ${path}`);
    const contents = await handle.readFile();
    const after = await handle.stat({ bigint: true });
    if (!sameFile(before, after)) fail(`manifest entry changed while it was read: ${path}`);
    const mode = Number(before.mode & 0o777n);
    return {
      path,
      mode,
      contents,
      sha256: createHash('sha256').update(contents).digest('hex'),
    };
  } catch (error) {
    if (error instanceof ExportError) throw error;
    fail(`unable to read manifest entry: ${path}`);
  } finally {
    await handle?.close();
  }
}

function buildSummary(entries) {
  const tree = createHash('sha256');
  const files = entries.map(({ path, mode, sha256 }) => {
    const modeText = mode.toString(8);
    tree.update(path);
    tree.update('\0');
    tree.update(modeText);
    tree.update('\0');
    tree.update(sha256);
    tree.update('\n');
    return { path, mode: modeText, sha256 };
  });
  return {
    fileCount: files.length,
    treeSha256: tree.digest('hex'),
    files,
  };
}

async function exportAlpha(sourceArgument, manifestArgument, destinationArgument) {
  const requestedSource = resolve(sourceArgument);
  const sourceStat = await lstatOrNull(requestedSource);
  if (!sourceStat?.isDirectory() || sourceStat.isSymbolicLink()) fail('source root must be a real directory');
  const sourceRoot = await realpath(requestedSource);

  const manifestPath = resolve(manifestArgument);
  const manifestStat = await lstatOrNull(manifestPath);
  if (!manifestStat?.isFile() || manifestStat.isSymbolicLink()) fail('manifest must be a real regular file');

  const requestedDestination = resolve(destinationArgument);
  const destinationParent = await realpath(dirname(requestedDestination));
  const destination = join(destinationParent, basename(requestedDestination));
  if (isWithin(sourceRoot, destination)) fail('destination must be outside the source root');
  if (await lstatOrNull(destination)) fail('destination already exists');

  const manifest = await readFile(manifestPath);
  const paths = parseManifest(manifest);
  const entries = [];
  for (const path of paths) entries.push(await readSourceEntry(sourceRoot, path));
  const summary = buildSummary(entries);

  let destinationCreated = false;
  try {
    await mkdir(destination);
    destinationCreated = true;
    for (const entry of entries) {
      const outputPath = join(destination, ...entry.path.split('/'));
      await mkdir(dirname(outputPath), { recursive: true });
      await writeFile(outputPath, entry.contents, { flag: 'wx', mode: entry.mode });
      await chmod(outputPath, entry.mode);
    }
  } catch (error) {
    if (destinationCreated) await rm(destination, { recursive: true, force: true });
    if (error instanceof ExportError) throw error;
    fail('unable to create export destination');
  }

  return summary;
}

function boundedError(error) {
  const message = error instanceof ExportError ? error.message : 'unexpected export failure';
  return message.slice(0, MAX_ERROR_LENGTH);
}

async function main() {
  if (process.argv.length !== 5) {
    process.stderr.write('usage: node scripts/export-alpha.mjs <source-root> <manifest-file> <new-destination>\n');
    process.exitCode = 2;
    return;
  }
  try {
    const summary = await exportAlpha(process.argv[2], process.argv[3], process.argv[4]);
    process.stdout.write(`${JSON.stringify(summary)}\n`);
  } catch (error) {
    process.stderr.write(`export-alpha: ${boundedError(error)}\n`);
    process.exitCode = 1;
  }
}

const invokedPath = process.argv[1] ? resolve(process.argv[1]) : '';
if (invokedPath === fileURLToPath(import.meta.url)) await main();

export { exportAlpha };
