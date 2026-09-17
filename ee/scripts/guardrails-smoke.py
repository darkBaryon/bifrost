#!/usr/bin/env python3
"""以真实 EE HTTP 服务和本地模型替身验证开发护栏；--serve 保留现场供浏览器联调。"""
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
        self.server.calls += 1
        text = '本地模型替身：' + request['messages'][-1]['content']
        common = {'id': 'chatcmpl-fake', 'created': 1, 'model': MODEL}
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


def check_response(node, model, text, *, code=None, calls=1, stream=False):
    before = model.calls
    status, body, _ = completion(node, text, stream)
    expected_status = 400 if code else 200
    assert status == expected_status, (status, body)
    assert model.calls - before == calls, ('unexpected model calls', model.calls - before, calls)
    if code:
        assert body['error']['code'] == code, body
        assert 'choices' not in body, body
    elif stream:
        assert b'[DONE]' in body, body
    else:
        assert body['choices'][0]['message']['content'] == '本地模型替身：' + text, body


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', default='ee/tmp/bifrost-http')
    parser.add_argument('--serve', choices=MODES)
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='ee-guardrails-'))
    model = ThreadingHTTPServer(('127.0.0.1', 0), ModelHandler)
    model.calls = 0
    threading.Thread(target=model.serve_forever, daemon=True).start()
    base = f'http://127.0.0.1:{model.server_port}'
    password = secrets.token_urlsafe(24)
    config = {
        'client': {'enable_logging': False},
        'framework': {'pricing': {'pricing_url': base + '/pricing', 'model_parameters_url': base + '/parameters',
                                  'mcp_library_url': base + '/mcp', 'mcp_library_sync_interval': 0}},
        'config_store': {'enabled': True, 'type': 'sqlite', 'config': {'path': str(root / 'config.db')}},
        'governance': {'auth_config': {'admin_username': 'guardrails-dev', 'admin_password': password, 'is_enabled': True}},
        'providers': {'openai': {'keys': [{'name': 'local-test', 'value': 'sk-local-fake', 'weight': 1, 'models': [MODEL]}],
                                 'network_config': {'base_url': base + '/v1', 'max_retries': 0}}},
    }
    node = identity_smoke.Node(args.binary, root / 'node', config, '')
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
            for mode in ['', *MODES, '']:
                node.start(extra_env={'EE_GUARDRAILS_FAKE': mode})
                node.login('guardrails-dev', password)
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
                print('PASS', mode or 'disabled', flush=True)
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
