"""Trusted synthetic data only: no archived program is executed."""
import importlib.util
import io
import json
import gzip
import hashlib
import lzma
import os
from pathlib import Path
import struct
import sys
sys.dont_write_bytecode = True
import tarfile
import tempfile
import unittest

MODULE = Path(__file__).parents[1] / 'sandbox' / 'build-guest-v2.py'

def tar(rows):
    out = io.BytesIO()
    with tarfile.open(fileobj=out, mode='w', format=tarfile.USTAR_FORMAT) as t:
        for name, data, kind, mode in rows:
            x = tarfile.TarInfo(name); x.mode = mode
            if kind == 'link': x.type = tarfile.SYMTYPE; x.linkname = data; data = b''
            elif kind == 'hard': x.type = tarfile.LNKTYPE; x.linkname = data; data = b''
            elif kind == 'dir': x.type = tarfile.DIRTYPE; data = b''
            elif kind == 'fifo': x.type = tarfile.FIFOTYPE; data = b''
            x.size = len(data); t.addfile(x, io.BytesIO(data))
    return out.getvalue()

def ar(data):
    out = b'!<arch>\n'
    for name, value in [('debian-binary', b'2.0\n'), ('control.tar.xz', lzma.compress(tar([]))), ('data.tar.xz', lzma.compress(data))]:
        h = (name + '/').ljust(16) + '0'.ljust(12) + '0'.ljust(6) + '0'.ljust(6) + '100644'.ljust(8) + str(len(value)).ljust(10) + '`\n'
        out += h.encode() + value + (b'\n' if len(value) % 2 else b'')
    return out

def elf(arch='amd64', interp=None, needed=(), rpath=None):
    strings = b'\0'; offsets = []
    for s in [*needed, *([rpath] if rpath else [])]: offsets.append(len(strings)); strings += s.encode() + b'\0'
    tags = [(1, x) for x in offsets[:len(needed)]]
    if rpath: tags.append((29, offsets[-1]))
    tags += [(5, 0x1000 + 512), (10, len(strings)), (0, 0)]
    dyn = b''.join(struct.pack('<QQ', *x) for x in tags)
    b = bytearray(1024); b[:16] = b'\x7fELF\x02\x01\x01' + b'\0' * 9
    struct.pack_into('<HHIQQQIHHHHHH', b, 16, 2, 62 if arch == 'amd64' else 183, 1, 0, 64, 0, 0, 64, 56, 2 + bool(interp), 0, 0, 0)
    struct.pack_into('<IIQQQQQQ', b, 64, 1, 5, 0, 0x1000, 0, len(b), len(b), 4096)
    struct.pack_into('<IIQQQQQQ', b, 120, 2, 4, 768, 0, 0, len(dyn), len(dyn), 8)
    b[512:512+len(strings)] = strings; b[768:768+len(dyn)] = dyn
    if interp:
        value = interp.encode() + b'\0'; b[256:256+len(value)] = value
        struct.pack_into('<IIQQQQQQ', b, 176, 3, 4, 256, 0, 0, len(value), len(value), 1)
    return bytes(b)

def host_alias(link,target):
    if os.name=='nt':
        location=Path(__file__).with_name('build_guest_v2_host_test.py')
        spec=importlib.util.spec_from_file_location('owned_host_fixtures',location)
        module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
        # A junction exercises the same reparse-point rejection without the
        # administrator/developer-mode privilege required by file symlinks.
        module.make_junction(module.load_helper(),link,target if target.is_dir() else target.parent)
    else:link.symlink_to(target,target_is_directory=target.is_dir())

def remove_alias(link):
    if os.name=='nt':link.rmdir()
    else:link.unlink()

class BuilderTests(unittest.TestCase):
    def test_offline_decoder_returns_normalized_data_without_extraction(self):
        self.assertTrue(MODULE.exists(), 'offline v2 decoder is not implemented')
        spec = importlib.util.spec_from_file_location('builder', MODULE)
        m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
        entries = m.read_tar(tar([('./usr/bin/tool', b'hello', 'file', 0o755)]))
        self.assertEqual(entries['usr/bin/tool'], {'type': 'file', 'mode': '0555', 'data': b'hello'})


