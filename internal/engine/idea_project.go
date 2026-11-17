package engine

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/platformfs"
	"foundry/internal/store"
	"foundry/internal/workspace"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ideaParentIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type IdeaCreateRequest struct {
	Title      string   `json:"title"`
	Goal       string   `json:"goal"`
	Acceptance []string `json:"acceptance"`
	ProfileID  string   `json:"profile_id"`
	MaxCalls   int      `json:"max_calls"`
}

func (e *Engine) IdeaAvailable() (bool, string) {
	for _, p := range e.CodingProfilesV2() {
		if p.ProfileID == "c4_node_model_v2" && p.Available {
			return true, ""
		}
	}
	return false, "embedded_vm_v2_node_unavailable"
}

func validateIdeaRequest(q IdeaCreateRequest) error {
	if boundedPlanText("title", q.Title, 200) != nil || boundedPlanText("goal", q.Goal, 16000) != nil || strings.TrimSpace(q.ProfileID) == "" || q.MaxCalls < 1 || q.MaxCalls > 16 || len(q.Acceptance) < 1 || len(q.Acceptance) > 24 {
		return domain.ImportInvalidRequest
	}
	for _, a := range q.Acceptance {
		if strings.TrimSpace(a) == "" || len(a) > 2000 || !utf8.ValidString(a) {
			return domain.ImportInvalidRequest
		}
	}
	return nil
}

// CreateIdea prepares controller-owned bytes only. Inspection/admission do not
// execute project scripts, consult credentials, invoke providers or start VMs.
func (e *Engine) CreateIdea(ctx context.Context, q IdeaCreateRequest) (domain.Mission, error) {
	origin := &domain.IdeaProject{SchemaVersion: 2, Template: "browser_app_v1", Acceptance: append([]string{}, q.Acceptance...)}
	return e.createManagedIdeaMission(ctx, q, origin, nil, "")
}

// ContinueIdea derives an immutable follow-up browser app mission from an
// existing idea mission's current candidate src/ files while keeping the parent
// mission, baseline, evidence, and budget read-only.
func (e *Engine) ContinueIdea(ctx context.Context, sourceMissionID string, q IdeaCreateRequest) (domain.Mission, error) {
	zero := domain.Mission{}
	if err := validateIdeaRequest(q); err != nil {
		return zero, err
	}
	if ok, _ := e.IdeaAvailable(); !ok {
		return zero, ErrC4Unavailable
	}
	if _, _, _, _, err := e.s.ResolveMissionModelSelection(q.ProfileID); err != nil {
		return zero, err
	}
	e.mu.Lock()
	closed := e.closed
	busySource := e.active != nil && e.active.id == sourceMissionID
	e.mu.Unlock()
	if closed {
		return zero, errors.New("engine closed")
	}
	if busySource {
		return zero, errors.New("source mission has an active operation")
	}
	src, candidateFiles, err := e.ideaFiles(sourceMissionID)
	if err != nil {
		return zero, err
	}
	switch src.State {
	case "planning", "executing", "verifying", "repairing", "pausing":
		return zero, errors.New("source mission has an active operation")
	}
	catalog, err := e.s.Catalog()
	if err != nil {
		return zero, err
	}
	projectID := "idea-" + src.ID
	for _, link := range catalog.MissionLinks {
		if link.MissionID == src.ID && link.ProjectID != "" {
			projectID = link.ProjectID
			break
		}
	}
	origin := &domain.IdeaProject{SchemaVersion: 2, Template: "browser_app_v1", Acceptance: append([]string{}, q.Acceptance...), ParentMissionID: src.ID}
	return e.createManagedIdeaMission(ctx, q, origin, candidateFiles, projectID)
}

