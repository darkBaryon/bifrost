#!/usr/bin/env python3
"""启动临时Bifrost实例和测试数据库，通过真实HTTP请求验证角色管理、权限撤回和升级恢复。"""
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


def chief_has_no_permission_rows(node):
    # 主管理员的全部权限在查询时补齐；初始化、修改和升级回退都不能把这些权限逐项存进表。
    query = 'SELECT COUNT(*) FROM ee_rbac_role_permissions WHERE role_id=1'
    store = node.config['config_store']
    if store['type'] == 'sqlite':
        with sqlite3.connect(store['config']['path']) as db:
            count = db.execute(query).fetchone()[0]
    else:
        count = int(psql(os.environ['BIFROST_TEST_POSTGRES_DSN'] + ' dbname=' + store['config']['db_name'], query))
    assert count == 0, 'chief derived permission rows persisted'


def exercise(nodes):
    a, b = nodes[0], nodes[-1]
    a.expect(401, '/api/roles/list', {}, anonymous=True)
    a.expect(201, '/api/identity/initialize', {'setup_token': a.setup, 'username': 'admin', 'password': 'Admin-password-1'})
    admin = a.login('admin', 'Admin-password-1')
    catalogue, _ = a.expect(200, '/api/permissions/list', {}, token=admin)
    assert sum(len(m['permissions']) for m in catalogue['modules']) == 29
    me, _ = a.expect(200, '/api/permissions/me', {}, token=admin)
    assert len(me['permissions']) == 29
    own_id = me['account_id']
    chief_has_no_permission_rows(a)
    for headers, status in [({'Origin': 'https://evil.invalid'}, 403), ({'Authorization': 'Bearer invalid'}, 401)]:
        a.expect(status, '/api/roles/list', {}, token=admin, headers=headers)
    for raw in (b'{"role_id":"1"}', b'{"role_id":1.0}', b'{"role_id":1e0}', b'{"role_id":null}', b'{"role_id":1,"role_id":2}', b'{"role_id":9007199254740992}'):
        a.expect(400, '/api/roles/get', raw=raw, token=admin)
    a.expect(403, '/api/accounts/set-roles', {'account_id': own_id, 'role_ids': []}, token=admin)
    row, _ = a.expect(201, '/api/accounts/create', {'username': 'member'}, token=admin)
    member_id = row['account']['id']
    member = a.login('member', '123456')
    a.expect(403, '/api/permissions/me', {}, token=member)
    a.expect(200, '/api/identity/change-password', {'old_password': '123456', 'new_password': 'Member-password-1'}, token=member)
    a.expect(401, '/api/permissions/me', {}, token=member)
    member = a.login('member', 'Member-password-1')
    empty, _ = b.expect(200, '/api/permissions/me', {}, token=member)
    assert empty['roles'] == [] and empty['permissions'] == []
    b.expect(403, '/api/roles/get', {'role_id': 999999}, token=member)
    reader, _ = a.expect(201, '/api/roles/create', {'name': ' reader ', 'permission_codes': ['Users.View', 'Users.View']}, token=admin)
    role_id = reader['role']['id']
    assert reader['role']['name'] == 'reader' and reader['role']['permission_codes'] == ['Users.View']
    a.expect(200, '/api/accounts/set-roles', {'account_id': member_id, 'role_ids': [role_id, role_id]}, token=admin)
    effective, _ = b.expect(200, '/api/accounts/get-roles', {'account_id': member_id}, token=member)
    assert len(effective['roles']) == 1 and effective['permissions'] == [{'code': 'Users.View', 'role_ids': [role_id]}]
    b.expect(200, '/api/roles/list', {}, token=member)
    b.expect(200, '/api/roles/get', {'role_id': role_id}, token=member)
    b.expect(200, '/api/accounts/list', {}, token=member)
    b.expect(403, '/api/roles/create', {'name': 'denied', 'permission_codes': []}, token=member)
    conflict, _ = a.expect(409, '/api/roles/delete', {'role_id': role_id}, token=admin)
    assert conflict['error']['details']['account_count'] == 1
    a.expect(200, '/api/roles/update', {'role_id': role_id, 'name': 'manager', 'description': '', 'permission_codes': ['Users.Manage', 'Notifications.View', 'ModelProvider.Manage']}, token=admin)
    b.expect(403, '/api/accounts/list', {}, token=member)
    created, _ = b.expect(201, '/api/roles/create', {'name': 'created-by-manager', 'permission_codes': []}, token=member)
    b.expect(201, '/api/accounts/create', {'username': 'managed'}, token=member)
    b.expect(409, '/api/accounts/set-roles', {'account_id': own_id, 'role_ids': []}, token=member)
    for path in ('/api/providers', '/api/config', '/api/notifications', '/ws'):
        b.expect(403, path, method='GET', token=member)
    b.expect(403, '/api/identity/ws-ticket', {}, token=member)
    a.expect(200, '/api/providers', method='GET', token=admin)
    projected, _ = a.expect(200, '/api/config', method='GET', token=admin)
    assert projected['auth_config']['admin_username']['value'] == 'admin'
    assert projected['auth_config']['admin_password']['value'] == '<redacted>'
    # 两个节点共享数据库：移除角色分配后，下一次请求必须重新检查权限。
    a.expect(200, '/api/accounts/set-roles', {'account_id': member_id, 'role_ids': []}, token=admin)
    b.expect(403, '/api/roles/create', {'name': 'revoked', 'permission_codes': []}, token=member)
    a.expect(200, '/api/roles/delete', {'role_id': role_id}, token=admin)
    a.expect(200, '/api/roles/delete', {'role_id': created['role']['id']}, token=admin)
    a.expect(404, '/api/roles/get', {'role_id': role_id}, token=admin)
    a.expect(409, '/api/roles/delete', {'role_id': 1}, token=admin)
    for path in ('/api/permissions/list', '/api/permissions/me', '/api/roles/list', '/api/roles/get', '/api/roles/create', '/api/roles/update', '/api/roles/delete', '/api/accounts/get-roles', '/api/accounts/set-roles'):
        _, headers = a.expect(405, path, method='GET', token=admin)
        assert headers['Allow'] == 'POST'
    a.expect(200, '/api/identity/logout', {}, token=member)
    b.expect(401, '/api/permissions/me', {}, token=member)
    print('PASS: 1C nine HTTP routes, strict inputs, role grant/revoke, account policy and unchanged host boundaries')


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
    # 准备回退旧版本并运行它的恢复命令，先停止所有共享这个数据库的实例。
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
    # 首次角色初始化只执行一次；重新启动新版，不会自动给原管理员补回已被移除的主管理员角色。
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
    root = Path(tempfile.mkdtemp(prefix='bifrost-rbac-1c-smoke-'))
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
        nodes.append(Node(args.upgrade_from or args.binary, root/'node1', config, setup))
        if args.database == 'postgres':
            nodes.append(Node(args.upgrade_from or args.binary, root/'node2', config, setup))
        for node in nodes:
            node.start()
        if args.upgrade_from:
            upgrade(nodes, args.upgrade_from, args.binary)
        else:
            exercise(nodes)
    finally:
        for node in nodes:
            node.stop()
        if database:
            psql(control, 'DROP DATABASE ' + database + ' WITH (FORCE)')
        print('Isolated evidence:', root)


if __name__ == '__main__':
    main()
