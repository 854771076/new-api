#!/usr/bin/env node
/**
 * 讯飞 IFASR 离线转写的端侧（客户端）联调脚本。
 *
 * 同步模式只从客户端视角调用网关的 /v1/audio/transcriptions，覆盖三种音频来源
 * （multipart 文件 / Base64 扩展字段 / HTTP(S) URL 扩展字段）与三种响应格式，
 * 用来验证「配置好渠道后，客户端确实能拿到转写结果」。
 *
 * 异步模式调用网关的任务端点：POST /v1/audio/transcriptions/tasks 提交后拿任务号，
 * 再轮询 GET /v1/audio/transcriptions/tasks/:task_id 直到终态，用来验证
 * 「转写被托管成 new-api 的异步任务，客户端可以自己取回结果」。
 *
 * 用法（凭据只从环境变量或参数读，不落盘）:
 *   node asr-client.mjs --base http://localhost:3000 --token "$ASR_TOKEN" \
 *     --audio ./sample.wav --source multipart --format verbose_json
 *
 *   node asr-client.mjs --mode async --base http://localhost:3000 \
 *     --token "$ASR_TOKEN" --audio ./sample.wav
 *
 * 参数:
 *   --base    网关地址，默认 http://localhost:3000
 *   --token   用户令牌（也可用环境变量 ASR_TOKEN）
 *   --model   模型名，默认 ifasr-llm-v1
 *   --audio   本地音频文件路径（multipart 与 base64 来源必填；异步模式必填）
 *   --url     HTTP(S) 音频地址（--source url 时必填）
 *   --source  multipart（默认）| base64 | url（异步模式只接受 multipart）
 *   --format  json（默认）| verbose_json | text（异步模式只接受 json 语义）
 *   --mode    sync（默认）| async
 *   --expect  期望响应文本包含的子串；命中即判定通过
 *   --poll-interval  异步模式轮询间隔（毫秒），默认 3000
 *   --timeout  异步模式等待终态的超时（秒），默认 300
 *   --submit-timeout  异步模式提交阶段的超时（秒），默认 120。超时后不要盲目
 *                     重投：任务可能已被受理并正在转写，重复提交会重复计费
 *
 * 退出码: 0 通过 / 1 断言失败 / 2 参数或环境不合法 / 3 跳过（未提供令牌）
 *
 * 代理: Node 内置 fetch 默认直连，不读 HTTP_PROXY 等环境变量，内网网关地址
 *       可直接使用。若环境里设了 NODE_USE_ENV_PROXY=1（Node 24 起支持），
 *       fetch 会改走代理，内网域名解析不了，表现为提交失败——去掉该变量即可。
 */

import fs from 'node:fs'
import path from 'node:path'

const DEFAULT_BASE = 'http://localhost:3000'
const DEFAULT_MODEL = 'ifasr-llm-v1'
const DEFAULT_SOURCE = 'multipart'
const DEFAULT_FORMAT = 'json'
const DEFAULT_MODE = 'sync'
const DEFAULT_POLL_INTERVAL_MS = 3000
const DEFAULT_TIMEOUT_SECONDS = 300
// 提交阶段的独立超时。网关收下请求后可能迟迟不答复（例如上游转写慢或链路
// 抖动），没有这个上限时 fetch 会一直挂着，表现为脚本「卡住」且看不出卡在哪。
const DEFAULT_SUBMIT_TIMEOUT_SECONDS = 120
// 退出码：0 通过 / 1 断言失败 / 2 参数或环境不合法 / 3 跳过（未提供令牌）。
// 跳过与参数错误分开，CI 才能把「没配凭据」和「参数写错」区分开。
const EXIT_FAIL = 1
const EXIT_USAGE = 2
const EXIT_SKIP = 3

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

function sleep(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds))
}

// fetchWithTimeout 给单次请求加上上限。超时抛出的错误带 timeout 标记，
// 调用方据此区分「网络不通」与「网关收下了但迟迟不答复」。
async function fetchWithTimeout(url, options, timeoutMs, stage) {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  try {
    return await fetch(url, { ...options, signal: controller.signal })
  } catch (error) {
    if (error.name === 'AbortError') {
      const failure = new Error(`${stage}超时（${Math.round(timeoutMs / 1000)}s 无响应）`)
      failure.timeout = true
      failure.stage = stage
      throw failure
    }
    throw error
  } finally {
    clearTimeout(timer)
  }
}

