#!/usr/bin/env python3
"""复用认证冒烟的传输辅助，在隔离存储上验证真实 RBAC HTTP/WS。"""
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
sensitive = runpy.run_path(str(Path(__file__).with_name('rbac-sensitive-smoke.py')))['exercise_sensitive']


def chief_has_no_permission_rows(node):
    # chief 权限只在读取时展开；新迁移、更新及升级回退均不得写入派生关系。
    query = 'SELECT COUNT(*) FROM ee_rbac_role_permissions WHERE role_id=1'
    store = node.config['config_store']
    if store['type'] == 'sqlite':
        with sqlite3.connect(store['config']['path']) as db:
            count = db.execute(query).fetchone()[0]
    else:
        count = int(psql(os.environ['RBAC_TEST_POSTGRES_DSN'] + ' dbname=' + store['config']['db_name'], query))
    assert count == 0, 'chief derived permission rows persisted'


def exercise(nodes):
    a, b = nodes[0], nodes[-1]
    a.expect(201, '/api/identity/initialize', {'setup_token': a.setup, 'username': 'admin', 'password': 'Admin-password-1'})
    admin = a.login('admin', 'Admin-password-1')
    permissions, _ = a.expect(200, '/api/permissions/list', {}, token=admin)
    assert sum(len(m['permissions']) for m in permissions['modules']) == 29
    me, _ = a.expect(200, '/api/permissions/me', {}, token=admin)
    assert len(me['permissions']) == 29 and len(me['roles']) == 1
    chief_has_no_permission_rows(a)
    chief, _ = a.expect(200, '/api/roles/get', {'role_id': 1}, token=admin)
    role = chief['role']
    updated, _ = a.expect(200, '/api/roles/update', {'role_id': 1, 'name': role['name'], 'description': 'updated chief', 'permission_codes': role['permission_codes']}, token=admin)
    assert len(updated['role']['permission_codes']) == 29
    chief_has_no_permission_rows(a)
    own_id = me['account_id']
    a.expect(403, '/api/accounts/set-roles', {'account_id': own_id, 'role_ids': []}, token=admin)
    for raw in (b'{"role_id":"1"}', b'{"role_id":1.0}', b'{"role_id":1e0}', b'{"role_id":null}', b'{"role_id":1,"role_id":2}', b'{"role_id":9007199254740992}'):
        a.expect(400, '/api/roles/get', raw=raw, token=admin)
    account, _ = a.expect(201, '/api/accounts/create', {'username': 'member', 'display_name': 'Member'}, token=admin)
    member_id = account['account']['id']
    member = a.login('member', '123456')
    a.expect(403, '/api/permissions/me', {}, token=member)
    a.expect(200, '/api/identity/change-password', {'old_password': '123456', 'new_password': 'Member-password-1'}, token=member)
    member = a.login('member', 'Member-password-1')
    empty, _ = b.expect(200, '/api/permissions/me', {}, token=member)
    assert empty['roles'] == [] and empty['permissions'] == []
    b.expect(403, '/api/roles/get', {'role_id': 999999}, token=member)
    reader, _ = a.expect(201, '/api/roles/create', {'name': ' reader ', 'permission_codes': ['Users.View', 'Notifications.View', 'Users.View']}, token=admin)
    role = reader['role']
    assert role['name'] == 'reader' and len(role['permission_codes']) == 2
    a.expect(200, '/api/accounts/set-roles', {'account_id': member_id, 'role_ids': [role['id'], role['id']]}, token=admin)
    b.expect(200, '/api/accounts/list', {}, token=member)
    b.expect(403, '/api/accounts/create', {'username': 'denied'}, token=member)
    conflict, _ = a.expect(409, '/api/roles/delete', {'role_id': role['id']}, token=admin)
    assert conflict['error']['details']['account_count'] == 1
    stream_role, _ = a.expect(201, '/api/roles/create', {'name': 'Stream reader', 'permission_codes': ['Notifications.View']}, token=admin)
    stream_id = stream_role['role']['id']
    a.expect(200, '/api/accounts/set-roles', {'account_id': member_id, 'role_ids': [role['id'], stream_id]}, token=admin)
    ticket, _ = b.expect(200, '/api/identity/ws-ticket', {}, token=member)
    with contextlib.closing(auth['websocket'](b, ticket['ticket'])) as ws:
        notification = {'title': 'rbac-visible', 'message': 'role audience', 'severity': 'info', 'audience': 'roles', 'role_ids': [role['id']]}
        b.expect(201, '/api/notifications', notification, token=admin)
        message = auth['frame'](ws)
        assert message and b'rbac-visible' in message[1]
        a.expect(200, '/api/accounts/set-roles', {'account_id': member_id, 'role_ids': [stream_id]}, token=admin)
        b.expect(201, '/api/notifications', notification, token=admin)
        visible, _ = b.expect(200, '/api/notifications', method='GET', token=member)
        assert 'rbac-visible' not in json.dumps(visible), 'lost audience remained visible over HTTP'
        b.expect(201, '/api/notifications', {'title': 'still-connected', 'message': 'all audience', 'severity': 'info', 'audience': 'all'}, token=admin)
        message = auth['frame'](ws)
        assert message and b'still-connected' in message[1] and b'rbac-visible' not in message[1], 'audience loss closed WS or leaked a role notification'
        a.expect(200, '/api/roles/update', {'role_id': stream_id, 'name': 'Stream reader', 'description': '', 'permission_codes': []}, token=admin)
        b.expect(403, '/api/accounts/list', {}, token=member)
        b.expect(403, '/api/identity/ws-ticket', {}, token=member)
        b.expect(201, '/api/notifications', notification, token=admin)
        message = auth['frame'](ws)
        assert message is None or message[0] == 8, 'revoked websocket received a notification'
    a.expect(404, '/api/notifications', {'title': 'unknown', 'message': 'unknown role', 'severity': 'info', 'audience': 'roles', 'role_ids': [999999]}, token=admin)
    a.expect(200, '/api/accounts/set-roles', {'account_id': member_id, 'role_ids': []}, token=admin)
    a.expect(200, '/api/roles/delete', {'role_id': role['id']}, token=admin)
    a.expect(409, '/api/roles/delete', {'role_id': 1}, token=admin)
    a.expect(404, '/api/not-registered', {}, token=admin)
    a.expect(405, '/api/roles/list', method='GET', token=admin)
    sensitive(a, admin, member, member_id, psql)
    print('RBAC HTTP/WS passed: strict API, current permissions, assignment protection, audience and revocation')


