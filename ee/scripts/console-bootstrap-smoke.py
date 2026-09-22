#!/usr/bin/env python3
"""用隔离真实 EE 和完整 UI 验证外壳；基线模式应返回明确的浏览器红灯。"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import runpy
import signal
import subprocess
import sys
import tempfile
import time
import uuid

HELPER = runpy.run_path(str(Path(__file__).with_name("console-bootstrap-check.py")), run_name="console_check_helpers")
FIXTURE = HELPER["FIXTURE"]


def tool_environment():
    return {k: v for k, v in os.environ.items() if k != "NODE_TLS_REJECT_UNAUTHORIZED"}


def cli_command(explicit=None):
    if explicit:
        path = Path(explicit).expanduser().resolve()
        return ["node", str(path)] if path.suffix == ".js" else [str(path)]
    cached = sorted((Path.home() / ".npm/_npx").glob("*/node_modules/@playwright/cli/playwright-cli.js"))
    if cached:
        return ["node", str(cached[-1])]
    wrapper = Path.home() / ".codex/skills/playwright/scripts/playwright_cli.sh"
    if not wrapper.is_file():
        raise RuntimeError("Playwright CLI unavailable; pass --playwright-cli")
    return [str(wrapper)]


class Browser:
    def __init__(self, root, command):
        self.root, self.command = root, command
        self.session = "console-bootstrap-" + uuid.uuid4().hex[:12]
        self.sequence = 0
        self.opened = False

    def call(self, *arguments, result=False):
        self.sequence += 1
        command = [*self.command, "--session", self.session, "--raw", *map(str, arguments)]
        process = subprocess.run(command, cwd=self.root, env=tool_environment(), capture_output=True, text=True, timeout=65)
        text = process.stdout + process.stderr
        (self.root / f"cli-{self.sequence:03}.txt").write_text(text)
        if process.returncode or text.lstrip().startswith("### Error"):
            raise RuntimeError("Playwright CLI failed at step " + str(self.sequence))
        return json.loads(process.stdout) if result else process.stdout

    def open(self, origin):
        self.opened = True
        self.call("open", "about:blank")
        # Record only path/method/status. Never store headers, Cookie, ticket or response bodies.
        self.call("run-code", "async page => { page.__bootstrapResponses = []; page.on('response', r => {"
                  " const prefix = " + json.dumps(origin + "/api/") + "; if (!r.url().startsWith(prefix)) return;"
                  " const path = '/api/' + r.url().slice(prefix.length).split('?')[0];"
                  " page.__bootstrapResponses.push({path,method:r.request().method(),status:r.status(),"
                  " ...(path === '/api/console/bootstrap' ? {emptyBody:r.request().postData() === '{}'} : {})}); }); }")

    def responses(self):
        return self.call("run-code", "async page => page.__bootstrapResponses || []", result=True)

    def settle(self):
        self.call("run-code", "async page => { await page.waitForFunction(() => "
                  "!!document.querySelector('[data-testid=config-unreachable], [data-testid=routing-rules-empty-state]'),"
                  " null, {timeout:30000}); }")
        self.call("snapshot")

    def wait_for_routing_response(self):
        self.call("run-code", "async page => { for (let i=0;i<200;i++) {"
                  " if (page.__bootstrapResponses.some(r => r.path === '/api/routing/rules' && r.method === 'GET' && r.status === 200)) return;"
                  " await page.waitForTimeout(100); } throw new Error('No successful automatic routing response'); }")

    def close(self):
        if self.opened:
            self.call("close")
            self.opened = False


def verified_binary(args, repo, root):
    manifest_path = args.build_manifest
    if args.baseline_only and args.binary is None:
        raise RuntimeError("Baseline mode requires an explicit --binary")
    if args.binary is None and manifest_path is None:
        build = argparse.Namespace(repo=repo, node_modules=args.node_modules, output=root / "build", build_binary=True)
        manifest_path = HELPER["check"](build)
    if args.baseline_only:
        binary = args.binary.expanduser().resolve()
        return binary, {"mode": "baseline-only", "binary": str(binary),
                        "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest()}
    if manifest_path is None:
        raise RuntimeError("Candidate --binary requires --build-manifest to verify current source provenance")
    manifest = json.loads(Path(manifest_path).read_text())
    if manifest.get("kind") != "console-bootstrap-build-v1" or not manifest.get("complete") or not manifest.get("binary"):
        raise RuntimeError("Incomplete or foreign build manifest")
    if manifest["source_fingerprint"] != HELPER["source_fingerprint"](repo):
        raise RuntimeError("Candidate manifest does not match current tracked and untracked source")
    binary = (args.binary or Path(manifest["binary"]["path"])).expanduser().resolve()
    digest = hashlib.sha256(binary.read_bytes()).hexdigest()
    if digest != manifest["binary"]["sha256"]:
        raise RuntimeError("Candidate binary does not match its build manifest")
    fingerprint = manifest["source_fingerprint"]
    return binary, {"mode": "candidate", "binary": str(binary), "binary_sha256": digest,
                    "build_manifest": str(Path(manifest_path).resolve()),
                    "source_fingerprint": {"sha256": fingerprint["sha256"], "file_count": fingerprint["file_count"]}}


def start_fixture(repo, binary, root):
    fixture_root = root / "fixture"
    log = (root / "fixture-process.log").open("w")
    process = subprocess.Popen([sys.executable, "-B", str(repo / FIXTURE), "start", "--repo", str(repo),
                                "--binary", str(binary), "--root", str(fixture_root)],
                               stdout=log, stderr=subprocess.STDOUT, env=tool_environment(), start_new_session=True)
    try:
        deadline = time.monotonic() + 90
        while not (fixture_root / "summary.json").is_file():
            if process.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError("Owned EE fixture failed to become ready; not a baseline UI red")
            time.sleep(.2)
        summary = json.loads((fixture_root / "summary.json").read_text())
        if summary["settings_granted"] or summary["port"] == 59817 or not summary["both_password_changes_completed"]:
            raise RuntimeError("Invalid fixture authority or reserved port")
        return process, log, fixture_root, summary
    except BaseException:
        stop_fixture(process)
        log.close()
        remove_session_files(fixture_root)
        raise


def stop_fixture(process):
    if process and process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=25)
        except subprocess.TimeoutExpired:
            # This process group was created by this run; never kill by port or global process name.
            os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=5)


def remove_session_files(fixture_root):
    for secret in [fixture_root / "private-state.json", *fixture_root.glob("*.storage.json")]:
        secret.unlink(missing_ok=True)


def observe(browser):
    return browser.call("eval", "async () => {"
                        " const config = await fetch('/api/config', {credentials:'include'});"
                        " const routing = await fetch('/api/routing/rules?limit=25&offset=0', {credentials:'include'});"
                        " const permissions = await fetch('/api/permissions/me', {method:'POST',credentials:'include',"
                        " headers:{'Content-Type':'application/json'},body:'{}'});"
                        " const body = await permissions.json();"
                        " return {configStatus:config.status,routingStatus:routing.status,permissionsStatus:permissions.status,"
                        " permissionCodes:(body.permissions||[]).map(x=>x.code).sort(),"
                        " configUnreachable:!!document.querySelector('[data-testid=config-unreachable]'),"
                        " routingVisible:!!document.querySelector('[data-testid=routing-rules-empty-state]'),"
                        " path:location.pathname}; }", result=True)


def error_recovery(browser):
    # Only the failure path is mocked. Recovery must read the real EE endpoint again.
    browser.call("route", "**/api/console/bootstrap", "--status", "503", "--content-type", "application/json",
                 "--body", '{"error":{"code":"unavailable"}}')
    browser.call("reload")
    browser.call("run-code", "async page => { await page.getByTestId('config-unreachable').waitFor({timeout:30000}); }")
    browser.call("snapshot")
    browser.call("unroute", "**/api/console/bootstrap")
    browser.call("click", '[data-testid="config-retry-btn"]')
    browser.call("run-code", "async page => { await page.getByTestId('routing-rules-empty-state').waitFor({timeout:30000}); }")
    browser.call("snapshot")
    return {"injected_status": 503, "unreachable_visible": True, "retry_with_real_backend": True}


def mock_shell_branches(browser, origin):
    """真实登录会话，仅覆盖 bootstrap 响应；日志分支只断言页面挂载，不断言日志数据权限。"""
    fallback = "Configuration changes require a server restart to take effect."
    cases = [
        ("missing-stores", "/workspace/routing-rules", False, False, False, "config-store-documentation-link"),
        ("logs-only", "/workspace/logs", False, True, False, "logs-refresh-btn"),
        ("restart-without-reason", "/workspace/routing-rules", True, False, True, "routing-rules-empty-state"),
    ]
    results = []
    try:
        for name, path, database, logs, restart, test_id in cases:
            body = {"is_db_connected": database, "is_logs_connected": logs, "env_label": None}
            if restart:
                body["restart_required"] = {"required": True}
            browser.call("route", "**/api/console/bootstrap", "--status", "200", "--content-type", "application/json",
                         "--body", json.dumps(body))
            try:
                browser.call("goto", origin + path)  # Full navigation clears prior RTK Query cache.
                browser.call("run-code", "async page => { await page.getByTestId(" + json.dumps(test_id) +
                             ").waitFor({state:'visible',timeout:30000}); }")
                if restart:
                    browser.call("run-code", "async page => { await page.locator('[data-sidebar=sidebar]').getByText(" +
                                 json.dumps(fallback) + ", {exact:true}).waitFor({state:'visible',timeout:30000}); }")
                browser.call("snapshot")
                state = browser.call("eval", "() => ({missingStoreBanner:!!document.querySelector('[data-testid=config-store-documentation-link]'),"
                                     "unreachable:!!document.querySelector('[data-testid=config-unreachable]')})", result=True)
                if state["unreachable"] or state["missingStoreBanner"] != (name == "missing-stores"):
                    raise AssertionError("Mocked bootstrap store flags did not select the expected shell branch")
                results.append({"case": name, "path": path, "bootstrap_response": body, "visible_test_id": test_id,
                                **state, **({"generic_restart_visible": True} if restart else {})})
            finally:
                browser.call("unroute", "**/api/console/bootstrap")
    finally:
        browser.call("run-code", "async page => { page.__bootstrapResponses = []; }")
        browser.call("goto", origin + "/workspace/routing-rules")
        browser.wait_for_routing_response()
        browser.settle()
        restored = [r for r in browser.responses() if r["path"] == "/api/console/bootstrap"]
        if not restored or any(r["status"] != 200 for r in restored):
            raise AssertionError("Real bootstrap did not recover after the isolated UI response mocks")
    return {"kind": "bootstrap-response-mocks", "scope": "shell-render-only; logs data authorization not asserted",
            "cases": results, "route_removed": True, "real_bootstrap_restored": True}


def excluded_pages(browser, origin):
    results = []
    for path in ("/workspace/mcp-sessions/auth-success", "/workspace/mcp-sessions/auth#t=isolated-invalid-temp-token"):
        browser.close()
        browser.open(origin)  # A fresh anonymous context: no state-load for these two modes.
        browser.call("goto", origin + path)
        browser.call("snapshot")
        browser.call("run-code", "async page => { await page.waitForLoadState('networkidle', {timeout:15000}); }")
        state = browser.call("eval", "async () => {const r=await fetch('/api/session/is-auth-enabled');"
                             "const s=await r.json();return {enabled:s.is_auth_enabled,authenticated:s.has_valid_token};}", result=True)
        if state != {"enabled": True, "authenticated": False}:
            raise AssertionError("Public/temp check did not use the authenticated deployment with an anonymous browser")
        responses = browser.responses()
        if any(r["path"] == "/api/console/bootstrap" for r in responses):
            raise AssertionError("Public or temporary-token page queried bootstrap")
        results.append({"path": path.split("#")[0], "bootstrap_requests": 0, "mode": "anonymous-public-or-temp"})
    return results


def smoke(args):
    repo = Path(HELPER["git"](args.repo.resolve(), "rev-parse", "--show-toplevel").decode().strip())
    root = HELPER["temporary_root"](args.output)
    evidence = root / "output/playwright"
    evidence.mkdir(parents=True)
    binary, provenance = verified_binary(args, repo, root)
    browser = Browser(evidence, cli_command(args.playwright_cli))
    process = log = fixture_root = None
    report = {"status": "failed", "provenance": provenance, "evidence": str(evidence)}
    try:
        process, log, fixture_root, summary = start_fixture(repo, binary, root)
        origin = summary["base_url"]
        browser.open(origin)
        browser.call("state-load", fixture_root / "routing.storage.json")
        browser.call("goto", origin + "/workspace/routing-rules")
        browser.settle()
        if not args.baseline_only:
            browser.wait_for_routing_response()
        auto_requests = browser.responses()
        observation = observe(browser)
        report.update(observation=observation, page_requests_before_probes=auto_requests,
                      fixture={"port": summary["port"], "password_change_completed": True, "settings_granted": False})
        browser.call("screenshot", "--filename", evidence / "routing.png")
        if observation["permissionCodes"] != ["RoutingRules.View"] or observation["configStatus"] != 403 or observation["routingStatus"] != 200:
            raise AssertionError("HTTP authority preconditions failed; not a valid UI red or green")
        if observation["configUnreachable"]:
            report.update(status="expected-baseline-red" if args.baseline_only else "candidate-regression",
                          failed_assertion="routing-only account must not render config-unreachable")
            return 1
        if args.baseline_only:
            raise AssertionError("Baseline did not reproduce the expected config-unreachable failure")
        if not observation["routingVisible"]:
            raise AssertionError("Actual routing page did not render")
        bootstrap = [r for r in auto_requests if r["path"] == "/api/console/bootstrap"]
        if not bootstrap or any(r["status"] != 200 or r["method"] != "POST" or not r["emptyBody"] for r in bootstrap):
            raise AssertionError("Page did not successfully use real POST bootstrap with empty JSON body")
        if any(r["path"] == "/api/config" for r in auto_requests):
            raise AssertionError("Outer shell still queries complete config")
        browser.call("click", '[data-testid="topbar-menu-btn"]')
        browser.call("snapshot")
        logout = browser.call("eval", "() => !!document.querySelector('[data-testid=topbar-logout-btn]')", result=True)
        if not logout:
            raise AssertionError("Topbar lost the sign-out entry")
        browser.call("press", "Escape")
        report["logout_entry"] = True
        report["fault_injection"] = error_recovery(browser)
        report["mock_shell_branches"] = mock_shell_branches(browser, origin)
        report["excluded_pages"] = excluded_pages(browser, origin)
        report["status"] = "passed"
        return 0
    except Exception as error:
        report["error_type"] = type(error).__name__
        if isinstance(error, AssertionError):
            # 本脚本的断言均使用固定消息，可公开具体失败条件；第三方错误仍不输出原文。
            report["failed_assertion"] = str(error)
        # Error text from third-party tools or HTTP bodies may contain credentials; keep it private.
        report["failure_step"] = browser.sequence
        return 2
    finally:
        try:
            browser.close()
        finally:
            stop_fixture(process)
            if log:
                log.close()
            if fixture_root and (process is None or process.poll() is not None):
                # Only private session material created in this run, after the owned server stopped.
                remove_session_files(fixture_root)
                report["private_session_files_removed"] = True
            report["owned_services_stopped"] = process is None or process.poll() is not None
            report["owned_browser_closed"] = not browser.opened
            (evidence / "result.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
            print(json.dumps({"status": report["status"], "result": str(evidence / "result.json")}, ensure_ascii=False), flush=True)


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=Path.cwd())
    parser.add_argument("--binary", type=Path)
    parser.add_argument("--build-manifest", type=Path)
    parser.add_argument("--baseline-only", action="store_true")
    parser.add_argument("--node-modules", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--playwright-cli", type=Path)
    args = parser.parse_args()
    try:
        return smoke(args)
    except Exception as error:
        print("FAIL " + str(error), file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
