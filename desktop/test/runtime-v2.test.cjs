'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),crypto=require('node:crypto');
const {sandboxFixture,v2Fixture,loadDesktopFixtureModule}=require('./sandbox-fixture.cjs');
const fixture=path.resolve(__dirname,'../../internal/vmruntime/testdata/runtime-inventory-v2.json');
const sha=raw=>crypto.createHash('sha256').update(raw).digest('hex');
test('Node descriptor validator shares exact Go/Python canonical fixture and rejects semantic/noncanonical forms',()=>{
  const {parseRuntimeInventoryV2,canonicalInventory,inventoryDigest}=require('../runtime-inventory-v2.cjs'),raw=fs.readFileSync(fixture);
  assert.equal(canonicalInventory(JSON.parse(raw)),raw.toString());assert.equal(inventoryDigest(raw),fs.readFileSync(fixture.replace('.json','.sha256'),'utf8').trim());
  assert.equal(parseRuntimeInventoryV2(raw).guest_platform,'linux/arm64');
  for(const change of [d=>d.node.version='24.0.0',d=>d.python.options.reverse(),d=>d.runners.reverse(),d=>d.compiler_contract.compiler_options.strict=false,d=>d.projected_runtime.entries=0,d=>d.supervisor.size=0,d=>d.extra=true,d=>delete d.node.version,d=>d.python=null,d=>d.typescript.compiler.path='/tmp/compiler']) {
    const d=JSON.parse(raw);change(d);assert.throws(()=>parseRuntimeInventoryV2(Buffer.from(canonicalInventory(d))));
  }
  for(const bad of [raw+'\n',raw.toString().replace('"schema_version":2','"schema_version":2,"schema_version":2'),raw.toString().replace('"schema_version":2','"schema_version":2.0')])assert.throws(()=>parseRuntimeInventoryV2(Buffer.from(bad)));
});
test('separate v2 loader validates platform, supervisor and descriptor bindings without v1 negotiation',t=>{
  const root=fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'foundry-v2-check-')));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
  const {verifySandboxBundle,verifySandboxBundleV2}=require('../runtime.cjs');const m=v2Fixture(root, 'darwin/arm64');
  assert.equal(verifySandboxBundleV2(root,'darwin/arm64').schema_version,2);assert.throws(()=>verifySandboxBundle(root,'darwin/arm64'));
  for(const change of [x=>x.runtime_inventory_digest='0'.repeat(64),x=>x.guest_platform='linux/amd64',x=>x.verifier='foundry-vm-guest',x=>x.protocol='foundry-vm-v1']) {const d=structuredClone(m);change(d);fs.writeFileSync(path.join(root,'manifest.json'),JSON.stringify(d));assert.throws(()=>verifySandboxBundleV2(root,'darwin/arm64'));}
  fs.writeFileSync(path.join(root,'manifest.json'),JSON.stringify(m));
  const d=JSON.parse(fs.readFileSync(path.join(root,m.runtime_inventory)));d.supervisor.sha256='1'.repeat(64);
  const raw=Buffer.from(require('../runtime-inventory-v2.cjs').canonicalInventory(d));fs.writeFileSync(path.join(root,m.runtime_inventory),raw);
  m.runtime_inventory_digest=require('../runtime-inventory-v2.cjs').inventoryDigest(raw);Object.assign(m.assets.find(a=>a.path===m.runtime_inventory),{size:raw.length,sha256:sha(raw)});
  fs.writeFileSync(path.join(root,'manifest.json'),JSON.stringify(m));assert.throws(()=>verifySandboxBundleV2(root,'darwin/arm64'),/supervisor/i);
});
test("installed dual resources retain v1 and missing v2 stays unavailable without executing", async (t) => {
  for (const host of ["darwin/arm64", "windows/amd64"]) {
    await t.test(host, (t) => {
      const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "foundry-dual-")));
      t.after(() => fs.rmSync(root, { recursive: true, force: true }));
      const runtime = loadDesktopFixtureModule("runtime.cjs", host);
      const suffix = host === "windows/amd64" ? ".exe" : "";
      for (const name of ["bin/foundry" + suffix, "bin/foundry-acceptance" + suffix, "bin/foundry-vm-acceptance" + suffix, "web/index.html"]) {
        fs.mkdirSync(path.dirname(path.join(root, name)), { recursive: true }); fs.writeFileSync(path.join(root, name), "inert");
      }
      sandboxFixture(path.join(root, "sandbox"), host);
      const v1 = fs.readFileSync(path.join(root, "sandbox/manifest.json"));
      const { writeManifest } = require("../build-support.cjs"); writeManifest(root);
      assert.equal(runtime.verifyResources(root).v2, null);
      v2Fixture(path.join(root, "sandbox-v2"), host);
      fs.unlinkSync(path.join(root, "resources-manifest.json")); writeManifest(root);
      const catalog = runtime.verifyResources(root);
      assert.equal(catalog.v1.host_platform, host); assert.equal(catalog.v2.host_platform, host);
      assert.equal(catalog.v2.schema_version, 2); assert.deepEqual(fs.readFileSync(path.join(root, "sandbox/manifest.json")), v1);
      fs.writeFileSync(path.join(root, "sandbox-v2/extra"), "unlisted");
      assert.throws(() => runtime.verifyResources(root), /unlisted/i);
    });
  }
});
test('v2 installed validation rejects escaped manifest keys and orphan directories',t=>{
  const root=fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'foundry-v2-exact-')));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));const m=v2Fixture(root, 'darwin/arm64'),{verifySandboxBundleV2}=require('../runtime.cjs');
  fs.writeFileSync(path.join(root,'manifest.json'),JSON.stringify(m).replace('"schema_version"','"\\u0073chema_version"'));assert.throws(()=>verifySandboxBundleV2(root,'darwin/arm64'));
  fs.writeFileSync(path.join(root,'manifest.json'),JSON.stringify(m));fs.mkdirSync(path.join(root,'orphan'));assert.throws(()=>verifySandboxBundleV2(root,'darwin/arm64'));
});
test('oversized descriptor is refused before an allocating read',t=>{
  const root=fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'foundry-v2-bounds-')));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));const m=v2Fixture(root, 'darwin/arm64'),file=path.join(root,m.runtime_inventory),raw=Buffer.alloc(65537,32);fs.writeFileSync(file,raw);Object.assign(m.assets.find(a=>a.path===m.runtime_inventory),{size:raw.length,sha256:sha(raw)});fs.writeFileSync(path.join(root,'manifest.json'),JSON.stringify(m));
  const read=fs.readFileSync;let allocated=false;fs.readFileSync=function(target,...args){if(target===file)allocated=true;return read.call(this,target,...args);};
  try{assert.throws(()=>require('../runtime.cjs').verifySandboxBundleV2(root,'darwin/arm64'));assert.equal(allocated,false);}finally{fs.readFileSync=read;}
});
test('v1 and v2 fixture defaults share the same supported fixture platform',t=>{
  const root=fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'foundry-fixture-platform-')));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
  const host=process.platform==='win32'?'windows/amd64':'darwin/arm64';
  const v1=path.join(root,'v1'),v2=path.join(root,'v2');sandboxFixture(v1);v2Fixture(v2);
  const runtime=require('../runtime.cjs');assert.equal(runtime.verifySandboxBundle(v1,host).host_platform,host);assert.equal(runtime.verifySandboxBundleV2(v2,host).host_platform,host);
});
