// RunningHub task plugin: runs ComfyUI workflows on RunningHub's cloud.
//
// Two models are supported, one per modality:
//   - minimax-h3-reference-to-video  (video)  MiniMax H3 reference-to-video
//   - krea2-normal-v1                (image)  Krea2 text-to-image
//
// A channel maps each declared model to a RunningHub workflow id in its model
// mapping (ctx.upstreamModel); one RunningHub account is one type-61 channel,
// so an account pool is expressed as multiple channels.
//
// Reference assets (image/video/audio) are uploaded before submit through the
// host's utils.uploadFile primitive and the returned file name is injected into
// the workflow. The upload share the per-hook network budget, so a task is
// capped at MAX_REFERENCE_FILES assets.

export const meta = {
  apiVersion: 1,
  key: "runninghub",
  name: "RunningHub",
  description: {
    en: "RunningHub ComfyUI workflow execution (MiniMax H3 reference-to-video and Krea2 text-to-image)",
    zh: "RunningHub ComfyUI 工作流执行（MiniMax H3 参考生视频、Krea2 文生图）",
  },
  version: "1.0.0",
  author: { name: "QuantumNous" },
  models: ["minimax-h3-reference-to-video", "krea2-normal-v1"],
  fetchMode: "per_task",
  upstreams: ["vendor"],
  requiredCapabilities: ["upload-file@1"],
  baseUrl: "https://www.runninghub.ai",
  usageSchema: {
    coins: {
      type: "number",
      unit: "credit",
      description: { en: "RunningHub coin unit price", zh: "RunningHub 币单价" },
    },
  },
  protocols: [
    { name: "openai_responses", supports: ["stream", "sync", "background"], models: ["minimax-h3-reference-to-video"] },
    { name: "openai_video", models: ["minimax-h3-reference-to-video"] },
    { name: "openai_image", models: ["krea2-normal-v1"] },
  ],
};

const BASE_URL = "https://www.runninghub.ai";
const PATHS = {
  CREATE_TASK: "/task/openapi/create",
  V2_QUERY: "/openapi/v2/query",
  UPLOAD_FILE_V2: "/openapi/v2/media/upload/binary",
};
// upload V2 reports success as 200, create/query use 0.
const SUCCESS_CODES = [0, "0", 200, "200"];
// Each uploaded asset spends one network budget slot; a submit hook has four.
const MAX_REFERENCE_FILES = 4;

// —————————————————————————————————————————— 通用工具 ——————————————————————————————————————————

function trimmed(value) {
  return String(value == null ? "" : value).trim();
}

function asRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value : null;
}

