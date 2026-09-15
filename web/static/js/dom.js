// 极简 DOM 工具：节点构造、选择、转义、时间/耗时格式化、骨架屏。

export function $id(id) { return document.getElementById(id); }
export function $qs(node, sel) { return node.querySelector(sel); }
export function $all(node, sel) { return Array.from(node.querySelectorAll(sel)); }

export function clear(node) {
  while (node.firstChild) node.removeChild(node.firstChild);
}

// h('div', {class, type, value, text, html, attrs:{}, aria:{}, checked, disabled, on:{click}}, children...)
export function h(tag, props, ...children) {
  const el = document.createElement(tag);
  if (props) {
    for (const [k, v] of Object.entries(props)) {
      if (v === null || v === undefined || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k === 'text') el.textContent = v;
      else if (k === 'html') el.innerHTML = v;
      else if (k === 'checked' || k === 'disabled') { if (v) el[k] = true; }
      else if (k === 'on' && v) {
        for (const [ev, fn] of Object.entries(v)) el.addEventListener(ev, fn);
      } else if (k === 'attrs') {
        for (const [ak, av] of Object.entries(v)) el.setAttribute(ak, av);
      } else if (k === 'aria') {
        for (const [ak, av] of Object.entries(v)) el.setAttribute('aria-' + ak, String(av));
      } else if (k === 'dataset') {
        Object.assign(el.dataset, v);
      } else {
        el.setAttribute(k, v);
      }
    }
  }
  for (const child of children.flat(Infinity)) {
    if (child === null || child === undefined || child === false) continue;
    el.append(child.nodeType ? child : document.createTextNode(String(child)));
  }
  return el;
}

export function escapeHtml(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
  ));
}

const pad = (n) => String(n).padStart(2, '0');

// 毫秒时间戳 → 本地 YYYY-MM-DD HH:mm:ss；0/空返回 —。
export function fmtDateTime(ms) {
  if (!ms) return '—';
  const d = new Date(ms);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

// datetime-local 输入值 ↔ 毫秒。
export function localInputValue(ms) {
  if (!ms) return '';
  const d = new Date(ms);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
export function localInputMS(v) {
  if (!v) return 0;
  const t = new Date(v).getTime();
  return Number.isNaN(t) ? 0 : t;
}

export function fmtDuration(ms) {
  if (!ms && ms !== 0) return '—';
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

// 骨架屏：向 parent 填充 n 行。
export function skeletonRows(parent, n = 5) {
  clear(parent);
  for (let i = 0; i < n; i++) {
    parent.append(h('div', { class: 'skeleton sk-line', style: `width:${88 - (i % 3) * 17}%` }));
  }
}

// 空态块。
export function emptyState(title, sub) {
  return h('div', { class: 'empty-state' },
    h('div', { class: 'es-title', text: title }),
    sub ? h('div', { text: sub }) : null);
}
