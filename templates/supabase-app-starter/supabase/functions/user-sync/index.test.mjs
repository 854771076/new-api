import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

const source = await readFile(new URL('./index.ts', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

async function sync({ identities, metadata = {}, authorization = 'Bearer fixture', authError = null, configError = null, configuredClient = 'app-one', envClient = 'app-one' }) {
  let handler;
  const writes = [];
  const configWrites = [];
  const supabase = {
    auth: { getUser: async () => ({ data: { user: { id: 'fixture-user', identities, app_metadata: metadata } }, error: authError }) },
    from(table) {
      if (table === 'app_config') {
        return {
          select: () => ({ eq: () => ({ maybeSingle: async () => ({ data: configuredClient ? { client_id: configuredClient } : null, error: configError }) }) }),
          upsert: async (row) => { configWrites.push(row); return { error: null }; },
        };
      }
      assert.equal(table, 'user_profiles');
      return {
        upsert: async (row) => { writes.push(row); return { data: null, error: null }; },
      };
    },
  };
  const env = { SUPABASE_URL: 'https://fixture.invalid', SUPABASE_SERVICE_ROLE_KEY: 'fixture-service', GATEWAY_CLIENT_ID: envClient };
  vm.runInNewContext(compiled, {
    exports: {},
    require: () => ({ createClient: () => supabase }),
    Deno: { env: { get: (name) => env[name] }, serve: (callback) => { handler = callback; } },
    Request, Response, console: { error() {}, warn() {} },
  });
  const response = await handler(new Request('https://fixture.invalid/user-sync', {
    method: 'POST', headers: authorization ? { Authorization: authorization } : {},
  }));
  return { status: response.status, body: await response.json(), writes, configWrites };
}

const identity = (sub = '42', aud = ['app-one'], provider = 'custom:newapi') => ({ provider, identity_data: { sub, aud } });

test('sync maps only a verified provider identity belonging to the configured App', async () => {
  for (const aud of [['app-one'], 'app-one']) {
    const result = await sync({ identities: [identity('42', aud)], metadata: { gateway_user_id: 99 } });
    assert.equal(result.status, 200);
    assert.equal(result.writes.length, 1);
    assert.equal(result.writes[0].gateway_user_id, 42);
    assert.equal(result.writes[0].client_id, 'app-one');
  }
});

test('foreign, missing, malformed and ambiguous identities cannot create a profile', async () => {
  const cases = [
    [], [identity('42', ['app-two'])], [identity('42', ['app-one', 'app-two'])],
    [identity('42', undefined, 'email')], [identity('42'), identity('42')],
    [identity('42suffix')], [identity('0')], [identity('9007199254740992')],
    [identity(null)], [identity('42', null)],
  ];
  for (const identities of cases) {
    const result = await sync({ identities, metadata: { gateway_user_id: 42 } });
    assert.equal(result.status, 403);
    assert.equal(result.writes.length, 0);
  }
});

test('missing and invalid sessions cannot sync', async () => {
  for (const options of [{ authorization: '' }, { authError: { message: 'expired' } }]) {
    const result = await sync({ identities: [identity()], ...options });
    assert.equal(result.status, 401);
    assert.equal(result.writes.length, 0);
    assert.equal(result.configWrites.length, 0);
  }
});

test('configuration failures cannot overwrite the App binding', async () => {
  for (const options of [{ configError: { message: 'database unavailable' } }, { envClient: 'app-two' }]) {
    const result = await sync({ identities: [identity()], ...options });
    assert.equal(result.status, 500);
    assert.equal(result.writes.length, 0);
    assert.equal(result.configWrites.length, 0);
  }
});
