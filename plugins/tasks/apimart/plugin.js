// APIMart routes every upstream video model through one endpoint pair:
// POST /v1/videos/generations submits a task and GET /v1/tasks/{task_id}
// polls it. The submit reply carries the task id in data[0].task_id, the poll
// reply in data.status (pending, processing, completed, failed, cancelled), and
// the finished videos in data.result.videos[]. The gateway bills the duration
// it sends upstream, so the only usage fact is that duration.

// The public duration range APIMart accepts for a video task.
const DEFAULT_DURATION = 5;
const MIN_DURATION = 3;
const MAX_DURATION = 15;
// The quality tiers APIMart exposes for a generation.
const MODES = ["std", "pro", "4k"];
// The frame shapes APIMart accepts.
const ASPECT_RATIOS = ["16:9", "9:16", "1:1"];
// The shot styles of a multi-shot request.
const SHOT_TYPES = ["customize", "intelligence"];
// APIMart takes at most two reference images per task.
const MAX_IMAGE_URLS = 2;
// At most three subjects may reference images.
const MAX_ELEMENTS = 3;
// A subject references between two and four images.
const MIN_ELEMENT_IMAGES = 2;
const MAX_ELEMENT_IMAGES = 4;

// The billing subject is the generated video, priced by its duration.
const SECONDS_FIELD = {
  type: "number",
  unit: "second",
  description: { en: "Video generation unit price", zh: "视频生成单价" },
};

// The video models APIMart serves, taken from the names its catalog uses.
// Names that another built-in plugin already claims are left out, so every
// model name stays unique across the whole plugin library.
const VIDEO_MODELS = [
  "seedance-1.0-pro",
  "seedance-1.5-pro",
  "seedance-2.0",
  "seedance-2.5",
  "veo3",
  "veo3-remix",
  "veo3-official",
  "flux-3-video",
  "skyreels-v4",
  "happyhorse-1.0",
  "happyhorse-1.1",
  "wan3.0",
  "wan2.7",
  "wan2.7-r2v",
  "wan2.7-videoedit",
  "wan2.6",
  "wan2.5-preview",
  "kling-v3-omni",
  "kling-v4",
  "kling-v4-flash",
  "kling-video-o1",
  "kling-3.0-turbo",
  "vidu-q3-pro",
  "vidu-q3",
  "vidu-q4-preview",
  "pixverse-v6",
  "pixverse-v6-max",
  "muse-video-ext",
  "gemini-omni-1.1-flash-ext",
];

export const meta = {
  apiVersion: 1,
  key: "apimart",
  name: "APIMart",
  icon: "text",
  description: {
    en: "APIMart video tasks",
    zh: "APIMart 视频任务",
  },
  version: "0.1.0",
  author: { name: "QuantumNous" },
  channelTypes: [64],
  baseUrl: "https://api.apimart.ai",
  allowedHosts: ["api.apimart.ai"],
  models: VIDEO_MODELS,
  fetchMode: "per_task",
  upstreams: ["vendor", "new_api"],
  usageSchema: { seconds: SECONDS_FIELD },
  usageExamples: [
    { label: "std · 5s", facts: { seconds: 5 } },
    { label: "std · 10s", facts: { seconds: 10 } },
    { label: "pro · 15s", facts: { seconds: 15 } },
  ],
  // APIMart's own paths, served under the plugin's own address.
  routes: [
    { method: "POST", path: "/apimart/v1/videos/generations", type: "submit", decode: "createGeneration", render: "videoCreated" },
    { method: "GET", path: "/apimart/v1/tasks/:task_id", type: "query", taskIdParam: "task_id", render: "videoStatus" },
  ],
  protocols: [{ name: "openai_responses", supports: ["stream", "sync", "background"] }, "openai_video"],
};

function trimmed(value) {
  return String(value || "").trim();
}

function plainObject(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? value : null;
}

// A New API gateway that hosts this plugin serves it under its own prefix.
function apiRoot(ctx) {
  return ctx.baseUrl + (ctx.upstream && ctx.upstream.kind === "new_api" ? "/apimart" : "");
}

