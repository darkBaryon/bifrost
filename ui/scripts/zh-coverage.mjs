#!/usr/bin/env node
// zh-coverage —— 中文化覆盖率扫描器(方案 v4 §4 契约实现)
// 用法: node scripts/zh-coverage.mjs [--check] [--self-test]
//   默认: 输出未翻译清单(词条⇥次数⇥首处) + 围栏排除统计 + 行级旁路报告
//   --check: 清单非空退出 1(供 preflight)
//   --self-test: 内置断言(fixture 驱动,不依赖线上词条数量)
// 只读,不写任何源文件。零第三方依赖,Node >= 20。

import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const UI_ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const SCAN_DIRS = ["app", "components", "hooks", "lib"];
const ZH_LOCALE = join(UI_ROOT, "lib", "zhLocale.ts");
const WHITELIST = join(UI_ROOT, "scripts", "zh-coverage-whitelist.txt");
const TOPBAR_UTILS = join(UI_ROOT, "components", "topbar.utils.ts");
const CANARY = "XQ7#鑫W9-zk";
const PROPS = ["placeholder", "title", "label", "description", "aria-label", "tooltip"];
// ⑤ 对象属性位可扫键(阶段C 盲区A):与 PROPS 同名但语法位不同(冒号 vs 等号),aria-label 不作对象键
const OBJ_KEYS = ["placeholder", "title", "label", "description", "tooltip", "message", "required", "required_error", "invalid_type_error"];
const ZOD_MESSAGE_METHODS = ["min", "max", "length", "email", "url", "uuid", "regex", "nonempty"];

// ───────────────────────── 归一化(F4: 嵌套花括号朴素切割,错切进清单=响的失败) ─────────────────────────
export function normalize(tpl) {
  let s = tpl, prev;
  do { prev = s; s = s.replace(/\$\{[^{}]*\}/g, "⟨x⟩"); } while (s !== prev);
  // 残留未闭合的 ${ (深嵌套朴素切割失败):保留原样进清单,不静默
  return s.replace(/\s+/g, " ").trim();
}

// 形态围栏:骨架须含 ≥2 个空格分隔的英文词
export function passesFormFence(skeleton) {
  const words = skeleton.split(" ").filter((w) => /[A-Za-z]{2,}/.test(w));
  return words.length >= 2;
}

// ───────────────────────── zhLocale.ts 解析(带对账自检, 评审2-N2 行锚定) ─────────────────────────
export function parseZhLocale(src) {
  const dictBlock = src.match(/const DICT[^=]*=\s*\{([\s\S]*?)\n\};/);
  if (!dictBlock) throw new Error("对账失败: 未找到 DICT 块");
  const dictLines = dictBlock[1].split("\n");
  const anchored = dictLines.filter((l) => /^\s*"/.test(l)).length;
  const dict = new Map();
  for (const l of dictLines) {
    const m = l.match(/^\s*"((?:[^"\\]|\\.)*)":\s*"((?:[^"\\]|\\.)*)",?\s*$/);
    if (m) dict.set(unesc(m[1]), unesc(m[2]));
  }
  if (dict.size !== anchored)
    throw new Error(`对账失败: DICT 解析 ${dict.size} 条 ≠ 行锚定计数 ${anchored}`);

  const rules = [];
  const rulesBlock = src.match(/const RULES[^=]*=\s*\[([\s\S]*?)\n\];/);
  if (rulesBlock) {
    const body = rulesBlock[1];
    const headCount = body.split("\n").filter((l) => /^\s*\{ re:/.test(l)).length;
    const re = /\{ re: \/(.+?)\/([a-z]*), out: "((?:[^"\\]|\\.)*)",\s*\n\s*skeleton: "((?:[^"\\]|\\.)*)", sample: "((?:[^"\\]|\\.)*)" \},?/g;
    let m;
    while ((m = re.exec(body)) !== null) {
      rules.push({ re: new RegExp(m[1], m[2]), out: unesc(m[3]), skeleton: unesc(m[4]), sample: unesc(m[5]) });
    }
    if (rules.length !== headCount)
      throw new Error(`对账失败: RULES 解析 ${rules.length} 条 ≠ 行锚定计数 ${headCount}`);
  }
  return { dict, rules };
}
const unesc = (s) => s.replace(/\\(.)/g, "$1");
export const decodeEnt = (s) => s.replace(/&amp;/g, "&").replace(/&apos;/g, "'").replace(/&quot;/g, '"')
  .replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&#10;/g, "\n");

