"""Owned Win32 build-host I/O. No archive paths, commands or imports enter here."""
import ctypes as c
import hashlib
import ntpath
import os
import re
from pathlib import Path


def windows_path(value):
    text=os.fspath(value)
    if (not isinstance(text,str) or not re.match(r'^[A-Za-z]:[\\/]',text)
            or len(text.encode('utf-16-le'))//2 > 32760):
        raise ValueError('canonical drive-absolute Windows path required')
    text=text.replace('/','\\')
    for part in text[3:].split('\\') if len(text)>3 else []:
        if (not part or part in ('.','..') or part.endswith(('.',' '))
                or re.search(r'[:*?"<>|~\x00-\x1f\x7f]',part)
                or any(0xd800<=ord(x)<=0xdfff for x in part)):
            raise ValueError('Windows path alias refused')
        base=part.split('.')[0].upper()
        if base in ('CON','PRN','AUX','NUL') or re.fullmatch(r'(COM|LPT)[1-9¹²³]',base):
            raise ValueError('Windows device path refused')
    return text


# Fixed Win32 widths also let the portable contract test validate this ABI.
class FileTime(c.Structure):
    _fields_=[('low',c.c_uint32),('high',c.c_uint32)]

class FileInfo(c.Structure):
    _fields_=[('attributes',c.c_uint32),('created',FileTime),('accessed',FileTime),
              ('written',FileTime),('volume',c.c_uint32),('size_high',c.c_uint32),
              ('size_low',c.c_uint32),('links',c.c_uint32),('index_high',c.c_uint32),('index_low',c.c_uint32)]
    def identity(self):return (self.volume,self.index_high,self.index_low)
    def size(self):return self.size_high*2**32+self.size_low
    def stable(self):
        return (self.identity(),self.attributes,self.size(),self.links,self.created.low,
                self.created.high,self.written.low,self.written.high)

class DeleteInfo(c.Structure):
    _fields_=[('delete',c.c_ubyte)]

class Pinned:
    def __init__(self,fs,handles):self.fs=fs;self.handles=handles
    @property
    def handle(self):return self.handles[-1]
    def close(self):
        while self.handles:self.fs.close(self.handles.pop())

class WindowsFS:
    def __init__(self):
        if os.name!='nt':raise OSError('native Windows host required for Win32 filesystem I/O')
        # Load only a trusted system DLL, never from an input/current directory.
        self.kernel=c.WinDLL('kernel32.dll',use_last_error=True,winmode=0x00000800)
        signatures={
          'CreateFileW':(c.c_void_p,[c.c_wchar_p,c.c_uint32,c.c_uint32,c.c_void_p,c.c_uint32,c.c_uint32,c.c_void_p]),
          'CloseHandle':(c.c_int32,[c.c_void_p]),
          'GetFileType':(c.c_uint32,[c.c_void_p]),
          'GetFileInformationByHandle':(c.c_int32,[c.c_void_p,c.POINTER(FileInfo)]),
          'GetFinalPathNameByHandleW':(c.c_uint32,[c.c_void_p,c.c_wchar_p,c.c_uint32,c.c_uint32]),
          'ReadFile':(c.c_int32,[c.c_void_p,c.c_void_p,c.c_uint32,c.POINTER(c.c_uint32),c.c_void_p]),
          'WriteFile':(c.c_int32,[c.c_void_p,c.c_void_p,c.c_uint32,c.POINTER(c.c_uint32),c.c_void_p]),
          'FlushFileBuffers':(c.c_int32,[c.c_void_p]),
          'SetFileInformationByHandle':(c.c_int32,[c.c_void_p,c.c_int32,c.c_void_p,c.c_uint32])}
        for name,(result,args) in signatures.items():
            fn=getattr(self.kernel,name);fn.restype=result;fn.argtypes=args

    def close(self,handle):
        if not self.kernel.CloseHandle(handle):raise c.WinError(c.get_last_error())

    def raw_open(self,path,access,sharing,creation):
        path=windows_path(path)
        h=self.kernel.CreateFileW('\\\\?\\'+path,access,sharing,None,creation,0x02200000,None)
        if h==c.c_void_p(-1).value:
            code=c.get_last_error()
            if creation==1 and code in (80,183):raise ValueError('refusing output overwrite')
            if code in (2,3):raise ValueError('missing input path')
            raise c.WinError(code)
        return h

    def info(self,handle):
        info=FileInfo()
        if not self.kernel.GetFileInformationByHandle(handle,c.byref(info)):raise c.WinError(c.get_last_error())
        return info

    def check(self,handle,path,directory):
        info=self.info(handle)
        if (self.kernel.GetFileType(handle)!=1 or info.attributes&0x400
                or bool(info.attributes&0x10)!=directory or (not directory and info.links!=1)):
            raise ValueError('nonregular, linked or reparse Windows path refused')
        name=c.create_unicode_buffer(32768)
        count=self.kernel.GetFinalPathNameByHandleW(handle,name,len(name),0)
        if not 0<count<len(name):raise ValueError('Windows canonical handle path unavailable')
        if name.value.casefold()!=('\\\\?\\'+windows_path(path)).casefold():
            raise ValueError('Windows canonical handle path mismatch')
        return info

    def open_chain(self,path,directory):
        path=windows_path(path);parts=path[3:].split('\\') if len(path)>3 else []
        current=path[:3];handles=[]
        try:
            for index,part in enumerate(['',*parts]):
                if part:current=ntpath.join(current,part)
                want_dir=index<len(parts) or directory
                # Directory-data access plus no SHARE_DELETE pins even empty
                # ancestors. Files also deny SHARE_WRITE for stable byte reads.
                access=0x20081 if want_dir else 0x80000000
                h=self.raw_open(current,access,3 if want_dir else 1,3);handles.append(h)
                self.check(h,current,want_dir)
            return Pinned(self,handles)
        except BaseException:
            for h in reversed(handles):self.close(h)
            raise

    def host_path(self,path,directory=False):
        path=windows_path(path);chain=self.open_chain(path,directory)
        try:return Path(path)
        finally:chain.close()

    def read_regular(self,path,limit,size=None,digest=None):
        chain=self.open_chain(path,False)
        try:
            before=self.info(chain.handle)
            if before.size()>limit or (size is not None and before.size()!=size):raise ValueError('input size/type mismatch')
            chunks=[];total=0;buffer=c.create_string_buffer(min(1024*1024,limit+1))
            while True:
                count=c.c_uint32();maximum=min(len(buffer),limit+1-total)
                if not maximum:raise ValueError('input size bound')
                if not self.kernel.ReadFile(chain.handle,buffer,maximum,c.byref(count),None):raise c.WinError(c.get_last_error())
                if not count.value:break
                chunks.append(buffer.raw[:count.value]);total+=count.value
            if before.stable()!=self.info(chain.handle).stable() or total!=before.size() or total>limit:
                raise ValueError('input changed/size bound')
            data=b''.join(chunks)
            if digest is not None and hashlib.sha256(data).hexdigest()!=digest:raise ValueError('input SHA-256 mismatch')
            return data
        finally:chain.close()

    def publish(self,output,receipt,image,receipt_bytes,inventory=None):
        paths=[windows_path(output),windows_path(receipt)]+([windows_path(inventory[0])] if inventory else [])
        if len({p.casefold() for p in paths})!=len(paths):raise ValueError('output, receipt and inventory must differ')
        parents=[];owned=[]
        try:
            for path in paths:parents.append(self.open_chain(ntpath.dirname(path),True))
            for index,path in enumerate(paths):
                data=image if index==0 else receipt_bytes() if index==1 else inventory[1]
                # CREATE_NEW, no sharing, DELETE right for rollback on this
                # exact still-open handle; no path-based deletion race.
                h=self.raw_open(path,0xc0010000,0,1)
                owned.append(h);self.check(h,path,False)
                for at in range(0,len(data),1024*1024):
                    chunk=data[at:at+1024*1024];buffer=c.create_string_buffer(chunk,len(chunk));count=c.c_uint32()
                    if not self.kernel.WriteFile(h,buffer,len(chunk),c.byref(count),None):raise c.WinError(c.get_last_error())
                    if count.value!=len(chunk):raise OSError('incomplete Windows output write')
                if not self.kernel.FlushFileBuffers(h):raise c.WinError(c.get_last_error())
        except BaseException:
            for h in reversed(owned):
                # CREATE_NEW plus the retained exclusive handle proves exact
                # ownership even if subsequent metadata validation failed.
                deletion=DeleteInfo(1)
                if not self.kernel.SetFileInformationByHandle(h,4,c.byref(deletion),c.sizeof(deletion)):
                    raise c.WinError(c.get_last_error())
            raise
        finally:
            for h in reversed(owned):self.close(h)
            for chain in reversed(parents):chain.close()
