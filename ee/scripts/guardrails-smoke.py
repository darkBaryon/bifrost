#!/usr/bin/env python3
"""以真实 EE HTTP 服务和本地模型替身验证内容安全：配置接口、密钥拦截、判官拦截、热更新与重启加载，以及开发假检测器；--serve 保留现场供浏览器联调。"""
import argparse
import importlib.util
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import secrets
import tempfile
import threading
import time

spec = importlib.util.spec_from_file_location('identity_smoke', Path(__file__).with_name('identity-smoke.py'))
identity_smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(identity_smoke)

MARKER = '[guardrails-test]'
# 判官替身：系统提示词含此字样即视为判官调用（与 detectors/judge/prompts/*.md 首句耦合），待审文本含 JUDGE_HIGH 时返回 high，否则 none。
JUDGE_PROMPT_HINT = '审核器'
JUDGE_HIGH = '[judge-high]'
MODEL = 'guardrails-test-model'
MODES = ['input-block', 'input-observe', 'output-block']


class ModelHandler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def send_json(self, value):
        data = json.dumps(value).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path.endswith('/models'):
            self.send_json({'object': 'list', 'data': [{'id': MODEL, 'object': 'model', 'owned_by': 'openai'}]})
        else:
            self.send_json({})

    def do_POST(self):
        request = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        common = {'id': 'chatcmpl-fake', 'created': 1, 'model': MODEL}
        messages = request['messages']
        if messages[0]['role'] == 'system' and JUDGE_PROMPT_HINT in messages[0]['content']:
            self.server.judge_calls += 1
            level = 'high' if JUDGE_HIGH in messages[-1]['content'] else 'none'
            self.send_json({**common, 'object': 'chat.completion',
                            'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': json.dumps({'risk_level': level, 'reason': 'fake'})}, 'finish_reason': 'stop'}],
                            'usage': {'prompt_tokens': 5, 'completion_tokens': 5, 'total_tokens': 10}})
            return
        self.server.calls += 1
        text = '本地模型替身：' + messages[-1]['content']
        if request.get('stream'):
            chunks = [
                {**common, 'object': 'chat.completion.chunk', 'choices': [{'index': 0, 'delta': {'role': 'assistant', 'content': text}, 'finish_reason': None}]},
                {**common, 'object': 'chat.completion.chunk', 'choices': [{'index': 0, 'delta': {}, 'finish_reason': 'stop'}]},
            ]
            data = ''.join('data: ' + json.dumps(c) + '\n\n' for c in chunks) + 'data: [DONE]\n\n'
            data = data.encode()
            self.send_response(200)
            self.send_header('Content-Type', 'text/event-stream')
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            self.wfile.write(data)
        else:
            self.send_json({**common, 'object': 'chat.completion',
                            'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': text}, 'finish_reason': 'stop'}],
                            'usage': {'prompt_tokens': 1, 'completion_tokens': 1, 'total_tokens': 2}})


def completion(node, text, stream=False):
    return node.call('/v1/chat/completions', {
        'model': 'openai/' + MODEL, 'messages': [{'role': 'user', 'content': text}], 'stream': stream,
    })


def check_response(node, model, text, *, code=None, calls=1, stream=False, judge_calls=None):
    before, judge_before = model.calls, model.judge_calls
    status, body, _ = completion(node, text, stream)
    expected_status = 400 if code else 200
    assert status == expected_status, (status, body)
    assert model.calls - before == calls, ('unexpected model calls', model.calls - before, calls)
    if judge_calls is not None:
        assert model.judge_calls - judge_before == judge_calls, ('unexpected judge calls', model.judge_calls - judge_before, judge_calls)
    if code:
        assert body['error']['code'] == code, body
        assert 'choices' not in body, body
    elif stream:
        assert b'[DONE]' in body, body
    else:
        assert body['choices'][0]['message']['content'] == '本地模型替身：' + text, body


