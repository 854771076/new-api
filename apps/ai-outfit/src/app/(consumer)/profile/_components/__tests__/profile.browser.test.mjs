// Run with: bun test 'src/app/(consumer)/profile/_components/__tests__/profile.browser.test.mjs' --timeout 20000
// PROFILE_PLAYWRIGHT_MODULE may point to a cached Playwright module without changing app dependencies.
import { after, before, test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';
import { compile } from '@tailwindcss/node';
import { renderToStaticMarkup } from 'react-dom/server';

const { chromium } = await import(process.env.PROFILE_PLAYWRIGHT_MODULE || 'playwright');

const componentRoot = fileURLToPath(new URL('../', import.meta.url)).replaceAll('\\', '/');
const appRoot = resolve(componentRoot, '../../../../..').replaceAll('\\', '/');
const user = { id: 'profile-fixture', email: 'profile@example.com', app_metadata: { provider: 'custom:newapi' }, user_metadata: { name: '测试用户' } };
const plan = { id: 1, title: 'App 月套餐', price_amount: 3, currency: 'USD', total_amount: 300000, duration_unit: 'month', duration_value: 1, allow_balance_pay: true, max_purchase_per_user: 2, quota_reset_period: 'monthly' };
const product = { id: 2, name: '积分包', description: '换装积分', price_amount: 2, currency: 'USD', quota_amount: 200000, max_purchase_per_user: 0 };
let browser;
let server;
let gatewaySuccess;
const characterRoot = appRoot + '/src/app/(consumer)/characters/_components/CharactersClient.tsx';
const studioRoot = appRoot + '/src/app/(consumer)/studio/_components/StudioClient.tsx';
const character = { id: 'character-1', name: '原人物', description: '原描述', is_public: false, created_at: '2026-11-10T00:00:00Z' };

// Keep production components/query builders; replace only framework and external IO boundaries.
async function loadServerFixture(relativePath, state = {}) {
  const calls = [];
  globalThis.materialFetch = async (url) => {
    calls.push(url);
    const table = url.pathname.split('/').at(-1);
    if (table === 'ai_models' && state.modelError) return Response.json({ message: 'database fixture unavailable' }, { status: 503 });
    const data = state.tables?.[table] ?? [];
    const total = state.counts?.[table] ?? data.length;
    return Response.json(data, { headers: { 'Content-Range': `0-${Math.max(0, data.length - 1)}/${total}` } });
  };
  const boundaries = {
    'server-only': '',
    '@/lib/config': "export const config={gatewayClientId:'app-fixture'};",
    '@/lib/supabase/server': `import {createClient} from '@supabase/supabase-js';
      const client=createClient('https://supabase-fixture.example','fixture-anon-key',{auth:{persistSession:false,autoRefreshToken:false},global:{fetch:(input)=>globalThis.materialFetch(new URL(input))}});
      export async function createServerSupabase(){return {from:client.from.bind(client),auth:{getUser:async()=>({data:{user:${JSON.stringify(user)}}})}};}`,
    '@/lib/media-urls': 'export async function getSignedUrls(){return {}}',
    'next/navigation': 'export function redirect(path){throw new Error(path)}',
    'next/link': 'export default function Link(props){return <a {...props}/>}',
    './_components/StudioClient': 'export function StudioClient(){return null}',
  };
  const build = await Bun.build({ entrypoints: [appRoot + '/' + relativePath], target: 'bun', format: 'esm',
    plugins: [{ name: 'material-server-boundaries', setup(builder) {
      builder.onResolve({ filter: /^react(?:\/.*)?$/ }, (args) => ({ path: fileURLToPath(import.meta.resolve(args.path)).replaceAll('\\', '/'), external: true }));
      builder.onResolve({ filter: /^(server-only|@\/lib\/(config|supabase\/server|media-urls)|next\/(navigation|link)|\.\/_components\/StudioClient)$/ }, (args) => ({ path: args.path, namespace: 'material-boundary' }));
      builder.onLoad({ filter: /.*/, namespace: 'material-boundary' }, (args) => ({ loader: 'tsx', resolveDir: appRoot, contents: boundaries[args.path] }));
    } }],
  });
  assert.equal(build.success, true, String(build.logs));
  const compiledModule = await import(`data:text/javascript;base64,${Buffer.from(await build.outputs[0].text()).toString('base64')}`);
  return { compiledModule, calls };
}

before(async () => {
  ({ compiledModule: { gatewaySuccess } } = await loadServerFixture('src/lib/gateway-bff.ts'));
  const build = await Bun.build({
    entrypoints: ['profile-test-entry'], target: 'browser', format: 'esm',
    plugins: [{ name: 'profile-test-entry', setup(builder) {
      builder.onResolve({ filter: /^profile-test-entry$/ }, () => ({ path: 'entry', namespace: 'profile-test' }));
      builder.onLoad({ filter: /.*/, namespace: 'profile-test' }, () => ({
        loader: 'tsx', resolveDir: appRoot,
        contents: `import {createRoot} from 'react-dom/client';
          import {ProfileClient} from ${JSON.stringify(componentRoot + 'ProfileClient.tsx')};
          import {CharactersClient} from ${JSON.stringify(characterRoot)};
          import {StudioClient} from ${JSON.stringify(studioRoot)};
          const root=createRoot(document.getElementById('root'));
          window.mountProfile=()=>root.render(<ProfileClient user={${JSON.stringify(user)}}/>);
          window.mountCharacters=(items)=>root.render(<CharactersClient initialItems={items}/>);
          window.mountStudio=(props)=>root.render(<StudioClient {...props}/>);
          window.unmountProfile=()=>root.unmount(); window.mountProfile();`,
      }));
      builder.onResolve({ filter: /^next\/(navigation|link)$/ }, (args) => ({ path: args.path, namespace: 'client-framework' }));
      builder.onLoad({ filter: /.*/, namespace: 'client-framework' }, (args) => ({ loader: 'tsx', contents: args.path === 'next/link'
        ? 'export default function Link(props){return <a {...props}/>}'
        : 'export function useRouter(){return {refresh:()=>window.refreshCharacters?.(),push:()=>{}}} export function useSearchParams(){return new URLSearchParams(window.location.search)}',
      }));
    } }],
  });
  assert.equal(build.success, true, String(build.logs));
  const sources = await Promise.all([
    ...['ProfileClient.tsx', 'Payments.tsx', 'Catalog.tsx'].map((file) => componentRoot + file),
    characterRoot, studioRoot, appRoot + '/src/components/CharacterUploader.tsx',
    appRoot + '/src/app/(consumer)/wardrobe/page.tsx', appRoot + '/src/app/(consumer)/poses/page.tsx',
  ].map((file) => readFile(file, 'utf8')));
  const compiler = await compile('@import "tailwindcss"; @plugin "daisyui";', { base: appRoot, onDependency() {} });
  const css = compiler.build(`${sources.join('\n')} mx-auto w-full max-w-6xl px-4 py-6 sm:px-6`.match(/[\w:/.-]+/g));
  server = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch(request) {
    const path = new URL(request.url).pathname;
    if (path === '/profile.js') return new Response(build.outputs[0], { headers: { 'Content-Type': 'text/javascript' } });
    if (path === '/style.css') return new Response(css, { headers: { 'Content-Type': 'text/css' } });
    return new Response('<!doctype html><html lang="zh-CN"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/style.css"><body><main id="root" class="mx-auto w-full max-w-6xl px-4 py-6 sm:px-6"></main><script type="module" src="/profile.js"></script></body></html>', { headers: { 'Content-Type': 'text/html' } });
  } });
  browser = await chromium.launch({ channel: 'msedge', headless: true });
});
after(async () => { await browser?.close(); server?.stop(true); delete globalThis.materialFetch; });

