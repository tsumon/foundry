'use strict';
// Owned portable fixtures only; host execution is not pinned Linux VM evidence.
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os'),cp=require('node:child_process'),crypto=require('node:crypto');
function fixture(t) {const root=fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'foundry-runners-v2-')));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));return root;}
function contract(root,file,phase){return {schema_version:1,phase,source_root:root,output_root:'',source_roots:['.'],test_files:[file],input_files:[]};}
function childEnv(root){return {LANG:'C.UTF-8',TZ:'UTC',TMPDIR:root,TEMP:root,TMP:root,...(process.env.SystemRoot?{SystemRoot:process.env.SystemRoot}:{} )};}
function report(file){const rows=fs.readFileSync(file,'utf8').trim().split('\n').map(JSON.parse);assert.equal(rows[0].schema_version,1);assert.equal(rows.at(-1).type,'suite_end');return rows;}
test('image-owned Node adapter emits a native selected-case report on the build host',t=>{
  const root=fixture(t),output=path.join(root,'report');fs.writeFileSync(path.join(root,'owned.test.cjs'),"require('node:test')('owned case',()=>{});\n");
  const runner=path.resolve(__dirname,'../sandbox/runners/node.cjs');const script="const fs=require('node:fs'),fd=fs.openSync(process.argv[3],'wx');require(process.argv[2]).runTests(JSON.parse(process.argv[4]),fd).then(c=>{fs.closeSync(fd);process.exitCode=c;},()=>{process.exitCode=2});";
  const bridge=path.join(root,'bridge.cjs');fs.writeFileSync(bridge,script);
  const result=cp.spawnSync(process.execPath,[bridge,runner,output,JSON.stringify(contract(root,'owned.test.cjs','test-node'))],{cwd:root,env:childEnv(root),encoding:'utf8',timeout:15000,shell:false});assert.equal(result.status,0,result.stderr);assert.equal(report(output).filter(r=>r.type==='case_end'&&r.status==='passed').length,1);
});
test('image-owned Python adapter distinguishes method success from setup subtest failure',t=>{
  const root=fixture(t),python=process.env.FOUNDRY_BUILD_PYTHON;assert.ok(python&&path.isAbsolute(python));const runner=path.resolve(__dirname,'../sandbox/runners/python.py');
  for(const [name,body,code,cases] of [['pass','import unittest\nclass T(unittest.TestCase):\n def test_owned(self): self.assertEqual(1,1)\n',0,1],['setup','import unittest\nclass T(unittest.TestCase):\n def setUp(self):\n  with self.subTest(stage="setup"): self.fail("infrastructure")\n def test_owned(self): pass\n',1,0]]) {
    const file=name+'.py',output=path.join(root,name+'.report');fs.writeFileSync(path.join(root,file),body);
    const script='import runpy,sys,json,os; m=runpy.run_path(sys.argv[1]); fd=os.open(sys.argv[2],os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600); sys.exit(m["run_tests"](json.loads(sys.argv[3]),fd))';
    const result=cp.spawnSync(python,['-I','-S','-B','-c',script,runner,output,JSON.stringify(contract(root,file,'test-python'))],{cwd:root,env:childEnv(root),encoding:'utf8',timeout:15000,shell:false});assert.equal(result.status,code,result.stderr);assert.equal(report(output).filter(r=>r.type==='case_end').length,cases);
  }
});
test('image-owned TypeScript adapter uses the lock-verified compiler and rejects changed options',async t=>{
  const root=fixture(t),input=path.join(process.env.FOUNDRY_MULTILANG_INPUTS||path.resolve(__dirname,'../../../build/multilang-inputs'),'typescript-5.9.3.tgz');
  const raw=fs.readFileSync(input);assert.equal(raw.length,4377468);assert.equal(crypto.createHash('sha256').update(raw).digest('hex'),'10e108c9cf7d5f2879053dff18515fb405abf2ccef63eaaf017d9c571687a1d3');
  const compilerRoot=path.join(root,'compiler');fs.mkdirSync(compilerRoot);
  await require('tar').t({file:input,onReadEntry(entry){const name=entry.path.replace(/^package\/lib\//,'');if(entry.type!=='File'||name===entry.path||name.includes('/')||!(name==='typescript.js'||/^lib\..*\.d\.ts$/.test(name)))return;assert.ok(entry.size<=12*1024**2);const chunks=[];entry.on('data',b=>chunks.push(b));entry.on('end',()=>fs.writeFileSync(path.join(compilerRoot,name),Buffer.concat(chunks),{flag:'wx'}));}});
  const ts=require(path.join(compilerRoot,'typescript.js'));assert.equal(ts.version,'5.9.3');const {compile}=require('../sandbox/runners/typescript.cjs');
  const source=path.join(root,'source'),out=path.join(root,'out');fs.mkdirSync(source);fs.mkdirSync(out);fs.writeFileSync(path.join(source,'a.ts'),'export const owned: number = 1;');fs.writeFileSync(path.join(source,'package.json'),'{"type":"module"}');
  const options={target:'ES2022',module:'NodeNext',moduleResolution:'NodeNext',strict:true,noEmitOnError:true,allowJs:false,skipLibCheck:false};fs.writeFileSync(path.join(source,'tsconfig.json'),JSON.stringify({compilerOptions:options,files:['a.ts']}));
  const c={...contract(source,'a.ts','compile-typescript'),input_files:['a.ts'],output_root:out};compile(c,ts);assert.match(fs.readFileSync(path.join(out,'a.js'),'utf8'),/export const owned = 1/);
  fs.unlinkSync(path.join(out,'a.js'));fs.writeFileSync(path.join(source,'tsconfig.json'),JSON.stringify({compilerOptions:{...options,strict:false},files:['a.ts']}));assert.throws(()=>compile(c,ts),/compiler option/);assert.equal(fs.existsSync(path.join(out,'a.js')),false);
});
