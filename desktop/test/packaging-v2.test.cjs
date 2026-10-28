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