function integerField(value, name, minimum, maximum) {
  const number = typeof value === "string" && value.trim() !== "" ? Number(value) : value;
  if (typeof number !== "number" || !Number.isInteger(number) || number < minimum || number > maximum)
    throw new Error(name + " must be an integer between " + minimum + " and " + maximum);
  return number;
}

function optionalInteger(value, name, minimum, maximum, fallback) {
  if (value === undefined || value === null || value === "") return fallback;
  return integerField(value, name, minimum, maximum);
}

function optionalEnum(value, name, allowed) {
  if (value === undefined || value === null || value === "") return undefined;
  const text = trimmed(value);
  if (!allowed.includes(text)) throw new Error(name + " must be one of " + allowed.join(", "));
  return text;
}

// The reference images of a task: public URLs APIMart can fetch.
function imageURLs(value, name) {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new Error(name + " must be an array of public image URLs");
  if (value.length > MAX_IMAGE_URLS) throw new Error(name + " must not hold more than " + MAX_IMAGE_URLS + " images");
  return value.map(function (item) {
    if (typeof item !== "string" || !item.trim()) throw new Error(name + " must hold public image URLs");
    return item.trim();
  });
}

// The shots of a multi-shot request: consecutive indexes and, when the client
// fixed the duration of the task, a duration sum that matches it. A client
// that leaves the duration out lets the shots decide it.
function multiPrompt(value, duration) {
  if (value === undefined || value === null) return undefined;
  if (!Array.isArray(value)) throw new Error("multi_prompt must be an array of shots");
  let total = 0;
  const shots = value.map(function (shot, index) {
    const object = plainObject(shot);
    if (!object) throw new Error("multi_prompt[" + index + "] must be an object");
    if (object.index !== index + 1) throw new Error("multi_prompt index must start at 1 and stay consecutive");
    const shotDuration = integerField(object.duration, "multi_prompt[" + index + "].duration", 1, MAX_DURATION);
    total += shotDuration;
    return { index: object.index, prompt: trimmed(object.prompt), duration: shotDuration };
  });
  if (duration !== undefined && total !== duration) throw new Error("multi_prompt durations must add up to the duration of the task");
  return shots;
}

// The subjects of a reference-image task, each with its own images.
function elementList(value) {
  if (value === undefined || value === null) return undefined;
  if (!Array.isArray(value)) throw new Error("element_list must be an array of subjects");
  if (value.length > MAX_ELEMENTS) throw new Error("element_list must not hold more than " + MAX_ELEMENTS + " subjects");
  return value.map(function (element, index) {
    const object = plainObject(element);
    if (!object) throw new Error("element_list[" + index + "] must be an object");
    const name = trimmed(object.name);
    if (!name) throw new Error("element_list[" + index + "].name is required");
    const urls = object.element_input_urls;
    if (!Array.isArray(urls) || urls.length < MIN_ELEMENT_IMAGES || urls.length > MAX_ELEMENT_IMAGES)
      throw new Error("element_list[" + index + "].element_input_urls must hold " + MIN_ELEMENT_IMAGES + " to " + MAX_ELEMENT_IMAGES + " images");
    return {
      name: name,
      description: trimmed(object.description),
      element_input_urls: urls.map(function (url) {
        if (typeof url !== "string" || !url.trim()) throw new Error("element_list[" + index + "].element_input_urls must hold public image URLs");
        return url.trim();
      }),
    };
  });
}

