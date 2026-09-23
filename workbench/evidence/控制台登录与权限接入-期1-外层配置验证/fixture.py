#!/usr/bin/env python3
"""Temporary real-EE authentication fixture; never imports/runs a smoke main().

Start (foreground; SIGINT/SIGTERM stops only the EE process this run created):
  python3 /tmp/console-real-auth-fixture.py start --binary /tmp/ee-binary --root /tmp/new-fixture

After capturing the baseline browser state, explicitly grant Settings.View:
  python3 /tmp/console-real-auth-fixture.py grant-settings --root /tmp/new-fixture

Browser state files contain sessions and are mode 0600. Load them into separate
browser contexts. Never print their contents or private-state.json. The start
command does not grant Settings.View. No existing server or database is touched.
"""

from __future__ import annotations

import argparse
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import runpy
import secrets
import signal
import sys
import tempfile
import time
from urllib.parse import urlparse


DEFAULT_REPO = Path("/Users/xinyue/VSCode/ws_2026/bifrost-console-ui")
FIXTURE_KIND = "console-real-config-auth-v1"
STATE_NAME = "private-state.json"


def emit(**value):
    print(json.dumps(value, ensure_ascii=False), flush=True)


def private_json(path: Path, value):
    # Create private from the first byte; replacement is atomic within our root.
    target = path.with_name(path.name + ".tmp")
    fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(value, stream, ensure_ascii=False, indent=2)
            stream.write("\n")
        os.replace(target, path)
        path.chmod(0o600)
    finally:
        if target.exists():
            target.unlink()


def temp_root(path: Path) -> Path:
    root = path.expanduser().resolve()
    allowed = {Path("/tmp").resolve(), Path(tempfile.gettempdir()).resolve()}
    if not any(root != base and root.is_relative_to(base) for base in allowed):
        raise RuntimeError("Fixture root must be a child of a system temporary directory")
    return root


def node_class(repo: Path):
    helper = repo.resolve() / "ee/scripts/identity-smoke.py"
    # run_name is not __main__: only helper definitions are loaded.
    BaseNode = runpy.run_path(str(helper), run_name="console_auth_helpers")["Node"]

    class FixtureNode(BaseNode):
        def call(self, path, data=None, **kwargs):
            headers = dict(kwargs.pop("headers", None) or {})
            headers.setdefault("Origin", self.public_origin)
            return super().call(path, data, headers=headers, **kwargs)

    return FixtureNode


def cookie_state(node, expected_token):
    cookie = next(c for c in node.cookies if c.name == "ee_session")
    if cookie.value != expected_token or not cookie.has_nonstandard_attr("HttpOnly"):
        raise RuntimeError("Unexpected session cookie attributes")
    return {
        "cookies": [{
            "name": "ee_session",
            "value": cookie.value,
            "domain": cookie.domain,
            "path": cookie.path or "/",
            "expires": cookie.expires if cookie.expires is not None else -1,
            "httpOnly": True,
            "secure": bool(cookie.secure),
            "sameSite": "Lax",
        }],
        "origins": [],
    }


def permission_codes(node, token):
    value, _ = node.expect(200, "/api/permissions/me", {}, token=token)
    return {item["code"] for item in value["permissions"]}


def assert_regular_session(node, token, account_id):
    value, _ = node.expect(200, "/api/identity/me", {}, token=token)
    account = value["account"]
    if account["id"] != account_id or account.get("must_change_password", False):
        raise RuntimeError("Session is not the expected account with completed password change")


def check_pair(node, token, expected_config):
    config_code, config, _ = node.call("/api/config", method="GET", token=token)
    routing_code, rules, _ = node.call("/api/routing/rules?limit=25&offset=0", method="GET", token=token)
    if config_code != expected_config or routing_code != 200:
        raise AssertionError(f"Expected config={expected_config}, routing=200; got config={config_code}, routing={routing_code}")
    result = {"config_status": config_code, "routing_status": routing_code}
    if config_code == 200:
        # These are the outer FullPage gate inputs; do not persist full config.
        result["is_db_connected"] = config.get("is_db_connected")
        result["is_logs_connected"] = config.get("is_logs_connected")
    result["routing_count"] = len(rules.get("rules", []))
    return result


def new_password(label):
    return label + "-" + secrets.token_urlsafe(24)


