// 入口：视图注册、tab 切换（懒挂载 + 再进入刷新）、XHR 静默健康检查。
import { $id, h } from './dom.js';
import { mountSettings } from './views/settings.js';
import { mountChannels } from './views/channels.js';
import { mountBindings } from './views/bindings.js';
import { mountRecords } from './views/records.js';

const views = {
  settings: { mount: mountSettings },
  channels: { mount: mountChannels },
  bindings: { mount: mountBindings },
  records: { mount: mountRecords },
};

const state = new Map(); // name -> { mounted, refresh }

async function activate(name) {
  for (const [key, def] of Object.entries(views)) {
    const panel = $id(`panel-${key}`);
    const tab = $id(`tab-${key}`);
    const active = key === name;
    panel.hidden = !active;
    tab.classList.toggle('is-active', active);
    tab.setAttribute('aria-selected', String(active));
  }

  let st = state.get(name);
  if (!st) {
    st = { mounted: false, refresh: null };
    state.set(name, st);
    try {
      const refresh = await mountView(name);
      st.mounted = true;
      st.refresh = typeof refresh === 'function' ? refresh : null;
    } catch (err) {
      panelFatal(name, err);
      return;
    }
  } else if (st.refresh) {
    try { await st.refresh(); } catch { /* 视图内部自行提示，避免打断切换 */ }
  }
}

async function mountView(name) {
  return views[name].mount($id(`panel-${name}`));
}

function panelFatal(name, err) {
  const panel = $id(`panel-${name}`);
  panel.innerHTML = '';
  panel.append(h('div', { class: 'card card-body', text: `视图加载失败：${err && err.message ? err.message : err}` }));
}

document.querySelectorAll('.tab').forEach((tab) => {
  tab.addEventListener('click', () => activate(tab.dataset.view));
});

// 静默健康检查：按需求使用 XMLHttpRequest，失败只更新角标，绝不弹打扰。
function pingHealth() {
  const dot = $id('health-dot');
  const txt = $id('health-text');
  const xhr = new XMLHttpRequest();
  xhr.open('GET', '/api/health', true);
  xhr.timeout = 3000;
  const mark = (ok, label) => {
    dot.classList.toggle('is-ok', ok);
    dot.classList.toggle('is-bad', !ok);
    dot.setAttribute('aria-label', label);
    txt.textContent = label;
  };
  xhr.onload = () => mark(xhr.status === 200, xhr.status === 200 ? '服务正常' : `异常 ${xhr.status}`);
  xhr.onerror = () => mark(false, '无法连接');
  xhr.ontimeout = () => mark(false, '检查超时');
  xhr.send();
}

pingHealth();
setInterval(pingHealth, 20000);
activate('settings');
