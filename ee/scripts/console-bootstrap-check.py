#!/usr/bin/env python3
"""在临时源码副本检查工作台基础信息；不修改原工作树或复制 node_modules。"""
from __future__ import annotations

import argparse
from collections import Counter
import difflib
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

SOURCE_ROOTS = ("ui", "ee", "core", "framework", "plugins", "transports")
FIXTURE = "workbench/evidence/控制台登录与权限接入-期1-外层配置验证/fixture.py"
TEST_CONFIG = "ee/ui/console-bootstrap.vitest.config.ts"
FORMAT_BASELINE = "fa3eacedc28891cc45bc5107c350457cf196e42e"


def git(repo, *arguments):
    return subprocess.check_output(["git", "-C", str(repo), *arguments])


def source_files(repo):
    names = git(repo, "ls-files", "-z", "--cached", "--others", "--exclude-standard",
                "--", ".editorconfig", *SOURCE_ROOTS, FIXTURE).decode().split("\0")
    generated = ("ui/app/enterprise/", "ui/out/", "ee/tmp/", "ee/cmd/bifrost-http/ui/",
                 "ee/transports/bifrost-http/ui/", "transports/bifrost-http/ui/")
    files = []
    for name in sorted(set(filter(None, names))):
        path = repo / name
        if name.startswith(generated) or "node_modules" in path.parts or name.endswith(".tsbuildinfo"):
            continue
        if path.is_symlink():
            raise RuntimeError("Source snapshot refuses symlinks: " + name)
        if path.is_file():
            files.append(name)
    return files


def source_fingerprint(repo):
    digest = hashlib.sha256()
    files = source_files(repo)
    hashes = {}
    for name in files:
        file_hash = hashlib.sha256((repo / name).read_bytes()).hexdigest()
        hashes[name] = file_hash
        digest.update(name.encode() + b"\0")
        digest.update(bytes.fromhex(file_hash))
    return {"sha256": digest.hexdigest(), "file_count": len(files), "files": hashes}


def temporary_root(raw=None):
    if raw is None:
        return Path(tempfile.mkdtemp(prefix="bifrost-console-check-"))
    path = Path(raw).expanduser().resolve()
    bases = {Path("/tmp").resolve(), Path(tempfile.gettempdir()).resolve()}
    if not any(path != base and path.is_relative_to(base) for base in bases):
        raise RuntimeError("Build root must be a new child of a system temporary directory")
    if path.exists():
        raise RuntimeError("Build root already exists; never reuse or delete existing contents")
    path.mkdir(mode=0o700, parents=True)
    return path


def dependencies(repo, explicit=None):
    # 仓库及同级依赖目录是本机便利兜底；其他环境应通过 --node-modules 显式指定。
    candidates = [explicit, os.environ.get("CONSOLE_UI_NODE_MODULES"), repo / "ui/node_modules",
                  repo.parent / "bifrost/ui/node_modules", repo.parent / "bifrost-permissions/ui/node_modules"]
    for raw in candidates:
        if raw and (Path(raw).expanduser() / "vite/bin/vite.js").is_file():
            return Path(raw).expanduser().resolve()
    raise RuntimeError("Existing UI dependencies not found; pass --node-modules (no automatic install)")


def run(command, cwd, root, name, env=None):
    print("CHECK " + name, flush=True)
    with (root / (name + ".log")).open("w") as log:
        log.write("COMMAND " + json.dumps(list(map(str, command))) + "\n")
        log.flush()
        result = subprocess.run(list(map(str, command)), cwd=cwd, env=env,
                                stdout=log, stderr=subprocess.STDOUT)
    if result.returncode:
        raise RuntimeError(name + " failed; inspect " + str(root / (name + ".log")))


def snapshot(repo, root):
    before = source_fingerprint(repo)
    target = root / "source"
    for name in source_files(repo):
        destination = target / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(repo / name, destination)
        if hashlib.sha256(destination.read_bytes()).hexdigest() != before["files"][name]:
            raise RuntimeError("Snapshot copy differs from source fingerprint: " + name)
    if source_fingerprint(repo) != before:
        raise RuntimeError("Source changed while creating snapshot; retry after edits settle")
    return target, before


def check_sidebar_format(repo, source, root, modules, env):
    """只容许固定基线已有的格式差异；比较无行号、无上下文的逐块内容及次数。"""
    name = "ui/components/sidebar.tsx"
    versions = {"baseline": git(repo, "show", FORMAT_BASELINE + ":" + name).decode(),
                "candidate": (source / name).read_text()}
    debts = {}
    for version, text in versions.items():
        formatted = subprocess.run([str(modules / ".bin/oxfmt"), "--stdin-filepath", str(source / name)],
                                   input=text, text=True, capture_output=True, cwd=source / "ui", env=env, check=True).stdout
        before, after = text.splitlines(keepends=True), formatted.splitlines(keepends=True)
        debts[version] = [(tuple(before[a:b]), tuple(after[c:d]))
                         for op, a, b, c, d in difflib.SequenceMatcher(None, before, after, autojunk=False).get_opcodes()
                         if op != "equal"]
    extra = Counter(debts["candidate"]) - Counter(debts["baseline"])
    (root / "format-sidebar-debt.json").write_text(json.dumps(
        {"baseline_commit": FORMAT_BASELINE, "comparison": "exact changed-line blocks, including multiplicity; no context or line numbers",
         "baseline_blocks": debts["baseline"], "candidate_blocks": debts["candidate"], "new_blocks": list(extra.elements())},
        ensure_ascii=False, indent=2) + "\n")
    if extra:
        raise RuntimeError("sidebar.tsx introduces formatting differences beyond the fixed baseline; inspect format-sidebar-debt.json")
    print("CHECK sidebar-format-debt (fixed baseline; no new differences)", flush=True)


