#!/usr/bin/env python3
"""通过真实 EE 网关 + 真实 DeepSeek 判官跑判官实验样本：记录拦截结果、延迟与判官日志。业务模型用本地替身（回显）。"""
import importlib.util
import json
import os
import secrets
import statistics
import sys
import tempfile
import threading
import time
from http.server import ThreadingHTTPServer
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


identity_smoke = load('identity_smoke', REPO / 'ee/scripts/identity-smoke.py')
guard_smoke = load('guard_smoke', REPO / 'ee/scripts/guardrails-smoke.py')
MODEL = guard_smoke.MODEL


def config(stage, rule):
    item = lambda: {'enabled': True, 'stages': [stage], 'threshold': 'medium', 'on_match': 'block', 'on_error': 'block'}
    c = {
        'deny': {'status': 400, 'message': '内容未通过安全检查'},
        'judge': {'provider': 'deepseek', 'model': 'deepseek-v4-flash', 'retries': 3, 'timeout_ms': 20000},
        'secrets': {'enabled': stage == 'input', 'stages': ['input'], 'threshold': 'medium', 'on_match': 'block', 'on_error': 'block'},
        'harmful': item(), 'prompt_attack': {**item(), 'enabled': stage == 'input'},
        'business_rules': [{'id': 'pricing', 'rule': rule, **item()}],
    }
    return c


def main():
    key = os.environ['DEEPSEEK_API_KEY']
    samples = json.loads((REPO / 'product/需求/内容安全/判官实验/samples.json').read_text())
    root = Path(tempfile.mkdtemp(prefix='ee-judge-e2e-'))
    model = ThreadingHTTPServer(('127.0.0.1', 0), guard_smoke.ModelHandler)
    model.calls = model.judge_calls = 0
    threading.Thread(target=model.serve_forever, daemon=True).start()
    base = f'http://127.0.0.1:{model.server_port}'
    password = secrets.token_urlsafe(24)
    cfg = {
        'client': {'enable_logging': False},
        'framework': {'pricing': {'pricing_url': base + '/pricing', 'model_parameters_url': base + '/parameters', 'mcp_library_url': base + '/mcp', 'mcp_library_sync_interval': 0}},
        'config_store': {'enabled': True, 'type': 'sqlite', 'config': {'path': str(root / 'config.db')}},
        'governance': {'auth_config': {'admin_username': 'judge-e2e', 'admin_password': password, 'is_enabled': True}},
        'providers': {
            'openai': {'keys': [{'name': 'local', 'value': 'sk-local-fake', 'weight': 1, 'models': [MODEL]}], 'network_config': {'base_url': base + '/v1', 'max_retries': 0}},
            'deepseek': {'keys': [{'name': 'real', 'value': key, 'weight': 1, 'models': ['deepseek-v4-flash']}], 'network_config': {'max_retries': 0}},
        },
    }
    node = identity_smoke.Node(str(REPO / 'ee/tmp/bifrost-http'), root / 'node', cfg, '')
    results = []
    try:
        node.start()
        node.login('judge-e2e', password)
        version = 0
        for stage in ['input', 'output']:
            state, _ = node.expect(200, '/api/guardrails/update', {'config': config(stage, samples['business_rule']), 'version': version})
            version = state['version']
            for s in [x for x in samples['samples'] if x['stage'] == stage]:
                start = time.perf_counter()
                status, body, _ = guard_smoke.completion(node, s['text'])
                ms = (time.perf_counter() - start) * 1000
                got = 'block' if status == 400 else 'pass'
                code = body.get('error', {}).get('code') if status != 200 else ''
                results.append({'id': s['id'], 'group': s['group'], 'stage': stage, 'expect': s['expect'], 'got': got, 'code': code or '', 'ms': round(ms), 'status': status})
                print(f"{s['id']:4} {stage:6} expect={s['expect']:5} got={got:5} {code:35} {ms:6.0f}ms", flush=True)
        node.expect(200, '/api/guardrails/reset')
    finally:
        node.stop()
        model.shutdown()
        (root / 'config.json').unlink(missing_ok=True)
        (root / 'node' / 'config.json').unlink(missing_ok=True)
    (root / 'results.json').write_text(json.dumps(results, ensure_ascii=False, indent=1))
    ok = [r for r in results if r['got'] == r['expect']]
    lat = sorted(r['ms'] for r in results)
    print(f"\nmatch {len(ok)}/{len(results)}; latency p50={statistics.median(lat):.0f}ms p90={lat[int(len(lat)*0.9)-1]}ms max={lat[-1]}ms")
    for r in results:
        if r['got'] != r['expect']:
            print('MISMATCH', r)
    print('log:', root / 'node' / 'server.log')
    print('results:', root / 'results.json')


if __name__ == '__main__':
    main()
