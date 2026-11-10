// lib/prompt.ts — 提示词模板渲染与内置默认模板。

export const DEFAULT_OUTFIT_TEMPLATE =
  '以参考图中的人物为唯一主体，保持其面部特征、发型、肤色和体型完全不变。服装替换为：{{outfits}}。姿势参考：{{pose}}。{{extra}} 输出高质量写实摄影风格，光线柔和自然，比例正确，不要出现多余肢体。';

export const DEFAULT_NEGATIVE_PROMPT = '不要改变人物脸型和五官，不要多余的肢体，不要文字和水印';

const PLACEHOLDER_RE = /\{\{\s*([A-Za-z0-9_]+)\s*\}\}/g;

/**
 * 渲染 {{key}} 占位符。未提供的变量替换为空字符串；
 * 渲染后清理残留的花括号，并把连续空格压成一个。
 */
export function renderPrompt(template: string, vars: Record<string, string>): string {
  return template
    .replace(PLACEHOLDER_RE, (_match, key: string) => vars[key] ?? '')
    .replace(/\{\{|\}\}/g, '')
    .replace(/ {2,}/g, ' ');
}
