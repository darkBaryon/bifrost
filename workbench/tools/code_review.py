#!/usr/bin/env python3
"""代码评审启动器：在新的 Codex 会话中执行独立代码评审，并在后台监控进度。

与 convergence_review.py 同款分工：实施方只提供 checklist.yaml 与轮次。提示词由本脚本从
templates/代码评审提示词.md 与 checklist.yaml 的「收敛评审」段（风险等级、基线）机械生成，评审范围
取 git diff 的完整文件清单，评审原文由本脚本加上 frontmatter 后保存为 代码评审<R>.md。实施方不撰写
或改写提示词、不限定范围、不改写评审原文。

首轮不带 --since，整包评审「基线..HEAD」；后续轮带 --since <上一轮候选 commit>，只核上一轮修复项、
声明修改与受影响区域，并把最新收敛评审记录的「转代码评审」栏原样交给评审者处置。每轮都是新会话，
模型与推理强度固定为 gpt-6-astra / high。

评审者以 workspace-write 沙箱运行（开 network_access 才能跑本地 HTTP 冒烟，产物写到本轮 TMPDIR）；结束时
校验工作树干净且候选到 HEAD 之间 workbench 之外零差异，评审者一旦改动了跟踪文件，本轮判失败、记录不落盘。
实施方在评审期间提交 workbench 过程记录不影响校验。

用法（在项目根执行）:
    python3 workbench/tools/code_review.py start --config <checklist.yaml> --round <R> [--since <commit>] [--dry-run]
    python3 workbench/tools/code_review.py status --config <checklist.yaml> --round <R>
    python3 workbench/tools/code_review.py wait --config <checklist.yaml> --round <R> [--timeout <秒>]
    python3 workbench/tools/code_review.py finalize --config <checklist.yaml> --round <R>   # 落盘失败后（如校验误判）重做落盘
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
PROMPT_TEMPLATE = WORKBENCH / "templates" / "代码评审提示词.md"
RUNS_DIR = WORKBENCH / "tmp" / "codereview"

MODEL = "gpt-6-astra"
EFFORT = "high"
RUN_TIMEOUT_SECONDS = 120 * 60
POLL_SECONDS = 30
VERDICTS = ("通过", "需修改", "驳回")
VERDICT_RE = re.compile(r"verdict\s*[:：]\s*\**\s*(通过|需修改|驳回)")
# Go 工具链在 Claude Code 的 shell 里不在 PATH；评审者跑 checklist 命令需要它。
EXTRA_PATH = ["/opt/homebrew/bin", "/opt/homebrew/Cellar/go/1.27.1/libexec/bin", str(Path.home() / "go/bin")]


def git(repo: Path, *args: str) -> str:
    return subprocess.run(["git", "-c", "core.quotepath=off", *args], cwd=repo, capture_output=True,
                          text=True, check=True).stdout.strip()


def latest(phase_dir: Path, prefix: str) -> Path | None:
    found = []
    for path in phase_dir.glob(f"{prefix}*.md"):
        m = re.fullmatch(re.escape(prefix) + r"(\d+)\.md", path.name)
        if m:
            found.append((int(m.group(1)), path))
    return max(found)[1] if found else None


def section(markdown: str, heading: str, level: int = 2) -> str:
    """取「## heading」（或指定层级）到下一个同级或更高级标题之间的正文；heading 按前缀匹配。"""
    mark = "#" * level + " "
    out, inside = [], False
    for line in markdown.splitlines():
        if line.startswith("#") and not line.startswith(mark) and len(line) - len(line.lstrip("#")) < level and inside:
            break
        if line.startswith(mark):
            if inside:
                break
            inside = line[len(mark):].strip().startswith(heading)
            continue
        if inside:
            out.append(line)
    return "\n".join(out).strip()


def frontmatter(markdown: str) -> dict:
    parts = markdown.split("---\n", 2)
    return (yaml.safe_load(parts[1]) or {}) if len(parts) >= 3 else {}


def render_prompt(template: str, values: dict[str, str], extra: list[str]) -> str:
    m = re.search(r"```text\n(.*?)\n```", template, re.S)
    if not m:
        raise ValueError("提示词模板缺少 ```text 代码块")
    prompt = m.group(1)
    for placeholder, value in values.items():
        if placeholder not in prompt:
            raise ValueError(f"提示词模板缺少占位符 {placeholder}，请同步脚本与模板")
        prompt = prompt.replace(placeholder, value)
    return "\n\n".join([prompt, *extra]).strip() + "\n"


class Run:
    def __init__(self, config: Path, round_no: int):
        self.phase_dir = config.resolve().parent
        self.case = self.phase_dir.parent.name
        self.phase = int(self.phase_dir.name.removeprefix("期"))
        self.round = round_no
        self.record = self.phase_dir / f"代码评审{round_no}.md"
        self.dir = RUNS_DIR / f"{self.case}-期{self.phase}-代码评审{round_no}"
        self.meta_file = self.dir / "meta.json"
        self.stream = self.dir / "stream.jsonl"
        self.last = self.dir / "last.md"
        self.scratch = self.dir / "scratch"

    def meta(self) -> dict:
        return json.loads(self.meta_file.read_text(encoding="utf-8"))

    def save(self, meta: dict) -> None:
        self.meta_file.write_text(json.dumps(meta, ensure_ascii=False, indent=2), encoding="utf-8")