def conditional_profiles(binary, root):
    # 上游在包初始化时探测 git，因此必须使用独立进程。
    for enabled in (False, True):
        directory = root / ('profile-on' if enabled else 'profile-off')
        config = {'providers': {}, 'client': {'enable_logging': enabled, 'log_retention_days': 7},
                  'plugins': [{'name': 'telemetry', 'enabled': True, 'config': {'metrics_enabled': enabled}}],
                  'config_store': {'enabled': True, 'type': 'sqlite', 'config': {'path': str(directory/'config.db')}}}
        node = Node(binary, directory, config, secrets.token_urlsafe(32))
        original_path = os.environ.get('PATH', '')
        try:
            if not enabled:
                os.environ['PATH'] = str(directory/'no-executables')
            node.start()
            os.environ['PATH'] = original_path
            node.expect(401 if enabled else 404, '/api/logs', method='GET', anonymous=True)
            node.expect(200 if enabled else 404, '/api/skills/serve/codex/.agents/plugins/marketplace.json', method='GET', anonymous=True)
            node.expect(404, '/api/dev/pprof', method='GET', anonymous=True)
            node.expect(201, '/api/identity/initialize', {'setup_token': node.setup, 'username': 'admin', 'password': 'Admin-password-1'})
            admin = node.login('admin', 'Admin-password-1')
            node.expect(200 if enabled else 404, '/metrics', method='GET', token=admin)
            print(f'PASS: actual startup profile logging={enabled}, git={enabled}; absent routes remain 404')
        finally:
            os.environ['PATH'] = original_path
            node.stop()


def switch(nodes, binary):
    for node in nodes:
        node.stop()
        node.binary = str(Path(binary).resolve())
    for node in nodes:
        node.start()


def recover(node, binary, password):
    node.stop()
    result = subprocess.run([str(Path(binary).resolve()), 'identity', 'recover-admin', '--app-dir', str(node.root)],
                            input=(password+'\n').encode(), capture_output=True, timeout=40)
    assert result.returncode == 0, 'offline recovery failed'


