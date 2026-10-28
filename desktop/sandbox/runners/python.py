"""Image-owned unittest adapter. Same-interpreter Python is not anti-forgery
isolation against hostile code with identical process privileges."""
# Import stdlib/unittest before adding any admitted project/dependency roots.
import importlib.util
import json
import os
import sys
import tempfile
import unittest


class FailureDiagnostics:
    """Bounded scalar stderr only. Never format tracebacks or project objects."""
    def __init__(self, root=''):
        self.root = os.path.abspath(root) if root else ''
        self.remaining = 16384

    def text(self, value, limit):
        if type(value) is not str:
            return '[non-scalar]'
        value = value[:4096]
        if self.root:
            value = value.replace(self.root, '<project>')
        return ''.join(c if 32 <= ord(c) <= 126 else '\\u%04x' % ord(c) for c in value[:limit])[:limit]

    def failure(self, stage, test, err):
        if not self.remaining:
            return
        name, file, line, error_type, message = '', '', 0, '', ''
        if isinstance(test, unittest.TestCase):
            # Use the base instance-dict descriptor, not an overridden id/str
            # method or subtest parameter repr supplied by the project.
            fields = unittest.TestCase.__dict__['__dict__'].__get__(test, type(test))
            method = fields.get('_testMethodName', '')
            name = self.text(type.__dict__['__name__'].__get__(type(test)), 63) + '.' + self.text(method, 64)
        if type(err) is tuple and len(err) == 3 and isinstance(err[1], BaseException):
            exc = err[1]
            error_type = self.text(type.__dict__['__name__'].__get__(type(exc)), 64)
            args = BaseException.args.__get__(exc, type(exc))
            # Exception args may contain hostile __str__/__repr__ objects.
            message = self.text('; '.join(self.text(a, 1024) for a in args[:4]), 1024)
            tb = err[2]
            for _ in range(64):
                if tb is None:
                    break
                if self.root and os.path.isabs(tb.tb_frame.f_code.co_filename):
                    try:
                        p = os.path.relpath(tb.tb_frame.f_code.co_filename, self.root)
                    except ValueError:
                        p = '..'  # A host regression runtime may use another drive.
                    if p != '..' and not p.startswith('..' + os.sep) and not os.path.isabs(p):
                        file, line = self.text(p, 256), tb.tb_lineno if 0 < tb.tb_lineno <= 1000000 else 0
                tb = tb.tb_next
        raw = (json.dumps(dict(kind='foundry_test_failure_diagnostic_v2', runner='python-unittest-v2',
            stage=stage, name=name, file=file, line=line, error_type=error_type, message=message),
            ensure_ascii=True, separators=(',', ':')) + '\n').encode('ascii')
        if len(raw) > self.remaining:
            self.remaining = 0
            return
        self.remaining -= len(raw)
        # FD3 remains the sole closed count/class report channel.
        while raw:
            try:
                n = os.write(2, raw)
            except OSError:
                break  # Diagnostic delivery never changes framework authority.
            if n <= 0:
                break
            raw = raw[n:]


class Report:
    def __init__(self, fd):
        self.fd, self.seq, self.started, self.size = fd, 0, 0, 0
        self.counts = dict.fromkeys(('passed', 'failed', 'skipped', 'expected_failed', 'unexpected_passed'), 0)
        self.event('suite_start', schema_version=1, runner='python-unittest-v2')

    def event(self, kind, **fields):
        self.seq += 1
        raw = (json.dumps(dict(type=kind, seq=self.seq, **fields), separators=(',', ':')) + '\n').encode('ascii')
        self.size += len(raw)
        if self.size > 2097152 or self.seq > 8194:
            raise RuntimeError('report limit')
        while raw:
            n = os.write(self.fd, raw)
            if n <= 0:
                raise RuntimeError('report write')
            raw = raw[n:]

    def completed(self, status):
        if self.started == 4096:
            raise RuntimeError('case limit')
        self.started += 1
        self.event('case_start', id=self.started)
        self.event('case_end', id=self.started, status=status)
        self.counts[status] += 1

    def end(self, discovery):
        if discovery > 4096:
            raise RuntimeError('discovery limit')
        self.event('suite_end', started=self.started, **self.counts, discovery_errors=discovery)
        return 1 if discovery or self.counts['failed'] or self.counts['unexpected_passed'] else 0


