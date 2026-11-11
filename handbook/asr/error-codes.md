# ASR 错误码参考

> 本页列出 ASR 接口（`Qwen3-ASR-1.7B`、`xfyun-asr`）所有可能的错误码及其含义与处理建议。

---

## 一、错误响应格式

所有错误响应均为 JSON 格式：

```json
{
  "error": {
    "message": "错误描述信息",
    "code": "语义化错误码",
    "type": "错误大类"
  }
}
```

---

## 二、错误码全集

### 2.1 请求参数类错误（HTTP 400）

| error.code | error.type | 说明 | 可能原因 | 处理建议 |
|------------|------------|------|---------|---------|
| `missing_required_param` | `invalid_request_error` | 缺少必填参数 | 未传入 `model` 或 `messages` | 检查请求体，确保包含 `model` 和 `messages` |
| `empty_audio_file` | `invalid_request_error` | 音频数据为空 | 文件读取失败或文件为空 | 检查音频文件路径是否正确，文件是否存在 |
| `invalid_audio_format` | `invalid_request_error` | 不支持的音频格式 | 传入了非支持格式的音频 | 转换为 `wav` / `mp3` / `m4a` / `webm` / `flac` |
| `corrupted_audio` | `invalid_request_error` | 音频损坏或无法解码 | 音频文件损坏或编码不标准 | 重新准备音频文件，或用音频软件重新转码 |
| `audio_duration_exceeded` | `invalid_request_error` | 音频时长超过限制 | 单个音频超过模型上限（`Qwen3-ASR-1.7B` 最大 150s，`xfyun-asr` 最大 5h） | 缩短音频时长，或使用静音检测分块策略 |
| `no_speech_detected` | `invalid_request_error` | 未检测到有效人声 | 音频中无语音内容 | 检查音频是否包含有效语音 |
| `language_detection_failed` | `invalid_request_error` | 语种检测失败 | 音频内容无法判断语种 | 明确指定语种，或更换音频内容 |
| `invalid_audio_url` | `invalid_request_error` | audio_url 格式不正确 | Base64 编码错误或 MIME 类型缺失 | 确保格式为 `data:audio/{type};base64,{data}` |
| `invalid_base64` | `invalid_request_error` | Base64 编码无效 | 音频数据编码有误 | 重新对音频文件进行 Base64 编码 |
| `audio_too_large` | `invalid_request_error` | 文件过大（HTTP 413） | 音频文件超过服务端限制（`xfyun-asr` 最大 500MB） | 压缩采样率、降低位深或分段上传 |
| `invalid_temperature` | `invalid_request_error` | temperature 参数不合法 | 值超出范围 | ASR 建议使用默认值 `0.01` |
| `invalid_max_tokens` | `invalid_request_error` | max_tokens 参数不合法 | 值小于等于 0 或超出上限 | 设置合理范围，如 `10000` |

### 2.2 认证授权类错误（HTTP 401 / 403）

| HTTP 状态码 | error.code | error.type | 说明 | 处理建议 |
|------------|------------|------------|------|---------|
| 401 | `invalid_api_key` | `authentication_error` | API Key 无效 | 检查 Authorization 头格式是否正确（`Bearer <key>`），确认 key 未过期或未撤销 |
| 401 | `missing_api_key` | `authentication_error` | 缺少 API Key | 在请求头中添加 `Authorization: Bearer <api-key>` |
| 401 | `expired_api_key` | `authentication_error` | API Key 已过期 | 联系管理员获取新的 API Key |
| 403 | `model_access_denied` | `permission_error` | 无权访问该模型 | 确认账号有 ASR 模型的使用权限，联系管理员 |
| 403 | `ip_blocked` | `permission_error` | IP 地址被禁止访问 | 确认请求来源 IP 在白名单中 |

### 2.3 限流类错误（HTTP 429）

