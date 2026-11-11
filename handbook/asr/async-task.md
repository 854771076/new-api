# ASR 异步任务接口文档

> **接口**：`POST /v1/audio/transcriptions/tasks`（提交）、`GET /v1/audio/transcriptions/tasks/{task_id}`（查询）
> **协议**：OpenAI 兼容的异步任务语义
> **适用模型**：向管理员索取 ASR 模型的**调用名**（如 `xfyun-ifasr-llm-v1`），提交时填这个值
> **配套**：同步转写接口见 [ASR 语音识别](README)；错误码全集见 [ASR 错误码参考](error-codes)

**一至十节是业务接入需要知道的内容**；附录 A、附录 B 是服务端配置与排障，只有部署与运维需要看。

---

## 一、什么时候用异步接口

同步转写接口（`POST /v1/chat/completions`）会一直阻塞到转写完成。长音频（会议录音、访谈、讲座）在转写期间会持续占用一个 HTTP 连接，网关的读取超时、客户端的代理超时都可能先于转写结束触发。

异步任务接口把转写托管成网关的任务：客户端提交音频后立刻拿到任务号，之后自己按需轮询任务状态。转写由网关后台调度执行，客户端连接不会被长时间占用。

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
| `file` | file | 是 | 音频文件。内容原样转发，不做转码或重编码 |
| `model` | string | 是 | 模型标识，填管理员给的调用名，例如 `xfyun-ifasr-llm-v1` |
| `roleType` | string | 否 | 说话人分离开关（非负整数）。需要区分说话人时才传 |
| `roleNum` | string | 否 | 说话人个数（非负整数），`0` 表示由服务端自动判断。不传则由服务端决定 |

> **重要**：异步端点**只接受文件上传**。Base64 与 HTTP(S) URL 两种音频来源仅同步接口提供，需要时请使用同步接口。

> **`model` 填哪个名字**：填管理员给你的**调用名**，不是模型的技术名称。填错会得到 `503` / `model_not_found`。

> **文件名要求**：文件名要带扩展名，服务端按扩展名判定音频格式（`wav` / `mp3` / `flac` / `m4a` / `ogg` / `opus` / `aac` 等）。文件名没有扩展名时，服务端会按 MIME 类型补一个。

> **要说话人怎么办**：是否分离说话人取决于账号是否开通该能力。开通后标签形如 `[说话人1 00:00:00.000-00:00:03.000]`，`segments[].speaker` 也会带上说话人标识；未开通时标签里只有时间戳 `[00:00:00.000-00:00:03.000]`。**以返回结果为准。**

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

以下为实测响应（82.152 秒音频）：

```json
{
  "id": "task_0708RiNIfVqy6tkNWMuYAu8ZBrb6Ma7g",
  "object": "audio.transcription",
  "status": "succeeded",
  "progress": 100,
  "created_at": 1793171022,
  "completed_at": 1793171036,
  "model": "xfyun-ifasr-llm-v1",
  "text": "[说话人1 00:00:06.550-00:01:22.130] 执法灵和已对对中医是他们么有嗯尽量的商业价值……",
  "duration": 82.15,
  "segments": [
    { "id": 0, "start": 6.55, "end": 82.13, "text": "执法灵和已对对中医是他们么有嗯尽量的商业价值……", "speaker": "0" }
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
| `text` | string | **转写文本**（任务成功后返回）。每段一行，行首是时间戳标签 `[说话人N 起始-结束]`，`HH:MM:SS.mmm`；未开启说话人分离时标签里只有时间戳 `[00:00:00.000-00:00:29.930]` |
| `duration` | number | 音频时长（秒） |
| `segments` | array | 分段列表，每段含 `id` / `start` / `end`（秒）/ `text` / `speaker`（上游的说话人标识，未开启说话人分离时为空字符串） |
| `language` | string | 语种标识，未返回时为空字符串 |

> **两个说话人编号不是一回事**：`text` 里的「说话人N」按说话人首次出现的顺序编号（从 1 起），而 `segments[].speaker` 是上游返回的原始标识。上例里标签是「说话人1」而 `speaker` 是「0」——**不要拿两者互相对照**。

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
    "code": "invalid_request",
    "message": "iFLYTEK transcription failed: audio duration exceeds the limit (failType: 4)"
  }
}
```

