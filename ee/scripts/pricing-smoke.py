#!/usr/bin/env python3
"""用隔离 SQLite、随机端口和虚假密钥验证国内定价启动同步；只清理本脚本的进程。"""
import argparse
import importlib.util
import json
import math
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import tempfile
import time

spec = importlib.util.spec_from_file_location('identity_smoke', Path(__file__).with_name('identity-smoke.py'))
identity_smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(identity_smoke)

API = '/api/governance/pricing-overrides'
REQUEST_TYPES = ['chat_completion', 'responses', 'text_completion']


class PricingNode(identity_smoke.Node):
    """复用现有 HTTP/登录夹具，只在本子类注入定价环境变量。"""

    def __init__(self, binary, root):
        self.username = 'pricing-test'
        self.password = secrets.token_urlsafe(24)
        providers = {}
        for name, url in [('myqwen', 'https://dashscope.aliyuncs.com/compatible-mode/v1'),
                          ('mystery', 'https://example.invalid')]:
            providers[name] = {
                'keys': [{'name': 'k', 'value': 'sk-fake', 'weight': 1}],
                'network_config': {'base_url': url},
                'custom_provider_config': {
                    'base_provider_type': 'openai',
                    'allowed_requests': {'list_models': False},
                },
            }
        config = {
            'client': {'enable_logging': False},
            'governance': {'auth_config': {'admin_username': self.username,
                                         'admin_password': self.password, 'is_enabled': True}},
            'providers': providers,
        }
        super().__init__(binary, root, config, '')
        self.log_offset = 0

    def start(self, *, pricing_env=None):
        # 抄自 identity_smoke.Node.start：父类剥掉 EE_* 变量，方案 §8 禁止修改父脚本；
        # 父类就绪检查或环境隔离规则变化时需同步。此处额外注入定价配置并记录日志起点。
        env = {k: v for k, v in os.environ.items()
               if not k.startswith(('BIFROST_', 'EE_', 'OPENAI_', 'ANTHROPIC_', 'AWS_', 'AZURE_'))}
        env.update(EE_PUBLIC_ORIGIN=self.url, BIFROST_SETUP_TOKEN=self.setup)
        # 明确允许三项，避免调用方意外覆盖夹具的环境隔离设置。
        for key, value in (pricing_env or {}).items():
            if key not in ('EE_PRICING_FILE', 'EE_PRICING_USD_CNY', 'EE_PRICING_VENDOR_MAP'):
                raise ValueError(f'unsupported pricing environment: {key}')
            env[key] = value
        self.log = (self.root / 'server.log').open('ab')
        self.log_offset = self.log.tell()
        self.process = subprocess.Popen(
            [self.binary, '-host', '127.0.0.1', '-port', str(self.port),
             '-app-dir', str(self.root), '-log-style', 'json'],
            env=env, stdout=self.log, stderr=subprocess.STDOUT)
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                raise RuntimeError(f'EE exited during startup; isolated evidence: {self.root}')
            try:
                if self.call('/api/session/is-auth-enabled', method='GET', anonymous=True)[0] == 200:
                    return
            except OSError:
                pass
            time.sleep(.1)
        raise RuntimeError(f'EE startup timeout; isolated evidence: {self.root}')

    def login(self):
        return super().login(self.username, self.password)

    def current_log(self):
        # 每次只查本次启动区间，旧日志不能让重启断言假通过。
        return (self.root / 'server.log').read_bytes()[self.log_offset:].decode(errors='replace')

    def rows(self):
        value, _ = self.expect(200, API, method='GET')
        return value['pricing_overrides']


def price_file(path):
    models = [
        {'model': 'qwen-smoke', 'input_cost': 7.2, 'output_cost': 14.4, 'cache_read_input_cost': 3.6},
        {'model': 'qwen-smoke-alt', 'input_cost': 3.6, 'output_cost': 7.2},
    ]
    for model in models:
        model.update(currency='CNY', unit='per_million_tokens', checked_at='2026-09-13', note='隔离冒烟数据')
    data = {'version': 'smoke', 'pricing_rule': 'lowest-tier-standard-rate',
            'rates': {'CNY': 7.2, 'checked_at': '2026-09-13'},
            'vendors': [{'id': 'dashscope', 'name': '阿里百炼',
                         'endpoint_hosts': ['dashscope.aliyuncs.com'],
                         'price_page': 'https://example.invalid/prices', 'models': models}]}
    path.write_text(json.dumps(data), encoding='utf-8')
    return models