// 异步模式：提交任务 → 轮询到终态 → 打印转写结果。
// 任务链路只接受 multipart 上传，因此这里不提供 Base64 / URL 来源。
async function runAsync({ base, token, model, audio, expect, pollIntervalMs, timeoutMs, submitTimeoutMs }) {
  const endpoint = `${base}/v1/audio/transcriptions/tasks`
  const built = buildMultipartBody(audio, { model })

  console.log(`→ POST ${endpoint}`)
  console.log(`  模式 async / 模型 ${model} / 音频 ${audio.fileName} (${audio.data.length} 字节)`)

  const startedAt = Date.now()
  let response
  try {
    response = await fetchWithTimeout(
      endpoint,
      {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${token}`,
          'Content-Type': built.contentType,
        },
        body: built.body,
      },
      submitTimeoutMs,
      '提交',
    )
  } catch (error) {
    if (error.timeout) {
      // 提交超时的处置与其它失败不同：请求可能已被网关受理并开始转写，
      // 盲目重投会产生重复任务并重复计费。
      console.error(
        `✗ ${error.message}\n` +
          '  网关可能已经受理并正在转写——请先到任务日志确认是否已产生任务，\n' +
          '  确认没有再重投；重复提交会重复计费。',
      )
      process.exit(1)
    }
    fail(`请求失败: ${error.message}`)
  }
  const body = await response.text()
  console.log(`← ${response.status} (${Date.now() - startedAt}ms)`)
  if (!response.ok) {
    fail(`网关返回非 2xx: ${body.slice(0, 600)}`)
  }

  let submitted
  try {
    submitted = JSON.parse(body)
  } catch {
    fail(`提交响应不是合法 JSON: ${body.slice(0, 300)}`)
  }
  const taskId = submitted.id || submitted.task_id
  if (!taskId) {
    fail(`提交响应缺少任务号: ${body.slice(0, 300)}`)
  }
  console.log(`  任务号 ${taskId} / 初始状态 ${submitted.status || '(未知)'}`)

  const pollEndpoint = `${base}/v1/audio/transcriptions/tasks/${encodeURIComponent(taskId)}`
  const deadline = Date.now() + timeoutMs
  console.log(`  轮询中（上限 ${Math.round(timeoutMs / 1000)}s，间隔 ${Math.round(pollIntervalMs / 1000)}s）…`)
  while (Date.now() < deadline) {
    await sleep(pollIntervalMs)
    let poll
    try {
      poll = await fetchWithTimeout(
        pollEndpoint,
        { headers: { Authorization: `Bearer ${token}` } },
        30_000,
        '查询',
      )
    } catch (error) {
      if (error.timeout) {
        // 任务已提交，只是这一次查询没等到答复；重投会重复计费。
        console.error(
          `✗ ${error.message}\n` +
            `  任务 ${taskId} 已提交，可用同一令牌稍后继续查询该任务号，不必重新提交。`,
        )
        process.exit(1)
      }
      fail(`查询任务失败: ${error.message}`)
    }
    const pollBody = await poll.text()
    if (!poll.ok) {
      fail(`查询任务返回非 2xx: ${pollBody.slice(0, 600)}`)
    }
    let parsed
    try {
      parsed = JSON.parse(pollBody)
    } catch {
      fail(`任务响应不是合法 JSON: ${pollBody.slice(0, 300)}`)
    }
    const status = String(parsed.status || '')
    console.log(`  状态 ${status}（${Math.round((Date.now() - startedAt) / 1000)}s）`)
    if (status === 'failed' || status === 'cancelled') {
      const reason =
        (parsed.error && (parsed.error.message || parsed.error.code)) || '(未给出原因)'
      fail(`任务失败: ${reason}`)
    }
    if (status === 'succeeded') {
      const text = typeof parsed.text === 'string' ? parsed.text : ''
      const segments = Array.isArray(parsed.segments) ? parsed.segments.length : 0
      console.log(`  duration ${parsed.duration}s / segments ${segments} 段 / text ${text.length} 字`)
      console.log(`  首 40 字: ${text.slice(0, 40)}`)
      console.log(`  总耗时 ${Math.round((Date.now() - startedAt) / 1000)}s`)
      if (text === '') {
        fail('任务成功但文本为空')
      }
      if (expect && !text.includes(expect)) {
        fail(`响应文本未包含期望子串: ${expect}`)
      }
      console.log('✓ 通过')
      return
    }
  }
  fail(`等待任务终态超时（${Math.round(timeoutMs / 1000)}s），任务号 ${taskId}`)
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
  const mode = args.mode || DEFAULT_MODE

  if (!token) {
    // 没有凭据就不跑：这是「跳过」，不是「失败」，也不是「参数错误」。
    // 单独的退出码让 CI 不会把拼错的参数当成跳过而放过。
    console.log('- 跳过：未提供令牌（--token 或环境变量 ASR_TOKEN）')
    process.exit(EXIT_SKIP)
  }
  if (!['sync', 'async'].includes(mode)) {
    console.error(`✗ 不支持的 --mode: ${mode}`)
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

  if (mode === 'async') {
    // 异步端点只接受 multipart 上传，且总是返回 JSON 语义的转写对象。
    // 其它组合在这里直接拒绝，避免「脚本跑通了但其实没覆盖到目标路径」。
    if (source !== 'multipart') {
      console.error(`✗ 异步模式只支持 --source multipart，收到: ${source}`)
      process.exit(2)
    }
    if (format !== 'json') {
      console.error(`✗ 异步模式只支持 --format json，收到: ${format}`)
      process.exit(2)
    }
    let audio
    try {
      audio = readAudio(args.audio)
    } catch (error) {
      console.error(`✗ ${error.message}`)
      process.exit(2)
    }
    const pollIntervalMs = Number(args['poll-interval'] || DEFAULT_POLL_INTERVAL_MS)
    const timeoutMs = Number(args.timeout || DEFAULT_TIMEOUT_SECONDS) * 1000
    const submitTimeoutMs = Number(args['submit-timeout'] || DEFAULT_SUBMIT_TIMEOUT_SECONDS) * 1000
    if (!Number.isFinite(pollIntervalMs) || pollIntervalMs <= 0) {
      console.error('✗ --poll-interval 必须是正数（毫秒）')
      process.exit(2)
    }
    if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
      console.error('✗ --timeout 必须是正数（秒）')
      process.exit(2)
    }
    if (!Number.isFinite(submitTimeoutMs) || submitTimeoutMs <= 0) {
      console.error('✗ --submit-timeout 必须是正数（秒）')
      process.exit(2)
    }
    await runAsync({
      base,
      token,
      model,
      audio,
      expect: args.expect,
      pollIntervalMs,
      timeoutMs,
      submitTimeoutMs,
    })
    return
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
