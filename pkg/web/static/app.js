'use strict';

// 记忆库阅读页前端：目录树懒加载 + 正文渲染 + 检索 + 下载 + 回收站（软删除）。
// 全部请求走同源 Cookie 认证（登录时下发），故无需在脚本里保存口令。

const $ = (id) => document.getElementById(id);
const treeEl = $('sidebar');
const contentEl = $('content');
const statusEl = $('status');
const trashEntryEl = $('trash-entry');
const trashCountEl = $('trash-count');

let currentPath = '';   // 当前正在看的文件（用于删除后提示）
let trashCache = [];    // 回收站条目缓存（恢复/清除后局部刷新）

// ---- 请求 ----

function apiURL(path, params) {
  const url = new URL(path, window.location.origin);
  for (const [k, v] of Object.entries(params || {})) {
    url.searchParams.set(k, v);
  }
  return url;
}

async function handleResp(res) {
  if (res.status === 401) {
    window.location.replace('/memory/login');
    throw new Error('未登录');
  }
  if (!res.ok) {
    let msg = 'HTTP ' + res.status;
    try {
      const body = await res.json();
      if (body && body.error) msg = body.error;
    } catch (err) {
      // 非 JSON 响应：保留 HTTP 状态作为提示
    }
    throw new Error(msg);
  }
  return res.json();
}

// api 调只读接口（GET），401 时跳登录页。
async function api(path, params) {
  return handleResp(await fetch(apiURL(path, params), { headers: { Accept: 'application/json' } }));
}

// apiPost 调写接口：**只发 POST**（服务端对 GET 一律 405，防 CSRF）。
async function apiPost(path, body) {
  return handleResp(await fetch(apiURL(path), {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify(body || {}),
  }));
}

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null) node.textContent = text;
  return node;
}

function joinPath(parent, name) {
  return parent ? parent + '/' + name : name;
}

function showError(err) {
  statusEl.textContent = '出错了：' + err.message;
}

function showStatus(text) {
  statusEl.textContent = text;
}

// ---- 目录树 ----

function nodeFor(entry, parent) {
  const li = el('li', entry.dir ? 'dir' : 'file');
  const label = el('span', 'label');
  label.appendChild(el('span', 'twisty', entry.dir ? '▸' : '·'));
  label.appendChild(el('span', 'name', entry.name));

  if (entry.dir) {
    const children = el('ul', 'children');
    children.hidden = true;
    label.addEventListener('click', () => toggleDir(li, children, joinPath(parent, entry.name)));
    li.append(label, children);
  } else {
    label.addEventListener('click', () => {
      markActive(label);
      openFile(joinPath(parent, entry.name));
    });
    li.appendChild(label);
  }
  return li;
}

async function toggleDir(li, children, path) {
  const twisty = li.querySelector('.twisty');
  if (children.dataset.loaded !== '1') {
    try {
      const data = await api('/memory/api/tree', { path });
      for (const entry of data.entries) {
        children.appendChild(nodeFor(entry, path));
      }
      children.dataset.loaded = '1';
    } catch (err) {
      showError(err);
      return;
    }
  }
  children.hidden = !children.hidden;
  twisty.textContent = children.hidden ? '▸' : '▾';
}

function markActive(label) {
  for (const node of treeEl.querySelectorAll('.label.active')) {
    node.classList.remove('active');
  }
  label.classList.add('active');
}

async function loadRoot() {
  try {
    const data = await api('/memory/api/tree', { path: '.' });
    const ul = el('ul', 'tree');
    for (const entry of data.entries) {
      ul.appendChild(nodeFor(entry, ''));
    }
    treeEl.replaceChildren(ul);
  } catch (err) {
    showError(err);
  }
}

// ---- 正文 ----