// The APIMart generation body. Only the documented fields are sent upstream,
// and the duration the task carries is the one the gateway bills.
function generationBody(source) {
  const prompt = trimmed(source.prompt);
  if (!prompt) throw new Error("prompt is required");
  // A client that leaves the duration out lets the shots of the task decide it.
  const durationGiven = source.duration !== undefined && source.duration !== null && source.duration !== "";
  const fixedDuration = durationGiven ? optionalInteger(source.duration, "duration", MIN_DURATION, MAX_DURATION) : undefined;
  const shots = multiPrompt(source.multi_prompt, fixedDuration);
  let duration = fixedDuration;
  if (duration === undefined) {
    const shotTotal = shots ? shots.reduce(function (sum, shot) { return sum + shot.duration; }, 0) : 0;
    duration = shots ? integerField(shotTotal, "duration", MIN_DURATION, MAX_DURATION) : DEFAULT_DURATION;
  }
  const body = { prompt: prompt, duration: duration };

  const negativePrompt = trimmed(source.negative_prompt);
  if (negativePrompt) body.negative_prompt = negativePrompt;

  const mode = optionalEnum(source.mode, "mode", MODES);
  if (mode) body.mode = mode;

  const aspectRatio = optionalEnum(source.aspect_ratio, "aspect_ratio", ASPECT_RATIOS);
  if (aspectRatio) body.aspect_ratio = aspectRatio;

  const images = imageURLs(source.image_urls, "image_urls");
  if (images.length) body.image_urls = images;

  // APIMart documents these switches without a type, so they travel as given.
  if (source.watermark !== undefined && source.watermark !== null) body.watermark = source.watermark;
  if (source.audio !== undefined && source.audio !== null) body.audio = source.audio;
  if (source.multi_shot !== undefined && source.multi_shot !== null) body.multi_shot = source.multi_shot;

  const shotType = optionalEnum(source.shot_type, "shot_type", SHOT_TYPES);
  if (shotType) body.shot_type = shotType;

  if (shots) body.multi_prompt = shots;

  const elements = elementList(source.element_list);
  if (elements) body.element_list = elements;

  return { action: images.length ? "image_to_video" : "text_to_video", body: body };
}

// The seconds a task is billed for. Only the duration the gateway sends
// upstream counts; a placeholder duration is never billed.
function billedSeconds(requestBody) {
  const body = plainObject(requestBody) || {};
  const duration = Number(body.duration);
  if (!Number.isFinite(duration) || !Number.isInteger(duration) || duration < MIN_DURATION || duration > MAX_DURATION) return DEFAULT_DURATION;
  return duration;
}

// The videos of a finished task. The poll reply keeps them in
// data.result.videos[], and every video holds its signed URLs in an array.
function artifactVideos(data) {
  const body = plainObject(data) || {};
  const payload = plainObject(body.data);
  if (!payload) return [];
  const result = plainObject(payload.result);
  if (!result || !Array.isArray(result.videos)) return [];
  return result.videos;
}

// The signed upstream link of one finished video.
function videoURL(video) {
  const object = plainObject(video);
  if (!object) return "";
  if (Array.isArray(object.url)) return trimmed(object.url[0]);
  return trimmed(object.url);
}

// The videos of a finished task that carry a signed link. The artifact keys
// and the content requests both address this list, so a video without a link
// never shifts the positions of the ones after it.
function artifactVideoEntries(data) {
  return artifactVideos(data).filter(function (video) {
    return videoURL(video);
  });
}

// The artifact of a key like video-0 is the video at that position.
function artifactIndex(key) {
  const match = /^video-(\d+)$/.exec(trimmed(key));
  return match ? Number(match[1]) : -1;
}

// The gateway's own address of a finished video.
function artifactURL(ctx) {
  const artifacts = plainObject(ctx && ctx.artifacts) || {};
  const keys = Object.keys(artifacts).sort();
  for (const key of keys) {
    const item = plainObject(artifacts[key]);
    if (item && item.type === "video" && trimmed(item.url)) return trimmed(item.url);
  }
  return "";
}

function responsesVideoText(ctx) {
  const url = artifactURL(ctx);
  if (!url) throw new Error("video artifact is unavailable");
  const escaped = url.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return '<video controls src="' + escaped + '"></video>';
}

