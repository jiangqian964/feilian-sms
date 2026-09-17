// 视图 3：场景绑定——9 种 sms_type 行内表格（启用胶囊/通道/模板码/参数下标）。
import { api } from '../api.js';
import { h, clear, skeletonRows } from '../dom.js';
import { toast, switchEl, withSaving } from '../ui.js';

const TYPE_LABELS = {
  code: '登录验证码',
  init_password: '初始密码',
  reset_password: '重置密码',
  alert: '安全告警',
  guest_wifi: '访客 Wi-Fi',
  password_expiration: '密码过期提醒',
  accout_expire: '账号到期提醒',
  wifi_info: 'Wi-Fi 信息通知',
  exchange_mfa: 'Exchange 多因素认证',
};

export async function mountBindings(root) {
  root.append(h('div', { class: 'page-head' },
    h('div', null,
      h('h2', { class: 'page-title', text: '场景绑定' }),
      h('div', { class: 'page-sub', text: '决定每种飞连短信场景经由哪个通道、使用哪个模板码下发。停用后该场景事件只留痕不转发。' }))));

  const body = h('div');
  root.append(body);
  await load(body);
  return async () => { await load(body); };
}

async function load(root) {
  clear(root);
  skeletonRows(root, 8);
  const [{ sms_types: types, bindings }, { channels }] = await Promise.all([
    api.get('/api/bindings'),
    api.get('/api/channels'),
  ]);
  clear(root);

  if (!channels.length) {
    root.append(h('div', { class: 'card card-body' },
      h('div', { class: 'empty-state' },
        h('div', { class: 'es-title', text: '还没有可用通道' }),
        h('div', { text: '请先到「通道」视图通过内置示例预置创建通道并补全凭证。' }))));
    return;
  }

  const bound = new Map(bindings.map((b) => [b.sms_type, b]));
  const rows = new Map(); // sms_type -> {enabled, channel, template, paramIndex}

  const table = h('table', { class: 'grid' });
  const thead = h('thead', null, h('tr', null,
    h('th', { style: 'width:44px', text: '启用' }),
    h('th', { text: '短信场景' }),
    h('th', { style: 'width:200px', text: '下发通道' }),
    h('th', { style: 'width:170px', text: '模板码 templateCode' }),
    h('th', { style: 'width:150px', text: '参数下标' })));
  const tbody = h('tbody');

  for (const t of types) {
    const cur = bound.get(t);
    const channelSel = h('select', { class: 'input', 'aria-label': `${TYPE_LABELS[t]} 通道` },
      h('option', { value: '', text: '— 未绑定 —' }),
      ...channels.map((c) => {
        const o = h('option', { value: c.id, text: c.name });
        if (cur && cur.channel_id === c.id) o.selected = true;
        return o;
      }));
    const tplIn = h('input', { class: 'input', value: cur ? cur.template_code : '',
      placeholder: '如 SMS_0001', 'aria-label': `${TYPE_LABELS[t]} 模板码` });
    const idxIn = h('input', { class: 'input tnum', value: cur && cur.param_index ? cur.param_index.join(',') : '',
      placeholder: '如 1,0', 'aria-label': `${TYPE_LABELS[t]} 参数下标` });

    const state = {
      enabled: !!(cur && cur.enabled),
      channel: cur ? cur.channel_id : '',
      template: cur ? cur.template_code : '',
      param: cur && cur.param_index ? cur.param_index.join(',') : '',
    };
    rows.set(t, state);

    const sw = switchEl(state.enabled, `启用 ${TYPE_LABELS[t]}`, async (next) => { state.enabled = next; });
    channelSel.addEventListener('change', () => { state.channel = channelSel.value; });
    tplIn.addEventListener('input', () => { state.template = tplIn.value.trim(); });
    idxIn.addEventListener('input', () => { state.param = idxIn.value.trim(); });

    tbody.append(h('tr', null,
      h('td', null, h('div', { style: 'display:flex;justify-content:center' }, sw)),
      h('td', null,
        h('div', { style: 'font-weight:600', text: TYPE_LABELS[t] || t }),
        h('div', { class: 'mono card-hint', text: t })),
      h('td', null, channelSel),
      h('td', null, tplIn),
      h('td', null, idxIn)));
  }
  table.append(thead, tbody);

  const errSlot = h('span', { class: 'field-error' });
  const saveBtn = h('button', { class: 'btn primary', type: 'button', text: '保存绑定' });
  const card = h('div', { class: 'card' },
    h('div', { class: 'card-head' },
      h('h3', { class: 'card-title', text: '9 种短信场景' }),
      h('span', { class: 'card-hint', text: '整表 UPSERT：未改动的场景保持原样；停用请关闭胶囊而非清空通道。' })),
    h('div', { class: 'card-body tight' }, h('div', { class: 'table-wrap' }, table)),
    h('div', { class: 'pager' },
      h('span', { class: 'pinfo', text: '参数下标为飞连 params 数组的 0 基顺序，多个用英文逗号分隔；留空表示按原顺序透传。' }),
      h('div', { class: 'actions' }, errSlot, saveBtn)));
  root.append(card);

  saveBtn.addEventListener('click', () => withSaving(saveBtn, save));

  async function save() {
    errSlot.textContent = '';
    const payload = [];
    for (const t of types) {
      const r = rows.get(t);
      if (!r.channel) continue; // 未配置的场景不提交，服务端保留既有行
      if (!r.template) {
        errSlot.textContent = `「${TYPE_LABELS[t]}」已选择通道但模板码为空`;
        toast('error', '保存失败：模板码不能为空', 'bindings');
        await rollback();
        return;
      }
      let paramIndex = [];
      if (r.param) {
        const parts = r.param.split(/[,，\s]+/).filter(Boolean);
        for (const p of parts) {
          const n = Number(p);
          if (!Number.isInteger(n) || n < 0) {
            errSlot.textContent = `「${TYPE_LABELS[t]}」参数下标存在非法值：${p}`;
            toast('error', '参数下标必须为非负整数', 'bindings');
            await rollback();
            return;
          }
          paramIndex.push(n);
        }
      }
      payload.push({
        sms_type: t, channel_id: r.channel, template_code: r.template,
        param_index: paramIndex, enabled: r.enabled,
      });
    }
    try {
      await api.put('/api/bindings', { bindings: payload });
      toast('success', `场景绑定已保存（${payload.length} 条）`);
      await load(root);
    } catch (err) {
      toast('error', err.message, err.field || '');
      // 保存未生效：整表回滚为服务端权威状态，避免胶囊开关停留在乐观值。
      await rollback();
    }
  }

  // rollback 以服务端数据重渲染整表（开关为乐观更新，保存失败必须回滚）。
  async function rollback() {
    try {
      await load(root);
    } catch (err) {
      toast('error', `回滚绑定状态失败：${err.message}`);
    }
  }
}