# These cases catch relaxed decoder bounds, ignored lock identities, unsafe loader
# resolution, and accidental image publication on a failed build.
class BoundaryTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec = importlib.util.spec_from_file_location('v2', MODULE)
        cls.m = importlib.util.module_from_spec(spec); spec.loader.exec_module(cls.m)

    def test_compressed_and_ar_data_are_bounded_and_complete(self):
        data = tar([('usr/x', b'x', 'file', 0o644)])
        self.assertEqual(self.m.decode(lzma.compress(data), 'xz'), data)
        self.assertEqual(self.m.decode(gzip.compress(data), 'gz'), data)
        self.assertEqual(self.m.read_deb(ar(data)), lzma.compress(data))
        for value in [ar(data)[:-1], ar(data) + b'x', b'!<arch>\n', b'bad']:
            with self.subTest(value=value[:8]), self.assertRaises(ValueError): self.m.read_deb(value)
        for codec, value in [('xz', lzma.compress(data)), ('gz', gzip.compress(data))]:
            for broken in [value[:-1], value + b'x', value + value]:
                with self.assertRaises(ValueError): self.m.decode(broken, codec)
            with self.assertRaises(ValueError): self.m.decode(value, codec, limit=10)

    def test_tar_rejects_unsafe_types_paths_metadata_and_bounds(self):
        cases = [ [('a', b'a', 'file', 0o4755)], [('a', b'', 'fifo', 0o644)],
                  [('../x', b'a', 'file', 0o644)], [('/x', b'a', 'file', 0o644)],
                  [('A/x', b'a', 'file', 0o644), ('a/y', b'b', 'file', 0o644)],
                  [('x', b'a', 'file', 0o644), ('x', b'a', 'file', 0o644)],
                  [('x', b'a', 'file', 0o644), ('x/y', b'b', 'file', 0o644)],
                  [('x', 'y', 'hard', 0o644), ('y', 'x', 'hard', 0o644)],
                  [('x', '../y', 'hard', 0o644)], [('x', 'missing', 'hard', 0o644)]]
        for rows in cases:
            with self.subTest(rows=rows), self.assertRaises(ValueError): self.m.read_tar(tar(rows))
        data = tar([('x', b'data', 'file', 0o644)])
        for broken in [data[:-1], data[:1024], data + bytes(511) + b'x', b'!' + data[1:]]:
            with self.assertRaises(ValueError): self.m.read_tar(broken)
        for typ in [tarfile.XHDTYPE, tarfile.GNUTYPE_SPARSE, tarfile.CHRTYPE]:
            out = io.BytesIO()
            with tarfile.open(fileobj=out, mode='w') as t:
                x = tarfile.TarInfo('bad'); x.type = typ; t.addfile(x)
            with self.assertRaises(ValueError): self.m.read_tar(out.getvalue())
        old = self.m.MAX_MEMBERS
        try:
            self.m.MAX_MEMBERS = 0
            with self.assertRaises(ValueError): self.m.read_tar(data)
        finally: self.m.MAX_MEMBERS = old

    def test_gnu_longname_is_bounded_path_only_metadata(self):
        out = io.BytesIO()
        with tarfile.open(fileobj=out, mode='w', format=tarfile.GNU_FORMAT) as t:
            x = tarfile.TarInfo('a/'+'b'*150); x.size = 1
            t.addfile(x, io.BytesIO(b'x'))
        self.assertEqual(self.m.read_tar(out.getvalue())['a/'+'b'*150]['data'], b'x')

    def test_hardlinks_materialize_and_virtual_links_check_every_component(self):
        entries = self.m.read_tar(tar([('usr/a', b'abc', 'file', 0o755), ('usr/b', './usr/a', 'hard', 0o644)]))
        self.assertEqual(entries['usr/b'], {'type': 'file', 'mode': '0555', 'data': b'abc'})
        entries['lib'] = {'type': 'symlink', 'mode': '0777', 'target': '/usr'}
        self.assertEqual(self.m.resolve(entries, 'lib/a'), 'usr/a')
        for links in [ {'x': '../out'}, {'x': 'y', 'y': 'x'}, {'x': '/missing'}, {'x': 'usr/a/child'} ]:
            bad = dict(entries)
            for n, t in links.items(): bad[n] = {'type': 'symlink', 'mode': '0777', 'target': t}
            with self.subTest(links=links), self.assertRaises(ValueError): self.m.check_links(bad)

    def test_virtual_links_expand_before_parent_components(self):
        entries = {n:{'type':'file','mode':'0444','data':b'x'} for n in ['usr/file','real/file','real/dir/child']}
        entries.update({'usr/link':{'type':'symlink','mode':'0777','target':'/real/dir'},'x':{'type':'symlink','mode':'0777','target':'/usr/link/../file'}})
        self.assertEqual(self.m.resolve(entries,'x'),'real/file')
        entries['x']['target']='/usr/file/../file'
        with self.assertRaises(ValueError):self.m.resolve(entries,'x')

    def test_elf_checks_arch_headers_imports_interpreter_and_runpath(self):
        m = self.m
        entries = {'usr/bin/tool': {'type': 'file', 'mode': '0555', 'data': elf(interp='/lib64/ld-linux-x86-64.so.2', needed=['libc.so.6'])},
                   'usr/lib/x86_64-linux-gnu/libc.so.6': {'type': 'file', 'mode': '0555', 'data': elf()},
                   'usr/lib64/ld-linux-x86-64.so.2': {'type': 'file', 'mode': '0555', 'data': elf()},
                   'lib64': {'type': 'symlink', 'mode': '0777', 'target': 'usr/lib64'}}
        policy = {'search_dirs': ['usr/lib/x86_64-linux-gnu'], 'origin_rpaths': {}}
        self.assertEqual(m.check_elf(entries, 'amd64', policy), {'objects': 3, 'imports': 1})
        with self.assertRaises(ValueError): m.elf_info(elf('arm64'), 'amd64')
        with self.assertRaises(ValueError): m.elf_info(elf()[:100], 'amd64')
        with self.assertRaises(ValueError): m.elf_info(elf(interp='/lib64/ld-linux-x86-64.so.2'), 'amd64', static=True)
        for broken in [elf(interp='/foreign'), elf(needed=['missing.so']), elf(rpath='/tmp'), elf(rpath='$ORIGIN')]:
            bad = dict(entries); bad['usr/bin/tool'] = {'type': 'file', 'mode': '0555', 'data': broken}
            with self.subTest(broken=hashlib.sha256(broken).hexdigest()), self.assertRaises(ValueError): m.check_elf(bad, 'amd64', policy)

    def fixture(self, root):
        runtime = ar(tar([('usr/bin/python3.13', elf(), 'file', 0o755), ('usr/lib/python3.13/unittest/__init__.py', b'# stdlib', 'file', 0o644), ('usr/share/doc/python/copyright', b'license', 'file', 0o644), ('usr/bin/python3','python3.13','link',0o777)]))
        node = lzma.compress(tar([('node-v/bin/node', elf(), 'file', 0o755), ('node-v/LICENSE', b'node license', 'file', 0o644), ('node-v/npm', b'NEVER INCLUDED', 'file', 0o755)]))
        ts = gzip.compress(tar([('package/lib/typescript.js', b'compiler', 'file', 0o644), ('package/package.json', b'{}', 'file', 0o644), ('package/LICENSE.txt', b'ts license', 'file', 0o644), ('package/ThirdPartyNoticeText.txt', b'notices', 'file', 0o644)]))
        artifacts = []
        for name, value, kind in [('py.deb', runtime, 'debian-runtime-package'), ('node.tar.xz', node, 'node-runtime'), ('ts.tgz', ts, 'typescript-compiler'), ('source.tar', b'NOT AN ARCHIVE, HASH ONLY', 'node-source')]:
            (root/name).write_bytes(value)
            artifacts.append({'archive_root':'node-v', 'file': name, 'size': len(value), 'sha256': hashlib.sha256(value).hexdigest(), 'kind': kind, 'target_architectures': ['amd64'], 'url': 'https://example.invalid/'+name})
        projected = {
          'usr/bin/python3.13': ('0555', elf()), 'usr/lib/python3.13/unittest/__init__.py': ('0444', b'# stdlib'), 'usr/share/doc/python/copyright': ('0444', b'license'),
          'opt/foundry/node/bin/node': ('0555', elf()), 'opt/foundry/node/LICENSE': ('0444', b'node license'),
          'opt/foundry/typescript/lib/typescript.js': ('0444', b'compiler'), 'opt/foundry/typescript/package.json': ('0444', b'{}'),
          'opt/foundry/typescript/LICENSE.txt': ('0444', b'ts license'), 'opt/foundry/typescript/ThirdPartyNoticeText.txt': ('0444', b'notices')}
        inventory = [{'path': n, 'type': 'file', 'mode': mode, 'size': len(data), 'sha256': hashlib.sha256(data).hexdigest()} for n, (mode, data) in sorted(projected.items())]
        inventory += [{'path':'lib','type':'symlink','mode':'0777','target':'usr/lib'}, {'path':'usr/bin/python3','type':'symlink','mode':'0777','target':'python3.13'}]
        inventory.sort(key=lambda x:x['path'])
        expected = {'sha256': hashlib.sha256(json.dumps(inventory, sort_keys=True, separators=(',', ':')).encode()).hexdigest(), 'entries': 11, 'regular_bytes': sum(len(x[1]) for x in projected.values())}
        return {'schema_version': 2, 'versions':{'python_command':'/usr/bin/python3.13','python_upstream_version':'3.13.5','python_distribution':'Debian trixie','python_package_version':'3.13.5-2+deb13u5','python_stdlib':'/usr/lib/python3.13','python_options':['-I','-S','-B'],'node_command':'/opt/foundry/node/bin/node','node_version':'24.22.1','typescript_command':['/opt/foundry/node/bin/node','/opt/foundry/typescript/lib/tsc.js'],'typescript_version':'5.9.3'}, 'artifacts': artifacts, 'architectures': {'amd64': {'expected': expected, 'links': {'lib':'usr/lib','usr/bin/python3':'python3.13'}, 'case_exceptions': [], 'archive_case_exceptions': [], 'elf': {'search_dirs': [], 'origin_rpaths': {}}, 'required': list(projected)}}, 'projection_policy': 'foundry-runtime-projection-v2'}

    def test_full_offline_build_is_deterministic_and_newc_independently_parses(self):
        m = self.m
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve(); lock = self.fixture(root)
            result = m.validate(lock, root, 'amd64', sources=True)
            self.assertEqual(result['runtime']['entries'], 11)
            self.assertEqual(result['sources_hashed'], 1)
            runners = {n: {'data': b'# owned '+n.encode(), 'sha256': hashlib.sha256(b'# owned '+n.encode()).hexdigest()} for n in m.RUNNERS}
            a, receipt = m.assemble(lock, root, 'amd64', elf(), runners)
            b, receipt2 = m.assemble(lock, root, 'amd64', elf(), runners)
            self.assertEqual(a, b); self.assertEqual(receipt, receipt2)
            raw = gzip.decompress(a); at = 0; parsed = {}
            while True:
                self.assertEqual(raw[at:at+6], b'070701')
                fields = [int(raw[at+6+i*8:at+14+i*8], 16) for i in range(13)]
                size, namesize = fields[6], fields[11]; start = at+110
                name = raw[start:start+namesize-1].decode(); at = (start+namesize+3)//4*4
                value = raw[at:at+size]; at = (at+size+3)//4*4
                self.assertEqual(fields[2:4], [0, 0]); self.assertEqual(fields[5], 0)
                if name == 'TRAILER!!!': break
                parsed[name] = (fields[1], value)
            self.assertEqual(len(parsed),40)
            self.assertEqual(parsed['lib'],(0o120777,b'usr/lib'))
            self.assertEqual(parsed['usr/bin/python3'],(0o120777,b'python3.13'))
            self.assertEqual(parsed['init'], (0o100555, elf()))
            self.assertEqual(parsed['opt/foundry/node/LICENSE'][1], b'node license')
            self.assertEqual(parsed['workspace'][0], 0o40555)
            self.assertNotIn('source.tar', parsed); self.assertNotIn('opt/foundry/node/npm', parsed)
            self.assertEqual(receipt['image']['sha256'], hashlib.sha256(a).hexdigest())
            for badguest, badrunners in [(elf('arm64'), runners), (elf(interp='/bad'), runners), (elf(), {}), (elf(), {**runners, 'etc/control': {'data': b'x', 'sha256': '0'*64}})]:
                with self.assertRaises(ValueError): m.assemble(lock, root, 'amd64', badguest, badrunners)

    def test_descriptor_binds_actual_selected_files_and_embeds_exact_canonical_bytes(self):
        m = self.m
        fixture = Path(__file__).parents[2]/'internal/vmruntime/testdata/runtime-inventory-v2.json'
        raw = fixture.read_bytes()
        self.assertEqual(m.canonical(json.loads(raw)), raw)
        self.assertEqual(m.inventory_digest(raw), fixture.with_suffix('.sha256').read_text().strip())
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp).resolve();lock=self.fixture(root)
            runners={n:{'data':b'owned '+n.encode(),'sha256':hashlib.sha256(b'owned '+n.encode()).hexdigest()} for n in m.RUNNERS}
            image,receipt=m.assemble(lock,root,'amd64',elf(),runners)
            descriptor=receipt['runtime_inventory']; encoded=m.canonical(descriptor)
            self.assertEqual(descriptor['projected_runtime'], receipt['runtime'])
            self.assertEqual(descriptor['supervisor'], {'path':'/init','size':1024,'sha256':hashlib.sha256(elf()).hexdigest()})
            self.assertEqual(descriptor['typescript']['compiler']['sha256'],hashlib.sha256(b'compiler').hexdigest())
            self.assertEqual(descriptor['python']['executable']['sha256'],hashlib.sha256(elf()).hexdigest())
            self.assertEqual(descriptor['runners'][1]['file']['sha256'],runners[m.RUNNERS[1]]['sha256'])
            self.assertIn(encoded,gzip.decompress(image))
            self.assertEqual(receipt['descriptor'],{'path':'runtime-inventory-v2.json','size':len(encoded),'sha256':hashlib.sha256(encoded).hexdigest(),'digest':m.inventory_digest(encoded)})
            self.assertNotIn('rootfs',descriptor);self.assertNotIn('manifest',descriptor)
            entries,_=m.project(lock,root,'amd64')
            for name,change in [('usr/bin/python3.13','missing'),('opt/foundry/node/bin/node','mode'),('opt/foundry/typescript/lib/typescript.js','type')]:
                bad=dict(entries)
                if change=='missing':del bad[name]
                else:bad[name]={**bad[name],change:'symlink' if change=='type' else '0444','target':'/usr/bin/python3.13'}
                with self.subTest(name=name),self.assertRaises(ValueError):m.runtime_descriptor(bad,'amd64',receipt['runtime'],elf(),runners)

    def test_runtime_notices_require_each_component_source_and_resolve_owned_copyright_links(self):
        entries={n:{'type':'file','mode':'0444','data':b'notice'} for n in ['usr/share/doc/shared/copyright','opt/foundry/node/LICENSE','opt/foundry/typescript/LICENSE.txt','opt/foundry/typescript/ThirdPartyNoticeText.txt']}
        entries['usr/share/doc/py/copyright']={'type':'symlink','mode':'0777','target':'../shared/copyright'}
        def row(kind,**kw):return {'kind':kind,'file':kind+'.data','size':1,'sha256':'1'*64,**kw}
        lock={'artifacts':[row('debian-runtime-package',package='py',source='python3.13',source_version='3.13.5',target_architectures=['amd64']),row('node-runtime',version='24.22.1',target_architectures=['amd64']),row('typescript-compiler',version='5.9.3',target_architectures=['amd64']),row('debian-corresponding-source',source='python3.13',source_version='3.13.5'),row('node-source',version='24.22.1'),row('typescript-source',version='5.9.3')]}
        notices,components=self.m.runtime_notices(entries,lock,'amd64')
        self.assertEqual(notices['debian/py/copyright'],b'notice');self.assertEqual(len(components),3)
        self.assertEqual(components[0]['sources'][0]['file'],'debian-corresponding-source.data')
        bad=json.loads(json.dumps(lock));bad['artifacts']=[r for r in bad['artifacts'] if r['kind']!='node-source']
        with self.assertRaisesRegex(ValueError,'source'):self.m.runtime_notices(entries,bad,'amd64')
        del entries['opt/foundry/typescript/ThirdPartyNoticeText.txt']
        with self.assertRaises(ValueError):self.m.runtime_notices(entries,lock,'amd64')

    def test_source_notice_export_reads_pinned_archive_license_bytes_without_extracting_programs(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp).resolve();raw=gzip.compress(tar([('source/LICENSE',b'license text','file',0o644),('source/COPYING3.LIB',b'LGPL text','file',0o644),('source/build/LICENSE',b'','file',0o644),('source/program',b'NEVER EXECUTE','file',0o755)]));(root/'source.tar.gz').write_bytes(raw)
            lock={'artifacts':[{'kind':'node-source','file':'source.tar.gz','size':len(raw),'sha256':hashlib.sha256(raw).hexdigest()}]}
            values,proof=self.m.source_notices(lock,root)
            self.assertEqual(values,{'source.tar.gz/source/LICENSE':b'license text','source.tar.gz/source/COPYING3.LIB':b'LGPL text'})
            self.assertEqual([(r['archive'],r['member']) for r in proof],[('source.tar.gz','source/COPYING3.LIB'),('source.tar.gz','source/LICENSE')])
            (root/'source.tar.gz').write_bytes(raw+b'!')
            with self.assertRaises(ValueError):self.m.source_notices(lock,root)

    def test_projection_rejects_control_roots_and_unlisted_links(self):
        m = self.m
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve(); lock = self.fixture(root)
            row = lock['artifacts'][0]
            extra = [('opt/foundry/runners/foreign.cjs', b'bad', 'file', 0o644)]
            data = ar(tar(extra)); (root/row['file']).write_bytes(data)
            row.update(size=len(data), sha256=hashlib.sha256(data).hexdigest())
            inv = [{'path':extra[0][0], 'type':'file','mode':'0444','size':3,'sha256':hashlib.sha256(b'bad').hexdigest()}]
            # Other two archives remain selected; use their known normalized rows.
            node_ts = [
              ('opt/foundry/node/bin/node','0555',elf()), ('opt/foundry/node/LICENSE','0444',b'node license'),
              ('opt/foundry/typescript/lib/typescript.js','0444',b'compiler'), ('opt/foundry/typescript/package.json','0444',b'{}'),
              ('opt/foundry/typescript/LICENSE.txt','0444',b'ts license'), ('opt/foundry/typescript/ThirdPartyNoticeText.txt','0444',b'notices')]
            inv += [{'path':n,'type':'file','mode':mode,'size':len(d),'sha256':hashlib.sha256(d).hexdigest()} for n,mode,d in node_ts]
            lock['architectures']['amd64']['required'] = []
            lock['architectures']['amd64']['links'] = {}
            lock['architectures']['amd64']['expected'] = {'sha256':hashlib.sha256(json.dumps(sorted(inv,key=lambda x:x['path']),sort_keys=True,separators=(',',':')).encode()).hexdigest(),'entries':7,'regular_bytes':sum(x['size'] for x in inv)}
            runners = {n:{'data':b'x','sha256':hashlib.sha256(b'x').hexdigest()} for n in m.RUNNERS}
            with self.assertRaisesRegex(ValueError,'overlap'): m.assemble(lock,root,'amd64',elf(),runners)

    def test_static_elf_rejects_dynamic_imports_and_origin_rule_is_gconv_only(self):
        m=self.m
        with self.assertRaises(ValueError): m.elf_info(elf(needed=['libc.so.6']),'amd64',static=True)
        entries={'usr/bin/tool':{'type':'file','mode':'0555','data':elf(rpath='$ORIGIN')}}
        with self.assertRaises(ValueError): m.check_elf(entries,'amd64',{'search_dirs':[],'origin_rpaths':{'usr/bin/tool':['$ORIGIN']}})

    def test_archive_mapping_and_codec_are_closed(self):
        with self.assertRaises(ValueError): self.m.decode(gzip.compress(b'abc'),'unknown')
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp).resolve();lock=self.fixture(root)
            lock['artifacts'][1]['archive_root']='foreign-root'
            with self.assertRaises(ValueError): self.m.validate(lock,root,'amd64')

    def test_source_hashes_are_optional_but_never_decoded_and_partial_publication_cleans_up(self):
        m=self.m
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp).resolve();lock=self.fixture(root)
            (root/'source.tar').write_bytes(b'bad source')
            self.assertEqual(m.validate(lock,root,'amd64')['sources_hashed'],0)
            with self.assertRaises(ValueError):m.validate(lock,root,'amd64',sources=True)
            out=root/'out';receipt=root/'receipt'
            with self.assertRaises(TypeError):m.publish(out,receipt,b'image',{'invalid':object()})
            self.assertFalse(out.exists());self.assertFalse(receipt.exists())

    def test_member_file_bound_and_elf_section_table_are_checked_before_retention(self):
        m=self.m;old=m.MAX_FILE
        try:
            m.MAX_FILE=2
            with self.assertRaises(ValueError):m.read_tar(tar([('x',b'123','file',0o644)]))
        finally:m.MAX_FILE=old
        for change in ['program','section','dynamic']:
            data=bytearray(elf())
            if change=='program':struct.pack_into('<Q',data,32,2**63)
            if change=='section':struct.pack_into('<Q',data,40,len(data)-1);struct.pack_into('<HH',data,58,64,1)
            if change=='dynamic':struct.pack_into('<Q',data,768+len([(5,0),(10,0)])*16,1)
            with self.subTest(change=change),self.assertRaises(ValueError):m.elf_info(bytes(data),'amd64')

    def test_inputs_lock_and_output_fail_closed(self):
        m = self.m
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve(); lock = self.fixture(root)
            for change in ['schema', 'arch', 'size', 'hash', 'missing', 'symlink', 'digest', 'unknown', 'filename', 'metadata']:
                bad = json.loads(json.dumps(lock)); arch = 'amd64'; path = root/'node.tar.xz'; saved = path.read_bytes()
                if change == 'metadata': bad['versions']['node_version']='24.0.0'
                if change == 'schema': bad['schema_version'] = 1
                if change == 'arch': arch = 'arm64'
                if change == 'size': bad['artifacts'][0]['size'] += 1
                if change == 'hash': bad['artifacts'][0]['sha256'] = '0'*64
                if change == 'digest': bad['architectures']['amd64']['expected']['sha256'] = '0'*64
                if change == 'filename': bad['artifacts'][0]['file'] = '../escape'
                if change == 'unknown': bad['artifacts'][0]['kind'] = 'untrusted-project'
                if change == 'missing': path.unlink()
                if change == 'symlink': path.unlink(); host_alias(path,root/'ts.tgz')
                with self.subTest(change=change), self.assertRaises(ValueError): m.validate(bad, root, arch)
                if change=='symlink':remove_alias(path)
                if change in ['missing','symlink']:path.write_bytes(saved)
            alias = root/'alias'; host_alias(alias,root)
            with self.assertRaises(ValueError): m.validate(lock, alias, 'amd64')
            remove_alias(alias)
            out = root/'image.gz'; receipt = root/'receipt.json'; out.write_bytes(b'KEEP')
            with self.assertRaises((ValueError, FileExistsError)): m.publish(out, receipt, b'NEW', {})
            self.assertEqual(out.read_bytes(), b'KEEP'); self.assertFalse(receipt.exists())
            out.unlink(); receipt.write_bytes(b'KEEP RECEIPT')
            with self.assertRaises((ValueError, FileExistsError)): m.publish(out, receipt, b'NEW', {})
            self.assertFalse(out.exists()); self.assertEqual(receipt.read_bytes(), b'KEEP RECEIPT')

if __name__ == '__main__': unittest.main()
