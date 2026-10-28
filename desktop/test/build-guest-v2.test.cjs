'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {spawnSync} = require('node:child_process');
const script = path.join(__dirname, 'build_guest_v2_test.py');
const python = process.env.FOUNDRY_BUILD_PYTHON;
const missing = path.join(fs.realpathSync(require('node:os').tmpdir()),'foundry-v2-missing-'+process.pid);

test('v2 builder requires an explicitly chosen trusted host Python', () => {
  const {buildGuestV2} = require('../sandbox/build-guest-v2.cjs');
  assert.throws(() => buildGuestV2({inputs:missing, arch:'amd64', validateOnly:true}), /explicit.*Python/);
  assert.throws(() => buildGuestV2({python:'python3', inputs:missing, arch:'amd64', validateOnly:true}), /absolute/);
  assert.throws(() => buildGuestV2({python, inputs:missing, arch:'amd64', command:'npm test'}), /unknown/);
});

test('trusted host Python exercises synthetic offline assembly and decoder rejection cases', () => {
  assert.ok(python && path.isAbsolute(python), 'FOUNDRY_BUILD_PYTHON must explicitly name trusted Python >=3.13');
  assert.ok(fs.statSync(python).isFile());
  const version = spawnSync(python, ['-I','-B','-c','import sys; assert sys.version_info >= (3,13), "Python >=3.13 required"'], {encoding:'utf8',shell:false});
  assert.equal(version.status,0,version.stderr);
  const result = spawnSync(python, ['-I','-B','-m','unittest','discover','-s',__dirname,'-p','build_guest_v2*_test.py'], {encoding:'utf8',shell:false,maxBuffer:1024*1024});
  assert.equal(result.status,0,result.stderr || result.stdout);
  assert.match(result.stderr,/Ran \d+ tests/);
});

test('Node wrapper invokes trusted Python with literal arguments and surfaces validation failures', () => {
  const {buildGuestV2} = require('../sandbox/build-guest-v2.cjs');
  assert.throws(() => buildGuestV2({python, inputs:missing+' ; echo injected', arch:'amd64', validateOnly:true}), /missing input path/);
  assert.throws(() => buildGuestV2({python, inputs:missing, arch:'amd64', validateOnly:true, output:path.join(missing,'no')}), /validate-only/);
});

test('Node interface builds from literal owned inputs and publishes a matching receipt', () => {
  const os = require('node:os');
  const {buildGuestV2} = require('../sandbox/build-guest-v2.cjs');
  const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'foundry-v2-')));
  try {
    const prepare = String.raw`
import importlib.util,json,sys,hashlib
from pathlib import Path
spec=importlib.util.spec_from_file_location('fixtures',sys.argv[1]);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
r=Path(sys.argv[2]);lock=m.BoundaryTests().fixture(r);(r/'lock.json').write_text(json.dumps(lock));(r/'guest').write_bytes(m.elf())
rows=[]
for n in ['node.cjs','python.py','typescript.cjs']:
 p=r/n;p.write_bytes(b'# owned source');rows.append({'path':'opt/foundry/runners/'+n,'source':str(p),'sha256':hashlib.sha256(p.read_bytes()).hexdigest()})
(r/'runners.json').write_text(json.dumps(rows))
`;
    const setup = spawnSync(python,['-I','-B','-c',prepare,script,root],{encoding:'utf8',shell:false});
    assert.equal(setup.status,0,setup.stderr);
    const common = {python, lock:path.join(root,'lock.json'),inputs:root,arch:'amd64'};
    const validation = buildGuestV2({...common,validateOnly:true,sources:true});
    assert.equal(validation.status,'static-input-validation-only');
    assert.equal(validation.sources_hashed,1);
    const options = {...common,guest:path.join(root,'guest'),runners:path.join(root,'runners.json'),output:path.join(root,'image.gz'),receipt:path.join(root,'receipt.json')};
    const receipt = buildGuestV2(options);
    assert.equal(receipt.status,'assembled-build-only');
    assert.deepEqual(receipt,JSON.parse(fs.readFileSync(options.receipt)));
    assert.equal(receipt.image.sha256,require('node:crypto').createHash('sha256').update(fs.readFileSync(options.output)).digest('hex'));
    assert.throws(() => buildGuestV2(options),/overwrite/);
  } finally {fs.rmSync(root,{recursive:true,force:true});}
});
