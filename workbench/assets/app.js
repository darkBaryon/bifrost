// 工作台站点交互：主题切换 / 顶部 tab 钉住 / 代码复制 / 移动端抽屉 / TOC 滚动高亮。
(function () {
  "use strict";

  // ── 主题（localStorage 记忆，跟随系统初值）──
  var root = document.documentElement;
  var saved = localStorage.getItem("wb-theme");
  if (saved) root.setAttribute("data-theme", saved);
  else if (window.matchMedia && matchMedia("(prefers-color-scheme: dark)").matches)
    root.setAttribute("data-theme", "dark");

  function onClick(sel, fn) {
    document.querySelectorAll(sel).forEach(function (el) { el.addEventListener("click", fn); });
  }

  onClick(".theme-btn", function () {
    var next = root.getAttribute("data-theme") === "dark" ? "light" : "dark";
    root.setAttribute("data-theme", next);
    localStorage.setItem("wb-theme", next);
  });

  // ── 顶部 tab：点击钉住下拉（hover 仍可展开）──
  document.querySelectorAll(".tab > .tab-link").forEach(function (link) {
    link.addEventListener("click", function (e) {
      var tab = link.parentElement;
      // 有下拉内容才拦截跳转，钉住菜单
      if (tab.querySelector(".dropdown a")) {
        e.preventDefault();
        var open = tab.classList.contains("open");
        document.querySelectorAll(".tab.open").forEach(function (t) { t.classList.remove("open"); });
        if (!open) tab.classList.add("open");
      }
    });
  });
  document.addEventListener("click", function (e) {
    if (!e.target.closest(".tab"))
      document.querySelectorAll(".tab.open").forEach(function (t) { t.classList.remove("open"); });
  });

  // ── 代码复制 ──
  onClick(".copy", function (e) {
    var btn = e.currentTarget;
    var code = btn.parentElement.querySelector("code");
    if (!code) return;
    navigator.clipboard.writeText(code.innerText).then(function () {
      btn.textContent = "已复制"; btn.classList.add("done");
      setTimeout(function () { btn.textContent = "复制"; btn.classList.remove("done"); }, 1400);
    });
  });

  // ── 移动端抽屉 ──
  onClick(".menu-btn", function () { document.body.classList.toggle("nav-open"); });
  onClick(".scrim", function () { document.body.classList.remove("nav-open"); });

  // ── TOC 滚动高亮 ──
  var tocLinks = [].slice.call(document.querySelectorAll(".toc-link"));
  if (tocLinks.length) {
    var targets = tocLinks
      .map(function (a) { return document.getElementById(a.getAttribute("href").slice(1)); })
      .filter(Boolean);
    var spy = function () {
      var pos = window.scrollY + 90, cur = targets[0];
      targets.forEach(function (t) { if (t.offsetTop <= pos) cur = t; });
      tocLinks.forEach(function (a) {
        a.classList.toggle("active", cur && a.getAttribute("href") === "#" + cur.id);
      });
    };
    window.addEventListener("scroll", spy, { passive: true });
    spy();
  }

  // ── 侧栏：定位到当前激活项 ──
  var active = document.querySelector(".side-link.active");
  if (active) {
    var sb = document.querySelector(".sidebar");
    if (sb && active.offsetTop > sb.clientHeight - 120)
      sb.scrollTop = active.offsetTop - sb.clientHeight / 2;
  }
})();
