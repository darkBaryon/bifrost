#!/usr/bin/env python3
"""把 Gitleaks 默认规则转换成密钥检测器的数据文件，并合入本仓的修正与新增规则。

用法：
  python3 ee/scripts/secrets-rules-convert.py <gitleaks.toml> \
      --extra ee/internal/guardrails/detectors/secrets/data/rules.extra.json \
      --out   ee/internal/guardrails/detectors/secrets/data/rules.json

转换规则：
- 丢弃只按文件路径判断的规则和规则里的 path 条件（网关扫描的是对话文本，没有路径）。
- 规则级白名单：含 paths 且 condition 为 AND 的整条丢弃（永远无法满足）；其余去掉 paths 部分。
- 等级：id 以 generic- 开头的为 medium，其余为 high；extra 文件可覆盖。
- 终止符：把 Gitleaks 面向代码的终止符字符类补上中英文句读，对话里"密钥是 xxx，…"才不会漏（见 TERMINATOR_NEW）。
- extra 文件：replace 按 id 覆盖整条规则；remove 删除；add 追加；keyword_regex_patch 对指定规则追加关键词并替换正则片段。
输出按 id 排序，便于 diff。
"""
from __future__ import annotations

import argparse
import json
import sys
import tomllib
from pathlib import Path


def convert_allowlist(allow: dict) -> dict | None:
    if allow.get("paths"):
        if allow.get("condition", "OR").upper() == "AND":
            return None
    out = {"target": allow.get("regexTarget", "secret")}
    if allow.get("regexes"):
        out["regexes"] = allow["regexes"]
    if allow.get("stopwords"):
        out["stop_words"] = sorted({w.lower() for w in allow["stopwords"]})
    if "regexes" not in out and "stop_words" not in out:
        return None
    return out


# Gitleaks 的终止符面向代码扫描：密钥后面只认反引号、引号、空白、分号、字面 \n\r 或行尾。
# 对话文本里"我的密钥是 xxx，帮我看看""the key is xxx, please" 才是常态，逗号句号等标点会让整条规则漏检，
# 所以统一把中英文常见句读补进终止符字符类。只放宽边界字符，不改前缀与长度约束。
TERMINATOR_OLD = r"""(?:[\x60'"\s;]|\\[nr]|$)"""
TERMINATOR_NEW = r"""(?:[\x60'"\s;,.:!?)\]}]|[，。、；：！？）】》」"']|\\[nr]|$)"""


def patch_terminator(regex: str) -> str:
    return regex.replace(TERMINATOR_OLD, TERMINATOR_NEW)


def convert_rule(rule: dict) -> dict | None:
    if "regex" not in rule:
        return None
    out = {
        "id": rule["id"],
        "description": rule.get("description", ""),
        "regex": patch_terminator(rule["regex"]),
        "keywords": sorted({k.lower() for k in rule.get("keywords", [])}),
        "entropy": float(rule.get("entropy", 0)),
        "secret_group": int(rule.get("secretGroup", 0)),
        "level": "medium" if rule["id"].startswith("generic-") else "high",
    }
    allows = [a for a in (convert_allowlist(x) for x in rule.get("allowlists", [])) if a]
    if allows:
        out["allow"] = allows
    return out


def apply_extra(rules: dict[str, dict], extra: dict) -> None:
    for rid in extra.get("remove", []):
        rules.pop(rid, None)
    for rule in extra.get("replace", []):
        if rule["id"] not in rules:
            sys.exit(f"replace target not found: {rule['id']}")
        rules[rule["id"]] = normalize_extra(rule)
    for rule in extra.get("add", []):
        if rule["id"] in rules:
            sys.exit(f"add target already exists: {rule['id']}")
        rules[rule["id"]] = normalize_extra(rule)
    for patch in extra.get("keyword_regex_patch", []):
        rule = rules[patch["id"]]
        rule["keywords"] = sorted(set(rule["keywords"]) | {k.lower() for k in patch.get("keywords", [])})
        for old, new in patch.get("regex_replace", []):
            if old not in rule["regex"]:
                sys.exit(f"regex fragment not found in {patch['id']}: {old}")
            rule["regex"] = rule["regex"].replace(old, new, 1)


def normalize_extra(rule: dict) -> dict:
    return {
        "id": rule["id"],
        "description": rule.get("description", ""),
        "regex": patch_terminator(rule["regex"]),
        "keywords": sorted({k.lower() for k in rule.get("keywords", [])}),
        "entropy": float(rule.get("entropy", 0)),
        "secret_group": int(rule.get("secret_group", 0)),
        "level": rule.get("level", "high"),
        **({"allow": rule["allow"]} if rule.get("allow") else {}),
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("toml", type=Path)
    parser.add_argument("--extra", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    source = tomllib.load(args.toml.open("rb"))
    extra = json.loads(args.extra.read_text(encoding="utf-8"))
    rules: dict[str, dict] = {}
    for raw in source["rules"]:
        rule = convert_rule(raw)
        if rule:
            rules[rule["id"]] = rule
    apply_extra(rules, extra)
    global_allow = source.get("allowlist", {})
    out = {
        "source": {"gitleaks_version": extra["gitleaks_version"], "min_version": source.get("minVersion", ""), "extra": args.extra.name},
        "global_allow": {
            "regexes": global_allow.get("regexes", []),
            "stop_words": sorted({w.lower() for w in global_allow.get("stopwords", [])}),
        },
        "rules": [rules[k] for k in sorted(rules)],
    }
    args.out.write_text(json.dumps(out, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    print(f"{len(out['rules'])} rules written to {args.out}")


if __name__ == "__main__":
    main()
