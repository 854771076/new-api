// 讯飞 IFASR（录音文件转写大模型）任务插件。
//
// 上游是两段式异步接口：先把音频原始字节 POST 到 /v2/upload 换取订单号，再轮询
// /v2/getResult 直到订单进入终态。宿主负责任务落库、轮询调度与结算，本文件只描述
// 请求形态、签名与结果解析。
//
// 已知限制：
// - 异步端点只接受 multipart 的 file 字段；Base64 / URL 两种音频来源仍由同步端点
//   /v1/audio/transcriptions 提供。
// - 插件拿不到文件字节，时长由宿主在收包时测量后随文件元数据下发（media-duration@1
//   能力）；宿主测不到的格式（pcm、speex、webm 等）由宿主给出可计费上限，结算时
//   由上游回传的真实时长替换。
// - 本地时长解析与格式支持（webm 等）由同步端点的来源解析层承担；异步路径把文件
//   原样交给上游，由上游判定格式是否可接受。
// - 上游允许 5 小时音频，但宿主对 duration 用量事实有 1 小时上限校验；超限的结算
//   事实会被宿主丢弃并保留提交期的预扣值。这是宿主侧限制，插件不做规避。
// - 说话人依赖上游的角色分离：客户端用 multipart 表单字段 roleType（可选 roleNum）
//   请求，插件把它们作为上游查询参数透传。未开启时上游不返回 rl/spk，转写文本里
//   只有时间戳、没有说话人。
// - 结算时长只认上游回传的真实时长（realDuration）。上游未回传时插件不产出时长
//   事实，由宿主保留提交期的预扣值——宿主对测不出长度的格式给的是可计费上限，
//   不是实测长度。

const SUCCESS_CODE = "000000";
const ORDER_NOT_FINISHED_CODE = "100013";
// RATE_LIMIT_CODE 是上游的频率限制：退避后重试即可恢复，属于可自愈的瞬时状态。
const RATE_LIMIT_CODE = "100012";
const UPLOAD_PATH = "/v2/upload";
const RESULT_PATH = "/v2/getResult";
// language 只接受 autodialect / autominor。autodialect 支持中英与方言免切识别；
// autominor 需要单独申请权限，因此不做默认。
const DEFAULT_LANGUAGE = "autodialect";
const SIGNATURE_RANDOM_LENGTH = 16;
const SIGNATURE_RANDOM_ALPHABET = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
const SUPPORTED_AUDIO_EXTENSIONS = {
  "audio/mpeg": "mp3",
  "audio/mp3": "mp3",
  "audio/wav": "wav",
  "audio/wave": "wav",
  "audio/x-wav": "wav",
  "audio/ogg": "ogg",
  "audio/flac": "flac",
  "audio/x-flac": "flac",
  "audio/opus": "opus",
  "audio/speex": "speex",
  "audio/pcm": "pcm",
  "audio/x-pcm": "pcm",
};

export const meta = {
  apiVersion: 1,
  key: "xfyun-asr",
  name: "iFLYTEK IFASR",
  description: {
    en: "Audio file transcription via iFLYTEK",
    zh: "通过讯飞转写音频文件",
  },
  version: "1.1.0",
  author: { name: "QuantumNous" },
  baseUrl: "https://office-api-ist-dx.iflyaisol.com",
  models: ["ifasr-llm-v1"],
  fetchMode: "per_task",
  // openai_audio 没有模式：create 只提交任务，retrieve 由宿主用持久化结果调用 render。
  protocols: [{ name: "openai_audio" }],
  // 按时长计费需要宿主在收包时测量上传音频的长度，否则提交阶段无法预扣。
  requiredCapabilities: ["media-duration@1"],
  // 渠道密钥是三段式 appId|apiSecret|accessKeyId，插件自己切分。
  auth: "api_key",
  usageSchema: {
    // 时长事实来自上游回传的真实时长（毫秒，realDuration 优先），换算成秒后按单价计价。
    duration: {
      type: "number",
      unit: "second",
      description: { en: "Audio transcription unit price", zh: "音频转写单价" },
    },
  },
  usageExamples: [
    { label: "60s", facts: { duration: 60 } },
    { label: "5min", facts: { duration: 300 } },
  ],
};

function trimmed(value) {
  if (value === undefined || value === null) return "";
  return String(value).trim();
}