function readText(record, keys) {
  if (!record) return "";
  for (const key of keys) {
    const value = record[key];
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
}

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

// —————————————————————————————————————————— 产物 URL 提取 ——————————————————————————————————————————

const URL_KEYS = ["url", "fileUrl", "videoUrl", "video_url", "imageUrl", "image_url", "downloadUrl", "download_url"];
const MEDIA_EXTENSIONS = {
  video: [".mp4"],
  audio: [".mp3", ".wav", ".m4a", ".flac", ".aac", ".opus", ".pcm", ".ogg"],
  image: [".png", ".jpg", ".jpeg", ".webp", ".bmp", ".gif", ".avif"],
};

// The output nodes can be an array directly, or nested under outputs/results/data.
function collectOutputs(data) {
  if (Array.isArray(data)) return data.map(asRecord).filter(Boolean);
  const record = asRecord(data);
  if (!record) return [];
  for (const key of ["outputs", "results", "data"]) {
    if (Array.isArray(record[key])) return record[key].map(asRecord).filter(Boolean);
  }
  return [];
}

function readOutputUrl(output, keys) {
  return readText(output, keys);
}

function hasMediaExtensionPath(url, extensions) {
  const path = String(url || "").split("?")[0].split("#")[0].toLowerCase();
  return extensions.some(function (ext) {
    return path.endsWith(ext);
  });
}

function outputMediaType(output) {
  return readText(output, ["outputType", "fileType", "type"]).toLowerCase();
}

function findVideoUrl(outputs) {
  for (const output of outputs) {
    const url = readOutputUrl(output, URL_KEYS);
    if (!url) continue;
    const type = outputMediaType(output);
    if (hasMediaExtensionPath(url, MEDIA_EXTENSIONS.video) || type === "mp4" || type === "video/mp4") return url;
  }
  return "";
}

function findImageUrl(outputs) {
  for (const output of outputs) {
    const url = readOutputUrl(output, URL_KEYS);
    if (!url) continue;
    const type = outputMediaType(output);
    if (hasMediaExtensionPath(url, MEDIA_EXTENSIONS.image) || type === "image" || type.indexOf("image/") === 0 || MEDIA_EXTENSIONS.image.indexOf("." + type) !== -1) return url;
  }
  return "";
}

function findAudioUrl(outputs) {
  for (const output of outputs) {
    const url = readOutputUrl(output, URL_KEYS);
    if (!url) continue;
    const type = outputMediaType(output);
    if (hasMediaExtensionPath(url, MEDIA_EXTENSIONS.audio) || type === "audio" || type.indexOf("audio/") === 0 || MEDIA_EXTENSIONS.audio.indexOf("." + type) !== -1) return url;
  }
  return "";
}

// The declared model picks the output modality; the two supported models never
// overlap, so the media type is unambiguous from the polled URL.
function artifactFor(data) {
  const outputs = collectOutputs(data);
  const video = findVideoUrl(outputs);
  if (video) return { key: "video", type: "video", url: video };
  const image = findImageUrl(outputs);
  if (image) return { key: "image", type: "image", url: image };
  const audio = findAudioUrl(outputs);
  if (audio) return { key: "audio", type: "audio", url: audio };
  return null;
}

// —————————————————————————————————————————— 工作流模板 ——————————————————————————————————————————

// MiniMax H3 reference-to-video (ComfyUI). Node ids match the platform template:
// 25 prompt, 28 duration, 26 resolution selector, 31 the H3 generator; reference
// images/videos/audios are injected from node 100/120/140 upward.
const MINIMAX_H3_TEMPLATE = {
  "4": { inputs: { vae_name: "minimax_h3_video_vae_fp16.safetensors" }, class_type: "VAELoader", _meta: { title: "Load VAE" } },
  "5": { inputs: { noise_seed: 65232742064168 }, class_type: "RandomNoise", _meta: { title: "RandomNoise" } },
  "6": { inputs: { noise: ["5", 0], guider: ["7", 0], sampler: ["8", 0], sigmas: ["9", 0], latent_image: ["40", 0] }, class_type: "SamplerCustomAdvanced", _meta: { title: "SamplerCustomAdvanced" } },
  "7": { inputs: { model: ["47", 0], conditioning: ["31", 0] }, class_type: "BasicGuider", _meta: { title: "Basic Guider" } },
  "8": { inputs: { sampler_name: "euler" }, class_type: "KSamplerSelect", _meta: { title: "KSamplerSelect" } },
  "9": { inputs: { scheduler: "beta", steps: 8, denoise: 1, model: ["47", 0] }, class_type: "BasicScheduler", _meta: { title: "BasicScheduler" } },
  "10": { inputs: { samples: ["6", 0], vae: ["4", 0] }, class_type: "VAEDecode", _meta: { title: "VAE Decode" } },
  "11": { inputs: { samples: ["6", 0], vae: ["12", 0] }, class_type: "VAEDecodeAudio", _meta: { title: "VAE Decode Audio" } },
  "12": { inputs: { vae_name: "minimax_h3_audio_vae_fp32.safetensors" }, class_type: "VAELoader", _meta: { title: "Load VAE" } },
  "17": { inputs: { frame_rate: 24, loop_count: 0, filename_prefix: "AnimateDiff", format: "video/h264-mp4", pix_fmt: "yuv420p", crf: 19, save_metadata: true, trim_to_audio: false, pingpong: false, save_output: true, no_preview: false, images: ["10", 0], audio: ["11", 0] }, class_type: "VHS_VideoCombine", _meta: { title: "Video Combine 🎥🅥🅗🅢" } },
  "25": { inputs: { value: "" }, class_type: "PrimitiveStringMultiline", _meta: { title: "Input Text (Prompt)" } },
  "26": { inputs: { aspect_ratio: "16:9 (Widescreen)", megapixels: 1, multiple: 32 }, class_type: "ResolutionSelector", _meta: { title: "Resolution Selector" } },
  "28": { inputs: { value: 15 }, class_type: "PrimitiveFloat", _meta: { title: "Float (duration)" } },
  "29": { inputs: { expression: "max(5, round(a * 24)) + (5 - (max(5, round(a * 24)) % 17)) % 17", "values.a": ["28", 0] }, class_type: "ComfyMathExpression", _meta: { title: "Math Expression" } },
  "31": { inputs: { prompt: ["25", 0], width: ["26", 0], height: ["26", 1], length: ["29", 1], ref_image_size: "match", clip: ["65", 0], vae: ["4", 0], audio_vae: ["12", 0] }, class_type: "MiniMaxH3ReferenceToVideo", _meta: { title: "MiniMax H3 Reference to Video" } },
  "40": { inputs: { anything: ["31", 1] }, class_type: "easy cleanGpuUsed", _meta: { title: "Clean VRAM Used" } },
  "47": { inputs: { lora_name: "T8-minimax_h3_turbo_4步加速_comfyui.safetensors", strength_model: 1, model: ["48", 0] }, class_type: "LoraLoaderModelOnly", _meta: { title: "Load LoRA" } },
  "48": { inputs: { model: ["63", 0] }, class_type: "MiniMaxH3MemoryEfficientSageAttentionPatch", _meta: { title: "MiniMax H3 Memory Efficient Sage Attention Patch" } },
  "63": { inputs: { unet_name: "minimax_h3_fl2va_bf16.safetensors", weight_dtype: "default" }, class_type: "UNETLoader", _meta: { title: "Load Diffusion Model" } },
  "65": { inputs: { clip_name: "qwen3vl_32b_minimax_h3_bf16.safetensors", type: "minimax", device: "default" }, class_type: "CLIPLoader", _meta: { title: "Load CLIP" } },
};

// Krea2 normal-v1 text-to-image (ComfyUI). Node 5 prompt, node 8 latent size.
const KREA2_TEMPLATE = {
  "1": { inputs: { unet_name: "krea2_turbo_fp8_scaled.safetensors", weight_dtype: "default" }, class_type: "UNETLoader", _meta: { title: "UNet加载器" } },
  "4": { inputs: { clip_name: "qwen3vl_4b_fp8_scaled.safetensors", type: "krea2", device: "default" }, class_type: "CLIPLoader", _meta: { title: "加载CLIP" } },
  "5": { inputs: { text: "", clip: ["4", 0] }, class_type: "CLIPTextEncode", _meta: { title: "Positive Prompt" } },
  "7": { inputs: { vae_name: "qwen_image_vae.safetensors" }, class_type: "VAELoader", _meta: { title: "加载VAE" } },
  "8": { inputs: { width: 2048, height: 1536, batch_size: 1 }, class_type: "EmptySD3LatentImage", _meta: { title: "空Latent图像（SD3）" } },
  "9": { inputs: { seed: ["51", 0], steps: 8, cfg: 1, sampler_name: "euler", scheduler: "beta", denoise: 1, model: ["1", 0], positive: ["5", 0], negative: ["74", 0], latent_image: ["8", 0] }, class_type: "KSampler", _meta: { title: "K采样器" } },
  "10": { inputs: { samples: ["9", 0], vae: ["7", 0] }, class_type: "VAEDecode", _meta: { title: "VAE解码" } },
  "11": { inputs: { filename_prefix: "krea2", images: ["10", 0] }, class_type: "SaveImage", _meta: { title: "保存图像" } },
  "51": { inputs: { seed: 8338671205951 }, class_type: "Seed (rgthree)", _meta: { title: "Seed (rgthree)" } },
  "74": { inputs: { conditioning: ["5", 0] }, class_type: "ConditioningZeroOut", _meta: { title: "条件零化" } },
};

// —————————————————————————————————————————— builder 参数 ——————————————————————————————————————————

const ASPECT_RATIO_INPUTS = { "16:9": "16:9 (Widescreen)", "9:16": "9:16 (Portrait Widescreen)" };
const RESOLUTION_MEGAPIXELS = { "480p": 0.4, "720p": 0.9, "1K": 1, "2K": 2 };
const KREA2_DIMENSIONS = {
  "1K": {
    "1:1": { width: 1024, height: 1024 },
    "16:9": { width: 1024, height: 576 },
    "9:16": { width: 576, height: 1024 },
    "3:4": { width: 768, height: 1024 },
    "4:3": { width: 1024, height: 768 },
    "2:3": { width: 680, height: 1024 },
    "3:2": { width: 1024, height: 680 },
  },
  "2K": {
    "1:1": { width: 2048, height: 2048 },
    "16:9": { width: 2048, height: 1152 },
    "9:16": { width: 1152, height: 2048 },
    "3:4": { width: 1536, height: 2048 },
    "4:3": { width: 2048, height: 1536 },
    "2:3": { width: 1360, height: 2048 },
    "3:2": { width: 2048, height: 1360 },
  },
};

function normalizeMinimaxH3Duration(duration) {
  if (!Number.isFinite(duration) || !Number.isInteger(duration) || duration < 1 || duration > 15) {
    throw new Error("duration must be an integer between 1 and 15");
  }
  return Math.max(duration, 5);
}

function buildMinimaxH3Workflow(input) {
  const workflow = clone(MINIMAX_H3_TEMPLATE);
  workflow["25"].inputs.value = input.prompt;
  workflow["28"].inputs.value = normalizeMinimaxH3Duration(input.duration);
  workflow["26"].inputs.aspect_ratio = ASPECT_RATIO_INPUTS[input.aspectRatio];
  workflow["26"].inputs.megapixels = RESOLUTION_MEGAPIXELS[input.resolution || "1K"];

  (input.images || []).forEach(function (image, index) {
    const nodeId = String(100 + index);
    workflow[nodeId] = { inputs: { image: image }, class_type: "LoadImage", _meta: { title: "Load Image" } };
    workflow["31"].inputs["ref_images.ref_image_" + index] = [nodeId, 0];
  });
  (input.videos || []).forEach(function (video, index) {
    const nodeId = String(120 + index);
    workflow[nodeId] = {
      inputs: { video: video, force_rate: 0, custom_width: 0, custom_height: 0, frame_load_cap: 0, skip_first_frames: 0, select_every_nth: 1 },
      class_type: "VHS_LoadVideo",
      _meta: { title: "Load Video (Upload) 🎥🅥🅗🅢" },
    };
    workflow["31"].inputs["ref_videos.ref_video_" + index] = [nodeId, 0];
  });
  (input.audios || []).forEach(function (audio, index) {
    const nodeId = String(140 + index);
    workflow[nodeId] = { inputs: { audio: audio }, class_type: "LoadAudio", _meta: { title: "Load Audio" } };
    workflow["31"].inputs["ref_audios.ref_audio_" + index] = [nodeId, 0];
  });

  return workflow;
}

function buildKrea2Workflow(input) {
  const workflow = clone(KREA2_TEMPLATE);
  const dimensions = KREA2_DIMENSIONS[input.resolution || "2K"][input.aspectRatio];
  workflow["5"].inputs.text = input.prompt;
  workflow["8"].inputs.width = dimensions.width;
  workflow["8"].inputs.height = dimensions.height;
  return workflow;
}

// —————————————————————————————————————————— nodeInfoList 差量覆盖 ——————————————————————————————————————————

// A rule matches a class_type either exactly (prefix false) or as a prefix
// (prefix true); Primitive matches PrimitiveStringMultiline and PrimitiveFloat.
const NODE_FIELD_RULES = [
  { match: "CLIPTextEncode", prefix: false, required: ["text"], fields: ["text"] },
  { match: "EmptyLatentImage", prefix: false, required: [], fields: ["width", "height"] },
  { match: "EmptySD3LatentImage", prefix: false, required: [], fields: ["width", "height"] },
  { match: "KSampler", prefix: false, required: [], fields: ["steps", "cfg"] },
  { match: "Primitive", prefix: true, required: [], fields: ["value"] },
];

function isLinkedInput(value) {
  return Array.isArray(value) && value.length === 2 && typeof value[0] === "string" && typeof value[1] === "number";
}

function matchesClassType(classType, rule) {
  return rule.prefix ? classType.indexOf(rule.match) === 0 : classType === rule.match;
}

function resolveRule(classType, inputs) {
  for (const rule of NODE_FIELD_RULES) {
    if (!matchesClassType(classType, rule)) continue;
    if (rule.required.some(function (field) { return !(field in inputs); })) continue;
    return rule;
  }
  return null;
}

function buildNodeInfoList(workflow) {
  const result = [];
  const seen = {};
  for (const nodeId of Object.keys(workflow)) {
    const node = workflow[nodeId];
    if (!node || typeof node !== "object") continue;
    const classType = typeof node.class_type === "string" ? node.class_type : "";
    const inputs = node.inputs && typeof node.inputs === "object" ? node.inputs : null;
    if (!inputs) continue;
    const rule = resolveRule(classType, inputs);
    if (!rule) continue;
    for (const fieldName of rule.fields) {
      if (!(fieldName in inputs)) continue;
      const value = inputs[fieldName];
      if (isLinkedInput(value)) continue;
      const key = nodeId + ":" + fieldName;
      if (seen[key]) continue;
      seen[key] = true;
      const fieldValue = value !== null && typeof value === "object" ? JSON.stringify(value) : value;
      result.push({ nodeId: nodeId, fieldName: fieldName, fieldValue: fieldValue });
    }
  }
  return result;
}

// —————————————————————————————————————————— 提交 / 查询 ——————————————————————————————————————————

function responseFileName(response) {
  const body = asRecord(response.body);
  if (!body) throw new Error("upload returned an empty response");
  const data = asRecord(body.data) || body;
  const name = trimmed(data.filename) || trimmed(data.fileName);
  if (!name) throw new Error("upload response is missing the file name");
  return name;
}

function mediaClass(mimeType, filename) {
  const mime = trimmed(mimeType).toLowerCase();
  if (mime.indexOf("image/") === 0) return "image";
  if (mime.indexOf("video/") === 0) return "video";
  if (mime.indexOf("audio/") === 0) return "audio";
  // Fall back to the file extension when the client omitted the MIME type, so
  // reference assets are not silently dropped.
  if (hasMediaExtensionPath(filename, MEDIA_EXTENSIONS.video)) return "video";
  if (hasMediaExtensionPath(filename, MEDIA_EXTENSIONS.image)) return "image";
  if (hasMediaExtensionPath(filename, MEDIA_EXTENSIONS.audio)) return "audio";
  return "";
}

function videoDuration(req) {
  const raw = req.duration !== undefined ? req.duration : req.seconds;
  if (raw === undefined || raw === null || trimmed(raw) === "") return 5;
  const n = typeof raw === "string" ? Number(raw) : raw;
  return normalizeMinimaxH3Duration(n);
}

function submitHeaders(apiKey) {
  return { "Content-Type": "application/json", Host: "www.runninghub.ai", Authorization: "Bearer " + apiKey };
}

export function buildSubmitRequest(ctx) {
  const apiKey = trimmed(ctx.apiKey);
  if (!apiKey) throw new Error("api_key is empty");
  const workflowId = trimmed(ctx.upstreamModel);
  if (!workflowId) throw new Error("the channel model mapping must resolve to a RunningHub workflow id");
  const isVideo = ctx.model === "minimax-h3-reference-to-video";
  const req = asRecord(ctx.requestBody) || {};

  const images = [];
  const videos = [];
  const audios = [];
  for (const file of ctx.files || []) {
    const kind = mediaClass(file.mimeType, file.filename);
    if (kind === "image") images.push(file);
    else if (kind === "video") videos.push(file);
    else if (kind === "audio") audios.push(file);
  }
  const total = images.length + videos.length + audios.length;
  if (total > MAX_REFERENCE_FILES) throw new Error("too many reference files (max " + MAX_REFERENCE_FILES + ")");

  const uploadUrl = BASE_URL + PATHS.UPLOAD_FILE_V2;
  const authHeaders = { Authorization: "Bearer " + apiKey };
  const uploaded = function (files) {
    return files.map(function (file) {
      return responseFileName(
        utils.uploadFile({ url: uploadUrl, fileRef: file.ref, fieldName: "file", filename: file.filename, mimeType: file.mimeType, headers: authHeaders }),
      );
    });
  };

  let workflow;
  let nodeInfoList;
  if (isVideo) {
    const aspectRatio = trimmed(req.aspect_ratio) || "16:9";
    if (!ASPECT_RATIO_INPUTS[aspectRatio]) throw new Error("aspect_ratio must be 16:9 or 9:16");
    const resolution = trimmed(req.resolution) || "1K";
    if (RESOLUTION_MEGAPIXELS[resolution] === undefined) throw new Error("resolution must be 480p, 720p, 1K or 2K");
    workflow = buildMinimaxH3Workflow({
      prompt: trimmed(req.prompt),
      duration: videoDuration(req),
      aspectRatio: aspectRatio,
      resolution: resolution,
      images: uploaded(images),
      videos: uploaded(videos),
      audios: uploaded(audios),
    });
  } else {
    const prompt = trimmed(req.prompt);
    if (!prompt) throw new Error("prompt is required for text-to-image");
    const resolution = trimmed(req.resolution) || "2K";
    const aspectRatio = trimmed(req.aspect_ratio) || "1:1";
    if (!KREA2_DIMENSIONS[resolution] || !KREA2_DIMENSIONS[resolution][aspectRatio])
      throw new Error("invalid aspect_ratio or resolution");
    workflow = buildKrea2Workflow({ prompt: prompt, aspectRatio: aspectRatio, resolution: resolution });
  }
  nodeInfoList = buildNodeInfoList(workflow);

  const body = { apiKey: apiKey, workflowId: workflowId, workflow: JSON.stringify(workflow), addMetadata: true };
  if (nodeInfoList.length) body.nodeInfoList = nodeInfoList;

  return { url: BASE_URL + PATHS.CREATE_TASK, method: "POST", headers: submitHeaders(apiKey), body: body };
}

export function parseSubmitResponse(ctx, response) {
  const body = asRecord(response.body);
  const code = body && body.code;
  if (SUCCESS_CODES.indexOf(code) === -1) {
    const message = (body && (body.msg || body.message)) || "submit failed";
    throw new Error("RunningHub submit error (" + code + "): " + message);
  }
  const data = asRecord(body && body.data) || {};
  const taskId = trimmed(data.taskId);
  if (!taskId) throw new Error("submit response is missing taskId");
  return { taskId: taskId, taskData: body };
}

export function extractUsage(ctx) {
  return null;
}

export function buildQueryRequest(ctx) {
  const apiKey = trimmed(ctx.apiKey);
  return {
    url: BASE_URL + PATHS.V2_QUERY,
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: "Bearer " + apiKey },
    body: { taskId: ctx.taskId },
  };
}

