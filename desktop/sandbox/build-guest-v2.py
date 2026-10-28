#!/usr/bin/env python3
"""Build-only offline v2 assembler. Reads data; never executes archive contents."""
import gzip
import hashlib
import io
import json
import lzma
import os
from pathlib import Path
import posixpath
import re
import stat
import struct
import sys
import tarfile
import zlib

MAX_ARCHIVE = 768 * 1024 * 1024
MAX_FINAL = 512 * 1024 * 1024
MAX_MEMBERS = 20000
MAX_FILE = 128 * 1024 * 1024

def fail(message):
    raise ValueError(message)

def sha(data):
    return hashlib.sha256(data).hexdigest()

def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=True).encode()

def path_name(name):
    if (not isinstance(name, str) or not name or len(name.encode()) > 4096
            or re.search(r'[\\\x00-\x1f\x7f]', name) or name.startswith('/')
            or len(name.split('/')) > 64 or any(x in ('', '.', '..') for x in name.split('/'))):
        fail('unsafe archive path')
    return name

def tree_check(entries, case_exceptions=()):
    folded = {}
    allowed = {tuple(sorted(x)) for x in case_exceptions}
    for name in entries:
        path_name(name)
        parts = name.split('/')
        for i in range(1, len(parts) + 1):
            parent = '/'.join(parts[:i]); key = parent.casefold()
            if key in folded and folded[key] != parent and tuple(sorted((folded[key], parent))) not in allowed:
                fail('case alias')
            folded[key] = parent
            if i < len(parts) and parent in entries and entries[parent]['type'] != 'dir':
                fail('ancestor collision')

def read_tar(data, case_exceptions=()):
    if len(data) > MAX_ARCHIVE or len(data) % 512:
        fail('invalid tar size')
    entries = {}; at = 0; total = 0; ended = False; pending_name = None; headers = 0
    while at + 512 <= len(data):
        header = data[at:at+512]; at += 512
        if header == bytes(512):
            if at + 512 > len(data) or any(data[at:]): fail('trailing tar data or missing terminator')
            ended = True; break
        try: member = tarfile.TarInfo.frombuf(header, 'utf-8', 'strict')
        except (tarfile.HeaderError, UnicodeError, ValueError) as e: fail('invalid tar header: ' + str(e))
        if header[257:263] not in (b'ustar\0', b'ustar '): fail('unsupported tar format')
        headers += 1
        if headers > MAX_MEMBERS: fail('member count bound')
        if member.type == tarfile.GNUTYPE_LONGNAME:
            if pending_name is not None or not 1 < member.size <= 4096 or at+member.size > len(data): fail('GNU path metadata bound')
            value = data[at:at+member.size]
            if value[-1:] != b'\0' or b'\0' in value[:-1]: fail('GNU path metadata encoding')
            try:
                pending_name = value[:-1].decode('utf-8')
                path_name(pending_name.removesuffix('/'))
            except UnicodeError: fail('GNU path metadata encoding')
            padded = (member.size+511)//512*512
            if at+padded > len(data) or any(data[at+member.size:at+padded]): fail('GNU metadata padding')
            at += padded; continue
        name = (pending_name if pending_name is not None else member.name).removeprefix('./')
        if member.isdir(): name = name.removesuffix('/')
        pending_name = None
        if name in ('', '.') and member.isdir():
            if member.size: fail('directory data')
            continue
        path_name(name)
        if name in entries: fail('duplicate archive path')
        if member.mode & 0o7000: fail('privileged archive member')
        if member.size < 0 or member.size > MAX_FILE or at + member.size > len(data): fail('invalid member size')
        total += member.size
        if total > MAX_ARCHIVE: fail('archive decompression bound')
        if len(entries) >= MAX_MEMBERS: fail('member count bound')
        if member.type in (tarfile.REGTYPE, tarfile.AREGTYPE):
            entry = {'type': 'file', 'mode': '0555' if member.mode & 0o111 else '0444', 'data': data[at:at+member.size]}
        elif member.isdir(): entry = {'type': 'dir', 'mode': '0555'}
        elif member.issym() or member.islnk():
            if len(member.linkname.encode()) > 4096 or re.search(r'[\\\x00-\x1f\x7f]', member.linkname): fail('unsafe link target')
            entry = {'type': 'symlink' if member.issym() else 'hardlink', 'mode': '0777', 'target': member.linkname}
        else: fail('unsupported archive member (PAX/sparse/special)')
        if entry['type'] != 'file' and member.size: fail('non-file data')
        entries[name] = entry
        padded = (member.size + 511) // 512 * 512
        if at + padded > len(data) or any(data[at+member.size:at+padded]): fail('invalid tar padding')
        at += padded
    if not ended or pending_name is not None: fail('missing tar terminator/orphan long name')
    tree_check(entries, case_exceptions)
    # Hardlinks refer to archive-root members, never host paths.
    def materialize(name, seen):
        if name in seen or len(seen) >= 40: fail('hardlink cycle')
        e = entries.get(name)
        if not e: fail('missing hardlink target')
        if e['type'] == 'hardlink':
            target = path_name(e['target'].removeprefix('./'))
            value = materialize(target, seen | {name})
            entries[name] = dict(value)
        elif e['type'] != 'file': fail('hardlink terminal is not regular')
        return entries[name]
    for name in list(entries):
        if entries[name]['type'] == 'hardlink': materialize(name, set())
    return entries

