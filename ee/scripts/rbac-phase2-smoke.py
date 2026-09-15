#!/usr/bin/env python3
"""使用临时实例验证期二权限、敏感字段隐藏和厂商配置保存；不连接真实模型服务。"""
import argparse
import contextlib
import json
import os
from pathlib import Path
import runpy
import secrets
import shlex
import tempfile
import subprocess
import sqlite3
import uuid

auth = runpy.run_path(str(Path(__file__).with_name('identity-smoke.py')))
Node, psql = auth['Node'], auth['psql']


def exercise(nodes):
    a, b = nodes[0], nodes[-1]
    a.expect(201, '/api/identity/initialize', {'setup_token': a.setup, 'username': 'admin', 'password': 'Admin-password-1'})
    admin = a.login('admin', 'Admin-password-1')
    created, _ = a.expect(201, '/api/accounts/create', {'username': 'member'}, token=admin)
    member_id = created['account']['id']
    member = a.login('member', '123456')
    a.expect(200, '/api/identity/change-password', {'old_password': '123456', 'new_password': 'Member-password-1'}, token=member)
    member = a.login('member', 'Member-password-1')
    role, _ = a.expect(201, '/api/roles/create', {'name': 'phase2', 'permission_codes': []}, token=admin)
    role_id = role['role']['id']
    a.expect(200, '/api/accounts/set-roles', {'account_id': member_id, 'role_ids': [role_id]}, token=admin)

    def grant(*codes):
        a.expect(200, '/api/roles/update', {'role_id': role_id, 'name': 'phase2', 'description': '', 'permission_codes': list(codes)}, token=admin)

    def stored(query):
        store = a.config['config_store']
        if store['type'] == 'sqlite':
            with sqlite3.connect(store['config']['path']) as db:
                return db.execute(query).fetchall()
        return psql(os.environ['BIFROST_TEST_POSTGRES_DSN'] + ' dbname=' + store['config']['db_name'], query)

    # 在另一个节点请求，角色更新后下一次请求就必须看到新的权限。
    views = [
        ('ModelProvider.View', '/api/providers'),
        ('VirtualKeys.View', '/api/governance/virtual-keys'),
        ('Governance.View', '/api/governance/teams'),
        ('RoutingRules.View', '/api/routing/rules'),
        ('MCPGateway.View', '/api/mcp/clients'),
        ('PromptRepository.View', '/api/prompt-repo/folders'),
        ('PromptRepository.View', '/api/skills'),
    ]
    for permission, path in views:
        grant()
        b.expect(403, path, method='GET', token=member)
        grant(permission)
        b.expect(200, path, method='GET', token=member)
        b.expect(401, path, method='GET', anonymous=True)
    grant('ModelProvider.Manage')
    b.expect(403, '/api/providers', method='GET', token=member)
    grant('ModelProvider.View')
    b.expect(403, '/api/providers', {'provider': 'must-not-create'}, token=member)
    b.expect(403, '/api/providers/test', {}, method='PUT', token=member, headers={'Origin': 'https://evil.invalid'})

    created, _ = a.expect(200, '/api/governance/virtual-keys', {'name': 'phase2 secret'}, token=admin)
    vk = created['virtual_key']
    grant('VirtualKeys.View', 'VirtualKeys.Manage')
    for path in ('/api/governance/virtual-keys', '/api/governance/virtual-keys?from_memory=true', '/api/governance/virtual-keys/' + vk['id']):
        result, _ = a.expect(200, path, method='GET', token=member)
        assert vk['value'] not in json.dumps(result)
    b.expect(403, '/api/governance/virtual-keys?export=true', method='GET', token=member)
    grant('VirtualKeys.RevealKey')
    b.expect(403, '/api/governance/virtual-keys/' + vk['id'], method='GET', token=member)
    grant('VirtualKeys.View', 'VirtualKeys.RevealKey')
    result, _ = a.expect(200, '/api/governance/virtual-keys/' + vk['id'], method='GET', token=member)
    assert vk['value'] in json.dumps(result)
    grant('Logs.View', 'Logs.Manage', 'Plugins.View', 'Plugins.Manage', 'Settings.View', 'Settings.Manage', 'Notifications.View', 'Notifications.Manage')
    for path in ('/api/logs', '/api/plugins', '/api/config', '/api/webhooks', '/api/notifications', '/ws'):
        b.expect(403, path, method='GET', token=member)
    b.expect(403, '/api/identity/ws-ticket', {}, token=member)
    a.expect(200, '/api/config', method='GET', token=admin)
    grant('ModelProvider.View')
    for path, status in [('/api/no-such-route', 404), ('/api/Providers', 404), ('/api/providers/', 400)]:
        b.expect(status, path, method='GET', token=member)
    b.expect(405, '/api/roles/list', method='GET', token=member)
    exercise_provider(a, admin, member, grant, stored)
    grant('ModelProvider.View')
    b.expect(200, '/api/providers', method='GET', token=member)
    grant()
    b.expect(403, '/api/providers', method='GET', token=member)
    print('PASS: phase2 module access, cross-node revocation, VK reveal and unchanged phase3/4 boundaries')


