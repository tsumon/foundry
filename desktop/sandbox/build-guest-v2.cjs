'use strict';
// Build-only interface. The desktop application never loads or invokes this file.
const path = require('node:path');
const {spawnSync} = require('node:child_process');

function buildGuestV2(options) {
  const keys = ['python','lock','inputs','arch','validateOnly','sources','guest','runners','output','receipt'];
  if (!options || Object.keys(options).some(k => !keys.includes(k))) throw Error('unknown v2 builder option');
  if (!options.python) throw Error('explicit trusted host Python >=3.13 path required');
  if (!path.isAbsolute(options.python)) throw Error('trusted Python path must be absolute');
  if (!['amd64','arm64'].includes(options.arch) || !options.inputs) throw Error('explicit inputs and architecture required');
  if (options.validateOnly && ['guest','runners','output','receipt'].some(k => options[k])) throw Error('validate-only cannot publish an image');
  const args = ['-I','-B',path.join(__dirname,'build-guest-v2.py'),'--lock',options.lock || path.join(__dirname,'multilang-inputs.lock.json'),'--inputs',options.inputs,'--arch',options.arch];
  for (const [key, flag] of [['validateOnly','--validate-only'],['sources','--sources']]) if (options[key]) args.push(flag);
  for (const key of ['guest','runners','output','receipt']) if (options[key]) args.push('--'+key,options[key]);
  const result = spawnSync(options.python,args,{encoding:'utf8',shell:false,maxBuffer:1024*1024,timeout:300000});
  if (result.error) throw result.error;
  if (result.status !== 0) throw Error(result.stderr.trim() || 'offline v2 builder failed');
  return JSON.parse(result.stdout);
}
module.exports = {buildGuestV2};
if (require.main === module) {
  try {
    const options = {};
    for (let i=2;i<process.argv.length;i++) {
      const flag = process.argv[i];
      const key = flag === '--validate-only' ? 'validateOnly' : flag.slice(2);
      if (!flag.startsWith('--') || Object.hasOwn(options,key)) throw Error('invalid/duplicate builder argument');
      if (['validateOnly','sources'].includes(key)) options[key]=true;
      else {if (!process.argv[i+1]) throw Error('missing builder argument'); options[key]=process.argv[++i];}
    }
    console.log(JSON.stringify(buildGuestV2(options)));
  } catch (error) {console.error(error.message);process.exitCode=1;}
}
