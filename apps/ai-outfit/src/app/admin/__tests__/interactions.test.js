import { afterAll, afterEach, beforeEach, expect, mock, test } from 'bun:test';
// Reuse the workspace's installed DOM harness; production stays Ant Design-only.
import { JSDOM } from '../../../../../../web/node_modules/jsdom/lib/api.js';

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost' });
const savedGlobals = new Map();
for (const key of ['window', 'document', 'navigator', 'HTMLElement', 'Element', 'Node', 'SVGElement', 'ShadowRoot', 'MutationObserver', 'getComputedStyle']) {
  savedGlobals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
  Object.defineProperty(globalThis, key, { configurable: true, writable: true, value: key === 'getComputedStyle' ? (node) => dom.window.getComputedStyle(node) : dom.window[key] });
}
dom.window.matchMedia = () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} });
dom.window.getComputedStyle = new Proxy(dom.window.getComputedStyle, { apply: (target, receiver, args) => Reflect.apply(target, receiver, [args[0]]) });
const oldActEnvironment = globalThis.IS_REACT_ACT_ENVIRONMENT;
globalThis.IS_REACT_ACT_ENVIRONMENT = true;
const originalResizeObserver = globalThis.ResizeObserver;
globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
const originalFetch = globalThis.fetch;

const React = await import('react');
const { act, createElement } = React;
const { createRoot } = await import('react-dom/client');
const { ConfigProvider, message } = await import('antd');
ConfigProvider.config({ holderRender: (children) => createElement(ConfigProvider, { theme: { token: { motion: false } } }, children) });
message.config({ duration: 0 });
mock.module('next/navigation', () => ({ usePathname: () => '/admin/models' }));
mock.module('next/link', () => ({ default: ({ href, children, ...props }) => createElement('a', { href, ...props }, children) }));
const Models = (await import('../models/page')).default;
const Prompts = (await import('../prompts/page')).default;
const Outfits = (await import('../outfits/page')).default;
const Generations = (await import('../generations/page')).default;
const Layout = (await import('../_components/layout-inner')).default;
let root;
let host;

beforeEach(() => {
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => {
    root.unmount();
    message.destroy();
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  host.remove();
  globalThis.fetch = originalFetch;
});
afterAll(async () => {
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
  dom.window.close();
  for (const [key, descriptor] of savedGlobals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor);
    else delete globalThis[key];
  }
  globalThis.IS_REACT_ACT_ENVIRONMENT = oldActEnvironment;
  globalThis.ResizeObserver = originalResizeObserver;
  mock.restore();
});
const response = (data, status = 200) => new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
const mount = async (Page, props) => act(async () => { root.render(createElement(ConfigProvider, { theme: { token: { motion: false } } }, createElement(Page, props))); });
const button = (text, scope = document) => Array.from(scope.querySelectorAll('button')).find((node) => node.textContent.replace(/\s/g, '') === text.replace(/\s/g, ''));
const click = async (node) => { expect(node).toBeTruthy(); await act(async () => { node.click(); }); };
const settle = async (predicate) => {
  for (let i = 0; i < 40 && !predicate(); i++) await act(async () => { await new Promise((resolve) => setImmediate(resolve)); });
  expect(predicate()).toBe(true);
};
const baseRow = { id: 'bb2ed7f9-b315-447c-9389-fb40b2ca13e4', client_id: 'nac_outfit', is_active: true, is_default: false, usage: 'outfit', sort_order: 0, created_at: '2026-11-10T00:00:00Z', updated_at: '2026-11-10T00:00:00Z' };
const cases = [
  { Page: Models, path: 'ai_models', row: { ...baseRow, model_id: 'safe-model', display_name: 'Preserved model' }, field: 'display_name', value: 'Preserved model' },
  { Page: Prompts, path: 'prompt_templates', row: { ...baseRow, name: 'Preserved prompt', template: 'Use {{outfits}}' }, field: 'name', value: 'Preserved prompt' },
  { Page: Outfits, path: 'outfits', row: { ...baseRow, name_zh: 'Preserved outfit', category: 'casual', provider_id: 'provider', bucket: 'media-official', thumb_path: 'nac_outfit/thumb.png', preview_path: 'nac_outfit/preview.png' }, field: 'name_zh', value: 'Preserved outfit' },
];

