#!/usr/bin/env node
/**
 * 讯飞 IFASR 离线转写的端侧（客户端）联调脚本。
 *
 * 它只从客户端视角调用网关的 /v1/audio/transcriptions，覆盖三种音频来源
 * （multipart 文件 / Base64 扩展字段 / HTTP(S) URL 扩展字段）与三种响应格式，
 * 用来验证「配置好渠道后，客户端确实能拿到转写结果」。
 *
 * 用法（凭据只从环境变量或参数读，不落盘）:
 *   node asr-client.mjs --base http://localhost:3000 --token "$ASR_TOKEN" \
 *     --audio ./sample.wav --source multipart --format verbose_json
 *
 * 参数:
 *   --base    网关地址，默认 http://localhost:3000
 *   --token   用户令牌（也可用环境变量 ASR_TOKEN）
 *   --model   模型名，默认 ifasr-llm-v1
 *   --audio   本地音频文件路径（multipart 与 base64 来源必填）
 *   --url     HTTP(S) 音频地址（--source url 时必填）
 *   --source  multipart（默认）| base64 | url
 *   --format  json（默认）| verbose_json | text
 *   --expect  期望响应文本包含的子串；命中即判定通过
 *
 * 退出码: 0 通过 / 1 失败 / 2 参数或环境错误
 */

import fs from 'node:fs'
import path from 'node:path'

const DEFAULT_BASE = 'http://localhost:3000'
const DEFAULT_MODEL = 'ifasr-llm-v1'
const DEFAULT_SOURCE = 'multipart'
const DEFAULT_FORMAT = 'json'

const MIME_BY_EXT = {
  '.wav': 'audio/wav',
  '.mp3': 'audio/mpeg',
  '.flac': 'audio/flac',
  '.m4a': 'audio/mp4',
  '.ogg': 'audio/ogg',
  '.opus': 'audio/opus',
  '.aac': 'audio/aac',
  '.aiff': 'audio/aiff',
}

function parseArgs(argv) {
  const args = {}
  for (let i = 0; i < argv.length; i += 1) {
    const token = argv[i]
    if (!token.startsWith('--')) {
      throw new Error(`无法识别的参数: ${token}`)
    }
    const name = token.slice(2)
    const value = argv[i + 1]
    if (value === undefined || value.startsWith('--')) {
      throw new Error(`参数 --${name} 缺少取值`)
    }
    args[name] = value
    i += 1
  }
  return args
}

function fail(message) {
  console.error(`✗ ${message}`)
  process.exit(1)
}

function readAudio(filePath) {
  if (!filePath) {
    throw new Error('multipart 与 base64 来源需要 --audio 指定音频文件')
  }
  const resolved = path.resolve(filePath)
  if (!fs.existsSync(resolved)) {
    throw new Error(`音频文件不存在: ${resolved}`)
  }
  const data = fs.readFileSync(resolved)
  if (data.length === 0) {
    throw new Error(`音频文件为空: ${resolved}`)
  }
  const ext = path.extname(resolved).toLowerCase()
  return {
    resolved,
    data,
    fileName: path.basename(resolved),
    mimeType: MIME_BY_EXT[ext] || 'application/octet-stream',
    ext,
  }
}

function buildMultipartBody(audio, fields) {
  const boundary = `----asr-e2e-${Date.now().toString(16)}`
  const chunks = []
  const pushText = (text) => chunks.push(Buffer.from(text, 'utf8'))

  for (const [name, value] of Object.entries(fields)) {
    pushText(`--${boundary}\r\n`)
    pushText(`Content-Disposition: form-data; name="${name}"\r\n\r\n`)
    pushText(`${value}\r\n`)
  }

  pushText(`--${boundary}\r\n`)
  pushText(
    `Content-Disposition: form-data; name="file"; filename="${audio.fileName}"\r\n`
  )
  pushText(`Content-Type: ${audio.mimeType}\r\n\r\n`)
  chunks.push(audio.data)
  pushText('\r\n')
  pushText(`--${boundary}--\r\n`)

  return {
    body: Buffer.concat(chunks),
    contentType: `multipart/form-data; boundary=${boundary}`,
  }
}

function buildJSONBody(audio, fields) {
  const payload = { ...fields }
  if (audio) {
    // 与适配器约定的扩展字段一致：data URI 前缀带上 MIME 子类型，供网关推断格式。
    payload.audio_base64 = `data:${audio.mimeType};base64,${audio.data.toString('base64')}`
  }
  return {
    body: Buffer.from(JSON.stringify(payload), 'utf8'),
    contentType: 'application/json',
  }
}

