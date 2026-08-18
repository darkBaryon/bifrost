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
