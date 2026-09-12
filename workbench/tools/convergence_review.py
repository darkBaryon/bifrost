#!/usr/bin/env python3
"""收敛评审启动器：在新的 Claude Code 会话中执行收敛评审，并在后台监控进度。

实施方只提供 checklist.yaml 与轮次。提示词由本脚本从 templates/收敛评审提示词.md 与
checklist.yaml 的「收敛评审」段机械生成，评审范围取 git diff 的完整文件清单，评审原文由
本脚本原样保存为 收敛评审<R>.md。实施方不撰写或改写提示词、不限定范围、不改写评审原文。

首轮不带 --since，整包评审「基线..HEAD」；后续轮带 --since <上一轮候选 commit>，只评审差量并
定向复核上一轮修复项。每轮都是新会话，模型与推理强度固定为 claude-opus-5 / high。

用法（在项目根执行）:
    python3 workbench/tools/convergence_review.py start --config <checklist.yaml> --round <R> [--since <commit>] [--dry-run]
    python3 workbench/tools/convergence_review.py status --config <checklist.yaml> --round <R>
    python3 workbench/tools/convergence_review.py wait --config <checklist.yaml> --round <R> [--timeout <秒>]
"""
from __future__ import annotations

import argparse
import datetime
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import time
from pathlib import Path

try:
    import yaml
except ImportError:
    print("需要 pyyaml：pip install pyyaml", file=sys.stderr)
    raise

WORKBENCH = Path(__file__).resolve().parents[1]
DEFAULT_REPO = WORKBENCH.parent
PROMPT_TEMPLATE = WORKBENCH / "templates" / "收敛评审提示词.md"
RUNS_DIR = WORKBENCH / "tmp" / "convergence"

MODEL = "claude-opus-5"
EFFORT = "high"
RUN_TIMEOUT_SECONDS = 90 * 60
POLL_SECONDS = 30
VERDICTS = ("无碍", "仅立债挂账", "有Blocking")
# 评审者只读：文件读取、搜索与只读 git 查询；禁止写文件、派生 agent 和联网。
ALLOWED_TOOLS = [
    "Read", "Grep", "Glob",
    "Bash(git diff *)", "Bash(git show *)", "Bash(git log *)", "Bash(git status *)",
    "Bash(git ls-files *)", "Bash(git rev-parse *)", "Bash(git merge-base *)", "Bash(git grep *)",
]
DISALLOWED_TOOLS = ["Edit", "Write", "NotebookEdit", "Agent", "Workflow", "WebFetch", "WebSearch"]


def git(repo: Path, *args: str) -> str:
    # 关闭路径转义，中文路径按原文输出，才能正确过滤并交给评审者阅读。
    return subprocess.run(["git", "-c", "core.quotepath=off", *args], cwd=repo, capture_output=True,
                          text=True, check=True).stdout.strip()


def latest(phase_dir: Path, prefix: str) -> Path | None:
    """返回 期<N> 目录下编号最大的「<prefix><数字>.md」，例如 方案v2.md、代码评审4.md。"""
    found = []
    for path in phase_dir.glob(f"{prefix}*.md"):
        m = re.fullmatch(re.escape(prefix) + r"(\d+)\.md", path.name)
        if m:
            found.append((int(m.group(1)), path))
    return max(found)[1] if found else None


def section(markdown: str, heading: str) -> str:
    """取「## heading」到下一个二级标题之间的正文；heading 按前缀匹配。"""
    lines, out, inside = markdown.splitlines(), [], False
    for line in lines:
        if line.startswith("## "):
            if inside:
                break
            inside = line[3:].strip().startswith(heading)
            continue
        if inside:
            out.append(line)
    return "\n".join(out).strip()


def render_prompt(template: str, values: dict[str, str], extra: list[str]) -> str:
    """把模板 text 代码块中的占位符逐个替换；模板缺少任一占位符即报错，防止模板与脚本脱节。"""
    m = re.search(r"```text\n(.*?)\n```", template, re.S)
    if not m:
        raise ValueError("提示词模板缺少 ```text 代码块")
    prompt = m.group(1)
    for placeholder, value in values.items():
        if placeholder not in prompt:
            raise ValueError(f"提示词模板缺少占位符 {placeholder}，请同步脚本与模板")
        prompt = prompt.replace(placeholder, value)
    return "\n\n".join([prompt, *extra]).strip() + "\n"