def decode(data, codec, limit=MAX_ARCHIVE):
    if codec not in ('xz','gz'): fail('unknown compression format')
    if len(data) > MAX_ARCHIVE: fail('compressed archive bound')
    try:
        decoder = lzma.LZMADecompressor(format=lzma.FORMAT_XZ, memlimit=256*1024*1024) if codec == 'xz' else zlib.decompressobj(31)
        result = bytearray()
        # Incremental output prevents a forged length from allocating unbounded memory.
        chunk = data
        while True:
            remaining = min(1024*1024, limit + 1 - len(result))
            if remaining <= 0: fail('archive decompression bound')
            value = decoder.decompress(chunk, max_length=remaining); result.extend(value)
            if len(result) > limit: fail('archive decompression bound')
            if decoder.eof:
                if decoder.unused_data: fail('trailing compressed data')
                break
            if codec == 'xz':
                if decoder.needs_input: fail('truncated compressed archive')
                chunk = b''
            else:
                chunk = decoder.unconsumed_tail
                if not chunk and not value: fail('truncated compressed archive')
        return bytes(result)
    except (lzma.LZMAError, zlib.error) as e: fail('invalid compressed archive: ' + str(e))

def read_deb(data):
    if len(data) > MAX_ARCHIVE or not data.startswith(b'!<arch>\n'): fail('invalid ar magic/bound')
    at = 8; members = {}
    while at < len(data):
        h = data[at:at+60]; at += 60
        if len(h) != 60 or h[58:] != b'`\n': fail('invalid ar header')
        try:
            name = h[:16].decode('ascii').strip().removesuffix('/')
            number = h[48:58].decode('ascii').strip()
            if not number.isdecimal(): fail('invalid ar size')
            size = int(number)
        except UnicodeError: fail('invalid ar name/size')
        if name not in ('debian-binary', 'control.tar.xz', 'data.tar.xz') or name in members: fail('unknown/duplicate ar member')
        if size > MAX_ARCHIVE or at + size > len(data): fail('truncated ar member')
        members[name] = data[at:at+size]; at += size
        if size % 2:
            if data[at:at+1] != b'\n': fail('invalid ar alignment')
            at += 1
    if set(members) != {'debian-binary', 'control.tar.xz', 'data.tar.xz'} or members['debian-binary'] != b'2.0\n': fail('incomplete Debian archive')
    return members['data.tar.xz']