def node_config(root, base, username, password):
    """隔离 EE 实例的配置：临时 SQLite、本地替身模型；判官 e2e 脚本在此基础上追加真实 provider。"""
    return {
        'client': {'enable_logging': False},
        'framework': {'pricing': {'pricing_url': base + '/pricing', 'model_parameters_url': base + '/parameters',
                                  'mcp_library_url': base + '/mcp', 'mcp_library_sync_interval': 0}},
        'config_store': {'enabled': True, 'type': 'sqlite', 'config': {'path': str(root / 'config.db')}},
        'governance': {'auth_config': {'admin_username': username, 'admin_password': password, 'is_enabled': True}},
        'providers': {'openai': {'keys': [{'name': 'local-test', 'value': 'sk-local-fake', 'weight': 1, 'models': [MODEL]}],
                                 'network_config': {'base_url': base + '/v1', 'max_retries': 0}}},
    }


def login_if_needed(node, password):
    """会话存在库里，重启后仍有效；只在会话失效时重新登录，避免触发登录频率限制。"""
    if node.call('/api/guardrails/get')[0] != 200:
        node.login('guardrails-dev', password)


def guardrails_config(*, harmful_output=True):
    return {
        'deny': {'status': 400, 'message': '内容未通过安全检查'},
        'judge': {'provider': 'openai', 'model': MODEL, 'retries': 3, 'timeout_ms': 5000},
        'secrets': {'enabled': True, 'stages': ['input'], 'threshold': 'medium', 'on_match': 'block', 'on_error': 'block'},
        'harmful': {'enabled': True, 'stages': ['input', 'output'] if harmful_output else ['input'], 'threshold': 'medium', 'on_match': 'block', 'on_error': 'block'},
        'prompt_attack': {'enabled': True, 'stages': ['input'], 'threshold': 'medium', 'on_match': 'block', 'on_error': 'block'},
        'business_rules': [{'id': 'pricing', 'rule': '不得透露内部底价', 'enabled': True, 'stages': ['input'], 'threshold': 'medium', 'on_match': 'observe', 'on_error': 'block'}],
    }


FAKE_ACCESS_KEY = 'LTAI' + secrets.token_hex(10)


def check_production(node, model, password):
    """无假检测器时的完整链路：接口读写、密钥与判官拦截、热更新、版本冲突、重置、重启加载。"""
    node.start()
    login_if_needed(node, password)
    state, _ = node.expect(200, '/api/guardrails/get')
    assert state == {'config': None, 'version': 0}, state
    check_response(node, model, '普通测试消息', judge_calls=0)
    state, _ = node.expect(200, '/api/guardrails/update', {'config': guardrails_config(), 'version': 0})
    assert state['version'] == 1 and state['config']['judge']['model'] == MODEL, state
    # 输入阶段：密钥（本地）与三路判官并行；有输出规则时非流式回答再过一次判官。
    check_response(node, model, '普通测试消息', judge_calls=4)
    check_response(node, model, '我的阿里云 AccessKey 是 ' + FAKE_ACCESS_KEY, code='content_safety_blocked', calls=0)
    check_response(node, model, JUDGE_HIGH + ' 危险内容', code='content_safety_blocked', calls=0)
    check_response(node, model, '普通测试消息', stream=True, code='content_safety_output_stream_unsupported', calls=0, judge_calls=0)
    node.expect(409, '/api/guardrails/update', {'config': guardrails_config(), 'version': 0})
    custom = guardrails_config(harmful_output=False)
    custom['deny'] = {'status': 451, 'message': '自定义拒绝文案'}
    state, _ = node.expect(200, '/api/guardrails/update', {'config': custom, 'version': 1})
    assert state['version'] == 2, state
    check_response(node, model, '普通测试消息', stream=True, judge_calls=3)
    # 拒绝策略随配置热更新，不需要重启。
    status, body, _ = completion(node, JUDGE_HIGH + ' 危险内容')
    assert status == 451 and body['error']['message'] == '自定义拒绝文案', (status, body)
    node.expect(400, '/api/guardrails/update', {'config': {**guardrails_config(), 'judge': {'provider': 'missing', 'model': MODEL}}, 'version': 2})
    node.stop()
    # 重启后从库加载版本 2 的配置。
    node.start()
    login_if_needed(node, password)
    assert 'configuration version 2 loaded' in (node.root / 'server.log').read_text()
    status, body, _ = completion(node, JUDGE_HIGH + ' 危险内容')
    assert status == 451 and body['error']['code'] == 'content_safety_blocked', (status, body)
    state, _ = node.expect(200, '/api/guardrails/reset')
    assert state == {'config': None, 'version': 0}, state
    check_response(node, model, JUDGE_HIGH + ' 现在不再检测', judge_calls=0)
    node.stop()
    print('PASS production', flush=True)


