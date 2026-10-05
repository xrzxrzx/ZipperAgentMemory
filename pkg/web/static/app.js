'use strict';

// 记忆库只读阅读页前端：目录树懒加载 + 正文渲染 + 检索 + 下载。
// 全部请求走同源 Cookie 认证（登录时下发），故无需在脚本里保存口令。

const $ = (id) => document.getElementById(id);
const treeEl = $('sidebar');
const contentEl = $('content');
const statusEl = $('status');

// api 调接口：比路径参数拼进 query，401 时跳登录页。
async function api(path, params) {
  const url = new URL(path, window.location.origin);
  for (const [k, v] of Object.entries(params || {})) {
    url.searchParams.set(k, v);
  }
  const res = await fetch(url, { headers: { Accept: 'application/json' } });
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
    statusEl.textContent = data.path + ' · ' + data.size + ' 字节' + (data.mtime ? ' · 修改于 ' + data.mtime : '');
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
      statusEl.textContent = '搜索「' + query + '」无命中，可试试 OR 模式或减少关键词。';
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
    statusEl.textContent = '搜索「' + query + '」· ' + data.mode.toUpperCase() + ' · ' + data.count + ' 条';
  } catch (err) {
    showError(err);
  }
}

// ---- 装配 ----

$('search-form').addEventListener('submit', (ev) => {
  ev.preventDefault();
  runSearch($('q').value);
});

$('home').addEventListener('click', () => {
  loadRoot();
  contentEl.replaceChildren(el('p', 'hint', '在左侧展开目录选择文件，或在上方搜索。'));
});

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
  try {
    const st = await api('/memory/api/status');
    statusEl.textContent = '共 ' + st.files + ' 个文件 · ' + st.bytes + ' 字节' +
      (st.last_modified ? ' · 最近修改 ' + st.last_modified : '');
  } catch (err) {
    showError(err);
  }
})();