func (e *Engine) createManagedIdeaMission(ctx context.Context, q IdeaCreateRequest, origin *domain.IdeaProject, srcOverrides map[string][]byte, targetProjectID string) (domain.Mission, error) {
	zero := domain.Mission{}
	if err := validateIdeaRequest(q); err != nil {
		return zero, err
	}
	if ok, _ := e.IdeaAvailable(); !ok {
		return zero, ErrC4Unavailable
	}
	// Reject invalid selections before leaving a persistent seed behind.
	if _, _, _, _, err := e.s.ResolveMissionModelSelection(q.ProfileID); err != nil {
		return zero, err
	}
	catalog, err := e.s.Catalog()
	if err != nil {
		return zero, err
	}
	targetExists := false
	if targetProjectID != "" {
		for _, p := range catalog.Projects {
			if p.ID == targetProjectID {
				targetExists = true
				break
			}
		}
	}
	if !targetExists && len(catalog.Projects) >= 50 {
		return zero, domain.ImportInvalidRequest
	}
	id, err := opaque()
	if err != nil {
		return zero, err
	}
	// Imports deliberately forbid nesting source and workspace data. This private
	// sibling is controller-owned and is never a caller-selected repository.
	projects := e.c4Root + "-projects"
	if err = platformfs.PrivateDirectory(projects); err != nil {
		return zero, err
	}
	canonical, err := filepath.EvalSymlinks(projects)
	if err != nil || canonical != projects {
		return zero, domain.ImportLocation
	}
	repo := filepath.Join(projects, id)
	if err = os.Mkdir(repo, 0700); err != nil {
		return zero, err
	}
	keep := false
	inspectionID := ""
	defer func() {
		if !keep && inspectionID != "" {
			_, opErr := e.s.GetRepositoryImportByInspection(inspectionID)
			keep = !errors.Is(opErr, store.ErrNotFound)
		}
		if !keep {
			_ = os.RemoveAll(repo)
		}
	}()
	files := browserAppSeed(origin)
	if len(srcOverrides) > 0 {
		for k := range files {
			if strings.HasPrefix(k, "src/") {
				delete(files, k)
			}
		}
		for k, b := range srcOverrides {
			if strings.HasPrefix(k, "src/") {
				files[k] = string(b)
			}
		}
	}
	for p, b := range files {
		name := filepath.Join(repo, p)
		if err = os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			return zero, err
		}
		if err = os.WriteFile(name, []byte(b), 0644); err != nil {
			return zero, err
		}
	}
	if err = workspace.InitializeManagedAppRepository(ctx, repo); err != nil {
		return zero, domain.ImportPrepareFailed
	}

	var target CodingProfileTemplateV2
	for _, p := range e.CodingProfilesV2() {
		if p.ProfileID == "c4_node_model_v2" {
			target = p
		}
	}
	if target.EmbeddedVM == nil {
		return zero, ErrC4Unavailable
	}
	input := filepath.Join(repo, ".empty-dependencies")
	if err = os.Mkdir(input, 0700); err != nil {
		return zero, err
	}
	deps, err := e.ImportCodingDependencies(ctx, CodingDependencyImportRequest{RepoPath: repo, ProfileID: target.ProfileID, EmbeddedVM: *target.EmbeddedVM, RuntimeInventoryDigest: target.RuntimeInventoryDigest, InputDirectory: input, Consent: true})
	if err != nil {
		return zero, err
	}
	if err = os.Remove(input); err != nil {
		return zero, err
	}
	spec := domain.RepositoryImportSpec{Scope: []string{"src/", "APP_CONTRACT.md", "idea-project.json"}, Protected: []string{"APP_CONTRACT.md", "idea-project.json"}, Acceptance: []string{target.Verifier}, VerificationV2: &domain.C4ImportSelectionV2{SchemaVersion: 2, ProfileID: target.ProfileID, EmbeddedVM: *target.EmbeddedVM, RuntimeInventoryDigest: target.RuntimeInventoryDigest, AdmissionDigest: deps.AdmissionDigest, Dependencies: deps.Dependencies, SourceRoot: ".", TestRoot: "tests", TestSupportPrefixes: []string{}}}
	inspection, err := e.InspectRepository(ctx, domain.RepositoryInspectionRequest{RepoPath: repo, Spec: spec, Consent: true})
	if err != nil {
		return zero, err
	}
	inspectionID = inspection.ID
	if inspection.Status != "ready" {
		return zero, domain.ImportInputs
	}
	m, err := e.Create(ctx, CreateRequest{ideaProject: origin, ImportVersion: 1, ImportSelection: &domain.ImportSelection{InspectionID: inspection.ID, Digest: inspection.Digest}, CodingVersion: 1, WorkspaceMode: "generations_v1", Title: q.Title, Goal: q.Goal, RepoPath: repo, Spec: domain.Spec{Scope: spec.Scope, Protected: spec.Protected, Acceptance: spec.Acceptance, ProfileID: q.ProfileID, MaxCalls: q.MaxCalls, Research: true, C4: &domain.C4Profile{SchemaVersion: 2, ID: target.ProfileID, Mode: "embedded_vm"}}})
	if err != nil {
		return zero, err
	}
	keep = true
	if err = e.ensureIdeaCatalogProject(m, targetProjectID); err != nil {
		return m, err
	}
	return m, nil
}