// 官方签名要求与查询串使用同一套 URL 编码：unreserved 只保留 A-Za-z0-9 - _ . ~。
// encodeURIComponent 会保留 ! ' ( ) *，必须补编码；否则签名基础串与查询串不一致，
// 上游返回 100009（签名校验不通过）。
function encodeComponent(value) {
  return encodeURIComponent(String(value)).replace(/[!'()*]/g, function (character) {
    return "%" + character.charCodeAt(0).toString(16).toUpperCase();
  });
}

// 参与签名与查询串的是同一组键值对：排除 signature、过滤空值、按参数名排序。
// 签名基础串与最终 URL 都从本函数生成，保证两者逐字节一致。
function signedPairs(params) {
  const pairs = [];
  const keys = Object.keys(params).sort();
  for (const key of keys) {
    if (key === "signature") continue;
    const value = params[key];
    if (value === undefined || value === null || String(value) === "") continue;
    pairs.push(encodeComponent(key) + "=" + encodeComponent(value));
  }
  return pairs;
}

function queryString(params) {
  return signedPairs(params).join("&");
}

function signatureOf(params, secret) {
  return utils.hmacSHA1Base64(signedPairs(params).join("&"), secret);
}

// 16 位大小写字母 + 数字。用宿主 UUID（crypto/rand）作为随机源，成对取十六进制，
// 丢弃 248 以上的值以避免取模偏置。
function newSignatureRandom() {
  let result = "";
  while (result.length < SIGNATURE_RANDOM_LENGTH) {
    const hex = String(utils.uuid()).replace(/-/g, "");
    for (let index = 0; index + 1 < hex.length && result.length < SIGNATURE_RANDOM_LENGTH; index += 2) {
      const value = parseInt(hex.slice(index, index + 2), 16);
      if (value < 248) result += SIGNATURE_RANDOM_ALPHABET.charAt(value % SIGNATURE_RANDOM_ALPHABET.length);
    }
  }
  return result;
}

function pad2(value) {
  return value < 10 ? "0" + value : String(value);
}

function pad3(value) {
  return value < 10 ? "00" + value : value < 100 ? "0" + value : String(value);
}

// 官方格式 yyyy-MM-dd'T'HH:mm:ss±HHmm，东八区偏移不带冒号。用 UTC getter 加 8 小时
// 得到北京时间，避免宿主运行时本地时区影响。
function requestDateTime() {
  const beijing = new Date(utils.unixNow() * 1000 + 8 * 60 * 60 * 1000);
  return (
    beijing.getUTCFullYear() +
    "-" +
    pad2(beijing.getUTCMonth() + 1) +
    "-" +
    pad2(beijing.getUTCDate()) +
    "T" +
    pad2(beijing.getUTCHours()) +
    ":" +
    pad2(beijing.getUTCMinutes()) +
    ":" +
    pad2(beijing.getUTCSeconds()) +
    "+0800"
  );
}

// 渠道密钥格式固定为 appId|apiSecret|accessKeyId；accessKeyId 即讯飞的 APIKey。
// 在提交前切分并报错，避免带着残缺凭据去请求上游。
function credentialsFrom(apiKey) {
  const parts = String(apiKey === undefined || apiKey === null ? "" : apiKey).split("|");
  if (parts.length !== 3 || !trimmed(parts[0]) || !trimmed(parts[1]) || !trimmed(parts[2])) {
    throw new Error("channel key must be appId|apiSecret|accessKeyId");
  }
  return { appId: trimmed(parts[0]), apiSecret: trimmed(parts[1]), accessKeyId: trimmed(parts[2]) };
}

// 上游按 fileName 的后缀判断音频格式；客户端文件名缺后缀时用 MIME 类型补一个
// 上游支持的扩展名，其余情况原样交给上游判定。
function uploadFileName(filename, mimeType) {
  const name = trimmed(filename) || "audio";
  if (/\.[A-Za-z0-9]+$/.test(name)) return name;
  const normalizedType = String(mimeType || "")
    .toLowerCase()
    .split(";")[0]
    .trim();
  const extension = SUPPORTED_AUDIO_EXTENSIONS[normalizedType];
  return extension ? name + "." + extension : name;
}

// 官方成功码是字符串 "000000"，失败码常以数字给出（如 100001）。统一补零到 6 位，
// 使两种形态落到同一套判断上；-1 这类带符号的码保持原样。
function normalizedCode(value) {
  if (value === undefined || value === null) return "";
  let text = trimmed(value);
  if (text === "") return "";
  if (/^\d+$/.test(text) && text.length < 6) text = "000000".slice(text.length) + text;
  return text;
}

