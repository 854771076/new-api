// delay-proxy.mjs — 本地延迟注入代理:先等 DELAY_MS 再转发到上游,用于制造渠道间真实延迟差
// 用法: DELAY_MS=1000 PORT=3099 node concurrency-test/delay-proxy.mjs
import http from 'node:http';

const UP = {
  host: process.env.UPSTREAM_HOST || 'upstream.example.internal',
  port: Number(process.env.UPSTREAM_PORT || 30000),
};
const DELAY = Number(process.env.DELAY_MS || 1000);
const PORT = Number(process.env.PORT || 3099);

http.createServer((req, res) => {
  const chunks = [];
  req.on('data', (c) => chunks.push(c));
  req.on('end', () => {
    const body = Buffer.concat(chunks);
    setTimeout(() => {
      const up = http.request({
        ...UP,
        path: req.url,
        method: req.method,
        headers: { ...req.headers, host: `${UP.host}:${UP.port}` },
      }, (ur) => {
        res.writeHead(ur.statusCode, ur.headers);
        ur.pipe(res);
      });
      up.on('error', (e) => {
        res.writeHead(502, { 'content-type': 'application/json' });
        res.end(JSON.stringify({ error: { message: 'delay-proxy upstream error: ' + e.message } }));
      });
      if (body.length) up.write(body);
      up.end();
    }, DELAY);
  });
}).listen(PORT, '127.0.0.1', () => console.log(`delay-proxy :${PORT} -> ${UP.host}:${UP.port} (+${DELAY}ms)`));