def tree_changed(repo: Path, candidate: str) -> str:
    """评审者不得改动 workbench 之外的任何跟踪文件：工作树须干净，候选到 HEAD 之间 workbench 之外须零差异。
    实施方在评审期间提交 workbench 过程记录是允许的，不算改动。返回空串表示未改动，否则返回说明。"""
    dirty = git(repo, "status", "--porcelain", "--", ".", ":(exclude)workbench")
    if dirty:
        return "工作树有未提交改动：\n" + dirty
    head = git(repo, "rev-parse", "HEAD")
    committed = git(repo, "diff", "--name-only", f"{candidate}..{head}", "--", ".", ":(exclude)workbench")
    if committed:
        return f"候选 {candidate[:9]} 到 HEAD {head[:9]} 之间 workbench 之外有改动：\n" + committed
    return ""


def build_prompt(run: Run, cfg: dict, repo: Path, since: str | None) -> tuple[str, str]:
    review = cfg.get("收敛评审") or {}
    level, base = review.get("风险等级"), review.get("基线")
    if level not in ("L1", "L2", "L3") or not base:
        raise ValueError("checklist.yaml 的「收敛评审」段须给出 风险等级(L1/L2/L3) 与 基线")
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
    relates = frontmatter(plan_text).get("relates") or [run.case]
    report = WORKBENCH / "reports" / f"{relates[0]}.md"
    if not report.exists():
        raise ValueError(f"找不到需求文件 {report}")
    scope = section(plan_text, "给用户的摘要") or section(plan_text, "3.")
    evidence = sorted(p.relative_to(repo).as_posix() for p in (WORKBENCH / "evidence").glob(f"{run.case}-期{run.phase}-*"))
    values = {
        "<仓库绝对路径>": str(repo),
        "<需求路径>": report.relative_to(repo).as_posix(),
        "<方案路径>": plan.relative_to(repo).as_posix(),
        "<L1/L2/L3>": level,
        "<分支>": git(repo, "rev-parse", "--abbrev-ref", "HEAD"),
        "<基线 commit>": base,
        "<未提交工作树 / 某 commit>": f"commit {head}（工作树干净）",
        "<逐文件列出路径>": "\n".join(files + [f"证据（只读）：{e}" for e in evidence]),
        "<一段话，或指向需求文件>": f"验收口径以 {report.relative_to(repo).as_posix()} 的「验收口径」为准；产品范围见方案「给用户的摘要」：\n{scope}",
    }
    extra = [f"评审产物（二进制、临时目录）写到 {run.scratch}（已设为 TMPDIR）。Go 工具链已在 PATH；ee 模块命令用 `cd ee && GOWORK=off go ...`。"
             "checklist.yaml 在方案同目录，逐条亲核其命令与产物。"]
    if since:
        previous = run.phase_dir / f"代码评审{run.round - 1}.md"
        delta = (f"本轮为差量复核：上一轮候选 {since}，本轮候选 {head}。评审范围是上面列出的差量文件"
                 f"（git diff {since}..{head}），读这些文件的完整现状；只核上一轮意见、声明修改和受影响区域，"
                 f"无法证明未修改区域不受影响时恢复全量。")
        if previous.exists():
            delta += f"必须读取上一轮记录 {previous.relative_to(repo).as_posix()}，对其中「本轮修复项」逐条给出处置（已修复 / 有依据撤回 / 仍未关闭），附证据。"
        extra.append(delta)
        convergence = latest(run.phase_dir, "收敛评审")
        handoff = section(convergence.read_text(encoding="utf-8"), "转代码评审", level=3) if convergence else ""
        if handoff:
            extra.append(f"收敛评审记录 {convergence.relative_to(repo).as_posix()} 的「转代码评审」栏原文如下，逐条处置（修复 / 撤回 / 挂账），每条给依据：\n{handoff}")
    extra.append("最终回复按上述 8 段结构直接输出 Markdown，最后单独一行写 `verdict: 通过` / `verdict: 需修改` / `verdict: 驳回`。"
                 "启动脚本会把你的最终回复原样保存为 " + run.record.relative_to(repo).as_posix() + "。")
    return render_prompt(PROMPT_TEMPLATE.read_text(encoding="utf-8"), values, extra), head


def resolve_codex(env: dict) -> str:
    found = os.environ.get("CODEX_BIN") or shutil.which("codex", path=env["PATH"])
    if not found:
        raise FileNotFoundError("找不到 codex 可执行文件；可用环境变量 CODEX_BIN 指定")
    return found


