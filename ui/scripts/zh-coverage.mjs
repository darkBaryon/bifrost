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
const CANARY = "XQ7#鑫W9-zk";
const PROPS = ["placeholder", "title", "label", "description", "aria-label", "tooltip"];

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
  lines.forEach((line, i) => {
    const loc = `${file}:${i + 1}`;
    // ① JSX 文本节点(同行 >text<)
    for (const m of line.matchAll(/>([^<>{}`]+)</g)) {
      const t = m[1].trim();
      if (t && /[A-Za-z]{2}/.test(t)) found.push({ text: t, loc, cat: 1 });
    }
    // ①b 独立文本行(上下皆标签的多行 JSX children,保守:≥2 英文词)
    if (/^\s+[A-Z][A-Za-z0-9 ,.'’&/():%+-]*[.?!:]?\s*$/.test(line) && passesFormFence(line.trim())
        && i > 0 && />$/.test(lines[i - 1].trim())) {
      found.push({ text: line.trim(), loc, cat: 1 });
    }
    // ② 六 prop 字面量
    for (const p of PROPS) {
      for (const m of line.matchAll(new RegExp(`${p}\\s*=\\s*(?:\\{\\s*)?"([^"]+)"`, "g"))) {
        if (/[A-Za-z]{2}/.test(m[1])) found.push({ text: m[1], loc, cat: 2 });
      }
    }
    // ③ toast 字符串字面量
    for (const m of line.matchAll(/toast(?:\.\w+)?\(\s*"([^"]+)"/g)) {
      found.push({ text: m[1], loc, cat: 3 });
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
        || /(?:=|return)\s*(?:\[?\s*)?$/.test(before); // 第四通道:赋值/return 位(评审3-F1)
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
    if (name === "_fallbacks" || name === "node_modules") continue;
    const st = statSync(p);
    if (st.isDirectory()) yield* walkFiles(p);
    else if (/\.tsx?$/.test(name) && !/\.gen\.ts$/.test(name) && name !== "zhLocale.ts") yield p;
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
  return { miss, allBypass, totals };
}

// ───────────────────────── self-test(fixture 驱动, ≥14) ─────────────────────────
function selfTest() {
  let n = 0; const ok = (cond, name) => { n++; if (!cond) { console.error(`✗ [${n}] ${name}`); process.exit(1); } console.log(`✓ [${n}] ${name}`); };
  const fx = (src) => extractFromSource(src, "fx.tsx");

  ok(fx(`<b>Save</b>`).found.some((f) => f.text === "Save" && f.cat === 1), "①同行文本节点");
  ok(fx(`<p>\n  No results found here\n</p>`).found.some((f) => f.text === "No results found here"), "①b 独立文本行");
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

  const rules = [{ re: /^(\d+) model budgets?$/, out: "$1 个模型预算", skeleton: "⟨x⟩ model budget⟨x⟩", sample: "3 model budgets" }];
  const translate = makeTranslate(new Map([["Save", "保存"]]), rules);
  ok(rules.filter((r) => r.re.test(rules[0].sample)).length === 1, "规则 sample 恰中一条");
  ok(translate(translate(rules[0].sample)) === null, "固定点:输出再入必 null");
  ok(sampleInstantiatesSkeleton(rules[0].sample, rules[0].skeleton) && rules[0].re.test(rules[0].sample), "互证:sample 是 skeleton 代入实例(F2)");
  let threw = false; try { parseWhitelist("/.*/"); } catch { threw = true; } ok(threw, "白名单金丝雀拒绝 /.*/");
  ok(parseWhitelist("Issue \\#1 tracker").literals.has("Issue #1 tracker"), "白名单 \\# 转义");
  { const { dict } = parseZhLocale(readFileSync(ZH_LOCALE, "utf8")); ok(dict.size > 200, "线上 DICT 解析+对账通过"); }
  console.log(`\nself-test 全绿(${n} 项)`);
}

// ───────────────────────── CLI ─────────────────────────
const mode = process.argv[2] ?? "";
if (mode === "--self-test") selfTest();
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
