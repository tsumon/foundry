"""Host filesystem contract; real Win32 operations run on native Windows only."""
import ctypes
import importlib.util
import os
from pathlib import Path
import struct
import sys
import tempfile
import unittest
sys.dont_write_bytecode = True

HELPER = Path(__file__).parents[1] / 'sandbox' / 'build-guest-v2-windows.py'

def load_helper():
    spec=importlib.util.spec_from_file_location('owned_windows_fs',HELPER)
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
    return module

class WindowsContractTests(unittest.TestCase):
    def test_windows_host_boundary_has_canonical_drive_path_and_fixed_width_abi(self):
        self.assertTrue(HELPER.exists(),'native Windows build-host boundary is missing')
        m=load_helper()
        for value in [r'C:\Source Folder\用户\input.deb',r'd:\build\image.gz']:
            self.assertEqual(m.windows_path(value),value)
        for value in [r'\rooted',r'C:relative',r'\\server\share\file',r'\\?\C:\file',
                      'C:\\dir\\..\\file','C:\\dir\\.\\file','C:\\dir\\file.',
                      'C:\\dir\\file ',r'C:\dir\file:stream',r'C:\dir\NUL.txt',
                      r'C:\dir\COM¹',r'C:\SHORT~1\file',r'C:\dir\\file']:
            with self.subTest(value=value),self.assertRaises(ValueError):m.windows_path(value)
        self.assertEqual(ctypes.sizeof(m.FileInfo),52)
        self.assertEqual(ctypes.sizeof(m.DeleteInfo),1)

if os.name=='nt':
    class NativeWindowsTests(unittest.TestCase):
        @classmethod
        def setUpClass(cls):cls.m=load_helper();cls.fs=cls.m.WindowsFS()

        def test_native_read_pins_ancestors_and_file_until_close(self):
            with tempfile.TemporaryDirectory() as tmp:
                root=Path(tmp).resolve();parent=root/'用户 Source Folder';parent.mkdir()
                file=parent/'data';file.write_bytes(b'fixed bytes')
                chain=self.fs.open_chain(str(file),False)
                try:
                    for path in [parent,file]:
                        with self.assertRaises(OSError):path.rename(Path(str(path)+'-moved'))
                    with self.assertRaises(OSError):file.write_bytes(b'change')
                finally:chain.close()
                self.assertEqual(self.fs.read_regular(str(file),64),b'fixed bytes')
                file.rename(parent/'renamed');parent.rename(root/'moved')

        def test_native_empty_directory_chain_is_pinned_until_close(self):
            with tempfile.TemporaryDirectory() as tmp:
                parent=Path(tmp).resolve()/'parent';parent.mkdir();leaf=parent/'empty';leaf.mkdir()
                chain=self.fs.open_chain(str(leaf),True)
                try:
                    for path in [parent,leaf]:
                        with self.assertRaises(OSError):path.rename(Path(str(path)+'-moved'))
                finally:chain.close()
                leaf.rename(parent/'moved')

        def test_native_junction_ancestor_terminal_and_output_parent_are_refused(self):
            with tempfile.TemporaryDirectory() as tmp:
                root=Path(tmp).resolve();target=root/'target';target.mkdir();(target/'file').write_bytes(b'outside')
                link=root/'junction';make_junction(self.m,link,target)
                try:
                    self.assertEqual((link/'file').read_bytes(),b'outside')
                    with self.assertRaisesRegex(ValueError,'reparse'):self.fs.read_regular(str(link/'file'),64)
                    with self.assertRaisesRegex(ValueError,'reparse'):self.fs.host_path(str(link),True)
                    with self.assertRaisesRegex(ValueError,'reparse'):self.fs.publish(str(link/'image'),str(root/'receipt'),b'image',lambda:b'{}')
                    self.assertFalse((target/'image').exists())
                finally:link.rmdir()

        def test_native_file_alias_is_refused_and_publication_rolls_back_owned_handle(self):
            with tempfile.TemporaryDirectory() as tmp:
                root=Path(tmp).resolve();file=root/'input';file.write_bytes(b'bytes');alias=root/'alias';os.link(file,alias)
                with self.assertRaises(ValueError):self.fs.read_regular(str(alias),64)
                out=root/'image';receipt=root/'receipt'
                receipt.write_bytes(b'KEEP')
                with self.assertRaises(ValueError):self.fs.publish(str(out),str(receipt),b'image',lambda:b'{}')
                self.assertFalse(out.exists());self.assertEqual(receipt.read_bytes(),b'KEEP')
                receipt.unlink()
                def broken():raise TypeError('receipt serialization failure')
                with self.assertRaises(TypeError):self.fs.publish(str(out),str(receipt),b'image',broken)
                self.assertFalse(out.exists());self.assertFalse(receipt.exists())
                self.fs.publish(str(out),str(receipt),b'image',lambda:b'{}')
                self.assertEqual(out.read_bytes(),b'image');self.assertEqual(receipt.read_bytes(),b'{}')

# Direct equivalent of internal/platformfs/native_windows_test.go's testJunction.
# No shell, administrator-only symlink privilege or project code is needed.
def make_junction(module,link,target):
    link.mkdir()
    api=module.WindowsFS();h=api.raw_open(str(link),0x40000000,0,3)
    try:
        substitute=('\\??\\'+str(target)+'\0').encode('utf-16-le')
        display=(str(target)+'\0').encode('utf-16-le')
        payload=struct.pack('<IHHHHHH',0xA0000003,8+len(substitute)+len(display),0,0,len(substitute)-2,len(substitute),len(display)-2)+substitute+display
        ioctl=api.kernel.DeviceIoControl
        ioctl.argtypes=[ctypes.c_void_p,ctypes.c_uint32,ctypes.c_void_p,ctypes.c_uint32,ctypes.c_void_p,ctypes.c_uint32,ctypes.POINTER(ctypes.c_uint32),ctypes.c_void_p]
        ioctl.restype=ctypes.c_int32
        buffer=ctypes.create_string_buffer(payload);returned=ctypes.c_uint32()
        if not ioctl(h,0x000900A4,buffer,len(payload),None,0,ctypes.byref(returned),None):raise ctypes.WinError(ctypes.get_last_error())
    finally:api.close(h)

if __name__=='__main__':unittest.main()
