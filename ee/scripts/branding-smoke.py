#!/usr/bin/env python3
"""Isolated branding smoke: fresh config, own PID only, no personal database.

--serve keeps a fresh fixture alive for browser testing until interrupted.
"""
import argparse
import base64
import contextlib
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import socket
import sqlite3
import struct
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import zlib


# Small geometric JPEG fixture kept as a file (no runtime imaging dependency).
JPEG = (Path(__file__).resolve().parent / 'testdata' / 'branding-icon.jpg').read_bytes()

def png(width=96, height=32):
    def chunk(kind, data):
        return struct.pack('!I', len(data)) + kind + data + struct.pack('!I', zlib.crc32(kind + data) & 0xffffffff)
    pixels = b''.join(b'\0' + bytes((35, 145, 190, 255)) * width for _ in range(height))
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('!IIBBBBB', width, height, 8, 6, 0, 0, 0)) + chunk(b'IDAT', zlib.compress(pixels)) + chunk(b'IEND', b'')


class Fixture:
    def __init__(self, binary, root):
        self.binary, self.root = str(Path(binary).resolve()), Path(root)
        self.proc = None
        self.log = None
        self.username = 'branding-test'
        self.password = secrets.token_urlsafe(24)
        config = {
            'client': {'enable_logging': False},
            'governance': {'auth_config': {'admin_username': self.username, 'admin_password': self.password, 'is_enabled': True}},
            'providers': {},
        }
        self.root.mkdir(parents=True, exist_ok=True)
        (self.root / 'config.json').write_text(json.dumps(config))
        os.chmod(self.root / 'config.json', 0o600)
        self.cookies = http.cookiejar.CookieJar()
        self.client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.cookies), urllib.request.ProxyHandler({}))
        self.anonymous = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def start(self):
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        self.url = f'http://127.0.0.1:{port}'
        self.log = (self.root / 'server.log').open('ab')
        # Do not inherit deployment credentials or app-directory overrides.
        env = {k: v for k, v in os.environ.items() if not k.startswith(('BIFROST_', 'OPENAI_', 'ANTHROPIC_'))}
        self.proc = subprocess.Popen([self.binary, '-host', '127.0.0.1', '-port', str(port), '-app-dir', str(self.root)], stdout=self.log, stderr=subprocess.STDOUT, env=env)
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            if self.proc.poll() is not None:
                raise RuntimeError('test server exited; see isolated server.log')
            try:
                if self.request('POST', '/api/branding/get', anonymous=True)[0] == 200:
                    return
            except (OSError, urllib.error.URLError):
                pass
            time.sleep(0.15)
        raise RuntimeError('test server startup timed out')

    def stop(self):
        if self.proc is not None and self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait(timeout=5)
        if self.log:
            self.log.close()

    def request(self, method, path, data=None, anonymous=False, headers=None):
        body = json.dumps(data).encode() if data is not None else None
        req = urllib.request.Request(self.url + path, data=body, method=method, headers={'Content-Type': 'application/json', **(headers or {})})
        opener = self.anonymous if anonymous else self.client
        try:
            response = opener.open(req, timeout=10)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            return response.status, response.headers, response.read()

    def state(self):
        status, headers, body = self.request('POST', '/api/branding/get', anonymous=True)
        assert status == 200 and headers.get('Cache-Control') == 'no-store'
        return json.loads(body)

    def login(self):
        assert self.request('POST', '/api/session/login', {'username': self.username, 'password': self.password})[0] == 200


