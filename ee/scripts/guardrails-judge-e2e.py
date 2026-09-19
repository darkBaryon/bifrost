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
import traceback
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


def classify(status, body):
    """只有带非空正文的合法回答算放行、只有内容命中算拦截；其他一律记为验证失败，不能混入放行或拦截。"""
    if status == 200 and isinstance(body, dict):
        choices = body.get('choices') or []
        message = choices[0].get('message', {}) if choices and isinstance(choices[0], dict) else {}
        if isinstance(message.get('content'), str) and message['content'].strip():
            return 'pass', ''
        return 'failure', 'invalid_completion'
    code = body.get('error', {}).get('code', '') if isinstance(body, dict) else ''
    if status == 400 and code == 'content_safety_blocked':
        return 'block', code
    return 'failure', code or f'http_{status}'


def main():
    key = os.environ['DEEPSEEK_API_KEY']
    samples = json.loads((REPO / 'product/需求/内容安全/判官实验/samples.json').read_text())
    root = Path(tempfile.mkdtemp(prefix='ee-judge-e2e-'))
    model = ThreadingHTTPServer(('127.0.0.1', 0), guard_smoke.ModelHandler)
    model.calls = model.judge_calls = 0
    threading.Thread(target=model.serve_forever, daemon=True).start()
    base = f'http://127.0.0.1:{model.server_port}'
    password = secrets.token_urlsafe(24)
    cfg = guard_smoke.node_config(root, base, 'judge-e2e', password)
    # key 须列出判官模型，否则网关按"没有支持该模型的 key"拒绝判官请求。
    cfg['providers']['deepseek'] = {'keys': [{'name': 'real', 'value': key, 'weight': 1, 'models': ['deepseek-v4-flash']}], 'network_config': {'max_retries': 0}}
    node = identity_smoke.Node(str(REPO / 'ee/tmp/bifrost-http'), root / 'node', cfg, '')
    results = []
    error = None

    def persist():
        # 逐条结果与中途异常都落盘，异常中断也保留已完成部分；error 由 main 的 except 写入，闭包读到最新值。
        (root / 'results.json').write_text(json.dumps({'results': results, 'error': error}, ensure_ascii=False, indent=1))

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
                got, code = classify(status, body)
                results.append({'id': s['id'], 'group': s['group'], 'stage': stage, 'expect': s['expect'], 'got': got, 'code': code, 'ms': round(ms), 'status': status})
                persist()
                print(f"{s['id']:4} {stage:6} expect={s['expect']:5} got={got:8} {code:35} {ms:6.0f}ms", flush=True)
        node.expect(200, '/api/guardrails/reset')
    except Exception:  # noqa: BLE001 - 记录现场（含调用栈）后按失败退出；调用栈只在结尾打印一次
        error = traceback.format_exc()
    finally:
        node.stop()
        model.shutdown()
        (root / 'config.json').unlink(missing_ok=True)
        (root / 'node' / 'config.json').unlink(missing_ok=True)
        persist()
    ok = [r for r in results if r['got'] == r['expect']]
    failures = [r for r in results if r['got'] == 'failure']
    lat = sorted(r['ms'] for r in results)
    if lat:
        print(f"\nmatch {len(ok)}/{len(results)}; failures {len(failures)}; latency p50={statistics.median(lat):.0f}ms p90={lat[int(len(lat)*0.9)-1]}ms max={lat[-1]}ms")
    for r in results:
        if r['got'] != r['expect']:
            print('MISMATCH' if r['got'] != 'failure' else 'FAILURE', r)
    print('log:', root / 'node' / 'server.log')
    print('results:', root / 'results.json')
    if error:
        print('ABORTED', f'({len(results)} results kept)\n' + error, file=sys.stderr)
    if error or len(ok) != len(results) or failures:
        sys.exit(1)


if __name__ == '__main__':
    main()
