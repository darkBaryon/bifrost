#!/usr/bin/env python3
"""把工作台 Markdown 渲染成一个干净、好看的静态站点（site/）。

设计取舍：不依赖 mkdocs / 第三方主题，纯标准库自带渲染器 + 一套手写主题
（assets/styles.css + assets/app.js），保证工作台自包含、可离线、可二次定制。

导航树、阅读顺序、当前页高亮全部来自 build_views.py 的统一导航模型
（与侧栏 SUMMARY、顶部下拉同源），新增需求/方案零配置自动进站点。

用法:
    python3 tools/build_site.py          # 先刷新视图，再生成 site/
"""
from __future__ import annotations

import html
import re
import shutil
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import build_views as bv  # noqa: E402

ROOT = bv.ROOT
SITE = ROOT / "site"
ASSETS = ROOT / "assets"
SITE_NAME = "XHS 开发工作台"

# 不作为页面渲染的路径片段
SKIP_PARTS = {"templates", "tools", "assets", "site", ".git"}
SKIP_NAMES = {"SUMMARY.md", "README.md"}


# ─────────────────────────────── Markdown ───────────────────────────────

ADMONITION = {
    "note": ("提示", "ⓘ"), "info": ("说明", "ⓘ"), "tip": ("建议", "✓"),
    "warning": ("注意", "▲"), "danger": ("禁止", "✕"), "rule": ("硬规则", "§"),
    "example": ("示例", "›"),
}


def slugify(text: str) -> str:
    text = re.sub(r"<[^>]+>", "", text)
    text = text.strip().lower()
    text = re.sub(r"[^\w一-鿿]+", "-", text)
    return text.strip("-") or "section"


def inline(text: str) -> str:
    """行内：转义 → 代码 → 粗体 → 斜体 → 链接。代码段先抽出占位，避免内部被二次处理。"""
    holds: list[str] = []

    def stash(s: str) -> str:
        holds.append(s)
        return f"\x00{len(holds) - 1}\x00"

    # 行内代码（先抽出，原文保留，仅转义）
    text = re.sub(r"`([^`]+)`",
                  lambda m: stash(f"<code>{html.escape(m.group(1))}</code>"), text)
    text = html.escape(text)
    # 链接 [label](href)
    text = re.sub(r"\[([^\]]+)\]\(([^)]+)\)",
                  lambda m: stash(link(m.group(1), m.group(2))), text)
    text = re.sub(r"\*\*([^*]+)\*\*", r"<strong>\1</strong>", text)
    text = re.sub(r"(?<![\w*])\*([^*\n]+)\*(?![\w*])", r"<em>\1</em>", text)
    for i, s in enumerate(holds):
        text = text.replace(f"\x00{i}\x00", s)
    return text


def link(label: str, href: str) -> str:
    label = html.escape(label)
    if re.match(r"^https?://", href) or href.startswith("#"):
        ext = ' target="_blank" rel="noopener"' if href.startswith("http") else ""
        return f'<a href="{html.escape(href)}"{ext}>{label}</a>'
    href = re.sub(r"\.md(#.*)?$", lambda m: ".html" + (m.group(1) or ""), href)
    return f'<a href="{html.escape(href)}">{label}</a>'


