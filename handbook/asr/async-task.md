# ASR 异步任务接口文档

> **接口**：`POST /v1/audio/transcriptions/tasks`（提交）、`GET /v1/audio/transcriptions/tasks/{task_id}`（查询）
> **协议**：OpenAI 兼容的异步任务语义
> **适用模型**：ASR 任务插件声明的模型名（插件 key `xfyun-asr`，声明名 `ifasr-llm-v1`）。**客户端实际提交的是渠道「模型」列里的值**，见 [〇、服务端配置指引](#〇服务端配置指引)
> **配套**：同步转写接口见 [ASR 语音识别](README)；错误码全集见 [ASR 错误码参考](error-codes)

---

## 〇、服务端配置指引

> 这一节面向部署与运维。业务方只需要知道「提交哪个 `model`」和「拿到任务号后怎么轮询」，配置细节不需要了解。

配置分四步，顺序不能颠倒：

| 步骤 | 做什么 | 在哪做 | 漏了会怎样 |
|------|--------|--------|-----------|
| 1 | 开启任务插件系统（配置项 `TaskPluginEnabled`） | 系统设置 | 提交返回 `400` / `task_plugin_system_disabled` |
| 2 | 建一条 ASR 任务插件渠道 | 渠道 → 添加渠道 | 提交返回 `503` / `model_not_found` |
| 3 | 配按时长计费的计费表达式 | 系统设置 | 扣费与音频长度脱钩（退化成固定额度） |
| 4 | 跑一遍自检 | 见 0.5 | 问题带到生产才暴露 |

### 0.1 建渠道

「渠道」→「添加渠道」，按下表填写：

| 字段 | 取值 |
|------|------|
| 类型 | `Task Plugin`（渠道类型 61） |
| 插件 | `xfyun-asr`（选好后后台自动带出插件名、模型列表、默认地址） |
| 密钥 | **一个字段，三段**，竖线连接：`应用ID\|应用密钥\|访问密钥ID`，见 0.2 |
| 模型 | 渠道暴露名，例如 `xfyun-ifasr-llm-v1`。**这是客户端要提交的值** |
| 模型映射 | `{"<渠道暴露名>": "ifasr-llm-v1"}`，把暴露名翻译成插件声明名 |
| 分组 | 至少覆盖调用方令牌所在的分组 |
| 状态 | 启用 |
| 基础地址 | 留空，用插件声明的默认值 |

> 类型 61 的渠道需要 `task_plugin.bind` 权限（超管默认具备），否则保存会提示权限不足。

### 0.2 密钥怎么填

后台「密钥」只有**一个输入框**，但插件需要三样凭据。用竖线 `|` 连成一行，顺序固定：

```
应用ID|应用密钥|访问密钥ID
```

| 段位 | 是什么 | 特征 |
|------|--------|------|
| 第 1 段 | appId | 短，一般 8 位十六进制 |
| 第 2 段 | apiSecret（HMAC 签名密钥） | 较长 |
| 第 3 段 | accessKeyId（讯飞 APIKey） | 长串，通常 32 位十六进制 |

**最容易填反的是第 2、3 段**：配置文件的字段名里第 3 段叫 `access_key_id`，而第 2 段叫 `api_key`——**字段名里的 `api_key` 是签名密钥（第 2 段）**。填错时后台保存不报错，要到提交任务才失败。

三段与部署配置文件字段的对应关系：

| 配置文件字段 | 填到第几段 |
|-------------|-----------|
| `app_id` | 第 1 段 |
| `api_key` | 第 2 段 |
| `access_key_id` | 第 3 段 |

### 0.3 三个模型名不要混

这是本功能最容易出错的地方，三者用途不同：

| 名称 | 值 | 谁定义 | 用途 |
|------|-----|--------|------|
| 插件 key | `xfyun-asr` | 插件作者（`meta.key`） | 插件身份，决定用哪个插件 |
| 插件声明名 | `ifasr-llm-v1` | 插件作者（`meta.models`） | 上游认的模型标识；协议索引的键 |
| 渠道暴露名 | 如 `xfyun-ifasr-llm-v1` | 运维在后台填 | **客户端提交的名字**；能力表按它建条目 |

关键事实：**能力表（`abilities`）只按渠道「模型」列建条目**。插件声明名不在能力表里，渠道暴露名也不在插件里，两者靠渠道的「模型映射」打通。因此客户端提交暴露名，能力表查得到；提交插件声明名则查不到，直接 503。

### 0.4 计费表达式

```
billing_setting.plugin_billing_expr["xfyun-asr::ifasr-llm-v1"] = tier("base", u("duration") * 0.00003325)
```

key 的格式是 `<插件 key>::<模型名>`，模型名要与渠道「模型」列一致。写错不会报错，只是表达式对不上任何模型，退回按固定额度计费——**扣费与音频长度脱钩**，长音频被严重少扣。

### 0.5 自检清单

在交给业务方之前，服务端先跑一遍：

1. 提交一条几秒的音频，确认 `200` 且带 `id`；
2. **用业务方实际会用的 `model` 再提交一次**（不是插件 key、不是同步接口的模型别名），确认同样 `200`；
3. 轮询到 `succeeded`，确认 `text` 非空、`duration` 与音频实际长度一致；
4. 任务日志里确认 `billing_mode` 为 `tiered_expr`，`usage_facts.duration` 等于音频秒数；
5. 用 1 分钟与 30 秒两条音频各跑一次，确认扣费与时长成正比。

### 0.6 常见配置错误对照

| 现象 | 真实原因 | 怎么改 |
|------|---------|--------|
| `503` / `model_not_found`，message 说模型被任务插件认领但没有启用渠道承载 | 渠道没启用 / 分组不覆盖调用方 / 「模型」列与提交值不一致 | 启用渠道；确认分组；逐字核对 |
| `400` / `invalid_request` / `prompt is required` | 不是要补 prompt，是旧网关的通用任务校验在报错，真实原因同上 | 升级网关后再排查 |
| `fail_to_fetch_task`（状态码取上游） | 密钥三段填错或填反 | 按 0.2 核对三段顺序 |

---

## 一、接口说明

同步转写接口（`POST /v1/chat/completions`）会一直阻塞到转写完成。长音频（会议录音、访谈、讲座）在转写期间会持续占用一个 HTTP 连接，网关的读取超时、客户端的代理超时都可能先于转写结束触发。

异步任务接口把转写托管成网关的任务：客户端提交音频后立刻拿到任务号，之后自己按需轮询任务状态。转写由网关后台调度执行，客户端连接不会被长时间占用。

**什么时候用哪个**：

| 场景 | 推荐接口 |
|------|---------|
| 短音频（几秒到几分钟）、一次、需要立即拿到文本 | 同步 `POST /v1/chat/completions` |
| 长音频、大批量、需要排队或与其它业务并行 | 异步 `POST /v1/audio/transcriptions/tasks` |
| 客户端会被代理/网关超时掐断 | 异步任务接口 |
| 需要在后台看板跟踪每条转写的状态与用量 | 异步任务接口（任务落库、任务日志可见） |

---

## 二、接口一览

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/v1/audio/transcriptions/tasks` | 提交转写任务，返回任务号 |
| `GET` | `/v1/audio/transcriptions/tasks/{task_id}` | 查询任务状态与转写结果 |

两个端点都要求 `Authorization: Bearer <api-key>`，请求地址与同步接口一致（见 [ASR 语音识别](README) 的「请求说明」）。

---

## 三、提交任务

### 3.1 请求头

| 请求头字段 | 类型 | 必传 | 说明 |
|-----------|------|------|------|
| `Authorization` | string | 是 | 格式：`Bearer <api-key>` |
| `Content-Type` | string | 是 | 固定值：`multipart/form-data`（由 HTTP 客户端自动生成 boundary） |

### 3.2 请求体（`multipart/form-data`）

| 字段 | 类型 | 必传 | 说明 |
|------|------|------|------|
| `file` | file | 是 | 音频文件。二进制内容会被原样转发给上游，不做转码或重编码 |
| `model` | string | 是 | 模型标识，填**渠道「模型」列里的值**（渠道暴露名），例如 `xfyun-ifasr-llm-v1`。见 0.3 |

> **重要**：异步端点**只接受文件上传**。Base64 与 HTTP(S) URL 两种音频来源仅同步接口提供，需要时请使用同步接口。

> **`model` 填哪个名字**：客户端提交的是**渠道暴露名**（运维在后台「模型」列填的值），不是插件声明名 `ifasr-llm-v1`。能力表只按暴露名建条目，提交声明名会得到 `503` / `model_not_found`。三个名字的区别见 0.3。

> **文件名要求**：上游按文件名的扩展名判定音频格式。客户端文件名缺扩展名时，网关会按 MIME 类型补一个上游支持的扩展名（`wav` / `mp3` / `flac` / `m4a` / `ogg` / `opus` / `aac` / `pcm` / `speex`）。

### 3.3 请求示例

#### curl

```bash
curl -X POST http://10.255.87.24:30000/v1/audio/transcriptions/tasks \
  -H "Authorization: Bearer sk-YOUR-API-KEY" \
  -F "model=xfyun-ifasr-llm-v1" \
  -F "file=@meeting.wav"
```

#### Python

```python
import requests

BASE_URL = "http://10.255.87.24:30000"
API_KEY = "sk-YOUR-API-KEY"

with open("meeting.wav", "rb") as audio:
    response = requests.post(
        f"{BASE_URL}/v1/audio/transcriptions/tasks",
        headers={"Authorization": f"Bearer {API_KEY}"},
        files={"file": ("meeting.wav", audio, "audio/wav")},
        data={"model": "xfyun-ifasr-llm-v1"},
        timeout=60,
    )
response.raise_for_status()
print(response.json()["id"])
```

#### Java（OkHttp）

```java
RequestBody body = new MultipartBody.Builder()
        .setType(MultipartBody.FORM)
        .addFormDataPart("model", "xfyun-ifasr-llm-v1")
        .addFormDataPart("file", "meeting.wav",
                RequestBody.create(new File("meeting.wav"), MediaType.parse("audio/wav")))
        .build();

Request request = new Request.Builder()
        .url("http://10.255.87.24:30000/v1/audio/transcriptions/tasks")
        .header("Authorization", "Bearer " + API_KEY)
        .post(body)
        .build();

try (Response response = client.newCall(request).execute()) {
    String json = response.body().string();
    // 从 json 中取出 id 字段，作为后续查询用的 task_id
}
```

### 3.4 提交响应

```json
{
  "id": "task_MIJdP5U3BCZ3GVLd8MVvRERRnPnOGrbQ",
  "object": "audio.transcription",
  "status": "queued",
  "progress": 0,
  "model": "xfyun-ifasr-llm-v1",
  "created_at": 1793025143
}
```

| 参数名 | 类型 | 说明 |
|--------|------|------|
| `id` | string | 任务号，公开标识，查询接口使用它 |
| `object` | string | 固定值：`audio.transcription` |
| `status` | string | 提交后为 `queued` |
| `progress` | integer | 完成百分比（0–100），刚提交时为 0 |
| `model` | string | 模型标识 |
| `created_at` | integer | 任务创建时间戳（Unix 秒） |

> HTTP 状态码为 `200`。**提交成功即表示任务已被接受**，即使此时还没有开始转写。

> **提交响应与查询响应同形**：两个端点返回同一套字段（查询成功后多出 `text` / `duration` / `segments` / `language`，并补上 `completed_at`），客户端一套解析逻辑即可覆盖两个端点。

---

## 四、查询任务

### 4.1 请求

```bash
curl http://10.255.87.24:30000/v1/audio/transcriptions/tasks/task_MIJdP5U3BCZ3GVLd8MVvRERRnPnOGrbQ \
  -H "Authorization: Bearer sk-YOUR-API-KEY"
```

### 4.2 成功响应

```json
{
  "id": "task_MIJdP5U3BCZ3GVLd8MVvRERRnPnOGrbQ",
  "object": "audio.transcription",
  "status": "succeeded",
  "progress": 100,
  "created_at": 1793025143,
  "completed_at": 1793025149,
  "model": "xfyun-ifasr-llm-v1",
  "text": "那今天我们看到模型的本质，其实在很多时候是今天把我们人类的知识能够有效地聚集起来……",
  "duration": 29.952,
  "segments": [
    { "id": 0, "start": 0.42, "end": 29.93, "text": "那今天我们看到模型的本质……" }
  ],
  "language": ""
}
```

| 参数名 | 类型 | 说明 |
|--------|------|------|
| `id` | string | 任务号 |
| `object` | string | 固定值：`audio.transcription` |
| `status` | string | 任务状态，见下表 |
| `progress` | integer | 完成百分比（0–100） |
| `created_at` | integer | 任务创建时间戳（Unix 秒） |
| `completed_at` | integer | 任务完成时间戳（Unix 秒），任务未完成时不返回 |
| `model` | string | 模型标识 |
| `text` | string | **转写文本**（任务成功后返回） |
| `duration` | number | 音频时长（秒） |
| `segments` | array | 分段列表，每段含 `id` / `start` / `end`（秒）/ `text` |
| `language` | string | 语种标识，上游未返回时为空字符串 |

### 4.3 状态枚举

| status | 含义 | 客户端应对 |
|--------|------|-----------|
| `queued` | 已入队，尚未开始 | 继续轮询 |
| `in_progress` | 转写进行中 | 继续轮询 |
| `succeeded` | 转写成功 | 读取 `text` / `duration` / `segments` |
| `failed` | 转写失败 | 读取 `error`，按需重试 |
| `unknown` | 状态未知 | 继续轮询；长时间不变视为异常 |

### 4.4 失败响应

```json
{
  "status": "failed",
  "error": {
    "code": "100001",
    "message": "iFLYTEK transcription failed: upload failed (failType: 1)"
  }
}
```

`error.message` 带上游的失败原因，`error.code` 是上游业务码或语义化错误码。

---

## 五、完整轮询示例

### 5.1 Python

```python
import time
import requests

BASE_URL = "http://10.255.87.24:30000"
API_KEY = "sk-YOUR-API-KEY"
HEADERS = {"Authorization": f"Bearer {API_KEY}"}
POLL_INTERVAL = 3      # 轮询间隔（秒）
TIMEOUT = 300          # 等待终态的上限（秒）


def transcribe_async(audio_path):
    with open(audio_path, "rb") as audio:
        submitted = requests.post(
            f"{BASE_URL}/v1/audio/transcriptions/tasks",
            headers=HEADERS,
            files={"file": (audio_path.split("/")[-1], audio, "audio/wav")},
            data={"model": "xfyun-ifasr-llm-v1"},
            timeout=60,
        )
    submitted.raise_for_status()
    task_id = submitted.json()["id"]
    print(f"已提交任务 {task_id}")

    deadline = time.time() + TIMEOUT
    while time.time() < deadline:
        time.sleep(POLL_INTERVAL)
        result = requests.get(
            f"{BASE_URL}/v1/audio/transcriptions/tasks/{task_id}",
            headers=HEADERS,
            timeout=30,
        )
        result.raise_for_status()
        body = result.json()
        status = body.get("status")
        print(f"状态 {status}")
        if status == "succeeded":
            return body
        if status == "failed":
            raise RuntimeError(body.get("error", {}).get("message", "转写失败"))
    raise TimeoutError(f"等待任务终态超时：{task_id}")


if __name__ == "__main__":
    result = transcribe_async("meeting.wav")
    print(f"时长 {result['duration']}s，分段 {len(result['segments'])} 段")
    print(result["text"])
```

### 5.2 curl

```bash
TASK_ID=$(curl -s -X POST http://10.255.87.24:30000/v1/audio/transcriptions/tasks \
  -H "Authorization: Bearer sk-YOUR-API-KEY" \
  -F "model=xfyun-ifasr-llm-v1" \
  -F "file=@meeting.wav" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

echo "任务号 $TASK_ID"
while true; do
  BODY=$(curl -s http://10.255.87.24:30000/v1/audio/transcriptions/tasks/$TASK_ID \
    -H "Authorization: Bearer sk-YOUR-API-KEY")
  echo "$BODY" | head -c 200; echo
  case "$BODY" in
    *'"status":"succeeded"'*) break ;;
    *'"status":"failed"'*)    exit 1 ;;
  esac
  sleep 3