// 上游的毫秒值与说话人常以字符串承载。0 是合法起点，因此用 ok 标记区分「未取到」，
// 不能用 0 当哨兵。
function integerValue(value) {
  if (typeof value === "number") {
    return Number.isFinite(value) ? { value: Math.trunc(value), ok: true } : { value: 0, ok: false };
  }
  const text = trimmed(value);
  if (!/^-?\d+$/.test(text)) return { value: 0, ok: false };
  return { value: parseInt(text, 10), ok: true };
}

function orderInfoOf(body) {
  if (!body || typeof body !== "object") return null;
  const content = body.content;
  if (!content || typeof content !== "object") return null;
  const info = content.orderInfo;
  return info && typeof info === "object" ? info : null;
}

// content.orderResult 是 JSON 字符串，上游对反斜杠做了二次转义：外层 JSON 解码后
// 仍残留两个反斜杠。官方实现固定做一次全局还原（两个反斜杠换成一个，不迭代），
// 然后再解码。部分版本直接给对象，此时原样使用。
function decodeOrderResult(value) {
  if (value === undefined || value === null) return null;
  if (typeof value === "string") {
    const text = value.replace(/\\\\/g, "\\");
    try {
      return JSON.parse(text);
    } catch {
      return null;
    }
  }
  return typeof value === "object" ? value : null;
}

// lattice 是做了顺滑的识别结果；缺失或为空时回退到未做顺滑的 lattice2。
function chooseUnits(orderResult) {
  if (!orderResult || typeof orderResult !== "object") return [];
  if (Array.isArray(orderResult.lattice) && orderResult.lattice.length > 0) return orderResult.lattice;
  if (Array.isArray(orderResult.lattice2) && orderResult.lattice2.length > 0) return orderResult.lattice2;
  return [];
}

// lattice 的 json_1best 是字符串，lattice2 是对象。字符串形态先按原样解码，
// 失败时再做一次过度转义还原后重试，覆盖上游重复转义的响应。
function decodeSentence(payload) {
  if (typeof payload === "string") {
    try {
      return JSON.parse(payload);
    } catch {
      try {
        return JSON.parse(payload.replace(/\\\\/g, "\\"));
      } catch {
        return null;
      }
    }
  }
  return payload && typeof payload === "object" ? payload : null;
}

// 文本由每个词候选数组的第一个候选顺序拼接。
function sentenceText(sentence) {
  const st = sentence && typeof sentence === "object" ? sentence.st : null;
  if (!st || typeof st !== "object" || !Array.isArray(st.rt)) return "";
  let text = "";
  for (const rt of st.rt) {
    if (!rt || typeof rt !== "object" || !Array.isArray(rt.ws)) continue;
    for (const ws of rt.ws) {
      if (!ws || typeof ws !== "object" || !Array.isArray(ws.cw) || ws.cw.length === 0) continue;
      const candidate = ws.cw[0];
      if (candidate && typeof candidate === "object" && typeof candidate.w === "string") text += candidate.w;
    }
  }
  return text;
}