def resolve(entries, name):
    path_name(name)
    pending = name.split('/'); resolved = []; hops = 0
    while pending:
        part = pending.pop(0)
        if part in ('', '.'):
            continue
        if part == '..':
            if not resolved: fail('link escapes virtual root')
            resolved.pop(); continue
        current = '/'.join([*resolved, part]); e = entries.get(current)
        if e and e['type'] == 'symlink':
            hops += 1
            if hops > 40: fail('link cycle/depth bound')
            target = e['target']
            if not target or len(target.encode()) > 4096 or re.search(r'[\\\x00-\x1f\x7f]', target): fail('unsafe link target')
            # Expand links before consuming '..', exactly as a guest VFS does.
            if target.startswith('/'): resolved = []
            pending = target.split('/') + pending
            if len(pending) > 256: fail('link component bound')
            continue
        directory = e and e['type'] == 'dir'
        if e is None:
            directory = any(n.startswith(current+'/') for n in entries)
            if not directory: fail('missing link target: '+current)
        if pending and not directory: fail('link ancestor is not directory')
        resolved.append(part)
    result = '/'.join(resolved)
    path_name(result)
    return result

def check_links(entries):
    for name, e in entries.items():
        if e['type'] == 'symlink': resolve(entries, name)

def elf_info(data, arch, static=False):
    if arch not in ('amd64', 'arm64') or len(data) < 64 or data[:7] != b'\x7fELF\x02\x01\x01': fail('invalid ELF64 platform')
    typ, machine, version = struct.unpack_from('<HHI', data, 16)
    if machine != {'amd64':62, 'arm64':183}[arch] or typ not in (2, 3) or version != 1 or struct.unpack_from('<H', data, 52)[0] != 64: fail('invalid ELF architecture/header')
    phoff, shoff = struct.unpack_from('<QQ', data, 32)
    phsize, phnum, shsize, shnum = struct.unpack_from('<HHHH', data, 54)
    if phsize != 56 or phnum > 4096 or phoff + phnum*phsize > len(data): fail('ELF program header bound')
    if shnum and (shsize != 64 or shoff + shnum*64 > len(data)): fail('ELF section header bound')
    for i in range(shnum):
        row = struct.unpack_from('<IIQQQQIIQQ', data, shoff+i*64)
        if row[1] != 8 and row[4]+row[5] > len(data): fail('ELF section data bound')
    loads = []; dynamic = None; interp = None
    for i in range(phnum):
        ptype, flags, off, va, pa, size, mem, align = struct.unpack_from('<IIQQQQQQ', data, phoff+i*56)
        if off+size > len(data): fail('ELF segment bound')
        if ptype == 1: loads.append((va, off, size))
        if ptype == 2:
            if dynamic is not None or size % 16 or size // 16 > 10000: fail('ELF dynamic bound')
            dynamic = (off, size)
        if ptype == 3:
            value = data[off:off+size]
            if interp is not None or not 2 <= size <= 4096 or value[-1:] != b'\0' or b'\0' in value[:-1]: fail('ELF interpreter bound')
            try: interp = value[:-1].decode('ascii')
            except UnicodeError: fail('ELF interpreter encoding')
    if static and interp: fail('guest must be static (no interpreter)')
    tags = {}
    if dynamic:
        terminated = False
        for off in range(dynamic[0], sum(dynamic), 16):
            tag, value = struct.unpack_from('<QQ', data, off)
            if tag == 0: terminated = True; break
            tags.setdefault(tag, []).append(value)
        if not terminated: fail('ELF dynamic terminator missing')
    needed = []; rpath = []
    if tags:
        if len(tags.get(5, [])) != 1 or len(tags.get(10, [])) != 1: fail('ELF dynamic string table missing')
        address = tags[5][0]; size = tags[10][0]
        choices = [(off+address-va, length-(address-va)) for va, off, length in loads if va <= address < va+length]
        if len(choices) != 1 or size > choices[0][1]: fail('ELF string table bound')
        off = choices[0][0]; table = data[off:off+size]
        def string(index):
            end = table.find(b'\0', index)
            if not 0 <= index < end < len(table) or end-index > 4096: fail('ELF string bound')
            try: return table[index:end].decode('ascii')
            except UnicodeError: fail('ELF string encoding')
        needed = [string(x) for x in tags.get(1, [])]
        rpath = [string(x) for tag in (15, 29) for x in tags.get(tag, [])]
    if static and needed: fail('guest must have no shared imports')
    return {'interpreter': interp, 'needed': needed, 'rpath': rpath}

