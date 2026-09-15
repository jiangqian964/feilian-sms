// 通用交互：Toast、二次确认模态框、复制、胶囊开关、状态徽章、保存按钮态。
import { h, $id, clear } from './dom.js';

let toastSeq = 0;

export function toast(type, message, field) {
  const region = $id('toast-region');
  const id = ++toastSeq;
  const el = h('div', { class: `toast ${type || ''}`, role: 'status', dataset: { id } },
    h('div', { class: 't-msg' },
      h('span', { text: message }),
      field ? h('span', { class: 't-field', text: `字段：${field}` }) : null));
  region.append(el);
  setTimeout(() => {
    el.style.opacity = '0';
    el.style.transition = 'opacity .25s';
    setTimeout(() => el.remove(), 260);
  }, 4200);
  return id;
}

// confirmModal 返回 Promise<boolean>；危险操作用 danger。
export function confirmModal({ title, body, okText = '确认', danger = false }) {
  return new Promise((resolve) => {
    const root = $id('modal-root');
    const close = (val) => {
      root.hidden = true;
      clear(root);
      document.removeEventListener('keydown', onKey);
      resolve(val);
    };
    const onKey = (e) => { if (e.key === 'Escape') close(false); };

    const okBtn = h('button', {
      class: `btn ${danger ? 'danger' : 'primary'}`,
      on: { click: () => close(true) },
    }, okText);
    const cancelBtn = h('button', { class: 'btn', on: { click: () => close(false) } }, '取消');

    const mask = h('div', { class: 'modal-mask', on: { click: (e) => { if (e.target === mask) close(false); } } },
      h('div', { class: 'modal', role: 'dialog', 'aria-modal': 'true', 'aria-label': title },
        h('div', { class: 'modal-head' }, h('h3', { class: 'modal-title', text: title })),
        h('div', { class: 'modal-body' }, typeof body === 'string' ? h('p', { style: 'margin:0;color:var(--text-2)', text: body }) : body),
        h('div', { class: 'modal-foot' }, cancelBtn, okBtn)));

    clear(root);
    root.append(mask);
    root.hidden = false;
    document.addEventListener('keydown', onKey);
    okBtn.focus();
  });
}

// 复制文本：优先 Clipboard API，降级临时选区。
export async function copyText(text) {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch { /* 走降级 */ }
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.append(ta);
    ta.select();
    const ok = document.execCommand('copy');
    ta.remove();
    return ok;
  } catch {
    return false;
  }
}

// 胶囊开关（role=switch）；返回 button，onToggle 返回 Promise 期间禁用。
export function switchEl(checked, ariaLabel, onToggle) {
  const btn = h('button', {
    type: 'button',
    class: 'switch',
    role: 'switch',
    aria: { checked: String(!!checked), label: ariaLabel },
  });
  btn.setChecked = (v) => {
    btn.setAttribute('aria-checked', String(!!v));
  };
  btn.addEventListener('click', async () => {
    if (btn.disabled) return;
    btn.disabled = true;
    const next = btn.getAttribute('aria-checked') !== 'true';
    btn.setChecked(next); // 乐观更新；onToggle 抛错或显式调 setChecked 可回滚
    try {
      await onToggle(next, btn);
    } finally {
      btn.disabled = false;
    }
  });
  btn.setChecked(checked);
  return btn;
}

export function badge(text, variant) {
  return h('span', { class: `badge ${variant || 'muted'}`, text });
}

// 按钮执行异步动作期间显示转圈并禁用。
export async function withSaving(btn, fn) {
  if (btn.classList.contains('saving')) return;
  const old = btn.textContent;
  btn.classList.add('saving');
  btn.disabled = true;
  try {
    return await fn();
  } finally {
    btn.classList.remove('saving');
    btn.disabled = false;
    btn.textContent = old;
  }
}

// 错误统一提示：优先在字段容器内显示行内错误，同时 toast。
export function showFieldError(container, field, message) {
  if (!container) return;
  const slot = container.querySelector(`[data-error-for="${cssEscape(field)}"]`);
  if (slot) {
    slot.textContent = message;
    const input = container.querySelector(`[data-field="${cssEscape(field)}"]`);
    if (input) input.classList.add('is-error');
  }
}

export function clearFieldErrors(container) {
  if (!container) return;
  container.querySelectorAll('.field-error').forEach((n) => { n.textContent = ''; });
  container.querySelectorAll('.is-error').forEach((n) => n.classList.remove('is-error'));
}

function cssEscape(s) {
  return window.CSS && CSS.escape ? CSS.escape(s) : String(s).replace(/"/g, '\\"');
}
