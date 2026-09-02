from workbench.tools.build_views import blueprint_nav_items, blueprint_version, nav_model


def _blueprint(title: str, path: str) -> dict:
    return {
        "title": title,
        "type": "开发蓝图",
        "case": "示例",
        "status": "需修改",
        "relates": ["示例"],
        "created": "2026-07-16",
        "_path": path,
    }


def test_blueprint_versions_and_current_label():
    docs = [
        _blueprint("示例 开发蓝图 v3", "cases/示例/开发蓝图v3.md"),
        _blueprint("示例 开发蓝图 v1", "cases/示例/开发蓝图.md"),
        _blueprint("示例 开发蓝图 v2", "cases/示例/开发蓝图v2.md"),
    ]

    assert [blueprint_version(d) for d in docs] == [3, 1, 2]
    assert [label for label, _ in blueprint_nav_items(docs)] == [
        "蓝图 v1",
        "蓝图 v2",
        "蓝图 v3（当前）",
    ]

    process_section = next(node for node in nav_model(docs, {"示例": docs}) if node["label"] == "过程文档")
    case_node = process_section["children"][0]
    assert [node["label"] for node in case_node["children"][:4]] == [
        "主页",
        "蓝图 v1",
        "蓝图 v2",
        "蓝图 v3（当前）",
    ]


# ── workbench外壳重设计 期1:收敛评审类型 + 视图收敛(方案v3 S2 四断言) ──
from workbench.tools.build_views import is_active, phase_flat, render_index, validate


def _doc(**kw):
    base = {"title": "t", "type": "方案", "case": "示例", "phase": 1,
            "created": "2026-09-02", "_path": "cases/示例/期1/x.md"}
    base.update(kw)
    return base


def test_convergence_verdict_enum_rejects_legacy_value():
    bad = _doc(type="收敛评审", round=1, verdict="通过")  # 旧代码评审枚举值,收敛评审非法
    errs = validate([bad])
    assert any("verdict 非法" in e for e in errs)
    ok = _doc(type="收敛评审", round=1, verdict="仅立债挂账")
    assert not any("verdict" in e for e in validate([ok]))


def test_archived_case_with_convergence_review_absent_from_index():
    plan = _doc(status="已归档", version=4, relates=["示例"])
    conv = _doc(type="收敛评审", round=1, verdict="仅立债挂账")
    assert not is_active(plan) and not is_active(conv)
    page = render_index([plan, conv], "")
    assert "示例" not in page  # 已归档 case 不得因收敛评审重现于活跃工作台


def test_nav_has_no_active_or_bycase_pages():
    model = nav_model([], {})
    flat = []
    def walk(nodes):
        for n in nodes:
            flat.append(n.get("path", ""))
            walk(n.get("children", []))
    walk(model)
    assert not any("views/active.md" in p or "views/by-case.md" in p for p in flat)


def test_timeline_convergence_after_code_review_before_acceptance():
    docs = [
        _doc(type="验收记录"),
        _doc(type="收敛评审", round=1, verdict="无碍"),
        _doc(type="代码评审", round=3, verdict="通过"),
        _doc(version=1, status="已归档"),
    ]
    order = [d["type"] for d in phase_flat(docs)]
    assert order == ["方案", "代码评审", "收敛评审", "验收记录"]