def check_elf(entries, arch, policy):
    objects = {n: elf_info(e['data'], arch) for n,e in entries.items() if e['type']=='file' and e['data'].startswith(b'\x7fELF')}
    imports = 0
    interpreter = {'amd64':'/lib64/ld-linux-x86-64.so.2', 'arm64':'/lib/ld-linux-aarch64.so.1'}[arch]
    for name, info in objects.items():
        if info['interpreter']:
            if info['interpreter'] != interpreter or resolve(entries, interpreter[1:]) not in objects: fail('foreign/missing ELF interpreter')
        if info['rpath'] != policy.get('origin_rpaths', {}).get(name, []): fail('unsafe ELF RPATH/RUNPATH: '+name)
        multi = 'x86_64-linux-gnu' if arch == 'amd64' else 'aarch64-linux-gnu'
        if info['rpath'] and (info['rpath'] != ['$ORIGIN'] or not re.fullmatch(r'usr/lib/'+multi+r'/gconv/[^/]+\.so', name)):
            fail('unsafe ELF origin rule')
        search = ([posixpath.dirname(name)] if info['rpath'] else []) + policy['search_dirs']
        for needed in info['needed']:
            if '/' in needed or not needed or len(needed) > 255: fail('unsafe ELF import')
            found = False
            for directory in search:
                try: target = resolve(entries, directory+'/'+needed)
                except ValueError: continue
                if target in objects: found = True; break
            if not found: fail('unresolved ELF import: '+name+' '+needed)
            imports += 1
    return {'objects':len(objects), 'imports': imports}

RUNNERS = ('opt/foundry/runners/node.cjs', 'opt/foundry/runners/python.py', 'opt/foundry/runners/typescript.cjs')
RUNTIME_KINDS = ('debian-runtime-package', 'node-runtime', 'typescript-compiler')
SOURCE_KINDS = ('debian-corresponding-source', 'node-source', 'typescript-source')

_windows_backend = None

def windows_backend():
    global _windows_backend
    if _windows_backend is None:
        import importlib.util
        # -I excludes sibling directories from sys.path. Load only this fixed,
        # owned helper next to the trusted builder, never a runtime/input file.
        location=Path(__file__).with_name('build-guest-v2-windows.py')
        spec=importlib.util.spec_from_file_location('foundry_owned_windows_fs',location)
        module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
        _windows_backend=module.WindowsFS()
    return _windows_backend

def host_path(value, directory=False):
    if os.name=='nt':return windows_backend().host_path(value,directory)
    path = Path(value)
    if not path.is_absolute() or '..' in path.parts: fail('canonical absolute host path required')
    for part in [*reversed(path.parents), path]:
        try: mode = part.lstat().st_mode
        except OSError: fail('missing input path')
        if stat.S_ISLNK(mode): fail('host symlink ancestry')
    if (not path.is_dir() if directory else not path.is_file()): fail('invalid host input type')
    return path

def directory_fd(value):
    """Pin every host directory component without following symlinks."""
    path = Path(value)
    if not path.is_absolute() or '..' in path.parts: fail('canonical absolute host path required')
    fd = os.open('/', os.O_RDONLY | os.O_DIRECTORY)
    try:
        for part in path.parts[1:]:
            next_fd = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
            os.close(fd); fd = next_fd
        return fd
    except BaseException:
        os.close(fd); raise