`error.code` 是**语义码**，`error.message` 是可直接展示给用户看的失败原因。

语义码是调用方唯一的结构化判据——任务失败同样返回 `200`，没法用 HTTP 状态码区分原因。取值与网关其它接口的错误码同一套：

| error.code | 含义 | 客户端应对 |
|-----------|------|-----------|
| `invalid_request` | 音频或参数本身有问题（时长超限、静音文件、格式不支持等） | 换素材或改参数后重提 |
| `access_denied` | 账号缺少该能力（未开通角色分离、翻译、质检等） | 找管理员开通 |
| `channel:invalid_key` | 服务端配置的讯飞凭据或签名有误 | 找管理员核对密钥 |
| `rate_limit_exceeded` | 上游频率限制 | 退避后重试 |
| `do_request_failed` | 上游故障 | 可重试 |
| `task_not_exist` | 结果不会再出现（上游订单已不存在） | 重新提交 |

> **上游原始码在 `message` 里**：`iFLYTEK error 100004: ...`（业务码）或 `... (failType: 4)`（订单失败类型）。需要精确到上游取值时看 `message`；要做分支决策用 `error.code`。

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

## 六、计费

按**音频时长**计费，扣费与音频长度成正比（与同步接口的音频 token 计费是两条独立口径）。单价由管理员配置，任务日志里能看到实际时长与扣费明细。

---

## 七、与同步接口的差异

| 维度 | 同步 `POST /v1/chat/completions` | 异步任务接口 |
|------|------------------------------|------------|
| 音频来源 | multipart / Base64 / HTTP(S) URL | 仅 multipart 文件 |
| 返回方式 | 阻塞返回转写文本 | 返回任务号，轮询取结果 |
| 响应结构 | OpenAI `chat.completion` | `audio.transcription` |
| 分段信息 | `verbose_json` 才有 | 始终返回 `segments` |
| 计费口径 | 音频 token（每分钟 1000 token） | 音频时长 |
| 任务落库 | 否 | 是，任务日志与后台看板可见 |
| 适合场景 | 短音频、实时需求 | 长音频、批量、可容忍延迟 |

---

## 八、错误处理

客户端按 **HTTP 状态码 + `message`** 处理即可，不需要解析上游业务码。

### 8.1 提交阶段

| HTTP 状态码 | error.code | 说明 | 处理建议 |
|------------|------------|------|---------|
| 400 | （空） | 请求体不合规。`message` 会说明具体原因：不是 multipart 上传 / 缺 `file` 字段 / `roleType` 不是非负整数 | 按 `message` 改请求体 |
| 400 | `model_price_error` | 该模型没有配置计费 | 找管理员处理 |
| 401 | `invalid_api_key` | API Key 无效 | 检查 Authorization 头 |
| 403 | `model_access_denied` | 无权访问该模型 | 找管理员开通 |
| 415 | （空） | 用了非 multipart 的表单体 | 改用 `multipart/form-data` |
| 429 | `rate_limit_exceeded` | 调用频率超限 | 降低提交频率后重试 |
| 502 | `fail_to_fetch_task` | 服务端提交失败 | 可重试 |
| 503 | `model_not_found` | 该模型当前不可用 | 核对 `model` 是否填对，找管理员确认 |

### 8.2 查询阶段

| HTTP 状态码 | code | 说明 | 处理建议 |
|------------|------|------|---------|
| 400 | `task_not_exist` | 任务不存在、不属于当前用户，或结果不可取回 | 核对任务号；确认是用提交时拿到的同一个令牌查询 |
| 400 | `task_plugin_system_disabled` | 服务端的异步任务能力已关闭 | 找管理员启用 |
| 501 | `not_implemented` | 当前平台不支持音频任务渲染 | 找管理员检查 |

> **注意**：提交阶段与查询阶段的错误响应结构不同。提交阶段是 `{"error":{"code":"...","message":"...","type":"..."}}`，查询阶段是 `{"code":"...","message":"...","data":null}`。客户端做错误处理时两种结构都要兼容。