// RunningHub reports its state through several spellings; everything else is
// left UNKNOWN so the host keeps polling.
function runninghubStatus(raw) {
  switch (raw) {
    case "SUCCESS":
    case "COMPLETED":
    case "FINISHED":
    case "DONE":
      return "SUCCESS";
    case "FAILED":
    case "FAILURE":
    case "ERROR":
    case "CANCELED":
    case "CANCELLED":
      return "FAILURE";
    case "RUNNING":
    case "PROCESSING":
    case "IN_PROGRESS":
      return "IN_PROGRESS";
    case "QUEUED":
    case "PENDING":
    case "WAITING":
      return "QUEUED";
    default:
      return "UNKNOWN";
  }
}

export function parseTaskResult(ctx, body) {
  const record = asRecord(body);
  const data = asRecord(record && record.data) || record || {};
  const status = runninghubStatus(readText(data, ["status", "taskStatus", "task_status"]).toUpperCase());
  if (status === "SUCCESS") {
    const artifact = artifactFor(data);
    return { taskId: ctx.taskId, status: status, progress: "100", url: artifact ? artifact.url : undefined };
  }
  if (status === "FAILURE") {
    const reason = readText(data, ["reason", "failReason", "errorMessage", "message", "msg"]) || "workflow failed";
    return { taskId: ctx.taskId, status: status, reason: reason };
  }
  return { taskId: ctx.taskId, status: status, progress: readText(data, ["progress", "percent"]) || undefined };
}