def normalize_record(text: str) -> str:
    """去掉回复外层的代码围栏与 frontmatter 之前的文字，并校验记录 frontmatter。"""
    text = re.sub(r"^```(?:markdown|md)?\n", "", text.strip())
    text = re.sub(r"\n```$", "", text)
    start = text.find("---\n")
    if start < 0:
        raise ValueError("回复中没有 frontmatter")
    record = text[start:].rstrip() + "\n"
    front = record.split("---\n", 2)
    if len(front) < 3:
        raise ValueError("frontmatter 未闭合")
    meta = yaml.safe_load(front[1]) or {}
    if meta.get("type") != "收敛评审" or meta.get("verdict") not in VERDICTS:
        raise ValueError(f"frontmatter 的 type/verdict 不合规：{meta.get('type')!r}/{meta.get('verdict')!r}")
    return record


class Run:
    """一次评审运行的文件位置：提示词、元数据、事件流与日志都放在被忽略的 tmp 目录。"""

    def __init__(self, config: Path, round_no: int):
        self.phase_dir = config.resolve().parent
        self.case = self.phase_dir.parent.name
        self.phase = int(self.phase_dir.name.removeprefix("期"))
        self.round = round_no
        self.record = self.phase_dir / f"收敛评审{round_no}.md"
        self.dir = RUNS_DIR / f"{self.case}-期{self.phase}-收敛评审{round_no}"
        self.meta_file = self.dir / "meta.json"
        self.stream = self.dir / "stream.jsonl"

    def meta(self) -> dict:
        return json.loads(self.meta_file.read_text(encoding="utf-8"))

    def save(self, meta: dict) -> None:
        self.meta_file.write_text(json.dumps(meta, ensure_ascii=False, indent=2), encoding="utf-8")


def build_prompt(run: Run, cfg: dict, repo: Path, since: str | None) -> tuple[str, str]:
    review = cfg.get("收敛评审") or {}
    level, base = review.get("风险等级"), review.get("基线")
    if level not in ("L1", "L2", "L3") or not base:
        raise ValueError("checklist.yaml 的「收敛评审」段须给出 风险等级(L1/L2/L3) 与 基线")
    # 候选代码必须已提交；workbench 下的过程记录不在评审范围，不影响启动。
    if git(repo, "status", "--porcelain", "--", ".", ":(exclude)workbench"):
        raise ValueError("workbench 之外的工作树不干净：先提交候选，再启动评审")
    head = git(repo, "rev-parse", "HEAD")
    start = since or base
    files = [f for f in git(repo, "diff", "--name-only", f"{start}..{head}").splitlines() if not f.startswith("workbench/")]
    if not files:
        raise ValueError(f"{start}..{head} 在 workbench 之外没有改动")
    plan = latest(run.phase_dir, "方案v")
    if plan is None:
        raise ValueError("期目录下没有 方案v<N>.md")
    plan_text = plan.read_text(encoding="utf-8")
    scope = section(plan_text, "给用户的摘要") or section(plan_text, "3.")
    guides = review.get("开发指南") or []
    values = {
        "<仓库绝对路径>": str(repo),
        "<L1/L2/L3>": level,
        "<分支>": git(repo, "rev-parse", "--abbrev-ref", "HEAD"),
        "<基线 commit>": base,
        "<未提交工作树 / 某 commit>": f"commit {head}（工作树干净）",
        "<逐文件列出绝对/仓库相对路径>": "\n".join(files),
        "<一段话：做什么、不做什么>": scope,
        "<方案路径>": str(plan.relative_to(repo)),
        "<开发指南路径>": "、".join(guides) or "（无）",
    }
    extra = []
    if since:
        previous = run.phase_dir / f"收敛评审{run.round - 1}.md"
        delta = (f"本轮为差量复核：上一轮候选 {since}，本轮候选 {head}。评审范围是上面列出的差量文件"
                 f"（git diff {since}..{head}），读这些文件的完整现状，不重做整包评审。")
        if previous.exists():
            delta += f"必须读取上一轮记录 {previous.relative_to(repo)}，对其中「本轮修复项」逐条给出处置（已修复 / 有依据撤回 / 仍未关闭），附证据。"
        extra.append(delta)
        code_review = latest(run.phase_dir, "代码评审")
        handoff = section(code_review.read_text(encoding="utf-8"), "转收敛评审") if code_review else ""
        if handoff:
            extra.append(f"代码评审记录 {code_review.relative_to(repo)} 的「转收敛评审」栏原文如下，逐条处置（修复 / 撤回 / 挂账）：\n{handoff}")
    extra.append(f"记录 frontmatter 取值：case: {run.case}；phase: {run.phase}；round: {run.round}；"
                 f"created: {datetime.date.today().isoformat()}。启动脚本会把你的最终回复原样保存为 "
                 f"{run.record.relative_to(repo)}。")
    return render_prompt(PROMPT_TEMPLATE.read_text(encoding="utf-8"), values, extra), head