def start(args):
    root = temp_root(args.root)
    if root.exists() and any(root.iterdir()):
        raise RuntimeError("Use a fresh empty fixture root; existing contents are never reused")
    root.mkdir(parents=True, mode=0o700, exist_ok=True)
    root.chmod(0o700)
    binary = args.binary.expanduser().resolve()
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise RuntimeError("EE binary is missing or not executable")
    config = {
        "providers": {},
        "client": {"enable_logging": False},
        "config_store": {"enabled": True, "type": "sqlite", "config": {"path": str(root / "config.db")}},
    }
    Node = node_class(args.repo)
    node = Node(binary, root / "app", config, secrets.token_urlsafe(32))
    if node.port == 59817:
        # The existing service mentioned by the user must never be selected.
        raise RuntimeError("Reserved existing-service port selected; retry with a fresh fixture root")
    node.public_origin = args.public_origin or node.url
    parsed = urlparse(node.public_origin)
    if (parsed.scheme != "http" or parsed.hostname != "127.0.0.1" or not parsed.port
            or parsed.path or parsed.query or parsed.fragment or parsed.username or parsed.password
            or parsed.port == 59817):
        raise RuntimeError("Public origin must be an isolated http://127.0.0.1:PORT origin")
    admin_username, member_username = "fixture_admin", "fixture_routing_reader"
    admin_initial = new_password("Initial-admin")
    admin_password = new_password("Changed-admin")
    member_initial = new_password("Initial-member")
    member_password = new_password("Changed-member")
    stop_requested = False

    def request_stop(_signum, _frame):
        nonlocal stop_requested
        stop_requested = True

    signal.signal(signal.SIGINT, request_stop)
    signal.signal(signal.SIGTERM, request_stop)
    try:
        node.start(extra_env={"EE_INITIAL_PASSWORD": member_initial, "EE_PUBLIC_ORIGIN": node.public_origin})
        if stop_requested:
            return
        node.expect(201, "/api/identity/initialize", {
            "setup_token": node.setup, "username": admin_username, "password": admin_initial,
        }, anonymous=True)
        initial_admin_token = node.login(admin_username, admin_initial)
        node.expect(200, "/api/identity/change-password", {
            "old_password": admin_initial, "new_password": admin_password,
        }, token=initial_admin_token)
        admin_token = node.login(admin_username, admin_password)
        admin_me, _ = node.expect(200, "/api/identity/me", {}, token=admin_token)
        admin_id = admin_me["account"]["id"]
        assert_regular_session(node, admin_token, admin_id)
        private_json(root / "admin.storage.json", cookie_state(node, admin_token))

        role_response, _ = node.expect(201, "/api/roles/create", {
            "name": "Fixture routing reader", "description": "Isolated outer config diagnosis",
            "permission_codes": ["RoutingRules.View"],
        }, token=admin_token)
        role_id = role_response["role"]["id"]
        member_response, _ = node.expect(201, "/api/accounts/create", {
            "username": member_username, "display_name": "Fixture routing reader",
        }, token=admin_token)
        member_id = member_response["account"]["id"]
        node.expect(200, "/api/accounts/set-roles", {
            "account_id": member_id, "role_ids": [role_id],
        }, token=admin_token)
        initial_member_token = node.login(member_username, member_initial)
        node.expect(200, "/api/identity/change-password", {
            "old_password": member_initial, "new_password": member_password,
        }, token=initial_member_token)
        member_token = node.login(member_username, member_password)
        assert_regular_session(node, member_token, member_id)
        if permission_codes(node, member_token) != {"RoutingRules.View"}:
            raise AssertionError("Baseline member has unexpected permissions")
        private_json(root / "routing.storage.json", cookie_state(node, member_token))
        baseline = check_pair(node, member_token, 403)
        admin_check = check_pair(node, admin_token, 200)
        if admin_check["is_db_connected"] is not True:
            raise AssertionError("Fixture config store is not connected")
        state = {
            "fixture_kind": FIXTURE_KIND, "repo": str(args.repo.resolve()),
            "binary": str(binary), "base_url": node.url, "public_origin": node.public_origin,
            "port": node.port, "root": str(root), "owner_pid": os.getpid(),
            "server_pid": node.process.pid, "role_id": role_id,
            "admin_id": admin_id, "admin_username": admin_username,
            "admin_password": admin_password, "admin_token": admin_token,
            "member_id": member_id, "member_username": member_username,
            "member_password": member_password, "member_token": member_token,
            "settings_granted": False,
        }
        private_json(root / STATE_NAME, state)
        summary = {
            "fixture_kind": FIXTURE_KIND, "phase": "baseline-ready", "port": node.port,
            "base_url": node.url, "public_origin": node.public_origin,
            "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
            "owner_pid": os.getpid(), "server_pid": node.process.pid,
            "baseline": baseline, "admin": admin_check,
            "admin_storage": str(root / "admin.storage.json"),
            "routing_storage": str(root / "routing.storage.json"),
            "both_password_changes_completed": True, "settings_granted": False,
        }
        private_json(root / "summary.json", summary)
        emit(status="baseline-ready", port=node.port, root=str(root), summary=str(root / "summary.json"),
             admin_storage=str(root / "admin.storage.json"), routing_storage=str(root / "routing.storage.json"),
             member_config=403, member_routing=200, admin_config=200, admin_routing=200)
        while not stop_requested:
            if node.process.poll() is not None:
                raise RuntimeError("Owned EE process exited")
            time.sleep(0.25)
    finally:
        node.stop()
        emit(status="owned-server-stopped", root=str(root))