| error.code | error.type | 说明 | 处理建议 |
|------------|------------|------|---------|
| `rate_limit_exceeded` | `rate_limit_error` | 调用频率超限 | 降低请求频率，实现请求队列或限流机制 |
| `daily_quota_exceeded` | `rate_limit_error` | 日调用量超限 | 联系管理员提升配额或等待次日配额重置 |
| `concurrent_request_limit` | `rate_limit_error` | 并发请求数超限 | 减少并发请求数，排队串行处理 |

### 2.4 服务端错误（HTTP 500 / 503 / 504）

| HTTP 状态码 | error.code | error.type | 说明 | 处理建议 |
|------------|------------|------------|------|---------|
| 500 | `audio_upload_failed` | `api_error` | 音频上传失败 | 网络抖动，稍后重试 |
| 500 | `transcription_failed` | `api_error` | 识别引擎失败 | 服务端处理异常，稍后重试 |
| 500 | `internal_error` | `api_error` | 服务内部错误 | 查看服务端日志，联系技术支持 |
| 503 | `model_not_found` | `new_api_error` | 没有可用渠道承载该模型 | **不是**上游故障，是配置问题：见下方说明 |
| 503 | `service_unavailable` | `api_error` | 服务不可用 | 服务正在重启或维护，等待恢复后重试 |
| 504 | `processing_timeout` | `api_error` | 处理超时 | 音频文件过大，减小分块大小后重试 |
| 502 | `bad_gateway` | `api_error` | 网关错误 | 网关异常，稍后重试 |
| 502 | `upstream_error` | `api_error` | 上游服务错误 | ASR 引擎异常，稍后重试 |

### 2.5 `503 model_not_found` 的两种情形

异步任务端点的 `503` 有两种成因，处置完全不同。**`message` 是区分依据**：

| `message` | 成因 | 处置 |
|-----------|------|------|
| `No available channel for model X under group Y` | 网关完全没找到承载 `X` 的渠道 | 建渠道 / 启用渠道 / 调整分组 |
| `No available channel for model X under group Y: the model is claimed by a task plugin, which has no enabled channel serving it` | 模型名**是对的**（已被任务插件认领），但插件没有可用渠道 | 核对渠道状态、渠道分组、渠道「模型」列与提交值逐字一致 |

第二种常见于「模型名对了但服务端没配完」，详见 [ASR 异步任务接口文档](async-task) 的服务端配置指引。**改客户端代码对这两种都无用。**

---

## 三、异步任务端点的错误结构不同

本页第一节的错误响应格式适用于**同步**接口。异步任务端点（`/v1/audio/transcriptions/tasks`）有**两种**失败响应结构，解析器要同时兼容：

```json
// 任务链路自己的错误：扁平的 {code, message, data}
{ "code": "model_not_found", "message": "No available channel for model X under group default", "data": null }
```

```json
// 网关自身的拒绝：错误在 error 这一层
{ "error": { "code": "model_not_found", "message": "...", "type": "new_api_error" } }
```

**只读顶层的解析器会把第二种结构的错误码与描述都读成空**，看起来像「503 但什么信息都没有」。先判断有没有 `error` 字段，再决定从哪一层取。

异步端点的错误码全集见 [ASR 异步任务接口文档](async-task) 的错误码章节。

---

## 四、重试策略参考

