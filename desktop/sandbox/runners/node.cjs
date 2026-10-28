'use strict';
// Image-owned adapter. The test runner's V8 event stream is consumed through
// node:test APIs. Project stdout is diagnostic data, never JSONL authority.
const fs = require('node:fs');
const path = require('node:path');
const { run } = require('node:test');

function failureDiagnostics(root) {
  let remaining = 16384;
  // Read data properties only; never inspect/serialize a project error object
  // or call its getters, toString, toJSON, or custom inspection hooks.
  function own(value, key) {
    if (!value || typeof value !== 'object') return undefined;
    try { const d = Object.getOwnPropertyDescriptor(value, key); return d && 'value' in d ? d.value : undefined; } catch { return undefined; }
  }
  function text(value, limit) {
    if (typeof value !== 'string') return '';
    return value.slice(0,4096).split(root).join('<project>').slice(0,limit)
      .replace(/[^\x20-\x7e]/g, c => '\\u' + c.charCodeAt(0).toString(16).padStart(4,'0')).slice(0,limit);
  }
  return (data, stage) => {
    if (!remaining) return;
    const detail = own(data, 'details'), error = own(detail, 'error'), cause = own(error, 'cause');
    const file = own(data, 'file');
    let relative = '';
    if (typeof file === 'string' && file.length <= 4096) {
      const p = path.relative(root, path.resolve(root, file));
      if (p !== '..' && !p.startsWith('..' + path.sep) && !path.isAbsolute(p)) relative = p;
    }
    const line = own(data, 'line');
    const raw = JSON.stringify({kind:'foundry_test_failure_diagnostic_v2',runner:'node-test-v2',stage,
      name:text(own(data,'name'),128),file:text(relative,256),line:Number.isSafeInteger(line) && line > 0 && line <= 1000000 ? line : 0,
      error_type:text(own(error,'name'),64),failure_type:text(own(error,'failureType'),64),
      message:text(own(error,'message'),1024),cause:text(own(cause,'message'),1024)}) + '\n';
    const size = Buffer.byteLength(raw);
    if (size > remaining) { remaining = 0; return; }
    remaining -= size;
    // Diagnostic-only stderr; FD3's closed framework report is unchanged.
    try {
      const b = Buffer.from(raw);
      let at = 0;
      while (at < b.length) { const n = fs.writeSync(2, b, at, b.length - at); if (!n) break; at += n; }
    } catch { /* Diagnostic delivery never changes framework authority. */ }
  };
}

