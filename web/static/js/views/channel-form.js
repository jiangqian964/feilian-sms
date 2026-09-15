// 通道详情表单：请求/常量 KV/字段映射表/签名构造器/响应判定/号码策略/回执映射。
// collect() 输出与 POST/PUT /api/channels 一致的 {name, description, config, secrets}。
import { h } from '../dom.js';
import { kvEditor, constEditor, iconBtns } from './roweditor.js';

const VARS = [
  ['appSmsId', 'appSmsId（我方唯一 ID）'],
  ['mobile', 'mobile（归一化后的手机号）'],
  ['mobileNumber', 'mobileNumber（不含国家码）'],
  ['countryCode', 'countryCode（国家码，含 +）'],
  ['smsType', 'smsType（短信场景）'],
  ['templateCode', 'templateCode（模板码）'],
  ['nonce', 'nonce（5-6 位随机数）'],
  ['timestamp', 'timestamp（毫秒时间戳）'],
  ['params', 'params（参数数组，JSON）'],
  ['param0', 'param0（第 1 个模板参数）'],
  ['param1', 'param1（第 2 个模板参数）'],
  ['param2', 'param2（第 3 个模板参数）'],
  ['sign', 'sign（已生成的签名，仅供请求体映射）'],
];
const STRATEGIES = [['none', '不签名'], ['sha1_salt', 'SHA-1 加盐'], ['hmac_sha256', 'HMAC-SHA256']];
const SOURCE_TYPES = [['variable', '内置变量'], ['const', '通道常量'], ['literal', '字面值']];
const VALUE_TYPES = [['string', '字符串 string'], ['number', '数字 number'], ['boolean', '布尔 boolean'], ['raw', '原始 JSON raw']];
const POLICIES = [['cc_prefix', '国家码前缀（如 86+手机号）'], ['raw', '原样使用（含 +）'], ['strip_plus', '去掉开头的 +']];
const SEGMENT_KINDS = [['literal', '字面量'], ['variable', '变量']];