function summarizeJSON(body) {
  const parsed = JSON.parse(body)
  const summary = []
  if (typeof parsed.text === 'string') {
    summary.push(`text ${parsed.text.length} 字`)
    summary.push(`首 40 字: ${parsed.text.slice(0, 40)}`)
  }
  if (typeof parsed.duration === 'number') {
    summary.push(`duration ${parsed.duration}s`)
  }
  if (Array.isArray(parsed.segments)) {
    summary.push(`segments ${parsed.segments.length} 段`)
    const first = parsed.segments[0]
    if (first) {
      summary.push(
        `首段 [${first.start}s, ${first.end}s] ${String(first.text).slice(0, 20)}`
      )
    }
    const last = parsed.segments[parsed.segments.length - 1]
    if (last && last !== first) {
      summary.push(`末段 [${last.start}s, ${last.end}s]`)
    }
  }
  if (typeof parsed.language === 'string') {
    summary.push(`language ${parsed.language === '' ? '(空)' : parsed.language}`)
  }
  return summary
}

async function main() {
  let args
  try {
    args = parseArgs(process.argv.slice(2))
  } catch (error) {
    console.error(`✗ ${error.message}`)
    process.exit(2)
  }

  const base = (args.base || DEFAULT_BASE).replace(/\/+$/, '')
  const token = args.token || process.env.ASR_TOKEN
  const model = args.model || DEFAULT_MODEL
  const source = args.source || DEFAULT_SOURCE
  const format = args.format || DEFAULT_FORMAT

  if (!token) {
    console.error('✗ 缺少令牌：用 --token 或环境变量 ASR_TOKEN 提供')
    process.exit(2)
  }
  if (!['multipart', 'base64', 'url'].includes(source)) {
    console.error(`✗ 不支持的 --source: ${source}`)
    process.exit(2)
  }
  if (!['json', 'verbose_json', 'text'].includes(format)) {
    console.error(`✗ 不支持的 --format: ${format}`)
    process.exit(2)
  }

  const fields = { model, response_format: format }
  let audio = null
  let requestBody
  let contentType

  if (source === 'multipart') {
    try {
      audio = readAudio(args.audio)
    } catch (error) {
      console.error(`✗ ${error.message}`)
      process.exit(2)
    }
    const built = buildMultipartBody(audio, fields)
    requestBody = built.body
    contentType = built.contentType
  } else {
    if (source === 'base64') {
      try {
        audio = readAudio(args.audio)
      } catch (error) {
        console.error(`✗ ${error.message}`)
        process.exit(2)
      }
    } else {
      if (!args.url) {
        console.error('✗ url 来源需要 --url 指定音频地址')
        process.exit(2)
      }
      fields.audio_url = args.url
    }
    const built = buildJSONBody(audio, fields)
    requestBody = built.body
    contentType = built.contentType
  }

  const endpoint = `${base}/v1/audio/transcriptions`
  console.log(`→ POST ${endpoint}`)
  console.log(
    `  来源 ${source} / 格式 ${format} / 模型 ${model}` +
      (audio ? ` / 音频 ${audio.fileName} (${audio.data.length} 字节)` : '')
  )

  const startedAt = Date.now()
  let response
  try {
    response = await fetch(endpoint, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': contentType,
      },
      body: requestBody,
    })
  } catch (error) {
    fail(`请求失败: ${error.message}`)
  }
  const elapsed = Date.now() - startedAt
  const body = await response.text()

  console.log(`← ${response.status} (${elapsed}ms)`)
  console.log(`  Content-Type: ${response.headers.get('content-type') || '(无)'}`)

  if (!response.ok) {
    fail(`网关返回非 2xx: ${body.slice(0, 600)}`)
  }

  let text = ''
  if (format === 'text') {
    text = body
    console.log(`  纯文本响应 ${body.length} 字`)
    console.log(`  首 40 字: ${body.slice(0, 40)}`)
  } else {
    let parsed
    try {
      parsed = JSON.parse(body)
    } catch {
      fail(`响应不是合法 JSON: ${body.slice(0, 300)}`)
    }
    text = typeof parsed.text === 'string' ? parsed.text : ''
    for (const line of summarizeJSON(body)) {
      console.log(`  ${line}`)
    }
    if (text === '') {
      fail('响应缺少 text 字段（或为空）')
    }
  }

  if (args.expect && !text.includes(args.expect)) {
    fail(`响应文本未包含期望子串: ${args.expect}`)
  }

  console.log('✓ 通过')
}

main()