func browserAppSeed(origin *domain.IdeaProject) map[string]string {
	raw, _ := json.MarshalIndent(origin, "", "  ")
	return map[string]string{
		"idea-project.json": string(raw) + "\n",
		"package.json":      `{"name":"foundry-browser-app","version":"1.0.0"}` + "\n",
		"package-lock.json": `{"name":"foundry-browser-app","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"foundry-browser-app","version":"1.0.0"}}}` + "\n",
		"APP_CONTRACT.md": `# Managed browser application contract
Open src/index.html directly in a browser. Build the requested application in src/index.html, src/style.css and src/app.js. Additional local .html/.css/.js text assets under src/ are supported. src/checks.js is the mutable candidate-authored requirement test module, loaded by the fixed Node harness only; do not include it in index.html.
- Keep a complete HTML document starting with <!doctype html><html ...><head> (only optional lang/dir attributes on <html>), </head>, </body>, a <title>, and a <main> element.
- Use classic scripts (optional type="text/javascript" and defer) and relative local stylesheet/script URLs under src/. Inline SVG and data:image assets are supported; no external URLs, ES module imports/exports, iframe/object/base tags, CSS url()/@import/backslash escapes, npm dependencies, servers, or build steps.
- Do not put action/formaction on <form> or <button>; handle form submission with addEventListener('submit', event => { event.preventDefault(); ... }). Use <a href="#..."> only for in-page anchors.
- Persist local state with localStorage (the isolated preview provides a built-in in-memory fallback when opaque-origin sandboxing restricts browser storage). For data tools, prefer offering local JSON/CSV export and import.
- Keep DOM element IDs and classes referenced in src/app.js synchronized with src/index.html; the protected Node harness validates preview-bundle rules, classic syntax, and mounts scripts against src/index.html in a DOM smoke context before running src/checks.js.
- Do not modify tests/, package.json, package-lock.json, this contract, or idea-project.json. Implement business logic as browser-compatible CommonJS exports in src/app.js guarded by typeof module !== "undefined", and guard DOM initialization with typeof document !== "undefined". Replace the baseline smoke test in src/checks.js with node:test and node:assert/strict assertions covering every user acceptance requirement and edge case. QA separately reviews every user requirement against both source implementation and src/checks.js.
`,
		"src/index.html": `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>My application</title><link rel="stylesheet" href="style.css"></head><body><main><h1>My application</h1><p id="status">Your workspace is ready.</p><button id="action" type="button">Try it</button></main><script src="app.js"></script></body></html>`,
		"src/style.css":  `body{font:18px system-ui,sans-serif;margin:0;background:#f4f5f7;color:#17202a}main{max-width:800px;margin:8vh auto;padding:32px}button{font:inherit;padding:10px 20px;cursor:pointer}`,
		"src/app.js":     `function createCounter(){let value=0;return {get value(){return value;},increment(){return ++value;},reset(){value=0;return value;}};}if(typeof module!=="undefined")module.exports={createCounter};if(typeof document!=="undefined"){document.getElementById('action').addEventListener('click',()=>{document.getElementById('status').textContent='Ready to build your idea.';});}`,
		"src/checks.js": `// Candidate-authored baseline smoke; replace with assertions for user requirements.
const {test}=require('node:test');const assert=require('node:assert/strict');const {createCounter}=require('./app.js');test('candidate-authored baseline smoke',()=>{assert.equal(typeof createCounter,'function');});`,
		"tests/app.test.cjs": `const {test}=require('node:test');const assert=require('node:assert/strict');const fs=require('node:fs');const path=require('node:path');const vm=require('node:vm');const root=path.join(__dirname,'..');require('../src/checks.js');test('managed document and classic source contract',()=>{const srcRoot=path.join(root,'src');function visit(dir){for(const item of fs.readdirSync(dir,{withFileTypes:true})){const p=path.join(dir,item.name);if(item.isDirectory())visit(p);else{assert.ok(/\.(html|css|js)$/.test(item.name),'unsupported application asset: '+p);const source=fs.readFileSync(p,'utf8');if(p.endsWith('.js')){new vm.Script(source,{filename:p});assert.ok(!/<\/script/i.test(source),'script file cannot contain </script: '+p);}if(p.endsWith('.css')){assert.ok(!/(?:url\s*\(|@import|\\)/i.test(source),'CSS url(), @import, or backslash escape unsupported: '+p);assert.ok(!/<\/style/i.test(source),'CSS file cannot contain </style: '+p);}}}}visit(srcRoot);const html=fs.readFileSync(path.join(srcRoot,'index.html'),'utf8');const headMatch=/^\s*(?:<!doctype\s+html\s*>\s*)?<html(?:\s+(?:lang|dir)\s*=\s*(?:"[a-zA-Z-]+"|'[a-zA-Z-]+'))*\s*>\s*<head\s*>/i.exec(html);assert.ok(headMatch,'src/index.html must start with valid html/head structure');assert.ok(/<\/head>/i.test(html)&&/<\/body>/i.test(html),'src/index.html missing </head> or </body>');for(const tag of ['title','main'])assert.match(html,new RegExp('<'+tag+'(?:\\s|>)','i'));function unesc(v){return String(v).replace(/&quot;/g,'"').replace(/&#39;|&apos;/g,"'").replace(/&lt;/g,'<').replace(/&gt;/g,'>').replace(/&amp;/g,'&');}function parseTag(tag){const s=tag.slice(1,-1).trim();if(s.startsWith('/'))return{name:s.slice(1).trim().toLowerCase(),attrs:{},closing:true};let i=0;while(i<s.length&&!/[\s/]/.test(s[i]))i++;const name=s.slice(0,i).toLowerCase(),attrs={};while(i<s.length){while(i<s.length&&/[\s/]/.test(s[i]))i++;if(i===s.length)break;const kStart=i;while(i<s.length&&!/[\s=/]/.test(s[i]))i++;assert.ok(i>kStart,'malformed HTML tag attribute');const key=s.slice(kStart,i).toLowerCase();assert.ok(!Object.prototype.hasOwnProperty.call(attrs,key),'duplicate HTML attribute: '+key);while(i<s.length&&/\s/.test(s[i]))i++;let val='';if(i<s.length&&s[i]==='='){i++;while(i<s.length&&/\s/.test(s[i]))i++;assert.ok(i<s.length,'missing attribute value');if(s[i]==='"'||s[i]==="'"){const q=s[i++];const vStart=i;while(i<s.length&&s[i]!==q)i++;assert.ok(i<s.length,'unclosed attribute quote');val=s.slice(vStart,i++);}else{const vStart=i;while(i<s.length&&!/\s/.test(s[i]))i++;val=s.slice(vStart,i);}}attrs[key]=unesc(val);}return{name,attrs,closing:false};}function resolveSrcAsset(ref,ext){assert.ok(ref&&!/[\\?#:\0]/.test(ref)&&!ref.startsWith('/')&&ref!=='..'&&!ref.startsWith('../')&&!ref.includes('/../'),'external or invalid asset ref: '+ref);const clean=path.posix.normalize(path.posix.join('src',ref));assert.ok(clean.startsWith('src/')&&path.posix.extname(clean)===ext,'invalid asset path: '+ref);const disk=path.join(root,clean);assert.ok(fs.existsSync(disk),'missing referenced asset: '+clean);return fs.readFileSync(disk,'utf8');}const safeDataImg=/^data:image\/(?:png|jpeg|gif|webp|svg\+xml)(?:;[a-z0-9=+-]+)*,/i,extCSS=/(?:url\s*\(|@import|\\)/i,htmlLower=html.toLowerCase();const tagRe=/<!--[\s\S]*?-->|<!doctype\s+html\s*>|<\/?[a-z][a-z0-9:-]*(?:\s+[^<>]*?)?\s*\/?>/gi;const ids=new Map(),classes=new Set(),seenTags=new Set(['html','head','body','main']),eagerScripts=[],deferredScripts=[];let cursor=headMatch[0].length,m;tagRe.lastIndex=cursor;while((m=tagRe.exec(html))!==null){if(m.index<cursor)continue;const raw=m[0],posEnd=m.index+raw.length;if(raw.startsWith('<!'))continue;const t=parseTag(raw);if(t.closing)continue;seenTags.add(t.name);assert.ok(!['iframe','frame','frameset','object','embed','base','math'].includes(t.name),'disallowed element: <'+t.name+'>');assert.ok(!(t.name==='meta'&&String(t.attrs['http-equiv']||'').toLowerCase()==='refresh'),'meta refresh unsupported');for(const [k,v] of Object.entries(t.attrs)){assert.ok(!['srcset','poster','background','data','action','formaction','srcdoc','ping'].includes(k),'disallowed attribute '+k+' on <'+t.name+'>');if(k==='href'||k==='xlink:href')assert.ok(t.name==='link'||v.startsWith('#')||(t.name==='image'&&safeDataImg.test(v)),'external href unsupported on <'+t.name+'>');if(k==='src')assert.ok(t.name==='script'||(t.name==='img'&&safeDataImg.test(v)),'external src unsupported on <'+t.name+'>');}if(Object.prototype.hasOwnProperty.call(t.attrs,'style'))assert.ok(!extCSS.test(t.attrs.style),'inline CSS url/@import/escape unsupported');if(t.attrs.id)ids.set(t.attrs.id,t.name);if(t.attrs.class)for(const c of String(t.attrs.class).split(/\s+/))if(c)classes.add(c);if(t.name==='style'){const end=htmlLower.indexOf('</style>',posEnd);assert.ok(end>=0&&!extCSS.test(html.slice(posEnd,end)),'invalid <style> block');}if(t.name==='link'){assert.ok(String(t.attrs.rel||'').toLowerCase()==='stylesheet'&&String(t.attrs.href||'').trim()!=='','<link> must be rel="stylesheet"');for(const [k,v] of Object.entries(t.attrs)){if(['rel','href','id','media','title'].includes(k))continue;if(k==='type'){assert.ok(!v||v.toLowerCase()==='text/css','disallowed link type');continue;}assert.fail('disallowed <link> attribute: '+k);}resolveSrcAsset(t.attrs.href,'.css');}if(t.name==='script'){const st=t.attrs.type||'';assert.ok(!st||st==='text/javascript','non-classic script unsupported: '+st);if(Object.prototype.hasOwnProperty.call(t.attrs,'src')){for(const k of Object.keys(t.attrs))assert.ok(['src','type','defer'].includes(k),'disallowed <script src> attribute: '+k);const end=htmlLower.indexOf('</script>',posEnd);assert.ok(end>=0&&html.slice(posEnd,end).trim()==='','<script src> cannot contain inline body');const body=resolveSrcAsset(t.attrs.src,'.js');if(Object.prototype.hasOwnProperty.call(t.attrs,'defer'))deferredScripts.push({file:t.attrs.src,code:body});else eagerScripts.push({file:t.attrs.src,code:body});cursor=end+9;tagRe.lastIndex=cursor;}else{const end=htmlLower.indexOf('</script>',posEnd);assert.ok(end>=0,'unclosed inline <script>');eagerScripts.push({file:'inline-script',code:html.slice(posEnd,end)});cursor=end+9;tagRe.lastIndex=cursor;}}}const elMap=new Map(),classEls=new Map(),tagEls=new Map(),listeners={doc:new Map(),win:new Map()};function registerMarkup(fragment){if(typeof fragment!=='string')return;const idRe=/\sid\s*=\s*(?:"([^"]+)"|'([^']+)')/gi;let rm;while((rm=idRe.exec(fragment))!==null){const id=rm[1]||rm[2];if(id&&!elMap.has(id))elMap.set(id,makeEl('div',id));}const clsRe=/\sclass\s*=\s*(?:"([^"]+)"|'([^']+)')/gi;while((rm=clsRe.exec(fragment))!==null){for(const c of String(rm[1]||rm[2]).split(/\s+/))if(c&&!classEls.has(c))classEls.set(c,makeEl('div',''));}}function makeEl(tag,id){const attrs=new Map(),ev=new Map(),cls=new Set();let _html='',_text='',_id=id||'';const el={nodeType:1,tagName:String(tag||'div').toUpperCase(),style:{setProperty(){},removeProperty(){},getPropertyValue(){return '';}},dataset:{},value:'',checked:false,disabled:false,hidden:false,tabIndex:0,children:[],childNodes:[],parentNode:null,classList:{add(...xs){for(const x of xs)cls.add(x);},remove(...xs){for(const x of xs)cls.delete(x);},toggle(x,f){if(f===undefined){if(cls.has(x)){cls.delete(x);return false;}cls.add(x);return true;}if(f)cls.add(x);else cls.delete(x);return !!f;},contains(x){return cls.has(x);},replace(a,b){cls.delete(a);cls.add(b);}},get id(){return _id;},set id(v){_id=String(v||'');if(_id)elMap.set(_id,el);},get className(){return[...cls].join(' ');},set className(v){cls.clear();for(const c of String(v||'').split(/\s+/))if(c)cls.add(c);},get textContent(){return _text;},set textContent(v){_text=v==null?'':String(v);},get innerText(){return _text;},set innerText(v){_text=v==null?'':String(v);},get innerHTML(){return _html;},set innerHTML(v){_html=v==null?'':String(v);registerMarkup(_html);},setAttribute(k,v){const key=String(k).toLowerCase();attrs.set(key,String(v));if(key==='id')el.id=String(v);if(key==='class')el.className=String(v);if(key==='disabled')el.disabled=true;if(key==='hidden')el.hidden=true;},getAttribute(k){return attrs.has(String(k).toLowerCase())?attrs.get(String(k).toLowerCase()):null;},removeAttribute(k){const key=String(k).toLowerCase();attrs.delete(key);if(key==='disabled')el.disabled=false;if(key==='hidden')el.hidden=false;},hasAttribute(k){return attrs.has(String(k).toLowerCase());},append(...nodes){for(const n of nodes)if(n&&typeof n==='object'){el.children.push(n);el.childNodes.push(n);n.parentNode=el;}},appendChild(n){el.append(n);return n;},prepend(...nodes){for(const n of nodes.reverse())if(n&&typeof n==='object'){el.children.unshift(n);el.childNodes.unshift(n);n.parentNode=el;}},removeChild(n){el.children=el.children.filter(c=>c!==n);el.childNodes=el.childNodes.filter(c=>c!==n);return n;},replaceChildren(...nodes){el.children=[];el.childNodes=[];el.append(...nodes);},remove(){if(el.parentNode&&el.parentNode.removeChild)el.parentNode.removeChild(el);},insertAdjacentHTML(_p,s){registerMarkup(s);},cloneNode(){return makeEl(tag,'');},contains(){return false;},closest(){return null;},matches(){return false;},querySelector(s){return doc.querySelector(s);},querySelectorAll(s){return doc.querySelectorAll(s);},getElementsByTagName(t){return doc.getElementsByTagName(t);},getElementsByClassName(c){return doc.getElementsByClassName(c);},getBoundingClientRect(){return{top:0,left:0,right:100,bottom:30,width:100,height:30};},focus(){},blur(){},click(){},reset(){},select(){},scrollIntoView(){},addEventListener(type,fn){if(!ev.has(type))ev.set(type,[]);ev.get(type).push(fn);},removeEventListener(type,fn){if(ev.has(type))ev.set(type,ev.get(type).filter(f=>f!==fn));},dispatchEvent(e){for(const fn of ev.get(e&&e.type)||[])if(typeof fn==='function')fn.call(el,e);return true;}};return el;}for(const [id,tag] of ids.entries())elMap.set(id,makeEl(tag,id));for(const c of classes)classEls.set(c,makeEl('div',''));for(const t of seenTags)tagEls.set(t,makeEl(t,''));function mkStore(){const m=new Map();return{get length(){return m.size;},key(i){return[...m.keys()][i]??null;},getItem(k){return m.has(String(k))?m.get(String(k)):null;},setItem(k,v){m.set(String(k),String(v));},removeItem(k){m.delete(String(k));},clear(){m.clear();}};}const doc={readyState:'loading',documentElement:tagEls.get('html'),head:tagEls.get('head'),body:tagEls.get('body'),title:'',activeElement:null,cookie:'',getElementById(id){return elMap.get(String(id))||null;},createElement(tag){return makeEl(tag,'');},createTextNode(t){return{nodeType:3,textContent:String(t||'')};},createDocumentFragment(){return makeEl('#fragment','');},querySelector(sel){const s=String(sel||'').trim();const idM=/^#([A-Za-z_][\w-]*)$/.exec(s);if(idM)return elMap.get(idM[1])||null;const tagM=/^[a-z][a-z0-9-]*$/i.exec(s);if(tagM)return tagEls.get(s.toLowerCase())||null;const clsM=/^\.([A-Za-z_][\w-]*)$/.exec(s);if(clsM)return classEls.get(clsM[1])||null;return null;},querySelectorAll(){return[];},getElementsByTagName(t){const el=tagEls.get(String(t||'').toLowerCase());return el?[el]:[];},getElementsByClassName(c){const el=classEls.get(String(c||''));return el?[el]:[];},addEventListener(type,fn){if(!listeners.doc.has(type))listeners.doc.set(type,[]);listeners.doc.get(type).push(fn);},removeEventListener(type,fn){if(listeners.doc.has(type))listeners.doc.set(type,listeners.doc.get(type).filter(f=>f!==fn));},dispatchEvent(){return true;}};let nextTimer=1;const ctxObj={document:doc,localStorage:mkStore(),sessionStorage:mkStore(),navigator:{userAgent:'foundry-smoke',language:'en-US'},location:{href:'about:blank',origin:'null',protocol:'about:',hash:''},history:{pushState(){},replaceState(){}},console,Math,Date,JSON,Intl,Array,Object,String,Number,Boolean,RegExp,Error,TypeError,RangeError,Map,Set,Promise,URL,URLSearchParams,Blob:typeof Blob!=='undefined'?Blob:function(){},crypto:globalThis.crypto,Event:function(type){this.type=type;this.preventDefault=()=>{};this.stopPropagation=()=>{};},CustomEvent:function(type,init){this.type=type;this.detail=init&&init.detail;this.preventDefault=()=>{};this.stopPropagation=()=>{};},FormData:function(){this.entries=()=>[];this.get=()=>null;},matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),setTimeout:()=>nextTimer++,clearTimeout(){},setInterval:()=>nextTimer++,clearInterval(){},requestAnimationFrame:()=>nextTimer++,cancelAnimationFrame(){},alert(){},confirm(){return true;},prompt(){return null;},addEventListener(type,fn){if(!listeners.win.has(type))listeners.win.set(type,[]);listeners.win.get(type).push(fn);},removeEventListener(type,fn){if(listeners.win.has(type))listeners.win.set(type,listeners.win.get(type).filter(f=>f!==fn));}};ctxObj.window=ctxObj;ctxObj.self=ctxObj;ctxObj.globalThis=ctxObj;const context=vm.createContext(ctxObj);for(const s of [...eagerScripts,...deferredScripts])vm.runInContext(s.code,context,{filename:s.file});doc.readyState='complete';for(const fn of listeners.doc.get('DOMContentLoaded')||[])if(typeof fn==='function')fn({type:'DOMContentLoaded',target:doc,preventDefault(){}});for(const fn of listeners.win.get('DOMContentLoaded')||[])if(typeof fn==='function')fn({type:'DOMContentLoaded',target:doc,preventDefault(){}});for(const fn of listeners.win.get('load')||[])if(typeof fn==='function')fn({type:'load',target:ctxObj,preventDefault(){}});});`,
	}
}