> 任务失败时接口仍然返回 `200`，失败信息在 `status: "failed"` 的 `error` 里：`error.code` 是语义码（见 4.4），`error.message` 是可直接展示的原因，上游原始码也在 `error.message` 里。

---

## 九、限制与注意事项

| 项 | 限制 |
|---|---|
| 音频来源 | 仅 multipart 文件上传，不支持 Base64 与 URL |
| 单文件大小 | 默认 64 MB |
| 可计费时长 | 上限 3600 秒（1 小时），超出部分不额外计价 |
| 音频格式 | 由上游判定，客户端应使用带扩展名的常见格式 |
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
| 查询 | 任务不存在 | 400 | 否 | 核对任务号与令牌 |
| 查询 | 任务失败 | 200 + `status=failed` | 视原因 | 按 `error.message` 判断：上游临时错误可重试，音频本身不合法需换文件 |

> **提交重试要格外小心**：提交超时不代表上游没收到，可能已经在转写。盲目重试会产生重复任务并重复计费。建议提交阶段设置宽松的读取超时，或在重试前先确认没有拿到任务号。

---

## 附录 A：服务端配置指引（运维向）

> 业务方只需要知道「提交哪个 `model`」和「拿到任务号后怎么轮询」，本节不需要了解。

配置分四步，顺序不能颠倒：

| 步骤 | 做什么 | 在哪做 | 漏了会怎样 |
|------|--------|--------|-----------|
| 1 | 开启任务插件系统（配置项 `TaskPluginEnabled`） | 系统设置 | 提交返回 `400` / `task_plugin_system_disabled` |
| 2 | 建一条 ASR 任务插件渠道 | 渠道 → 添加渠道 | 提交返回 `503` / `model_not_found` |
| 3 | 配按时长计费的计费表达式 | 系统设置 | 扣费与音频长度脱钩（退化成固定额度） |
| 4 | 跑一遍自检 | 见 A.5 | 问题带到生产才暴露 |

### A.1 建渠道

「渠道」→「添加渠道」，按下表填写：

| 字段 | 取值 |
|------|------|
| 类型 | `Task Plugin`（渠道类型 61） |
| 插件 | `xfyun-asr`（选好后后台自动带出插件名、模型列表、默认地址） |
| 密钥 | **一个字段，三段**，竖线连接：`应用ID\|应用密钥\|访问密钥ID`，见 A.2 |
| 模型 | 渠道暴露名，例如 `xfyun-ifasr-llm-v1`。**这是客户端要提交的值** |
| 模型映射 | `{"<渠道暴露名>": "ifasr-llm-v1"}`，把暴露名翻译成插件声明名 |
| 分组 | 至少覆盖调用方令牌所在的分组 |
| 状态 | 启用 |
| 基础地址 | 留空，用插件声明的默认值 |

> 类型 61 的渠道需要 `task_plugin.bind` 权限（超管默认具备），否则保存会提示权限不足。

### A.2 密钥怎么填

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

### A.3 三个模型名不要混

这是本功能最容易出错的地方，三者用途不同：

| 名称 | 值 | 谁定义 | 用途 |
|------|-----|--------|------|
| 插件 key | `xfyun-asr` | 插件作者（`meta.key`） | 插件身份，决定用哪个插件 |
| 插件声明名 | `ifasr-llm-v1` | 插件作者（`meta.models`） | 上游认的模型标识；协议索引的键 |
| 渠道暴露名 | 如 `xfyun-ifasr-llm-v1` | 运维在后台填 | **客户端提交的名字**；能力表按它建条目 |

关键事实：**能力表（`abilities`）只按渠道「模型」列建条目**。插件声明名不在能力表里，渠道暴露名也不在插件里，两者靠渠道的「模型映射」打通。因此客户端提交暴露名，能力表查得到；提交插件声明名则查不到，直接 503。

### A.4 计费表达式

```
billing_setting.plugin_billing_expr["xfyun-asr::ifasr-llm-v1"] = tier("base", u("duration") * 0.00003325)
```