def check_fake_conflict(node, model, password):
    """库中有配置时，开发假检测器模式必须拒绝启动。"""
    node.start()
    login_if_needed(node, password)
    node.expect(200, '/api/guardrails/update', {'config': guardrails_config(), 'version': 0})
    node.stop()
    try:
        node.start(extra_env={'EE_GUARDRAILS_FAKE': 'input-block'})
    except RuntimeError:
        assert 'cannot be combined with stored guardrails configuration' in (node.root / 'server.log').read_text()
    else:
        raise AssertionError('fake mode started with stored configuration')
    node.stop()
    node.start()
    login_if_needed(node, password)
    node.expect(200, '/api/guardrails/reset')
    node.stop()
    print('PASS fake-conflict', flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', default='ee/tmp/bifrost-http')
    parser.add_argument('--serve', choices=MODES)
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='ee-guardrails-'))
    model = ThreadingHTTPServer(('127.0.0.1', 0), ModelHandler)
    model.calls = 0
    model.judge_calls = 0
    threading.Thread(target=model.serve_forever, daemon=True).start()
    base = f'http://127.0.0.1:{model.server_port}'
    password = secrets.token_urlsafe(24)
    node = identity_smoke.Node(args.binary, root / 'node', node_config(root, base, 'guardrails-dev', password), '')
    try:
        if args.serve:
            node.start(extra_env={'EE_GUARDRAILS_FAKE': args.serve})
            node.login('guardrails-dev', password)
            # 仅用于浏览器在本机隔离环境登录；文件不进入仓库。
            session = next(c.value for c in node.cookies if c.name == 'ee_session')
            (root / 'browser.json').write_text(json.dumps({'url': node.url, 'username': 'guardrails-dev',
                                                          'password': password, 'session': session}))
            (root / 'browser.json').chmod(0o600)
            print(f'SERVING {node.url} mode={args.serve} evidence={root}', flush=True)
            while True:
                time.sleep(1)
        else:
            check_production(node, model, password)
            check_fake_conflict(node, model, password)
            for mode in MODES:
                node.start(extra_env={'EE_GUARDRAILS_FAKE': mode})
                login_if_needed(node, password)
                check_response(node, model, '普通测试消息')
                blocked = mode in ['input-block', 'output-block']
                check_response(node, model, MARKER, code='content_safety_blocked' if blocked else None,
                               calls=0 if mode == 'input-block' else 1)
                if mode == 'output-block':
                    check_response(node, model, '普通测试消息', stream=True,
                                   code='content_safety_output_stream_unsupported', calls=0)
                else:
                    check_response(node, model, '普通测试消息', stream=True)
                if mode == 'input-block':
                    check_response(node, model, MARKER, stream=True, code='content_safety_blocked', calls=0)
                if mode == 'input-observe':
                    assert 'action=observe' in (node.root / 'server.log').read_text()
                node.stop()
                print('PASS', mode, flush=True)
    finally:
        node.stop()
        model.shutdown()
        model.server_close()
        print('Evidence:', root, flush=True)


if __name__ == '__main__':
    try:
        main()
    except KeyboardInterrupt:
        pass