def attach(root):
    state_path = root / STATE_NAME
    if state_path.stat().st_mode & 0o077:
        raise RuntimeError("Private state has unsafe permissions")
    state = json.loads(state_path.read_text(encoding="utf-8"))
    if state.get("fixture_kind") != FIXTURE_KIND or state.get("root") != str(root):
        raise RuntimeError("Not this fixture's private state")
    if state["port"] == 59817 or state["base_url"] != f'http://127.0.0.1:{state["port"]}':
        raise RuntimeError("Not an isolated loopback fixture")
    Node = node_class(Path(state["repo"]))
    # Reuse existing HTTP helper methods without Node.__init__, which would
    # allocate a new port and overwrite config.json. This never starts a process.
    node = Node.__new__(Node)
    node.url, node.port, node.public_origin = state["base_url"], state["port"], state["public_origin"]
    node.cookies = http.cookiejar.CookieJar()
    node.client = node.new_client(node.cookies)
    node.timeout = 12
    assert_regular_session(node, state["admin_token"], state["admin_id"])
    assert_regular_session(node, state["member_token"], state["member_id"])
    return node, state


def grant_settings(args):
    root = temp_root(args.root)
    node, state = attach(root)
    if state["settings_granted"]:
        emit(status="already-granted", root=str(root), summary=str(root / "comparison.json"))
        return
    if permission_codes(node, state["member_token"]) != {"RoutingRules.View"}:
        raise AssertionError("Baseline permissions changed outside this fixture")
    before = check_pair(node, state["member_token"], 403)
    node.expect(200, "/api/roles/update", {
        "role_id": state["role_id"], "name": "Fixture routing reader",
        "description": "Isolated outer config diagnosis: Settings.View control",
        "permission_codes": ["RoutingRules.View", "Settings.View"],
    }, token=state["admin_token"])
    # Explicit new login for the SAME account. Keep the baseline storage file
    # untouched for provenance, and use a new context for the comparison.
    member_token = node.login(state["member_username"], state["member_password"])
    assert_regular_session(node, member_token, state["member_id"])
    if permission_codes(node, member_token) != {"RoutingRules.View", "Settings.View"}:
        raise AssertionError("Comparison member has unexpected permissions")
    after = check_pair(node, member_token, 200)
    if after["is_db_connected"] is not True:
        raise AssertionError("Comparison config store is not connected")
    private_json(root / "routing-settings.storage.json", cookie_state(node, member_token))
    state["member_token"] = member_token
    state["settings_granted"] = True
    private_json(root / STATE_NAME, state)
    comparison = {"fixture_kind": FIXTURE_KIND, "phase": "settings-granted", "same_account": True,
                  "only_permission_added": "Settings.View", "before": before, "after": after,
                  "routing_storage": str(root / "routing-settings.storage.json")}
    private_json(root / "comparison.json", comparison)
    emit(status="settings-granted-same-account", port=node.port, root=str(root),
         comparison=str(root / "comparison.json"), routing_storage=str(root / "routing-settings.storage.json"),
         member_config=200, member_routing=200)


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    starter = sub.add_parser("start", help="Start and hold a fresh real EE fixture in the foreground")
    starter.add_argument("--binary", required=True, type=Path)
    starter.add_argument("--root", required=True, type=Path, help="Fresh empty child of the system temporary directory")
    starter.add_argument("--repo", type=Path, default=DEFAULT_REPO, help="Read-only source of identity-smoke.Node")
    starter.add_argument("--public-origin", help="Optional isolated full-UI origin; default is the random EE origin")
    grant = sub.add_parser("grant-settings", help="Run only after baseline browser evidence has been captured")
    grant.add_argument("--root", required=True, type=Path)
    args = parser.parse_args()
    try:
        if args.command == "start":
            start(args)
        else:
            grant_settings(args)
    except Exception as exc:
        # Deliberately never serialize HTTP bodies, headers, cookie jars or
        # private state into tool-visible errors. Node.expect only names paths.
        emit(status="failed", error_type=type(exc).__name__,
             detail="Inspect the private fixture log and endpoint status; no secrets emitted")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
