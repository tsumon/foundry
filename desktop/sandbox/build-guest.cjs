'use strict';
// Offline deterministic image assembly. Input hashes/provenance are verified by
// the caller's pinned asset loader; no downloads, shell, Docker or guest code.
const fs = require('node:fs');
const zlib = require('node:zlib');

const MAX_TAR = 768 * 1024 * 1024;
function ordinary(name) {
  return typeof name === 'string' && name.length > 0 && name.length <= 4096 && !/[\\\x00-\x1f\x7f]/.test(name) && !name.startsWith('/') && name.split('/').every(p=>p && p!=='.' && p!=='..');
}
function ascii(b) { const i=b.indexOf(0);return b.subarray(0,i<0?b.length:i).toString('utf8'); }
function octal(b) {const s=ascii(b).trim();if(!/^[0-7]+$/.test(s))throw Error('invalid tar number');return parseInt(s,8);}
function paxPath(b) {
  // Official Go archives use local PAX path metadata for two Unicode test
  // files. No global metadata, ownership override, sparse file or link fields.
  if(b.length>8192)throw Error('oversized PAX metadata');
  const space=b.indexOf(32),length=b.subarray(0,space).toString('ascii');
  if(space<1||!/^\d+$/.test(length)||Number(length)!==b.length||b[b.length-1]!==10)throw Error('invalid PAX record');
  const field=b.subarray(space+1,b.length-1),text=field.toString('utf8');
  if(!Buffer.from(text).equals(field)||!text.startsWith('path=')||!ordinary(text.slice(5))||!text.slice(5).startsWith('go/'))throw Error('unsafe PAX path');
  return text.slice(5);
}
function readTar(buffer) {
  if(buffer.length>MAX_TAR || buffer.length%512)throw Error('invalid tar size');
  const entries=new Map();let end=false,pendingPath=null;
  for(let at=0;at<buffer.length;) {
    const h=buffer.subarray(at,at+512);at+=512;
    if(h.every(v=>v===0)) {if(buffer.subarray(at).some(v=>v!==0))throw Error('trailing tar data');end=true;break;}
    const checksum=octal(h.subarray(148,156));let sum=0;for(let i=0;i<512;i++)sum+=(i>=148&&i<156)?32:h[i];if(sum!==checksum)throw Error('tar checksum mismatch');
    if(ascii(h.subarray(257,263))!=='ustar')throw Error('unsupported tar format');
    const prefix=ascii(h.subarray(345,500));let name=(prefix?prefix+'/':'')+ascii(h.subarray(0,100));
    const type=h[156];if(type!==0&&type!==48&&type!==53&&type!==120)throw Error('unsupported archive member');
    if(type===53&&name.endsWith('/'))name=name.slice(0,-1);
    if(!ordinary(name)||(name!=='go'&&!name.startsWith('go/'))||entries.has(name))throw Error('unsafe or duplicate archive path');
    const size=octal(h.subarray(124,136));if(size>MAX_TAR||at+size>buffer.length||(type===53&&size!==0))throw Error('invalid archive member size');
    if(type===120){if(pendingPath)throw Error('multiple PAX records');pendingPath=paxPath(buffer.subarray(at,at+size));at+=Math.ceil(size/512)*512;continue;}
    if(pendingPath){name=pendingPath;pendingPath=null;if(entries.has(name))throw Error('duplicate PAX path');}
    const mode=octal(h.subarray(100,108));if(mode&0o7000)throw Error('privileged archive member');
    entries.set(name,{data:buffer.subarray(at,at+size),directory:type===53,mode:mode&0o111?0o555:0o444});
    if(entries.size>50000)throw Error('too many archive members');at+=Math.ceil(size/512)*512;
  }
  if(!end||pendingPath)throw Error('missing archive terminator or orphan PAX metadata');
  for(const name of entries.keys())for(let p=name.lastIndexOf('/');p>0;p=name.lastIndexOf('/',p-1)){const parent=entries.get(name.slice(0,p));if(parent&&!parent.directory)throw Error('archive file used as directory');}
  return entries;
}
function staticELF(b,arch) {
  if(!['arm64','amd64'].includes(arch)||b.length<64||b.toString('hex',0,4)!=='7f454c46'||b[4]!==2||b[5]!==1||b[6]!==1||b.readUInt16LE(18)!==(arch==='arm64'?183:62)||![2,3].includes(b.readUInt16LE(16)))throw Error('invalid guest ELF platform');
  const offset=Number(b.readBigUInt64LE(32)),count=b.readUInt16LE(56),size=b.readUInt16LE(54);
  if(!Number.isSafeInteger(offset)||size!==56||offset+count*size>b.length)throw Error('invalid ELF program headers');
  for(let i=0;i<count;i++)if(b.readUInt32LE(offset+i*size)===3)throw Error('guest must be static (no interpreter)');
}
function newc(entries) {
  // Go repairs closed standard descriptors before PID1 can mount devtmpfs.
  // This one boot-owned device never comes from the input Go archive.
  if(entries.has('dev/null'))throw Error('boot device collision');
  const chunks=[];let inode=1;
  for(const [name,e] of [...entries.entries(),['dev/null',{mode:0o020600}]].sort(([a],[b])=>a<b?-1:a>b?1:0).concat([['TRAILER!!!',{mode:0,data:Buffer.alloc(0)}]])) {
    const data=e.data||Buffer.alloc(0),n=Buffer.from(name+'\0');
    const fields=[inode++,e.mode,0,0,e.mode&0o40000?2:1,0,data.length,0,0,name==='dev/null'?1:0,name==='dev/null'?3:0,n.length,0];
    const h=Buffer.from('070701'+fields.map(v=>v.toString(16).padStart(8,'0')).join(''));
    chunks.push(h,n,Buffer.alloc((4-(h.length+n.length)%4)%4),data,Buffer.alloc((4-data.length%4)%4));
  }
  return Buffer.concat(chunks);
}
function buildInitramfs({guest,goArchive,arch}) {
  staticELF(guest,arch);
  if(goArchive.length>256*1024*1024)throw Error('Go archive too large');
  const go=readTar(zlib.gunzipSync(goArchive,{maxOutputLength:MAX_TAR}));
  const version=go.get('go/VERSION');if(!version||!/^go1\.27\.3\n(?:time [^\n]+\n)?$/.test(version.data.toString()))throw Error('exact Go 1.27.3 required');
  for(const name of ['go/bin/go',`go/pkg/tool/linux_${arch}/compile`]) {const e=go.get(name);if(!e||e.directory)throw Error('missing platform toolchain');if(!(e.mode&0o111))throw Error('non-executable platform toolchain');staticELF(e.data,arch);}
  if(!go.has('go/src/runtime/runtime.go'))throw Error('missing Go standard library');
  const entries=new Map();const directory=name=>{if(!entries.has(name))entries.set(name,{mode:0o40755});};
  for(const name of ['dev','proc','sys','sys/fs','sys/fs/cgroup','workspace','tmp','foundry-build','usr','usr/local'])directory(name);
  for(const [name,e] of go) {
    const dest='usr/local/'+name;
    for(let p=dest.lastIndexOf('/');p>0;p=dest.lastIndexOf('/',p-1))directory(dest.slice(0,p));
    entries.set(dest,{mode:e.directory?0o40755:0o100000|e.mode,data:e.data});
  }
  entries.set('init',{mode:0o100555,data:guest});
  return zlib.gzipSync(newc(entries),{level:9,mtime:0});
}
function regular(file,max) {const st=fs.lstatSync(file);if(!st.isFile()||st.isSymbolicLink()||st.size>max)throw Error('invalid input file');return fs.readFileSync(file);}
function main(args) {
  const opts={};for(let i=0;i<args.length;i+=2){if(!['--guest','--go-archive','--arch','--output'].includes(args[i])||!args[i+1]||opts[args[i]])throw Error('usage: --guest static-init --go-archive verified.tar.gz --arch arm64|amd64 --output initramfs.gz');opts[args[i]]=args[i+1];}
  if(Object.keys(opts).length!==4)throw Error('all image inputs required');
  const image=buildInitramfs({guest:regular(opts['--guest'],64*1024*1024),goArchive:regular(opts['--go-archive'],256*1024*1024),arch:opts['--arch']});
  fs.writeFileSync(opts['--output'],image,{flag:'wx',mode:0o644});
}
module.exports={buildInitramfs,readTar,staticELF};
if(require.main===module){try{main(process.argv.slice(2));}catch(e){console.error(e.message);process.exitCode=1;}}