// 相邻单元的说话人变化时切新段；同一说话人内的时间取并集（起始最小、结束最新）。
// 时间与说话人的位置随响应版本不同：单元层级（bg/ed/rl，部分响应为 begin/end/spk）、
// json_1best 里 st 的同级（bg/ed/rl）、st 内部（bg/ed/rl）。按此优先级取值；
// 前三处与 Go 侧 ParseASRResult 一致，单元层级的 begin/end/spk 是按协议事实补充的。
function buildSegments(units) {
  const segments = [];
  let lastHasStart = false;
  let lastHasEnd = false;
  for (const unit of units) {
    if (!unit || typeof unit !== "object") continue;
    const sentence = decodeSentence(unit.json_1best);
    if (!sentence) continue;
    const st = sentence.st && typeof sentence.st === "object" ? sentence.st : {};

    // 起点与终点分别回退：上游只丢掉其中一个时间是常见形态，成对回退会让
    // 另一端停在 0，产出 end 为 0 的分段。
    let start = integerValue(unit.bg);
    let end = integerValue(unit.ed);
    if (!start.ok) start = integerValue(unit.begin);
    if (!end.ok) end = integerValue(unit.end);
    if (!start.ok) start = integerValue(sentence.bg);
    if (!end.ok) end = integerValue(sentence.ed);
    if (!start.ok) start = integerValue(st.bg);
    if (!end.ok) end = integerValue(st.ed);

    let speaker = "";
    // rl 是上游的角色编号，语义上就是说话人；spk 只在没有 rl 的响应里才当说话人用。
    for (const candidate of [unit.rl, sentence.rl, st.rl, unit.spk]) {
      speaker = trimmed(candidate);
      if (speaker !== "") break;
    }

    const text = sentenceText(sentence);
    if (segments.length > 0 && segments[segments.length - 1].speaker === speaker) {
      const last = segments[segments.length - 1];
      if (start.ok && (!lastHasStart || start.value < last.start)) {
        last.start = start.value;
        lastHasStart = true;
      }
      if (end.ok && (!lastHasEnd || end.value > last.end)) {
        last.end = end.value;
        lastHasEnd = true;
      }
      last.text += text;
      continue;
    }
    segments.push({ id: segments.length, start: start.value, end: end.value, text: text, speaker: speaker });
    lastHasStart = start.ok;
    lastHasEnd = end.ok;
  }

  // 去掉没有文本的空段后重新编号。
  const result = [];
  for (const segment of segments) {
    if (segment.text === "") continue;
    segment.id = result.length;
    result.push(segment);
  }
  return result;
}

// 展示用的时长事实。
//
// 上传时设置了 durationCheckDisable=true（插件拿不到文件字节，无法保证声明时长与实际一致），
// 此时上游不再回显声明值：实测 8 秒音频的 originalDuration 退化为 60000，真实长度只在
// realDuration 里。因此优先取 realDuration，缺失时回退 originalDuration。
//
// 回退值可能远大于真实长度，只用于展示与响应时长，不作为结算事实：结算只看
// authoritativeDurationMs。
function displayDurationMs(info) {
  if (!info) return null;
  const real = integerValue(info.realDuration);
  if (real.ok && real.value > 0) return real.value;
  const original = integerValue(info.originalDuration);
  if (original.ok && original.value > 0) return original.value;
  return null;
}

// 结算用的时长事实：只认上游回传的真实时长。
//
// originalDuration 在关闭时长校验时是退化值（实测 8 秒音频返回 60000），拿它结算会把
// 8 秒音频按 60 秒计费。上游没有回传真实时长时返回 null，由宿主保留提交期的预扣值。
function authoritativeDurationMs(info) {
  if (!info) return null;
  const real = integerValue(info.realDuration);
  if (real.ok && real.value > 0) return real.value;
  return null;
}

function summarizeResult(body) {
  const info = orderInfoOf(body);
  const content = body && typeof body === "object" && body.content && typeof body.content === "object" ? body.content : {};
  const segments = buildSegments(chooseUnits(decodeOrderResult(content.orderResult)));
  let text = "";
  for (const segment of segments) text += segment.text;
  const display = displayDurationMs(info);
  const authoritative = authoritativeDurationMs(info);
  const summary = {
    code: normalizedCode(body && body.code),
    descInfo: trimmed(body && body.descInfo),
    text: text,
    segments: segments,
    language: info ? trimmed(info.language) : "",
    billedDuration: display === null ? 0 : display,
  };
  // 权威时长单独落库：结算只认它，billedDuration 只是展示值。缺失时字段整体不写，
  // 空值会让结算把「没取到」当成「取到了 0」。
  if (authoritative !== null) summary.realDurationMs = authoritative;
  return summary;
}

// 订单异常的 failType 语义来自讯飞 IFASR 接口文档，与 Go 侧 asrFailTypeDescriptions
// 覆盖同一组取值。
const FAIL_TYPE_REASONS = {
  1: "upload failed",
  2: "transcoding failed",
  3: "recognition failed",
  4: "audio duration exceeds the limit",
  5: "audio duration check failed",
  6: "silent audio",
  7: "translation failed",
  8: "the account has no translation permission",
  9: "transcription quality check failed",
  10: "the quality check matched no keyword",
  11: "quality check or translation is not enabled",
  12: "audio language analysis failed",
  99: "unknown upstream error",
};

