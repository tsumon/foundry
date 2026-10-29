import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtemp, mkdir, readFile, readdir, rm, stat, symlink, writeFile, chmod, lstat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const script = fileURLToPath(new URL('./export-alpha.mjs', import.meta.url));

async function withFixture(run) {
  const root = await mkdtemp(join(tmpdir(), 'foundry-alpha-export-'));
  const source = join(root, 'source');
  const manifest = join(root, 'manifest.txt');
  const destination = join(root, 'candidate');
  await mkdir(source);
  try {
    await run({ root, source, manifest, destination });
  } finally {
    await rm(root, { recursive: true, force: true });
  }
}

async function put(root, relativePath, contents, mode = 0o644) {
  const path = join(root, ...relativePath.split('/'));
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, contents, { mode });
  await chmod(path, mode);
  return path;
}

function exportAlpha(source, manifest, destination) {
  return spawnSync(process.execPath, [script, source, manifest, destination], {
    encoding: 'utf8',
    maxBuffer: 1024 * 1024,
  });
}

async function pathDoesNotExist(path) {
  await assert.rejects(stat(path), { code: 'ENOENT' });
}

async function listRegularFiles(root, relativeRoot = '') {
  const directory = join(root, ...relativeRoot.split('/').filter(Boolean));
  const names = await readdir(directory);
  const files = [];
  for (const name of names) {
    const relativePath = relativeRoot ? `${relativeRoot}/${name}` : name;
    const entry = await lstat(join(root, ...relativePath.split('/')));
    assert.equal(entry.isSymbolicLink(), false, `${relativePath} must not be a symlink`);
    if (entry.isDirectory()) files.push(...await listRegularFiles(root, relativePath));
    else {
      assert.equal(entry.isFile(), true, `${relativePath} must be a regular file`);
      files.push(relativePath);
    }
  }
  return files.sort();
}

test('rejects traversal before it can read outside the source', async () => {
  await withFixture(async ({ root, source, manifest, destination }) => {
    const outside = join(root, 'outside-secret.txt');
    await writeFile(outside, 'do-not-copy-this-secret');
    await writeFile(manifest, '../outside-secret.txt\n');

    const result = exportAlpha(source, manifest, destination);

    assert.notEqual(result.status, 0);
    assert.doesNotMatch(result.stderr, /do-not-copy-this-secret/);
    assert.equal(await readFile(outside, 'utf8'), 'do-not-copy-this-secret');
    await pathDoesNotExist(destination);
  });
});

test('rejects a manifest file reached through a symlink', async () => {
  await withFixture(async ({ root, source, manifest, destination }) => {
    const outside = join(root, 'outside.txt');
    await writeFile(outside, 'outside bytes');
    await symlink(outside, join(source, 'linked.txt'));
    await writeFile(manifest, 'linked.txt\n');

    const result = exportAlpha(source, manifest, destination);

    assert.notEqual(result.status, 0);
    assert.doesNotMatch(result.stderr, /outside bytes/);
    await pathDoesNotExist(destination);
  });
});

test('copies only listed files and does not leak omitted files', async () => {
  await withFixture(async ({ source, manifest, destination }) => {
    await put(source, 'included.txt', 'public bytes\n');
    await put(source, 'private-note.txt', 'not for release\n');
    for (const privatePath of ['.git/config', '.superpowers/sdd/report.md', 'docs/superpowers/plan.md', 'evidence/receipt.json']) {
      await put(source, privatePath, 'private evidence must remain omitted\n');
    }
    await writeFile(manifest, 'included.txt\n');

    const result = exportAlpha(source, manifest, destination);

    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(await readdir(destination), ['included.txt']);
    assert.equal(await readFile(join(destination, 'included.txt'), 'utf8'), 'public bytes\n');
    await pathDoesNotExist(join(destination, 'private-note.txt'));
  });
});

test('refuses to overwrite an existing target', async () => {
  await withFixture(async ({ source, manifest, destination }) => {
    await put(source, 'included.txt', 'new bytes\n');
    await writeFile(manifest, 'included.txt\n');
    await mkdir(destination);
    await writeFile(join(destination, 'sentinel.txt'), 'keep me');

    const result = exportAlpha(source, manifest, destination);

    assert.notEqual(result.status, 0);
    assert.deepEqual(await readdir(destination), ['sentinel.txt']);
    assert.equal(await readFile(join(destination, 'sentinel.txt'), 'utf8'), 'keep me');
  });
});

