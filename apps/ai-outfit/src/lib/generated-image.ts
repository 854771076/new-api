import 'server-only';
import { lookup } from 'node:dns/promises';
import { request } from 'node:https';
import { BlockList, isIP } from 'node:net';
import sharp from 'sharp';

const MAX_IMAGE_BYTES = 20 * 1024 * 1024;
const blockedAddresses = new BlockList();
for (const [network, prefix] of [
  ['0.0.0.0', 8], ['10.0.0.0', 8], ['100.64.0.0', 10], ['127.0.0.0', 8],
  ['169.254.0.0', 16], ['172.16.0.0', 12], ['192.0.0.0', 24], ['192.0.2.0', 24],
  ['192.168.0.0', 16], ['198.18.0.0', 15], ['198.51.100.0', 24], ['203.0.113.0', 24],
  ['224.0.0.0', 4], ['240.0.0.0', 4],
] as const) blockedAddresses.addSubnet(network, prefix, 'ipv4');
for (const [network, prefix] of [
  ['::', 128], ['::1', 128], ['64:ff9b::', 96],
  ['100::', 64], ['2001::', 32], ['2001:db8::', 32], ['2002::', 16],
  ['fc00::', 7], ['fe80::', 10], ['ff00::', 8],
] as const) blockedAddresses.addSubnet(network, prefix, 'ipv6');

export async function downloadImage(urlValue: string, redirects = 0): Promise<Buffer> {
  const url = new URL(urlValue);
  if (url.protocol !== 'https:' || url.username || url.password || (url.port && url.port !== '443')) {
    throw new Error('图片地址必须使用安全 HTTPS');
  }
  const hostname = url.hostname.replace(/^\[|\]$/g, '');
  const addresses = isIP(hostname)
    ? [{ address: hostname, family: isIP(hostname) }]
    : await lookup(hostname, { all: true });
  if (!addresses.length || addresses.some(({ address, family }) =>
    (family === 6 && address.toLowerCase().startsWith('::ffff:')) ||
    blockedAddresses.check(address, family === 6 ? 'ipv6' : 'ipv4'),
  )) throw new Error('图片地址不可访问');

  const selected = addresses[0];
  const response = await new Promise<{ bytes?: Buffer; redirect?: string }>((resolve, reject) => {
    const req = request(url, {
      // Pin the validated address so DNS cannot change between validation and connection.
      lookup: (_host, options, callback) => {
        if (options.all) callback(null, [selected]);
        else callback(null, selected.address, selected.family);
      },
      signal: AbortSignal.timeout(30_000),
      headers: { Accept: 'image/png,image/jpeg,image/webp' },
    }, (res) => {
      if (res.statusCode && [301, 302, 303, 307, 308].includes(res.statusCode)) {
        const location = res.headers.location;
        res.resume();
        if (!location || redirects >= 3) return reject(new Error('图片重定向无效'));
        resolve({ redirect: new URL(location, url).href });
        return;
      }
      if (res.statusCode !== 200) {
        res.resume();
        reject(new Error('无法下载生成图片'));
        return;
      }
      if (Number(res.headers['content-length']) > MAX_IMAGE_BYTES) {
        res.destroy();
        reject(new Error('图片超过 20MB 限制'));
        return;
      }
      const chunks: Buffer[] = [];
      let length = 0;
      res.on('data', (chunk: Buffer) => {
        length += chunk.length;
        if (length > MAX_IMAGE_BYTES) {
          res.destroy(new Error('图片超过 20MB 限制'));
          return;
        }
        chunks.push(chunk);
      });
      res.on('error', reject);
      res.on('end', () => resolve({ bytes: Buffer.concat(chunks) }));
    });
    req.on('error', reject);
    req.setTimeout(30_000, () => req.destroy(new Error('图片下载超时')));
    req.end();
  });
  if (response.redirect) return downloadImage(response.redirect, redirects + 1);
  return response.bytes!;
}

export async function readGeneratedImage(image: { url?: string; b64_json?: string }): Promise<Buffer> {
  let bytes: Buffer;
  if (image.b64_json) {
    if (image.b64_json.length > Math.ceil(MAX_IMAGE_BYTES / 3) * 4 || !/^[A-Za-z0-9+/]+={0,2}$/.test(image.b64_json)) {
      throw new Error('生成图片数据无效或过大');
    }
    bytes = Buffer.from(image.b64_json, 'base64');
  } else if (image.url) {
    bytes = await downloadImage(image.url);
  } else {
    throw new Error('网关未返回图片数据');
  }
  if (!bytes.length || bytes.length > MAX_IMAGE_BYTES) throw new Error('生成图片数据无效或过大');
  const pipeline = sharp(bytes, { limitInputPixels: 40_000_000, animated: false });
  const metadata = await pipeline.metadata();
  if (!['png', 'jpeg', 'webp'].includes(metadata.format ?? '')) throw new Error('生成图片格式不支持');
  const png = await pipeline.rotate().png().toBuffer();
  if (png.length > MAX_IMAGE_BYTES) throw new Error('生成图片超过 20MB 限制');
  return png;
}