def render_markdown(text: str) -> tuple[str, list[tuple[int, str, str]]]:
    """返回 (html, toc)；toc 为 [(level, slug, title)]。"""
    lines = text.split("\n")
    out: list[str] = []
    toc: list[tuple[int, str, str]] = []
    i, n = 0, len(lines)
    seen: dict[str, int] = {}

    def uniq(slug: str) -> str:
        if slug in seen:
            seen[slug] += 1
            return f"{slug}-{seen[slug]}"
        seen[slug] = 0
        return slug

    while i < n:
        line = lines[i]
        stripped = line.strip()

        if stripped.startswith("<!--") and "generated" in stripped:
            i += 1
            continue

        # 代码围栏
        m = re.match(r"^```\s*([\w-]*)", stripped)
        if m:
            lang = m.group(1)
            buf = []
            i += 1
            while i < n and not lines[i].strip().startswith("```"):
                buf.append(lines[i])
                i += 1
            i += 1
            code = html.escape("\n".join(buf))
            label = f'<span class="code-lang">{html.escape(lang)}</span>' if lang else ""
            out.append(
                f'<div class="codeblock">{label}'
                f'<button class="copy" type="button" aria-label="复制">复制</button>'
                f'<pre><code>{code}</code></pre></div>')
            continue

        # admonition: !!! type "title"
        m = re.match(r'^!!!\s+(\w+)(?:\s+"([^"]*)")?\s*$', stripped)
        if m:
            kind = m.group(1).lower()
            default_title, icon = ADMONITION.get(kind, ("提示", "ⓘ"))
            title = m.group(2) or default_title
            body = []
            i += 1
            while i < n and (lines[i].startswith("    ") or not lines[i].strip()):
                if not lines[i].strip() and (i + 1 >= n or not lines[i + 1].startswith("    ")):
                    break
                body.append(lines[i][4:] if lines[i].startswith("    ") else "")
                i += 1
            inner, _ = render_markdown("\n".join(body))
            out.append(
                f'<div class="admonition adm-{html.escape(kind)}">'
                f'<p class="adm-title"><span class="adm-icon">{icon}</span>{html.escape(title)}</p>'
                f'{inner}</div>')
            continue

        # 表格
        if stripped.startswith("|") and stripped.endswith("|"):
            rows = []
            while i < n and lines[i].strip().startswith("|"):
                rows.append([c.strip() for c in lines[i].strip().strip("|").split("|")])
                i += 1
            out.append(render_table(rows))
            continue

        # 标题
        m = re.match(r"^(#{1,6})\s+(.*)$", stripped)
        if m:
            level = len(m.group(1))
            raw = m.group(2).strip()
            slug = uniq(slugify(raw))
            inner = inline(raw)
            if 2 <= level <= 3:
                toc.append((level, slug, re.sub(r"<[^>]+>", "", inner)))
            out.append(
                f'<h{level} id="{slug}">{inner}'
                f'<a class="headerlink" href="#{slug}" aria-label="锚点">¶</a></h{level}>')
            i += 1
            continue

        # 水平线
        if re.match(r"^(-{3,}|\*{3,}|_{3,})$", stripped):
            out.append("<hr>")
            i += 1
            continue

        # 引用块
        if stripped.startswith(">"):
            buf = []
            while i < n and lines[i].strip().startswith(">"):
                buf.append(re.sub(r"^\s*>\s?", "", lines[i]))
                i += 1
            inner, _ = render_markdown("\n".join(buf))
            out.append(f"<blockquote>{inner}</blockquote>")
            continue

        # 列表（有序/无序，按缩进嵌套）
        if re.match(r"^(\s*)([-*+]|\d+\.)\s+", line):
            block, i = collect_list(lines, i)
            out.append(render_list(block))
            continue

        if not stripped:
            i += 1
            continue

        # 段落（合并到空行）
        buf = [stripped]
        i += 1
        while i < n and lines[i].strip() and not re.match(
                r"^(#{1,6}\s|```|\||>|!!!|\s*([-*+]|\d+\.)\s|(-{3,}|\*{3,}|_{3,})$)",
                lines[i].strip() if lines[i].strip().startswith(("#", "`", "|", ">", "!")) else lines[i]):
            buf.append(lines[i].strip())
            i += 1
        out.append(f"<p>{inline(' '.join(buf))}</p>")

    return "\n".join(out), toc


def render_table(rows: list[list[str]]) -> str:
    rows = [r for r in rows if not all(set(c) <= {"-", ":", " "} for c in r)]
    if not rows:
        return ""
    head, body = rows[0], rows[1:]
    th = "".join(f"<th>{inline(c)}</th>" for c in head)
    trs = "".join("<tr>" + "".join(f"<td>{inline(c)}</td>" for c in r) + "</tr>" for r in body)
    return f"<div class='table-wrap'><table><thead><tr>{th}</tr></thead><tbody>{trs}</tbody></table></div>"


def collect_list(lines: list[str], i: int) -> tuple[list[str], int]:
    n = len(lines)
    block = []
    while i < n:
        if re.match(r"^(\s*)([-*+]|\d+\.)\s+", lines[i]):
            block.append(lines[i])
            i += 1
        elif lines[i].startswith(("    ", "\t")) and lines[i].strip():
            block.append(lines[i])  # 续行/子项
            i += 1
        elif not lines[i].strip() and i + 1 < n and re.match(r"^(\s*)([-*+]|\d+\.)\s+", lines[i + 1]):
            i += 1  # 列表项间空行
        else:
            break
    return block, i


