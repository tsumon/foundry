'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const zlib = require('node:zlib');
const { buildInitramfs, readTar, staticELF } = require('../sandbox/build-guest.cjs');

function tar(entries) {
  const chunks=[];
  for (const [name,data,type='0',mode=(name==='go/bin/go'||name.endsWith('/compile'))?'0000755\0':'0000644\0'] of entries) {
    const b=Buffer.from(data),h=Buffer.alloc(512);h.write(name);h.write(mode,100);h.write('0000000\0',108);h.write('0000000\0',116);h.write(b.length.toString(8).padStart(11,'0')+'\0',124);h.write('00000000000\0',136);h.fill(32,148,156);h.write(type,156);h.write('ustar\0',257);h.write('00',263);
    h.write([...h].reduce((a,b)=>a+b,0).toString(8).padStart(6,'0')+'\0 ',148);chunks.push(h,b,Buffer.alloc((512-b.length%512)%512));
  }
  return Buffer.concat([...chunks,Buffer.alloc(1024)]);
}
function elf(arch='amd64') {const b=Buffer.alloc(64);b.write('\x7fELF');b[4]=2;b[5]=1;b[6]=1;b.writeUInt16LE(2,16);b.writeUInt16LE(arch==='amd64'?62:183,18);b.writeUInt32LE(1,20);b.writeUInt16LE(64,52);b.writeUInt16LE(56,54);return b;}
function archive(arch='amd64') {return zlib.gzipSync(tar([['go/VERSION','go1.27.3\ntime 2026-10-01T00:00:00Z\n'],['go/bin/go',elf(arch)],['go/pkg/tool/linux_'+arch+'/compile',elf(arch),'0','0000755\0'],['go/src/runtime/runtime.go','package runtime\n']]))}
function cpioEntries(b) {let at=0,entries=new Map();for(;;){assert.equal(b.toString('ascii',at,at+6),'070701');const fields=Array.from({length:13},(_,i)=>parseInt(b.toString('ascii',at+6+i*8,at+14+i*8),16)),size=fields[6],n=fields[11];const name=b.toString('utf8',at+110,at+110+n-1);at=(at+110+n+3)&~3;if(name==='TRAILER!!!')break;assert.ok(!entries.has(name),'duplicate cpio entry');entries.set(name,{fields,data:b.subarray(at,at+size)});at=(at+size+3)&~3;}return entries;}
test('deterministic root-owned newc includes pinned toolchain and init',()=>{
 const args={guest:elf(),goArchive:archive(),arch:'amd64'};
 const a=buildInitramfs(args),b=buildInitramfs(args);assert.deepEqual(a,b);
 assert.deepEqual([...cpioEntries(zlib.gunzipSync(a)).keys()].filter(n=>n==='init'||n==='usr/local/go/bin/go'),['init','usr/local/go/bin/go']);
});
test('both images supply only the fixed null device needed before Go PID1 bootstrap',()=>{
 for(const arch of ['amd64','arm64']) {
  const args={guest:elf(arch),goArchive:archive(arch),arch},image=buildInitramfs(args);
  assert.deepEqual(image,buildInitramfs(args));
  const entries=cpioEntries(zlib.gunzipSync(image)),node=entries.get('dev/null');
  assert.ok(node,'Go checkfds needs /dev/null before Main can mount devtmpfs');
  assert.deepEqual(node.fields.slice(1,11),[0o020600,0,0,1,0,0,0,0,1,3]);
  assert.equal(node.data.length,0);
  assert.deepEqual([...entries.keys()].filter(n=>n.startsWith('dev/')),['dev/null']);
  for(const [name,{fields}] of entries)if(name!=='dev/null') {
   assert.ok([0o100000,0o40000].includes(fields[1]&0o170000));
   assert.deepEqual(fields.slice(9,11),[0,0]);
  }
 }
});
test('a non-executable Go toolchain cannot yield a bootable-looking image',()=>{
 const bad=zlib.gzipSync(tar([['go/VERSION','go1.27.3\n'],['go/bin/go',elf(),'0','0000644\0'],['go/pkg/tool/linux_amd64/compile',elf()],['go/src/runtime/runtime.go','package runtime\n']]));
 assert.throws(()=>buildInitramfs({guest:elf(),goArchive:bad,arch:'amd64'}),/executable/);
});
test('archive parser rejects traversal, links, duplicate paths, checksum and non-Go content',()=>{
 for(const entries of [[['../evil','x']],[['go/../evil','x']],[['go/link','', '2']],[['go/dev/null','','3']],[['go/dev/disk','','4']],[['go/VERSION','x'],['go/VERSION','y']],[['bin/sh','x']]]) assert.throws(()=>readTar(tar(entries)));
 const corrupt=tar([['go/x','x']]);corrupt[0]^=1;assert.throws(()=>readTar(corrupt),/checksum/);
});
test('wrong Go version or guest platform and dynamic interpreter fail closed',()=>{
 assert.throws(()=>buildInitramfs({guest:elf(),goArchive:zlib.gzipSync(tar([['go/VERSION','go1.27.2\n']])),arch:'amd64'}));
 assert.throws(()=>staticELF(elf('arm64'),'amd64'));
 const dynamic=Buffer.concat([elf(),Buffer.alloc(56)]);dynamic.writeBigUInt64LE(64n,32);dynamic.writeUInt16LE(1,56);dynamic.writeUInt32LE(3,64);assert.throws(()=>staticELF(dynamic,'amd64'),/static/);
});
test('official Go PAX unicode path metadata is parsed but cannot smuggle traversal or links',()=>{
 const pax='50 path=go/test/fixedbugs/issue27836.dir/Þfoo.go\n';
 const entries=readTar(tar([['go/test/PaxHeaders.0/foo.go',pax,'x'],['go/test/foo.go','package main\n']]));
 assert.equal(entries.get('go/test/fixedbugs/issue27836.dir/Þfoo.go').data.toString(),'package main\n');
 assert.equal(entries.size,1);
 for(const record of ['20 path=../escape\n','17 linkpath=bad\n',pax+'13 uid=10000\n'])assert.throws(()=>readTar(tar([['go/pax',record,'x'],['go/a','x']])));
 assert.throws(()=>readTar(tar([['go/pax',pax,'x']])));
});
