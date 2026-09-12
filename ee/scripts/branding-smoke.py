#!/usr/bin/env python3
"""Isolated branding smoke: fresh config, own PID only, no personal database.

--serve keeps a fresh fixture alive for browser testing until interrupted.
The EE process fixture is shared with identity-smoke.py; this script only adds
the legacy administrator configuration and the branding assertions.
"""
import argparse
import base64
import contextlib
import importlib.util
import json
import os
from pathlib import Path
import secrets
import sqlite3
import struct
import tempfile
import time
import zlib

spec = importlib.util.spec_from_file_location('identity_smoke', Path(__file__).with_name('identity-smoke.py'))
identity_smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(identity_smoke)


# Small geometric JPEG fixture kept as a file (no runtime imaging dependency).
JPEG = (Path(__file__).resolve().parent / 'testdata' / 'branding-icon.jpg').read_bytes()


def png(width=96, height=32):
    def chunk(kind, data):
        return struct.pack('!I', len(data)) + kind + data + struct.pack('!I', zlib.crc32(kind + data) & 0xffffffff)
    pixels = b''.join(b'\0' + bytes((35, 145, 190, 255)) * width for _ in range(height))
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('!IIBBBBB', width, height, 8, 6, 0, 0, 0)) + chunk(b'IDAT', zlib.compress(pixels)) + chunk(b'IEND', b'')


class Fixture(identity_smoke.Node):
    """A Node started from a legacy shared-administrator configuration, as an upgraded deployment would be."""

    def __init__(self, binary, root):
        self.username = 'branding-test'
        self.password = secrets.token_urlsafe(24)
        config = {
            'client': {'enable_logging': False},
            'governance': {'auth_config': {'admin_username': self.username, 'admin_password': self.password, 'is_enabled': True}},
            'providers': {},
        }
        super().__init__(binary, root, config, '')

    def state(self):
        status, value, headers = self.call('/api/branding/get', {}, anonymous=True)
        assert status == 200 and headers.get('Cache-Control') == 'no-store'
        return value

    def login(self):
        return super().login(self.username, self.password)

    def session_cookie(self):
        return next(cookie.value for cookie in self.cookies if cookie.name == 'ee_session')


def smoke(fixture):
    fixture.start()
    # 上游路由与嵌入 UI 正常，骨架探针不再随正式应用装配。
    code, body, headers = fixture.call('/api/ee/ping', method='GET', anonymous=True)
    assert code == 200 and 'text/html' in headers.get('Content-Type', '')  # 上游 SPA fallback
    assert b'"probe_rows"' not in body and b'ee-probe' not in body
    assert headers.get('X-Bifrost-EE') is None
    code, _, headers = fixture.call('/api/version', method='GET', anonymous=True)
    assert code == 200 and headers.get('X-Bifrost-EE') is None
    code, body, headers = fixture.call('/', method='GET', anonymous=True)
    assert code == 200 and 'text/html' in headers.get('Content-Type', '')
    assert b'x-bifrost-ee' not in body
    with sqlite3.connect(fixture.root / 'config.db') as db:
        assert db.execute("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='ee_probe'").fetchone()[0] == 0
        assert db.execute("SELECT count(*) FROM config_plugins WHERE name='ee-probe'").fetchone()[0] == 0
    assert 'ee-probe' not in (fixture.root / 'server.log').read_text()
    assert fixture.state() == {'enabled': False, 'has_logo': False, 'has_icon': False}
    for action in ('update', 'reset'):
        fixture.expect(401, '/api/branding/' + action, {'logo': ''}, anonymous=True)
        fixture.expect(401, '/api/branding/' + action, {'logo': ''}, anonymous=True, headers={'x-bf-vk': 'invalid-test-vk'})
        fixture.expect(401, '/api/branding/' + action, {'logo': ''}, anonymous=True, headers={'Authorization': 'Bearer invalid-test-session'})
    fixture.login()
    expired_token = fixture.session_cookie()
    with sqlite3.connect(fixture.root / 'config.db') as db:
        db.execute("UPDATE ee_identity_sessions SET expires_at='2000-01-01 00:00:00'")
    for action in ('update', 'reset'):
        fixture.expect(401, '/api/branding/' + action, {'logo': ''})
        fixture.expect(401, '/api/branding/' + action, {'logo': ''}, anonymous=True, headers={'Authorization': 'Bearer ' + expired_token})
    fixture.login()
    data, icon = png(), JPEG
    payload = {'logo': base64.b64encode(data).decode(), 'icon': base64.b64encode(icon).decode()}
    fixture.expect(200, '/api/branding/update', payload)
    state = fixture.state()
    for slot, expected in [('logo', data), ('icon', icon)]:
        code, body, headers = fixture.call(state[slot + '_url'], method='GET', anonymous=True)
        assert code == 200 and body == expected and headers.get('X-Content-Type-Options') == 'nosniff'
        fixture.expect(304, state[slot + '_url'], method='GET', anonymous=True, headers={'If-None-Match': headers['ETag']})
    fixture.expect(400, '/api/branding/update', {'logo': payload['logo'], 'icon': 'invalid'})
    assert fixture.state() == state
    basic = base64.b64encode(f'{fixture.username}:{fixture.password}'.encode()).decode()
    fixture.expect(401, '/api/branding/update', {'icon': payload['icon']}, anonymous=True, headers={'Authorization': 'Basic ' + basic})
    fixture.expect(401, '/api/branding/update', {'icon': payload['icon']}, anonymous=True, headers={'Authorization': 'Bearer ' + fixture.session_cookie()})
    state = fixture.state()
    fixture.stop()
    fixture.start()
    assert fixture.state() == state, 'restart changed image state'
    assert fixture.call(state['logo_url'], method='GET', anonymous=True)[1] == data
    fixture.login()
    fixture.expect(200, '/api/branding/reset', {})
    assert not fixture.state()['enabled']
    fixture.expect(404, state['logo_url'], method='GET', anonymous=True)
    fixture.stop()
    fixture.start()
    assert not fixture.state()['enabled'], 'reset was not persistent'
    print('PASS: shared host/embedded UI, probes removed, EE cookie auth and rejected legacy basic/bearer, image bytes/cache, atomic rejection, restart retention, reset + second restart', flush=True)


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
                while fixture.process.poll() is None:
                    time.sleep(0.5)
            else:
                smoke(fixture)
        finally:
            fixture.stop()


if __name__ == '__main__':
    with contextlib.suppress(KeyboardInterrupt):
        main()