export function renderChannelForm(ch) {
  const cfg = ch.config || {};
  const req = cfg.request || {};

  const nameIn = inp(ch.name || '', '如：示例短信平台-生产');
  const descIn = inp(ch.description || '', '可选备注');
  const baseIn = inp(req.base_url || '', '如 https://sms.example.com:1443');
  const urlIn = inp(req.url || '', '如 ${base}/sms/send，也可写完整 URL');
  const methodSel = sel(['POST', 'GET', 'PUT'], req.method || 'POST');
  const ctIn = inp(req.content_type || 'application/json', 'application/json');

  const headerPairs = Object.entries(req.headers || {}).sort((a, b) => a[0].localeCompare(b[0]));
  const headers = kvEditor(headerPairs);

  const constItems = Object.entries(cfg.constants || {})
    .sort((a, b) => a[0].localeCompare(b[0]))
    .map(([name, spec]) => ({
      name, value: spec.value || '', secret: !!spec.secret,
      set: !!(ch.secrets_masked[name] && ch.secrets_masked[name].set),
      masked: ch.secrets_masked[name] ? ch.secrets_masked[name].value : '',
    }));
  const consts = constEditor(constItems);

  const mappings = listEditor((cfg.body_mappings || []).map((m) => ({ ...m })), () => consts.names());
  const segments = segEditor(((cfg.sign && cfg.sign.segments) || []).map((s) => ({ ...s })));

  const signStrategy = sel(STRATEGIES, (cfg.sign && cfg.sign.strategy) || 'none');
  const signEncoding = sel([['hex', 'hex（小写）'], ['base64', 'base64']], (cfg.sign && cfg.sign.encoding) || 'hex');
  const secretConst = inp((cfg.sign && cfg.sign.secret_const) || '', '选择签名密钥常量名');
  attachConstDatalist(secretConst, consts, true);

  const resp = cfg.response || {};
  const sucPath = inp(resp.success_path || '', 'status');
  const sucVal = inp(resp.success_value || '', '0');
  const msgIDPath = inp(resp.msg_id_path || '', 'data.id');
  const msgPath = inp(resp.message_path || '', 'message');

  const policySel = sel(POLICIES, cfg.mobile_policy || 'cc_prefix');

  const rc = cfg.receipt || {};
  const rMsgID = inp(rc.msg_id_path || '', 'smsId');
  const rAppMsgID = inp(rc.app_msg_id_path || '', 'appSmsId');
  const rStatus = inp(rc.status_path || '', 'status');
  const rDelivered = inp(rc.delivered_value || '', 'DELIVRD');
  const rMsgPath = inp(rc.message_path || '', 'statusMessage');
  const rSeq = inp(rc.seq_no_path || '', 'seqNo');
  const rBody = inp(rc.success_body || '', '留空=默认 {"status":0,"message":"success"}');

  const el = h('div', null,
    section('基本信息', null, grid2(
      fld('通道名称', nameIn, true), fld('描述', descIn))),
    section('出站请求', 'URL 支持 ${base} 占位；请求头值支持 ${const:name} 插值。', grid2(
      fld('基址 Base URL', baseIn), fld('请求路径 URL', urlIn, true),
      fld('HTTP 方法', methodSel), fld('Content-Type', ctIn)),
      fld('额外请求头', headers.el)),
    section('通道常量', '非密钥常量明文内联；勾选「密钥」后真实值仅经密文存储，此处留空表示不修改。',
      consts.el),
    section('请求体字段映射表', '禁止手写 JSON：逐行声明 源 → 目标 JSON 路径；保存时会用样例输入试渲染校验。',
      mappings.el),
    section('签名构造器', '按片段顺序拼接待签名原文；策略为 none 时不签名。', grid2(
      fld('签名策略', signStrategy), fld('HMAC 编码', signEncoding),
      fld('签名密钥常量（Constants 中的密钥名）', secretConst)),
      segments.el),
    section('响应判定', '点分路径宽松比较（数字 0 与字符串 "0" 等价）。', grid2(
      fld('成功标志路径', sucPath), fld('成功期望值', sucVal),
      fld('厂商消息 ID 路径', msgIDPath), fld('错误描述路径', msgPath))),
    section('手机号归一化', null, fld('策略', policySel)),
    section('厂商异步回执映射', '网关对回执默认固定回 {status:0,message:success}；success_body 可自定义原文。', grid2(
      fld('厂商短信 ID 字段', rMsgID), fld('我方 appSmsId 字段', rAppMsgID),
      fld('回执状态字段', rStatus), fld('送达成功值', rDelivered),
      fld('回执描述字段', rMsgPath), fld('回执序号字段', rSeq),
      fld('成功响应原文', rBody))));

  function collect() {
    const name = nameIn.value.trim();
    if (!name) throw new FormError('name', '通道名称不能为空');
    if (!urlIn.value.trim()) throw new FormError('config.request.url', '出站请求 URL 不能为空');
    const maps = mappings.get();
    for (let i = 0; i < maps.length; i++) {
      if (!maps[i].target || !maps[i].source) {
        throw new FormError('config.body_mappings', `第 ${i + 1} 行映射的目标路径与来源均不能为空`);
      }
    }
    const segs = segments.get();
    for (let i = 0; i < segs.length; i++) {
      if (!segs[i].value) throw new FormError('config.sign', `第 ${i + 1} 个签名片段内容为空`);
    }
    return {
      name,
      description: descIn.value.trim(),
      secrets: consts.secrets(),
      config: {
        request: {
          base_url: baseIn.value.trim(), url: urlIn.value.trim(),
          method: methodSel.value, content_type: ctIn.value.trim(),
          headers: headers.get(),
        },
        constants: consts.constants(),
        body_mappings: maps,
        sign: {
          strategy: signStrategy.value, encoding: signEncoding.value,
          secret_const: secretConst.value.trim(), segments: segs,
        },
        response: {
          success_path: sucPath.value.trim(), success_value: sucVal.value.trim(),
          msg_id_path: msgIDPath.value.trim(), message_path: msgPath.value.trim(),
        },
        mobile_policy: policySel.value,
        receipt: {
          msg_id_path: rMsgID.value.trim(), app_msg_id_path: rAppMsgID.value.trim(),
          status_path: rStatus.value.trim(), delivered_value: rDelivered.value.trim(),
          message_path: rMsgPath.value.trim(), seq_no_path: rSeq.value.trim(),
          success_body: rBody.value,
        },
      },
    };
  }
  return { el, collect };
}