def cmd_start(args) -> int:
    run, repo = Run(Path(args.config), args.round), Path(args.repo).resolve()
    if run.record.exists() and not args.dry_run:
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
    run.scratch.mkdir(parents=True)
    (run.dir / "prompt.md").write_text(prompt, encoding="utf-8")
    run.save({"case": run.case, "phase": run.phase, "round": run.round, "candidate": head,
              "since": args.since, "model": MODEL, "effort": EFFORT, "repo": str(repo),
              "record": str(run.record), "status": "starting", "started": time.time()})
    with open(run.dir / "runner.log", "w", encoding="utf-8") as log:
        subprocess.Popen([sys.executable, str(Path(__file__).resolve()), "_run", "--config",
                          str(Path(args.config).resolve()), "--round", str(args.round)], cwd=repo, stdin=subprocess.DEVNULL,
                         stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
    print(f"已在后台启动第 {run.round} 轮代码评审（{MODEL} / {EFFORT}，候选 {head[:9]}）；运行目录 {run.dir}")
    return 0


def cmd_run(args) -> int:
    run = Run(Path(args.config), args.round)
    meta = run.meta()
    meta["runner_pid"] = os.getpid()
    env = dict(os.environ)
    env["PATH"] = os.pathsep.join([env.get("PATH", ""), *EXTRA_PATH])
    env["TMPDIR"] = str(run.scratch)
    repo = Path(meta["repo"])
    cmd = [resolve_codex(env), "exec", "-C", str(repo), "-s", "workspace-write",
           "-c", "sandbox_workspace_write.network_access=true",
           "-m", MODEL, "-c", f"model_reasoning_effort={EFFORT}", "--json", "-o", str(run.last), "-"]
    with open(run.dir / "prompt.md", encoding="utf-8") as prompt, open(run.stream, "w", encoding="utf-8") as out, \
            open(run.dir / "stderr.log", "w", encoding="utf-8") as err:
        proc = subprocess.Popen(cmd, cwd=repo, stdin=prompt, stdout=out, stderr=err, env=env)
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
    meta.update(finished=time.time(), thread_id=next((e.get("thread_id") for e in events(run.stream) if e.get("type") == "thread.started"), None))
    if proc.returncode != 0:
        meta.update(status="failed", reason=f"评审会话未正常结束（退出码 {proc.returncode}）")
        run.save(meta)
        return 1
    return finalize(run, meta)


def finalize(run: Run, meta: dict) -> int:
    """把已保存的最终回复落盘为记录；校验评审者未改动工作树。可由 finalize 子命令重跑（例如运行期间 HEAD 因 workbench 提交变化后）。"""
    repo = Path(meta["repo"])
    try:
        if not run.last.exists():
            raise ValueError("没有最终回复文件 last.md")
        changed = tree_changed(repo, meta["candidate"])
        if changed:
            raise ValueError("评审者改动了 workbench 之外的工作树，记录不落盘；" + changed)
        body = run.last.read_text(encoding="utf-8").strip()
        verdicts = VERDICT_RE.findall(body)
        if not verdicts:
            raise ValueError("最终回复没有 verdict 行")
        verdict = verdicts[-1]
        record = "\n".join([
            "---", f"title: {run.case} 期{run.phase} 代码评审 #{run.round}", "type: 代码评审", f"case: {run.case}",
            f"phase: {run.phase}", f"round: {run.round}", f"verdict: {verdict}", f"created: {datetime.date.today().isoformat()}", "---", "",
            f"> 独立评审者：Codex 新会话（{MODEL} / {EFFORT}），由 tools/code_review.py 启动，与实施上下文隔离；以下为其最终回复原文。",
            f"> 基线 {meta['since'] or '（见 checklist 基线）'} 到候选 {meta['candidate']}；评审前后 workbench 之外的工作树与提交均未变动。", "",
            body, ""])
        with open(run.record, "x", encoding="utf-8") as f:
            f.write(record)
        meta.update(status="done", verdict=verdict)
    except (ValueError, FileExistsError) as e:
        meta.update(status="failed", reason=str(e))
    run.save(meta)
    return 0 if meta["status"] == "done" else 1


def cmd_finalize(args) -> int:
    run = Run(Path(args.config), args.round)
    if not run.meta_file.exists():
        print("没有运行记录", file=sys.stderr)
        return 1
    meta = run.meta()
    if meta.get("status") == "done":
        print("已落盘，无需重做", file=sys.stderr)
        return 0
    rc = finalize(run, meta)
    print(describe(progress(run)))
    return rc


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
        runner = meta.get("runner_pid")
        try:
            if runner is None:
                if time.time() - meta["started"] > 120:
                    raise OSError
            else:
                os.kill(runner, 0)
        except OSError:
            meta.update(status="failed", reason="后台执行体已退出但未完成落盘；查看 runner.log 与 stderr.log")
    tools = sum(1 for e in events(run.stream) if e.get("type") == "item.completed"
                and (e.get("item") or {}).get("type") not in (None, "agent_message", "reasoning"))
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
    ap = argparse.ArgumentParser(description="代码评审启动器")
    sub = ap.add_subparsers(dest="command", required=True)
    for name in ("start", "status", "wait", "finalize", "_run"):
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
    return {"start": cmd_start, "status": cmd_status, "wait": cmd_wait, "finalize": cmd_finalize, "_run": cmd_run}[args.command](args)


if __name__ == "__main__":
    sys.exit(main())