def smoke(fixture):
    fixture.start()
    assert fixture.state() == {'enabled': False, 'has_logo': False, 'has_icon': False}
    for action in ('update', 'reset'):
        assert fixture.request('POST', '/api/branding/' + action, {'logo': ''}, anonymous=True)[0] == 401
        assert fixture.request('POST', '/api/branding/' + action, {'logo': ''}, anonymous=True, headers={'x-bf-vk': 'invalid-test-vk'})[0] == 401
        assert fixture.request('POST', '/api/branding/' + action, {'logo': ''}, anonymous=True, headers={'Authorization': 'Bearer invalid-test-session'})[0] == 401
    fixture.login()
    expired_token = next(cookie.value for cookie in fixture.cookies if cookie.name == 'token')
    with sqlite3.connect(fixture.root / 'config.db') as db:
        db.execute("UPDATE sessions SET expires_at='2000-01-01 00:00:00'")
    for action in ('update', 'reset'):
        assert fixture.request('POST', '/api/branding/' + action, {'logo': ''})[0] == 401
        assert fixture.request('POST', '/api/branding/' + action, {'logo': ''}, anonymous=True, headers={'Authorization': 'Bearer ' + expired_token})[0] == 401
    fixture.login()
    data, icon = png(), JPEG
    payload = {'logo': base64.b64encode(data).decode(), 'icon': base64.b64encode(icon).decode()}
    assert fixture.request('POST', '/api/branding/update', payload)[0] == 200
    state = fixture.state()
    for slot, expected in [('logo', data), ('icon', icon)]:
        code, headers, body = fixture.request('GET', state[slot + '_url'], anonymous=True)
        assert code == 200 and body == expected and headers.get('X-Content-Type-Options') == 'nosniff'
        assert fixture.request('GET', state[slot + '_url'], anonymous=True, headers={'If-None-Match': headers['ETag']})[0] == 304
    assert fixture.request('POST', '/api/branding/update', {'logo': payload['logo'], 'icon': 'invalid'})[0] == 400
    assert fixture.state() == state
    basic = base64.b64encode(f'{fixture.username}:{fixture.password}'.encode()).decode()
    assert fixture.request('POST', '/api/branding/update', {'icon': payload['icon']}, anonymous=True, headers={'Authorization': 'Basic ' + basic})[0] == 200
    token = next(cookie.value for cookie in fixture.cookies if cookie.name == 'token')
    assert fixture.request('POST', '/api/branding/update', {'icon': payload['icon']}, anonymous=True, headers={'Authorization': 'Bearer ' + token})[0] == 200
    state = fixture.state()
    fixture.stop()
    fixture.start()
    assert fixture.state() == state, 'restart changed image state'
    assert fixture.request('GET', state['logo_url'], anonymous=True)[2] == data
    fixture.login()
    assert fixture.request('POST', '/api/branding/reset')[0] == 200
    assert not fixture.state()['enabled']
    assert fixture.request('GET', state['logo_url'], anonymous=True)[0] == 404
    fixture.stop()
    fixture.start()
    assert not fixture.state()['enabled'], 'reset was not persistent'
    print('PASS: real auth (cookie/basic/bearer), image bytes/cache, atomic rejection, restart retention, reset + second restart', flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True)
    parser.add_argument('--serve', action='store_true')
    parser.add_argument('--fixture-info', help='write temporary browser fixture coordinates (contains test credentials)')
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='bifrost-branding-') as directory:
        fixture = Fixture(args.binary, directory)
        try:
            if args.serve:
                fixture.start()
                (fixture.root / 'logo.png').write_bytes(png())
                (fixture.root / 'icon.png').write_bytes(png(24, 24))
                (fixture.root / 'invalid.svg').write_text('<svg/>')
                if args.fixture_info:
                    info = Path(args.fixture_info)
                    info.write_text(json.dumps({'url': fixture.url, 'root': str(fixture.root), 'username': fixture.username, 'password': fixture.password}))
                    os.chmod(info, 0o600)
                print('Browser fixture ready at ' + fixture.url, flush=True)
                while fixture.proc.poll() is None:
                    time.sleep(0.5)
            else:
                smoke(fixture)
        finally:
            fixture.stop()


if __name__ == '__main__':
    with contextlib.suppress(KeyboardInterrupt):
        main()