test('rejects duplicate manifest paths before creating the target', async () => {
  await withFixture(async ({ source, manifest, destination }) => {
    await put(source, 'same.txt', 'same bytes\n');
    await writeFile(manifest, 'same.txt\nsame.txt\n');

    const result = exportAlpha(source, manifest, destination);

    assert.notEqual(result.status, 0);
    await pathDoesNotExist(destination);
  });
});

test('preserves source execution permissions', async () => {
  await withFixture(async ({ source, manifest, destination }) => {
    await put(source, 'run.sh', '#!/bin/sh\nexit 0\n', 0o751);
    await writeFile(manifest, 'run.sh\n');

    const result = exportAlpha(source, manifest, destination);

    assert.equal(result.status, 0, result.stderr);
    assert.equal((await stat(join(destination, 'run.sh'))).mode & 0o777, 0o751);
  });
});

test('rejects reserved directories before creating the target', async (t) => {
  for (const privatePath of ['.git/config', '.superpowers/sdd/report.md', '.foundry/state.json']) {
    await t.test(privatePath, async () => withFixture(async ({ source, manifest, destination }) => {
      await put(source, privatePath, 'private repository metadata\n');
      await writeFile(manifest, `${privatePath}\n`);

      const result = exportAlpha(source, manifest, destination);

      assert.notEqual(result.status, 0);
      assert.doesNotMatch(result.stderr, /private repository metadata/);
      await pathDoesNotExist(destination);
    }));
  }
});

test('rejects a destination inside the source tree without mutating the source', async () => {
  await withFixture(async ({ source, manifest }) => {
    await put(source, 'included.txt', 'public bytes\n');
    await writeFile(manifest, 'included.txt\n');
    const nestedDestination = join(source, 'generated-candidate');
    const before = await readdir(source);

    const result = exportAlpha(source, manifest, nestedDestination);

    assert.notEqual(result.status, 0);
    assert.deepEqual(await readdir(source), before);
    await pathDoesNotExist(nestedDestination);
  });
});

test('exports deterministic digests without mutating the source or creating Git metadata', async () => {
  await withFixture(async ({ source, manifest, destination }) => {
    const alpha = await put(source, 'alpha.txt', 'alpha\n', 0o644);
    const bravo = await put(source, 'nested/bravo.sh', '#!/bin/sh\nprintf bravo\n', 0o751);
    await writeFile(manifest, 'nested/bravo.sh\nalpha.txt\n');
    const beforeAlpha = await stat(alpha);
    const beforeBravo = await stat(bravo);

    const result = exportAlpha(source, manifest, destination);

    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(JSON.parse(result.stdout), {
      fileCount: 2,
      treeSha256: 'afafb77ae8d8fbd31c5099b4bd6f2e7c01f513902a97c2c8cb4a6423cdf89624',
      files: [
        {
          path: 'alpha.txt',
          mode: '644',
          sha256: 'b6a98d9ce9a2d9149288fa3df42d377c3e42737afdcdaf714e33c0a100b51060',
        },
        {
          path: 'nested/bravo.sh',
          mode: '751',
          sha256: '55c768b71358ce7bd43d6187f92b2009beec1479e87702015b9ecd77615d7cb8',
        },
      ],
    });
    assert.equal(await readFile(join(destination, 'alpha.txt'), 'utf8'), 'alpha\n');
    assert.equal(await readFile(join(destination, 'nested/bravo.sh'), 'utf8'), '#!/bin/sh\nprintf bravo\n');
    await pathDoesNotExist(join(destination, '.git'));
    assert.equal((await stat(alpha)).mode, beforeAlpha.mode);
    assert.equal((await stat(bravo)).mode, beforeBravo.mode);
    assert.equal((await stat(alpha)).size, beforeAlpha.size);
    assert.equal((await stat(bravo)).size, beforeBravo.size);
  });
});