async function openProfile(overrides = {}, viewport = { width: 1024, height: 768 }) {
  const state = {
    info: { id: user.id, quota: 9000000, app_quota: 0, app_used_quota: 123 },
    catalog: { plans: [plan], products: [product] },
    checkout: { status: 'success', payment_method: 'balance', order_id: 'order-success' },
    total: 21, calls: [], ...overrides,
  };
  const page = await browser.newPage({ viewport });
  await page.addInitScript(() => {
    const fetch = window.fetch.bind(window);
    window.profileRequests = [];
    window.fetch = (input, options) => {
      const record = { path: String(input), signalAttached: !!options?.signal, aborted: false };
      window.profileRequests.push(record);
      options?.signal?.addEventListener('abort', () => { record.aborted = true; }, { once: true });
      return fetch(input, options);
    };
  });
  await page.route('**/api/gateway/**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    state.calls.push({ path: url.pathname, search: url.search, body: request.postDataJSON() });
    if (state.beforeResponse) await state.beforeResponse(url, request);
    if (state.unauthorized === url.pathname) { await route.fulfill({ status: 401, json: { success: false, message: '登录已失效', error_code: 'invalid_token' } }); return; }
    if (state.failure === url.pathname) { await route.fulfill({ json: { success: false, message: '测试请求被拒绝', error_code: 'checkout_failed' } }); return; }
    let data = state.checkout;
    if (url.pathname.endsWith('/userinfo')) data = state.info;
    if (url.pathname.endsWith('/catalog')) data = state.catalog;
    if (url.pathname.endsWith('/logs')) {
      const pageNumber = Number(url.searchParams.get('page'));
      if (state.failPage === pageNumber) { await route.fulfill({ status: 502, json: { success: false, message: '日志暂时不可用', error_code: 'server_error' } }); return; }
      data = { data: state.total === 0 ? [] : [{ id: pageNumber, model: `model-page-${pageNumber}`, quota: 11, prompt_tokens: 0, completion_tokens: 7, app_id: 7 }], total: state.total, page: pageNumber, page_size: 20 };
    }
    if (request.method() === 'POST' && state.checkout.status === 'success') state.info.app_quota = 500000;
    // Serialize the real BFF success response, not an independently maintained mock envelope.
    await route.fulfill({ json: await gatewaySuccess(data).json() });
  });
  await page.route('https://checkout.example/**', (route) => route.fulfill({ body: '模拟支付页，不发生付款' }));
  await page.goto(server.url.href);
  return { page, state };
}
const waitText = (page, text) => page.getByText(text, { exact: false }).first().waitFor({ state: 'visible' });
const count = (state, endpoint) => state.calls.filter((call) => call.path.endsWith(endpoint)).length;
async function confirmTopup(page) {
  await page.getByRole('button', { name: '确认充值', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '确认付款：积分充值', exact: true });
  await dialog.waitFor({ state: 'visible' });
  return dialog;
}