def read_regular(path, limit, size=None, digest=None):
    if os.name=='nt':return windows_backend().read_regular(path,limit,size,digest)
    path = host_path(path)
    parent = directory_fd(path.parent)
    try: fd = os.open(path.name, os.O_RDONLY | os.O_NOFOLLOW, dir_fd=parent)
    finally: os.close(parent)
    try:
        before = os.fstat(fd)
        if not stat.S_ISREG(before.st_mode) or before.st_size > limit or (size is not None and before.st_size != size): fail('input size/type mismatch')
        with os.fdopen(fd, 'rb', closefd=False) as stream: data = stream.read(limit+1)
        after = os.fstat(fd)
        identity = lambda s: (s.st_dev,s.st_ino,s.st_mode,s.st_size,s.st_mtime_ns,s.st_ctime_ns)
        if identity(before) != identity(after) or len(data) != before.st_size or len(data) > limit: fail('input changed/size bound')
        if digest is not None and sha(data) != digest: fail('input SHA-256 mismatch')
        return data
    finally: os.close(fd)

def inventory(entries):
    result = []
    for name, e in sorted(entries.items()):
        if e['type'] == 'dir': continue
        row = {'path':name, 'type':e['type'], 'mode':e['mode']}
        if e['type']=='file': row.update(size=len(e['data']), sha256=sha(e['data']))
        else: row['target']=e['target']
        result.append(row)
    return {'sha256':sha(canonical(result)), 'entries':len(result), 'regular_bytes':sum(len(e['data']) for e in entries.values() if e['type']=='file')}

def project(lock, input_dir, arch, sources=False):
    if lock.get('schema_version') != 2 or lock.get('projection_policy') != 'foundry-runtime-projection-v2': fail('unknown lock schema/policy')
    if arch not in ('amd64','arm64') or arch not in lock['architectures']: fail('unsupported lock architecture')
    root = host_path(input_dir, directory=True); policy = lock['architectures'][arch]
    entries = {}; identities = []; seen = set(); source_count = 0
    for row in lock['artifacts']:
        filename = path_name(row['file'])
        if '/' in filename or filename in seen or row['kind'] not in RUNTIME_KINDS+SOURCE_KINDS: fail('unknown/duplicate lock input')
        seen.add(filename)
        if not isinstance(row['size'], int) or not 0 < row['size'] <= MAX_ARCHIVE or not re.fullmatch('[0-9a-f]{64}', row['sha256']): fail('invalid lock identity')
        if row['kind'] in RUNTIME_KINDS and arch not in row['target_architectures']: continue
        if row['kind'] in SOURCE_KINDS and not sources: continue
        data = read_regular(root/filename, MAX_ARCHIVE, row['size'], row['sha256'])
        identities.append({'file':filename, 'size':len(data), 'sha256':sha(data)})
        if row['kind'] in SOURCE_KINDS: source_count += 1; continue
        codec = 'gz' if row['kind']=='typescript-compiler' else 'xz'
        archive = read_tar(decode(read_deb(data) if row['kind']=='debian-runtime-package' else data, codec), policy.get('archive_case_exceptions', []))
        for name, e in archive.items():
            if e['type']=='dir': continue
            dest = name
            if row['kind']=='node-runtime':
                parts = name.split('/',1)
                if not row.get('archive_root') or parts[0] != row['archive_root']: fail('foreign Node archive root')
                if len(parts)!=2 or parts[1] not in ('bin/node','LICENSE'): continue
                dest = 'opt/foundry/node/'+parts[1]
            elif row['kind']=='typescript-compiler':
                if not name.startswith('package/'): fail('foreign TypeScript root')
                tail=name[8:]
                if not tail.startswith('lib/') and tail not in ('package.json','LICENSE.txt','ThirdPartyNoticeText.txt'): continue
                dest='opt/foundry/typescript/'+tail
            if e['type']=='symlink' and policy['links'].get(dest) != e['target']: fail('unlisted runtime link')
            if dest in entries and entries[dest] != e: fail('conflicting runtime files: '+dest)
            entries[dest] = e
            if len(entries)>MAX_MEMBERS: fail('final runtime member bound')
            if sum(len(v['data']) for v in entries.values() if v['type']=='file') > MAX_FINAL: fail('final runtime bound')
    for name, target in policy['links'].items():
        if name in ('lib','lib64','etc/localtime'):
            if name in entries: fail('runtime/owned alias collision')
            entries[name]={'type':'symlink','mode':'0777','target':target}
    if {n:e['target'] for n,e in entries.items() if e['type']=='symlink'} != policy['links']: fail('missing lock link')
    tree_check(entries, policy.get('case_exceptions', [])); check_links(entries)
    measured = inventory(entries)
    if measured != policy['expected']: fail('normalized runtime inventory mismatch: '+json.dumps(measured))
    for name in policy['required']:
        if resolve(entries, name) not in entries: fail('missing required runtime file')
    elf = check_elf(entries, arch, policy['elf'])
    result = {'schema_version':2, 'status':'static-input-validation-only', 'arch':arch, 'lock_sha256':sha(canonical(lock)), 'inputs_sha256':sha(canonical(sorted(identities,key=lambda x:x['file']))), 'runtime':measured, 'elf':elf, 'sources_hashed':source_count}
    return entries, result