def upgrade(nodes, baseline, candidate):
    a, b = nodes[0], nodes[-1]
    a.expect(201, '/api/identity/initialize', {'setup_token': a.setup, 'username': 'admin', 'password': 'Admin-password-1'})
    old_admin = a.login('admin', 'Admin-password-1')
    original, _ = a.expect(200, '/api/identity/me', {}, token=old_admin)
    anchor = original['account']['id']
    a.expect(405, '/api/roles/list', {}, token=old_admin)
    member, _ = a.expect(201, '/api/accounts/create', {'username': 'successor'}, token=old_admin)
    successor_id = member['account']['id']
    successor = a.login('successor', '123456')
    a.expect(200, '/api/identity/change-password', {'old_password': '123456', 'new_password': 'Successor-password-1'}, token=successor)
    successor = a.login('successor', 'Successor-password-1')
    switch(nodes, candidate)
    chief_has_no_permission_rows(a)
    a.expect(200, '/api/permissions/list', {}, token=old_admin)
    a.expect(200, '/api/accounts/set-roles', {'account_id': successor_id, 'role_ids': [1]}, token=old_admin)
    b.expect(200, '/api/accounts/set-roles', {'account_id': anchor, 'role_ids': [3]}, token=successor)
    b.expect(200, '/api/accounts/set-status', {'account_id': anchor, 'status': 'disabled'}, token=successor)
    b.expect(200, '/api/roles/update', {'role_id': 2, 'name': 'Edited developer', 'description': 'persisted', 'permission_codes': ['Users.View']}, token=successor)
    switch(nodes, candidate)
    chief_has_no_permission_rows(a)
    b.expect(401, '/api/permissions/me', {}, token=old_admin)
    role, _ = b.expect(200, '/api/roles/get', {'role_id': 2}, token=successor)
    assert role['role']['permission_codes'] == ['Users.View'], 'restart reseeded role'
    # 切换授权语义并运行旧恢复命令前，先停止所有节点。
    for node in nodes:
        node.stop()
    recover(a, baseline, 'Rollback-password-1')
    switch(nodes, baseline)
    chief_has_no_permission_rows(a)
    rollback_admin = a.login('admin', 'Rollback-password-1')
    b.expect(200, '/api/accounts/list', {}, token=rollback_admin)
    b.expect(403, '/api/accounts/list', {}, token=successor)
    switch(nodes, candidate)
    chief_has_no_permission_rows(a)
    # 迁移只执行一次；重启新版不会给旧恢复锚点补上 chief。
    now, _ = a.expect(200, '/api/permissions/me', {}, token=rollback_admin)
    assert all(r['system_code'] != 'chief' for r in now['roles'])
    for node in nodes:
        node.stop()
    recover(a, candidate, 'Upgrade-password-1')
    switch(nodes, candidate)
    chief_has_no_permission_rows(a)
    restored = a.login('admin', 'Upgrade-password-1')
    now, _ = a.expect(200, '/api/permissions/me', {}, token=restored)
    assert {r['id'] for r in now['roles']} == {1, 3}, 'recovery lost other roles'
    role, _ = a.expect(200, '/api/roles/get', {'role_id': 2}, token=restored)
    assert role['role']['name'] == 'Edited developer' and role['role']['permission_codes'] == ['Users.View']
    events, _ = a.expect(200, '/api/identity/password-events', {}, token=restored)
    assert sum(e['action'] == 'admin_recovery' for e in events['items']) >= 2, 'upgrade lost password history'
    print('PASS: baseline upgrade, one-time migration, edited-role retention, old-version recovery, downgrade and re-upgrade')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', required=True)
    parser.add_argument('--database', choices=('sqlite', 'postgres'), required=True)
    parser.add_argument('--upgrade-from')
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='bifrost-rbac-smoke-'))
    nodes, control, database = [], None, None
    try:
        if args.database == 'postgres':
            control = os.environ.get('RBAC_TEST_POSTGRES_DSN')
            if not control:
                raise RuntimeError('RBAC_TEST_POSTGRES_DSN is required; PostgreSQL validation cannot be skipped')
            fields = dict(part.split('=', 1) for part in shlex.split(control))
            database = 'rbac_smoke_' + uuid.uuid4().hex
            psql(control, 'CREATE DATABASE ' + database + " TEMPLATE template0 ENCODING 'UTF8'")
            store = {'enabled': True, 'type': 'postgres', 'config': {'host': fields['host'], 'port': fields.get('port', '5432'), 'user': fields['user'], 'password': fields.get('password', ''), 'db_name': database, 'ssl_mode': fields.get('sslmode', 'disable')}}
        else:
            store = {'enabled': True, 'type': 'sqlite', 'config': {'path': str(root/'config.db')}}
        config = {'providers': {}, 'client': {'enable_logging': True, 'log_retention_days': 7}, 'config_store': store}
        setup = secrets.token_urlsafe(32)
        nodes.append(Node(args.upgrade_from or args.binary, root/'node1', config, setup))
        if args.database == 'postgres':
            nodes.append(Node(args.upgrade_from or args.binary, root/'node2', config, setup))
        for node in nodes:
            node.start()
        if args.upgrade_from:
            upgrade(nodes, args.upgrade_from, args.binary)
        else:
            exercise(nodes)
            if args.database == 'sqlite':
                conditional_profiles(args.binary, root)
    finally:
        for node in nodes:
            node.stop()
        if database:
            psql(control, 'DROP DATABASE ' + database + ' WITH (FORCE)')
        print('Isolated evidence:', root)


if __name__ == '__main__':
    main()