export class FormError extends Error {
  constructor(field, message) { super(message); this.field = field; }
}

// ---------- 小部件 ----------
function inp(value, ph) {
  return h('input', { class: 'input', value: value || '', placeholder: ph || '' });
}
function sel(options, value) {
  const s = h('select', { class: 'input' });
  for (const item of options) {
    const v = Array.isArray(item) ? item[0] : item;
    const label = Array.isArray(item) ? (item[1] || item[0]) : item;
    const o = h('option', { value: v, text: label });
    if (v === value) o.selected = true;
    s.append(o);
  }
  return s;
}
function fld(labelText, control, required) {
  return h('div', { class: 'field' },
    h('label', null, labelText, required ? h('span', { class: 'req', text: '*' }) : null), control);
}
function grid2(...children) { return h('div', { class: 'form-grid' }, ...children); }
function section(title, hint, ...children) {
  return h('div', { class: 'card' },
    h('div', { class: 'card-head' },
      h('h3', { class: 'card-title', text: title }),
      hint ? h('span', { class: 'card-hint', text: hint }) : null),
    h('div', { class: 'card-body' }, ...children));
}

// 常量名下拉建议（datalist），聚焦时按当前行实时重建。
function attachConstDatalist(input, consts, secretOnly) {
  const listId = `dl-${Math.random().toString(36).slice(2, 9)}`;
  const dl = h('datalist', { id: listId });
  input.setAttribute('list', listId);
  input.insertAdjacentElement('afterend', dl);
  input.addEventListener('focus', () => {
    dl.textContent = '';
    for (const n of consts.names(secretOnly ? true : undefined)) {
      dl.append(h('option', { value: n }));
    }
  });
}

// 字段映射表行编辑器。
function listEditor(initial, constNames) {
  const rows = initial;
  const host = h('div', { class: 'row-editor' });

  function draw() {
    host.textContent = '';
    if (!rows.length) host.append(h('div', { class: 'er-empty', text: '暂无映射行' }));
    rows.forEach((row, i) => {
      const target = h('input', { class: 'input', value: row.target || '', placeholder: '目标路径，如 timestamp',
        'aria-label': `第 ${i + 1} 行目标路径` });
      target.addEventListener('input', () => { row.target = target.value.trim(); });
      const st = sel(SOURCE_TYPES, row.source_type || 'variable');
      st.setAttribute('aria-label', `第 ${i + 1} 行来源类型`);
      const srcHost = h('div', { style: 'grid-column: span 3' });
      function drawSrc() {
        srcHost.textContent = '';
        srcHost.append(sourceWidget(row, st.value, constNames));
      }
      st.addEventListener('change', () => {
        // 切回内置变量时，旧的常量名/字面值不属于变量目录，回落默认值，避免提交脏数据。
        if (st.value === 'variable' && !VARS.some(([v]) => v === row.source)) row.source = 'appSmsId';
        drawSrc();
      });
      drawSrc();
      const vt = sel(VALUE_TYPES, row.value_type || 'string');
      vt.addEventListener('change', () => { row.value_type = vt.value; });
      const er = h('div', { class: 'er-row' },
        h('div', { style: 'grid-column: span 3' }, target),
        h('div', { style: 'grid-column: span 2' }, st),
        srcHost,
        h('div', { style: 'grid-column: span 2' }, vt),
        iconBtns(() => move(i, -1), () => move(i, 1), () => remove(i)));
      host.append(er);
    });
  }
  function move(i, d) { const j = i + d; if (j < 0 || j >= rows.length) return; [rows[i], rows[j]] = [rows[j], rows[i]]; draw(); }
  function remove(i) { rows.splice(i, 1); draw(); }
  draw();
  const add = h('button', { type: 'button', class: 'btn tiny', style: 'margin-top:8px',
    on: { click: () => { rows.push({ target: '', source_type: 'variable', source: '', value_type: 'string' }); draw(); } } },
    '＋ 新增映射');
  return { el: h('div', null, host, add), get: () => rows };
}

