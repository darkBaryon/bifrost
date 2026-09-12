import pytest

from workbench.tools.convergence_review import latest, normalize_record, render_prompt, section

TEMPLATE = "说明\n\n```text\n仓库 <仓库绝对路径>，等级 <L1/L2/L3>。\n```\n"
RECORD = "---\ntitle: 示例 期1 收敛评审 #2\ntype: 收敛评审\ncase: 示例\nphase: 1\nround: 2\nverdict: 无碍\ncreated: 2026-09-12\n---\n\n## 结论\n\n无碍。\n"


def test_render_prompt_fills_placeholders_and_appends_extra():
    prompt = render_prompt(TEMPLATE, {"<仓库绝对路径>": "/repo", "<L1/L2/L3>": "L3"}, ["差量说明"])
    assert prompt == "仓库 /repo，等级 L3。\n\n差量说明\n"


def test_render_prompt_rejects_template_drift():
    with pytest.raises(ValueError, match="<方案路径>"):
        render_prompt(TEMPLATE, {"<方案路径>": "x"}, [])


def test_section_stops_at_next_heading():
    text = "## 给用户的摘要\n\n做什么。\n\n## 1. 起点\n\n别的"
    assert section(text, "给用户的摘要") == "做什么。"
    assert section(text, "不存在") == ""


def test_latest_picks_highest_round(tmp_path):
    for name in ("代码评审1.md", "代码评审10.md", "代码评审2.md", "代码评审提示词.md"):
        (tmp_path / name).write_text("", encoding="utf-8")
    assert latest(tmp_path, "代码评审").name == "代码评审10.md"
    assert latest(tmp_path, "方案v") is None


def test_normalize_record_strips_wrapper_and_checks_verdict():
    assert normalize_record(f"下面是记录：\n```markdown\n{RECORD}```") == RECORD
    with pytest.raises(ValueError, match="verdict"):
        normalize_record(RECORD.replace("verdict: 无碍", "verdict: 通过"))