done
```

---

## 六、计费说明

异步任务按**音频时长**计费，与同步接口的音频 token 计费是两条独立的口径。

| 环节 | 计费行为 |
|------|---------|
| 提交 | 网关测量上传音频的可计费时长（按文件扩展名，缺失时按 part 的 `Content-Type`），按它预扣额度 |
| 结算 | 上游回传真实时长后覆盖提交期的估算值，重算并补收或退回差额；回传时长超过上限时按上限计费 |

> **需要管理员先配置计费表达式**：形如 `tier("base", u("duration") * 0.00003325)`，其中系数是**每秒多少美元**。表达式输出即美元，网关按 `输出 × 配额单价 × 分组倍率` 折算成配额。未配置表达式时，任务按固定额度（模型倍率）计费，与音频时长无关。表达式 key 的格式是 `<插件 key>::<插件声明名>`，例如 `xfyun-asr::ifasr-llm-v1`，见 0.4。

任务日志里能看到 `billing_mode=tiered_expr`、`matched_tier` 与 `usage_facts.duration`，便于对账。

**时长上限**：可计费时长以 3600 秒（1 小时）为上限。超过 1 小时的音频按 1 小时计价。

---

## 七、与同步接口的差异

| 维度 | 同步 `POST /v1/chat/completions` | 异步任务接口 |
|------|------------------------------|------------|
| 音频来源 | multipart / Base64 / HTTP(S) URL | 仅 multipart 文件 |
| 返回方式 | 阻塞返回转写文本 | 返回任务号，轮询取结果 |
| 响应结构 | OpenAI `chat.completion` | `audio.transcription` |
| 分段信息 | `verbose_json` 才有 | 始终返回 `segments` |
| 计费口径 | 音频 token（每分钟 1000 token） | 音频时长（表达式计价） |
| 任务落库 | 否 | 是，任务日志与后台看板可见 |
| 适合场景 | 短音频、实时需求 | 长音频、批量、可容忍延迟 |

---

## 八、错误码

### 8.1 提交阶段

| HTTP 状态码 | error.code | 说明 | 处理建议 |
|------------|------------|------|---------|
| 400 | （空） | 缺少 `file` 字段，或音频文件为空 | 检查 multipart 表单，确保 `file` 与 `model` 都已携带 |
| 400 | `model_price_error` | 模型没有配置可用的计费表达式 | 联系管理员配置该模型的计费表达式 |
| 400 | `plugin_usage_invalid` | 插件提交的用量事实不合法 | 检查插件与模型的用量口径是否匹配 |
| 400 | `invalid_request` | 请求格式不合法 | 检查请求体与 Content-Type |
| 401 | `invalid_api_key` | API Key 无效 | 检查 Authorization 头 |
| 403 | `model_access_denied` | 无权访问该模型 | 联系管理员开通 |
| 429 | `rate_limit_exceeded` | 调用频率超限 | 降低提交频率后重试 |
| 502 | `fail_to_fetch_task` | 上游提交失败 | 上游返回体在 `error.message` 里；可重试 |
| 503 | `model_not_found` | 没有可用渠道承载该模型 | 确认模型已绑定渠道、渠道已启用 |

### 8.2 查询阶段

| HTTP 状态码 | code | 说明 | 处理建议 |
|------------|------|------|---------|
| 400 | `task_not_exist` | 任务不存在、不属于当前用户，或结果不可取回 | 核对任务号；确认是用提交时拿到的同一个令牌查询 |
| 400 | `task_plugin_system_disabled` | 网关的任务插件系统已关闭 | 联系管理员启用任务插件 |
| 400 | `task_plugin_disabled` | 承载该任务的插件已被禁用 | 联系管理员启用对应插件 |
| 400 | `invalid_api_platform` | 任务记录的平台已无法解析 | 联系管理员核对任务与插件版本 |
| 501 | `not_implemented` | 当前平台不支持音频任务渲染 | 联系管理员检查插件与渠道绑定 |

> **注意**：提交阶段与查询阶段的错误响应结构不同。提交阶段是 `{"error":{"code":"...","message":"...","type":"..."}}`，查询阶段是 `{"code":"...","message":"...","data":null}`。客户端做错误处理时两种结构都要兼容。

---

## 九、限制与注意事项

| 项 | 限制 |
|---|---|
| 音频来源 | 仅 multipart 文件上传，不支持 Base64 与 URL |
| 单文件大小 | 默认 64 MB（由服务端 `MAX_FILE_DOWNLOAD_MB` 控制） |
| 可计费时长 | 上限 3600 秒（1 小时），超出部分不额外计价 |
| 音频格式 | 由上游判定，客户端应使用带扩展名的常见格式（`wav` / `mp3` / `flac` / `m4a` / `ogg` / `opus` / `aac`） |
| 轮询间隔 | 建议 3–5 秒；上游转写期间无需高频轮询 |
| 任务可见性 | 任务号只能由提交它的同一个 API Key 查询 |

---

## 十、重试策略

| 阶段 | 错误类型 | HTTP 状态码 | 重试 | 策略 |
|------|---------|------------|------|------|
| 提交 | 参数错误 | 400 | 否 | 修正请求后重发 |
| 提交 | 认证 / 权限错误 | 401 / 403 | 否 | 检查 API Key 与模型权限 |
| 提交 | 限流 | 429 | 是 | 指数退避重试，最多 5 次 |
| 提交 | 上游错误 | 502 / 503 | 是 | 指数退避重试，最多 3 次；注意**重试可能产生重复任务**，重试前先确认没有拿到任务号 |
| 查询 | 任务不存在 | 400 / 404 | 否 | 核对任务号与令牌 |
| 查询 | 任务失败 | 200 + `status=failed` | 视原因 | 按 `error.message` 判断：上游临时错误可重试，音频本身不合法需换文件 |

> **提交重试要格外小心**：提交超时不代表上游没收到，可能已经在转写。盲目重试会产生重复任务并重复计费。建议提交阶段设置宽松的读取超时，或在重试前先确认没有拿到任务号。
