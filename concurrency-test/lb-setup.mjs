// lb-setup.mjs — 负载均衡测试拓扑搭建(LOADBALANCE-PLAN.md 步骤2)
// 幂等:token/channel 已存在则复用;创建后暂停旧渠道 1/2,并打一条探针请求。
// 用法: node concurrency-test/lb-setup.mjs
import fs from 'node:fs';
const { DatabaseSync } = await import('node:sqlite');

const BASE = 'http://127.0.0.1:3001';
const ADMIN = {
  username: process.env.ADMIN_USER || 'root',
  password: process.env.ADMIN_PASSWORD || '',
};
const MODEL = 'Qwen3.6-35B';
const LB_NAMES = ['lb-01', 'lb-02', 'lb-03', 'lb-04'];
const PAUSE_IDS = [1, 2]; // upstream-190 / _copy,测试期间暂停,报告后由 restore 段恢复

async function api(path, opts = {}, token) {
  const r = await fetch(`${BASE}${path}`, {
    ...opts,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(token ? { 'New-API-User': '1' } : {}),
      ...(opts.headers || {}),
    },
  });
  return r.json();
}

// 1. 登录
const login = await api('/api/user/login', { method: 'POST', body: JSON.stringify(ADMIN) });
const TOK = login.data?.access_token;
if (!TOK) throw new Error('login failed: ' + JSON.stringify(login).slice(0, 200));
console.log('login ok');

// 2. 网关 token(concurrency_limit=0,不触发网关闸)
const tokList = await api('/api/token/?p=1&size=100', {}, TOK);
let lbTok = tokList.data.items.find((t) => t.name === 'lb-test-token');
if (!lbTok) {
  const created = await api('/api/token/', {
    method: 'POST',
    body: JSON.stringify({
      name: 'lb-test-token', remain_quota: 50_000_000, unlimited_quota: false,
      expired_time: 0, model_limits_enabled: false, model_limits: '',
      allow_ips: '', group: '', cross_group_retry: false, status: 1,
      concurrency_limit: 0, concurrency_limit_by_model: '',
    }),
  }, TOK);
  if (!created.success) throw new Error('create token: ' + created.message);
  const after = await api('/api/token/?p=1&size=100', {}, TOK);
  lbTok = after.data.items.find((t) => t.name === 'lb-test-token');
  if (!lbTok) throw new Error('token created but not found on re-list');
  console.log('token created:', lbTok.id);
} else console.log('token exists:', lbTok.id);
// token 列表 API 也会掩码 key——从 SQLite 读真 key
const db0 = new DatabaseSync('one-api.db', { readOnly: true });
const KEY = db0.prepare('SELECT key FROM tokens WHERE id = ?').get(lbTok.id)?.key || '';
db0.close();
if (!KEY || KEY.includes('*')) throw new Error('token key missing/masked in DB');
fs.writeFileSync('logs/lb-token.txt', KEY);
console.log('gateway token key loaded: len=' + KEY.length);

// 3. 渠道 1 详情做克隆母本
const ch1 = (await api('/api/channel/1', {}, TOK)).data;
if (!ch1) throw new Error('channel 1 not found');

// 3b. 从 SQLite 只读取渠道 1 真实 key(API 会掩码为空)——克隆渠道用
const db = new DatabaseSync('one-api.db', { readOnly: true });
const row = db.prepare('SELECT key FROM channels WHERE id = 1').get();
db.close();
const UPKEY = row?.key || '';
if (!UPKEY) throw new Error('channel 1 key empty in DB');
console.log('upstream key loaded: len=' + UPKEY.length);

// 4. 编号渠道 lb-01..04
const chList = await api('/api/channel/?p=1&size=100', {}, TOK);
const lbIds = [];
for (const name of LB_NAMES) {
  let ch = chList.data.items.find((c) => c.name === name);
  if (!ch) {
    const created = await api('/api/channel/', {
      method: 'POST',
      body: JSON.stringify({
        mode: 'single', multi_key_mode: 'single',
        channel: {
          name, type: ch1.type, key: UPKEY, base_url: ch1.base_url,
          models: ch1.models, group: ch1.group, weight: 0, priority: 0, status: 1, other: '',
        },
      }),
    }, TOK);
    if (!created.success) throw new Error(`create ${name}: ` + created.message);
    const afterCh = await api('/api/channel/?p=1&size=100', {}, TOK);
    ch = afterCh.data.items.find((c) => c.name === name);
    if (!ch) throw new Error(`${name} created but not found on re-list`);
    console.log(`${name} created: id=${ch.id}`);
  } else console.log(`${name} exists: id=${ch.id}`);
  lbIds.push(ch.id);
}
fs.writeFileSync('logs/lb-channel-ids.txt', lbIds.join(','));

// 5. 暂停旧渠道 1/2(专用批量状态端点;status: 2=ManuallyDisabled,可管理值只有 1/2)
const st = await api('/api/channel/status/batch', {
  method: 'POST',
  body: JSON.stringify({ ids: PAUSE_IDS, status: 2 }),
}, TOK);
if (!st.success) throw new Error('pause: ' + st.message);
fs.writeFileSync('logs/lb-paused-orig.txt', '1,2'); // 恢复时置回 status=1
console.log('paused channels:', PAUSE_IDS.join(','), 'changed=', st.data);

// 6. 探针:单条补全
const t0 = Date.now();
const probe = await fetch(`${BASE}/v1/chat/completions`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${KEY}` },
  body: JSON.stringify({
    model: MODEL,
    messages: [{ role: 'user', content: 'Reply with exactly: OK' }],
    max_tokens: 8,
    stream: false,
  }),
});
const pj = await probe.json();
const dt = Date.now() - t0;
if (!probe.ok) throw new Error(`probe ${probe.status}: ` + JSON.stringify(pj).slice(0, 300));
console.log(`probe ok: ${dt}ms, channel_hint=`, pj.choices?.[0]?.message?.content?.slice(0, 20), '| model:', pj.model);
fs.writeFileSync('logs/lb-setup-done.txt', JSON.stringify({ lbIds, probeMs: dt }));