test('zero and missing credits render in user-facing credit units without platform wallet text', async () => {
  for (const info of [{ id: user.id, quota: 9000000, app_quota: 0, app_used_quota: 123 }, { id: user.id, quota: 9000000 }]) {
    const { page } = await openProfile({ info });
    try {
      await waitText(page, '我的积分');
      assert.equal(await page.locator('p.text-primary').innerText(), info.app_quota === 0 ? '0 积分' : '—');
      if (info.app_quota === 0) {
        await waitText(page, '积分不足，可在下方充值后继续换装。');
        await waitText(page, '已用积分：0.25 积分');
      }
      assert.equal(await page.getByText('平台余额', { exact: false }).count(), 0);
      assert.equal(await page.getByText('quota', { exact: false }).count(), 0);
    } finally { await page.close(); }
  }
});

test('real success envelope with null error_code displays credits, catalog and logs', async () => {
  const { page } = await openProfile();
  try {
    await waitText(page, '剩余积分');
    await waitText(page, plan.title);
    await waitText(page, product.name);
    await waitText(page, 'model-page-1');
    assert.equal(await page.getByRole('button', { name: '重试', exact: true }).count(), 0);
    assert.equal(await page.getByRole('button', { name: '重试目录', exact: true }).count(), 0);
  } finally { await page.close(); }
});

test('topup rejects values outside integer 1..10000 and accepts both boundaries', async () => {
  const { page, state } = await openProfile();
  try {
    const input = page.getByLabel('自定义数量（1 至 10000 的整数）');
    for (const amount of ['0', '1.5', '10001']) {
      await input.fill(amount); await input.blur();
      await waitText(page, '请输入 1 至 10000 的整数');
      assert.equal(await input.getAttribute('aria-invalid'), 'true');
      assert.equal(count(state, '/topup'), 0);
    }
    for (const amount of ['1', '10000']) {
      await input.fill(amount);
      const dialog = await confirmTopup(page);
      await waitText(page, `充值数量：${amount}，到账积分以网关结算结果为准。`);
      await dialog.getByRole('button', { name: '取消', exact: true }).click();
      await dialog.waitFor({ state: 'hidden' });
    }
    assert.equal(count(state, '/topup'), 0);
  } finally { await page.close(); }
});

test('fractional amount is rejected; balance topup requires confirmation and submits display units', async () => {
  const { page, state } = await openProfile();
  try {
    const input = page.getByLabel('自定义数量（1 至 10000 的整数）');
    await input.fill('1.5'); await input.blur();
    await waitText(page, '请输入 1 至 10000 的整数');
    assert.equal(count(state, '/topup'), 0);
    await input.fill('10');
    await page.getByLabel('选择付款方式').selectOption('balance');
    const dialog = await confirmTopup(page);
    await waitText(page, '将使用账户余额充值，余额不足时会被拒绝。');
    assert.equal(count(state, '/topup'), 0);
    assert.equal(await dialog.getByRole('button', { name: '取消', exact: true }).evaluate((button) => button === document.activeElement), true);
    await dialog.getByRole('button', { name: '确认付款', exact: true }).click();
    await waitText(page, '支付成功，积分已更新。'); await waitText(page, '1,000 积分');
    assert.deepEqual(state.calls.find((call) => call.path.endsWith('/topup')).body, { amount: 10, payment_method: 'balance' });
    assert.equal(count(state, '/catalog'), 1);
    assert.equal(count(state, '/userinfo'), 2);
    assert.equal(count(state, '/logs'), 2);
  } finally { await page.close(); }
});