// 运行时同口径判定:精确 → 小写兜底 → RULES(评审2-3.1.1 对账=骨架逐字符相等在别处)
export function makeTranslate(dict, rules) {
  const lookup = new Map();
  for (const [k, v] of dict) { lookup.set(k, v); const lo = k.toLowerCase(); if (!lookup.has(lo)) lookup.set(lo, v); }
  return (s) => {
    const t = s.trim();
    if (!t) return null;
    const hit = lookup.get(t) ?? lookup.get(t.toLowerCase());
    if (hit !== undefined) return hit === t ? null : hit;
    for (const r of rules) {
      if (r.re.test(t)) {
        const out = t.replace(r.re, r.out);
        return out === t ? null : out; // 收敛契约①
      }
    }
    return null;
  };
}

// F2: sample 必须是 skeleton 的代入实例
export function sampleInstantiatesSkeleton(sample, skeleton) {
  const rx = new RegExp("^" + skeleton.split("⟨x⟩").map(escRx).join("(.*?)") + "$");
  return rx.test(sample);
}
const escRx = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

// ───────────────────────── 白名单(评审2-N3 金丝雀 + \# 转义) ─────────────────────────
export function parseWhitelist(text) {
  const literals = new Set(); const regexes = [];
  for (let raw of text.split("\n")) {
    const line = raw.replace(/(?<!\\) #.*$/, "").trim();
    if (!line || line.startsWith("#")) continue;
    const rm = line.match(/^\/(.+)\/$/);
    if (rm) {
      const rx = new RegExp(rm[1]);
      if (rx.test(CANARY)) throw new Error(`白名单全匹配拒绝: /${rm[1]}/ 命中金丝雀`);
      regexes.push(rx);
    } else literals.add(line.replace(/\\#/g, "#"));
  }
  return { literals, regexes };
}
const inWhitelist = (wl, s) => wl.literals.has(s) || wl.regexes.some((r) => r.test(s));

// ───────────────────────── 四类提取 ─────────────────────────
export function extractFromSource(src, file) {
  const found = [];   // {text, file, line, cat}
  const bypass = [];  // 行级旁路(形态围栏内但位置/上下文围栏外)
  const excluded = { position: 0, form: 0, context: 0 };
  const lines = src.split("\n");
  // ①c 跨行聚合已消费的行号(避免与①同行匹配重复处理同一段落时产生误导性行号)
  const consumedByMultiline = new Set();
  lines.forEach((line, i) => {
    const loc = `${file}:${i + 1}`;
    // ① JSX 文本节点(同行 >text<)
    for (const m of line.matchAll(/>([^<>{}`]+)<(\/?)/g)) {
      const t = m[1].trim();
      if (!t || !/[A-Za-z]{2}/.test(t)) continue;
      // 单词候选须终止于 </(闭合标签),否则是 TS 泛型(>Promise<void>)误报
      if (!t.includes(" ") && m[2] !== "/") continue;
      if (/^[^A-Za-z0-9"'(]/.test(t) || t.includes("&&") || t.includes("=>")) continue; // 代码碎片
      if (/^\(\w+:/.test(t) || /^extends /.test(t)) continue; // TS 类型标注碎片
      found.push({ text: decodeEnt(t), loc, cat: 1 });
    }
    // ①d 自闭合标签后的行尾文本(阶段C 盲区C):`<Icon /> Add Profile` 行尾无 `<` 终止,
    // ①永远不触发。锚定 `/>`(裸 `>text$` 形态实测全仓 0 命中,且会撞箭头函数/泛型,不做)。
    {
      const m = line.match(/\/>\s+([A-Za-z][^<>{}`]*)$/);
      if (m) {
        const t = m[1].trim();
        if (/[A-Za-z]{2}/.test(t)) found.push({ text: decodeEnt(t), loc, cat: 1 });
      }
    }
    // ①c JSX 独立文本 children,按 JSX 空白折叠规则跨行聚合(修复:原①b只取紧邻单行,
    // 漏掉"多行纯文本、最终被闭合标签终止"这一常见写法,截断后的 key 永远命中不上
    // 真实 DOM 完整文本节点)。起点:上一行以 '>' 收尾;逐行累积直到遇到含 <{` 的行
    // (取该行标签前的残余文本后停止)或空行。
    if (!consumedByMultiline.has(i) && i > 0 && /(?<!=)>$/.test(lines[i - 1].trim())) {
      const parts = [];
      let j = i;
      while (j < lines.length) {
        const t = lines[j].trim();
        if (t === "") break;
        const boundary = t.search(/[<{}`]/); // 遇标签/表达式起止符/反引号即止(圆括号在自然语言文案中常见,不作边界)
        if (boundary === 0) break; // 本行本身是标签/表达式开头,不属于文本
        if (boundary > 0) { parts.push(t.slice(0, boundary).trim()); consumedByMultiline.add(j); j++; break; }
        parts.push(t); consumedByMultiline.add(j); j++;
      }
      const text = parts.join(" ").trim();
      if (text && passesFormFence(text) && /^[A-Z0-9"']/.test(text)) {
        found.push({ text: decodeEnt(text), loc, cat: 1 });
      }
    }
    // ② 六 prop 字面量
    for (const p of PROPS) {
      for (const m of line.matchAll(new RegExp(`${p}\\s*=\\s*(?:\\{\\s*)?"([^"]+)"`, "g"))) {
        if (/[A-Za-z]{2}/.test(m[1])) found.push({ text: decodeEnt(m[1]), loc, cat: 2 });
      }
    }
    // ③ toast 字符串字面量
    for (const m of line.matchAll(/toast(?:\.\w+)?\(\s*"([^"]+)"/g)) {
      found.push({ text: m[1], loc, cat: 3 });
    }
    // ⑤ 对象属性位字符串(阶段C 盲区A):`title: "User Success Rate"`。
    // 通道②只认 JSX 等号位;对象字面量冒号位此前不可见。抽样 18/18 全为真实 UI 文案(巡检证据)。
    for (const m of line.matchAll(new RegExp(`(?:^\\s*|[{,(]\\s*)(?:${OBJ_KEYS.join("|")})\\s*:\\s*(["'])((?:\\\\.|(?!\\1).)*)\\1`, "g"))) {
      if (/[A-Za-z]{2}/.test(m[2])) found.push({ text: decodeEnt(m[2]), loc, cat: 5 });
    }
    // ⑤b Zod 校验方法参数(代码评审3 阻断修复):`z.string().min(1, "Name is required")`。
    // 只覆盖 Zod 风格校验方法,避免把普通字符串方法 `.startsWith("http")` 当 UI 文案。
    for (const m of line.matchAll(new RegExp(`\\.(?:${ZOD_MESSAGE_METHODS.join("|")})\\([^\\n]*?(["'])((?:\\\\.|(?!\\1).)*[A-Za-z]{2}(?:\\\\.|(?!\\1).)*)\\1`, "g"))) {
      if (/\bMath\s*$/.test(line.slice(0, m.index))) continue;
      found.push({ text: decodeEnt(m[2]), loc, cat: 5 });
    }
    for (const m of line.matchAll(/\.refine\([^,]+,\s*(["'])((?:\\.|(?!\1).)*[A-Za-z]{2}(?:\\.|(?!\1).)*)\1/g)) {
      found.push({ text: decodeEnt(m[2]), loc, cat: 5 });
    }
    // ⑥ 路由映射表值(阶段C 盲区B):`"/workspace/x": "Title"`。键以 / 开头限定为路径映射,避免泛化误报。
    for (const m of line.matchAll(/"\/[^"]+"\s*:\s*"([^"]+)"/g)) {
      if (/[A-Za-z]{2}/.test(m[1])) found.push({ text: decodeEnt(m[1]), loc, cat: 6 });
    }
    // ⑦ 三元字符串对(阶段C 盲区:表达式位字面量):`cond ? "Saving..." : "Save Changes"`。
    // 两分支均须「大写开头且含小写」——排除 "POST"/"GET" 类全大写技术值;全仓 172 处肉眼抽样全真。
    for (const m of line.matchAll(/\?\s*(["'])([A-Z](?:\\.|(?!\1).)*)\1\s*:\s*(["'])([A-Z](?:\\.|(?!\3).)*)\3/g)) {
      for (const t of [m[2], m[4]]) {
        if (/[a-z]/.test(t) && /[A-Za-z]{2}/.test(t)) found.push({ text: decodeEnt(t), loc, cat: 7 });
      }
    }
    // ⑦b 跨行三元(复巡检发现的残余盲区):prettier 会把长三元折成
    // `cond\n\t? "Long branch A..."\n\t: "Long branch B..."`,⑦的同行正则永不触发。
    // 行尾 `? "…"` 与行首 `: "…"` 各自独立提取,守卫同⑦(大写开头+含小写)。
    {
      const tail = line.match(/\?\s*(["'])([A-Z](?:\\.|(?!\1).)*)\1\s*$/);
      const head = line.match(/^\s*:\s*(["'])([A-Z](?:\\.|(?!\1).)*)\1/);
      for (const m of [tail, head]) {
        if (m && /[a-z]/.test(m[2]) && /[A-Za-z]{2}/.test(m[2])) found.push({ text: decodeEnt(m[2]), loc, cat: 7 });
      }
    }
    // ④ 模板字面量(四通道位置围栏 + 语法位上下文围栏 + 形态围栏)
    for (const m of line.matchAll(/`([^`]*\$\{[^`]*)`/g)) {
      const skeleton = normalize(m[1]);
      const before = line.slice(0, m.index);
      const ctxExcluded = /(?:className\s*=\s*\{?|cn\(|clsx\(|key\s*=\s*\{?|id\s*=\s*\{?)\s*$/.test(before) || /https?:\/\//.test(m[1]);
      const positional =
        />\s*\{\s*$/.test(before) || />\s*\{\s*[^{}]*$/.test(before) === false && />\s*\{/.test(before) // children 表达式(保守)
        || new RegExp(`(?:${PROPS.join("|")})\\s*=\\s*\\{\\s*$`).test(before)
        || /(?:toast(?:\.\w+)?|announce)\(\s*$/.test(before)
        || /(?:=|return)\s*(?:\[?\s*)?$/.test(before) // 第四通道:赋值/return 位(评审3-F1)
        || /\w+\s*:\s*$/.test(before); // 第五通道(阶段C 盲区#5):对象属性位 `title: \`${x} ...\``
      if (!passesFormFence(skeleton)) { excluded.form++; continue; }
      if (ctxExcluded) { excluded.context++; bypass.push({ skeleton, loc, why: "上下文围栏" }); continue; }
      if (!positional) { excluded.position++; bypass.push({ skeleton, loc, why: "位置围栏" }); continue; }
      found.push({ text: skeleton, loc, cat: 4 });
    }
  });
  return { found, bypass, excluded };
}

// ───────────────────────── 扫描主流程 ─────────────────────────
function* walkFiles(dir) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    // 阶段C 盲区D:_fallbacks 不再排除——其组件在社区版真实渲染(集群配置页实证)。
    // .test./.spec. 排除:测试字符串永不面向用户。
    if (name === "node_modules") continue;
    const st = statSync(p);
    if (st.isDirectory()) yield* walkFiles(p);
    else if (/\.tsx?$/.test(name) && !/\.gen\.ts$/.test(name) && !/\.(test|spec)\.tsx?$/.test(name) && name !== "zhLocale.ts") yield p;
  }
}

// ───────────────────────── ⑧ slug 合成标题(阶段C 盲区E) ─────────────────────────
// topbar 的 deriveTitleFromPathname 会把无 override 的路由段在运行时合成英文标题
// ("skills-repo" → "Skills Repo"),源码中不存在该字符串,静态扫描原理性不可见。
// 对策:枚举 app/workspace 全部 page.tsx 路由,按同一算法在扫描期预合成,纳入候选。
// titleAcronyms/routeTitleOverrides 从 topbar.utils.ts 解析(非硬编码,防漂移),解析失败即抛错(响的失败)。
export function parseTopbarMaps(src) {
  const grab = (name) => {
    const m = src.match(new RegExp(`const ${name}[^=]*=\\s*\\{([\\s\\S]*?)\\n\\};`));
    if (!m) throw new Error(`对账失败: topbar.utils.ts 未找到 ${name} 块`);
    const map = new Map();
    for (const line of m[1].split("\n")) {
      const kv = line.match(/^\s*(?:"([^"]+)"|(\w+)):\s*"([^"]+)",?\s*$/);
      if (kv) map.set(kv[1] ?? kv[2], kv[3]);
    }
    return map;
  };
  return { acronyms: grab("titleAcronyms"), overrides: grab("routeTitleOverrides") };
}
export function deriveSlugTitle(route, acronyms, overrides) {
  if (overrides.has(route)) return null; // override 值由通道⑥负责
  const seg = route.split("/").filter(Boolean).at(-1) ?? "dashboard";
  return seg.split("-").map((p) => acronyms.get(p.toLowerCase()) ?? p.charAt(0).toUpperCase() + p.slice(1)).join(" ");
}
function* slugTitleCandidates() {
  const { acronyms, overrides } = parseTopbarMaps(readFileSync(TOPBAR_UTILS, "utf8"));
  const routes = new Set();
  (function walk(dir, route) {
    for (const name of readdirSync(dir)) {
      const p = join(dir, name);
      if (statSync(p).isDirectory()) walk(p, `${route}/${name}`);
      else if (name === "page.tsx" && !route.includes("[")) routes.add(route); // 动态段标题含参数值,不可静态预判
    }
  })(join(UI_ROOT, "app", "workspace"), "/workspace");
  for (const route of [...routes].sort()) {
    const title = deriveSlugTitle(route, acronyms, overrides);
    if (title && /[A-Za-z]{2}/.test(title)) yield { text: title, loc: `slug:${route}`, cat: 8 };
  }
}

function scan() {
  const { dict, rules } = parseZhLocale(readFileSync(ZH_LOCALE, "utf8"));
  const translate = makeTranslate(dict, rules);
  const wl = parseWhitelist(readFileSync(WHITELIST, "utf8"));
  const skeletons = new Set(rules.map((r) => r.skeleton));
  const miss = new Map(); const allBypass = []; const totals = { position: 0, form: 0, context: 0 };
  for (const d of SCAN_DIRS) {
    for (const f of walkFiles(join(UI_ROOT, d))) {
      const rel = f.slice(UI_ROOT.length + 1);
      const { found, bypass, excluded } = extractFromSource(readFileSync(f, "utf8"), rel);
      for (const k of Object.keys(totals)) totals[k] += excluded[k];
      allBypass.push(...bypass);
      for (const it of found) {
        const covered = it.cat === 4 ? skeletons.has(it.text) : translate(it.text) !== null;
        if (covered || inWhitelist(wl, it.text)) continue;
        const e = miss.get(it.text) ?? { n: 0, first: it.loc, cat: it.cat };
        e.n++; miss.set(it.text, e);
      }
    }
  }
  // ⑧ slug 合成标题:不来自任何源文件行,单独并入
  for (const it of slugTitleCandidates()) {
    if (translate(it.text) !== null || inWhitelist(wl, it.text)) continue;
    const e = miss.get(it.text) ?? { n: 0, first: it.loc, cat: it.cat };
    e.n++; miss.set(it.text, e);
  }
  return { miss, allBypass, totals };
}

// ───────────────────────── self-test(fixture 驱动, ≥14) ─────────────────────────
function selfTest() {
  let n = 0; const ok = (cond, name) => { n++; if (!cond) { console.error(`✗ [${n}] ${name}`); process.exit(1); } console.log(`✓ [${n}] ${name}`); };
  const fx = (src) => extractFromSource(src, "fx.tsx");

  ok(fx(`<b>Save</b>`).found.some((f) => f.text === "Save" && f.cat === 1), "①同行文本节点");
  ok(fx(`<p>\n  No results found here\n</p>`).found.some((f) => f.text === "No results found here"), "①c 单行独立文本");
  { // ①c 多行聚合(修复代码复核发现的截断缺陷:纯文本 JSX children 跨行书写时,
    // 真实 DOM 折叠为单个文本节点,scanner 必须按同一规则聚合,否则字典 key 系统性截断)
    const src = `<AlertDescription>\n\tThese settings require a restart to take effect. Current connections continue until\n\trestart.\n</AlertDescription>`;
    const r = fx(src);
    ok(r.found.some((f) => f.text === "These settings require a restart to take effect. Current connections continue until restart."),
      "①c 多行聚合(跨行纯文本折叠为单节点)");
  }
  { // 多行聚合遇到同行文本+闭合标签(无换行结束)也要正确截断
    const src = `<span>\n\tPartial text ends here.</span>`;
    const r = fx(src);
    ok(r.found.some((f) => f.text === "Partial text ends here."), "①c 多行聚合在同行闭合标签处正确截断");
  }
  { // 回归防护:箭头函数 `=>` 末尾的 '>' 不是 JSX 标签闭合符,不得触发跨行聚合
    // (曾误吞多行 JS 表达式,见代码复核发现)
    const src = `<CollapsibleBox\n\tonCopy={() =>\n\t\tJSON.stringify({ a: 1 })\n\t}\n/>`;
    const r = fx(src);
    ok(!r.found.some((f) => f.cat === 1 && f.text.includes("JSON.stringify")),
      "①c 箭头函数 => 不误判为 JSX 闭合(回归防护)");
  }
  ok(fx(`<input placeholder="Search models" />`).found.some((f) => f.text === "Search models" && f.cat === 2), "②prop 字面量");
  ok(fx(`toast.error("Failed to save")`).found.some((f) => f.text === "Failed to save" && f.cat === 3), "③toast");
  ok(fx("x = `${n} keys found`").found.some((f) => f.text === "⟨x⟩ keys found" && f.cat === 4), "④赋值位模板(F1 第四通道)");
  ok(fx("announce(`${a} of ${b} options selected.`)").found.some((f) => f.text === "⟨x⟩ of ⟨x⟩ options selected."), "④announce 实参");
  ok(fx("title={`${n} items pending`}").found.some((f) => f.text === "⟨x⟩ items pending"), "④prop 位模板");
  { const r = fx("cn(`${x} col-span-2 grid`)"); ok(!r.found.some((f) => f.cat === 4) && r.excluded.context === 1, "④cn( 上下文围栏排除+计数"); }
  { const r = fx("x = `${id}_suffix`"); ok(r.excluded.form === 1, "④单词数形态围栏排除"); }
  { const r = fx('<div className={cn(`${w} p-2`)}>{`${n} rows loaded`}</div>');
    ok(r.found.some((f) => f.text === "⟨x⟩ rows loaded"), "④同行 className 不连坐 children(F3)"); }
  ok(normalize("${fn({a:1})} x") !== null, "④嵌套花括号不崩溃(F4 响失败进清单)");

  // 代码复核发现:以下三项此前只测孤立的硬编码样板规则,从未触达 zhLocale.ts 里的真实
  // RULES——改规则忘改 skeleton/sample、或新增规则违反契约,self-test 之前不会报错。
  // 现在对解析出的全部生产 RULES 做 .every() 遍历,才是真正的机械互证防线。
  {
    const { dict, rules } = parseZhLocale(readFileSync(ZH_LOCALE, "utf8"));
    ok(dict.size > 200, "线上 DICT 解析+对账通过");
    ok(rules.length > 0 && rules.length <= 100, `线上 RULES 解析+对账通过(${rules.length} 条,上限100)`);
    const translate = makeTranslate(dict, rules);
    ok(rules.every((r) => rules.filter((r2) => r2.re.test(r.sample)).length === 1),
      `全部 ${rules.length} 条生产规则:sample 恰中一条(无重叠捕获)`);
    ok(rules.every((r) => translate(translate(r.sample)) === null),
      `全部 ${rules.length} 条生产规则:固定点(输出再入必 null)`);
    ok(rules.every((r) => sampleInstantiatesSkeleton(r.sample, r.skeleton) && r.re.test(r.sample)),
      `全部 ${rules.length} 条生产规则:互证(sample 是 skeleton 代入实例,F2)`);
    const skeletons = new Set(rules.map((r) => r.skeleton));
    ok(skeletons.size === rules.length, "生产规则 skeleton 无重复");
  }
  let threw = false; try { parseWhitelist("/.*/"); } catch { threw = true; } ok(threw, "白名单金丝雀拒绝 /.*/");
  ok(parseWhitelist("Issue \\#1 tracker").literals.has("Issue #1 tracker"), "白名单 \\# 转义");

  // ── 阶段C 新通道(盲区修复,每通道正例+守卫负例) ──
  ok(fx(`<Plus className="size-4" /> Add Profile`).found.some((f) => f.text === "Add Profile" && f.cat === 1), "①d 自闭合标签后行尾文本");
  ok(fx(`<PencilIcon className="h-4 w-4" /> Edit`).found.some((f) => f.text === "Edit"), "①d 单词也提取(Edit/Delete 实例)");
  ok(!fx("const f = (x) => x").found.length, "①d 箭头函数不误触(锚定 /> 而非裸 >)");
  ok(fx(`\t\t\t\ttitle: "User Success Rate",`).found.some((f) => f.text === "User Success Rate" && f.cat === 5), "⑤对象属性位字符串(盲区A)");
  ok(fx(`{ label: "Beta Headers", value: 1 }`).found.some((f) => f.text === "Beta Headers" && f.cat === 5), "⑤行中对象键({ 前缀)");
  ok(fx(`{ message: "Name is required" }`).found.some((f) => f.text === "Name is required" && f.cat === 5), "⑤校验 message 对象键");
  ok(fx(`rules={{ required: "Folder name is required" }}`).found.some((f) => f.text === "Folder name is required" && f.cat === 5), "⑤表单 required 对象键");
  ok(!fx(`titleX: "Not a prop"`).found.some((f) => f.cat === 5), "⑤键名全词匹配(titleX 不触发)");
  ok(fx(`name: z.string().min(1, "Name is required")`).found.some((f) => f.text === "Name is required" && f.cat === 5), "⑤b Zod 方法消息参数");
  ok(fx(`\t.min(1, "Name is required")`).found.some((f) => f.text === "Name is required" && f.cat === 5), "⑤b 多行链式 Zod 方法消息参数");
  ok(fx(`endpoint: z.string().refine((v) => v.trim() === "" || v.includes("."), "Enter the endpoint DNS name")`).found.some((f) => f.text === "Enter the endpoint DNS name" && f.cat === 5), "⑤b Zod refine 第二参数消息");
  ok(fx(`\t.refine((hours) => hours === 0 || hours >= 1, "Sync interval must be 0 (disabled) or at least 1 hour")`).found.some((f) => f.text === "Sync interval must be 0 (disabled) or at least 1 hour" && f.cat === 5), "⑤b 多行链式 Zod refine 第二参数消息");
  ok(!fx(`z.string().refine((v) => v.startsWith("http://"), { message: "URL is required" })`).found.some((f) => f.text === "http://"), "⑤b Zod refine predicate 字符串不误触");
  ok(!fx(`Math.max(0, offset - PAGE_SIZE, "push")`).found.some((f) => f.cat === 5), "⑤b Math.max 不误触");
  ok(!fx(`value.startsWith("http://")`).found.some((f) => f.cat === 5), "⑤b 普通字符串方法不误触");
  ok(fx(`"/workspace/observability": "Observability Connectors",`).found.some((f) => f.text === "Observability Connectors" && f.cat === 6), "⑥路由映射表值(盲区B)");
  ok(!fx(`"active": "bg-green-500"`).found.some((f) => f.cat === 6), "⑥非路径键不触发");
  { const r = fx(`{isLoading ? "Saving..." : "Save Changes"}`);
    ok(r.found.some((f) => f.text === "Saving..." && f.cat === 7) && r.found.some((f) => f.text === "Save Changes"), "⑦三元字符串对两分支"); }
  { const r = fx(`hidden ? 'All goroutines are hidden. Click "Clear hidden" to show them.' : "No goroutine data available"`);
    ok(r.found.some((f) => f.text === `All goroutines are hidden. Click "Clear hidden" to show them.` && f.cat === 7), "⑦单引号三元分支"); }
  ok(!fx(`method === "POST" ? "POST" : "GET"`).found.some((f) => f.cat === 7), "⑦全大写技术值不触发");
  { const r = fx(`{canCreate\n\t? "Create prompts and version them."\n\t: "View prompts in the playground."}`);
    ok(r.found.some((f) => f.text === "Create prompts and version them." && f.cat === 7)
      && r.found.some((f) => f.text === "View prompts in the playground."), "⑦b 跨行三元两分支(复巡检盲区)"); }
  ok(!fx(`x\n\t? "POST"\n\t: "GET"`).found.some((f) => f.cat === 7), "⑦b 跨行三元全大写技术值不触发");
  ok(fx("title: `${v} is now available.`").found.some((f) => f.text === "⟨x⟩ is now available." && f.cat === 4), "④对象属性位模板(盲区#5,sidebar 横幅实例)");
  { const { acronyms, overrides } = parseTopbarMaps(`const titleAcronyms: Record<string, string> = {\n\tmcp: "MCP",\n};\nconst routeTitleOverrides: Record<string, string> = {\n\t"/workspace/logs": "LLM Logs",\n};`);
    ok(deriveSlugTitle("/workspace/skills-repo", acronyms, overrides) === "Skills Repo", "⑧slug 合成(skills-repo → Skills Repo)");
    ok(deriveSlugTitle("/workspace/mcp-registry", acronyms, overrides) === "MCP Registry", "⑧slug acronym 表生效");
    ok(deriveSlugTitle("/workspace/logs", acronyms, overrides) === null, "⑧override 路由让位于通道⑥"); }
  console.log(`\nself-test 全绿(${n} 项)`);
}

// ───────────────────────── CLI ─────────────────────────
// import 守卫:仅当此文件作为脚本直接执行时才跑 CLI 逻辑,避免其他脚本
// import 本文件的导出函数(如复核/验证用途)时意外触发一次完整扫描。
const isMain = process.argv[1] && import.meta.url === `file://${process.argv[1]}`;
const mode = process.argv[2] ?? "";
if (!isMain) { /* 被 import,不执行 CLI */ }
else if (mode === "--self-test") selfTest();
else {
  const { miss, allBypass, totals } = scan();
  const sorted = [...miss.entries()].sort((a, b) => b[1].n - a[1].n);
  if (mode === "--check") {
    if (sorted.length) { console.error(`未翻译 ${sorted.length} 条(--check 失败),运行无参模式查看清单`); process.exit(1); }
    console.log("覆盖率检查通过:清单为空"); process.exit(0);
  }
  console.log(`# 未翻译清单(${sorted.length} 条)\n`);
  for (const [t, e] of sorted) console.log(`${e.n}\t[${e.cat}]\t${t}\t${e.first}`);
  console.log(`\n# 围栏排除统计: 位置=${totals.position} 形态=${totals.form} 上下文=${totals.context}`);
  console.log(`# 行级旁路报告(形态围栏内、位置/上下文围栏外, ${allBypass.length} 行):`);
  for (const b of allBypass) console.log(`  ${b.why}\t${b.skeleton}\t${b.loc}`);
}
