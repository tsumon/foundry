'use strict';
// Release/build-side linkage; installed startup never opens source archives.
const fs=require('node:fs'),path=require('node:path'),{isDeepStrictEqual}=require('node:util');
const {verifySandboxBundle,verifySandboxBundleV2,resourceDigest,strictJSON}=require('./runtime.cjs');
const {verifySourceSidecar}=require('./build-support.cjs');
const {sourceInventory}=require('./sandbox/build.cjs');
function inventory(output,resources,installer,host) {
  if(!path.isAbsolute(output)||!path.isAbsolute(resources)||path.basename(installer)!==installer||!/^Foundry-[0-9][A-Za-z0-9.+-]*-(?:arm64\.dmg|x64\.exe)$/.test(installer))throw Error('Canonical distribution paths required');
  const bundles=[],files=[];
  verifySandboxBundle(path.join(resources,'sandbox'),host);verifySandboxBundleV2(path.join(resources,'sandbox-v2'),host);
  for(const [bundle,sidecar] of [['sandbox','sandbox-sources'],['sandbox-v2','sandbox-v2-sources']]) {
    verifySourceSidecar(path.join(resources,bundle),path.join(output,sidecar),sidecar);
    bundles.push({path:bundle,manifest_sha256:resourceDigest(path.join(resources,bundle,'manifest.json')),source_sidecar:sidecar});
    files.push(...sourceInventory(path.join(output,sidecar)).map(f=>({...f,path:sidecar+'/'+f.path})));
  }
  const file=path.join(output,installer),info=fs.lstatSync(file);if(!info.isFile()||info.nlink!==1)throw Error('Regular distribution installer required');
  files.push({path:installer,size:info.size,sha256:resourceDigest(file)});
  return {schema_version:1,host,installer,bundles,files:files.sort((a,b)=>a.path<b.path?-1:a.path>b.path?1:0)};
}
function writeDistributionManifest({output,resources,installer,host}) {
  if(path.dirname(installer)!==output)throw Error('Installer must be a direct distribution artifact');
  const value=inventory(output,resources,path.basename(installer),host),file=path.join(output,'distribution-manifest.json');
  fs.writeFileSync(file,JSON.stringify(value,null,2)+'\n',{flag:'wx'});
  fs.writeFileSync(path.join(output,'SHA256SUMS'),resourceDigest(installer)+'  '+path.basename(installer)+'\n'+resourceDigest(file)+'  distribution-manifest.json\n');
  return value;
}
function verifyDistribution(output,resources,host) {
  const file=path.join(output,'distribution-manifest.json'),info=fs.lstatSync(file);if(!info.isFile()||info.nlink!==1||info.size>4*1024**2)throw Error('Bounded distribution manifest required');
  const actual=strictJSON(fs.readFileSync(file,'utf8'));
  if(!isDeepStrictEqual(actual,inventory(output,resources,actual.installer,host)))throw Error('Distribution integrity mismatch');
  return true;
}
module.exports={writeDistributionManifest,verifyDistribution};
if(require.main===module) {
  try {if(process.argv[2]!=='--verify')throw Error('Explicit --verify output resources host required');verifyDistribution(...process.argv.slice(3));console.log('Verified dual runtime distribution and corresponding sources');}
  catch(e){console.error(e.message);process.exitCode=1;}
}
