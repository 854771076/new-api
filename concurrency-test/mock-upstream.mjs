/**
 * mock-upstream.mjs — 可控假上游(替代 delay-proxy,零真实成本、零上游压力)。
 *
 * - 监听 0.0.0.0:<upstream.mockPort>,端口/延迟读同目录 config.json(唯一配置源)
 * - GET  /health            -> 200 text/plain "ok"(就绪探针)
 * - POST 任意路径(典型 /v1/chat/completions) -> 睡 delay±jitter 后返回 OpenAI 风格 JSON
 * - 请求体 stream:true 也按非流 JSON 返回(执行器永远发 stream:false)
 * - 每请求独立 timer,天然并发;JSON 解析失败回 400 且不崩;SIGINT 优雅退出
 *
 * 用法: node concurrency-test/mock-upstream.mjs
 */
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const DIR = path.dirname(fileURLToPath(import.meta.url));
const cfg = JSON.parse(fs.readFileSync(path.join(DIR, 'config.json'), 'utf8'));
const PORT = Number(cfg.upstream.mockPort) || 3099;
const DELAY = Number(cfg.upstream.fakeDelayMs) || 0; // 固定延迟
const JIT = Number(cfg.upstream.fakeJitterMs) || 0; // ± 抖动
const MAX_BODY = 8 * 1024 * 1024;

const clock = () => {
  const d = new Date();
  const p = (n) => String(n).padStart(2, '0');
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
};
const line = (parts) => console.log(`[${clock()}] ${parts}`);

/** 读完请求体(超限/出错 reject,由调用方回 400)。 */
function collect(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    req.on('data', (c) => {
      size += c.length;
      if (size > MAX_BODY) {
        reject(new Error('body too large'));
        req.destroy();
        return;
      }
      chunks.push(c);
    });
    req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    req.on('error', reject);
  });
}

// 词数 → token 估算(约 1.3 token/词),只为让 usage 字段有合理量级。
function estimatePromptTokens(text) {
  const words = text.split(/\s+/).filter(Boolean).length;
  return Math.max(1, Math.round(words * 1.3));
}

function completionFor(body) {
  const model = typeof body?.model === 'string' && body.model ? body.model : 'unknown';
  const messages = Array.isArray(body?.messages) ? body.messages : [];
  let prompt = '';
  for (const m of messages) if (typeof m?.content === 'string') prompt += (prompt ? ' ' : '') + m.content;
  const promptTokens = estimatePromptTokens(prompt);
  return {
    id: 'cmpl-mock',
    object: 'chat.completion',
    created: Math.floor(Date.now() / 1000),
    model, // 回显请求 model
    choices: [{ index: 0, message: { role: 'assistant', content: 'mock-ok' }, finish_reason: 'stop' }],
    usage: { prompt_tokens: promptTokens, completion_tokens: 8, total_tokens: promptTokens + 8 },
  };
}

function sendJson(res, code, obj) {
  const payload = JSON.stringify(obj);
  res.writeHead(code, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(payload) });
  res.end(payload);
}

const server = http.createServer((req, res) => {
  const t0 = Date.now();
  const url = req.url || '/';
  const pathOnly = url.split('?')[0];
  // 每请求一行日志:方法/路径/model/耗时
  const done = (note) => line(`${req.method} ${pathOnly} ${note} ${Date.now() - t0}ms`);

  if (req.method === 'GET' && pathOnly === '/health') {
    res.writeHead(200, { 'Content-Type': 'text/plain' });
    res.end('ok');
    done('model=-');
    return;
  }
  if (req.method !== 'POST') {
    res.writeHead(404, { 'Content-Type': 'text/plain' });
    res.end('not found');
    done('model=- status=404');
    return;
  }

  collect(req)
    .then((raw) => {
      let body;
      try {
        body = JSON.parse(raw || '{}');
      } catch {
        // 解析失败回 400,进程不崩
        sendJson(res, 400, { error: { message: 'invalid json body', type: 'invalid_request_error' } });
        done(`model=- status=400 invalid-json`);
        return;
      }
      const delay = Math.max(0, DELAY + (Math.random() * 2 - 1) * JIT); // delay±jitter
      setTimeout(() => {
        sendJson(res, 200, completionFor(body));
        done(`model=${body.model || '-'} status=200 sleep=${Math.round(delay)}`);
      }, delay);
    })
    .catch((e) => {
      try {
        sendJson(res, 400, { error: { message: String(e?.message || e).slice(0, 200) } });
      } catch {
        /* 连接已断 */
      }
      done(`model=- status=400 err=${String(e?.message || e).slice(0, 80)}`);
    });
});

// 半包/非法请求直接断开,不让进程挂掉
server.on('clientError', (_err, socket) => socket.destroy());
server.on('error', (e) => {
  line(`mock 上游监听失败: ${e.message}`);
  process.exit(1);
});

server.listen(PORT, '0.0.0.0', () => {
  line(`mock 上游就绪 0.0.0.0:${PORT} delay=${DELAY}ms±${JIT}ms (config: fakeDelayMs/fakeJitterMs)`);
});

process.on('SIGINT', () => {
  line('收到 SIGINT,停止接收新连接并等待在途请求');
  server.closeIdleConnections?.();
  server.close(() => {
    line('已优雅退出');
    process.exit(0);
  });
  // keep-alive 连接可能迟迟不关:3s 兜底退出
  setTimeout(() => process.exit(0), 3000).unref();
});
