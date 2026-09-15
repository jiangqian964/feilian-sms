// 视图 4：发送记录——筛选栏、分页表格、右侧详情抽屉（敏感值均为后端脱敏形态）。
import { api } from '../api.js';
import { h, clear, skeletonRows, emptyState, fmtDateTime, fmtDuration, localInputValue, localInputMS } from '../dom.js';
import { toast, badge } from '../ui.js';

const STATUS_LABEL = { pending: '在途', success: '成功', failed: '失败' };
const SOURCE_LABEL = { feilian: '飞连事件', test: '测试发送' };
// 回执状态由后端按厂商报文归一化后持久化（delivered/delivery_failed），非厂商原值。
const DELIVERY_LABEL = { delivered: '已送达', delivery_failed: '送达失败' };
const ERROR_LABEL = {
  unbound: '场景未绑定', binding_disabled: '绑定已停用', channel_disabled: '通道已停用',
  channel_not_found: '通道不存在', invalid_mobile: '手机号非法', render: '配置渲染失败',
  vendor: '厂商业务失败', network: '网络错误', timeout: '下游超时', internal: '内部错误',
};

export async function mountRecords(root) {
  root.append(h('div', { class: 'page-head' },
    h('div', null,
      h('h2', { class: 'page-title', text: '发送记录' }),
      h('div', { class: 'page-sub', text: '全生命周期留痕：手机号与参数仅展示脱敏值；点击行查看厂商响应与回执明细。' }))));

  const ctx = { types: [], channels: [], limit: 50, offset: 0 };
  const [catalog, { channels }] = await Promise.all([
    api.get('/api/bindings').catch(() => ({ sms_types: [] })),
    api.get('/api/channels').catch(() => ({ channels: [] })),
  ]);
  ctx.types = catalog.sms_types || [];
  ctx.channels = channels;

  const filterBar = buildFilterBar(ctx, () => { ctx.offset = 0; return query(); });
  const tableCard = h('div');
  root.append(filterBar, tableCard);

  async function query() {
    await loadPage(ctx, tableCard, query);
  }
  ctx.refresh = query;
  await query();
  return query;
}

function buildFilterBar(ctx, onSearch) {
  const statusSel = h('select', { class: 'input', 'aria-label': '按状态筛选' },
    h('option', { value: '', text: '全部状态' }),
    ...Object.entries(STATUS_LABEL).map(([v, l]) => h('option', { value: v, text: l })));
  const typeSel = h('select', { class: 'input', 'aria-label': '按短信场景筛选' },
    h('option', { value: '', text: '全部场景' }),
    ...ctx.types.map((t) => h('option', { value: t, text: t })));
  const channelSel = h('select', { class: 'input', 'aria-label': '按通道筛选' },
    h('option', { value: '', text: '全部通道' }),
    ...ctx.channels.map((c) => h('option', { value: c.id, text: c.name })));
  const sourceSel = h('select', { class: 'input', 'aria-label': '按来源筛选' },
    h('option', { value: '', text: '全部来源' }),
    ...Object.entries(SOURCE_LABEL).map(([v, l]) => h('option', { value: v, text: l })));
  const fromIn = h('input', { class: 'input', type: 'datetime-local', 'aria-label': '起始时间' });
  const toIn = h('input', { class: 'input', type: 'datetime-local', 'aria-label': '结束时间' });

  const searchBtn = h('button', { class: 'btn primary', type: 'button', text: '查询' });
  const resetBtn = h('button', { class: 'btn', type: 'button', text: '重置' });

  searchBtn.addEventListener('click', onSearch);
  resetBtn.addEventListener('click', () => {
    [statusSel, typeSel, channelSel, sourceSel].forEach((s) => { s.selectedIndex = 0; });
    fromIn.value = ''; toIn.value = '';
    onSearch();
  });

  ctx.readFilter = () => {
    const p = new URLSearchParams();
    const map = { status: statusSel.value, sms_type: typeSel.value, channel_id: channelSel.value, source: sourceSel.value };
    for (const [k, v] of Object.entries(map)) if (v) p.set(k, v);
    const fromMS = localInputMS(fromIn.value);
    const toMS = localInputMS(toIn.value);
    if (fromMS) p.set('from', String(fromMS));
    if (toMS) p.set('to', String(toMS));
    return p;
  };

  return h('div', { class: 'card' },
    h('div', { class: 'card-body' },
      h('div', { class: 'filter-bar' },
        h('div', { class: 'field' }, h('label', { text: '状态' }), statusSel),
        h('div', { class: 'field' }, h('label', { text: '场景' }), typeSel),
        h('div', { class: 'field' }, h('label', { text: '通道' }), channelSel),
        h('div', { class: 'field' }, h('label', { text: '来源' }), sourceSel),
        h('div', { class: 'field' }, h('label', { text: '起始时间' }), fromIn),
        h('div', { class: 'field' }, h('label', { text: '结束时间' }), toIn),
        h('div', { class: 'actions' }, searchBtn, resetBtn))));
}