export function extractUsageOnComplete(task, result, data) {
  const record = asRecord(data);
  const usage = asRecord(record && record.usage);
  if (!usage) return null;
  const raw = usage.consumeCoins !== undefined ? usage.consumeCoins : usage.consume_coins;
  if (raw === undefined || raw === null || raw === "") return null;
  const coins = typeof raw === "string" ? Number(raw) : raw;
  if (!Number.isFinite(coins) || coins <= 0) return null;
  return { coins: coins };
}

export function listArtifacts(task) {
  if (task.status !== "SUCCESS") return [];
  const artifact = artifactFor(task.data);
  if (!artifact) return [];
  return [{ key: artifact.key, type: artifact.type }];
}

export function buildContentRequest(ctx) {
  const artifact = artifactFor(ctx.data);
  if (!artifact || artifact.key !== ctx.artifactKey) throw new Error("artifact_not_found");
  return { url: artifact.url, method: ctx.clientRequest.method, credentialless: true };
}

// —————————————————————————————————————————— 协议 ——————————————————————————————————————————

function responsesVideoText(ctx) {
  const artifact = ctx && ctx.artifacts && ctx.artifacts.video;
  const url = trimmed(artifact && artifact.url);
  if (!url) throw new Error("video artifact is unavailable");
  const escaped = url.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return '<video controls src="' + escaped + '"></video>';
}