def validate(lock, input_dir, arch, sources=False):
    return project(lock, input_dir, arch, sources)[1]

def newc(entries):
    out = io.BytesIO()
    for inode, (name,e) in enumerate([*sorted(entries.items()), ('TRAILER!!!', {'type':'file','mode':'0000','data':b''})], 1):
        data = e.get('data', b'') if e['type']!='symlink' else e['target'].encode()
        mode = (0o40000 if e['type']=='dir' else 0o120000 if e['type']=='symlink' else 0o100000) | int(e['mode'],8)
        if name=='TRAILER!!!': mode=0
        n = name.encode()+b'\0'
        fields=[inode,mode,0,0,2 if e['type']=='dir' else 1,0,len(data),0,0,0,0,len(n),0]
        out.write(('070701'+''.join(f'{v:08x}' for v in fields)).encode()); out.write(n)
        out.write(bytes((-out.tell())%4)); out.write(data); out.write(bytes((-len(data))%4))
        if out.tell()>MAX_FINAL: fail('final cpio bound')
    return out.getvalue()

def assemble(lock, input_dir, arch, guest, runners):
    if len(guest)>64*1024*1024: fail('guest size bound')
    elf_info(guest, arch, static=True)
    if set(runners)!=set(RUNNERS): fail('exact owned runner inventory required')
    owned=[]
    for name, row in runners.items():
        if not isinstance(row['data'],bytes) or not 0 < len(row['data']) <= 1024*1024 or sha(row['data'])!=row['sha256']: fail('runner hash/size mismatch')
        owned.append({'path':name, 'sha256':sha(row['data']), 'size':len(row['data'])})
    entries, receipt=project(lock, input_dir, arch)
    for name in ['init','opt/foundry/runners',*RUNNERS,'dev','proc','sys','workspace','tmp','foundry-build','foundry-deps']:
        if name in entries or any(n.startswith(name+'/') for n in entries): fail('runtime/control overlap')
    entries['init']={'type':'file','mode':'0555','data':guest}
    for name,row in runners.items(): entries[name]={'type':'file','mode':'0444','data':row['data']}
    for name in ['dev','proc','sys','sys/fs','sys/fs/cgroup','workspace','tmp','foundry-build','foundry-deps','opt/foundry']:
        entries.setdefault(name, {'type':'dir','mode':'0555'})
    for name in list(entries):
        for parent in list(Path(name).parents)[:-1]:
            n=parent.as_posix()
            if n in entries and entries[n]['type']!='dir': fail('image ancestor collision')
            entries.setdefault(n, {'type':'dir','mode':'0555'})
    tree_check(entries); check_links(entries)
    if len(entries)>MAX_MEMBERS: fail('final image member bound')
    raw=newc(entries)
    output=io.BytesIO()
    with gzip.GzipFile(filename='',fileobj=output, mode='wb', compresslevel=9, mtime=0) as stream: stream.write(raw)
    image=output.getvalue(); receipt.update(status='assembled-build-only', guest={'sha256':sha(guest),'size':len(guest)}, runners=sorted(owned,key=lambda x:x['path']), image={'sha256':sha(image),'size':len(image)})
    return image, receipt

