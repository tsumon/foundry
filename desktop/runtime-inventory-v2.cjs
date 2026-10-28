'use strict';
const crypto=require('node:crypto');
const keys=(v,n)=>v&&typeof v==='object'&&!Array.isArray(v)&&Object.keys(v).length===n.length&&n.every(k=>Object.hasOwn(v,k));
function canonicalInventory(value) {
  if(Array.isArray(value))return '['+value.map(canonicalInventory).join(',')+']';
  if(value&&typeof value==='object')return '{'+Object.keys(value).sort().map(k=>JSON.stringify(k)+':'+canonicalInventory(value[k])).join(',')+'}';
  return JSON.stringify(value);
}
const inventoryDigest=raw=>crypto.createHash('sha256').update('foundry:runtime-inventory:v2\n').update(raw).digest('hex');
function parseRuntimeInventoryV2(raw) {
  const bad=()=>{throw Error('Invalid runtime inventory v2');};
  if(!Buffer.isBuffer(raw)||raw.length<1||raw.length>65536)bad();
  const d=JSON.parse(raw.toString('utf8'));
  if(!raw.equals(Buffer.from(canonicalInventory(d))))bad();
  const same=(a,b)=>canonicalInventory(a)===canonicalInventory(b);
  const hash=v=>typeof v==='string'&&/^[a-f0-9]{64}$/.test(v);
  const file=(v,p,max)=>keys(v,['path','size','sha256'])&&v.path===p&&Number.isSafeInteger(v.size)&&v.size>0&&v.size<=max&&hash(v.sha256);
  if(!keys(d,['schema_version','guest_platform','projection_policy','projected_runtime','supervisor','node','python','typescript','runners','framework_report','child_contract_version','compiler_contract'])||d.schema_version!==2||!['linux/arm64','linux/amd64'].includes(d.guest_platform)||d.projection_policy!=='foundry-runtime-projection-v2'||d.child_contract_version!==1)bad();
  const p=d.projected_runtime;
  if(!keys(p,['sha256','entries','regular_bytes'])||!hash(p.sha256)||!Number.isSafeInteger(p.entries)||p.entries<1||p.entries>20000||!Number.isSafeInteger(p.regular_bytes)||p.regular_bytes<1||p.regular_bytes>512*1024**2||!file(d.supervisor,'/init',64*1024**2))bad();
  if(!keys(d.node,['version','executable'])||d.node.version!=='24.22.1'||!file(d.node.executable,'/opt/foundry/node/bin/node',128*1024**2))bad();
  if(!keys(d.python,['version','distribution','package_version','executable','stdlib','options'])||d.python.version!=='3.13.5'||d.python.distribution!=='Debian trixie'||d.python.package_version!=='3.13.5-2+deb13u5'||d.python.stdlib!=='/usr/lib/python3.13'||!same(d.python.options,['-I','-S','-B'])||!file(d.python.executable,'/usr/bin/python3.13',128*1024**2))bad();
  if(!keys(d.typescript,['version','compiler'])||d.typescript.version!=='5.9.3'||!file(d.typescript.compiler,'/opt/foundry/typescript/lib/typescript.js',128*1024**2))bad();
  const tuples=[['c4_node_model_v2','node-test-v2','node.cjs'],['c4_python_model_v2','python-unittest-v2','python.py'],['c4_typescript_model_v2','typescript-node-test-v2','typescript.cjs']];
  if(!Array.isArray(d.runners)||d.runners.length!==3)bad();
  for(const [i,[profile,runner,name]] of tuples.entries()) {
    const r=d.runners[i];if(!keys(r,['profile_id','runner_id','file'])||r.profile_id!==profile||r.runner_id!==runner||!file(r.file,'/opt/foundry/runners/'+name,1024**2))bad();
  }
  if(!same(d.framework_report,{schema_version:1,node_runner:'node-test-v2',python_runner:'python-unittest-v2',max_bytes:2097152,max_cases:4096})||!same(d.compiler_contract,{schema_version:1,config_path:'tsconfig.json',root_dir:'/workspace',out_dir:'/foundry-build/compiled',compiler_options:{target:'ES2022',module:'NodeNext',module_resolution:'NodeNext',strict:true,no_emit_on_error:true,allow_js:false,skip_lib_check:false},max_files:1000,max_bytes:67108864,max_file_bytes:8388608}))bad();
  return d;
}
module.exports={canonicalInventory,inventoryDigest,parseRuntimeInventoryV2};