async function openFile(path) {
  try {
    const data = await api('/memory/api/file', { path });
    currentPath = data.path;

    const bar = el('div', 'filebar');
    bar.appendChild(el('span', 'filepath', data.path));

    const download = el('a', 'action', '下载');
    download.href = '/memory/api/raw?path=' + encodeURIComponent(data.path);
    bar.appendChild(download);

    const copy = el('button', 'action', '复制路径');
    copy.addEventListener('click', async () => {
      try {
        await navigator.clipboard.writeText(data.path);
        copy.textContent = '已复制';
        setTimeout(() => { copy.textContent = '复制路径'; }, 1500);
      } catch (err) {
        showError(err);
      }
    });
    bar.appendChild(copy);

    const del = el('button', 'action danger', '删除');
    del.title = '移入回收站（可在回收站恢复）';
    del.addEventListener('click', () => deleteFile(data.path));
    bar.appendChild(del);

    const body = el('div', 'doc');
    if (data.kind === 'markdown') {
      // 服务端已渲染且转义了原始 HTML，可安全注入。
      body.innerHTML = data.html;
    } else if (data.kind === 'csv') {
      // 服务端用标准库解析 CSV/TSV 并逐单元格转义，可安全注入。
      body.innerHTML = data.html;
      if (data.truncated) {
        body.appendChild(el('p', 'hint', '表格仅显示前 5000 行，完整内容请点上方「下载」。'));
      }
    } else if (data.kind === 'text') {
      // 用 textContent 写入原文：浏览器负责转义，不做 HTML 解析。
      const pre = el('pre');
      pre.textContent = data.text;
      body.appendChild(pre);
    } else if (data.kind === 'large') {
      body.appendChild(el('p', 'hint', '文件较大（' + data.size + ' 字节），请在标题栏点「下载」查看。'));
    } else {
      body.appendChild(el('p', 'hint', '二进制文件（' + data.size + ' 字节），请在标题栏点「下载」查看。'));
    }

    contentEl.replaceChildren(bar, body);
    showStatus(data.path + ' · ' + data.size + ' 字节' + (data.mtime ? ' · 修改于 ' + data.mtime : ''));
  } catch (err) {
    showError(err);
  }
}

// ---- 删除 / 回收站 ----

async function deleteFile(path) {
  if (!window.confirm('把「' + path + '」移入回收站？\n\n文件不会被真正删除，可随时从回收站恢复。')) {
    return;
  }
  try {
    await apiPost('/memory/api/delete', { path });
    currentPath = '';
    contentEl.replaceChildren(el('p', 'hint', '已移入回收站：' + path));
    showStatus('已移入回收站：' + path);
    await loadRoot();
    await refreshTrashCount();
  } catch (err) {
    showError(err);
  }
}

async function refreshTrashCount() {
  try {
    const data = await api('/memory/api/trash');
    trashCache = data.entries;
    trashCountEl.textContent = String(data.count);
    trashEntryEl.hidden = false;
  } catch (err) {
    // 回收站不可用时不阻塞主界面
  }
}