test('catalog balance purchases debit credits and disable plans that disallow it', async () => {
  const { page, state } = await openProfile({ catalog: { plans: [{ ...plan, allow_balance_pay: false }], products: [product] } });
  try {
    await page.getByLabel('选择付款方式').selectOption('balance');
    await waitText(page, plan.title);
    assert.equal(await page.getByRole('button', { name: '购买套餐', exact: true }).isDisabled(), true);
    await page.getByRole('button', { name: '购买积分商品', exact: true }).click();
    await waitText(page, '将从积分中扣款');
    await page.getByRole('dialog').getByRole('button', { name: '确认付款', exact: true }).click();
    await waitText(page, '支付成功，积分已更新。');
    assert.deepEqual(state.calls.find((call) => call.path.endsWith('/checkout')).body, { kind: 'product', id: 2, payment_method: 'balance' });
  } finally { await page.close(); }
});

test('plan checkout confirms one plan and shows its credit allowance', async () => {
  const { page, state } = await openProfile();
  try {
    await page.getByLabel('选择付款方式').selectOption('balance');
    await page.getByRole('button', { name: '购买套餐', exact: true }).click();
    await waitText(page, '套餐积分：600 积分');
    await page.getByRole('dialog').getByRole('button', { name: '确认付款', exact: true }).click();
    await waitText(page, '支付成功，积分已更新。');
    assert.deepEqual(state.calls.find((call) => call.path.endsWith('/checkout')).body, { kind: 'plan', id: 1, payment_method: 'balance' });
  } finally { await page.close(); }
});

test('pending HTTPS order opens native modal; Escape restores focus and refreshes only info/logs', async () => {
  const { page, state } = await openProfile({ checkout: { status: 'pending', checkout_url: 'https://checkout.example/pay', order_id: 'order-pending' } });
  try {
    const dialog = await confirmTopup(page);
    await dialog.getByRole('button', { name: '确认付款', exact: true }).click();
    const payment = page.getByRole('dialog', { name: '在线支付', exact: true });
    await payment.waitFor({ state: 'visible' });
    assert.equal(await payment.evaluate((element) => element.matches(':modal') && element.contains(document.activeElement)), true);
    assert.equal(await payment.getByRole('link').getAttribute('rel'), 'noopener noreferrer');
    assert.equal(await payment.locator('iframe').getAttribute('src'), 'https://checkout.example/pay');
    await page.keyboard.press('Escape');
    await payment.waitFor({ state: 'hidden' });
    await waitText(page, '我的积分');
    assert.equal(await page.getByRole('button', { name: '确认充值', exact: true }).evaluate((button) => button === document.activeElement), true);
    assert.equal(count(state, '/userinfo'), 2); assert.equal(count(state, '/logs'), 2); assert.equal(count(state, '/catalog'), 1);
    assert.equal(await page.getByText('支付成功，积分已更新。', { exact: false }).count(), 0);
    assert.deepEqual(state.calls.find((call) => call.path.endsWith('/topup')).body, { amount: 50, payment_method: 'waffo_pancake' });
  } finally { await page.close(); }
});

test('unsafe or missing payment links are blocked while pending order handles remain visible', async () => {
  for (const url of ['javascript:alert(1)', 'http://checkout.example/pay', 'https://user:secret@checkout.example/pay', undefined]) {
    const { page, state } = await openProfile({ checkout: { status: 'pending', checkout_url: url, order_id: 'order-blocked' } });
    try {
      const dialog = await confirmTopup(page);
      await dialog.getByRole('button', { name: '确认付款', exact: true }).click();
      const payment = page.getByRole('dialog', { name: '在线支付', exact: true });
      await waitText(page, '支付链接暂时无法打开');
      assert.equal(await payment.locator('iframe').count(), 0); assert.equal(await payment.getByRole('link').count(), 0);
      await payment.getByRole('button', { name: '关闭并刷新', exact: true }).click();
      await waitText(page, '我的积分');
      assert.equal(count(state, '/userinfo'), 2); assert.equal(count(state, '/catalog'), 1);
      await waitText(page, 'order-blocked');
    } finally { await page.close(); }
  }
});

test('logs paginate at 20, isolate page failures, and allow independent retries', async () => {
  const { page, state } = await openProfile({ failPage: 2 });
  try {
    await waitText(page, 'model-page-1');
    await page.getByRole('button', { name: '下一页', exact: true }).click();
    await waitText(page, '日志暂时不可用'); await waitText(page, plan.title);
    assert.equal(count(state, '/userinfo'), 1); assert.equal(count(state, '/catalog'), 1);
    state.failPage = undefined;
    await page.getByRole('button', { name: '重试', exact: true }).click();
    await waitText(page, 'model-page-2');
    assert.equal(state.calls.at(-1).search, '?page=2&page_size=20');
    assert.equal(await page.getByRole('button', { name: '下一页', exact: true }).isDisabled(), true);
    await page.getByRole('button', { name: '上一页', exact: true }).click(); await waitText(page, 'model-page-1');
  } finally { await page.close(); }
});