async function loadPage(ctx, container, requery) {
  clear(container);
  const skel = h('div', { class: 'card card-body' });
  skeletonRows(skel, 7);
  container.append(skel);

  const p = ctx.readFilter();
  p.set('limit', String(ctx.limit));
  p.set('offset', String(ctx.offset));
  let data;
  try {
    data = await api.get(`/api/records?${p.toString()}`);
  } catch (err) {
    clear(container);
    container.append(h('div', { class: 'card card-body' },
      h('div', { class: 'empty-state' },
        h('div', { class: 'es-title', text: '记录加载失败' }),
        h('div', { text: err.message }))));
    return;
  }

  clear(container);
  const table = h('table', { class: 'grid' });
  table.append(h('thead', null, h('tr', null,
    h('th', { text: '发送时间' }), h('th', { text: 'appSmsId' }), h('th', { text: '场景' }),
    h('th', { text: '手机号（脱敏）' }), h('th', { text: '模板码' }), h('th', { text: '状态' }),
    h('th', { text: '回执' }), h('th', { class: 'tnum', text: '耗时' }))));
  const tbody = h('tbody');

  for (const r of data.records) {
    const tr = h('tr', { class: 'clickable', tabindex: '0', role: 'button',
      'aria-label': `查看 ${r.app_sms_id} 详情`,
      on: { click: () => openDrawer(r, ctx.channels), keydown: (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); openDrawer(r, ctx.channels); } } } },
      h('td', { class: 'mono tnum', text: fmtDateTime(r.created_at) }),
      h('td', { class: 'mono', text: r.app_sms_id }),
      h('td', { class: 'mono', text: r.sms_type || '—' }),
      h('td', { class: 'mono', text: r.mobile_masked || '—' }),
      h('td', { class: 'mono', text: r.template_code || '—' }),
      h('td', null, statusBadge(r)),
      h('td', null, deliveryBadge(r)),
      h('td', { class: 'num', text: r.latency_ms ? fmtDuration(r.latency_ms) : '—' }));
    tbody.append(tr);
  }
  table.append(tbody);

  const from = data.total === 0 ? 0 : data.offset + 1;
  const to = Math.min(data.offset + data.records.length, data.total);
  const page = Math.floor(data.offset / data.limit) + 1;
  const pages = Math.max(1, Math.ceil(data.total / data.limit));
  const prev = h('button', { class: 'btn tiny', disabled: data.offset === 0, on: { click: () => { ctx.offset = Math.max(0, ctx.offset - ctx.limit); requery(); } } }, '上一页');
  const next = h('button', { class: 'btn tiny', disabled: data.offset + data.records.length >= data.total,
    on: { click: () => { ctx.offset += ctx.limit; requery(); } } }, '下一页');

  const card = h('div', { class: 'card' },
    data.records.length
      ? h('div', { class: 'table-wrap' }, table)
      : emptyState('没有符合条件的发送记录', '调整筛选条件，或先完成一次测试发送。'),
    h('div', { class: 'pager' },
      h('span', { class: 'pinfo tnum', text: `第 ${from}-${to} 条 / 共 ${data.total} 条 · 第 ${page}/${pages} 页` }),
      h('div', { class: 'actions' }, prev, next)));
  container.append(card);
}