key 的格式是 `<插件 key>::<模型名>`，模型名要与渠道「模型」列一致。写错不会报错，只是表达式对不上任何模型，退回按固定额度计费——**扣费与音频长度脱钩**，长音频被严重少扣。

### A.5 自检清单

在交给业务方之前，服务端先跑一遍：

1. 提交一条几秒的音频，确认 `200` 且带 `id`；
2. **用业务方实际会用的 `model` 再提交一次**（不是插件 key、不是同步接口的模型别名），确认同样 `200`；
3. 轮询到 `succeeded`，确认 `text` 非空、`duration` 与音频实际长度一致；
4. 任务日志里确认 `billing_mode` 为 `tiered_expr`，`usage_facts.duration` 等于音频秒数；
5. 用 1 分钟与 30 秒两条音频各跑一次，确认扣费与时长成正比。

### A.6 常见配置错误对照

| 现象 | 真实原因 | 怎么改 |
|------|---------|--------|
| `503` / `model_not_found`，message 说模型被任务插件认领但没有启用渠道承载 | 渠道没启用 / 分组不覆盖调用方 / 「模型」列与提交值不一致 | 启用渠道；确认分组；逐字核对 |
| `400` / `invalid_request` / `prompt is required` | 不是要补 prompt，是旧网关的通用任务校验在报错，真实原因同上 | 升级网关后再排查 |
| `fail_to_fetch_task`（状态码取上游） | 密钥三段填错或填反 | 按 A.2 核对三段顺序 |

---

## 附录 B：错误形态细节（运维向）

### B.1 `503 model_not_found` 的三种成因

**`message` 是区分依据**：

| `message` | 成因 | 处置 |
|-----------|------|------|
| `No available channel for model X under group Y` | 网关完全没找到承载 `X` 的渠道 | 建渠道 / 启用渠道 / 调整分组 |
| `No available channel for model X under group Y: the model is claimed by a task plugin, which has no enabled channel serving it` | 模型名**被任务插件认领**，但插件没有可用渠道 | 核对渠道状态、分组、渠道「模型」列与提交值逐字一致 |
| `model "X" is not served by any task plugin on the audio transcription endpoint` | 提交的是**同步接口的模型**，任务插件不提供它 | 换用渠道暴露名 |

三种的**响应结构也不同**：第三种是扁平的 `{code, message, data}`，前两种把错误码包在 `error` 里。客户端解析器要兼容两种。

### B.2 轮询期间的上游业务码

转写期间上游可能回业务码而不是结果。网关按「是否可自愈」处理，客户端只在任务落到 `failed` 时才需要区分：

| 上游业务码 | 含义 | 网关的处理 | 客户端看到 |
|------------|------|-----------|-----------|
| `100012` | 请求超过频率限制 | 退避后继续轮询 | 任务仍为 `in_progress` |
| `100013` | 订单未完成 | 继续轮询 | 任务仍为 `in_progress` |
| 其它非 `000000` 码 | 上游故障（订单不存在、查询错误、未知异常等） | 判为终态失败 | `status: "failed"`，`error.code` 为上游业务码 |

> 上游业务码不直接作为 HTTP 错误码返回：它们出现在任务失败结果的 `error.code` 里。

### B.3 异步端点**的请求阶段**不产出同步接口的语义码

同步接口的 `empty_audio_file` / `invalid_audio_format` / `audio_duration_exceeded` 这类语义码，异步端点在请求阶段不产出：网关把插件解码器抛出的错误统一包装成 `plugin_request_invalid`，或直接中止并留下空 `code`。**插件在请求阶段无法自定义客户端可见的错误码。**

任务失败阶段不受这条限制：`error.code` 由插件给出，是 4.4 里的语义码。

| 请求侧问题 | 异步端点实际返回 |
|-----------|----------------|
| 缺少 `file` 字段 / 音频文件为空 | HTTP 400，`code` 为**空字符串**或 `plugin_request_invalid`，`message` 里带具体原因 |
| `roleType` / `roleNum` 不是非负整数 | 同上，`message` 说明是哪个参数 |
| 模型没有可用渠道 | HTTP 503，`model_not_found` |