test('business failures stay visible without marking an order paid', async () => {
  const { page, state } = await openProfile({ failure: '/api/gateway/topup' });
  try {
    const dialog = await confirmTopup(page); await dialog.getByRole('button', { name: '确认付款', exact: true }).click();
    await waitText(page, '测试请求被拒绝（checkout_failed）');
    await waitText(page, 'model-page-1'); await waitText(page, plan.title);
    assert.equal(await page.getByText('支付成功，积分已更新。', { exact: false }).count(), 0); assert.equal(count(state, '/topup'), 1);
  } finally { await page.close(); }
});

test('empty states are independent; mobile and landscape contain long catalog text', async () => {
  const empty = await openProfile({ catalog: { plans: [], products: [] }, total: 0 });
  try {
    await waitText(empty.page, '当前 App 暂无可购买的套餐'); await waitText(empty.page, '当前 App 暂无可购买的积分商品'); await waitText(empty.page, '暂无消费记录');
  } finally { await empty.page.close(); }
  for (const width of [375, 812]) {
    const { page } = await openProfile({ catalog: { plans: [plan], products: [{ ...product, name: 'LongTitle'.repeat(16), description: 'LongDescription'.repeat(32) }] } }, { width, height: 667 });
    try {
      await waitText(page, plan.title);
      const layout = await page.evaluate(() => ({ width: window.innerWidth, scroll: document.documentElement.scrollWidth,
        overflow: [...document.querySelectorAll('main *')].filter((element) => element.getBoundingClientRect().right > window.innerWidth).slice(0, 8).map((element) => ({ tag: element.tagName, classes: element.className, width: element.getBoundingClientRect().width })),
      }));
      assert.equal(layout.scroll <= layout.width, true, JSON.stringify(layout));
    } finally { await page.close(); }
  }
});

test('userinfo and catalog loading/errors keep the other resources available and retry independently', async () => {
  for (const endpoint of ['/api/gateway/userinfo', '/api/gateway/catalog']) {
    const gate = Promise.withResolvers();
    const { page, state } = await openProfile({ failure: endpoint, beforeResponse: async (url) => {
      if (url.pathname === endpoint) await gate.promise;
    } });
    try {
      await waitText(page, endpoint.endsWith('/userinfo') ? '正在读取积分' : '正在加载套餐');
      await waitText(page, 'model-page-1');
      await waitText(page, endpoint.endsWith('/userinfo') ? plan.title : '我的积分');
      gate.resolve();
      await waitText(page, endpoint.endsWith('/userinfo') ? '暂时无法读取积分，请稍后重试。' : '测试请求被拒绝（checkout_failed）');
      state.failure = undefined;
      await page.getByRole('button', { name: endpoint.endsWith('/userinfo') ? '重试' : '重试目录', exact: true }).first().click();
      await waitText(page, endpoint.endsWith('/userinfo') ? '我的积分' : plan.title);
      assert.equal(count(state, endpoint), 2); assert.equal(count(state, '/logs'), 1);
      assert.equal(count(state, endpoint.endsWith('/userinfo') ? '/catalog' : '/userinfo'), 1);
    } finally { gate.resolve(); await page.close(); }
  }
});

test('omitted optional reset metadata does not hide the App catalog or invent a reset policy', async () => {
  const { quota_reset_period, ...minimalPlan } = plan;
  assert.equal(quota_reset_period, 'monthly');
  const { page } = await openProfile({ catalog: { plans: [minimalPlan], products: [product] } });
  try {
    await waitText(page, '积分重置：未提供');
    await waitText(page, product.name);
    assert.equal(await page.getByRole('button', { name: '购买套餐', exact: true }).isEnabled(), true);
  } finally { await page.close(); }
});

test('catalog and confirmation preserve small configured prices without assuming a dollar conversion', async () => {
  const { page, state } = await openProfile({ catalog: { plans: [plan], products: [{ ...product, name: 'LongProductTitle'.repeat(16), price_amount: 0.0001, currency: 'CNY' }] },
    checkout: { status: 'pending', checkout_url: 'https://checkout.example/product', order_id: 'product-pending' },
  });
  try {
    await waitText(page, '价格：0.0001 CNY');
    await page.getByRole('button', { name: '购买积分商品', exact: true }).click();
    const confirmation = page.getByRole('dialog');
    await waitText(page, '购买数量：1 份；价格：0.0001 CNY');
    assert.equal(await confirmation.getByRole('heading').evaluate((heading) => heading.scrollWidth <= heading.clientWidth), true);
    await confirmation.getByRole('button', { name: '确认付款', exact: true }).click();
    const payment = page.getByRole('dialog', { name: '在线支付', exact: true });
    await payment.waitFor({ state: 'visible' });
    assert.deepEqual(state.calls.find((call) => call.path.endsWith('/checkout')).body, { kind: 'product', id: 2, payment_method: 'waffo_pancake' });
    await payment.getByRole('button', { name: '关闭并刷新', exact: true }).click();
    await payment.waitFor({ state: 'hidden' });
    assert.equal(await page.getByText('支付成功，积分已更新。', { exact: false }).count(), 0);
  } finally { await page.close(); }
});

