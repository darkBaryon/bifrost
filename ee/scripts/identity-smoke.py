#!/usr/bin/env python3
"""Run real EE HTTP authentication against isolated SQLite or PostgreSQL data.
PostgreSQL requires IDENTITY_TEST_POSTGRES_DSN (libpq keyword/value syntax).
Only processes and databases created by this script are cleaned up.
"""
import argparse
import base64
import concurrent.futures
import contextlib
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import shlex
import socket
import sqlite3
import struct
import sys
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid


def psql(dsn, sql):
    result = subprocess.run(['psql', '-X', '-v', 'ON_ERROR_STOP=1', '-d', dsn, '-Atc', sql], capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError('isolated PostgreSQL command failed')
    return result.stdout.strip()


class Node:
    def __init__(self, binary, root, config, setup):
        self.binary, self.root = str(Path(binary).resolve()), Path(root)
        self.root.mkdir(parents=True, exist_ok=True)
        self.config = config
        self.setup = setup
        self.cookies = http.cookiejar.CookieJar()
        self.client = self.new_client(self.cookies)
        self.process = None
        self.log = None
        with socket.socket() as s:
            s.bind(('127.0.0.1', 0))
            self.port = s.getsockname()[1]
        self.url = f'http://127.0.0.1:{self.port}'
        path = self.root / 'config.json'
        path.write_text(json.dumps(config))
        path.chmod(0o600)

    @staticmethod
    def new_client(cookies=None):
        return urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(cookies if cookies is not None else http.cookiejar.CookieJar()))

    def start(self, *, default_home=None):
        env = {k: v for k, v in os.environ.items() if not k.startswith(('BIFROST_', 'EE_', 'OPENAI_', 'ANTHROPIC_', 'AWS_', 'AZURE_'))}
        env.update(EE_PUBLIC_ORIGIN=self.url, BIFROST_SETUP_TOKEN=self.setup)
        self.log = (self.root / 'server.log').open('ab')
        command = [self.binary, '-host', '127.0.0.1', '-port', str(self.port)]
        cwd = None
        if default_home is None:
            command += ['-app-dir', str(self.root)]
        else:
            env.update(HOME=str(default_home), APPDATA=str(Path(default_home)/'.config'))
            cwd = Path(default_home)/'working-directory'
            cwd.mkdir(exist_ok=True)
        self.process = subprocess.Popen(command, cwd=cwd, env=env, stdout=self.log, stderr=subprocess.STDOUT)
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
        raise RuntimeError('EE startup timeout')

    def stop(self):
        if self.process and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
        if self.log:
            self.log.close()
            self.log = None

    def call(self, path, data=None, method='POST', headers=None, anonymous=False, token=None, raw=None):
        h = {'Content-Type': 'application/json', 'Origin': self.url, **(headers or {})}
        if token:
            h['Cookie'] = 'ee_session=' + token
        body = raw if raw is not None else (json.dumps(data).encode() if data is not None else None)
        req = urllib.request.Request(self.url + path, body, h, method=method)
        client = self.new_client() if anonymous or token else self.client
        try:
            response = client.open(req, timeout=12)
        except urllib.error.HTTPError as e:
            response = e
        with response:
            content = response.read()
            try:
                value = json.loads(content)
            except (ValueError, UnicodeDecodeError):
                value = content
            return response.status, value, response.headers

    def expect(self, code, path, data=None, **kwargs):
        actual, value, headers = self.call(path, data, **kwargs)
        if actual != code:
            raise AssertionError(f'{kwargs.get("method", "POST")} {path}: expected {code}, got {actual}')
        return value, headers

    def login(self, username, password):
        value, headers = self.expect(200, '/api/identity/login', {'username': username, 'password': password})
        cookie = next(c for c in self.cookies if c.name == 'ee_session')
        assert cookie.has_nonstandard_attr('HttpOnly')
        assert 'SameSite=Lax' in headers.get('Set-Cookie', '')
        assert 'token' not in value
        return cookie.value