function reporter(fd) {
  let seq = 0, started = 0, bytes = 0;
  const counts = { passed: 0, failed: 0, skipped: 0, expected_failed: 0, unexpected_passed: 0 };
  function event(type, data) {
    const raw = JSON.stringify({ type, seq: ++seq, ...data }) + '\n';
    bytes += Buffer.byteLength(raw);
    if (bytes > 2097152 || seq > 8194) throw new Error('report limit');
    let at = 0;
    const b = Buffer.from(raw);
    while (at < b.length) { const n = fs.writeSync(fd, b, at, b.length - at); if (!n) throw new Error('report write'); at += n; }
  }
  event('suite_start', { schema_version: 1, runner: 'node-test-v2' });
  return {
    completed(status) {
      if (started === 4096 || !(status in counts)) throw new Error('case limit');
      const id = ++started;
      // Emit a case only after the framework has completed it. Declaration
      // ordered nesting lets us exclude container tests and suite rollups.
      event('case_start', { id });
      event('case_end', { id, status });
      counts[status]++;
    },
    end(discovery) {
      if (discovery > 4096) throw new Error('discovery limit');
      event('suite_end', { started, ...counts, discovery_errors: discovery });
      return discovery || counts.failed || counts.unexpected_passed ? 1 : 0;
    }
  };
}
async function runTests(contract, fd) {
  const r = reporter(fd);
  const root = contract.output_root || contract.source_root;
  const diagnostic = failureDiagnostics(root);
  const stack = [];
  const fileSummaries = new Set(), namedCandidates = [];
  const entries = new Set(contract.test_files.flatMap(f => { const p = path.join(root, f); return [p, fs.realpathSync(p)]; }));
  let discovery = 0, summary = false;
  // Pinned runner.js constructs stdio=['pipe','pipe','pipe']; watch=false
  // prevents an IPC slot. No parent report/contract/control fd is forwarded.
  const stream = run({ files: contract.test_files.map(f => path.join(root, f)),
    cwd: root, concurrency: 1, isolation: 'process', watch: false,
    execArgv: [], argv: [], env: { LANG: 'C.UTF-8', TZ: 'UTC', TMPDIR: '/tmp' } });
  for await (const { type, data } of stream) {
    if (type === 'test:stdout' || type === 'test:stderr') {
      (type === 'test:stdout' ? process.stdout : process.stderr).write(data.message);
    } else if (type === 'test:start') {
      if (!Number.isInteger(data.nesting) || data.nesting < 0 || data.nesting > 4096) throw new Error('nesting');
      if (data.nesting > 0 && stack[data.nesting - 1]) stack[data.nesting - 1].children = true;
      stack[data.nesting] = { children: false };
    } else if (type === 'test:pass' || type === 'test:fail') {
      const frame = stack[data.nesting];
      const detail = data.details || {};
      const failure = detail.error && detail.error.failureType;
      if (!frame) throw new Error('test lifecycle');
      stack.length = data.nesting;
      if (type === 'test:fail' && failure !== 'subtestsFailed') diagnostic(data,
        detail.type === 'suite' || frame.children || !data.line || detail.error && ('exitCode' in detail.error || 'signal' in detail.error) || failure && failure !== 'testCodeFailure' ? 'infrastructure' : 'assertion');
      if (detail.type === 'suite' || frame.children) {
        if (type === 'test:fail' && failure !== 'subtestsFailed') discovery++;
      } else if (!data.line || detail.error && ('exitCode' in detail.error || 'signal' in detail.error) || failure && failure !== 'testCodeFailure') {
        // File/import errors, hooks, cancellation and timeouts are not
        // assertion cases. A test-file wrapper must never invent a pass.
        if (type === 'test:fail') discovery++;
      } else {
        const status = data.skip ? 'skipped' : data.todo ? (type === 'test:fail' ? 'expected_failed' : 'unexpected_passed') : type === 'test:pass' ? 'passed' : 'failed';
        if (entries.has(data.name) && data.name === data.file) {
          if (namedCandidates.length === 4096) throw new Error('case limit');
          namedCandidates.push({file:fs.realpathSync(data.file),status});
        } else r.completed(status);
      }
    } else if (type === 'test:summary') {
      if (data.file) fileSummaries.add(fs.realpathSync(data.file));
      else summary = true;
    }
    if (discovery > 4096) throw new Error('discovery limit');
  }
  if (!summary || stack.some(Boolean)) throw new Error('incomplete framework stream');
  // An empty file's synthetic wrapper has the same name/location shape as a
  // real test named __filename. Only a native per-file summary establishes
  // that the child actually emitted framework cases. Never count wrappers.
  for (const candidate of namedCandidates) if (fileSummaries.has(candidate.file)) r.completed(candidate.status);
  return r.end(discovery);
}
async function probe() {
  // Owned fixture only; no /workspace or dependency-store access.
  const dir = fs.mkdtempSync('/tmp/foundry-node-probe-');
  try {
    const file = path.join(dir, 'owned.test.cjs');
    fs.writeFileSync(file, "require('node:test').test('owned',()=>require('node:assert/strict').equal(1,1));\n", { flag: 'wx', mode: 0o600 });
    const output = path.join(dir, 'owned-report.jsonl');
    const fd = fs.openSync(output, 'wx+', 0o600);
    try {
      if (await runTests({source_root:dir,output_root:'',test_files:['owned.test.cjs']}, fd)) throw new Error('owned fixture failed');
      const events = fs.readFileSync(output, 'utf8').trimEnd().split('\n').map(line => JSON.parse(line));
      if (events.length !== 4 || events[0].schema_version !== 1 || events[0].runner !== 'node-test-v2' || events[3].passed !== 1 || events[3].discovery_errors !== 0) throw new Error('owned reporter fixture');
    } finally { fs.closeSync(fd); }
  } finally { fs.rmSync(dir, { recursive: true }); }
}
async function main() {
  if (process.argv.length !== 2 || process.versions.node !== '24.22.1') throw new Error('runtime version');
  const raw = fs.readFileSync(4); fs.closeSync(4);
  if (raw.length > 1048576) throw new Error('contract limit');
  const c = JSON.parse(raw);
  if (c.phase === 'probe-node') { await probe(); return 0; }
  if (c.phase !== 'test-node') throw new Error('phase');
  return runTests(c, 3);
}
module.exports = { runTests, reporter, probe };
if (require.main === module) main().then(code => { process.exitCode = code; }, () => { process.stderr.write('owned_node_runner_failed\n'); process.exitCode = 2; });