```python
import time
import requests

def call_with_retry(url, headers, payload, max_retries=5):
    """带指数退避的重试机制"""
    for attempt in range(max_retries):
        try:
            response = requests.post(url, headers=headers, json=payload, timeout=300)
            if response.status_code == 200:
                return response.json()
            elif response.status_code in (401, 403, 400):
                # 不重试，直接返回错误
                return response.json()
            elif response.status_code == 429:
                # 限流：指数退避
                wait_time = min(2 ** attempt * 10, 300)
                print(f"触发限流，等待 {wait_time}s 后重试（第{attempt + 1}次）")
                time.sleep(wait_time)
            else:
                # 服务端错误：指数退避
                wait_time = min(2 ** attempt * 5, 60)
                print(f"服务端错误 {response.status_code}，等待 {wait_time}s 后重试（第{attempt + 1}次）")
                time.sleep(wait_time)
        except requests.exceptions.Timeout:
            wait_time = min(2 ** attempt * 10, 300)
            print(f"请求超时，等待 {wait_time}s 后重试（第{attempt + 1}次）")
            time.sleep(wait_time)
        except requests.exceptions.RequestException as e:
            print(f"网络异常: {e}")
            wait_time = min(2 ** attempt * 5, 60)
            time.sleep(wait_time)
    return {"error": {"message": "重试次数耗尽，请检查服务状态"}}
```

| 场景 | HTTP 状态码 | 建议重试次数 | 退避策略 |
|------|------------|------------|---------|
| 限流 | 429 | 最多 5 次 | 指数退避，首次 10s，最大 300s |
| 服务端错误 | 500 / 502 / 503 / 504 | 最多 3 次 | 指数退避，首次 5s，最大 60s |
| 网络超时 | — | 最多 3 次 | 指数退避，首次 10s，最大 300s |
| 参数错误 | 400 / 401 / 403 | 不重试 | 修正参数后再发 |

---

## 五、常见问题排查

### Q0: `Qwen3-ASR-1.7B` 返回 `audio_duration_exceeded`

`Qwen3-ASR-1.7B` 最大仅支持 **150 秒（2.5 分钟）** 的音频。超出此限制请改用 `xfyun-asr`（支持最长 5 小时）。

```python
# 根据音频时长自动选择模型
import soundfile as sf

audio_data, samplerate = sf.read(audio_path, dtype="float32")
duration_sec = len(audio_data) / samplerate

if duration_sec <= 150:
    model = "Qwen3-ASR-1.7B"
else:
    model = "xfyun-asr"
```

### Q1: 返回 `invalid_audio_format`
请确认音频格式为以下之一：`wav`、`mp3`、`m4a`、`webm`、`flac`。如果音频是由微信或其他工具产生，可先转码：
```bash
# 使用 ffmpeg 转码为 wav
ffmpeg -i original.amr -ar 16000 -ac 1 output.wav
```

### Q2: 返回 `audio_too_large`
降低音频码率或采样率：
```bash
# 降低采样率为 16kHz，单声道
ffmpeg -i large_audio.mp3 -ar 16000 -ac 1 reduced_audio.mp3
```

### Q3: 返回 `no_speech_detected`
- 确认音频中确实包含人声（最低信噪比要求）
- 音频中静音过多可能导致检测失败
- 检查音频是否为纯音乐或无声文件

### Q4: 长时间无响应（超时）
- 可能是音频文件过大，建议分块后重试
- 检查网络连接是否稳定
- 长音频建议控制在 1 小时以内

### Q5: 返回 `service_unavailable`
- 服务可能正在重启，请等待 1-2 分钟后重试
- 可以访问 `/health` 接口确认服务状态

### Q6: 异步任务提交返回 `503`，message 里有「被某个任务插件认领」

这是**配置问题**，不是服务故障，也不是上游故障。网关已经认识这个模型，但插件没有可用的渠道来执行它。

按顺序核对三件事：

1. **渠道状态**是不是「启用」；
2. **渠道分组**是否覆盖调用方令牌所在的分组（message 里的 `under group Y` 指的就是调用方所在的组）；
3. **渠道「模型」列的值**与调用方提交的 `model` **逐字一致**——大小写差异、多余空格都算不一致。

三处都没问题，再确认调用方提交的是**渠道暴露名**而不是插件声明名。完整说明见 [ASR 异步任务接口文档](async-task) 的服务端配置指引。

> 同理，旧版网关把这个原因报成 `400` + `prompt is required`。**不要去补 `prompt`**——异步转写任务不读这个字段，它只是旧网关的通用任务校验在报错。