function failureReason(info, descInfo) {
  const failType = integerValue(info ? info.failType : undefined);
  let message = "iFLYTEK transcription failed: " + (FAIL_TYPE_REASONS[String(failType.value)] || "transcription failed");
  if (failType.ok && failType.value > 0) message += " (failType: " + failType.value + ")";
  if (descInfo && descInfo.toLowerCase() !== "success") message += ": " + descInfo;
  return message;
}

// 结算用的时长事实可能以两种形态到达：原始查询响应（content.orderInfo）或落库摘要
// （realDurationMs）。统一取毫秒值，取不到返回 null。
//
// 刻意不回退到 billedDuration：那是展示用的合并值，可能带着退化的 originalDuration，
// 拿它结算会把 8 秒音频按 60 秒计费。
function authoritativeDurationFrom(value) {
  if (!value || typeof value !== "object") return null;
  const content = value.content;
  if (content && typeof content === "object" && content.orderInfo && typeof content.orderInfo === "object") {
    const parsed = authoritativeDurationMs(content.orderInfo);
    if (parsed !== null) return parsed;
  }
  if (value.orderInfo && typeof value.orderInfo === "object") {
    const parsed = authoritativeDurationMs(value.orderInfo);
    if (parsed !== null) return parsed;
  }
  const stored = integerValue(value.realDurationMs);
  return stored.ok && stored.value > 0 ? stored.value : null;
}

// 宿主的单任务轮询把整段上游响应写入 task.data，因此 render 多半拿到原始响应；
// 同时兼容 parseTaskResult 产出的摘要形态，便于宿主或测试直接传入。
function parseStoredResult(data) {
  if (data && typeof data === "object" && typeof data.text === "string" && Array.isArray(data.segments)) {
    const segments = data.segments.map(function (segment, index) {
      const start = integerValue(segment.start);
      const end = integerValue(segment.end);
      return {
        id: index,
        start: start.ok ? start.value : 0,
        end: end.ok ? end.value : 0,
        text: typeof segment.text === "string" ? segment.text : "",
        speaker: trimmed(segment.speaker),
      };
    });
    const duration = integerValue(data.billedDuration !== undefined ? data.billedDuration : data.originalDuration);
    const authoritative = integerValue(data.realDurationMs);
    return {
      code: normalizedCode(data.code),
      descInfo: trimmed(data.descInfo),
      text: data.text,
      segments: segments,
      language: trimmed(data.language),
      billedDuration: duration.ok ? duration.value : 0,
      realDurationMs: authoritative.ok && authoritative.value > 0 ? authoritative.value : null,
    };
  }
  if (data && typeof data === "object") return summarizeResult(data);
  return { code: "", descInfo: "", text: "", segments: [], language: "", billedDuration: 0, realDurationMs: null };
}

// 取表单字段的第一个值。宿主把 multipart 的文本字段以「字段名 → 字符串数组」交给
// 插件，重复出现的字段按提交顺序排列。
function firstFieldValue(fields, name) {
  if (!fields || typeof fields !== "object") return "";
  const value = fields[name];
  if (Array.isArray(value)) return trimmed(value.length > 0 ? value[0] : "");
  return trimmed(value);
}

// 可选的非负整数字段：缺失或空返回空串（表示不启用）；非整数或负数直接报错。
//
// 取值范围由上游判定，插件只挡掉明显非法的写法——提前拒掉上游可能支持的取值，
// 会让请求以「参数非法」失败，而正确结果本应是上游照常处理。
function optionalUnsignedInteger(fields, name) {
  const value = firstFieldValue(fields, name);
  if (value === "") return "";
  if (!/^\d+$/.test(value)) throw new Error(name + " must be a non-negative integer");
  return value;
}

