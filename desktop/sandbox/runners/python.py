"""Image-owned unittest adapter. Same-interpreter Python is not anti-forgery
isolation against hostile code with identical process privileges."""
# Import stdlib/unittest before adding any admitted project/dependency roots.
import importlib.util
import json
import os
import sys
import tempfile
import unittest


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
    def __init__(self, report):
        super().__init__()
        self.report, self.discovery, self.active, self.status = report, 0, None, None
        self.infrastructure = False

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

    def addError(self, test, err):
        self.discovery += 1
        self.infrastructure = True

    def addSkip(self, test, reason):
        if self.active is test:
            self.status = 'skipped'
        elif self.stage == 'test' and self.active is not None and self.status is None:
            self.status = 'skipped'

    def addExpectedFailure(self, test, err):
        self.status = 'expected_failed'

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
        else:
            self.addError(test, err)


def run_tests(contract, fd):
    report = Report(fd)
    root = contract['source_root']
    # The static parent has already admitted and frozen these exact roots.
    sys.path[:0] = [os.path.join(root, p) for p in contract['source_roots']] + ['/foundry-deps/python']
    result = Result(report)
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