def resolve_claude(env: dict) -> str:
    found = os.environ.get("CLAUDE_BIN") or shutil.which("claude", path=env["PATH"])
    if not found:
        raise FileNotFoundError("找不到 claude 可执行文件；可用环境变量 CLAUDE_BIN 指定")
    return found


def cmd_start(args) -> int:
    run, repo = Run(Path(args.config), args.round), Path(args.repo).resolve()
    if run.record.exists():
        print(f"{run.record} 已存在，不覆盖", file=sys.stderr)
        return 1
    cfg = yaml.safe_load(Path(args.config).read_text(encoding="utf-8")) or {}
    try:
        prompt, head = build_prompt(run, cfg, repo, args.since)
    except (ValueError, subprocess.CalledProcessError) as e:
        print(f"无法启动：{e}", file=sys.stderr)
        return 1
    if args.dry_run:
        print(prompt)
        return 0
    if run.dir.exists():
        print(f"{run.dir} 已有运行记录；确认作废后删除该目录再启动", file=sys.stderr)
        return 1
    run.dir.mkdir(parents=True)
    (run.dir / "prompt.md").write_text(prompt, encoding="utf-8")
    run.save({"case": run.case, "phase": run.phase, "round": run.round, "candidate": head,
              "since": args.since, "model": MODEL, "effort": EFFORT, "repo": str(repo),
              "record": str(run.record), "status": "starting", "started": time.time()})
    with open(run.dir / "runner.log", "w", encoding="utf-8") as log:
        subprocess.Popen([sys.executable, str(Path(__file__).resolve()), "_run", "--config",
                          str(Path(args.config).resolve()), "--round", str(args.round)], cwd=repo, stdin=subprocess.DEVNULL,
                         stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
    print(f"已在后台启动第 {run.round} 轮收敛评审（{MODEL} / {EFFORT}，候选 {head[:9]}）；运行目录 {run.dir}")
    return 0


def cmd_run(args) -> int:
    """后台执行体：运行 claude，结束后把合规的最终回复落盘为记录。"""
    run = Run(Path(args.config), args.round)
    meta = run.meta()
    meta["runner_pid"] = os.getpid()
    env = dict(os.environ)
    env["PATH"] = os.pathsep.join([env.get("PATH", ""), "/opt/homebrew/bin", str(Path.home() / ".local/bin")])
    cmd = [resolve_claude(env), "-p", "--model", MODEL, "--effort", EFFORT,
           "--output-format", "stream-json", "--verbose", "--permission-mode", "dontAsk",
           "--no-session-persistence", "--allowedTools", ",".join(ALLOWED_TOOLS),
           "--disallowedTools", ",".join(DISALLOWED_TOOLS)]
    with open(run.dir / "prompt.md", encoding="utf-8") as prompt, open(run.stream, "w", encoding="utf-8") as out, \
            open(run.dir / "stderr.log", "w", encoding="utf-8") as err:
        proc = subprocess.Popen(cmd, cwd=meta["repo"], stdin=prompt, stdout=out, stderr=err, env=env)
        meta.update(status="running", pid=proc.pid)
        run.save(meta)
        try:
            proc.wait(timeout=RUN_TIMEOUT_SECONDS)
        except subprocess.TimeoutExpired:
            proc.send_signal(signal.SIGTERM)
            proc.wait()
            meta.update(status="failed", reason=f"超过 {RUN_TIMEOUT_SECONDS // 60} 分钟未完成", finished=time.time())
            run.save(meta)
            return 1
    result = next((e for e in reversed(events(run.stream)) if e.get("type") == "result"), None)
    meta.update(finished=time.time(), cost_usd=(result or {}).get("total_cost_usd"))
    try:
        if not result or result.get("is_error") or result.get("subtype") != "success":
            raise ValueError(f"评审会话未正常结束（退出码 {proc.returncode}）")
        record = normalize_record(result.get("result") or "")
        with open(run.record, "x", encoding="utf-8") as f:
            f.write(record)
        meta.update(status="done", verdict=yaml.safe_load(record.split("---\n", 2)[1])["verdict"])
    except (ValueError, FileExistsError) as e:
        (run.dir / "result.md").write_text((result or {}).get("result") or "", encoding="utf-8")
        meta.update(status="failed", reason=str(e))
    run.save(meta)
    return 0 if meta["status"] == "done" else 1


def events(stream: Path) -> list[dict]:
    out = []
    if stream.exists():
        for line in stream.read_text(encoding="utf-8").splitlines():
            try:
                out.append(json.loads(line))
            except json.JSONDecodeError:
                continue
    return out


def progress(run: Run) -> dict:
    meta = run.meta()
    if meta.get("status") in ("starting", "running"):
        # 以后台执行体是否存活为准；claude 退出后执行体还要落盘记录。
        runner = meta.get("runner_pid")
        try:
            if runner is None:
                if time.time() - meta["started"] > 120:
                    raise OSError
            else:
                os.kill(runner, 0)
        except OSError:
            meta.update(status="failed", reason="后台执行体已退出但未完成落盘；查看 runner.log 与 stderr.log")
    tools = sum(1 for e in events(run.stream) if e.get("type") == "assistant"
                for c in e.get("message", {}).get("content", []) if c.get("type") == "tool_use")
    idle = time.time() - run.stream.stat().st_mtime if run.stream.exists() else None
    return {**meta, "tool_calls": tools, "idle_seconds": idle}


def describe(p: dict) -> str:
    elapsed = int(((p.get("finished") or time.time()) - p["started"]) // 60)
    line = f"第 {p['round']} 轮：{p['status']}，已用 {elapsed} 分钟，工具调用 {p['tool_calls']} 次"
    if p["status"] == "running" and p["idle_seconds"] is not None:
        line += f"，最近活动 {int(p['idle_seconds'])} 秒前"
    if p["status"] == "done":
        line += f"，verdict={p['verdict']}，记录 {p['record']}"
    if p["status"] == "failed":
        line += f"，原因：{p.get('reason')}"
    return line


def cmd_status(args) -> int:
    run = Run(Path(args.config), args.round)
    if not run.meta_file.exists():
        print("没有运行记录", file=sys.stderr)
        return 1
    p = progress(run)
    print(describe(p))
    return 0 if p["status"] in ("starting", "running", "done") else 1


def cmd_wait(args) -> int:
    run, deadline = Run(Path(args.config), args.round), time.time() + args.timeout
    while True:
        p = progress(run)
        if p["status"] not in ("starting", "running") or time.time() > deadline:
            print(describe(p))
            return 0 if p["status"] == "done" else 1
        time.sleep(POLL_SECONDS)


def main() -> int:
    ap = argparse.ArgumentParser(description="收敛评审启动器")
    sub = ap.add_subparsers(dest="command", required=True)
    for name in ("start", "status", "wait", "_run"):
        p = sub.add_parser(name)
        p.add_argument("--config", required=True, help="cases/<案子>/期<N>/checklist.yaml")
        p.add_argument("--round", required=True, type=int)
        if name == "start":
            p.add_argument("--since", help="差量轮：上一轮评审的候选 commit")
            p.add_argument("--repo", default=str(DEFAULT_REPO))
            p.add_argument("--dry-run", action="store_true", help="只打印生成的提示词")
        if name == "wait":
            p.add_argument("--timeout", type=int, default=RUN_TIMEOUT_SECONDS)
    args = ap.parse_args()
    return {"start": cmd_start, "status": cmd_status, "wait": cmd_wait, "_run": cmd_run}[args.command](args)


if __name__ == "__main__":
    sys.exit(main())