test('a failed later purchase does not retain the previous paid-success receipt', async () => {
  const { page, state } = await openProfile();
  try {
    let dialog = await confirmTopup(page);
    await dialog.getByRole('button', { name: '确认付款', exact: true }).click();
    await dialog.waitFor({ state: 'hidden' });
    await waitText(page, '支付成功，积分已更新。'); await waitText(page, '1,000 积分');
    state.failure = '/api/gateway/topup';
    dialog = await confirmTopup(page);
    await dialog.getByRole('button', { name: '确认付款', exact: true }).click();
    await waitText(page, '测试请求被拒绝');
    assert.equal(await page.getByText('支付成功，积分已更新。', { exact: false }).count(), 0);
    assert.equal(count(state, '/topup'), 2);
  } finally { await page.close(); }
});

test('unknown or missing checkout status fails closed and never displays paid success', async () => {
  for (const checkout of [{ status: 'confirmed', order_id: 'ambiguous-order' }, { order_id: 'ambiguous-order' }]) {
    const { page, state } = await openProfile({ checkout });
    try {
      const dialog = await confirmTopup(page);
      await dialog.getByRole('button', { name: '确认付款', exact: true }).click();
      await waitText(page, '网关返回的数据不完整');
      await dialog.waitFor({ state: 'hidden' });
      assert.equal(await page.getByText('支付成功，积分已更新。', { exact: false }).count(), 0);
      assert.equal(await page.getByRole('dialog').count(), 0);
      assert.equal(count(state, '/topup'), 1);
    } finally { await page.close(); }
  }
});

test('unmount aborts all resource reads and in-flight payments; submitting cannot be dismissed or repeated', async () => {
  const readGate = Promise.withResolvers();
  const reads = await openProfile({ beforeResponse: () => readGate.promise });
  try {
    await reads.page.waitForFunction(() => window.profileRequests.length === 3);
    await reads.page.evaluate(() => window.unmountProfile());
    assert.equal(await reads.page.evaluate(() => window.profileRequests.every((request) => request.signalAttached && request.aborted)), true);
  } finally { readGate.resolve(); await reads.page.close(); }

  const paymentGate = Promise.withResolvers();
  const { page, state } = await openProfile({ beforeResponse: async (url) => {
    if (url.pathname.endsWith('/topup')) await paymentGate.promise;
  } });
  try {
    await waitText(page, 'model-page-1'); await waitText(page, plan.title);
    const dialog = await confirmTopup(page);
    await dialog.getByRole('button', { name: '确认付款', exact: true }).click();
    await waitText(page, '正在提交…');
    await page.keyboard.press('Escape');
    assert.equal(await dialog.evaluate((element) => element.matches(':modal')), true);
    assert.equal(await dialog.getByRole('button', { name: '正在提交…', exact: true }).isDisabled(), true);
    assert.equal(count(state, '/topup'), 1);
    await page.evaluate(() => window.unmountProfile());
    assert.equal(await page.evaluate(() => window.profileRequests.find((request) => request.path.endsWith('/topup')).aborted), true);
    assert.equal(count(state, '/userinfo'), 1); assert.equal(count(state, '/logs'), 1);
  } finally { paymentGate.resolve(); await page.close(); }
});

test('server page passes only display-field whitelist across the client boundary', async () => {
  const fixture = { ...user, identities: [{ access_token: 'identity-secret' }],
    app_metadata: { provider: 'custom:newapi', provider_token: 'app-secret', roles: ['admin'] },
    user_metadata: { name: '测试用户', full_name: '完整名字', provider_token: 'metadata-secret', refresh_token: 'refresh-secret', nested: { token: 'nested-secret' } },
  };
  const build = await Bun.build({
    entrypoints: [resolve(componentRoot, '../page.tsx')], target: 'bun', format: 'esm',
    plugins: [{ name: 'profile-server-boundaries', setup(builder) {
      builder.onResolve({ filter: /^(next\/navigation|@\/lib\/supabase\/server|\.\/_components\/ProfileClient)$/ }, (args) => ({ path: args.path, namespace: 'profile-boundary' }));
      builder.onLoad({ filter: /.*/, namespace: 'profile-boundary' }, (args) => ({ loader: 'js', contents:
        args.path === 'next/navigation' ? 'export function redirect(path){throw new Error(path)}' :
          args.path === '@/lib/supabase/server' ? `export async function createServerSupabase(){return {auth:{getUser:async()=>({data:{user:${JSON.stringify(fixture)}}})}}}` :
            'export function ProfileClient(){return null}',
      }));
    } }],
  });
  assert.equal(build.success, true, String(build.logs));
  const { default: ProfilePage } = await import(`data:text/javascript;base64,${Buffer.from(await build.outputs[0].text()).toString('base64')}`);
  const element = await ProfilePage();
  assert.deepEqual(element.props.user, { id: user.id, email: user.email,
    app_metadata: { provider: 'custom:newapi' }, user_metadata: { name: '测试用户', full_name: '完整名字' },
  });
  assert.equal(/secret|roles|identities/.test(JSON.stringify(element.props)), false);
});

