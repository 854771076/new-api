import { describe, expect, mock, test } from 'bun:test';
import sharp from 'sharp';

mock.module('server-only', () => ({}));
const { readGeneratedImage, downloadImage } = await import('./generated-image.ts');

describe('generated image storage', () => {
  test('decodes base64 results and writes a valid PNG', async () => {
    const source = await sharp({ create: { width: 2, height: 3, channels: 3, background: '#336699' } }).jpeg().toBuffer();
    const png = await readGeneratedImage({ b64_json: source.toString('base64') });
    const metadata = await sharp(png).metadata();
    expect(metadata.format).toBe('png');
    expect(metadata.width).toBe(2);
    expect(metadata.height).toBe(3);
  });

  test('rejects missing, invalid, oversized and unsupported images', async () => {
    await expect(readGeneratedImage({})).rejects.toThrow();
    await expect(readGeneratedImage({ b64_json: '<script>' })).rejects.toThrow();
    await expect(readGeneratedImage({ b64_json: 'A'.repeat(28_000_000) })).rejects.toThrow();
    const tiff = await sharp({ create: { width: 1, height: 1, channels: 3, background: '#ffffff' } }).tiff().toBuffer();
    await expect(readGeneratedImage({ b64_json: tiff.toString('base64') })).rejects.toThrow('格式');
  });

  test.each([
    'http://example.com/image.png', 'https://user:password@example.com/image.png',
    'https://example.com:444/image.png', 'https://127.0.0.1/image.png',
    'https://169.254.169.254/image.png', 'https://10.0.0.1/image.png',
    'https://[::1]/image.png', 'https://[::ffff:127.0.0.1]/image.png',
  ])('blocks unsafe remote image addresses: %s', async (url) => {
    await expect(downloadImage(url)).rejects.toThrow();
  });
});
