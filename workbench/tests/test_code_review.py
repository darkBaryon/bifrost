from workbench.tools.code_review import VERDICT_RE, frontmatter, section

CONVERGENCE = (
    "## 出口清单\n\n### 本轮修复项\n\n- R1\n\n### 转代码评审(范围外发现,一行一条,不出结论)\n\n- 第一条\n- 第二条\n\n"
    "## 台账对账\n\n无\n"
)


def test_section_level3_stops_at_higher_heading():
    assert section(CONVERGENCE, "转代码评审", level=3) == "- 第一条\n- 第二条"
    assert section(CONVERGENCE, "本轮修复项", level=3) == "- R1"
    assert section(CONVERGENCE, "不存在", level=3) == ""


def test_verdict_regex_takes_bold_and_fullwidth_colon():
    body = "**verdict：需修改。** 说明\n\n...\n\nverdict: 通过"
    assert VERDICT_RE.findall(body) == ["需修改", "通过"]
    assert VERDICT_RE.findall("没有结论") == []


def test_frontmatter_reads_relates():
    text = "---\ntitle: x\nrelates: [国内定价]\n---\n\n正文"
    assert frontmatter(text)["relates"] == ["国内定价"]
    assert frontmatter("没有 frontmatter") == {}