function statusBadge(r) {
  const wrap = h('div', { class: 'actions', style: 'gap:5px' });
  const map = {
    pending: ['在途', 'warn'], success: ['成功', 'success'], failed: ['失败', 'danger'],
  };
  const [label, variant] = map[r.status] || [r.status, 'muted'];
  wrap.append(badge(label, variant));
  if (r.status === 'failed' && r.error_kind) {
    wrap.append(badge(ERROR_LABEL[r.error_kind] || r.error_kind, 'muted'));
  }
  return wrap;
}

function deliveryBadge(r) {
  if (!r.delivery_status) return h('span', { class: 'card-hint', text: '—' });
  if (r.delivery_status === 'delivered') return badge('已送达', 'success');
  if (r.delivery_status === 'delivery_failed') return badge('送达失败', 'danger');
  return badge(r.delivery_status, 'warn');
}

function kv(title, items) {
  const nodes = [h('div', { class: 'kv-title', text: title }), h('dl', { class: 'kv-grid' })];
  const dl = nodes[1];
  for (const [k, v] of items) {
    dl.append(h('dt', { text: k }), h('dd', { class: typeof v === 'string' && v.length > 24 ? 'mono' : '', text: v || '—' }));
  }
  return nodes;
}

function openDrawer(r, channels) {
  const chName = (channels.find((c) => c.id === r.channel_id) || {}).name || r.channel_id || '—';
  let params = r.params_masked || '[]';
  try { params = JSON.stringify(JSON.parse(params)); } catch { /* 保留原文 */ }

  const close = () => { mask.remove(); drawer.remove(); document.removeEventListener('keydown', onKey); };
  const onKey = (e) => { if (e.key === 'Escape') close(); };

  const head = h('div', { class: 'drawer-head' },
    h('div', null,
      h('h3', { class: 'drawer-title', text: '发送记录详情' }),
      h('div', { class: 'mono card-hint', style: 'margin-top:3px', text: r.app_sms_id })),
    h('button', { class: 'icon-btn', 'aria-label': '关闭详情', on: { click: close } }, '✕'));

  const body = h('div', { class: 'drawer-body' });
  body.append(
    ...kv('基本信息', [
      ['事件 ID', r.event_id], ['来源', SOURCE_LABEL[r.source] || r.source],
      ['通道', chName], ['短信场景', r.sms_type], ['模板码', r.template_code],
      ['手机号', r.mobile_masked], ['参数（脱敏）', params],
      ['发送时间', fmtDateTime(r.created_at)], ['更新时间', fmtDateTime(r.updated_at)],
    ]),
    ...kv('发送结果', [
      ['状态', STATUS_LABEL[r.status] || r.status],
      ['错误分类', r.error_kind ? (ERROR_LABEL[r.error_kind] || r.error_kind) : ''],
      ['耗时', r.latency_ms ? fmtDuration(r.latency_ms) : ''],
      ['厂商消息 ID', r.provider_msg_id],
      ['厂商状态码', r.provider_status],
      ['厂商描述', r.provider_message],
    ]),
    ...kv('厂商回执', [
      ['回执状态', DELIVERY_LABEL[r.delivery_status] || r.delivery_status],
      ['回执描述', r.delivery_message],
      ['回执序号', r.seq_no ? String(r.seq_no) : ''],
      ['回执时间', r.receipt_at ? fmtDateTime(r.receipt_at) : ''],
    ]));

  const drawer = h('div', { class: 'drawer', role: 'dialog', 'aria-modal': 'true', 'aria-label': '发送记录详情' }, head, body);
  const mask = h('div', { class: 'drawer-mask', on: { click: close } });
  document.body.append(mask, drawer);
  document.addEventListener('keydown', onKey);
}