// The text and the images of a Responses request.
function responsesInput(req) {
  const texts = [];
  const images = [];
  const input = req.input;
  if (typeof input === "string") texts.push(input);
  else if (Array.isArray(input)) {
    for (const item of input) {
      if (typeof item === "string") {
        texts.push(item);
        continue;
      }
      if (!plainObject(item)) continue;
      const content = item.content === undefined ? [item] : Array.isArray(item.content) ? item.content : [item.content];
      for (const part of content) {
        if (typeof part === "string") {
          texts.push(part);
          continue;
        }
        if (!plainObject(part)) continue;
        if (["input_text", "text"].includes(part.type) && typeof part.text === "string") texts.push(part.text);
        if (["input_image", "image_url"].includes(part.type)) {
          let url = part.image_url;
          if (plainObject(url)) url = url.url;
          if (trimmed(url)) images.push(trimmed(url));
        }
      }
    }
  }
  return {
    prompt: texts
      .filter(function (text) {
        return trimmed(text);
      })
      .join("\n"),
    images: images,
  };
}

// The fields APIMart takes beyond the prompt, which a client passes in the
// request metadata.
const VENDOR_FIELDS = [
  "negative_prompt",
  "mode",
  "aspect_ratio",
  "image_urls",
  "watermark",
  "audio",
  "multi_shot",
  "shot_type",
  "multi_prompt",
  "element_list",
  "duration",
];

function vendorFields(source) {
  const fields = {};
  for (const key of VENDOR_FIELDS) {
    if (source[key] !== undefined && source[key] !== null) fields[key] = source[key];
  }
  return fields;
}

export const native = {
  // APIMart's own body, sent as it is.
  createGeneration: function (ctx) {
    if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
    const body = plainObject(ctx.body.value);
    if (!body) throw new Error("request body must be an object");
    const model = trimmed(body.model);
    if (!model) throw new Error("model is required");
    const request = generationBody(body);
    return { kind: "submit", model: model, action: request.action, requestBody: request.body };
  },
  // APIMart's own submit reply, with the task the gateway created.
  videoCreated: function (ctx, task) {
    return { code: 200, data: [{ status: "submitted", task_id: task.task_id }] };
  },
  // APIMart's own task reply.
  videoStatus: function (ctx, task) {
    const body = plainObject(task.data);
    const payload = body ? plainObject(body.data) : null;
    if (payload) return body;
    const statuses = {
      NOT_START: "pending",
      SUBMITTED: "pending",
      QUEUED: "pending",
      IN_PROGRESS: "processing",
      SUCCESS: "completed",
      FAILURE: "failed",
    };
    const status = statuses[String(task.status || "").toUpperCase()] || "pending";
    const progress = Number(String(task.progress || "").replace("%", ""));
    const output = { id: task.task_id, status: status, progress: Number.isFinite(progress) ? progress : 0 };
    if (task.status === "FAILURE") output.error = { code: "task_failed", message: task.fail_reason || "video generation failed" };
    return { code: 200, data: output };
  },
  error: function (ctx, error) {
    return { error: { code: error.code, message: error.message } };
  },
};

export function buildSubmitRequest(ctx) {
  const request = generationBody(ctx.requestBody || {});
  return {
    url: apiRoot(ctx) + "/v1/videos/generations",
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json", Authorization: "Bearer " + ctx.apiKey },
    body: Object.assign({}, request.body, { model: ctx.upstreamModel || ctx.model }),
    action: request.action,
  };
}

export function parseSubmitResponse(ctx, resp) {
  const body = plainObject(resp.body);
  if (!body) throw new Error("APIMart returned an unexpected submit reply");
  const apiError = plainObject(body.error);
  if (apiError) throw new Error(trimmed(apiError.message) || "APIMart rejected the request");
  // The submit reply carries the task id in the first entry of data.
  const entries = Array.isArray(body.data) ? body.data : [];
  const taskId = trimmed(plainObject(entries[0]) && plainObject(entries[0]).task_id);
  if (!taskId) throw new Error("APIMart did not return a task id");
  // The completion hook cannot see the submitted body, so the billed duration
  // travels with the task.
  return { taskId: taskId, taskData: body, state: { seconds: billedSeconds(ctx.requestBody) } };
}

export function extractUsage(ctx) {
  return { seconds: billedSeconds(ctx.requestBody) };
}