func (e *Engine) validateIdeaOrigin(m domain.Mission) error {
	if m.IdeaProject == nil {
		return domain.ImportInvalidBinding
	}
	if (m.IdeaProject.SchemaVersion != 1 && m.IdeaProject.SchemaVersion != 2) || m.IdeaProject.Template != "browser_app_v1" || m.RepositoryImport == nil || m.Spec.C4 == nil || m.Spec.C4.ID != "c4_node_model_v2" {
		return domain.ImportInvalidBinding
	}
	if m.IdeaProject.SchemaVersion == 1 && m.IdeaProject.ParentMissionID != "" {
		return domain.ImportInvalidBinding
	}
	if m.IdeaProject.ParentMissionID != "" && !ideaParentIDPattern.MatchString(m.IdeaProject.ParentMissionID) {
		return domain.ImportInvalidBinding
	}
	if err := e.ValidateRepositoryImport(m); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Join(e.dir(m.ID), "base"))
	if err != nil {
		return domain.ImportInvalidBinding
	}
	defer root.Close()
	f, err := root.Open("idea-project.json")
	if err != nil {
		return domain.ImportInvalidBinding
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 {
		return domain.ImportInvalidBinding
	}
	var frozen domain.IdeaProject
	if json.Unmarshal(raw, &frozen) != nil {
		return domain.ImportInvalidBinding
	}
	a, _ := json.Marshal(frozen)
	b, _ := json.Marshal(m.IdeaProject)
	if string(a) != string(b) {
		return domain.ImportInvalidBinding
	}
	return nil
}

