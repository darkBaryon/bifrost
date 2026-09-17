"""通过隔离的认证 fixture 验证真实管理预检与配置回写。"""
import json
import os
import socket
import sqlite3


def exercise_sensitive(node, admin, member, member_id, psql):
    role, _ = node.expect(201, '/api/roles/create', {'name': 'Sensitive fixture', 'permission_codes': []}, token=admin)
    role_id = role['role']['id']
    node.expect(200, '/api/accounts/set-roles', {'account_id': member_id, 'role_ids': [role_id]}, token=admin)

    def grant(*codes):
        node.expect(200, '/api/roles/update', {'role_id': role_id, 'name': 'Sensitive fixture', 'description': '', 'permission_codes': list(codes)}, token=admin)

    def stored(query):
        store = node.config['config_store']
        if store['type'] == 'sqlite':
            with sqlite3.connect(store['config']['path']) as db:
                return db.execute(query).fetchall()
        return psql(os.environ['RBAC_TEST_POSTGRES_DSN'] + ' dbname=' + store['config']['db_name'], query)

    # 原生插件操作被拒绝时，不得进入宿主提前建表分支。
    grant('Plugins.View', 'Plugins.Manage')
    before = stored("SELECT count(*) FROM config_plugins WHERE name='blocked-native'")
    for method, path, body in [
        ('POST', '/api/plugins', {'name': 'blocked-native', 'enabled': False, 'config': {}}),
        ('PUT', '/api/plugins/blocked-native', {'enabled': False, 'config': {}}),
        ('DELETE', '/api/plugins/blocked-native', None),
    ]:
        node.expect(403, path, body, method=method, token=member)
        assert stored("SELECT count(*) FROM config_plugins WHERE name='blocked-native'") == before
    for path in ('/api/plugins/builtins', '/api/plugins/loaded'):
        result, _ = node.expect(200, path, method='GET', token=member)
        assert isinstance(result['plugins'], list) and all(isinstance(name, str) for name in result['plugins'])
    before = stored("SELECT count(*) FROM config_plugins WHERE name='otel'")
    node.expect(403, '/api/plugins', {'name': 'otel', 'enabled': False, 'config': {}}, token=member)
    assert stored("SELECT count(*) FROM config_plugins WHERE name='otel'") == before

    # 权限拒绝必须早于数据库写入和运行时配置替换。
    grant('Settings.View', 'Settings.Manage')
    current, _ = node.expect(200, '/api/config', method='GET', token=member)
    desired = {'client_config': dict(current['client_config']), 'framework_config': current['framework_config']}
    desired['client_config']['disable_content_logging'] = not current['client_config']['disable_content_logging']
    before = stored('SELECT disable_content_logging, log_retention_days FROM config_client')
    node.expect(403, '/api/config', desired, method='PUT', token=member)
    assert stored('SELECT disable_content_logging, log_retention_days FROM config_client') == before
    after, _ = node.expect(200, '/api/config', method='GET', token=member)
    assert after['client_config'] == current['client_config']
    # 受保护字段未变更的 GET→PUT 仍是合法的 Settings.Manage 操作。
    unchanged = {'client_config': current['client_config'], 'framework_config': current['framework_config']}
    node.expect(200, '/api/config', unchanged, method='PUT', token=member)

    grant()
    node.expect(403, '/metrics', method='GET', token=member)
    grant('Logs.View')
    metrics, headers = node.expect(200, '/metrics', method='GET', token=member)
    assert headers.get_content_type() == 'text/plain' and isinstance(metrics, bytes)
    assert b'# HELP ' in metrics and b'# TYPE ' in metrics, 'Prometheus output was lost'
    grant()
    node.expect(403, '/metrics', method='GET', token=member)
    grant('Logs.View')
    for path in ('/api/logs?content_search=protected', '/api/logs?metadata_secret=protected', '/api/logs/dashboard?all=true', '/api/logs/rankings?all=true'):
        node.expect(403, path, method='GET', token=member)

    # VK 普通读取不暴露生成的值，覆盖内存读取与导出。
    grant('VirtualKeys.View', 'VirtualKeys.Manage')
    created, _ = node.expect(200, '/api/governance/virtual-keys', {'name': 'Sensitive VK'}, token=admin)
    vk = created['virtual_key']
    assert vk['value'] != '<redacted>'
    for path in ('/api/governance/virtual-keys', '/api/governance/virtual-keys?from_memory=true', '/api/governance/virtual-keys/' + vk['id']):
        result, _ = node.expect(200, path, method='GET', token=member)
        assert vk['value'] not in json.dumps(result), 'ordinary VK view revealed value'
    node.expect(403, '/api/governance/virtual-keys?export=true', method='GET', token=member)
    grant('VirtualKeys.RevealKey')
    node.expect(403, '/api/governance/virtual-keys/' + vk['id'], method='GET', token=member)
    grant('VirtualKeys.View', 'VirtualKeys.RevealKey')
    revealed, _ = node.expect(200, '/api/governance/virtual-keys/' + vk['id'], method='GET', token=member)
    assert vk['value'] in json.dumps(revealed)

    # 使用自有已绑定但未监听的套接字，使测试投递失败且不触达其他服务。
    grant('Notifications.View', 'Notifications.Manage')
    with socket.socket() as reserved:
        reserved.bind(('127.0.0.1', 0))
        secret = 'RBAC_SECRET_PROBE'
        body = {'name': 'Sensitive webhook', 'url': f'http://127.0.0.1:{reserved.getsockname()[1]}/hook?key={secret}', 'events': ['async_job.completed'], 'allow_private_network': True, 'include_response': True}
        before = stored("SELECT count(*) FROM config_webhook_endpoints WHERE name='Sensitive webhook'")
        node.expect(403, '/api/webhooks', body, token=member)
        assert stored("SELECT count(*) FROM config_webhook_endpoints WHERE name='Sensitive webhook'") == before
        body['include_response'] = False
        created, _ = node.expect(201, '/api/webhooks', body, token=member)
        endpoint = created['endpoint']
        assert endpoint['url'] == '<redacted>' and created['secret']
        safe, _ = node.expect(200, '/api/webhooks/' + endpoint['id'], method='GET', token=member)
        assert secret not in json.dumps(safe)
        node.expect(403, '/api/webhooks/' + endpoint['id'], dict(body, include_response=True), method='PUT', token=member)
        after, _ = node.expect(200, '/api/webhooks/' + endpoint['id'], method='GET', token=member)
        assert after == safe
        result, _ = node.expect(200, '/api/webhooks/' + endpoint['id'] + '/test', {}, token=member)
        assert result['delivered'] is False and secret not in json.dumps(result), 'synthetic delivery error revealed URL credential'
        node.expect(200, '/api/webhooks/' + endpoint['id'], dict(body, url='<redacted>'), method='PUT', token=member)
        assert stored("SELECT url FROM config_webhook_endpoints WHERE name='Sensitive webhook'") == ([(body['url'],)] if node.config['config_store']['type'] == 'sqlite' else body['url'])
    exercise_provider(node, admin, member, grant, stored)
    print('PASS: real sensitive guards, denied-write DB/runtime invariance, VK reveal separation, webhook error and URL writeback')


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
