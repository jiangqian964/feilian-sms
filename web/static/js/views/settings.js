// 视图 1：系统设置——接入校验、事件订阅地址、运行阈值。
import { api } from '../api.js';
import { h, clear, skeletonRows, fmtDateTime } from '../dom.js';
import { toast, copyText, withSaving, showFieldError, clearFieldErrors } from '../ui.js';

export async function mountSettings(root) {
  root.append(pageHead());
  const formWrap = h('div');
  root.append(formWrap);
  skeletonRows(formWrap, 6);
  const data = await api.get('/api/settings');
  render(formWrap, data);
  return async () => {
    try {
      const fresh = await api.get('/api/settings');
      render(formWrap, fresh);
    } catch (err) { toast('error', err.message); }
  };
}

function pageHead() {
  return h('div', { class: 'page-head' },
    h('div', null,
      h('h2', { class: 'page-title', text: '系统设置' }),
      h('div', { class: 'page-sub', text: '飞连事件订阅接入参数与运行阈值，保存后热生效，无需重启服务。' })));
}

function field(labelText, input, opts = {}) {
  const { fieldName, required = false, help } = opts;
  return h('div', { class: 'field' },
    h('label', null, labelText, required ? h('span', { class: 'req', text: '*' }) : null),
    input,
    help ? h('span', { class: 'help-text', text: help }) : null,
    h('span', { class: 'field-error', dataset: { errorFor: fieldName || '' } }));
}

function readonlyCopy(labelText, codeEl, btn) {
  return h('div', { class: 'field span-2' },
    h('label', { text: labelText }),
    h('div', { class: 'readonly-line' }, codeEl, btn));
}