export function buildQueryRequest(ctx) {
  return {
    url: apiRoot(ctx) + "/v1/tasks/" + encodeURIComponent(ctx.taskId),
    method: "GET",
    headers: { Accept: "application/json", Authorization: "Bearer " + ctx.apiKey },
  };
}

// The states in which a task has failed for good: the gateway stops polling on
// them and reports the failure.
const FAILED_STATUSES = ["failed", "cancelled", "canceled"];

export function parseTaskResult(ctx, body, _response) {
  // The poll reply is the body of this hook; the task data still holds the
  // reply of the submit round.
  const payload = plainObject(body);
  if (!payload) throw new Error("APIMart returned an unexpected task reply");
  const apiError = plainObject(payload.error);
  if (apiError) throw new Error(trimmed(apiError.message) || "APIMart rejected the request");
  const data = plainObject(payload.data);
  if (!data) throw new Error("APIMart returned an unexpected task reply");
  const status = trimmed(data.status);
  // A task that failed for good carries the reason of the failure in its
  // error field. Reporting the failure ends the polling round, while throwing
  // would spend the whole polling budget on a task that already finished.
  if (FAILED_STATUSES.includes(status.toLowerCase()))
    return { code: 0, status: "FAILURE", progress: "100%", reason: failureReason(data.error) };
  // An error on a task that has not failed yet is a transient upstream fault.
  const error = plainObject(data.error);
  if (error) throw new Error(trimmed(error.message) || "APIMart reported a task error");
  const statuses = { pending: "QUEUED", processing: "IN_PROGRESS", completed: "SUCCESS" };
  const mapped = statuses[status];
  const result = {
    code: 0,
    status: mapped || "UNKNOWN",
    progress: taskProgress(data, mapped),
    reason: "",
  };
  if (!mapped) result.reason = "unrecognized status: " + status;
  return result;
}

function taskProgress(data, status) {
  if (status === "SUCCESS" || status === "FAILURE") return "100%";
  const value = Number(data.progress);
  if (Number.isFinite(value) && value >= 0 && value <= 100) return String(Math.floor(value)) + "%";
  if (status === "QUEUED") return "0%";
  if (status === "IN_PROGRESS") return "50%";
  return "0%";
}

// The reason of a failed task: the message the upstream gave, or a default
// when it left the message out.
function failureReason(error) {
  const object = plainObject(error);
  const message = object && typeof object.message === "string" ? object.message.trim() : "";
  return message || "video generation failed";
}

// The completion hook is called with the task context while polling and with
// the task JSON while settling, so the seconds are looked for in both.
export function extractUsageOnComplete(first, second, _body) {
  const context = plainObject(first);
  const result = plainObject(second);
  const sources = [
    result ? plainObject(result.plugin_state) : null,
    context ? plainObject(context.state) : null,
    context ? plainObject(context.plugin_state) : null,
  ];
  for (const source of sources) {
    if (!source) continue;
    const seconds = Number(source.seconds);
    if (Number.isFinite(seconds) && seconds > 0) return { seconds: seconds };
  }
  return {};
}

export function listArtifacts(ctx) {
  const videos = artifactVideoEntries(ctx.data);
  const artifacts = [];
  for (let index = 0; index < videos.length && artifacts.length < 64; index++) {
    artifacts.push({ key: "video-" + index, type: "video" });
  }
  return artifacts;
}

export function buildContentRequest(ctx) {
  const videos = artifactVideoEntries(ctx.data);
  const index = artifactIndex(ctx.artifactKey);
  const url = index >= 0 ? videoURL(videos[index]) : "";
  if (!url) throw new Error("artifact_not_found");
  // The upstream link is signed and needs no credentials.
  return { url: url, method: ctx.clientRequest.method, credentialless: true };
}

