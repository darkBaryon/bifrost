#!/usr/bin/env python3
"""预飞机械自检（开发流程）：把代码评审里的体力活前置消化成「事实表」。

一鱼三吃：① 实施完自检；② 评审拿事实表只做判断；③ 合入后沉淀为常驻守护。

checklist.yaml 是方案与评审之间的合同，逻辑固定、参数每期换。四类检查：
  必过命令   commands       逐条 run，退出码须 0（可选 expect_empty=stdout 须空）
  必存产物   expect_files   实现完成后这些路径必须存在
  必含符号   must_contain   本期新增的接口/函数/导出必须出现（grep 命中 ≥1）
  禁留标记   forbid         调试残留 / 越界功能等，全仓 grep 须 0 命中

用法:
    python3 tools/preflight.py --config cases/<过程文档名>/期<N>/checklist.yaml \
        [--repo <项目根，默认工作台上一级>] [--out facts.md]

退出码: 0 = 全绿；1 = 有红项（不绿先修，绿了才请评审）。
"""
from __future__ import annotations

import argparse
import subprocess
import sys
from pathlib import Path

try:
    import yaml
except ImportError:
    print("需要 pyyaml：pip install pyyaml", file=sys.stderr)
    raise

DEFAULT_REPO = Path(__file__).resolve().parents[2]  # 工作台在 <项目>/workbench 之下


def run(cmd: str, cwd: Path, timeout: int = 600):
    p = subprocess.run(cmd, shell=True, cwd=str(cwd), capture_output=True,
                       text=True, timeout=timeout)
    return p.returncode, (p.stdout + p.stderr).strip()


def check(cfg: dict, repo: Path, rows: list) -> bool:
    ok = True

    # 1. 必过命令
    for item in cfg.get("必过命令") or []:
        name = item.get("name") or item.get("run", "")
        cmd = item["run"]
        expect_empty = bool(item.get("expect_empty"))
        rc, out = run(cmd, repo)
        green = (rc == 0) and (not out if expect_empty else True)
        ok &= green
        note = "" if green else (out.splitlines()[-1][:140] if out else f"退出码 {rc}")
        rows.append((f"命令 {name}", cmd, "通过" if green else "失败", note))

    # 2. 必存产物
    for rel in cfg.get("必存产物") or []:
        exists = (repo / rel).exists()
        ok &= exists
        rows.append((f"产物 {rel}", f"test -e {rel}", "存在" if exists else "缺失", ""))

    # 3. 必含符号（接口/导出确实落地）
    for item in cfg.get("必含符号") or []:
        word = item["grep"] if isinstance(item, dict) else item
        scope = item.get("in", ".") if isinstance(item, dict) else "."
        cmd = f"grep -rIn --exclude-dir=__pycache__ -- '{word}' {scope}"
        rc, out = run(cmd, repo)
        green = rc == 0 and bool(out)
        ok &= green
        n = len(out.splitlines()) if out else 0
        rows.append((f"必含 {word}", cmd, f"命中 {n}" if green else "未命中",
                     "" if green else "接口/符号未落地"))

    # 4. 禁留标记（调试残留 / 越界功能）
    for item in cfg.get("禁留标记") or []:
        word = item["grep"] if isinstance(item, dict) else item
        scope = item.get("in", ".") if isinstance(item, dict) else "."
        cmd = f"grep -rIn --exclude-dir=__pycache__ -- '{word}' {scope}"
        rc, out = run(cmd, repo)
        green = rc != 0 or not out  # 无命中
        ok &= green
        n = 0 if green else len(out.splitlines())
        rows.append((f"禁留 {word}", cmd, "0 处" if green else f"{n} 处残留",
                     "" if green else out.splitlines()[0][:140]))

    if not rows:
        rows.append(("（空）", "checklist 无检查项", "跳过", "至少填一项以建立合同"))
    return ok


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--config", required=True)
    ap.add_argument("--repo", default=str(DEFAULT_REPO))
    ap.add_argument("--out", help="把事实表另存为 markdown 文件")
    args = ap.parse_args()

    cfg = yaml.safe_load(Path(args.config).read_text(encoding="utf-8")) or {}
    repo = Path(args.repo).resolve()
    rows: list = []
    ok = check(cfg, repo, rows)

    lines = ["# 预飞事实表", "",
             f"- 目标仓库: `{repo}`",
             f"- 参数表: `{args.config}`",
             f"- 结论: **{'全绿' if ok else '有红项'}**", "",
             "| 检查项 | 命令 | 实际 | 备注 |", "|---|---|---|---|"]
    for name, cmd, result, note in rows:
        lines.append(f"| {name} | `{cmd}` | {result} | {note} |")
    table = "\n".join(lines) + "\n"
    print(table)
    if args.out:
        Path(args.out).write_text(table, encoding="utf-8")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