def publish(output, receipt_path, image, receipt):
    if os.name=='nt':return windows_backend().publish(output,receipt_path,image,lambda:canonical(receipt)+b'\n')
    paths=[Path(output),Path(receipt_path)]
    if paths[0]==paths[1]: fail('output and receipt must differ')
    parents=[]; owned=[]
    try:
        for p in paths:
            host_path(p.parent,directory=True)
            parent=directory_fd(p.parent); parents.append(parent)
            try: os.stat(p.name,dir_fd=parent,follow_symlinks=False)
            except FileNotFoundError: pass
            else: fail('refusing output overwrite')
        for index,(p,parent) in enumerate(zip(paths,parents)):
            data=image if index==0 else canonical(receipt)+b'\n'
            fd=os.open(p.name,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o644,dir_fd=parent)
            identity=os.fstat(fd); owned.append((p.name,parent,identity.st_dev,identity.st_ino))
            with os.fdopen(fd,'wb') as stream: stream.write(data); stream.flush(); os.fsync(stream.fileno())
    except BaseException:
        for name,parent,dev,ino in reversed(owned):
            try:
                s=os.stat(name,dir_fd=parent,follow_symlinks=False)
                if (s.st_dev,s.st_ino)==(dev,ino): os.unlink(name,dir_fd=parent)
            except FileNotFoundError: pass
        raise
    finally:
        for parent in parents: os.close(parent)

def main():
    if sys.version_info < (3,13): fail('trusted host Python >=3.13 required')
    import argparse
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--lock',required=True); parser.add_argument('--inputs',required=True); parser.add_argument('--arch',required=True,choices=['amd64','arm64'])
    parser.add_argument('--validate-only',action='store_true'); parser.add_argument('--sources',action='store_true')
    parser.add_argument('--guest'); parser.add_argument('--runners'); parser.add_argument('--output'); parser.add_argument('--receipt')
    args=parser.parse_args(); lock=json.loads(read_regular(Path(args.lock),2*1024*1024))
    if args.validate_only:
        if any([args.guest,args.runners,args.output,args.receipt]): fail('validate-only cannot publish an image')
        print(json.dumps(validate(lock,Path(args.inputs),args.arch,args.sources),sort_keys=True)); return
    if args.sources or not all([args.guest,args.runners,args.output,args.receipt]): fail('explicit guest/runners/output/receipt required')
    runner_rows=json.loads(read_regular(Path(args.runners),64*1024)); runners={}
    if not isinstance(runner_rows,list) or len(runner_rows)!=len(RUNNERS): fail('exact runner manifest required')
    for row in runner_rows:
        if set(row)!={'path','source','sha256'} or row['path'] in runners: fail('invalid runner manifest')
        runners[row['path']]={'data':read_regular(Path(row['source']),1024*1024,digest=row['sha256']),'sha256':row['sha256']}
    image,receipt=assemble(lock,Path(args.inputs),args.arch,read_regular(Path(args.guest),64*1024*1024),runners)
    publish(args.output,args.receipt,image,receipt); print(json.dumps(receipt,sort_keys=True))

if __name__=='__main__':
    try: main()
    except (ValueError,OSError,KeyError,TypeError,json.JSONDecodeError) as e:
        print(str(e),file=sys.stderr); sys.exit(1)