export const protocols = {
  openai_responses: {
    decodeRequest: function (ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
      const req = plainObject(ctx.body.value);
      if (!req) throw new Error("request body must be an object");
      const model = trimmed(ctx.model) || trimmed(req.model);
      if (!model) throw new Error("model is required");
      if (req.input !== undefined && typeof req.input !== "string" && !Array.isArray(req.input)) throw new Error("input must be a string or array");
      if (req.images !== undefined && !Array.isArray(req.images)) throw new Error("images must be an array");
      if (req.metadata !== undefined && (!plainObject(req.metadata))) throw new Error("metadata must be an object");
      const input = responsesInput(req);
      // The vendor fields may sit in the body itself or in the request metadata.
      const source = Object.assign({}, vendorFields(plainObject(req.metadata) || {}), vendorFields(req));
      const images = input.images.concat((req.images || []).map(trimmed).filter(Boolean));
      if (images.length) source.image_urls = (Array.isArray(source.image_urls) ? source.image_urls : []).concat(images);
      if (req.duration !== undefined && req.duration !== null) source.duration = req.duration;
      else if (req.seconds !== undefined && req.seconds !== null) source.duration = req.seconds;
      source.prompt = input.prompt || trimmed(req.prompt);
      const request = generationBody(source);
      return { kind: "submit", model: model, action: request.action, requestBody: Object.assign({}, request.body, { model: model }) };
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
    renderFinal: function (ctx, _task) {
      return {
        output: [
          {
            type: "message",
            status: "completed",
            role: "assistant",
            content: [{ type: "output_text", text: responsesVideoText(ctx), annotations: [], logprobs: [] }],
          },
        ],
        metadata: { vendor: "apimart" },
      };
    },
  },
  openai_video: {
    decodeRequest: function (ctx) {
      if (!ctx.body || (ctx.body.kind !== "json" && ctx.body.kind !== "multipart")) throw new Error("JSON or multipart body required");
      let req;
      if (ctx.body.kind === "json") {
        req = plainObject(ctx.body.value);
        if (!req) throw new Error("request body must be an object");
      } else {
        req = {};
        const fields = ctx.body.fields || {};
        for (const name of Object.keys(fields)) {
          if (fields[name].length > 1) throw new Error(name + " must be provided once");
          req[name] = fields[name][0];
        }
        const files = ctx.body.files || [];
        if (files.length) throw new Error("APIMart image references must be public URLs, so file uploads are not supported");
      }
      const model = trimmed(ctx.model) || trimmed(req.model);
      if (!model) throw new Error("model is required");
      const source = Object.assign({}, vendorFields(plainObject(req.metadata) || {}), vendorFields(req));
      const prompt = trimmed(req.prompt);
      if (!prompt) throw new Error("prompt is required");
      source.prompt = prompt;
      if (req.duration !== undefined && req.duration !== null) source.duration = req.duration;
      else if (req.seconds !== undefined && req.seconds !== null) source.duration = req.seconds;
      const reference = trimmed(req.input_reference);
      if (reference) source.image_urls = [reference];
      const aspectRatio = optionalEnum(req.aspect_ratio, "aspect_ratio", ASPECT_RATIOS);
      if (aspectRatio) source.aspect_ratio = aspectRatio;
      else if (trimmed(req.size)) source.aspect_ratio = sizeAspectRatio(trimmed(req.size));
      const request = generationBody(source);
      return { kind: "submit", model: model, action: request.action, requestBody: Object.assign({}, request.body, { model: model }) };
    },
    render: function (ctx, task) {
      const output = { object: "video" };
      if (task.status === "FAILURE")
        output.error = { code: "task_failed", message: task.fail_reason || "video generation failed" };
      return output;
    },
  },
};

// The OpenAI video size of a request, as the frame shape APIMart takes.
function sizeAspectRatio(size) {
  const match = /^(\d+)x(\d+)$/.exec(size);
  if (!match) return undefined;
  const width = Number(match[1]);
  const height = Number(match[2]);
  if (!width || !height) return undefined;
  const ratio = width / height;
  const shapes = [
    { name: "16:9", value: 16 / 9 },
    { name: "9:16", value: 9 / 16 },
    { name: "1:1", value: 1 },
  ];
  let best = shapes[0];
  let distance = Math.abs(ratio - best.value);
  for (const shape of shapes) {
    const candidate = Math.abs(ratio - shape.value);
    if (candidate < distance) {
      best = shape;
      distance = candidate;
    }
  }
  return best.name;
}
