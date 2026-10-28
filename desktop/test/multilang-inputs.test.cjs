'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),os=require('node:os'),path=require('node:path'),crypto=require('node:crypto');
const sha=b=>crypto.createHash('sha256').update(b).digest('hex');
function fixture(){return {schema_version:2,projection_policy:'foundry-runtime-projection-v2',artifacts:[{file:'arm.tar.xz',kind:'node-runtime',target_architectures:['arm64'],url:'https://nodejs.org/arm.tar.xz',size:3,sha256:sha('arm')},{file:'x64.tar.xz',kind:'node-runtime',target_architectures:['amd64'],url:'https://nodejs.org/x64.tar.xz',size:3,sha256:sha('x64')},{file:'source.tar.xz',kind:'node-source',url:'https://nodejs.org/source.tar.xz',size:6,sha256:sha('source')}]};}
test('explicit preparation selects architecture and complete source bytes and verifies injected transport',async t=>{
  const {prepareMultilangInputs}=require('../sandbox/multilang-inputs.cjs');const root=fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'foundry-inputs-')));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
  const output=path.join(root,'out'),lock=fixture();const receipt=await prepareMultilangInputs({output,arch:'arm64',lock,fetchImpl:async url=>new Response(url.pathname.includes('source')?'source':'arm')});
  assert.equal(receipt.files.length,2);assert.equal(fs.existsSync(path.join(output,'x64.tar.xz')),false);assert.equal(fs.readFileSync(path.join(output,'arm.tar.xz'),'utf8'),'arm');
  await assert.rejects(prepareMultilangInputs({output:path.join(root,'bad'),arch:'arm64',lock,fetchImpl:async()=>new Response('bad')}),/integrity/i);
  fs.writeFileSync(path.join(output,'arm.tar.xz'),'BAD');await assert.rejects(prepareMultilangInputs({output:path.join(root,'reuse'),local:output,arch:'arm64',lock}),/integrity/i);
});
test('acquisition rejects foreign origins, unsafe paths and unpinned identities before transport',async t=>{
  const {prepareMultilangInputs}=require('../sandbox/multilang-inputs.cjs');const root=fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'foundry-inputs-bound-')));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
  for(const [i,change] of [r=>r.url='http://nodejs.org/a',r=>r.url='https://evil.invalid/a',r=>r.file='../input',r=>r.sha256='x',r=>r.size=0].entries()) {const lock=fixture();change(lock.artifacts[0]);await assert.rejects(prepareMultilangInputs({output:path.join(root,'case'+i),arch:'arm64',lock,fetchImpl:()=>{throw Error('transport must not run');}}),/metadata|origin|URL/i);}
});
test('source verification acquisition selects only the exact compiler fixture artifact',()=>{
  const {selectInputs}=require('../sandbox/multilang-inputs.cjs');const rows=selectInputs(require('../sandbox/multilang-inputs.lock.json'),'compiler');assert.equal(rows.length,1);assert.equal(rows[0].file,'typescript-5.9.3.tgz');assert.equal(rows[0].sha256,'10e108c9cf7d5f2879053dff18515fb405abf2ccef63eaaf017d9c571687a1d3');
});