test('refreshed character props update the list without discarding an open edit draft; clearing description sends null', async () => {
  const { page } = await openProfile();
  let saved;
  await page.route('**/api/characters?**', async (route) => {
    saved = route.request().postDataJSON();
    await route.fulfill({ json: { success: true } });
  });
  try {
    await page.evaluate((item) => window.mountCharacters([item]), character);
    await page.getByRole('button', { name: '编辑', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: '编辑人物', exact: true });
    await dialog.getByLabel('人物名称', { exact: true }).fill('草稿名称');
    await dialog.getByLabel('人物描述', { exact: true }).fill('');
    await page.evaluate((item) => window.mountCharacters([{ ...item, name: '服务端名称' }, { ...item, id: 'character-2', name: '新增人物' }]), character);
    await waitText(page, '新增人物');
    assert.equal(await dialog.getByLabel('人物名称', { exact: true }).inputValue(), '草稿名称');
    assert.equal(await dialog.evaluate((element) => element.matches(':modal')), true);
    await dialog.getByRole('button', { name: '保存', exact: true }).click();
    await dialog.waitFor({ state: 'hidden' });
    await waitText(page, '草稿名称');
    assert.deepEqual(saved, { name: '草稿名称', description: null });
    await page.getByRole('button', { name: '编辑', exact: true }).first().click();
    await page.keyboard.press('Escape');
    await dialog.waitFor({ state: 'hidden' });
    assert.equal(await page.getByRole('button', { name: '编辑', exact: true }).first().evaluate((button) => button === document.activeElement), true);
  } finally { await page.close(); }
});

test('uploader rejects over 4 MiB and blank names, accepts exact cap and text bounds, and refresh displays the upload', async () => {
  const { page } = await openProfile();
  let upload;
  await page.route('**/api/characters', async (route) => {
    const request = route.request();
    const body = request.postDataBuffer();
    const form = await new Request('https://fixture.example', { method: 'POST', headers: request.headers(), body }).formData();
    upload = { fileSize: form.get('file').size, name: form.get('name'), description: form.get('description'), bodySize: body.length };
    await route.fulfill({ json: { success: true } });
  });
  try {
    await page.evaluate((item) => {
      window.mountCharacters([]);
      window.refreshCharacters = () => window.mountCharacters([{ ...item, name: '上传后人物' }]);
    }, character);
    await page.getByRole('button', { name: '上传人物', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: '上传人物', exact: true });
    const input = dialog.getByLabel('人物图片', { exact: true });
    await input.setInputFiles({ name: 'large.png', mimeType: 'image/png', buffer: Buffer.alloc(4 * 1024 * 1024 + 1) });
    await waitText(page, '上限 4 MiB');
    assert.equal(await dialog.getByRole('button', { name: '确认上传', exact: true }).count(), 0);
    await input.setInputFiles({ name: '人物.png', mimeType: 'image/png', buffer: Buffer.alloc(4 * 1024 * 1024) });
    const name = dialog.getByLabel('人物名称', { exact: true });
    const description = dialog.getByLabel('人物描述（可选）', { exact: true });
    assert.equal(await name.getAttribute('maxlength'), '100');
    assert.equal(await description.getAttribute('maxlength'), '1000');
    await name.fill('   ');
    await dialog.getByRole('button', { name: '确认上传', exact: true }).click();
    await waitText(page, '名称须为 1 至 100 个字符');
    assert.equal(upload, undefined);
    await name.fill('人'.repeat(100));
    await description.fill('描述'.repeat(500));
    await dialog.getByRole('button', { name: '确认上传', exact: true }).click();
    await dialog.waitFor({ state: 'hidden' });
    await waitText(page, '上传后人物');
    assert.equal(upload.fileSize, 4 * 1024 * 1024);
    assert.equal(upload.name.length, 100);
    assert.equal(upload.description.length, 1000);
    assert.equal(upload.bodySize < 4.5 * 1024 * 1024, true);
  } finally { await page.close(); }
});

