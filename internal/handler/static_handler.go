// Package handler 单页前端静态资源处理器：返回 index.html / 内置资源。
// 为避免依赖外部静态文件目录，所有前端资源（HTML/CSS/JS）均以内嵌字符串形式返回。
package handler

import (
	"net/http"
	"strings"
)

// StaticHandler 前端静态资源处理器。
type StaticHandler struct {
	indexHTML []byte
}

// NewStaticHandler 创建并初始化前端页面。
func NewStaticHandler() *StaticHandler {
	html := replacePlaceholders(indexHTMLContent)
	return &StaticHandler{indexHTML: []byte(html)}
}

// Serve 根据路径分发静态资源。
func (s *StaticHandler) Serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/" || path == "/index.html" || path == "" || strings.HasPrefix(path, "/?"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(s.indexHTML)
		return
	case strings.HasPrefix(path, "/static/app.js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write([]byte(replacePlaceholders(appJSContent)))
		return
	case strings.HasPrefix(path, "/static/app.css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write([]byte(appCSSContent))
		return
	default:
		// SPA 兜底：返回 index。
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(s.indexHTML)
	}
}

// replacePlaceholders 把 JS 里用的占位符替换为实际的模板字面量语法：
//   `BTICK` -> 反引号 `
//   `$(`   -> 模板插值开头 ${
func replacePlaceholders(s string) string {
	s = strings.ReplaceAll(s, "BTICK", "`")
	s = strings.ReplaceAll(s, "$(", "${")
	return s
}

// indexHTMLContent 内嵌前端页面（管理控制台）。
const indexHTMLContent = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width,initial-scale=1"/>
<title>设备固件升级管理服务</title>
<link rel="stylesheet" href="/static/app.css"/>
</head>
<body>
<div id="app">
  <header class="top">
    <h1>设备固件升级管理服务</h1>
    <nav class="tabs">
      <button data-tab="models" class="tab active">设备型号</button>
      <button data-tab="firmwares" class="tab">固件版本</button>
      <button data-tab="devices" class="tab">设备管理</button>
      <button data-tab="tasks" class="tab">升级任务</button>
      <button data-tab="histories" class="tab">升级历史</button>
      <button data-tab="stats" class="tab">统计总览</button>
    </nav>
  </header>
  <main class="container">
    <section id="models" class="panel active"></section>
    <section id="firmwares" class="panel"></section>
    <section id="devices" class="panel"></section>
    <section id="tasks" class="panel"></section>
    <section id="histories" class="panel"></section>
    <section id="stats" class="panel"></section>
  </main>
  <footer class="foot">
    <button id="healthBtn">检查健康</button>
    <span id="healthOut"></span>
  </footer>