function render(root, s) {
  clear(root);

  const tokenIn = h('input', { class: 'input', value: s.verification_token, autocomplete: 'off',
    dataset: { field: 'verification_token' }, 'aria-label': 'Verification Token' });
  const newKeyIn = h('input', { class: 'input', type: 'password', placeholder: '留空表示不修改',
    autocomplete: 'new-password', dataset: { field: 'encrypt_key' }, 'aria-label': '新 Encrypt Key' });
  const clearKeyCb = h('input', { type: 'checkbox', dataset: { field: 'clear_encrypt_key' },
    'aria-label': '清空 Encrypt Key' });

  // 互斥：输入新值时禁用清空勾选，反之亦然。
  newKeyIn.addEventListener('input', () => { clearKeyCb.disabled = !!newKeyIn.value; });
  clearKeyCb.addEventListener('change', () => { newKeyIn.disabled = clearKeyCb.checked; });
  if (!s.encrypt_key_set) clearKeyCb.disabled = true;

  const keyBadge = h('span', null,
    badgeInline(s.encrypt_key_set ? `已设置：${s.encrypt_key_masked}` : '未设置（本期默认不启用事件加密）',
      s.encrypt_key_set ? 'success' : 'muted'));

  // 厂商回执鉴权 token：与 Encrypt Key 相同的掩码/留空不改/清空互斥语义。
  const newReceiptTokenIn = h('input', { class: 'input', type: 'password', placeholder: '留空表示不修改（至少 16 个字符）',
    autocomplete: 'new-password', dataset: { field: 'receipt_auth_token' }, 'aria-label': '新厂商回执鉴权 Token' });
  const clearReceiptCb = h('input', { type: 'checkbox', dataset: { field: 'clear_receipt_auth_token' },
    'aria-label': '清空厂商回执鉴权 Token' });
  newReceiptTokenIn.addEventListener('input', () => { clearReceiptCb.disabled = !!newReceiptTokenIn.value; });
  clearReceiptCb.addEventListener('change', () => { newReceiptTokenIn.disabled = clearReceiptCb.checked; });
  if (!s.receipt_auth_token_set) clearReceiptCb.disabled = true;

  const receiptTokenBadge = h('span', null,
    badgeInline(s.receipt_auth_token_set ? `已设置：${s.receipt_auth_token_masked}` : '未设置（回执不校验 token，请确保网络层隔离）',
      s.receipt_auth_token_set ? 'success' : 'muted'));

  const pathIn = h('input', { class: 'input', value: s.webhook_path, placeholder: '/feilian/sms/events',
    dataset: { field: 'webhook_path' }, 'aria-label': 'Webhook 接收路径' });
  const baseIn = h('input', { class: 'input', value: s.public_base_url, placeholder: 'http://网关IP:8080',
    dataset: { field: 'public_base_url' }, 'aria-label': '对外访问基址' });

  const webhookCode = h('code', { id: 'ro-webhook', text: s.webhook_url || '—' });
  const receiptExample = h('code', { id: 'ro-receipt', text: receiptExampleURL(s.public_base_url) });
  const copyBtn = (target, label) => h('button', {
    type: 'button', class: 'btn tiny', 'aria-label': label,
    on: {
      click: async () => {
        const text = target.textContent;
        if (!text || text === '—') { toast('warn', '暂无可复制的地址'); return; }
        const ok = await copyText(text);
        toast(ok ? 'success' : 'error', ok ? '已复制到剪贴板' : '复制失败，请手动选择文本');
      },
    },
  }, '复制');

  const timeoutIn = h('input', { class: 'input tnum', type: 'number', min: '100', max: '60000', step: '100',
    value: String(s.downstream_timeout_ms), dataset: { field: 'downstream_timeout_ms' }, 'aria-label': '下游超时毫秒' });
  const staleIn = h('input', { class: 'input tnum', type: 'number', min: '1000', max: '86400000', step: '1000',
    value: String(s.stale_pending_ms), dataset: { field: 'stale_pending_ms' }, 'aria-label': '补发阈值毫秒' });

  const saveBtn = h('button', { type: 'button', class: 'btn primary', text: '保存设置' });

  const cardAccess = h('div', { class: 'card' },
    h('div', { class: 'card-head' }, h('h3', { class: 'card-title', text: '接入校验' })),
    h('div', { class: 'card-body' },
      h('div', { class: 'form-grid' },
        field('Verification Token', tokenIn, { fieldName: 'verification_token', required: true,
          help: '与飞连事件订阅的 Verification Token 一致，用于校验请求来源（明文保存，非密钥）。' }),
        h('div', { class: 'field' },
          h('label', { text: 'Encrypt Key（事件加密，本期可不启用）' }),
          keyBadge,
          newKeyIn,
          h('label', { class: 'inline-check' }, clearKeyCb, '清空已保存的 Encrypt Key'),
          h('span', { class: 'field-error', dataset: { errorFor: 'encrypt_key' } })),
        h('div', { class: 'field' },
          h('label', { text: '厂商回执鉴权 Token（可选）' }),
          receiptTokenBadge,
          newReceiptTokenIn,
          h('label', { class: 'inline-check' }, clearReceiptCb, '清空已保存的回执鉴权 Token'),
          h('span', { class: 'help-text', text: '设置后厂商回执须在 X-Receipt-Token 请求头或 ?token= 参数携带等值 token；至少 16 个字符，留空表示不修改，清空后回执不校验（依赖网络层隔离）。' }),
          h('span', { class: 'field-error', dataset: { errorFor: 'receipt_auth_token' } })))));

  const cardURL = h('div', { class: 'card' },
    h('div', { class: 'card-head' },
      h('h3', { class: 'card-title', text: '事件订阅地址' }),
      h('span', { class: 'card-hint', text: '在飞连后台事件订阅中填写完整地址' })),
    h('div', { class: 'card-body' },
      h('div', { class: 'form-grid' },
        field('Webhook 接收路径', pathIn, { fieldName: 'webhook_path', required: true, help: '必须以 / 开头，且不含空白；保存后旧路径立即失效。' }),
        field('对外访问基址', baseIn, { fieldName: 'public_base_url', help: '留空则仅展示相对路径；须为 http(s) 地址。' }),
        readonlyCopy('完整事件订阅 URL（只读）', webhookCode, copyBtn(webhookCode, '复制事件订阅 URL')),
        readonlyCopy('厂商回执地址示例（只读，{通道ID} 按通道替换）', receiptExample, copyBtn(receiptExample, '复制回执地址示例')))));

  const cardRuntime = h('div', { class: 'card' },
    h('div', { class: 'card-head' }, h('h3', { class: 'card-title', text: '运行阈值' })),
    h('div', { class: 'card-body' },
      h('div', { class: 'form-grid' },
        field('单次下发超时（毫秒）', timeoutIn, { fieldName: 'downstream_timeout_ms', help: '范围 100~60000；飞连要求 3 秒内响应，建议 ≤2000。' }),
        field('在途补发阈值（毫秒）', staleIn, { fieldName: 'stale_pending_ms', help: '范围 1000~86400000；超过该时长仍 pending 的记录由补发任务重试。' }))));

  const updated = h('span', { class: 'card-hint', text: s.updated_at ? `上次更新：${fmtDateTime(s.updated_at)}` : '' });
  const foot = h('div', { class: 'actions' }, saveBtn, updated);

  root.append(cardAccess, cardURL, cardRuntime, foot);

  saveBtn.addEventListener('click', () => withSaving(saveBtn, save));

  async function save() {
    clearFieldErrors(root);
    const body = {
      verification_token: tokenIn.value,
      webhook_path: pathIn.value,
      public_base_url: baseIn.value.trim(),
      downstream_timeout_ms: Number(timeoutIn.value),
      stale_pending_ms: Number(staleIn.value),
    };
    if (newKeyIn.value) body.encrypt_key = newKeyIn.value;
    if (clearKeyCb.checked) body.clear_encrypt_key = true;
    if (newReceiptTokenIn.value) body.receipt_auth_token = newReceiptTokenIn.value;
    if (clearReceiptCb.checked) body.clear_receipt_auth_token = true;

    try {
      const next = await api.put('/api/settings', body);
      render(root, next);
      toast('success', '系统设置已保存并热生效');
    } catch (err) {
      if (err.field) showFieldError(root, err.field, err.message);
      toast('error', err.message, err.field || '');
    }
  }
}

function receiptExampleURL(base) {
  const b = (base || '').replace(/\/+$/, '');
  return `${b}/receipts/{通道ID}`;
}

function badgeInline(text, variant) {
  return h('span', { class: `badge ${variant}`, text });
}
