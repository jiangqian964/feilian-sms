// 通道表单共用的可增删排序行编辑器：请求头 KV、通道常量（含密钥勾选）。
import { h } from '../dom.js';

export function iconBtns(onUp, onDown, onDel, opts = {}) {
  const up = h('button', { type: 'button', class: 'icon-btn', title: '上移', 'aria-label': '上移',
    on: { click: onUp } }, '↑');
  const down = h('button', { type: 'button', class: 'icon-btn', title: '下移', 'aria-label': '下移',
    on: { click: onDown } }, '↓');
  const del = h('button', { type: 'button', class: 'icon-btn danger', title: '删除', 'aria-label': '删除',
    on: { click: onDel } }, '✕');
  return h('div', { class: 'er-controls', style: opts.style || '' }, up, down, del);
}

// 简单 KV 行（请求头）。items: [key, value][]
export function kvEditor(items, { keyPh = '名称', valuePh = '值（可用 ${const:name}）' } = {}) {
  const host = h('div', { class: 'row-editor' });

  function draw() {
    host.textContent = '';
    rows.forEach((row, i) => {
      const k = h('input', { class: 'input', value: row[0], placeholder: keyPh, 'aria-label': '键' });
      const v = h('input', { class: 'input', value: row[1], placeholder: valuePh, 'aria-label': '值' });
      k.addEventListener('input', () => { row[0] = k.value.trim(); });
      v.addEventListener('input', () => { row[1] = v.value; });
      const er = h('div', { class: 'er-row' },
        h('div', { style: 'grid-column: span 5' }, k),
        h('div', { style: 'grid-column: span 5' }, v),
        iconBtns(() => move(i, -1), () => move(i, 1), () => remove(i)));
      host.append(er);
    });
    if (!rows.length) host.append(h('div', { class: 'er-empty', text: '暂无行，点击下方按钮新增' }));
  }
  function move(i, d) {
    const j = i + d;
    if (j < 0 || j >= rows.length) return;
    [rows[i], rows[j]] = [rows[j], rows[i]];
    draw();
  }
  function remove(i) { rows.splice(i, 1); draw(); }

  const rows = items.map((x) => [...x]);
  draw();

  const add = h('button', { type: 'button', class: 'btn tiny', style: 'margin-top:8px',
    on: { click: () => { rows.push(['', '']); draw(); } } }, '＋ 新增一行');
  return {
    el: h('div', null, host, add),
    get() {
      const out = {};
      for (const [k, v] of rows) if (k) out[k] = v;
      return out;
    },
  };
}

// 常量行：name/value/secret；secret 行 value 为只写密码框，掩码由外部传入。
// items: [{name, value, secret, set, masked}[]
export function constEditor(items) {
  const host = h('div', { class: 'row-editor' });
  const rows = items.map((x) => ({ ...x, input: '' }));

  function draw() {
    host.textContent = '';
    rows.forEach((row, i) => {
      const nameIn = h('input', { class: 'input', value: row.name, placeholder: '常量名（如 appCode）',
        'aria-label': '常量名' });
      nameIn.addEventListener('input', () => { row.name = nameIn.value.trim(); });

      const secretCb = h('input', { type: 'checkbox', checked: row.secret, 'aria-label': '标记为密钥' });
      let valCell;
      function buildValue() {
        valCell = h('div', { style: 'grid-column: span 5' });
        if (row.secret) {
          const pw = h('input', { class: 'input', type: 'password', autocomplete: 'new-password',
            placeholder: row.set ? `已设置 ${row.masked || '****'}，留空不修改` : '密钥值（仅密文存储）',
            'aria-label': '密钥值' });
          pw.addEventListener('input', () => { row.input = pw.value; });
          const hint = h('span', { class: `badge ${row.set ? 'success' : 'muted'}`,
            text: row.set ? `已设置 ${row.masked || '****'}` : '未设置' });
          valCell.append(h('div', { style: 'display:flex;gap:6px;align-items:center' }, pw, hint));
        } else {
          const v = h('input', { class: 'input', value: row.value, placeholder: '常量明文值', 'aria-label': '常量值' });
          v.addEventListener('input', () => { row.value = v.value; });
          valCell.append(v);
        }
      }
      buildValue();

      const er = h('div', { class: 'er-row' },
        h('div', { style: 'grid-column: span 3' }, nameIn),
        h('div', { style: 'grid-column: span 2; display:flex;align-items:center;gap:6px' },
          h('label', { class: 'inline-check' }, secretCb, '密钥')),
        valCell,
        iconBtns(() => move(i, -1), () => move(i, 1), () => remove(i)));
      let oldVal = valCell;
      // 勾选/取消密钥：切换值单元格（密码只写 / 明文），并清理另一侧的暂存值。
      secretCb.addEventListener('change', () => {
        row.secret = secretCb.checked;
        if (row.secret) row.value = ''; else row.input = '';
        buildValue();
        er.replaceChild(valCell, oldVal);
        oldVal = valCell;
      });
      host.append(er);
    });
    if (!rows.length) host.append(h('div', { class: 'er-empty', text: '暂无常量，点击下方按钮新增' }));
  }
  function move(i, d) {
    const j = i + d;
    if (j < 0 || j >= rows.length) return;
    [rows[i], rows[j]] = [rows[j], rows[i]];
    draw();
  }
  function remove(i) { rows.splice(i, 1); draw(); }

  draw();
  const add = h('button', { type: 'button', class: 'btn tiny', style: 'margin-top:8px',
    on: { click: () => { rows.push({ name: '', value: '', secret: false, set: false, masked: '', input: '' }); draw(); } },
  }, '＋ 新增常量');

  return {
    el: h('div', null, host, add),
    rows,
    // config.constants（密钥只声明）
    constants() {
      const out = {};
      for (const r of rows) {
        if (!r.name) continue;
        out[r.name] = r.secret ? { secret: true } : { value: r.value, secret: false };
      }
      return out;
    },
    // 本次提交的非空密钥（空=不修改）
    secrets() {
      const out = {};
      for (const r of rows) if (r.secret && r.name && r.input) out[r.name] = r.input;
      return out;
    },
    names(withSecret) {
      return rows.filter((r) => r.name && (withSecret === undefined || r.secret === withSecret))
        .map((r) => r.name);
    },
  };
}
