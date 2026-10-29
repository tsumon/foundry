'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),crypto=require('node:crypto');
const {sandboxFixture,v2Fixture}=require('./sandbox-fixture.cjs');
const sha=raw=>crypto.createHash('sha256').update(raw).digest('hex');
function kit(root,bundle,name) {
  fs.mkdirSync(root);fs.writeFileSync(path.join(root,'source.tar'),'owned source archive');
  const raw=Buffer.from(JSON.stringify({schema_version:1,sidecar:name,files:[{path:'source.tar',size:20,sha256:sha('owned source archive')}]}));
  fs.writeFileSync(path.join(bundle,'legal/sources.json'),raw);
  const m=JSON.parse(fs.readFileSync(path.join(bundle,'manifest.json')));m.assets=m.assets.filter(a=>a.path!=='legal/sources.json');m.assets.push({path:'legal/sources.json',size:raw.length,sha256:sha(raw)});fs.writeFileSync(path.join(bundle,'manifest.json'),JSON.stringify(m));
}
test("dual packaging copies exactly v1 and v2 plus verified distribution sidecars and preserves v1 bytes", async (t) => {
  for (const host of ["darwin/arm64", "windows/amd64"]) {
    await t.test(host, (t) => {
      const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "foundry-dual-copy-")));
      t.after(() => fs.rmSync(root, { recursive: true, force: true }));
      const v1 = path.join(root, "v1"), v2 = path.join(root, "v2"), s1 = path.join(root, "source1"), s2 = path.join(root, "source2");
      sandboxFixture(v1, host); v2Fixture(v2, host);
      kit(s1, v1, "sandbox-sources"); kit(s2, v2, "sandbox-v2-sources");
      const { copyDesktopSandboxes, verifySourceSidecar } = require("../build-support.cjs");
      const original = fs.readFileSync(path.join(v1, "manifest.json"));
      const inputs = path.join(root, "inputs"), output = path.join(root, "output"); fs.mkdirSync(inputs); fs.mkdirSync(output);
      copyDesktopSandboxes({ v1, v2, v1Sources: s1, v2Sources: s2, inputs, output, host });
      assert.deepEqual(fs.readdirSync(inputs).sort(), ["sandbox", "sandbox-v2"]);
      assert.deepEqual(fs.readdirSync(output).sort(), ["sandbox-sources", "sandbox-v2-sources"]);
      assert.deepEqual(fs.readFileSync(path.join(inputs, "sandbox/manifest.json")), original);
      assert.equal(verifySourceSidecar(path.join(inputs, "sandbox-v2"), path.join(output, "sandbox-v2-sources"), "sandbox-v2-sources"), true);
      fs.writeFileSync(path.join(output, "sandbox-v2-sources/source.tar"), "altered");
      assert.throws(() => verifySourceSidecar(v2, path.join(output, "sandbox-v2-sources"), "sandbox-v2-sources"), /source|integrity/i);
      assert.throws(() => copyDesktopSandboxes({ v1, v2: v1, v1Sources: s1, v2Sources: s2, inputs: path.join(root, "bad-inputs"), output: path.join(root, "bad-output"), host }));
    });
  }
});
test('sidecar validation rejects missing, unlisted, aliased and private files',t=>{
  const root=fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'foundry-sidecar-')));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));const bundle=path.join(root,'bundle'),sources=path.join(root,'sources');v2Fixture(bundle, 'darwin/arm64');kit(sources,bundle,'sandbox-v2-sources');const {verifySourceSidecar}=require('../build-support.cjs');
  assert.equal(verifySourceSidecar(bundle,sources,'sandbox-v2-sources'),true);
  fs.writeFileSync(path.join(sources,'extra'),'unlisted');assert.throws(()=>verifySourceSidecar(bundle,sources,'sandbox-v2-sources'));fs.unlinkSync(path.join(sources,'extra'));
  const index=JSON.parse(fs.readFileSync(path.join(bundle,'legal/sources.json')));index.files[0].path='../source.tar';fs.writeFileSync(path.join(bundle,'legal/sources.json'),JSON.stringify(index));assert.throws(()=>verifySourceSidecar(bundle,sources,'sandbox-v2-sources'));
  index.files[0].path='.env';fs.writeFileSync(path.join(bundle,'legal/sources.json'),JSON.stringify(index));assert.throws(()=>verifySourceSidecar(bundle,sources,'sandbox-v2-sources'));
});
test('Mac signing leaves both sealed runtime closures untouched',t=>{
  const root=fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'foundry-dual-sign-')));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));for(const n of ['Contents/Resources/sandbox/runtime/a.dylib','Contents/Resources/sandbox-v2/runtime/a.dylib','Contents/Frameworks/b.dylib']) {fs.mkdirSync(path.dirname(path.join(root,n)),{recursive:true});fs.writeFileSync(path.join(root,n),'sealed');}
  assert.deepEqual(require('../build-support.cjs').macSigningTargets(root),[path.join(root,'Contents/Frameworks/b.dylib'),root]);
});
test('v2 manifest writer binds completed descriptor and supervisor and refuses v1/missing input contracts',t=>{
  const root=fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'foundry-v2-manifest-')));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));const bundle=path.join(root,'bundle');v2Fixture(bundle, 'darwin/arm64');fs.unlinkSync(path.join(bundle,'manifest.json'));
  const {writeVMManifestV2}=require('../sandbox/build-bundle-v2.cjs');const m=writeVMManifestV2(bundle,'darwin/arm64');assert.equal(m.schema_version,2);assert.equal(require('../runtime.cjs').verifySandboxBundleV2(bundle,'darwin/arm64').runtime_inventory_digest,m.runtime_inventory_digest);assert.throws(()=>writeVMManifestV2(bundle,'darwin/arm64'));
  fs.unlinkSync(path.join(bundle,'manifest.json'));fs.writeFileSync(path.join(bundle,'foundry-vm-guest-v2'),'wrong supervisor');assert.throws(()=>writeVMManifestV2(bundle,'darwin/arm64'),/supervisor/i);assert.equal(fs.existsSync(path.join(bundle,'manifest.json')),false);
});
test("distribution manifest binds both installed runtimes and complete sidecars and detects late changes", async (t) => {
  for (const host of ["darwin/arm64", "windows/amd64"]) {
    await t.test(host, (t) => {
      const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "foundry-distribution-")));
      t.after(() => fs.rmSync(root, { recursive: true, force: true }));
      const resources = path.join(root, "resources"), output = path.join(root, "output"); fs.mkdirSync(resources); fs.mkdirSync(output);
      sandboxFixture(path.join(resources, "sandbox"), host); v2Fixture(path.join(resources, "sandbox-v2"), host);
      kit(path.join(output, "sandbox-sources"), path.join(resources, "sandbox"), "sandbox-sources");
      kit(path.join(output, "sandbox-v2-sources"), path.join(resources, "sandbox-v2"), "sandbox-v2-sources");
      const installer = path.join(output, host === "windows/amd64" ? "Foundry-0.1.0-x64.exe" : "Foundry-0.1.0-arm64.dmg");
      fs.writeFileSync(installer, "inert installer");
      const { writeDistributionManifest, verifyDistribution } = require("../distribution.cjs");
      writeDistributionManifest({ output, resources, installer, host });
      assert.equal(verifyDistribution(output, resources, host), true);
      fs.writeFileSync(path.join(output, "sandbox-v2-sources/source.tar"), "tampered");
      assert.throws(() => verifyDistribution(output, resources, host), /integrity|source/i);
    });
  }
});
function moduleNoticeFixture(t,filename='golang.org-x-sys-v0.48.0.zip') {
  const root=fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'foundry-module-notices-')));
  t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
  const moduleDir=path.join(root,'module'),legal=path.join(root,'bundle/legal/go-modules');
  fs.mkdirSync(moduleDir);fs.mkdirSync(legal,{recursive:true});
  return {root,moduleDir,legal,filename,source:path.join(moduleDir,'LICENSE'),target:path.join(legal,filename+'-LICENSE'),copy:()=>require('../sandbox/build-bundle-v2.cjs').copyGoModuleNotices(moduleDir,legal,filename)};
}
test('module notices copy absent LICENSE and PATENTS with exact bytes',t=>{
  const f=moduleNoticeFixture(t);fs.writeFileSync(f.source,'BSD license\n');fs.writeFileSync(path.join(f.moduleDir,'PATENTS'),'patent grant\n');
  f.copy();
  assert.equal(fs.readFileSync(f.target,'utf8'),'BSD license\n');
  assert.equal(fs.readFileSync(path.join(f.legal,f.filename+'-PATENTS'),'utf8'),'patent grant\n');
});
test('module notices preserve identical inherited legal bytes and file identity',t=>{
  const f=moduleNoticeFixture(t),base=path.join(f.root,'v1/legal/go-modules');fs.mkdirSync(base,{recursive:true});
  fs.writeFileSync(f.source,'exact inherited BSD license\n');fs.writeFileSync(path.join(base,f.filename+'-LICENSE'),'exact inherited BSD license\n');
  fs.writeFileSync(path.join(base,'inherited-only'),'complete inherited notice\n');
  // The assembler first inherits v1 legal files exclusively, then adds linked-module notices.
  for(const name of fs.readdirSync(base))fs.copyFileSync(path.join(base,name),path.join(f.legal,name),fs.constants.COPYFILE_EXCL);
  fs.chmodSync(f.target,0o444);fs.utimesSync(f.target,123456789,123456789);const before=fs.lstatSync(f.target);
  f.copy();
  const after=fs.lstatSync(f.target);
  assert.equal(fs.readFileSync(f.target,'utf8'),'exact inherited BSD license\n');
  for(const key of ['ino','mode','mtimeMs','size'])assert.equal(after[key],before[key]);
  assert.equal(fs.readFileSync(path.join(f.legal,'inherited-only'),'utf8'),'complete inherited notice\n');
});
test('module notices reject different inherited bytes without overwriting',t=>{
  const f=moduleNoticeFixture(t);fs.writeFileSync(f.source,'new notice');fs.writeFileSync(f.target,'old notice');
  assert.throws(f.copy);assert.equal(fs.readFileSync(f.target,'utf8'),'old notice');
});
test('module notices reject symlink and directory targets without replacing them',async t=>{
  for(const kind of ['symlink','dangling symlink','directory'])await t.test(kind,t=>{
    const f=moduleNoticeFixture(t);fs.writeFileSync(f.source,'exact notice');
    if(kind==='directory')fs.mkdirSync(f.target);
    else fs.symlinkSync(kind==='symlink'?f.source:path.join(f.root,'absent'),f.target);
    assert.throws(f.copy);
    if(kind==='directory')assert.equal(fs.lstatSync(f.target).isDirectory(),true);
    else {assert.equal(fs.lstatSync(f.target).isSymbolicLink(),true);assert.equal(fs.readlinkSync(f.target),kind==='symlink'?f.source:path.join(f.root,'absent'));}
    assert.equal(fs.readFileSync(f.source,'utf8'),'exact notice');
  });
});
test('module notices retain LICENSE.txt for a linked module',t=>{
  const f=moduleNoticeFixture(t,'github.com-Masterminds-semver-v3-v3.5.0.zip');fs.writeFileSync(path.join(f.moduleDir,'LICENSE.txt'),'semver MIT license\n');
  f.copy();f.copy();assert.equal(fs.readFileSync(path.join(f.legal,f.filename+'-LICENSE.txt'),'utf8'),'semver MIT license\n');
});
test('module notices reject a linked module without a recognized license',async t=>{
  for(const patents of [false,true])await t.test(patents?'PATENTS only':'no notices',t=>{
    const f=moduleNoticeFixture(t);if(patents)fs.writeFileSync(path.join(f.moduleDir,'PATENTS'),'not a license');
    assert.throws(f.copy,/license/i);
  });
});
test('module notices reject nonregular source notices even when inherited bytes match',async t=>{
  for(const name of ['LICENSE','LICENSE.txt','PATENTS'])for(const kind of ['symlink','dangling symlink','directory'])await t.test(name+'/'+kind,t=>{
    const f=moduleNoticeFixture(t),source=path.join(f.moduleDir,name),real=path.join(f.root,'real-notice'),target=path.join(f.legal,f.filename+'-'+name);
    if(name!=='LICENSE')fs.writeFileSync(f.source,'valid license');
    fs.writeFileSync(real,'exact notice');fs.writeFileSync(target,'exact notice');
    if(kind==='directory')fs.mkdirSync(source);else fs.symlinkSync(kind==='symlink'?real:path.join(f.root,'absent'),source);
    assert.throws(f.copy);assert.equal(fs.readFileSync(target,'utf8'),'exact notice');
  });
});
