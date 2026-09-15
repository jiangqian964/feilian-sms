// 管理 API 的极简 fetch 封装：同源 JSON、204 空体、结构化错误体。
export class ApiError extends Error {
  constructor(status, body) {
    super((body && body.message) || `请求失败（HTTP ${status}）`);
    this.name = 'ApiError';
    this.status = status;
    this.code = (body && body.code) || '';
    this.field = (body && body.field) || '';
  }
}

async function request(method, path, body) {
  const opts = { method, headers: {}, credentials: 'same-origin' };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  if (res.status === 204) return null;
  const text = await res.text();
  let data = null;
  if (text) {
    try { data = JSON.parse(text); } catch { data = null; }
  }
  if (!res.ok) throw new ApiError(res.status, data);
  return data;
}

export const api = {
  get: (p) => request('GET', p),
  post: (p, b) => request('POST', p, b === undefined ? {} : b),
  put: (p, b) => request('PUT', p, b === undefined ? {} : b),
  del: (p) => request('DELETE', p),
};