test('the real alpha manifest exports exactly its listed regular files', async () => {
  const repositoryRoot = fileURLToPath(new URL('../', import.meta.url));
  const manifest = join(repositoryRoot, 'scripts', 'alpha-files.txt');
  const paths = (await readFile(manifest, 'utf8')).trim().split('\n').sort();
  const temporaryRoot = await mkdtemp(join(tmpdir(), 'foundry-real-alpha-export-'));
  const destination = join(temporaryRoot, 'candidate');
  try {
    const result = exportAlpha(repositoryRoot, manifest, destination);

    assert.equal(result.status, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).fileCount, paths.length);
    assert.deepEqual(await listRegularFiles(destination), paths);
    // Export must retain the current VM/installer and C3/C4 dependencies, not
    // merely agree with an incomplete manifest. No Git metadata is needed.
    for (const dependency of [
      'internal/vmguest/guest.go',
      'internal/vmguest/linux.go',
      'internal/vmguest/scratch.go',
      'internal/vmruntime/runtime.go',
      'internal/vmruntime/process_windows.go',
      'internal/vmruntime/process_darwin.go',
      'internal/vmacceptance/controller.go',
      'internal/vmacceptance/summary.go',
      'cmd/foundry-vm-guest/main.go',
      'cmd/foundry-vm-acceptance/main.go',
      'cmd/foundry/coding_runtime.go',
      'internal/domain/c4_vm.go',
      'internal/engine/coding_runtime.go',
      'internal/engine/wait.go',
      'internal/httpapi/coding_tasks.go',
      'internal/sandbox/c4_vm.go',
      'internal/sandbox/c4_lifecycle.go',
      'internal/model/secrets_credential_manager.go',
      'internal/model/secrets_windows.go',
      'desktop/native-vm-smoke.cjs',
      'desktop/windows-ui-scroll.ps1',
      'desktop/windows-ui-scroll.test.ps1',
      'desktop/test/windows-scroll-fixture.cjs',
      'internal/engine/coding_dependencies_native_test.go',
      'internal/engine/coding_dependencies_windows_test.go',
      'desktop/coding-mode.cjs',
      'desktop/sandbox/build.cjs',
      'desktop/sandbox/build-guest.cjs',
      'desktop/sandbox/assets.cjs',
      'desktop/sandbox/inputs.lock.json',
      'desktop/sandbox/kernel.cjs',
      'desktop/sandbox/macos/main.swift',
      'desktop/sandbox/macos/entitlements.plist',
      'desktop/sandbox/windows-bundle.cjs',
      'desktop/sandbox/windows-source-build.cjs',
      'desktop/sandbox/windows-zlib.cjs',
      'desktop/sandbox/zlib-regression.c',
      'desktop/test/windows-zlib.test.cjs',
      'desktop/test/native-vm-smoke.test.cjs',
      'web/src/codingTaskApi.ts',
      'web/src/components/CodingTaskSetup.tsx',
      'web/src/components/C4VMIdentityFacts.tsx',
      'web/src/embeddedVM.test.tsx',
      'web/src/test/c4VMStatusContract.json',
      'docs/public/embedded-sandbox.md',
      'docs/public/embedded-sandbox-plan.md',
      'docs/public/live-coding.md',
      'docs/public/live-coding-plan.md',
      'internal/domain/repository_import.go',
      'internal/workspace/tree_walk.go',
      'internal/engine/repository_import_inspection.go',
      'internal/httpapi/repository_import.go',
      'internal/store/repository_import.go',
      'cmd/foundry-acceptance/repository_import.go',
      'web/src/components/RepositoryImport.tsx',
      'internal/domain/c4.go',
      'internal/sandbox/c4.go',
      'internal/engine/c4_baseline.go',
      'internal/httpapi/c4.go',
      'internal/c4verify/verify.go',
      'cmd/foundry-verify-go/main.go',
      'cmd/foundry-acceptance/c4_fixture.go',
      'web/src/components/C4Verification.tsx',
      'docs/public/coding-verification-c4.md',
    ]) {
      assert.equal((await stat(join(destination, dependency))).isFile(), true, `${dependency} required in clean export`);
    }
    // Include all reviewed first-party VM source/tests, not just their entry
    // points. This compares actual source/output trees without broadening the
    // exporter: production still copies only explicit allowlisted paths.
    for (const directory of ['internal/vmguest', 'internal/vmruntime', 'internal/vmacceptance', 'cmd/foundry-vm-guest', 'cmd/foundry-vm-acceptance', 'desktop/sandbox']) {
      assert.deepEqual(await listRegularFiles(destination, directory), await listRegularFiles(repositoryRoot, directory), `${directory} must be complete`);
    }
    for (const path of paths) {
      const sourceBytes = await readFile(join(repositoryRoot, ...path.split('/')));
      const destinationBytes = await readFile(join(destination, ...path.split('/')));
      assert.deepEqual(destinationBytes, sourceBytes, `${path} bytes must match`);
    }
    await pathDoesNotExist(join(destination, '.git'));
    await pathDoesNotExist(join(destination, 'AGENTS.md'));
    await pathDoesNotExist(join(destination, 'docs', 'superpowers'));
    await pathDoesNotExist(join(destination, '.superpowers'));
    await pathDoesNotExist(join(destination, 'evidence'));
  } finally {
    await rm(temporaryRoot, { recursive: true, force: true });
  }
});
