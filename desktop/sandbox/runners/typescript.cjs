'use strict';
const fs = require('node:fs');
const path = require('node:path');
const OPTIONS = {target:'ES2022',module:'NodeNext',moduleResolution:'NodeNext',strict:true,noEmitOnError:true,allowJs:false,skipLibCheck:false};
const keys = x => Object.keys(x).sort().join(',');
function strictConfig(raw, ts) {
  // JSON.parse forbids comments/trailing commas. The compiler AST additionally
  // rejects duplicate keys, which JSON.parse alone silently overwrites.
  const parsed = JSON.parse(raw);
  const tree = ts.parseJsonText('tsconfig.json', raw);
  function unique(n) {
    if (ts.isObjectLiteralExpression(n)) {
      const seen = new Set();
      for (const p of n.properties) {
        if (!ts.isPropertyAssignment(p) || !ts.isStringLiteral(p.name) || seen.has(p.name.text)) throw new Error('config key');
        seen.add(p.name.text);
      }
    }
    ts.forEachChild(n, unique);
  }
  unique(tree);
  if (tree.parseDiagnostics.length || !parsed || keys(parsed) !== 'compilerOptions,files' || !parsed.compilerOptions || keys(parsed.compilerOptions) !== keys(OPTIONS)) throw new Error('config grammar');
  for (const [key, value] of Object.entries(OPTIONS)) if (parsed.compilerOptions[key] !== value) throw new Error('compiler option');
  return parsed;
}
function compile(c, ts) {
  if (ts.version !== '5.9.3') throw new Error('compiler version');
  const root = path.resolve(c.source_root), out = path.resolve(c.output_root);
  const raw = fs.readFileSync(path.join(root, 'tsconfig.json'));
  if (raw.length > 1048576) throw new Error('config size');
  const config = strictConfig(raw.toString('utf8'), ts);
  if (JSON.stringify(config.files) !== JSON.stringify(c.input_files)) throw new Error('input inventory');
  const admitted = new Set(c.input_files.map(f => path.join(root, f)));
  const expected = new Set(c.input_files.filter(f => !f.endsWith('.d.ts')).map(f => path.join(out, f.slice(0, -3) + '.js')));
  const options = {target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.NodeNext,moduleResolution:ts.ModuleResolutionKind.NodeNext,strict:true,noEmitOnError:true,allowJs:false,skipLibCheck:false,rootDir:root,outDir:out};
  const lib = path.dirname(ts.getDefaultLibFilePath(options));
  const deps = path.join(root, 'node_modules');
  const inside = (f, dir) => f.startsWith(dir + path.sep);
  function allowed(f) {
    f = path.resolve(f);
    if (admitted.has(f) || f === path.join(root, 'package.json')) return true;
    if (inside(f, deps)) return f.endsWith('.d.ts') || path.basename(f) === 'package.json';
    if (inside(f, root) && f.endsWith('.ts') && fs.existsSync(f)) throw new Error('unadmitted_typescript_input');
    return path.dirname(f) === lib && /^lib\..*\.d\.ts$/.test(path.basename(f));
  }
  const read = f => { if (!allowed(f)) return undefined; try { return fs.readFileSync(f, 'utf8'); } catch (e) { if (e.code === 'ENOENT' || e.code === 'ENOTDIR') return undefined; throw e; } };
  const host = ts.createCompilerHost(options);
  host.readFile = read;
  host.fileExists = f => allowed(f) && fs.existsSync(f) && fs.statSync(f).isFile();
  host.getSourceFile = (f, language, onError) => { const source = read(f); return source === undefined ? undefined : ts.createSourceFile(f, source, language, true); };
  host.getCurrentDirectory = () => root;
  host.realpath = f => path.resolve(f); // admitted trees contain no links
  host.directoryExists = f => { f = path.resolve(f); return (f === root || f === deps || inside(f, deps) || inside(f, root) && c.input_files.some(p => path.join(root,p).startsWith(f + path.sep)) || f === lib) && fs.existsSync(f) && fs.statSync(f).isDirectory(); };
  host.getDirectories = f => { f = path.resolve(f); if (f !== deps && !inside(f, deps)) return []; try { return fs.readdirSync(f, {withFileTypes:true}).filter(d => d.isDirectory()).map(d => d.name).sort(); } catch (e) { if (e.code === 'ENOENT' || e.code === 'ENOTDIR') return []; throw e; } };
  host.readDirectory = () => [];
  let bytes = 0;
  const emitted = new Set();
  host.writeFile = (f, data) => {
    f = path.resolve(f);
    const size = Buffer.byteLength(data);
    if (!expected.has(f) || emitted.has(f) || size > 8388608 || emitted.size === 1000 || bytes + size > 67108864) throw new Error('output inventory');
    fs.mkdirSync(path.dirname(f), {recursive:true,mode:0o700});
    fs.writeFileSync(f, data, {flag:'wx',mode:0o600});
    bytes += size; emitted.add(f);
  };
  const program = ts.createProgram([...admitted], options, host);
  // TypeScript normally permits unresolved side-effect-only imports. Close
  // that hole explicitly rather than silently omitting their project inputs.
  const ambient = new Set(program.getTypeChecker().getAmbientModules().map(s => s.name));
  for (const source of program.getSourceFiles()) if (admitted.has(path.resolve(source.fileName))) {
    for (const statement of source.statements) if (ts.isImportDeclaration(statement) && !statement.importClause && ts.isStringLiteral(statement.moduleSpecifier)) {
      const name = statement.moduleSpecifier.text;
      if (!ts.resolveModuleName(name, source.fileName, options, host).resolvedModule && !ambient.has(JSON.stringify(name))) throw new Error('unresolved_side_effect_import');
    }
  }
  const diagnostics = ts.getPreEmitDiagnostics(program);
  if (diagnostics.length) throw new Error('typescript_diagnostics');
  const result = program.emit();
  if (result.emitSkipped || result.diagnostics.length || emitted.size !== expected.size) throw new Error('typescript_emit_failed');
}
async function probe(ts) {
  const dir = fs.mkdtempSync('/tmp/foundry-ts-probe-');
  try {
    const out = path.join(dir, 'out'); fs.mkdirSync(out);
    fs.writeFileSync(path.join(dir,'owned.ts'),'export const answer: number = 42;\n',{flag:'wx',mode:0o600});
    fs.writeFileSync(path.join(dir,'package.json'),'{"type":"module"}',{flag:'wx',mode:0o600});
    fs.writeFileSync(path.join(dir,'tsconfig.json'),JSON.stringify({compilerOptions:OPTIONS,files:['owned.ts']}),{flag:'wx',mode:0o600});
    compile({source_root:dir,output_root:out,input_files:['owned.ts']},ts);
    await require('./node.cjs').probe();
  } finally { fs.rmSync(dir,{recursive:true}); }
}
async function main() {
  if (process.argv.length !== 2 || process.versions.node !== '24.22.1') throw new Error('runtime version');
  const raw = fs.readFileSync(4); fs.closeSync(4);
  if (raw.length > 1048576) throw new Error('contract size');
  const c = JSON.parse(raw), ts = require('/opt/foundry/typescript/lib/typescript.js');
  if (c.phase === 'probe-typescript') { await probe(ts); return; }
  if (c.phase !== 'compile-typescript') throw new Error('phase');
  compile(c,ts);
}
module.exports = { compile, strictConfig };
if (require.main === module) main().catch(() => { process.stderr.write('owned_typescript_runner_failed\n'); process.exitCode = 2; });
