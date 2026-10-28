'use strict';
// Explicit CI/build input acquisition only. Never imported by installed code or
// the offline assembler. Local reuse is checked against the same committed lock.
const fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto');
const sourceKinds=new Set(['debian-corresponding-source','node-source','typescript-source']);
const runtimeKinds=new Set(['debian-runtime-package','node-runtime','typescript-compiler']);
const origins=new Set(['https://deb.debian.org','https://nodejs.org','https://registry.npmjs.org','https://codeload.github.com']);
const {canonicalInventory}=require('../runtime-inventory-v2.cjs');
const sha=raw=>crypto.createHash('sha256').update(raw).digest('hex');
function inputURL(raw) {
  const u=new URL(raw);if(!origins.has(u.origin)||u.username||u.password||u.search||u.hash)throw Error('Unapproved multilang input origin/URL');return u;
}
function selectInputs(lock,arch) {
  if(!['arm64','amd64','all','compiler'].includes(arch)||lock.schema_version!==2||lock.projection_policy!=='foundry-runtime-projection-v2'||!Array.isArray(lock.artifacts)||lock.artifacts.length>128)throw Error('Invalid multilang input metadata');
  const names=new Set();
  for(const a of lock.artifacts) {
    if(!a||!/^[A-Za-z0-9][A-Za-z0-9._+~:-]{0,159}$/.test(a.file)||a.file.includes(':')||/^(con|prn|aux|nul|com[0-9]|lpt[0-9])(?:\.|$)/i.test(a.file)||a.file.endsWith('.')||names.has(a.file.toLowerCase())||!Number.isSafeInteger(a.size)||a.size<1||a.size>768*1024**2||!/^[a-f0-9]{64}$/.test(a.sha256)||(!runtimeKinds.has(a.kind)&&!sourceKinds.has(a.kind)))throw Error('Invalid multilang input metadata');
    names.add(a.file.toLowerCase());inputURL(a.url);
    if(runtimeKinds.has(a.kind)&&(!Array.isArray(a.target_architectures)||!a.target_architectures.length||a.target_architectures.some(v=>!['arm64','amd64'].includes(v))))throw Error('Invalid architecture metadata');
  }
  // All locked corresponding sources are shared build-side input closure. No
  // foreign-architecture executable package enters a one-architecture input set.
  return lock.artifacts.filter(a=>arch==='compiler'?a.kind==='typescript-compiler':sourceKinds.has(a.kind)||arch==='all'||a.target_architectures.includes(arch));
}
function directory(root) {
  if(!path.isAbsolute(root)||fs.realpathSync(root)!==root||!fs.lstatSync(root).isDirectory())throw Error('Canonical regular input directory required');
}
async function prepareMultilangInputs({output,local,arch,lock=require('./multilang-inputs.lock.json'),fetchImpl=fetch}) {
  const selected=selectInputs(lock,arch);
  if(!path.isAbsolute(output)||fs.existsSync(output))throw Error('Fresh absolute multilang input output required');directory(path.dirname(output));if(local)directory(local);
  fs.mkdirSync(output,{mode:0o700});const receipt={schema_version:2,arch,lock_sha256:sha(Buffer.from(canonicalInventory(lock))),files:[]};
  for(const a of selected) {
    let raw;
    if(local) {
      const file=path.join(local,a.file),info=fs.lstatSync(file);
      if(!info.isFile()||info.nlink!==1||info.size!==a.size)throw Error('Multilang local integrity failure');raw=fs.readFileSync(file);
    } else {
      let url=inputURL(a.url),response;
      for(let hops=0;hops<=3;hops++) {
        response=await fetchImpl(url,{redirect:'manual',signal:AbortSignal.timeout(300000)});
        if(![301,302,303,307,308].includes(response.status))break;
        const next=response.headers.get('location');await response.body?.cancel();if(!next||hops===3)throw Error('Multilang redirect bound');url=inputURL(new URL(next,url).href);
      }
      if(!response.ok||!response.body)throw Error('Multilang input HTTP failure');
      const chunks=[];let bytes=0;for await(const chunk of response.body){bytes+=chunk.length;if(bytes>a.size)throw Error('Multilang input integrity/size failure');chunks.push(chunk);}raw=Buffer.concat(chunks);
    }
    if(raw.length!==a.size||sha(raw)!==a.sha256)throw Error('Multilang input integrity failure: '+a.file);
    fs.writeFileSync(path.join(output,a.file),raw,{flag:'wx',mode:0o600});receipt.files.push({file:a.file,size:raw.length,sha256:a.sha256});
  }
  fs.writeFileSync(path.join(output,'input-receipt.json'),JSON.stringify(receipt,null,2)+'\n',{flag:'wx'});return receipt;
}
module.exports={selectInputs,prepareMultilangInputs};
if(require.main===module)prepareMultilangInputs({output:process.argv[2]||'',arch:process.argv[3]||'',local:process.argv[4]}).then(r=>console.log(JSON.stringify({arch:r.arch,files:r.files.length}))).catch(e=>{console.error(e.message);process.exitCode=1;});
