#!/usr/bin/env python3
"""验证定价冒烟的失败现场、进程回收与金额断言，不调用真实模型服务。"""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('pricing_smoke', Path(__file__).with_name('pricing-smoke.py'))
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class SmokeFailureTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='pricing-smoke-regression-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / 'evidence'
        self.root.mkdir()
        self.stderr = io.StringIO()
        self.nodes = []

    def run_main(self, action, binary='/usr/bin/false'):
        # 本组只测进程/文件生命周期；端口与 HTTP 就绪由真实 smoke 覆盖。
        with patch.object(smoke.identity_smoke.socket, 'socket') as socket_mock, \
                patch.object(smoke.PricingNode, 'call', return_value=(503, None, None)), \
                patch.object(sys, 'argv', ['pricing-smoke.py', '--binary', binary]), \
                patch.object(smoke.tempfile, 'mkdtemp', return_value=str(self.root)), \
                patch.object(smoke, 'smoke', side_effect=action), \
                contextlib.redirect_stderr(self.stderr), contextlib.redirect_stdout(io.StringIO()):
            socket_mock.return_value.__enter__.return_value.getsockname.return_value = ('127.0.0.1', 0)
            smoke.main()

    def check_retained(self):
        self.assertTrue(self.root.is_dir())
        self.assertIn(str(self.root), self.stderr.getvalue())
        for node in self.nodes:
            if node.process is not None:
                self.assertIsNotNone(node.process.poll(), 'own child still running')
            self.assertIsNone(node.log, 'log handle still open')

    def test_startup_failure_retains_inputs_and_log(self):
        def action(node):
            self.nodes.append(node)
            smoke.price_file(self.root / 'prices.json')
            node.start()
        with self.assertRaisesRegex(RuntimeError, 'exited during startup'):
            self.run_main(action)
        self.check_retained()
        for name in ('config.json', 'prices.json', 'server.log'):
            self.assertTrue((self.root / name).exists(), name)

    def start_child(self, node):
        self.nodes.append(node)
        node.log = (self.root / 'server.log').open('ab')
        node.log.write(b'earlier startup\n' + b'x' * 16000 + b'\nlatest startup\n')
        node.log.flush()
        node.process = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)'],
                                        stdout=node.log, stderr=subprocess.STDOUT)
        self.addCleanup(node.stop)
        self.assertIsNone(node.process.poll())
        # 真实 SQLite 文件用于证明进程回收后仍保留完整现场。
        import sqlite3
        with sqlite3.connect(self.root / 'config.db') as db:
            db.execute('create table evidence (value text)')
            db.execute("insert into evidence values ('retained')")

    def test_assertion_failure_stops_child_and_retains_full_evidence(self):
        def action(node):
            self.start_child(node)
            raise AssertionError('injected amount mismatch')
        with self.assertRaisesRegex(AssertionError, 'injected amount mismatch'):
            self.run_main(action)
        self.check_retained()
        self.assertTrue((self.root / 'config.db').exists())
        log = (self.root / 'server.log').read_bytes()
        self.assertTrue(log.startswith(b'earlier startup\n'))
        self.assertTrue(log.endswith(b'latest startup\n'))
        self.assertGreater(len(log), 16000)

    def test_success_stops_child_and_removes_directory(self):
        self.run_main(self.start_child)
        self.assertFalse(self.root.exists())
        self.assertIsNotNone(self.nodes[0].process.poll())
        self.assertIsNone(self.nodes[0].log)

    def test_missing_binary_keeps_original_error_and_evidence(self):
        with self.assertRaises(FileNotFoundError):
            self.run_main(lambda node: (self.nodes.append(node), node.start()),
                          binary=str(self.root / 'missing-binary'))
        self.check_retained()

    def test_constructor_failure_without_log_preserves_original_error(self):
        with patch.object(smoke, 'PricingNode', side_effect=ValueError('invalid fixture')):
            with self.assertRaisesRegex(ValueError, 'invalid fixture'):
                self.run_main(lambda node: None)
        self.check_retained()
        self.assertFalse((self.root / 'server.log').exists())

    def test_amount_failure_identifies_model_field_actual_and_expected(self):
        rows = [{'name': 'ee-pricing: vendor/model', 'pattern': 'model',
                 'provider_id': 'myqwen', 'scope_kind': 'provider', 'match_type': 'exact',
                 'request_types': smoke.REQUEST_TYPES,
                 'pricing_patch': json.dumps({'input_cost_per_token': 99, 'output_cost_per_token': 2})}]
        class RowsNode:
            def rows(self):
                return rows
        with self.assertRaises(AssertionError) as caught:
            smoke.check_rows(RowsNode(), [{'model': 'model', 'input_cost': 7.2, 'output_cost': 14.4}], 7.2)
        message = str(caught.exception)
        for expected in ('model=model', 'field=input_cost_per_token', 'actual=99', 'expected=1e-06'):
            self.assertIn(expected, message)


if __name__ == '__main__':
    unittest.main()