test.each(cases)('$path failed edit keeps modal and inputs; retry saves only editable fields', async ({ Page, path, row, field, value }) => {
  let fail = true;
  const writes = [];
  globalThis.fetch = async (url, init) => {
    if (!init?.method) return response({ data: [row], total: 1 });
    writes.push({ url, method: init.method, fields: JSON.parse(init.body) });
    return fail ? response({ error: 'SAVE_FAILED' }, 500) : response({ success: true });
  };
  await mount(Page);
  await settle(() => Boolean(button('编辑')));
  await click(button('编辑'));
  const input = document.querySelector(`[id="${field}"]`);
  expect(input.value).toBe(value);
  await click(button('OK') ?? button('确定'));
  await settle(() => writes.length === 1 && document.body.textContent.includes('保存失败'));
  expect(document.querySelector('[role="dialog"]')).not.toBeNull();
  expect(input.value).toBe(value);
  expect(writes[0].url).toBe(`/api/admin/${path}?id=${row.id}`);
  expect(writes[0].method).toBe('PATCH');
  for (const key of ['id', 'client_id', 'user_id', 'created_at', 'updated_at', 'bucket', 'thumb_path', 'preview_path', 'provider_id']) expect(writes[0].fields).not.toHaveProperty(key);
  fail = false;
  await click(button('OK') ?? button('确定'));
  await settle(() => writes.length === 2 && document.querySelector('[role="dialog"]') === null);
});

test.each(cases.slice(0, 2))('$path shared defaults have no mutation controls', async ({ Page, row }) => {
  globalThis.fetch = async () => response({ data: [{ ...row, client_id: '' }], total: 1 });
  await mount(Page);
  await settle(() => document.body.textContent.includes('共享默认（只读）'));
  for (const label of ['编辑', '删除', '停用', '设为默认']) expect(button(label)).toBeUndefined();
});

test('failed admin list reports an error and clears the spinner', async () => {
  globalThis.fetch = async () => response({ error: 'LOAD_FAILED' }, 500);
  await mount(Models);
  await settle(() => document.body.textContent.includes('加载失败'));
  expect(document.querySelector('.ant-spin-spinning')).toBeNull();
});

test('repeating an unchanged model search reloads instead of leaving a spinner', async () => {
  let reads = 0;
  globalThis.fetch = async () => { reads++; return response({ data: [], total: 0 }); };
  await mount(Models);
  await settle(() => reads === 1 && !document.querySelector('.ant-spin-spinning'));
  await click(document.querySelector('.ant-input-search button'));
  await settle(() => reads === 2 && !document.querySelector('.ant-spin-spinning'));
});

test('admin navigation uses implemented links and removes local plans editing', async () => {
  await mount(Layout, { role: 'admin', children: 'Content' });
  for (const path of ['/admin', '/admin/outfits', '/admin/poses', '/admin/characters', '/admin/generations', '/admin/models', '/admin/prompts']) expect(document.querySelector(`a[href="${path}"]`)).not.toBeNull();
  expect(document.querySelector('a[href="/admin/plans"]')).toBeNull();
});

const deferred = () => { let resolve; const promise = new Promise((done) => { resolve = done; }); return { promise, resolve }; };
test('generation detail discards older responses and displays reference failures with retry', async () => {
  const olderRefs = deferred();
  const olderImage = deferred();
  const first = { ...baseRow, model: 'first', status: 'succeeded', result_bucket: 'media-user', result_path: 'nac_outfit/first.png' };
  const second = { ...baseRow, id: '232402b8-5678-40da-a2cb-3b92b6d403c4', model: 'second', status: 'pending' };
  let referenceReads = 0;
  globalThis.fetch = async (url) => {
    if (url.startsWith('/api/admin/outfit_generations')) return response({ data: [first, second], total: 2 });
    if (url.includes('signed-url')) return olderImage.promise;
    if (url.includes(first.id)) return olderRefs.promise;
    referenceReads++;
    return referenceReads === 1 ? response({ error: 'LOAD_FAILED' }, 500) : response({ data: { character: null, outfits: [], pose: null } });
  };
  await mount(Generations);
  await settle(() => document.querySelectorAll('tbody button').length === 2);
  await click(document.querySelectorAll('tbody button')[0]);
  await click(document.querySelector('.ant-drawer-close'));
  await click(document.querySelectorAll('tbody button')[1]);
  await settle(() => document.body.textContent.includes('参考图加载失败'));
  await act(async () => {
    olderRefs.resolve(response({ data: { character: { name: 'STALE', url: 'https://example.test/stale.png' }, outfits: [], pose: null } }));
    olderImage.resolve(response({ url: 'https://example.test/stale-result.png' }));
  });
  expect(document.body.textContent).not.toContain('STALE');
  expect(document.querySelector('img[src*="stale"]')).toBeNull();
  expect(document.querySelector('.ant-drawer-body .ant-spin-spinning')).toBeNull();
  await click(button('重试'));
  await settle(() => document.body.textContent.includes('未找到参考图'));
  expect(document.body.textContent).not.toContain('参考图加载失败');
});