// The Responses API `input` can be a plain string or a list of message parts;
// only the text parts become the workflow prompt. URL image references cannot
// be re-uploaded to RunningHub, so they are ignored.
function responsesPrompt(req) {
  const texts = [];
  const input = req.input;
  if (typeof input === "string") texts.push(input);
  else if (Array.isArray(input)) {
    for (const item of input) {
      if (typeof item === "string") {
        texts.push(item);
        continue;
      }
      if (!item || typeof item !== "object" || Array.isArray(item)) continue;
      const content = item.content === undefined ? [item] : Array.isArray(item.content) ? item.content : [item.content];
      for (const part of content) {
        if (typeof part === "string") {
          texts.push(part);
          continue;
        }
        if (!part || typeof part !== "object" || Array.isArray(part)) continue;
        if (["input_text", "text"].indexOf(part.type) !== -1 && typeof part.text === "string") texts.push(part.text);
      }
    }
  }
  return texts
    .filter(function (text) {
      return trimmed(text);
    })
    .join("\n");
}

const KREA2_RATIOS = ["1:1", "16:9", "9:16", "3:4", "4:3", "2:3", "3:2"];

// An OpenAI image `size` (WIDTHxHEIGHT) maps to the nearest Krea2 aspect ratio
// and a 1K/2K resolution bucket.
function aspectRatioFromSize(size) {
  const parts = trimmed(size).toLowerCase().split("x");
  const width = Number(parts[0]);
  const height = Number(parts[1]);
  if (parts.length !== 2 || !(width > 0) || !(height > 0)) return null;
  let nearest = KREA2_RATIOS[0];
  let distance = Infinity;
  for (const ratio of KREA2_RATIOS) {
    const sides = ratio.split(":").map(Number);
    const ratioDistance = Math.abs(Math.log(width / height) - Math.log(sides[0] / sides[1]));
    if (ratioDistance < distance) {
      nearest = ratio;
      distance = ratioDistance;
    }
  }
  return { aspectRatio: nearest, resolution: Math.max(width, height) > 1024 ? "2K" : "1K" };
}