function sourceWidget(row, type, constNames) {
  if (type === 'variable') {
    const s = sel(VARS.map(([v, l]) => [v, l]), row.source || 'appSmsId');
    s.addEventListener('change', () => { row.source = s.value; });
    row.source = row.source || 'appSmsId';
    return s;
  }
  if (type === 'const') {
    const listId = `dl-src-${Math.random().toString(36).slice(2, 9)}`;
    const input = h('input', { class: 'input', value: row.source || '', placeholder: '常量名（可输入或选择）',
      'aria-label': '常量名' });
    input.setAttribute('list', listId);
    const dl = h('datalist', { id: listId });
    input.addEventListener('input', () => { row.source = input.value.trim(); });
    input.addEventListener('focus', () => {
      dl.textContent = '';
      for (const n of (constNames ? constNames() : [])) dl.append(h('option', { value: n }));
    });
    const wrap = h('span', { style: 'position:relative;display:block' }, input, dl);
    return wrap;
  }
  const input = h('input', { class: 'input', value: row.source || '', placeholder: '字面值', 'aria-label': '字面值' });
  input.addEventListener('input', () => { row.source = input.value; });
  return input;
}

// 签名片段行编辑器。
function segEditor(initial) {
  const rows = initial;
  const host = h('div', { class: 'row-editor' });
  const varsWithoutSign = VARS.filter(([v]) => v !== 'sign');

  function draw() {
    host.textContent = '';
    if (!rows.length) host.append(h('div', { class: 'er-empty', text: '暂无签名片段（不签名策略可留空）' }));
    rows.forEach((row, i) => {
      const kind = sel(SEGMENT_KINDS, row.kind || 'literal');
      const valHost = h('div', { style: 'grid-column: span 7' });
      function drawVal() {
        valHost.textContent = '';
        if (kind.value === 'variable') {
          const s = sel(varsWithoutSign, row.value || 'timestamp');
          s.addEventListener('change', () => { row.value = s.value; });
          if (!row.value) row.value = 'timestamp';
          valHost.append(s);
        } else {
          const t = h('input', { class: 'input', value: row.value || '', placeholder: '字面量，如 timestamp=', 'aria-label': '字面量片段' });
          t.addEventListener('input', () => { row.value = t.value; });
          valHost.append(t);
        }
      }
      kind.addEventListener('change', () => {
        row.kind = kind.value;
        if (kind.value === 'variable') {
          if (!varsWithoutSign.some(([v]) => v === row.value)) row.value = 'timestamp';
        } else if (varsWithoutSign.some(([v]) => v === row.value)) {
          row.value = ''; // 从变量切到字面量时清空，避免把变量名误当字面文本提交
        }
        drawVal();
      });
      drawVal();
      const er = h('div', { class: 'er-row' },
        h('div', { style: 'grid-column: span 3' }, kind), valHost,
        iconBtns(() => move(i, -1), () => move(i, 1), () => remove(i)));
      host.append(er);
    });
  }
  function move(i, d) { const j = i + d; if (j < 0 || j >= rows.length) return; [rows[i], rows[j]] = [rows[j], rows[i]]; draw(); }
  function remove(i) { rows.splice(i, 1); draw(); }
  draw();
  const add = h('button', { type: 'button', class: 'btn tiny', style: 'margin-top:8px',
    on: { click: () => { rows.push({ kind: 'literal', value: '' }); draw(); } } }, '＋ 新增片段');
  return { el: h('div', null, host, add), get: () => rows };
}