function decodeRequest(ctx) {
  const body = ctx && ctx.body ? ctx.body : {};
  if (!body || body.kind !== "multipart") {
    throw new Error('audio transcription requires a multipart upload with a "file" field');
  }
  const files = Array.isArray(body.files) ? body.files : [];
  const audioFiles = [];
  for (const file of files) {
    if (!file || typeof file !== "object") continue;
    if (file.field !== "file") throw new Error("unexpected file field: " + trimmed(file.field));
    audioFiles.push(file);
  }
  if (audioFiles.length === 0) throw new Error('audio file field "file" is required');
  if (audioFiles.length > 1) throw new Error('audio file field "file" must be provided once');
  const file = audioFiles[0];
  const fileRef = trimmed(file.ref);
  if (fileRef === "") throw new Error('audio file field "file" is required');
  const fileSize = integerValue(file.size);
  if (!fileSize.ok || fileSize.value <= 0) throw new Error("audio file is empty");
  // 宿主在收包时给出了这个文件的可计费时长（media-duration@1 能力）：实测长度；
  // 宿主测不到的容器给出可计费上限。插件不再自行决定时长口径。
  // 宿主没给时长说明宿主没有兑现能力声明，此时必须让请求失败而不是按 0 秒
  // 免费提交——0 秒预扣等于把这次转写白送出去。
  if (typeof file.durationSeconds !== "number" || !Number.isFinite(file.durationSeconds) || file.durationSeconds < 0) {
    throw new Error("the host did not provide a billable duration for the audio file");
  }
  const billableSeconds = file.durationSeconds;

  const fields = body.fields && typeof body.fields === "object" ? body.fields : {};
  const model = trimmed(ctx && ctx.model) || firstFieldValue(fields, "model");
  if (model === "") throw new Error("model is required");
  // 角色分离是上游的可选查询参数（roleType、roleNum）。未传时上游不返回 rl/spk，
  // 转写结果里就没有说话人。
  const roleType = optionalUnsignedInteger(fields, "roleType");
  const roleNum = optionalUnsignedInteger(fields, "roleNum");

  return {
    kind: "submit",
    model: model,
    action: "transcription",
    requestBody: {
      model: model,
      fileRef: fileRef,
      filename: trimmed(file.filename),
      mimeType: trimmed(file.mimeType),
      fileSize: fileSize.value,
      durationSeconds: billableSeconds,
      roleType: roleType,
      roleNum: roleNum,
      // signatureRandom 在这里生成：上传与轮询必须复用同一个值，而宿主不会把
      // buildSubmitRequest 里的局部变量传给 parseSubmitResponse，只能随 requestBody
      // 持久化到提交结果，再由 parseSubmitResponse 写入插件状态。
      signatureRandom: newSignatureRandom(),
    },
  };
}

export function buildSubmitRequest(ctx) {
  const requestBody = ctx.requestBody || {};
  const credentials = credentialsFrom(ctx.apiKey);
  const signatureRandom = trimmed(requestBody.signatureRandom);
  if (signatureRandom === "") throw new Error("decoded request is missing signatureRandom");
  const params = {
    appId: credentials.appId,
    accessKeyId: credentials.accessKeyId,
    dateTime: requestDateTime(),
    signatureRandom: signatureRandom,
    fileSize: String(integerValue(requestBody.fileSize).value),
    fileName: uploadFileName(requestBody.filename, requestBody.mimeType),
    language: DEFAULT_LANGUAGE,
    // 插件拿不到文件字节，无法保证 duration 与实际时长一致。官方明确
    // durationCheckDisable=true 时不需要传 duration，因此关闭校验并省略 duration；
    // 同步端点用本地解析器得到时长后保持校验开启，两者不要互相照抄。
    durationCheckDisable: "true",
  };
  // 角色分离参数是上游的可选查询参数，参与签名。未启用时不进签名串，签名与调用方
  // 未传角色分离时完全一致。
  const roleType = trimmed(requestBody.roleType);
  if (roleType !== "") params.roleType = roleType;
  const roleNum = trimmed(requestBody.roleNum);
  if (roleNum !== "") params.roleNum = roleNum;
  return {
    url: String(ctx.baseUrl || "").replace(/\/+$/, "") + UPLOAD_PATH + "?" + queryString(params),
    method: "POST",
    headers: { "Content-Type": "application/octet-stream", signature: signatureOf(params, credentials.apiSecret) },
    bodyType: "file",
    parts: [{ name: "file", fileRef: requestBody.fileRef }],
    action: "transcription",
  };
}

export function parseSubmitResponse(ctx, response) {
  const body = response && response.body;
  if (!body || typeof body !== "object" || Array.isArray(body)) throw new Error("iFLYTEK upload response is not a JSON object");
  const code = normalizedCode(body.code);
  const descInfo = trimmed(body.descInfo);
  if (code !== "" && code !== SUCCESS_CODE) {
    throw new Error("iFLYTEK upload failed " + code + (descInfo === "" ? "" : ": " + descInfo));
  }
  const content = body.content && typeof body.content === "object" ? body.content : {};
  const orderId = trimmed(content.orderId);
  if (orderId === "") throw new Error("iFLYTEK upload response did not include an order id");
  const signatureRandom = trimmed((ctx.requestBody || {}).signatureRandom);
  if (signatureRandom === "") throw new Error("decoded request is missing signatureRandom");
  return {
    taskId: orderId,
    taskData: body,
    // 轮询必须复用上传时的 signatureRandom，dateTime 则在每次查询时重新生成。
    state: { orderId: orderId, signatureRandom: signatureRandom },
  };
}