export const protocols = {
  openai_responses: {
    decodeRequest: function (ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
      const req = ctx.body.value;
      if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("request body must be an object");
      const prompt = responsesPrompt(req);
      if (!trimmed(prompt)) throw new Error("input is required");
      const requestBody = { prompt: prompt };
      if (req.seconds !== undefined) requestBody.duration = req.seconds;
      else if (req.duration !== undefined) requestBody.duration = req.duration;
      if (trimmed(req.aspect_ratio)) requestBody.aspect_ratio = trimmed(req.aspect_ratio);
      if (trimmed(req.resolution)) requestBody.resolution = trimmed(req.resolution);
      return { kind: "submit", model: ctx.model, action: "reference_to_video", requestBody: requestBody };
    },
    renderEvents: function (ctx, task, previousState) {
      const status = String(task.status || "UNKNOWN").toUpperCase();
      const value = Number(String(task.progress || "").replace("%", ""));
      const progress = Number.isFinite(value) && value >= 0 && value <= 100 ? value : null;
      const state = { status: status, progress: progress };
      if (status === "SUCCESS") {
        const text = responsesVideoText(ctx);
        const events = previousState && previousState.status === status ? [] : [{ type: "output", data: text }];
        return { events: events, state: state, done: true };
      }
      if (status === "FAILURE")
        return { events: [{ type: "error", code: "task_failed", message: task.fail_reason || "task failed" }], state: state, done: true };
      if (previousState && previousState.status === status && previousState.progress === progress) return { events: [], state: state, done: false };
      const event = { type: "progress", message: status.toLowerCase() };
      if (progress !== null) event.progress = progress;
      return { events: [event], state: state, done: false };
    },
    renderFinal: function (ctx, task) {
      return {
        output: [
          {
            type: "message",
            status: "completed",
            role: "assistant",
            content: [{ type: "output_text", text: responsesVideoText(ctx), annotations: [], logprobs: [] }],
          },
        ],
        metadata: { vendor: "runninghub" },
      };
    },
  },
  openai_video: {
    decodeRequest: function (ctx) {
      if (!ctx.body || (ctx.body.kind !== "json" && ctx.body.kind !== "multipart")) throw new Error("JSON or multipart body required");
      const req = {};
      if (ctx.body.kind === "json") {
        if (!ctx.body.value || Array.isArray(ctx.body.value)) throw new Error("JSON object required");
        Object.assign(req, ctx.body.value);
      } else {
        const fields = ctx.body.fields || {};
        for (const name of Object.keys(fields)) {
          const values = fields[name] || [];
          if (values.length > 1) throw new Error(name + " must be provided once");
          req[name] = values[0];
        }
      }
      const requestBody = { prompt: trimmed(req.prompt) };
      if (req.duration !== undefined) requestBody.duration = req.duration;
      else if (req.seconds !== undefined) requestBody.duration = req.seconds;
      if (trimmed(req.aspect_ratio)) requestBody.aspect_ratio = trimmed(req.aspect_ratio);
      if (trimmed(req.resolution)) requestBody.resolution = trimmed(req.resolution);
      return { kind: "submit", model: ctx.model, action: "reference_to_video", requestBody: requestBody };
    },
    render: function (ctx, task) {
      const statusMap = { NOT_START: "queued", SUBMITTED: "queued", QUEUED: "queued", IN_PROGRESS: "in_progress", SUCCESS: "completed", FAILURE: "failed" };
      const data = asRecord(task.data) || {};
      const inner = asRecord(data.data) || {};
      const output = {
        id: task.task_id,
        object: "video",
        model: "",
        status: statusMap[task.status] || "unknown",
        progress: Number(String(task.progress || "0").replace("%", "")),
        created_at: Number(inner.created_at || inner.createTime || 0),
      };
      if (task.status === "FAILURE") output.error = { message: task.fail_reason || "", code: "" };
      return output;
    },
  },
  openai_image: {
    decodeRequest: function (ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
      const req = ctx.body.value;
      if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("request body must be an object");
      const prompt = trimmed(req.prompt);
      if (!prompt) throw new Error("prompt is required");
      const requestBody = { prompt: prompt };
      if (trimmed(req.aspect_ratio)) requestBody.aspect_ratio = trimmed(req.aspect_ratio);
      if (trimmed(req.resolution)) requestBody.resolution = trimmed(req.resolution);
      if (trimmed(req.size)) {
        const derived = aspectRatioFromSize(req.size);
        if (derived) {
          requestBody.aspect_ratio = derived.aspectRatio;
          requestBody.resolution = derived.resolution;
        }
      }
      return { kind: "submit", model: ctx.model, action: "text_to_image", requestBody: requestBody };
    },
    render: function (ctx, task) {
      const artifact = artifactFor(task.data);
      return { created: task.created_at, data: artifact ? [{ url: artifact.url }] : [], metadata: asRecord(task.data) || {} };
    },
  },
};