// Deterministic project identity lets recovery finish a catalog link interrupted
// after mission commit without duplicating projects or re-running admission.
func (e *Engine) ensureIdeaCatalog(m domain.Mission) error {
	return e.ensureIdeaCatalogProject(m, "")
}

func (e *Engine) ensureIdeaCatalogProject(m domain.Mission, projectID string) error {
	if m.IdeaProject == nil {
		return nil
	}
	name := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, m.Title)
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	description := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return ' '
		}
		return r
	}, m.Goal)
	if r := []rune(description); len(r) > 2000 {
		description = string(r[:2000])
	}
	for attempt := 0; attempt < 4; attempt++ {
		c, err := e.s.Catalog()
		if err != nil {
			return err
		}
		resolvedID := projectID
		if resolvedID == "" && m.IdeaProject.ParentMissionID != "" {
			for _, link := range c.MissionLinks {
				if link.MissionID == m.IdeaProject.ParentMissionID && link.ProjectID != "" {
					resolvedID = link.ProjectID
					break
				}
			}
			if resolvedID == "" {
				resolvedID = "idea-" + m.IdeaProject.ParentMissionID
			}
		}
		if resolvedID == "" {
			resolvedID = "idea-" + m.ID
		}
		exists := false
		for _, p := range c.Projects {
			if p.ID == resolvedID {
				exists = true
			}
		}
		if !exists {
			_, err = e.s.UpdateCatalog(c.Revision, "project.create", func(c *domain.CatalogSnapshot) ([]string, error) {
				c.Projects = append(c.Projects, domain.Project{CatalogMeta: domain.CatalogMeta{ID: resolvedID}, Name: name, Description: description})
				return []string{resolvedID}, nil
			})
			if errors.Is(err, store.ErrConflict) {
				continue
			}
			if err != nil {
				return err
			}
			continue
		}
		for _, link := range c.MissionLinks {
			if link.MissionID == m.ID {
				return nil
			}
		}
		_, err = e.s.UpdateCatalog(c.Revision, "mission.link", func(c *domain.CatalogSnapshot) ([]string, error) {
			c.MissionLinks = append(c.MissionLinks, domain.MissionProjectLink{MissionID: m.ID, ProjectID: resolvedID})
			return []string{m.ID}, nil
		})
		if errors.Is(err, store.ErrConflict) {
			continue
		}
		return err
	}
	return store.ErrConflict
}