test('wardrobe and poses paginate beyond page seven with current window, bounded navigation and stable scoped queries', async () => {
  for (const [route, table, size] of [['wardrobe', 'outfits', 20], ['poses', 'poses', 60]]) {
    const state = { tables: { [table]: [{ id: 'material-1', name_zh: '测试素材', thumb_path: 'fixture.png' }] }, counts: { [table]: size * 11 } };
    const { compiledModule, calls } = await loadServerFixture(`src/app/(consumer)/${route}/page.tsx`, state);
    for (const number of [1, 7, 8, 11]) {
      const params = { page: String(number), q: 'needle"),client_id.eq.other', category: 'dress', system: 'human', angle: 'front' };
      const element = await compiledModule.default({ searchParams: Promise.resolve(params) });
      const page = await browser.newPage();
      try {
        await page.setContent(renderToStaticMarkup(element));
        const nav = page.getByRole('navigation');
        assert.equal(await nav.locator('[aria-current="page"]').innerText(), String(number));
        assert.equal(await nav.getByRole('link', { name: '上一页', exact: true }).count(), number > 1 ? 1 : 0);
        assert.equal(await nav.getByRole('link', { name: '下一页', exact: true }).count(), number < 11 ? 1 : 0);
        if (number < 11) {
          const next = new URL(await nav.getByRole('link', { name: '下一页', exact: true }).getAttribute('href'), 'https://fixture.example');
          assert.equal(next.searchParams.get('page'), String(number + 1));
          assert.equal(next.searchParams.get('q'), params.q);
          assert.equal(next.searchParams.get(route === 'wardrobe' ? 'category' : 'angle'), route === 'wardrobe' ? 'dress' : 'front');
        }
        const query = calls.findLast((url) => url.searchParams.has('limit'));
        assert.equal(query.searchParams.get('offset'), String((number - 1) * size));
        assert.equal(query.searchParams.get('order').endsWith('id.asc'), true);
        assert.equal(query.searchParams.get('or').includes('ilike."%needle\\"),client_id.eq.other%"'), true);
      } finally { await page.close(); }
    }
    assert.equal(calls.every((url) => url.searchParams.get('client_id') === 'eq.app-fixture'), true);
    await assert.rejects(() => compiledModule.default({ searchParams: Promise.resolve({ page: '12', q: 'dress' }) }), new RegExp(`^Error: /${route}\\?q=dress&page=11$`));
  }
});

test('empty or failed model catalog never fabricates a model; Studio selections cannot enable generation', async () => {
  for (const state of [{}, { modelError: true }]) {
    const api = await loadServerFixture('src/app/api/models/route.ts', state);
    const response = await api.compiledModule.GET();
    assert.equal(response.status, state.modelError ? 503 : 200);
    const body = await response.json();
    if (state.modelError) assert.equal(body.success, false);
    else assert.deepEqual(body, { success: true, data: [] });
    assert.equal(api.calls[0].searchParams.get('client_id'), 'in.(app-fixture,)');
    assert.equal(api.calls[0].searchParams.get('usage'), 'eq.image');

    const fixture = { ...state, tables: {
      characters: [{ ...character, image_path: 'character.png' }],
      outfits: [{ id: 'outfit-1', name_zh: '测试服装', thumb_path: 'outfit.png' }],
      poses: [{ id: 'pose-1', name_zh: '测试姿势', thumb_path: 'pose.png' }],
    } };
    const studio = await loadServerFixture('src/app/(consumer)/studio/page.tsx', fixture);
    const element = await studio.compiledModule.default({ searchParams: Promise.resolve({ outfit_q: 'dress', pose_q: 'front' }) });
    const [alert, client] = element.props.children;
    assert.equal(renderToStaticMarkup(alert).includes('当前无法生成'), true);
    assert.deepEqual(client.props.models, []);
    assert.equal(studio.calls.filter((url) => !url.pathname.endsWith('/ai_models')).every((url) => url.searchParams.get('client_id') === 'eq.app-fixture'), true);
    const { page } = await openProfile();
    try {
      await page.evaluate((props) => window.mountStudio(props), client.props);
      await page.getByRole('button', { name: character.name, exact: true }).click();
      await page.getByRole('button', { name: '测试服装', exact: true }).click();
      await page.getByRole('button', { name: '测试姿势', exact: true }).click();
      assert.equal(await page.getByRole('button', { name: '开始生成', exact: true }).isDisabled(), true);
      assert.equal(await page.getByLabel('模型', { exact: true }).locator('option[value="gpt-image-1"]').count(), 0);
    } finally { await page.close(); }
  }
});

test('all expired-session responses redirect to login rather than continuing as success', async () => {
  for (const endpoint of ['/api/gateway/userinfo', '/api/gateway/logs', '/api/gateway/catalog', '/api/gateway/topup', '/api/gateway/checkout']) {
    const { page } = await openProfile({ unauthorized: endpoint });
    try {
      if (endpoint.endsWith('/topup')) {
        const dialog = await confirmTopup(page); await dialog.getByRole('button', { name: '确认付款', exact: true }).click();
      }
      if (endpoint.endsWith('/checkout')) {
        await page.getByRole('button', { name: '购买积分商品', exact: true }).click();
        await page.getByRole('dialog').getByRole('button', { name: '确认付款', exact: true }).click();
      }
      await page.waitForURL('**/login?error=session_expired');
      assert.equal(new URL(page.url()).search, '?error=session_expired');
    } finally { await page.close(); }
  }
});
