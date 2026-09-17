// 视图 2：通道——List+Detail 双栏、新建（预置/空白）、全分区表单、启停/删除/测试发送。
import { api } from '../api.js';
import { h, clear, emptyState, fmtDateTime } from '../dom.js';
import { toast, confirmModal, withSaving, badge, copyText } from '../ui.js';
import { renderChannelForm, FormError } from './channel-form.js';

const STRATEGY_LABEL = { none: '不签名', sha1_salt: 'SHA-1 加盐', hmac_sha256: 'HMAC-SHA256' };
const ERROR_LABEL = {
  unbound: '场景未绑定', binding_disabled: '绑定已停用', channel_disabled: '通道已停用',
  channel_not_found: '通道不存在', invalid_mobile: '手机号非法', render: '配置渲染失败',
  vendor: '厂商业务失败', network: '网络错误', timeout: '下游超时', internal: '内部错误',
  resend_exhausted: '补发次数耗尽',
};

export async function mountChannels(root) {
  const state = { list: [], selected: null, types: [] };
  api.get('/api/bindings').then((d) => { state.types = d.sms_types || []; }).catch(() => {});

  root.append(h('div', { class: 'page-head' },
    h('div', null,
      h('h2', { class: 'page-title', text: '短信通道' }),
      h('div', { class: 'page-sub', text: '通用 HTTP 通道：字段映射表单 + 可插拔签名，内置一个 HTTP JSON 示例预置。' })),
    h('button', { class: 'btn primary', id: 'btn-new-channel', on: { click: openCreate }, text: '＋ 新建通道' })));

  const listCol = h('div');
  const detailCol = h('div');
  root.append(h('div', { class: 'dual' },
    h('div', { class: 'list-col' }, listCol),
    h('div', { class: 'detail-col' }, detailCol)));

  await refresh();
  return refresh;

  async function refresh(keepSelect = true) {
    const data = await api.get('/api/channels');
    state.list = data.channels;
    renderList();
    if (state.selected && keepSelect && state.list.some((c) => c.id === state.selected)) {
      await select(state.selected);
    } else if (!keepSelect || !state.list.some((c) => c.id === state.selected)) {
      state.selected = null;
      renderPlaceholder();
    }
  }

  function renderList() {
    clear(listCol);
    if (!state.list.length) {
      listCol.append(h('div', { class: 'card card-body' },
        emptyState('还没有通道', '点击右上角「新建通道」，从内置示例预置开始最快。')));
      return;
    }
    const wrap = h('div', { class: 'channel-list' });
    for (const c of state.list) {
      const item = h('button', {
        type: 'button',
        class: `channel-item${state.selected === c.id ? ' is-selected' : ''}`,
        'aria-current': state.selected === c.id ? 'true' : 'false',
        on: { click: () => select(c.id) },
      },
        h('div', { class: 'ci-row' },
          h('span', { class: 'ci-name', text: c.name }),
          badge(c.enabled ? '已启用' : '已停用', c.enabled ? 'success' : 'muted')),
        h('div', { class: 'ci-sub mono', text: STRATEGY_LABEL[(c.config && c.config.sign && c.config.sign.strategy) || 'none'] || c.id }));
      wrap.append(item);
    }
    listCol.append(wrap);
  }

  function renderPlaceholder() {
    clear(detailCol);
    detailCol.append(h('div', { class: 'card card-body' },
      emptyState('选择左侧通道查看详情', '或新建一个通道。')));
  }

  async function select(id) {
    state.selected = id;
    renderList();
    clear(detailCol);
    detailCol.append(h('div', { class: 'card card-body' },
      h('div', { class: 'skeleton sk-line', style: 'width:40%' }),
      h('div', { class: 'skeleton sk-line', style: 'width:70%' }),
      h('div', { class: 'skeleton sk-line', style: 'width:55%' })));
    let ch;
    try {
      ch = await api.get(`/api/channels/${id}`);
    } catch (err) {
      toast('error', err.message);
      return;
    }
    renderDetail(ch);
  }

  function renderDetail(ch) {
    clear(detailCol);
    const form = renderChannelForm(ch);

    const banner = h('div', { hidden: true, role: 'alert' });
    function showBanner(field, message) {
      banner.hidden = false;
      banner.innerHTML = '';
      banner.append(h('div', { class: 'card', style: 'border-color:var(--danger);margin-bottom:16px' },
        h('div', { class: 'card-body', style: 'color:var(--danger);font-size:13px' },
          h('b', { text: '保存被拒绝（字段校验）' }),
          h('div', { style: 'margin-top:4px', text: message }),
          h('div', { class: 'mono', style: 'margin-top:4px;font-size:12px', text: `字段：${field}` }))));
    }

    const enableBtn = h('button', {
      class: `btn ${ch.enabled ? '' : 'primary'} tiny`,
      type: 'button',
      on: {
        click: () => withSaving(enableBtn, async () => {
          const action = ch.enabled ? 'disable' : 'enable';
          const verb = ch.enabled ? '停用' : '启用';
          try {
            const updated = await api.post(`/api/channels/${ch.id}/${action}`);
            toast('success', `通道已${verb}`);
            ch = updated;
            renderList();
            renderDetail(ch);
          } catch (err) {
            toast('error', `${verb}失败：${err.message}`);
          }
        }),
      },
    }, ch.enabled ? '停用' : '启用');

    const delBtn = h('button', { class: 'btn danger tiny', on: { click: doDelete } }, '删除');
    const saveBtn = h('button', { class: 'btn primary', type: 'button', text: '保存修改' });
    saveBtn.addEventListener('click', () => withSaving(saveBtn, doSave));

    const receiptCode = h('code', { text: ch.receipt_url || '（设置对外基址后生成）' });
    const copyReceipt = h('button', {
      class: 'btn tiny', type: 'button', text: '复制回执地址',
      on: { click: async () => {
        if (!ch.receipt_url) { toast('warn', '请先在系统设置中配置对外基址'); return; }
        const ok = await copyText(ch.receipt_url);
        toast(ok ? 'success' : 'error', ok ? '已复制回执地址' : '复制失败');
      } },
    });

    const headCard = h('div', { class: 'card' },
      h('div', { class: 'card-head' },
        h('div', null,
          h('h3', { class: 'card-title', text: ch.name }),
          h('div', { class: 'card-hint mono', style: 'margin-top:2px', text: ch.id })),
        h('div', { class: 'actions' },
          badge(ch.enabled ? '已启用' : '已停用', ch.enabled ? 'success' : 'muted'),
          enableBtn, delBtn)),
      h('div', { class: 'card-body' },
        h('div', { class: 'readonly-line' }, receiptCode, copyReceipt),
        ch.updated_at ? h('div', { class: 'card-hint', style: 'margin-top:8px',
          text: `最近更新：${fmtDateTime(ch.updated_at)}` }) : null));

    const testCard = buildTestPanel(ch);

    detailCol.append(headCard, banner, form.el,
      h('div', { class: 'actions', style: 'margin:4px 0 16px' },
        saveBtn,
        h('span', { class: 'card-hint', text: '保存前将用样例输入完整试渲染；未填写密钥的脚手架允许保存。' })),
      testCard);

    async function doSave() {
      banner.hidden = true;
      let body;
      try {
        body = form.collect();
      } catch (err) {
        if (err instanceof FormError) { showBanner(err.field, err.message); toast('error', err.message, err.field); }
        else toast('error', err.message);
        return;
      }
      try {
        const updated = await api.put(`/api/channels/${ch.id}`, body);
        toast('success', '通道配置已保存');
        ch = updated;
        await refresh();
      } catch (err) {
        if (err.status === 400 && err.field) { showBanner(err.field, err.message); toast('error', err.message, err.field); }
        else toast('error', err.message);
      }
    }

    async function doDelete() {
      const ok = await confirmModal({
        title: '删除通道',
        body: `确认删除通道「${ch.name}」？删除后该通道的场景绑定将失效，历史发送记录保留。此操作不可撤销。`,
        okText: '确认删除', danger: true,
      });
      if (!ok) return;
      try {
        await api.del(`/api/channels/${ch.id}`);
      } catch (err) {
        toast('error', `删除失败：${err.message}`);
        return;
      }
      toast('success', '通道已删除');
      state.selected = null;
      await refresh(false);
    }
  }

  // ---------- 测试发送面板 ----------
  function buildTestPanel(ch) {
    const typeSel = h('select', { class: 'input', 'aria-label': '测试短信场景' },
      ...(state.types.length ? stateTypes() : [['code', 'code']]).map(([v, l]) => h('option', { value: v, text: l })));
    function stateTypes() { return state.types.map((t) => [t, t]); }
    const tplIn = h('input', { class: 'input', placeholder: '模板码，如 SMS_0001', 'aria-label': '模板码' });
    const ccIn = h('input', { class: 'input', value: '+86', 'aria-label': '国家码' });
    const mobileIn = h('input', { class: 'input', placeholder: '不含国家码，如 13800000000', 'aria-label': '手机号' });
    const paramsIn = h('textarea', { class: 'input', placeholder: '模板参数，每行一个，例如：\n123456\n5', 'aria-label': '模板参数' });
    const resultBox = h('div');
    const btn = h('button', { class: 'btn primary', type: 'button', text: '发送测试短信' });
    btn.addEventListener('click', () => withSaving(btn, run));

    async function run() {
      clear(resultBox);
      if (!tplIn.value.trim() || !mobileIn.value.trim()) {
        toast('error', '模板码与手机号必填'); return;
      }
      const params = paramsIn.value.split('\n').map((s) => s.trim()).filter(Boolean);
      try {
        const r = await api.post(`/api/channels/${ch.id}/test`, {
          sms_type: typeSel.value, template_code: tplIn.value.trim(),
          country_code: ccIn.value.trim(), mobile_number: mobileIn.value.trim(), params,
        });
        renderResult(r);
      } catch (err) {
        if (err.status === 409) toast('error', err.message);
        else toast('error', err.message, err.field || '');
      }
    }
    function renderResult(r) {
      clear(resultBox);
      const ok = !!r.success;
      const pending = !!r.pending && !ok;
      const [headLabel, variant] = ok ? ['厂商受理成功', 'success']
        : pending ? ['结果不确定，已转补发队列', 'warn'] : ['厂商返回失败', 'danger'];
      resultBox.append(h('div', { style: 'margin-top:12px' },
        h('div', { class: 'actions', style: 'margin-bottom:8px' },
          badge(headLabel, variant),
          h('span', { class: 'card-hint', text: `HTTP ${r.http_code}` })),
        h('dl', { class: 'kv-grid' },
          h('dt', { text: 'appSmsId' }), h('dd', { class: 'mono', text: r.app_sms_id || '—' }),
          h('dt', { text: '厂商消息 ID' }), h('dd', { class: 'mono', text: r.provider_msg_id || '—' }),
          h('dt', { text: '错误分类' }), h('dd', { text: r.error_kind ? (ERROR_LABEL[r.error_kind] || r.error_kind) : '—' }),
          h('dt', { text: '描述' }), h('dd', { text: r.message || '—' }))));
      if (ok) toast('success', '测试发送成功');
      else if (pending) toast('warn', '结果不确定：记录保存在途，将由补发任务自动重试');
      else toast('warn', '测试发送完成但厂商返回失败');
    }

    return h('div', { class: 'card' },
      h('div', { class: 'card-head' },
        h('h3', { class: 'card-title', text: '测试发送' }),
        h('span', { class: 'card-hint warn', text: '将产生真实短信与费用，请仅使用测试手机号' })),
      h('div', { class: 'card-body' },
        h('div', { class: 'form-grid' },
          fld('短信场景', typeSel), fld('模板码', tplIn, true),
          fld('国家码', ccIn), fld('手机号（不含国家码）', mobileIn, true),
          h('div', { class: 'field span-2' }, h('label', { text: '模板参数（每行一个）' }), paramsIn)),
        h('div', { class: 'actions', style: 'margin-top:12px' }, btn),
        resultBox));
  }

  // ---------- 新建通道 ----------
  function openCreate() {
    let mode = 'http_json_v1';
    const nameIn = h('input', { class: 'input', placeholder: '如：示例短信平台-生产' });
    const descIn = h('input', { class: 'input', placeholder: '可选' });
    const baseIn = h('input', { class: 'input', value: 'https://sms.example.com:1443', placeholder: 'https://主机:端口' });
    const urlIn = h('input', { class: 'input', value: '${base}/sms/send', placeholder: '${base}/path 或完整 URL' });
    const errSlot = h('span', { class: 'field-error' });

    const cardPreset = choiceCard('http_json_v1', 'HTTP JSON 示例预置', '内置 9 行字段映射与 SHA-1 加盐签名，创建后补填基址/appCode/appSecret 即可。');
    const cardBlank = choiceCard('blank', '空白 HTTP 通道', '从零配置请求、常量、映射、签名与回执，适合对接其他厂商。');
    const blankFields = h('div', { class: 'form-grid', style: 'margin-top:14px', hidden: true },
      fld('基址 Base URL', baseIn), fld('请求路径 URL', urlIn, true));
    function highlight() {
      cardPreset.classList.toggle('is-selected', mode === 'http_json_v1');
      cardBlank.classList.toggle('is-selected', mode === 'blank');
      blankFields.hidden = mode !== 'blank';
    }
    cardPreset.addEventListener('click', () => { mode = 'http_json_v1'; highlight(); });
    cardBlank.addEventListener('click', () => { mode = 'blank'; highlight(); });
    highlight();

    const okBtn = h('button', { class: 'btn primary', text: '创建' });
    const cancelBtn = h('button', { class: 'btn', text: '取消' });
    const modal = h('div', { class: 'modal-mask' },
      h('div', { class: 'modal', role: 'dialog', 'aria-modal': 'true', 'aria-label': '新建通道' },
        h('div', { class: 'modal-head' }, h('h3', { class: 'modal-title', text: '新建短信通道' })),
        h('div', { class: 'modal-body' },
          h('div', { class: 'choice-grid' }, cardPreset, cardBlank),
          h('div', { class: 'form-grid', style: 'margin-top:14px' },
            fld('通道名称', nameIn, true), fld('描述', descIn)),
          blankFields, errSlot),
        h('div', { class: 'modal-foot' }, cancelBtn, okBtn)));

    const root2 = document.getElementById('modal-root');
    const onKey = (e) => { if (e.key === 'Escape') close(); };
    const close = () => {
      root2.hidden = true;
      clear(root2);
      document.removeEventListener('keydown', onKey);
    };
    cancelBtn.addEventListener('click', close);
    modal.addEventListener('click', (e) => { if (e.target === modal) close(); });
    document.addEventListener('keydown', onKey);
    okBtn.addEventListener('click', () => withSaving(okBtn, submit));
    clear(root2);
    root2.append(modal);
    root2.hidden = false;
    nameIn.focus();

    async function submit() {
      errSlot.textContent = '';
      const name = nameIn.value.trim();
      if (!name) { errSlot.textContent = '通道名称不能为空'; return; }
      try {
        let created;
        if (mode === 'http_json_v1') {
          created = await api.post('/api/channels/from-preset',
            { preset: 'http_json_v1', name, description: descIn.value.trim() });
        } else {
          if (!urlIn.value.trim()) { errSlot.textContent = '请求路径 URL 不能为空'; return; }
          created = await api.post('/api/channels',
            { name, description: descIn.value.trim(), config: minimalConfig(baseIn.value.trim(), urlIn.value.trim()) });
        }
        close();
        toast('success', '通道已创建，请补全凭证与模板参数');
        state.selected = created.id;
        await refresh();
      } catch (err) {
        errSlot.textContent = err.message;
        toast('error', err.message, err.field || '');
      }
    }
  }

  function choiceCard(key, title, sub) {
    return h('button', { type: 'button', class: 'choice-card', dataset: { mode: key } },
      h('b', { text: title }), h('span', { text: sub }));
  }
}

function fld(labelText, control, required) {
  return h('div', { class: 'field' },
    h('label', null, labelText, required ? h('span', { class: 'req', text: '*' }) : null), control);
}

function minimalConfig(base, url) {
  return {
    request: { base_url: base, url, method: 'POST', content_type: 'application/json', headers: {} },
    constants: {},
    body_mappings: [],
    sign: { strategy: 'none', encoding: 'hex', secret_const: '', segments: [] },
    response: { success_path: '', success_value: '', msg_id_path: '', message_path: '' },
    mobile_policy: 'cc_prefix',
    receipt: {
      msg_id_path: '', app_msg_id_path: '', status_path: '', delivered_value: '',
      failure_value: '', message_path: '', seq_no_path: '', success_body: '',
    },
  };
}