def exercise_provider(node, admin, member, grant, stored):
    """使用本地 OpenAI 协议接收器检查实际 Provider 运行时。"""
    import http.server
    import threading
    requests = []

    class Receiver(http.server.BaseHTTPRequestHandler):
        def do_POST(self):
            self.rfile.read(int(self.headers.get('Content-Length', '0')))
            requests.append((self.path, dict(self.headers)))
            body = b'{"id":"fixture","object":"chat.completion","model":"test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}'
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        def log_message(self, *_):
            pass

    server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Receiver)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    url = f'http://127.0.0.1:{server.server_port}/receiver?api_key=PROVIDER_SECRET'
    path = '/api/providers/rbac-fixture'
    custom = {'base_provider_type': 'openai', 'is_key_less': True}
    network = {'base_url': url, 'allow_private_network': True, 'extra_headers': {'Authorization': 'AUTH_SECRET', 'X-Delete': 'old'}, 'default_request_timeout_in_seconds': 10}
    query = "SELECT network_config_json FROM config_providers WHERE name='rbac-fixture'"

    def persisted():
        raw = stored(query)
        return json.loads(raw[0][0] if isinstance(raw, list) else raw)

    def runtime(expected):
        before = len(requests)
        node.expect(200, '/v1/chat/completions', {'model': 'rbac-fixture/test', 'messages': [{'role': 'user', 'content': 'fixture'}]}, anonymous=True)
        assert len(requests) == before + 1
        target, headers = requests[-1]
        assert target.startswith('/receiver?api_key=PROVIDER_SECRET'), 'runtime URL was not restored'
        headers = {k.lower(): v for k, v in headers.items()}
        for key, value in expected.items():
            assert headers.get(key.lower()) == value, 'runtime header mismatch'
        for absent in {'Authorization', 'X-Delete', 'X-New'} - set(expected):
            assert absent.lower() not in headers, 'deleted header remains active'

    try:
        node.expect(200, '/api/providers', {'provider': 'rbac-fixture', 'custom_provider_config': custom, 'network_config': network, 'concurrency_and_buffer_size': {'concurrency': 1, 'buffer_size': 2}}, token=admin)
        grant('ModelProvider.View')
        visible, _ = node.expect(200, path, method='GET', token=member)
        assert 'AUTH_SECRET' not in json.dumps(visible) and 'PROVIDER_SECRET' not in json.dumps(visible)
        payload = {'network_config': visible['network_config'], 'custom_provider_config': custom, 'concurrency_and_buffer_size': visible['concurrency_and_buffer_size']}
        payload['network_config']['default_request_timeout_in_seconds'] = 11
        grant('ModelProvider.Manage')
        node.expect(200, path, payload, method='PUT', token=member)
        assert persisted()['base_url'] == url and persisted()['extra_headers'] == network['extra_headers']
        runtime(network['extra_headers'])
        for expected in ({'Authorization': 'REPLACED', 'X-New': 'new'}, {'X-New': 'new'}, {}):
            payload['network_config']['extra_headers'] = expected
            node.expect(200, path, payload, method='PUT', token=member)
            assert persisted()['base_url'] == url and persisted().get('extra_headers', {}) == expected
            runtime(expected)
        before, calls = persisted(), len(requests)
        payload['network_config']['extra_headers'] = {'Missing': '<redacted>'}
        node.expect(400, path, payload, method='PUT', token=member)
        assert persisted() == before and len(requests) == calls, 'invalid marker reached DB or network'
        runtime({})
        print('PASS: Provider GET/PUT restore, header replacement/deletion/clear, invalid-marker atomicity and real local inference')
    finally:
        server.shutdown()
        server.server_close()
        worker.join()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', required=True)
    parser.add_argument('--database', choices=('sqlite', 'postgres'), required=True)
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='bifrost-rbac-phase2-smoke-'))
    nodes, control, database = [], None, None
    try:
        if args.database == 'postgres':
            control = os.environ.get('BIFROST_TEST_POSTGRES_DSN')
            if not control:
                raise RuntimeError('BIFROST_TEST_POSTGRES_DSN is required; PostgreSQL validation cannot be skipped')
            fields = dict(part.split('=', 1) for part in shlex.split(control))
            database = 'rbac_smoke_' + uuid.uuid4().hex
            psql(control, 'CREATE DATABASE ' + database + " TEMPLATE template0 ENCODING 'UTF8'")
            store = {'enabled': True, 'type': 'postgres', 'config': {'host': fields['host'], 'port': fields.get('port', '5432'), 'user': fields['user'], 'password': fields.get('password', ''), 'db_name': database, 'ssl_mode': fields.get('sslmode', 'disable')}}
        else:
            store = {'enabled': True, 'type': 'sqlite', 'config': {'path': str(root/'config.db')}}
        config = {'providers': {}, 'client': {'enable_logging': True, 'log_retention_days': 7}, 'config_store': store}
        setup = secrets.token_urlsafe(32)
        nodes.append(Node(args.binary, root/'node1', config, setup))
        if args.database == 'postgres':
            nodes.append(Node(args.binary, root/'node2', config, setup))
        for node in nodes:
            node.start()
        exercise(nodes)
    finally:
        for node in nodes:
            node.stop()
        if database:
            psql(control, 'DROP DATABASE ' + database + ' WITH (FORCE)')
        print('Isolated evidence:', root)


if __name__ == '__main__':
    main()