function renderTrashView(entries) {
  const head = el('div', 'trash-head');
  head.appendChild(el('h2', 'trash-title', '回收站'));
  head.appendChild(el('span', 'trash-sub', entries.length + ' 项 · 可恢复'));
  const spacer = el('span', 'spacer');
  head.appendChild(spacer);
  const back = el('button', 'action', '← 返回');
  back.addEventListener('click', () => { location.hash = ''; });
  head.appendChild(back);
  if (entries.length > 0) {
    const clear = el('button', 'action danger', '清空回收站');
    clear.addEventListener('click', clearTrash);
    head.appendChild(clear);
  }

  const wrap = el('div', 'trash-body');
  if (entries.length === 0) {
    wrap.appendChild(el('p', 'empty', '回收站是空的'));
  } else {
    const table = el('table', 'trash-table');
    const thead = el('thead');
    const hr = el('tr');
    for (const t of ['原路径', '删除时间', '大小', '操作']) hr.appendChild(el('th', null, t));
    thead.appendChild(hr);
    table.appendChild(thead);
    const tbody = el('tbody');
    for (const item of entries) {
      const tr = el('tr');
      tr.appendChild(el('td', 'path-cell', item.path));
      tr.appendChild(el('td', 'time', item.deleted_at));
      tr.appendChild(el('td', 'size', String(item.size) + ' 字节'));
      const ops = el('td', 'ops');
      const restore = el('button', 'action', '恢复');
      restore.addEventListener('click', () => restoreFile(item));
      const purge = el('button', 'action danger', '清除');
      purge.addEventListener('click', () => purgeFile(item));
      ops.append(restore, purge);
      tr.appendChild(ops);
      tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    wrap.appendChild(table);
  }

  contentEl.replaceChildren(head, wrap);
  showStatus('回收站 · ' + entries.length + ' 项');
}

async function openTrash() {
  try {
    const data = await api('/memory/api/trash');
    trashCache = data.entries;
    trashCountEl.textContent = String(data.count);
    renderTrashView(data.entries);
  } catch (err) {
    showError(err);
  }
}

async function restoreFile(item) {
  try {
    const res = await apiPost('/memory/api/restore', { id: item.id });
    showStatus('已恢复：' + (res.path || item.path));
    await loadRoot();
    await openTrash();
  } catch (err) {
    showError(err);
  }
}

async function purgeFile(item) {
  if (!window.confirm('彻底删除「' + item.path + '」？\n\n此操作不可撤销。')) {
    return;
  }
  try {
    await apiPost('/memory/api/purge', { id: item.id });
    showStatus('已彻底删除：' + item.path);
    await openTrash();
  } catch (err) {
    showError(err);
  }
}

async function clearTrash() {
  if (!window.confirm('清空回收站？\n\n所有条目将被彻底删除，不可撤销。')) {
    return;
  }
  try {
    const res = await apiPost('/memory/api/trash/clear', {});
    showStatus('回收站已清空（' + res.cleared + ' 项）');
    await openTrash();
  } catch (err) {
    showError(err);
  }
}

// ---- 检索 ----

function renderSnippet(snippet) {
  const frag = document.createDocumentFragment();
  // 服务端用 [ ] 标记命中词；按下标奇偶切成普通文本与高亮片段。
  snippet.split(/\[|\]/).forEach((part, i) => {
    if (i % 2 === 1) {
      frag.appendChild(el('mark', null, part));
    } else {
      frag.appendChild(document.createTextNode(part));
    }
  });
  return frag;
}

async function runSearch(query) {
  if (!query.trim()) return;
  const mode = $('mode').value;
  try {
    const data = await api('/memory/api/search', { q: query, mode: mode, limit: 50 });
    const head = el('p', 'hint', '模式 ' + data.mode.toUpperCase() + ' · 命中 ' + data.count + ' 条');
    contentEl.replaceChildren(head);
    if (data.count === 0) {
      showStatus('搜索「' + query + '」无命中，可试试 OR 模式或减少关键词。');
      return;
    }
    const ul = el('ul', 'hits');
    for (const hit of data.hits) {
      const li = el('li');
      const link = el('a', 'hit-path', hit.path);
      link.href = '#';
      link.addEventListener('click', (ev) => {
        ev.preventDefault();
        openFile(hit.path);
      });
      const snippet = el('p', 'snippet');
      snippet.appendChild(renderSnippet(hit.snippet));
      li.append(link, snippet);
      ul.appendChild(li);
    }
    contentEl.appendChild(ul);
    showStatus('搜索「' + query + '」· ' + data.mode.toUpperCase() + ' · ' + data.count + ' 条');
  } catch (err) {
    showError(err);
  }
}

// ---- 视图路由（#trash = 回收站子页） ----

function renderHashView() {
  if (location.hash === '#trash') {
    openTrash();
  } else if (location.hash === '' || location.hash === '#browse') {
    if (currentPath) {
      openFile(currentPath);
    } else {
      contentEl.replaceChildren(el('p', 'hint', '在左侧展开目录选择文件，或在上方搜索。'));
    }
  }
}

// ---- 装配 ----

$('search-form').addEventListener('submit', (ev) => {
  ev.preventDefault();
  runSearch($('q').value);
});

$('home').addEventListener('click', () => {
  currentPath = '';
  loadRoot();
  if (location.hash === '#trash') location.hash = '';
  contentEl.replaceChildren(el('p', 'hint', '在左侧展开目录选择文件，或在上方搜索。'));
  refreshTrashCount();
});

trashEntryEl.addEventListener('click', () => { location.hash = '#trash'; });

window.addEventListener('hashchange', renderHashView);

$('logout').addEventListener('click', async () => {
  try {
    await fetch('/memory/api/logout', { method: 'POST', headers: { Accept: 'application/json' } });
  } catch (err) {
    // 退出失败也要离开页面：Cookie 过期前不再访问即可
  }
  window.location.replace('/memory/login');
});

(async function init() {
  await loadRoot();
  await refreshTrashCount();
  try {
    const st = await api('/memory/api/status');
    showStatus('共 ' + st.files + ' 个文件 · ' + st.bytes + ' 字节' +
      (st.last_modified ? ' · 最近修改 ' + st.last_modified : ''));
  } catch (err) {
    showError(err);
  }
  if (location.hash === '#trash') renderHashView();
})();