export function buildQueryRequest(ctx) {
  const credentials = credentialsFrom(ctx.apiKey);
  const state = ctx.state && typeof ctx.state === "object" ? ctx.state : {};
  const data = ctx.data && typeof ctx.data === "object" ? ctx.data : {};
  const signatureRandom = trimmed(state.signatureRandom) || trimmed(data.signatureRandom);
  if (signatureRandom === "") throw new Error("signatureRandom is missing from the plugin state");
  const orderId = trimmed(ctx.taskId) || trimmed(state.orderId) || trimmed(data.orderId);
  if (orderId === "") throw new Error("order id is missing from the task");
  const params = {
    appId: credentials.appId,
    accessKeyId: credentials.accessKeyId,
    dateTime: requestDateTime(),
    signatureRandom: signatureRandom,
    orderId: orderId,
    resultType: "transfer",
  };
  return {
    url: String(ctx.baseUrl || "").replace(/\/+$/, "") + RESULT_PATH + "?" + queryString(params),
    method: "POST",
    headers: { "Content-Type": "application/json", signature: signatureOf(params, credentials.apiSecret) },
    body: "{}",
    action: "transcription",
  };
}

export function parseTaskResult(ctx, body) {
  if (!body || typeof body !== "object" || Array.isArray(body)) {
    return { status: "UNKNOWN", reason: "iFLYTEK response is not a JSON object" };
  }
  const code = normalizedCode(body.code);
  const descInfo = trimmed(body.descInfo);
  // 判据以业务码为准：订单信息缺失时 status 默认 0，只看 status 会把硬错误
  // （如 100001 订单不存在）当成处理中一直轮询，掩盖真实原因。
  if (code !== "" && code !== SUCCESS_CODE) {
    // 限流与订单未完成都是可自愈的瞬时状态：继续轮询，宿主有轮询上限兜底。
    // 其余业务码（查询订单错误 100004、未知异常 999999 等）不是瞬时状态，继续轮询
    // 只会把真实故障拖成宿主超时；直接判失败并带上游码，客户端才拿得到可定位的原因。
    if (code === ORDER_NOT_FINISHED_CODE || code === RATE_LIMIT_CODE) {
      return { status: "IN_PROGRESS", progress: "50%" };
    }
    return { status: "FAILURE", progress: "100%", reason: "iFLYTEK error " + code + (descInfo === "" ? "" : ": " + descInfo) };
  }
  const info = orderInfoOf(body);
  if (!info) return { status: "UNKNOWN", reason: "iFLYTEK response has no order info" };
  const status = integerValue(info.status);
  if (status.ok && status.value === 4) {
    const summary = summarizeResult(body);
    const data = {
      text: summary.text,
      duration_seconds: summary.billedDuration / 1000,
      segments: summary.segments,
      language: summary.language,
      billedDuration: summary.billedDuration,
    };
    // 权威时长单独落库：结算只认它。上游没回传真实时长时字段整体不写，空值会被
    // 结算当成「取到了 0」。
    if (summary.realDurationMs !== undefined && summary.realDurationMs !== null) {
      data.realDurationMs = summary.realDurationMs;
    }
    return {
      status: "SUCCESS",
      progress: "100%",
      taskId: ctx.taskId,
      data: data,
    };
  }
  if (status.ok && status.value === -1) {
    return { status: "FAILURE", progress: "100%", reason: failureReason(info, descInfo) };
  }
  if (status.ok && (status.value === 0 || status.value === 3)) {
    return { status: "IN_PROGRESS", progress: "50%" };
  }
  return { status: "UNKNOWN", reason: "iFLYTEK order status is not recognized" };
}

// 提交阶段用宿主给出的可计费时长预扣：宿主把用量事实冻结进计费快照，完成时再用上游
// 回传的真实时长替换，差额在结算阶段补收或退回。decodeRequest 已经拒绝没有时长的请求，
// 这里拿到的必定是宿主给出的可计费时长。
export function extractUsage(ctx) {
  if (ctx.usagePurpose === "billing_ratios") return null;
  const requestBody = (ctx && ctx.requestBody) || {};
  const billable = Number(requestBody.durationSeconds);
  if (!Number.isFinite(billable) || billable < 0) {
    throw new Error("the request is missing the billable duration");
  }
  return { duration: billable };
}