def render_list(block: list[str]) -> str:
    """按缩进递归构建嵌套列表。"""
    def indent(s: str) -> int:
        return len(s) - len(s.lstrip(" "))

    items = []  # (indent, ordered, content)
    for raw in block:
        m = re.match(r"^(\s*)([-*+]|\d+\.)\s+(.*)$", raw)
        if m:
            items.append([indent(raw), bool(re.match(r"\d+\.", m.group(2))), m.group(3)])
        elif items:
            items[-1][2] += " " + raw.strip()

    def build(idx: int, level: int) -> tuple[str, int]:
        ordered = items[idx][1] if idx < len(items) else False
        tag = "ol" if ordered else "ul"
        html_items = []
        while idx < len(items) and items[idx][0] >= level:
            if items[idx][0] > level:
                child, idx = build(idx, items[idx][0])
                if html_items:
                    html_items[-1] = html_items[-1][:-5] + child + "</li>"
                continue
            html_items.append(f"<li>{inline(items[idx][2])}</li>")
            idx += 1
        return f"<{tag}>{''.join(html_items)}</{tag}>", idx

    out, _ = build(0, items[0][0] if items else 0)
    return out


# ─────────────────────────────── 导航 / 站点 ───────────────────────────────

def discover_pages() -> list[Path]:
    pages = []
    for path in ROOT.rglob("*.md"):
        rel = path.relative_to(ROOT)
        if set(rel.parts) & SKIP_PARTS or rel.name in SKIP_NAMES:
            continue
        pages.append(path)
    return pages


def flatten(model) -> list[str]:
    order: list[str] = []

    def walk(nodes):
        for nd in nodes:
            if "path" in nd:
                order.append(nd["path"])
            else:
                walk(nd["children"])
    walk(model)
    return order


def out_href(path: str, prefix: str) -> str:
    return prefix + re.sub(r"\.md$", ".html", path)


def sidebar_html(model, current: str, prefix: str) -> str:
    def node(nd, depth):
        if "path" in nd:
            cls = "side-link" + (" active" if nd["path"] == current else "")
            return (f'<a class="{cls}" href="{out_href(nd["path"], prefix)}">'
                    f'{html.escape(nd["label"])}</a>')
        kids = "".join(node(c, depth + 1) for c in nd["children"])
        open_attr = " open" if contains(nd, current) else ""
        return (f'<details class="side-group side-d{depth}"{open_attr}>'
                f'<summary>{html.escape(nd["label"])}</summary>'
                f'<div class="side-children">{kids}</div></details>')

    def contains(nd, target):
        if "path" in nd:
            return nd["path"] == target
        return any(contains(c, target) for c in nd["children"])

    return "".join(node(n, 0) for n in model)


def topnav_html(model, prefix: str) -> str:
    """顶部 tab：每个顶层分区一个 tab，hover/点击展开下拉。"""
    def items(nodes, depth):
        h = ""
        for nd in nodes:
            if "path" in nd:
                h += (f'<a class="dd-link dd-d{depth}" '
                      f'href="{out_href(nd["path"], prefix)}">{html.escape(nd["label"])}</a>')
            else:
                h += f'<div class="dd-group dd-d{depth}">{html.escape(nd["label"])}</div>'
                h += items(nd["children"], depth + 1)
        return h

    tabs = ""
    for sec in model:
        first = out_href(first_path(sec), prefix)
        dd = f'<div class="dropdown">{items(sec.get("children", []), 0)}</div>'
        tabs += (f'<div class="tab"><a class="tab-link" href="{first}">'
                 f'{html.escape(sec["label"])}</a>{dd}</div>')
    return tabs


def first_path(nd) -> str:
    if "path" in nd:
        return nd["path"]
    for c in nd["children"]:
        return first_path(c)
    return "index.md"


def path_labels(model) -> dict:
    """path → 导航标签，供面包屑/上下页用人话标题而非文件名。"""
    out: dict = {}

    def walk(nodes):
        for nd in nodes:
            if "path" in nd:
                out.setdefault(nd["path"], nd["label"])
            else:
                walk(nd["children"])
    walk(model)
    return out