def websocket(node, ticket):
    s = socket.create_connection(('127.0.0.1', node.port), timeout=5)
    key = base64.b64encode(secrets.token_bytes(16)).decode()
    req = f'GET /ws?ticket={ticket} HTTP/1.1\r\nHost: 127.0.0.1:{node.port}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\nOrigin: {node.url}\r\n\r\n'
    s.sendall(req.encode())
    data = b''
    while b'\r\n\r\n' not in data:
        chunk = s.recv(1)
        if not chunk:
            raise AssertionError('WS handshake closed')
        data += chunk
    assert b' 101 ' in data.split(b'\r\n')[0], 'WS handshake rejected'
    return s


def frame(s):
    def read(n):
        out = b''
        while len(out) < n:
            part = s.recv(n-len(out))
            if not part:
                return None
            out += part
        return out
    head = read(2)
    if head is None:
        return None
    size = head[1] & 127
    if size == 126:
        size = struct.unpack('!H', read(2))[0]
    elif size == 127:
        size = struct.unpack('!Q', read(8))[0]
    assert size < 1_000_000
    payload = read(size)
    return head[0] & 15, payload


def exercise(nodes, dbconfig, dsn):
    a = nodes[0]
    b = nodes[-1]
    a.expect(401, '/api/accounts/list', {})
    a.expect(403, '/api/identity/initialize', {'setup_token': 'wrong', 'username': 'admin', 'password': 'Admin-password-1'})
    results = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        futures = [pool.submit(n.call, '/api/identity/initialize', {'setup_token': n.setup, 'username': 'admin', 'password': 'Admin-password-1'}, anonymous=True) for n in (a, b)]
        results = [f.result()[0] for f in futures]
    assert sorted(results) == [201, 409], 'initialization was not atomic'
    admin = a.login('admin', 'Admin-password-1')
    admin_account, _ = a.expect(200, '/api/identity/me', {}, token=admin)
    admin_id = admin_account['account']['id']
    b.expect(200, '/api/accounts/list', {}, token=admin)
    a.expect(401, '/api/accounts/list', {}, anonymous=True, headers={'Authorization': 'Bearer '+admin})
    a.expect(401, '/api/accounts/list', {}, anonymous=True, headers={'Cookie': 'token='+admin})
    a.expect(401, '/api/accounts/list', {}, anonymous=True, headers={'Authorization': 'Basic '+base64.b64encode(b'admin:Admin-password-1').decode()})
    a.expect(403, '/api/accounts/list', {}, token=admin, headers={'Origin': 'https://evil.invalid'})
    a.expect(403, '/api/accounts/list', {}, token=admin, headers={'Referer': 'https://evil.invalid/page'})
    a.expect(400, '/api/accounts/create', {'username':'alice','role':'admin'}, token=admin)
    row, _ = a.expect(201, '/api/accounts/create', {'username':'alice'}, token=admin)
    alice_id = row['account']['id']
    a.expect(409, '/api/accounts/create', {'username':'alice'}, token=admin)
    alice = a.login('alice', '123456')
    a.expect(403, '/api/accounts/list', {}, token=alice)
    a.expect(403, '/api/identity/password-events', {}, token=alice)
    a.expect(400, '/api/identity/change-password', {'old_password':'wrong','new_password':'Alice-password-1'}, token=alice)
    a.expect(200, '/api/identity/change-password', {'old_password':'123456','new_password':'Alice-password-1'}, token=alice)
    b.expect(401, '/api/identity/me', {}, token=alice)
    alice = a.login('alice', 'Alice-password-1')
    if dbconfig['type'] == 'sqlite':
        # 真实driver故障须有可关联的安全诊断，同时保持密码/会话/事件原子回滚。
        with sqlite3.connect(dbconfig['config']['path']) as db:
            db.execute("CREATE TRIGGER smoke_event_failure BEFORE INSERT ON ee_identity_password_events BEGIN SELECT RAISE(ABORT, 'private-driver-error'); END")
        try:
            _, headers = a.expect(503, '/api/accounts/reset-password', {'account_id':alice_id,'operation_id':str(uuid.uuid4())}, token=admin)
            diagnostic = (a.root/'server.log').read_text()
            assert 'operation=identity.reset-password stage=password_event.insert incident_id='+headers['X-Request-ID'] in diagnostic
            assert 'kind=sqlite code=19/' in diagnostic
            assert 'private-driver-error' not in diagnostic
            a.expect(200, '/api/identity/me', {}, token=alice)
        finally:
            with sqlite3.connect(dbconfig['config']['path']) as db:
                db.execute('DROP TRIGGER smoke_event_failure')
    op = str(uuid.uuid4())
    event, _ = b.expect(200, '/api/accounts/reset-password', {'account_id':alice_id,'operation_id':op}, token=admin)
    a.expect(401, '/api/identity/me', {}, token=alice)
    alice = a.login('alice', '123456')
    repeat, _ = a.expect(200, '/api/accounts/reset-password', {'account_id':alice_id,'operation_id':op}, token=admin)
    assert repeat['event_id'] == event['event_id']
    a.expect(200, '/api/identity/me', {}, token=alice)
    a.expect(200, '/api/identity/change-password', {'old_password':'123456','new_password':'Alice-password-2'}, token=alice)
    alice = a.login('alice', 'Alice-password-2')
    own, _ = a.expect(200, '/api/identity/password-events', {}, token=alice)
    assert any(e['id'] == event['event_id'] for e in own['items'])
    assert all(e['target_id'] == alice_id for e in own['items'])
    assert not any(secret in json.dumps(own) for secret in ('123456','password_hash','Alice-password'))
    a.expect(403, '/api/identity/password-events', {'target_id':admin_id}, token=alice)
    b.expect(200, '/api/accounts/set-status', {'account_id':alice_id,'status':'disabled'}, token=admin)
    a.expect(401, '/api/identity/me', {}, token=alice)
    b.expect(200, '/api/accounts/set-status', {'account_id':alice_id,'status':'active'}, token=admin)
    a.expect(401, '/api/identity/me', {}, token=alice)
    a.expect(403, '/api/accounts/set-status', {'account_id':admin_id,'status':'disabled'}, token=admin)
    cfg, _ = a.expect(200, '/api/config', method='GET', token=admin)
    cfg['client_config']['enable_logging'] = False
    cfg['client_config']['log_retention_days'] = 37
    a.expect(200, '/api/config', cfg, method='PUT', token=admin)
    stored, _ = a.expect(200, '/api/config', method='GET', token=admin)
    assert stored['client_config']['log_retention_days'] == 37
    bad = json.loads(json.dumps(cfg))
    bad['auth_config'] = {'is_enabled':False}
    bad['client_config']['log_retention_days'] = 38
    a.expect(409, '/api/config', bad, method='PUT', token=admin)
    stored, _ = a.expect(200, '/api/config', method='GET', token=admin)
    assert stored['client_config']['log_retention_days'] == 37, 'rejected auth edit partially wrote settings'
    a.expect(400, '/api/config', {'AUTH_CONFIG':{'is_enabled':False}}, method='PUT', token=admin)
    ticket, _ = a.expect(200, '/api/identity/ws-ticket', {}, token=admin)
    assert ticket['expires_in'] == 30
    # 解压和OPTIONS可在认证前返回；同一张未消费票据随后仍须可正常握手。
    a.expect(400, '/ws?ticket='+ticket['ticket'], method='GET', raw=b'not gzip', headers={'Content-Encoding':'gzip'})
    a.expect(200, '/ws?ticket='+ticket['ticket'], method='OPTIONS')
    a.expect(401, '/ws?token=legacy-private-token', method='GET')
    with contextlib.closing(websocket(b, ticket['ticket'])) as ws:
        notification = {'title':'identity-test','message':'test notification','severity':'info','audience':'roles','role_ids':[987654]}
        b.expect(201, '/api/notifications', notification, token=admin)
        ws.settimeout(8)
        message = frame(ws)
        assert message and message[0] == 1 and b'identity-test' in message[1], 'chief WS notification missing'
        a.expect(200, '/api/identity/change-password', {'old_password':'Admin-password-1','new_password':'Admin-password-2'}, token=admin)
        b.expect(401, '/api/accounts/list', {}, token=admin)
        admin_new = a.login('admin', 'Admin-password-2')
        b.expect(201, '/api/notifications', notification, token=admin_new)
        closed = frame(ws)
        assert closed is None or closed[0] == 8, 'revoked websocket still receives data'
    # 提交后重启与配置旧凭据变动均不能复活旧会话。
    for node in nodes:
        node.stop()
    env = os.environ.copy()
    result = subprocess.run([a.binary, 'identity', 'recover-admin', '--app-dir', str(a.root)], input=b'Recovered-password-1\n', stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, timeout=40)
    assert result.returncode == 0, 'offline recovery failed'
    for node in nodes:
        node.start()
    a.expect(401, '/api/identity/me', {}, token=admin_new)
    recovered = a.login('admin', 'Recovered-password-1')
    logs, _ = a.expect(200, '/api/identity/password-events', {}, token=recovered)
    assert any(e['action']=='admin_recovery' and e['actor_id']=='operator' for e in logs['items'])
    assert any(e['id']==event['event_id'] for e in logs['items'])
    a.expect(200, '/api/session/logout', token=recovered)
    b.expect(401, '/api/identity/me', {}, token=recovered)
    for i in range(5):
        a.expect(401, '/api/identity/login', {'username':'nonexistent','password':'invalid'}, anonymous=True)
    b.expect(429, '/api/identity/login', {'username':'nonexistent','password':'invalid'}, anonymous=True)
    if dbconfig['type']=='sqlite':
        with sqlite3.connect(dbconfig['config']['path']) as db:
            hashes = [r[0] for r in db.execute('SELECT token_hash FROM ee_identity_sessions')]
            assert all(len(h)==64 and h not in (admin,alice,recovered) for h in hashes)
    for node in nodes:
        log = (node.root/'server.log').read_bytes()
        for secret in (admin, admin_new, alice, recovered, ticket['ticket'], node.setup, 'legacy-private-token'):
            assert secret.encode() not in log, 'application log contains a credential'
    print('PASS: initialization race, local accounts, mandatory password change, cross-node revoke, idempotent reset, queryable events, configuration compatibility, WS revoke, offline recovery and restart')


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--binary',required=True)
    p.add_argument('--database',choices=('sqlite','postgres'),required=True)
    args=p.parse_args()
    root=Path(tempfile.mkdtemp(prefix='bifrost-identity-smoke-'))
    nodes=[]
    control=None
    database=None
    database_created=False
    succeeded=False
    try:
        if args.database=='postgres':
            control=os.environ.get('IDENTITY_TEST_POSTGRES_DSN')
            if not control:
                raise RuntimeError('IDENTITY_TEST_POSTGRES_DSN is required; PostgreSQL validation cannot be skipped')
            fields=dict(part.split('=',1) for part in shlex.split(control))
            database='identity_smoke_'+uuid.uuid4().hex
            psql(control,'CREATE DATABASE '+database)
            database_created=True
            dbconfig={'enabled':True,'type':'postgres','config':{'host':fields['host'],'port':fields.get('port','5432'),'user':fields['user'],'password':fields.get('password',''),'db_name':database,'ssl_mode':fields.get('sslmode','disable')}}
        else:
            dbconfig={'enabled':True,'type':'sqlite','config':{'path':str(root/'config.db')}}
        config={'providers':{},'client':{'enable_logging':False},'config_store':dbconfig}
        setup=secrets.token_urlsafe(32)
        nodes=[Node(args.binary,root/'node1',config,setup)]
        if args.database=='postgres':
            nodes.append(Node(args.binary,root/'node2',config,setup))
        for node in nodes:
            node.start()
        exercise(nodes,dbconfig,control)
        succeeded=True
    finally:
        for node in nodes:
            node.stop()
        print('Isolated evidence:',root, file=sys.stderr)
        if control and database_created:
            if succeeded:
                psql(control,'DROP DATABASE '+database+' WITH (FORCE)')
            else:
                print('Failure database retained:',database, file=sys.stderr)
                print('After inspecting evidence, explicitly drop only this test database using the original test connection.', file=sys.stderr)


if __name__=='__main__':
    main()
