'use strict';
// Offline, build-only. One v2 image serves all three fixed language profiles.
const fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto'),cp=require('node:child_process');
const {sourceInventory,verifyBuiltWindowsRuntime,verifyWindowsRedistribution}=require('./build.cjs');
const {verifySandboxBundle,verifySandboxBundleV2,resourceDigest}=require('../runtime.cjs');
const {validateOutput,verifySourceSidecar,verifyMacSandboxSignature}=require('../build-support.cjs');
const {parseRuntimeInventoryV2,inventoryDigest}=require('../runtime-inventory-v2.cjs');
const {buildGuestV2}=require('./build-guest-v2.cjs');
const sha=raw=>crypto.createHash('sha256').update(raw).digest('hex');
const json=(file,value)=>fs.writeFileSync(file,JSON.stringify(value,null,2)+'\n',{flag:'wx'});
function writeVMManifestV2(root,host) {
  if(!['darwin/arm64','windows/amd64'].includes(host))throw Error('Unsupported sandbox platform');
  if(fs.existsSync(path.join(root,'manifest.json')))throw Error('Refusing existing manifest');
  const raw=fs.readFileSync(path.join(root,'runtime-inventory-v2.json')),descriptor=parseRuntimeInventoryV2(raw);
  const runtime=host==='darwin/arm64'?'runtime/foundry-vm-helper':'runtime/qemu-system-x86_64.exe';
  const assets=sourceInventory(root),byPath=new Map(assets.map(a=>[a.path,a]));
  for(const n of [runtime,'kernel','initramfs.cpio.gz','foundry-vm-guest-v2','runtime-inventory-v2.json','legal/NOTICE.md','legal/GPL-2.0.txt','legal/sources.json','legal/runtime-components.json'])if(!byPath.has(n))throw Error('Missing v2 bundle item: '+n);
  const supervisor=byPath.get('foundry-vm-guest-v2');
  if(descriptor.guest_platform!==(host==='darwin/arm64'?'linux/arm64':'linux/amd64')||supervisor.sha256!==descriptor.supervisor.sha256||supervisor.size!==descriptor.supervisor.size)throw Error('Descriptor platform/supervisor mismatch');
  const manifest={schema_version:2,host_platform:host,guest_platform:descriptor.guest_platform,protocol:'foundry-vm-v2',policy_version:'multilang-vm-v2',runtime,kernel:'kernel',rootfs:'initramfs.cpio.gz',verifier:'foundry-vm-guest-v2',runtime_inventory:'runtime-inventory-v2.json',runtime_inventory_digest:inventoryDigest(raw),assets};
  json(path.join(root,'manifest.json'),manifest);verifySandboxBundleV2(root,host);return manifest;
}
function copyFile(source,target) {
  if(!fs.lstatSync(source).isFile())throw Error('Regular source input required');
  fs.mkdirSync(path.dirname(target),{recursive:true});fs.copyFileSync(source,target,fs.constants.COPYFILE_EXCL);
}
async function buildBundleV2({output,inputs,baseBundle,baseSources,nativeInputs,windowsRuntime,python=process.env.FOUNDRY_BUILD_PYTHON,go=process.env.FOUNDRY_GO||'go'}) {
  const host=process.platform==='win32'&&process.arch==='x64'?'windows/amd64':process.platform+'/'+process.arch;
  if(!['darwin/arm64','windows/amd64'].includes(host))throw Error('Native supported build host required');
  if(!python||!path.isAbsolute(python)||!inputs||!path.isAbsolute(inputs))throw Error('Explicit offline runtime inputs and trusted Python required');
  const source=path.resolve(__dirname,'../..'),arch=host==='darwin/arm64'?'arm64':'amd64';
  validateOutput(output,source);const base=verifySandboxBundle(baseBundle,host);verifySourceSidecar(baseBundle,baseSources,'sandbox-sources');
  const nativeLock=require('./inputs.lock.json'),lock=require('./multilang-inputs.lock.json');
  if(host==='darwin/arm64')verifyMacSandboxSignature(baseBundle);
  else {
    if(!windowsRuntime||!path.isAbsolute(windowsRuntime))throw Error('Explicit source-built Windows runtime required');
    const proof=JSON.parse(fs.readFileSync(path.join(windowsRuntime,'windows-runtime-build.json')));
    if(proof.kind!=='source-built-minimal-qemu'||proof.source_sha256!==nativeLock.windows.source_sha256)throw Error('Pinned minimal QEMU proof required');
    require('./windows-source-build.cjs').auditConfiguration({options:proof.options,devices:proof.devices,host:proof.host,target:proof.target});
    verifyBuiltWindowsRuntime(path.join(baseBundle,'runtime'),proof,nativeLock);verifyWindowsRedistribution(windowsRuntime,proof,nativeLock);
  }
  // Validate every runtime and corresponding-source input before compiling.
  const validated=buildGuestV2({python,inputs,arch,validateOnly:true,sources:true});
  const env={...process.env,GOENV:'off',GOTOOLCHAIN:'local',GOPROXY:'off',GOSUMDB:'off',GOTELEMETRY:'off',CGO_ENABLED:'0',GOOS:'linux',GOARCH:arch};
  const run=args=>cp.execFileSync(go,args,{cwd:source,env,encoding:'utf8',timeout:180000,maxBuffer:4*1024**2});
  const version=run(['version']).trim();if(!version.startsWith('go version go1.27.3 '))throw Error('Pinned Go 1.27.3 required');
  run(['mod','verify']);
  const archive='go1.27.3.linux-'+arch+'.tar.gz',goInput=path.join(nativeInputs||path.join(path.dirname(baseBundle),'inputs'),archive);
  require('./assets.cjs').verifyAsset(nativeLock.assets.find(a=>a.file===archive),fs.readFileSync(goInput));
  fs.mkdirSync(output,{mode:0o700});const bundle=path.join(output,'bundle'),sourceKit=path.join(output,'sandbox-v2-sources');fs.mkdirSync(bundle);fs.mkdirSync(sourceKit);
  fs.cpSync(path.join(baseBundle,'runtime'),path.join(bundle,'runtime'),{recursive:true,errorOnExist:true,force:false});copyFile(path.join(baseBundle,'kernel'),path.join(bundle,'kernel'));
  const guest=path.join(bundle,'foundry-vm-guest-v2');run(['build','-trimpath','-buildvcs=false','-ldflags=-s -w','-o',guest,'./cmd/foundry-vm-guest-v2']);
  const runners=['node.cjs','python.py','typescript.cjs'].map(n=>{const file=path.join(__dirname,'runners',n);return {path:'opt/foundry/runners/'+n,source:file,sha256:resourceDigest(file)};});
  const runnerManifest=path.join(output,'runners.json');json(runnerManifest,runners);
  const receipt=buildGuestV2({python,inputs,arch,guest,runners:runnerManifest,output:path.join(bundle,'initramfs.cpio.gz'),receipt:path.join(output,'image-build-v2.json'),inventory:path.join(bundle,'runtime-inventory-v2.json'),notices:path.join(bundle,'legal')});
  for(const a of base.assets.filter(a=>a.path.startsWith('legal/')&&!['legal/sources.json','legal/NOTICE.md'].includes(a.path)))copyFile(path.join(baseBundle,a.path),path.join(bundle,a.path));
  // Keep exact inherited kernel/native proofs and sources; all new recipes and
  // guest sources live separately so retained v1 recipes are never rewritten.
  fs.cpSync(baseSources,path.join(sourceKit,'native'),{recursive:true,errorOnExist:true,force:false});
  copyFile(goInput,path.join(sourceKit,'toolchain',archive));
  for(const a of lock.artifacts.filter(a=>['debian-corresponding-source','node-source','typescript-source','typescript-compiler'].includes(a.kind))) {
    const file=path.join(inputs,a.file),info=fs.lstatSync(file);if(!info.isFile()||info.size!==a.size||resourceDigest(file)!==a.sha256)throw Error('Corresponding source integrity mismatch');copyFile(file,path.join(sourceKit,'multilang',a.file));
  }
  const packages=run(['list','-deps','-f','{{if and .Module .Module.Main}}{{.ImportPath}}{{end}}','./cmd/foundry-vm-guest-v2']).split(/\r?\n/).filter(Boolean).map(n=>n.replace(/^foundry\//,'')+'/');
  const tracked=cp.execFileSync('git',['ls-files','--','*.go','go.mod','go.sum','LICENSE'],{cwd:source,encoding:'utf8'}).trim().split(/\r?\n/).filter(n=>!n.endsWith('.go')||packages.some(p=>n.startsWith(p)&&!n.slice(p.length).includes('/')));
  for(const relative of tracked)copyFile(path.join(source,relative),path.join(sourceKit,'foundry-source',relative));
  for(const a of sourceInventory(__dirname))if(/\.(cjs|py|json|swift|plist|c)$/.test(a.path))copyFile(path.join(__dirname,a.path),path.join(sourceKit,'foundry-source/desktop/sandbox',a.path));
  for(const name of ['build-support.cjs','runtime.cjs','runtime-inventory-v2.cjs'])copyFile(path.join(__dirname,'..',name),path.join(sourceKit,'foundry-source/desktop',name));
  const modules=[...new Set(run(['list','-deps','-f','{{if and .Module (not .Module.Main)}}{{.Module.Path}}@{{.Module.Version}}{{end}}','./cmd/foundry-vm-guest-v2']).split(/\r?\n/).filter(Boolean))];
  const moduleInputs=[];
  for(const spec of modules) {
    const info=JSON.parse(run(['mod','download','-json',spec]));
    if(!fs.readFileSync(path.join(source,'go.sum'),'utf8').split(/\r?\n/).includes(`${info.Path} ${info.Version} ${info.Sum}`))throw Error('Unpinned Go source module');
    const filename=spec.replaceAll('/','-').replace('@','-')+'.zip';copyFile(info.Zip,path.join(sourceKit,'go-modules',filename));
    moduleInputs.push({module:info.Path,version:info.Version,sum:info.Sum,file:filename,sha256:resourceDigest(info.Zip)});
    for(const n of ['LICENSE','PATENTS'])if(fs.existsSync(path.join(info.Dir,n)))copyFile(path.join(info.Dir,n),path.join(bundle,'legal/go-modules',filename+'-'+n));
  }
  json(path.join(sourceKit,'module-inputs.json'),moduleInputs);copyFile(path.join(__dirname,'multilang-inputs.lock.json'),path.join(sourceKit,'multilang-inputs.lock.json'));
  json(path.join(sourceKit,'build-contract.json'),{schema_version:2,host,guest_platform:'linux/'+arch,go:version,base_manifest_sha256:resourceDigest(path.join(baseBundle,'manifest.json')),projection:validated,runtime_inventory:receipt.descriptor,guest:receipt.guest,runners:receipt.runners,image:receipt.image,native_acceptance:'not_performed'});
  fs.writeFileSync(path.join(sourceKit,'README.md'),'# Foundry v2 corresponding sources\n\nThis sidecar accompanies the matching installer. multilang/ contains every pinned Debian upstream/distribution source, Node source, and TypeScript source/compiler artifact. native/ preserves kernel/native runtime sources and audited QEMU stdio/UTF-8/zlib proofs and recipes. toolchain/ contains exact Go source/toolchain input; go-modules/ contains pinned linked module source. foundry-source/ holds the exact guest and builder/runner recipes. Run desktop/sandbox/build-bundle-v2.cjs in the matching checkout with explicit local inputs; it invokes only pinned Go and trusted Python and performs no downloads. The descriptor precedes image compression; manifest identity is computed afterward. Build-only evidence is not native VM acceptance.\n',{flag:'wx'});
  fs.writeFileSync(path.join(bundle,'legal/NOTICE.md'),'# Bundled v2 sandbox components\n\nFoundry guest/runners/native helper: Apache-2.0. Go 1.27.3 and linked golang.org/x/sys: BSD-3-Clause (Go-LICENSE/Go-PATENTS and go-modules/). Linux kernel: GPL-2.0-only (GPL-2.0.txt). Node 24.22.1: MIT and bundled third-party terms in Node-LICENSE. Python 3.13.5 Debian trixie revision 3.13.5-2+deb13u5 and its complete runtime closure: exact per-package copyright notices in debian/; extracted upstream license texts in source-licenses/. TypeScript 5.9.3: Apache-2.0 and ThirdPartyNoticeText, included here. runtime-components.json maps every shipped package to exact corresponding sources and notices. Windows native QEMU/runtime notices and proofs remain in qemu/. This installer must be distributed with sandbox-v2-sources; sources.json binds its complete actual bytes. No native VM acceptance is claimed.\n',{flag:'wx'});
  json(path.join(bundle,'legal/sources.json'),{schema_version:1,sidecar:'sandbox-v2-sources',files:sourceInventory(sourceKit)});
  const manifest=writeVMManifestV2(bundle,host);verifySourceSidecar(bundle,sourceKit,'sandbox-v2-sources');
  json(path.join(output,'sandbox-build-v2.json'),{schema_version:2,host,base_manifest_sha256:resourceDigest(path.join(baseBundle,'manifest.json')),manifest_sha256:resourceDigest(path.join(bundle,'manifest.json')),runtime_inventory_digest:manifest.runtime_inventory_digest,native_acceptance:'not_performed'});
  return {bundle,sourceKit,manifest};
}
module.exports={writeVMManifestV2,buildBundleV2};
if(require.main===module)buildBundleV2({output:process.argv[2]||'',inputs:process.env.FOUNDRY_MULTILANG_INPUTS,baseBundle:process.env.FOUNDRY_SANDBOX_BUNDLE,baseSources:process.env.FOUNDRY_SANDBOX_SOURCES,nativeInputs:process.env.FOUNDRY_SANDBOX_INPUTS,windowsRuntime:process.env.FOUNDRY_WINDOWS_VM_RUNTIME}).then(r=>console.log(JSON.stringify({bundle:r.bundle,sourceKit:r.sourceKit,assets:r.manifest.assets.length}))).catch(e=>{console.error(e.message);process.exitCode=1;});