class Result(unittest.TestResult):
    """One case per TestCase method; subtests aggregate into that method.
    Import/setup/error placeholders never emit case lifecycles. Expected
    failures and skips remain distinct and never become completed assertions.
    """
    def __init__(self, report, root=''):
        super().__init__()
        self.report, self.discovery, self.active, self.status = report, 0, None, None
        self.infrastructure = False
        self.stage = 'discovery'
        self.diagnostics = FailureDiagnostics(root)

    def startTest(self, test):
        super().startTest(test)
        self.active, self.status, self.infrastructure = test, None, False
        self.stage = 'setup'
        self.original_calls = {}
        # unittest reports an assertion in setUp/tearDown/cleanup through the
        # same addFailure callback as a method assertion. Track the native
        # TestCase stage so setup placeholders cannot authorize repair.
        for name, stage in (('_callSetUp', 'setup'), ('_callTestMethod', 'test'), ('_callTearDown', 'teardown'), ('_callCleanup', 'cleanup')):
            original = getattr(test, name)
            self.original_calls[name] = original
            def call(*args, _original=original, _stage=stage, **kwargs):
                self.stage = _stage
                return _original(*args, **kwargs)
            setattr(test, name, call)

    def stopTest(self, test):
        if not self.infrastructure and self.status:
            self.report.completed(self.status)
        for name, original in self.original_calls.items():
            setattr(test, name, original)
        self.active = None
        super().stopTest(test)

    def addSuccess(self, test):
        if self.status != 'failed':
            self.status = 'passed'

    def addFailure(self, test, err):
        if self.stage != 'test':
            self.addError(test, err)
        else:
            self.status = 'failed'
            self.diagnostics.failure('test', test, err)

    def addError(self, test, err):
        self.discovery += 1
        self.infrastructure = True
        self.diagnostics.failure(self.stage if self.active is not None else 'discovery', test, err)

    def addSkip(self, test, reason):
        if self.active is test:
            self.status = 'skipped'
        elif self.stage == 'test' and self.active is not None and self.status is None:
            self.status = 'skipped'

    def addExpectedFailure(self, test, err):
        self.status = 'expected_failed'
        self.diagnostics.failure('test', test, err)

    def addUnexpectedSuccess(self, test):
        self.status = 'unexpected_passed'

    def addSubTest(self, test, subtest, err):
        if self.stage != 'test':
            if err is not None:
                self.addError(test, err)
            return
        if err is None:
            if self.status != 'failed':
                self.status = 'passed'
        elif issubclass(err[0], test.failureException):
            self.status = 'failed'
            self.diagnostics.failure('test', test, err)
        else:
            self.addError(test, err)


def run_tests(contract, fd):
    report = Report(fd)
    root = contract['source_root']
    # The static parent has already admitted and frozen these exact roots.
    sys.path[:0] = [os.path.join(root, p) for p in contract['source_roots']] + ['/foundry-deps/python']
    result = Result(report, root)
    loader = unittest.TestLoader()
    suite = unittest.TestSuite()
    for i, file in enumerate(contract['test_files']):
        try:
            spec = importlib.util.spec_from_file_location('foundry_owned_test_' + str(i), os.path.join(root, file))
            module = importlib.util.module_from_spec(spec)
            sys.modules[spec.name] = module
            spec.loader.exec_module(module)
            suite.addTests(loader.loadTestsFromModule(module))
        except BaseException:
            result.discovery += 1
            result.diagnostics.failure('discovery', None, sys.exc_info())
    suite.run(result)
    return report.end(result.discovery)


def probe():
    # Native unittest callbacks, including subtests, exercised without imports
    # from project or dependency paths and without project report records.
    class Owned(unittest.TestCase):
        def test_owned(self):
            with self.subTest(owned=True):
                self.assertEqual(1, 1)
    with tempfile.TemporaryFile() as output:
        report = Report(output.fileno())
        result = Result(report)
        unittest.defaultTestLoader.loadTestsFromTestCase(Owned).run(result)
        if report.end(result.discovery) or report.started != 1 or report.counts['passed'] != 1:
            raise RuntimeError('owned unittest fixture')
        output.seek(0)
        events = [json.loads(line) for line in output]
        if len(events) != 4 or events[0]['runner'] != 'python-unittest-v2' or events[-1]['passed'] != 1:
            raise RuntimeError('owned reporter fixture')


def main():
    if len(sys.argv) != 1 or sys.version_info[:3] != (3, 13, 5) or not sys.flags.isolated or not sys.flags.no_site or not sys.dont_write_bytecode:
        raise RuntimeError('runtime contract')
    raw = os.read(4, 1048577)
    os.close(4)
    if len(raw) > 1048576:
        raise RuntimeError('contract limit')
    contract = json.loads(raw)
    if contract['phase'] == 'probe-python':
        probe()
        return 0
    if contract['phase'] != 'test-python':
        raise RuntimeError('phase')
    return run_tests(contract, 3)


if __name__ == '__main__':
    try:
        sys.exit(main())
    except Exception:
        sys.stderr.write('owned_python_runner_failed\n')
        sys.exit(2)