// 结算阶段以上游回传的真实时长为准。宿主对时长类事实有上限校验，超限的事实会被宿主
// 丢弃并保留提交期的预扣值，因此这里不自行封顶。
//
// 上游没有回传真实时长时不产出时长事实，由宿主保留提交期的预扣值——预扣值是宿主
// 下发的可计费时长，对测不出长度的格式来说是可计费上限而不是实测长度。
export function extractUsageOnComplete(ctx, result, data) {
  let milliseconds = authoritativeDurationFrom(data);
  if (milliseconds === null && result) milliseconds = authoritativeDurationFrom(result);
  if (milliseconds === null && ctx) milliseconds = authoritativeDurationFrom(ctx.data);
  if (milliseconds === null || milliseconds <= 0) return null;
  return { duration: milliseconds / 1000 };
}

// 时间戳格式化：毫秒 → HH:MM:SS.mmm。
//
// 小时位可以超过两位（超过 1 小时的音频），毫秒补零到 3 位。取不到时间时给出占位符，
// 而不是伪造一个 0——0 是一个合法的起点，冒充它会让客户端以为音频从 0 毫秒开始。
function formatTimestampMs(milliseconds) {
  const value = Number(milliseconds);
  if (!Number.isFinite(value)) return "??:??:??.???";
  const total = Math.max(0, Math.trunc(value));
  const hours = Math.floor(total / 3600000);
  const minutes = Math.floor((total % 3600000) / 60000);
  const seconds = Math.floor((total % 60000) / 1000);
  return pad2(hours) + ":" + pad2(minutes) + ":" + pad2(seconds) + "." + pad3(total % 1000);
}

// 段文本渲染：每段一行，行首是时间戳标签。
//
// 说话人按首次出现顺序编号（从 1 起）；上游没开角色分离时标签里只有时间戳。
// 分段之间用换行分隔。
function formatSegmentsText(segments) {
  const speakerLabels = {};
  let speakerCount = 0;
  const lines = [];
  for (const segment of segments) {
    const speaker = trimmed(segment.speaker);
    let label = "";
    if (speaker !== "") {
      if (!Object.prototype.hasOwnProperty.call(speakerLabels, speaker)) {
        speakerCount += 1;
        speakerLabels[speaker] = String(speakerCount);
      }
      label = "说话人" + speakerLabels[speaker] + " ";
    }
    lines.push(
      "[" + label + formatTimestampMs(segment.start) + "-" + formatTimestampMs(segment.end) + "] " + segment.text
    );
  }
  return lines.join("\n");
}

function renderTranscription(_ctx, task) {
  const status = String((task && task.status) || "UNKNOWN").toUpperCase();
  // 非终态必须返回空对象：宿主会把 null、数组与标量判为非法转写对象。
  if (status !== "SUCCESS" && status !== "FAILURE") return {};
  const parsed = parseStoredResult(task && task.data);
  if (status === "FAILURE") {
    const code = parsed.code !== "" && parsed.code !== SUCCESS_CODE ? parsed.code : "task_failed";
    const info = orderInfoOf(task && task.data);
    return {
      status: "failed",
      error: {
        code: code,
        message: trimmed(task && task.fail_reason) || (info ? failureReason(info, parsed.descInfo) : "transcription failed"),
      },
    };
  }
  return {
    status: "succeeded",
    // 文本按分段渲染成带时间戳的行；分段缺失时退回解析层给出的全文。
    text: parsed.segments.length > 0 ? formatSegmentsText(parsed.segments) : parsed.text,
    duration: parsed.billedDuration / 1000,
    segments: parsed.segments.map(function (segment) {
      return {
        id: segment.id,
        start: segment.start / 1000,
        end: segment.end / 1000,
        text: segment.text,
        // 说话人是上游的原始标识，与解析层和 Go 侧同语义；文本标签里的编号是另一层。
        speaker: trimmed(segment.speaker),
      };
    }),
    // 上游未返回语种时留空字符串，不臆造。
    language: parsed.language,
  };
}

export const protocols = {
  openai_audio: {
    decodeRequest: decodeRequest,
    render: renderTranscription,
  },
};