def check_rows(node, models, rate, manual=None):
    rows = node.rows()
    managed = [row for row in rows if row['name'].startswith('ee-pricing: ')]
    assert len(rows) == len(models) + (manual is not None), rows
    assert len(managed) == len(models), managed
    expected = {model['model']: model for model in models}
    assert {row['pattern'] for row in managed} == set(expected)
    assert not any(row.get('provider_id') == 'mystery' for row in rows)
    assert not any(row['pattern'] == 'not-in-price-file' for row in rows)
    for row in managed:
        assert row['provider_id'] == 'myqwen' and row['scope_kind'] == 'provider'
        assert row['match_type'] == 'exact' and set(row['request_types']) == set(REQUEST_TYPES)
        patch = json.loads(row['pricing_patch'])
        model = expected[row['pattern']]
        fields = {'input_cost': 'input_cost_per_token', 'output_cost': 'output_cost_per_token',
                  'cache_read_input_cost': 'cache_read_input_token_cost'}
        assert set(patch) == {target for source, target in fields.items() if source in model}
        for source, target in fields.items():
            if source in model:
                actual = patch[target]
                expected_value = model[source] / 1e6 / rate
                assert math.isclose(actual, expected_value, rel_tol=1e-12), (
                    f"model={model['model']} field={target} "
                    f"actual={actual!r} expected={expected_value!r}")
    if manual is not None:
        assert next(row for row in rows if row['id'] == manual['id']) == manual
    return {row['pattern']: row['id'] for row in managed}


def smoke(node):
    path = node.root / 'prices.json'
    models = price_file(path)
    env = {'EE_PRICING_FILE': str(path)}
    node.start(pricing_env=env)
    node.login()
    ids = check_rows(node, models, 7.2)
    assert 'created=2 updated=0 deleted=0 unchanged=0 unrecognized=[mystery]' in node.current_log()
    assert 'example.invalid' in node.current_log() and '未识别厂商 mystery' in node.current_log()
    value, _ = node.expect(201, API, {
        'name': 'manual-pricing-smoke', 'scope_kind': 'provider', 'provider_id': 'myqwen',
        'match_type': 'exact', 'pattern': 'qwen-smoke', 'request_types': REQUEST_TYPES,
        'patch': {'input_cost_per_token': 0.000003},
    })
    manual = value['pricing_override']
    # 两次完整重启改用等价根域点地址，共库行与 UUID、手工行金额和时间戳均须保留。
    for host in ('dashscope.aliyuncs.com.', 'DASHSCOPE.ALIYUNCS.COM.:443'):
        node.stop()
        node.config['providers']['myqwen']['network_config']['base_url'] = f'https://{host}/compatible-mode/v1'
        (node.root / 'config.json').write_text(json.dumps(node.config), encoding='utf-8')
        node.start(pricing_env=env)
        node.login()
        assert check_rows(node, models, 7.2, manual) == ids, host
        assert 'created=0 updated=0 deleted=0 unchanged=2 unrecognized=[mystery]' in node.current_log(), host
    print('PASS: provider scope, conversion, unknown provider, manual preservation and two idempotent restarts')
    print('PASS: trailing root dot with and without port preserves overrides across restarts')
    invalid = node.root / 'invalid.json'
    invalid.write_text('{invalid')
    node.stop()
    node.start(pricing_env={'EE_PRICING_FILE': str(invalid)})
    node.login()
    assert check_rows(node, models, 7.2, manual) == ids
    errors = []
    for line in node.current_log().splitlines():
        try:
            item = json.loads(line)
        except ValueError:
            continue
        if item.get('level') == 'error':
            errors.append(item)
    assert any('pricing: load failed; synchronization skipped' in json.dumps(item) for item in errors)
    print('PASS: invalid price file logs error, keeps overrides and allows startup')
    node.stop()
    node.start(pricing_env={**env, 'EE_PRICING_USD_CNY': '3.6'})
    node.login()
    assert check_rows(node, models, 3.6, manual) == ids
    # 两条模型都为非零价，汇率减半后均在原 ID 上更新。
    assert 'created=0 updated=2 deleted=0 unchanged=0 unrecognized=[mystery]' in node.current_log()
    print('PASS: rate change doubles nonzero patch on the same row without duplicates')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True)
    args = parser.parse_args()
    directory = Path(tempfile.mkdtemp(prefix='ee-pricing-smoke-'))
    node = None
    try:
        try:
            node = PricingNode(args.binary, directory)
            smoke(node)
        finally:
            if node is not None:
                node.stop()
    except BaseException:
        # 清理进程后仍保留完整配置、价格文件、数据库与各次启动日志；日志缺失不掩盖原异常。
        print(f'FAIL: isolated evidence retained at {directory}', file=sys.stderr, flush=True)
        raise
    else:
        shutil.rmtree(directory)
    print('PASS: domestic pricing smoke')


if __name__ == '__main__':
    main()