def breadcrumb(rel: str, title: str, prefix: str) -> str:
    parts = rel.split("/")
    crumbs = [f'<a href="{prefix}index.html">{SITE_NAME}</a>']
    for p in parts[:-1]:
        crumbs.append(html.escape(p))
    if rel != "index.md":
        crumbs.append(html.escape(title))
    return ' <span class="sep">/</span> '.join(crumbs)


def toc_html(toc) -> str:
    if len(toc) < 2:
        return ""
    items = "".join(
        f'<a class="toc-l{level} toc-link" href="#{slug}">{html.escape(title)}</a>'
        for level, slug, title in toc)
    return f'<aside class="toc"><div class="toc-title">本页目录</div>{items}</aside>'


def page_html(title, body, toc, nav_side, nav_top, crumb, prevnext, prefix) -> str:
    toc_block = toc_html(toc)
    return f"""<!doctype html>
<html lang="zh-CN" data-theme="light">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{html.escape(title)} · {SITE_NAME}</title>
<link rel="stylesheet" href="{prefix}assets/styles.css">
</head>
<body>
<header class="topbar">
  <button class="menu-btn" aria-label="菜单">☰</button>
  <a class="brand" href="{prefix}index.html">{SITE_NAME}</a>
  <nav class="tabs">{nav_top}</nav>
  <button class="theme-btn" aria-label="切换主题">◐</button>
</header>
<div class="layout">
  <nav class="sidebar"><div class="sidebar-inner">{nav_side}</div></nav>
  <main class="content">
    <div class="breadcrumb">{crumb}</div>
    <article class="md">{body}</article>
    <div class="prevnext">{prevnext}</div>
  </main>
  {toc_block}
</div>
<div class="scrim"></div>
<script src="{prefix}assets/nav.js"></script>
<script src="{prefix}assets/app.js"></script>
</body>
</html>
"""


def main() -> int:
    # 1. 先刷新视图（保证 index/views/SUMMARY/nav 与源文档一致）
    rc = bv.main()
    if rc != 0:
        return rc

    docs, _ = bv.collect()
    cases = bv.group_cases(docs)
    model = bv.nav_model(docs, cases)
    order = flatten(model)
    labels = path_labels(model)

    if SITE.exists():
        shutil.rmtree(SITE)
    SITE.mkdir(parents=True)
    shutil.copytree(ASSETS, SITE / "assets")

    pages = discover_pages()
    rel_paths = {p.relative_to(ROOT).as_posix() for p in pages}

    for path in pages:
        rel = path.relative_to(ROOT).as_posix()
        depth = rel.count("/")
        prefix = "../" * depth
        raw = path.read_text(encoding="utf-8")
        # 去 frontmatter
        if raw.startswith("---\n"):
            end = raw.find("\n---\n", 4)
            if end >= 0:
                raw = raw[end + 5:]
        body, toc = render_markdown(raw)
        m = re.search(r"<h1[^>]*>(.*?)</h1>", body)
        title = re.sub(r"<[^>]+>", "", m.group(1)) if m else path.stem

        nav_side = sidebar_html(model, rel, prefix)
        nav_top = topnav_html(model, prefix)
        crumb = breadcrumb(rel, title, prefix)
        prevnext = build_prevnext(rel, order, rel_paths, labels, prefix)

        target = SITE / re.sub(r"\.md$", ".html", rel)
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(
            page_html(title, body, toc, nav_side, nav_top, crumb, prevnext, prefix),
            encoding="utf-8")

    print(f"站点已生成：{SITE}  （{len(pages)} 页）")
    return 0


def build_prevnext(rel, order, rel_paths, labels, prefix) -> str:
    seq = [p for p in order if p in rel_paths]
    if rel not in seq:
        return ""
    idx = seq.index(rel)
    parts = []
    if idx > 0:
        p = seq[idx - 1]
        parts.append(f'<a class="pn prev" href="{out_href(p, prefix)}">'
                     f'<span>上一页</span>{html.escape(labels.get(p, label_of(p)))}</a>')
    else:
        parts.append("<span></span>")
    if idx < len(seq) - 1:
        p = seq[idx + 1]
        parts.append(f'<a class="pn next" href="{out_href(p, prefix)}">'
                     f'<span>下一页</span>{html.escape(labels.get(p, label_of(p)))}</a>')
    return "".join(parts)


def label_of(path: str) -> str:
    name = path.rsplit("/", 1)[-1]
    return re.sub(r"\.md$", "", name)


if __name__ == "__main__":
    raise SystemExit(main())