def check(args):
    repo = Path(git(args.repo.resolve(), "rev-parse", "--show-toplevel").decode().strip())
    root = temporary_root(args.output)
    source, fingerprint = snapshot(repo, root)
    modules = dependencies(repo, args.node_modules)
    for directory in ("ui", "ee/ui"):
        (source / directory / "node_modules").symlink_to(modules, target_is_directory=True)
    env = {k: v for k, v in os.environ.items() if k != "NODE_TLS_REJECT_UNAUTHORIZED"
           and not k.startswith(("BIFROST_", "EE_", "OPENAI_", "ANTHROPIC_", "AWS_", "AZURE_"))}
    env.update(NODE_ENV="production", GOWORK="off", GOMAXPROCS="2")
    manifest = {"kind": "console-bootstrap-build-v1", "repo": str(repo), "source": str(source),
                "head": git(repo, "rev-parse", "HEAD").decode().strip(), "source_fingerprint": fingerprint,
                "node_modules": str(modules), "checks": [], "binary": None}
    manifest_path = root / "build-manifest.json"
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    print("DEPENDENCIES " + str(modules), flush=True)
    print("EVIDENCE " + str(root), flush=True)
    # 固定范围保证提交之后的预飞仍会执行，而不依赖空的 git diff HEAD。
    check_sidebar_format(repo, source, root, modules, env)
    manifest["checks"].append("sidebar-format-debt")
    owned = {"ui/app/clientLayout.tsx", "ui/components/topbar.tsx",
             "ui/lib/types/console.ts", "ui/app/_fallbacks/enterprise/hooks/useConsoleConfig.ts",
             "ee/ui/app/enterprise/hooks/useConsoleConfig.ts", TEST_CONFIG}
    formats = [source / n for n in source_files(repo) if (n in owned or "console-bootstrap" in n
               or n.startswith("ee/ui/app/enterprise/hooks/"))
               and Path(n).suffix in {".ts", ".tsx", ".mts", ".json"}]
    if formats:
        run([modules / ".bin/oxfmt", "--check", *formats], source / "ui", root, "format", env)
        manifest["checks"].append("format")
    for mode in ("ee", "oss"):
        overlay = source / "ui/app/enterprise"
        if mode == "ee":
            run(["bash", source / "ee/ui/sync.sh"], source, root, "ee-overlay", env)
        elif overlay.exists():
            shutil.rmtree(overlay)  # Only the freshly generated directory under this run's root.
        run(["node", modules / "vite/bin/vite.js", "build", "--configLoader", "runner"],
            source / "ui", root, mode + "-build", env)
        run(["node", modules / "typescript/bin/tsc", "--noEmit", "--incremental", "false"],
            source / "ui", root, mode + "-typecheck", env)
        manifest["checks"].extend([mode + "-build", mode + "-typecheck"])
        if mode == "ee":
            config = source / TEST_CONFIG
            if not config.is_file():
                raise RuntimeError("Required dedicated tests missing: " + TEST_CONFIG)
            run(["node", modules / "vitest/vitest.mjs", "run", "--config", config],
                source / "ui", root, "ee-component-unit-tests", env)
            manifest["checks"].append("ee-component-unit-tests")
            shutil.copytree(source / "ui/out", root / "ee-ui")
            if args.build_binary:
                embed = source / "ee/cmd/bifrost-http/ui"
                embed.parent.mkdir(parents=True, exist_ok=True)
                shutil.copytree(source / "ui/out", embed)
                binary = root / "bifrost-http"
                run(["go", "build", "-mod=readonly", "-p", "2", "-buildvcs=false", "-o", binary, "./cmd/bifrost-http"],
                    source / "ee", root, "ee-binary", env)
                manifest["binary"] = {"path": str(binary), "sha256": hashlib.sha256(binary.read_bytes()).hexdigest()}
                manifest["checks"].append("ee-binary")
        else:
            shutil.copytree(source / "ui/out", root / "oss-ui")
    if source_fingerprint(repo) != fingerprint:
        raise RuntimeError("Source changed during verification; do not use this build for candidate acceptance")
    manifest["complete"] = True
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"status": "passed", "manifest": str(manifest_path)}, ensure_ascii=False), flush=True)
    return manifest_path


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=Path.cwd())
    parser.add_argument("--node-modules", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--build-binary", action="store_true")
    args = parser.parse_args()
    try:
        check(args)
    except Exception as error:
        print("FAIL " + str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