</div>
<script src="/static/app.js"></script>
</body>
</html>`

// appCSSContent 内嵌样式。
const appCSSContent = `
*{box-sizing:border-box}
body{margin:0;font-family:-apple-system,BlinkMacSystemFont,"Helvetica Neue",Arial,"PingFang SC","Microsoft YaHei",sans-serif;color:#222;background:#f5f7fa}
.top{background:#1f6feb;color:#fff;padding:14px 20px}
.top h1{margin:0 0 10px;font-size:20px}
.tabs{display:flex;flex-wrap:wrap;gap:8px}
.tab{background:rgba(255,255,255,.12);border:none;color:#fff;padding:8px 14px;border-radius:6px;cursor:pointer}
.tab.active{background:#fff;color:#1f6feb;font-weight:600}
.container{padding:16px}
.panel{display:none;background:#fff;padding:16px;border-radius:8px;box-shadow:0 1px 4px rgba(0,0,0,.06);margin-bottom:12px}
.panel.active{display:block}
.row{display:flex;gap:10px;flex-wrap:wrap;margin-bottom:12px;align-items:center}
input,select,button,textarea{font-family:inherit;font-size:14px;padding:6px 10px;border-radius:6px;border:1px solid #d0d7de;outline:none}
input:focus,select:focus,textarea:focus{border-color:#1f6feb;box-shadow:0 0 0 2px rgba(31,111,235,.15)}
button.primary{background:#1f6feb;color:#fff;border-color:#1f6feb;cursor:pointer}
button.danger{background:#cf222e;color:#fff;border-color:#cf222e;cursor:pointer}
table{width:100%;border-collapse:collapse;font-size:14px}
th,td{padding:8px 10px;border-bottom:1px solid #eee;text-align:left;vertical-align:top}
th{background:#f6f8fa;color:#57606a;font-weight:600}
tr:hover td{background:#f6fafd}
.badge{display:inline-block;padding:2px 8px;border-radius:12px;font-size:12px}
.badge.online{background:#dafbe1;color:#116329}
.badge.offline{background:#ffebe9;color:#82071e}
.badge.unknown{background:#ffebc2;color:#8a5300}
.badge.pending{background:#ddf4ff;color:#0550ae}
.badge.running{background:#f5efff;color:#6639ba}
.badge.success{background:#dafbe1;color:#116329}
.badge.failed{background:#ffebe9;color:#82071e}
.badge.canceled{background:#eaeef2;color:#424a53}
.pager{margin-top:12px;display:flex;gap:8px;align-items:center}
.card{border:1px solid #eee;border-radius:8px;padding:14px;min-width:150px}
.k{color:#57606a;font-size:12px}
.v{font-size:20px;font-weight:600;margin-top:4px}
.stat-row{display:flex;gap:12px;flex-wrap:wrap}
.foot{padding:10px 20px;background:#fff;border-top:1px solid #eee;color:#57606a;display:flex;gap:12px;align-items:center}
.message{padding:10px 12px;border-radius:6px;margin:8px 0;font-size:14px}
.message.error{background:#ffebe9;color:#82071e}
.message.ok{background:#dafbe1;color:#116329}
`

// appJSContent 内嵌控制台前端逻辑。
const appJSContent = `
(() => {
  const api = "/api/v1";
  const fmt = (d) => (d ? new Date(d).toLocaleString() : "-");
  const $ = (s, r = document) => r.querySelector(s);
  const $$ = (s, r = document) => [...r.querySelectorAll(s)];

  async function http(url, method = "GET", body = null, extra = {}) {
    const opt = { method, headers: { "Content-Type": "application/json" }, ...extra };
    if (body) opt.body = JSON.stringify(body);
    const r = await fetch(url, opt);
    const ct = r.headers.get("Content-Type") || "";
    let data;
    if (ct.includes("application/json")) data = await r.json();
    else data = await r.text();
    if (!r.ok) {
      const msg = data && data.message ? data.message : (typeof data === "string" ? data : ("HTTP " + r.status));
      throw new Error(msg);
    }
    return data;
  }

  function msgBox(panel, text, isError = false) {
    const div = document.createElement("div");
    div.className = "message " + (isError ? "error" : "ok");
    div.textContent = text;
    panel.prepend(div);
    setTimeout(() => div.remove(), 3000);
  }

  function pager(panel, onLoad, page = 1, size = 10, total = 0) {
    const bar = document.createElement("div");
    bar.className = "pager";
    const maxPage = Math.max(1, Math.ceil(total / size));
    const prev = document.createElement("button");
    prev.textContent = "上一页";
    prev.disabled = page <= 1;
    prev.onclick = () => onLoad(page - 1, size);
    const next = document.createElement("button");
    next.textContent = "下一页";
    next.disabled = page >= maxPage;
    next.onclick = () => onLoad(page + 1, size);
    const info = document.createElement("span");
    info.textContent = \BTICK第 \$(page}/\$(maxPage} 页，共 \$(total} 条，每页 \$(size} 条\BTICK;
    bar.append(prev, info, next);
    panel.appendChild(bar);
  }

  function tab(name) {
    $$(".tab").forEach(b => b.classList.toggle("active", b.dataset.tab === name));
    $$(".panel").forEach(p => p.classList.toggle("active", p.id === name));
    if (name === "models") loadModels();
    if (name === "firmwares") loadFirmwares();
    if (name === "devices") loadDevices();
    if (name === "tasks") loadTasks();
    if (name === "histories") loadHistories();
    if (name === "stats") loadStats();
  }

  $$(".tab").forEach(b => b.onclick = () => tab(b.dataset.tab));

  // ============ Models ============
  const mState = { page: 1, size: 10 };
  function loadModels() {
    const p = $("#models");
    p.innerHTML = \BTICK<div class="row">
      <input id="mKeyword" placeholder="关键字"/>
      <input id="mVendor" placeholder="厂商"/>
      <button class="primary" id="mSearch">查询</button>
      <button class="primary" id="mNew">新建型号</button>
    </div><div id="mResult"></div><div id="mPager"></div>\BTICK;
    $("#mSearch").onclick = () => { mState.page = 1; doLoadModels(); };
    $("#mNew").onclick = () => createModel();
    doLoadModels();
  }
  async function doLoadModels() {
    const kw = $("#mKeyword").value || "";
    const vd = $("#mVendor").value || "";
    try {
      const r = await http(\BTICK\$(api}/models?keyword=\$(encodeURIComponent(kw)}&vendor=\$(encodeURIComponent(vd)}&page_num=\$(mState.page}&page_size=\$(mState.size}\BTICK);
      const res = r.data || r;
      const rows = res.list || [];
      const tb = document.createElement("table");
      tb.innerHTML = \BTICK<tr><th>ID</th><th>名称</th><th>厂商</th><th>架构</th><th>内存/Flash</th><th>启用</th><th>创建时间</th><th>操作</th></tr>\BTICK +
        rows.map(m => \BTICK<tr><td>\$(m.id}</td><td>\$(m.name}</td><td>\$(m.vendor||""}</td><td>\$(m.arch}</td><td>\$(m.memory_mb}MB / \$(m.flash_mb}MB</td>
        <td>\$(m.enabled ? "是" : "否"}</td><td>\$(fmt(m.created_at)}</td>
        <td><button data-del="\$(m.id}">删除</button></td></tr>\BTICK).join("");
      $("#mResult").innerHTML = "";
      $("#mResult").appendChild(tb);
      $$("[data-del]", tb).forEach(b => b.onclick = () => deleteModel(b.dataset.del));
      $("#mPager").innerHTML = "";
      pager($("#mPager"), (p, s) => { mState.page = p; mState.size = s; doLoadModels(); }, mState.page, mState.size, res.total || 0);
    } catch (e) { msgBox($("#models"), "加载失败: " + e.message, true); }
  }
  async function createModel() {
    const id = prompt("型号 ID（如 GW-100）：", "");
    if (!id) return;
    const name = prompt("型号名称：", "");
    if (!name) return;
    const arch = prompt("CPU 架构（如 arm64/x86_64）：", "arm64") || "arm64";
    try {
      await http(\BTICK\$(api}/models\BTICK, "POST", { id, name, arch, vendor: prompt("厂商：") || "", enabled: true });
      msgBox($("#models"), "创建成功");
      doLoadModels();
    } catch (e) { msgBox($("#models"), "创建失败: " + e.message, true); }
  }
  async function deleteModel(id) {
    if (!confirm("确认删除型号 " + id + " ?")) return;
    try { await http(\BTICK\$(api}/models/\$(id}\BTICK, "DELETE"); msgBox($("#models"), "已删除"); doLoadModels(); }
    catch (e) { msgBox($("#models"), "删除失败: " + e.message, true); }
  }

  // ============ Firmwares ============
  const fState = { page: 1, size: 10 };
  function loadFirmwares() {
    const p = $("#firmwares");
    p.innerHTML = \BTICK<div class="row">
      <input id="fModel" placeholder="型号ID"/>
      <input id="fKeyword" placeholder="关键字"/>
      <select id="fStatus"><option value="">全部</option><option value="draft">草稿</option><option value="published">已发布</option><option value="deprecated">废弃</option></select>
      <button class="primary" id="fSearch">查询</button>
      <button class="primary" id="fUpload">上传固件</button>
    </div>
    <div id="fUploadBox" style="display:none;border:1px dashed #ccc;padding:12px;border-radius:8px">
      <div class="row">
        <input type="file" id="fFile"/>
        <input id="fModelId" placeholder="型号ID"/>
        <input id="fVersion" placeholder="版本号 v1.0.0"/>
        <input id="fName" placeholder="固件名称"/>
        <button class="primary" id="fSubmit">提交上传</button>
      </div>
    </div>
    <div id="fResult"></div><div id="fPager"></div>\BTICK;
    $("#fSearch").onclick = () => { fState.page = 1; doLoadFw(); };
    $("#fUpload").onclick = () => { $("#fUploadBox").style.display = $("#fUploadBox").style.display === "none" ? "block" : "none"; };
    $("#fSubmit").onclick = uploadFirmware;
    doLoadFw();
  }
  async function uploadFirmware() {
    const file = $("#fFile").files[0];
    if (!file) { msgBox($("#firmwares"), "请选择固件文件", true); return; }
    const fd = new FormData();
    fd.append("file", file);
    fd.append("model_id", $("#fModelId").value || "");
    fd.append("version", $("#fVersion").value || "");
    fd.append("name", $("#fName").value || file.name);
    try {
      const res = await fetch(\BTICK\$(api}/firmwares/upload\BTICK, { method: "POST", body: fd }).then(r => r.json());
      if (!res.success) throw new Error(res.message || "上传失败");
      msgBox($("#firmwares"), "上传成功: id=" + res.data.id);
      doLoadFw();
    } catch (e) { msgBox($("#firmwares"), "上传失败: " + e.message, true); }
  }
  async function doLoadFw() {
    try {
      const mid = $("#fModel").value || "";
      const kw = $("#fKeyword").value || "";
      const st = $("#fStatus").value || "";
      const r = await http(\BTICK\$(api}/firmwares?model_id=\$(mid}&keyword=\$(encodeURIComponent(kw)}&status=\$(st}&page_num=\$(fState.page}&page_size=\$(fState.size}\BTICK);
      const data = r.data || r;
      const rows = data.list || [];
      const tb = document.createElement("table");
      tb.innerHTML = \BTICK<tr><th>ID</th><th>型号</th><th>版本</th><th>名称</th><th>大小</th><th>MD5</th><th>状态</th><th>创建时间</th><th>操作</th></tr>\BTICK +
        rows.map(f => \BTICK<tr><td>\$(f.id}</td><td>\$(f.model_id}</td><td>\$(f.version}</td><td>\$(f.name}</td>
        <td>\$(human(f.size)}</td><td>\$(f.md5.substring(0,12)}...</td>
        <td>\$(badge("firm", f.status)}</td><td>\$(fmt(f.created_at)}</td>
        <td>
          <button data-act="publish" data-id="\$(f.id}">发布</button>
          <button data-act="deprecate" data-id="\$(f.id}">废弃</button>
          <a href="\$(api}/firmwares/\$(f.id}/download" target="_blank">下载</a>
          <button class="danger" data-del="\$(f.id}">删除</button>
        </td></tr>\BTICK).join("");
      $("#fResult").innerHTML = "";
      $("#fResult").appendChild(tb);
      $$("button[data-act]", tb).forEach(b => b.onclick = () => fwAction(b.dataset.id, b.dataset.act));
      $$("button[data-del]", tb).forEach(b => b.onclick = () => fwDel(b.dataset.del));
      $("#fPager").innerHTML = "";
      pager($("#fPager"), (p, s) => { fState.page = p; fState.size = s; doLoadFw(); }, fState.page, fState.size, data.total || 0);
    } catch (e) { msgBox($("#firmwares"), "加载失败: " + e.message, true); }
  }
  const human = (n) => {
    if (!n) return "0 B";
    const u = ["B","KB","MB","GB","TB"]; let i=0;
    while (n>=1024 && i<u.length-1) { n/=1024; i++; }
    return n.toFixed(2) + " " + u[i];
  };
  const badge = (t, v) => {
    const map = { online:"online", offline:"offline", unknown:"unknown",
      pending:"pending", running:"running", success:"success", failed:"failed", canceled:"canceled",
      published:"success", draft:"pending", deprecated:"canceled" };
    const cls = map[v] || "pending";
    return \BTICK<span class="badge \$(cls}">\$(v || "-"}</span>\BTICK;
  };
  async function fwAction(id, action) {
    try { await http(\BTICK\$(api}/firmwares/\$(id}/status\BTICK, "POST", { action }); doLoadFw(); }
    catch (e) { msgBox($("#firmwares"), "操作失败: " + e.message, true); }
  }
  async function fwDel(id) {
    if (!confirm("删除该固件？")) return;
    try { await http(\BTICK\$(api}/firmwares/\$(id}\BTICK, "DELETE"); msgBox($("#firmwares"), "已删除"); doLoadFw(); }
    catch (e) { msgBox($("#firmwares"), "失败: " + e.message, true); }
  }

  // ============ Devices ============
  const dState = { page: 1, size: 10 };
  function loadDevices() {
    const p = $("#devices");
    p.innerHTML = \BTICK<div class="row">
      <input id="dKw" placeholder="关键字"/>
      <input id="dModel" placeholder="型号ID"/>
      <input id="dGroup" placeholder="分组"/>
      <select id="dStatus"><option value="">全部</option><option value="online">在线</option><option value="offline">离线</option><option value="unknown">未知</option></select>
      <button class="primary" id="dSearch">查询</button>
      <button class="primary" id="dRegister">模拟注册</button>
    </div>
    <div id="dOps" style="display:none;border:1px dashed #ccc;padding:12px;border-radius:8px">
      <div class="row">
        <input id="drId" placeholder="设备ID/SN"/>
        <input id="drModel" placeholder="型号ID"/>
        <input id="drVersion" placeholder="当前版本 v1.0.0"/>
        <button class="primary" id="drSubmit">提交</button>
      </div>
    </div>
    <div id="dResult"></div><div id="dPager"></div>\BTICK;
    $("#dSearch").onclick = () => { dState.page = 1; doLoadD(); };
    $("#dRegister").onclick = () => { $("#dOps").style.display = $("#dOps").style.display === "none" ? "block" : "none"; };
    $("#drSubmit").onclick = registerDevice;
    doLoadD();
  }
  async function registerDevice() {
    try {
      const body = { id: $("#drId").value, model_id: $("#drModel").value, current_version: $("#drVersion").value, name: $("#drId").value };
      await http(\BTICK\$(api}/devices\BTICK, "POST", body);
      msgBox($("#devices"), "注册成功");
      doLoadD();
    } catch (e) { msgBox($("#devices"), "失败: " + e.message, true); }
  }
  async function doLoadD() {
    try {
      const p = { page_num: dState.page, page_size: dState.size,
        keyword: $("#dKw").value, model_id: $("#dModel").value,
        group: $("#dGroup").value, status: $("#dStatus").value };
      const qs = Object.entries(p).map(([k,v])=>v?(\BTICK\$(k}=\$(encodeURIComponent(v)}\BTICK):"").filter(x=>x).join("&");
      const r = await http(\BTICK\$(api}/devices?\$(qs}\BTICK);
      const data = r.data || r;
      const rows = data.list || [];
      const tb = document.createElement("table");
      tb.innerHTML = \BTICK<tr><th>ID</th><th>名称</th><th>型号</th><th>当前版本</th><th>IP</th><th>状态</th><th>最后心跳</th><th>操作</th></tr>\BTICK +
        rows.map(d => \BTICK<tr><td>\$(d.id}</td><td>\$(d.name}</td><td>\$(d.model_id}</td><td>\$(d.current_version}</td>
          <td>\$(d.ip||""}</td><td>\$(badge("", d.status)}</td><td>\$(fmt(d.last_heartbeat_at)}</td>
          <td><button class="danger" data-del="\$(d.id}">删除</button></td></tr>\BTICK).join("");
      $("#dResult").innerHTML = "";
      $("#dResult").appendChild(tb);
      $$("button[data-del]", tb).forEach(b => b.onclick = () => deviceDel(b.dataset.del));
      $("#dPager").innerHTML = "";
      pager($("#dPager"), (p, s) => { dState.page = p; dState.size = s; doLoadD(); }, dState.page, dState.size, data.total || 0);
    } catch (e) { msgBox($("#devices"), "加载失败: " + e.message, true); }
  }
  async function deviceDel(id) {
    if (!confirm("删除该设备？")) return;
    try { await http(\BTICK\$(api}/devices/\$(id}\BTICK, "DELETE"); msgBox($("#devices"), "已删除"); doLoadD(); }
    catch (e) { msgBox($("#devices"), "失败: " + e.message, true); }
  }

  // ============ Tasks ============
  const tState = { page: 1, size: 10 };
  function loadTasks() {
    const p = $("#tasks");
    p.innerHTML = \BTICK<div class="row">
      <input id="tKw" placeholder="关键字"/>
      <input id="tModel" placeholder="型号"/>
      <select id="tStatus"><option value="">全部</option><option value="pending">待执行</option><option value="running">进行中</option><option value="paused">暂停</option><option value="finished">已完成</option><option value="canceled">取消</option><option value="failed">失败</option></select>
      <button class="primary" id="tSearch">查询</button>
      <button class="primary" id="tNew">新建任务</button>
    </div>
    <div id="tNewBox" style="display:none;border:1px dashed #ccc;padding:12px;border-radius:8px">
      <div class="row"><input id="tnName" placeholder="任务名"/>
        <input id="tnModel" placeholder="型号ID"/>
        <input id="tnVersion" placeholder="目标版本 vX.Y.Z"/>
        <select id="tnStrategy"><option value="full">全量</option><option value="gray_ratio">灰度比例</option><option value="device_list">指定设备列表</option></select>
        <input id="tnRatio" type="number" min="0" max="100" placeholder="灰度比例 0-100"/>
        <input id="tnDevices" placeholder="设备列表(逗号分隔)"/>
        <input id="tnGroups" placeholder="分组(逗号分隔)"/>
        <button class="primary" id="tnSubmit">提交</button>
      </div>
    </div>
    <div id="tResult"></div><div id="tPager"></div>\BTICK;
    $("#tSearch").onclick = () => { tState.page = 1; doLoadT(); };
    $("#tNew").onclick = () => { $("#tNewBox").style.display = $("#tNewBox").style.display === "none" ? "block" : "none"; };
    $("#tnSubmit").onclick = createTask;
    doLoadT();
  }
  async function createTask() {
    const strategy = $("#tnStrategy").value;
    const ratio = parseInt($("#tnRatio").value || "0", 10);
    const ids = $("#tnDevices").value.split(",").map(s=>s.trim()).filter(Boolean);
    const grp = $("#tnGroups").value.split(",").map(s=>s.trim()).filter(Boolean);
    try {
      await http(\BTICK\$(api}/tasks\BTICK, "POST", {
        name: $("#tnName").value || ("任务 " + Date.now()),
        model_id: $("#tnModel").value,
        target_version: $("#tnVersion").value,
        strategy, gray_ratio: ratio, device_ids: ids, group_filter: grp
      });
      msgBox($("#tasks"), "创建成功");
      doLoadT();
    } catch (e) { msgBox($("#tasks"), "失败: " + e.message, true); }
  }
  async function doLoadT() {
    try {
      const p = { page_num: tState.page, page_size: tState.size,
        keyword: $("#tKw").value, model_id: $("#tModel").value, status: $("#tStatus").value };
      const qs = Object.entries(p).map(([k,v])=>v?(\BTICK\$(k}=\$(encodeURIComponent(v)}\BTICK):"").filter(x=>x).join("&");
      const r = await http(\BTICK\$(api}/tasks?\$(qs}\BTICK);
      const data = r.data || r;
      const rows = data.list || [];
      const tb = document.createElement("table");
      tb.innerHTML = \BTICK<tr><th>ID</th><th>名称</th><th>型号</th><th>目标版本</th><th>策略</th><th>状态</th><th>进度</th><th>创建时间</th><th>操作</th></tr>\BTICK +
        rows.map(t => {
          const pr = t.progress || {};
          const pct = pr.total ? Math.round((pr.success+pr.failed+pr.canceled+pr.running)/pr.total*100) : 0;
          return \BTICK<tr><td>\$(t.id}</td><td>\$(t.name}</td><td>\$(t.model_id}</td><td>\$(t.target_version}</td>
            <td>\$(t.strategy} \$(t.gray_ratio?t.gray_ratio+"%":""}</td>
            <td>\$(badge("", t.status)}</td>
            <td>总数\$(pr.total || 0}/成功\$(pr.success || 0}/失败\$(pr.failed || 0}/进行中\$(pr.running || 0} (\$(pct}%)</td>
            <td>\$(fmt(t.created_at)}</td>
            <td>
              <button data-act="pause" data-id="\$(t.id}">暂停</button>
              <button data-act="resume" data-id="\$(t.id}">恢复</button>
              <button data-act="cancel" data-id="\$(t.id}">取消</button>
              <button class="danger" data-del="\$(t.id}">删除</button>
            </td></tr>\BTICK;
        }).join("");
      $("#tResult").innerHTML = "";
      $("#tResult").appendChild(tb);
      $$("button[data-act]", tb).forEach(b => b.onclick = () => taskAct(b.dataset.id, b.dataset.act));
      $$("button[data-del]", tb).forEach(b => b.onclick = () => taskDel(b.dataset.del));
      $("#tPager").innerHTML = "";
      pager($("#tPager"), (p, s) => { tState.page = p; tState.size = s; doLoadT(); }, tState.page, tState.size, data.total || 0);
    } catch (e) { msgBox($("#tasks"), "加载失败: " + e.message, true); }
  }
  async function taskAct(id, action) {
    try { await http(\BTICK\$(api}/tasks/\$(id}/action\BTICK, "POST", { action }); doLoadT(); }
    catch (e) { msgBox($("#tasks"), "操作失败: " + e.message, true); }
  }
  async function taskDel(id) {
    if (!confirm("删除该任务？")) return;
    try { await http(\BTICK\$(api}/tasks/\$(id}\BTICK, "DELETE"); msgBox($("#tasks"), "已删除"); doLoadT(); }
    catch (e) { msgBox($("#tasks"), "失败: " + e.message, true); }
  }

  // ============ Histories ============
  const hState = { page: 1, size: 10 };
  function loadHistories() {
    const p = $("#histories");
    p.innerHTML = \BTICK<div class="row">
      <input id="hTask" placeholder="任务ID"/>
      <input id="hDevice" placeholder="设备ID"/>
      <select id="hStatus"><option value="">全部</option><option value="success">成功</option><option value="failed">失败</option><option value="canceled">取消</option><option value="running">进行中</option><option value="pending">等待</option></select>
      <button class="primary" id="hSearch">查询</button>
    </div><div id="hResult"></div><div id="hPager"></div>\BTICK;
    $("#hSearch").onclick = () => { hState.page = 1; doLoadH(); };
    doLoadH();
  }
  async function doLoadH() {
    try {
      const p = { page_num: hState.page, page_size: hState.size,
        task_id: $("#hTask").value, device_id: $("#hDevice").value, status: $("#hStatus").value };
      const qs = Object.entries(p).map(([k,v])=>v?(\BTICK\$(k}=\$(encodeURIComponent(v)}\BTICK):"").filter(x=>x).join("&");
      const r = await http(\BTICK\$(api}/histories?\$(qs}\BTICK);
      const data = r.data || r;
      const rows = data.list || [];
      const tb = document.createElement("table");
      tb.innerHTML = \BTICK<tr><th>ID</th><th>任务</th><th>设备</th><th>来源</th><th>目标</th><th>状态</th><th>进度</th><th>耗时(ms)</th><th>错误</th><th>开始</th><th>结束</th></tr>\BTICK +
        rows.map(h => \BTICK<tr><td>\$(h.id}</td><td>\$(h.task_id||""}</td><td>\$(h.device_id}</td>
          <td>\$(h.from_version||""}</td><td>\$(h.to_version||""}</td>
          <td>\$(badge("", h.status)}</td><td>\$(h.progress||0}%</td><td>\$(h.duration_ms||0}</td>
          <td title="\$(h.error_message||""}">\$((h.error_message||"").substring(0,30)}</td>
          <td>\$(fmt(h.started_at)}</td><td>\$(fmt(h.finished_at)}</td></tr>\BTICK).join("");
      $("#hResult").innerHTML = "";
      $("#hResult").appendChild(tb);
      $("#hPager").innerHTML = "";
      pager($("#hPager"), (p, s) => { hState.page = p; hState.size = s; doLoadH(); }, hState.page, hState.size, data.total || 0);
    } catch (e) { msgBox($("#histories"), "加载失败: " + e.message, true); }
  }

  // ============ Stats ============
  async function loadStats() {
    const p = $("#stats");
    p.innerHTML = "<div id='statTop' class='stat-row'></div><hr/><h3>版本分布</h3><div id='statVersion'></div><h3>型号分布</h3><div id='statModel'></div><h3>最近14天升级</h3><div id='statDaily'></div>";
    try {
      const r = await http(\BTICK\$(api}/stats/overview\BTICK);
      const s = r.data || r;
      const cards = [
        ["设备总数", s.device_count||0], ["在线设备", s.online_count||0], ["固件总数", s.firmware_count||0],
        ["任务总数", s.task_count||0], ["进行中任务", s.running_task_count||0],
        ["累计升级次数", s.total_upgrade_count||0], ["成功次数", s.success_upgrade_count||0],
        ["失败次数", s.fail_upgrade_count||0], ["成功率", ((s.success_rate||0)*100).toFixed(2)+"%"]
      ];
      $("#statTop").innerHTML = cards.map(([k,v])=>\BTICK<div class="card"><div class="k">\$(k}</div><div class="v">\$(v}</div></div>\BTICK).join("");
      $("#statVersion").innerHTML = renderKV(s.version_distribution || {});
      $("#statModel").innerHTML = renderKV(s.model_distribution || {});
      const daily = s.daily_upgrade_history || [];
      $("#statDaily").innerHTML = daily.length ? \BTICK<table>\BTICK<tr><th>日期</th><th>总数</th><th>成功</th><th>失败</th></tr>\BTICK +
        daily.map(d => \BTICK<tr><td>\$(d.date}</td><td>\$(d.total}</td><td>\$(d.success}</td><td>\$(d.failed}</td></tr>\BTICK).join("") + \BTICK</table>\BTICK : "暂无数据";
    } catch (e) {
      p.innerHTML += "<div class='message error'>加载失败: " + e.message + "</div>";
    }
  }
  function renderKV(m) {
    const entries = Object.entries(m);
    if (!entries.length) return "<div>暂无数据</div>";
    entries.sort((a,b)=>b[1]-a[1]);
    return \BTICK<table>\BTICK<tr><th>Key</th><th>数量</th></tr>\BTICK + entries.map(([k,v])=>\BTICK<tr><td>\$(k}</td><td>\$(v}</td></tr>\BTICK).join("") + \BTICK</table>\BTICK;
  }

  $("#healthBtn").onclick = async () => {
    try {
      const r = await http("/health");
      $("#healthOut").textContent = \BTICK状态: \$(r.data.status}, 运行时间: \$(r.data.uptime}, 版本: \$(r.data.version}\BTICK;
    } catch (e) { $("#healthOut").textContent = "健康检查失败: " + e.message; }
  };

  // 进入默认 tab
  tab("models");
})();
`
