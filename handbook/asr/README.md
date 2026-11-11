# ASR 语音识别接口文档

> **接口**：`POST /v1/chat/completions`
> **协议**：OpenAI 兼容（通过 `audio_url` content block 传递音频）
> **适用模型**：`Qwen3-ASR-1.7B`、`xfyun-asr`
> **本页范围**：**同步**转写接口。异步（任务式）转写见 [ASR 异步任务接口文档](async-task)；错误码全集见 [ASR 错误码参考](error-codes)

## 一、接口说明

该接口用于将音频文件转换为文本，支持短音频直接转录和长音频智能分块处理。接口采用 OpenAI 兼容的 `audio_url` content block 格式上传音频数据，服务端内部自动处理音频解码与识别。

**适用场景**：语音内容存档、字幕生成、语音指令识别、会议记录转写等。

### 1.1 两个接口怎么选

ASR 提供两个入口，**模型与端点是绑定的**：

| 接口 | 端点 | 可用模型 | 转写结果形态 |
|------|------|---------|-------------|
| 同步 | `POST /v1/chat/completions` | `Qwen3-ASR-1.7B`、`xfyun-asr` | OpenAI `chat.completion`，纯文本 |
| 异步任务 | `POST /v1/audio/transcriptions/tasks` | 管理员给的 ASR 调用名，如 `xfyun-ifasr-llm-v1` | `audio.transcription`，带时间戳标签与 `segments` |

**模型填错或用错端点，会得到 `503` / `model_not_found`。**

> 异步接口的完整说明（提交、轮询、计费、错误码）见 [ASR 异步任务接口文档](async-task)。

---

## 二、模型限制

> **重要**：不同模型对音频的时长和大小限制不同，请根据实际需求选择合适的模型。

| 模型 | 最大音频时长 | 最大文件大小 | 适用场景 |
|------|------------|------------|---------|
| `Qwen3-ASR-1.7B` | 150 秒（2.5 分钟） | 受 HTTP 请求体大小限制 | 短音频转写，如语音指令、语音搜索 |
| `xfyun-asr` | 5 小时（18000 秒） | 500 MB | 长音频转写，如会议记录、访谈、讲座 |

> **模型选择建议**：
> - 音频 ≤ 150 秒：优先使用 `Qwen3-ASR-1.7B`，推理速度快
> - 音频 150 秒 ~ 5 小时：使用 `xfyun-asr`
> - 音频 > 5 小时：使用 `xfyun-asr`，需配合静音检测分块策略

> **异步任务接口的 ASR 模型**（如 `xfyun-ifasr-llm-v1`）不在本表范围内：它只走异步任务端点，最长支持 5 小时音频，但**可计费时长以 3600 秒（1 小时）为上限**；输出带时间戳标签与分段信息。详见 [ASR 异步任务接口文档](async-task)。

---

## 三、请求说明

| 内容 | 说明 |
|------|------|
| 请求协议 | HTTP |
| 请求地址 | `http://10.255.87.24:30000/v1/chat/completions` |
| 认证方式 | `Authorization: Bearer <api-key>` |
| 字符编码 | UTF-8 |
| 响应格式 | JSON |
| 开发语言 | 任意（推荐使用 OpenAI SDK） |
| 最大音频时长 | 取决于所选模型，详见上方模型限制表 |
| 建议单次时长 | `Qwen3-ASR-1.7B` ≤ 150 秒；`xfyun-asr` ≤ 5 小时 |

---

## 四、请求参数

### 4.1 请求头（Headers）

| 请求头字段 | 类型 | 必传 | 说明 |
|-----------|------|------|------|
| `Content-Type` | string | 是 | 固定值：`application/json` |
| `Authorization` | string | 是 | 格式：`Bearer <api-key>` |

### 4.2 请求体（Body）

| 参数 | 类型 | 必传 | 默认值 | 说明 |
|------|------|------|--------|------|
| `model` | string | 是 | — | 模型标识，`Qwen3-ASR-1.7B` 或 `xfyun-asr` |
| `messages` | array | 是 | — | 消息数组，role 固定为 `user`，content 包含 `audio_url` |
| `max_tokens` | integer | 否 | `10000` | 最大输出 token 数（转写结果的最大字符数） |
| `temperature` | float | 否 | `0.01` | 采样温度，建议保持极低值以保证转写准确性 |

### 4.3 audio_url content block

| 字段 | 类型 | 必传 | 说明 |
|------|------|------|------|
| `type` | string | 是 | 固定值：`audio_url` |
| `audio_url.url` | string | 是 | 音频数据 URI，格式：`data:audio/{格式};base64,{base64数据}` |

**支持的音频格式**：`wav` / `mp3` / `m4a` / `webm` / `flac`

**音频格式与 MIME 类型对照**：

| 格式 | MIME 类型 |
|------|----------|
| wav | `audio/wav` |
| mp3 | `audio/mpeg` |
| m4a | `audio/mp4` |
| webm | `audio/webm` |
| flac | `audio/flac` |

---

## 五、请求示例

> **环境要求**：
> - Python == 3.12
> - Java == 17
> - Python 依赖：`pip install openai==2.41.0 soundfile numpy`
> - Java 依赖：OkHttp 4.12.0 + Jackson 2.16.0（HTTP 直调方式，详见下方示例）

### 5.1 Python（OpenAI SDK）— 短音频直接调用

```python
import base64
from openai import OpenAI

client = OpenAI(
    api_key="sk-YOUR-API-KEY",
    base_url="http://10.255.87.24:30000/v1"
)

audio_path = "audio.wav"

with open(audio_path, "rb") as f:
    audio_b64 = base64.b64encode(f.read()).decode("utf-8")

# 根据文件扩展名确定 MIME 类型
if audio_path.lower().endswith(".wav"):
    mime = "audio/wav"
elif audio_path.lower().endswith(".mp3"):
    mime = "audio/mpeg"
elif audio_path.lower().endswith(".m4a"):
    mime = "audio/mp4"
elif audio_path.lower().endswith(".webm"):
    mime = "audio/webm"
elif audio_path.lower().endswith(".flac"):
    mime = "audio/flac"
else:
    mime = "audio/wav"

audio_url = f"data:{mime};base64,{audio_b64}"

response = client.chat.completions.create(
    model="xfyun-asr",
    messages=[{
        "role": "user",
        "content": [{
            "type": "audio_url",
            "audio_url": {"url": audio_url}
        }]
    }],
    max_tokens=10000,
    temperature=0.01
)

transcript = response.choices[0].message.content
print(f"转写结果：{transcript}")
```

### 5.2 Java（OkHttp + Jackson）— 短音频直接调用

```java
import okhttp3.*;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.PropertyNamingStrategies;
import java.util.*;
import java.util.Map;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.util.Base64;

public class AsrExample {
    public static void main(String[] args) throws Exception {
        String apiKey = "sk-YOUR-API-KEY";
        String endpoint = "http://10.255.87.24:30000/v1/chat/completions";
        String audioFilePath = "audio.wav";

        ObjectMapper mapper = new ObjectMapper();
        mapper.configure(
            com.fasterxml.jackson.databind.DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false);
        mapper.setPropertyNamingStrategy(PropertyNamingStrategies.SNAKE_CASE);

        OkHttpClient httpClient = new OkHttpClient.Builder()
            .connectTimeout(java.time.Duration.ofSeconds(30))
            .readTimeout(java.time.Duration.ofSeconds(120))
            .writeTimeout(java.time.Duration.ofSeconds(120))
            .build();

        // 读取音频文件并转为 Base64
        byte[] audioBytes = Files.readAllBytes(Paths.get(audioFilePath));
        String audioB64 = Base64.getEncoder().encodeToString(audioBytes);
        String mimeType = getMimeType(audioFilePath);
        String audioUrl = "data:" + mimeType + ";base64," + audioB64;

        // 构建 audio_url content block
        List<Object> contentBlocks = new ArrayList<>();
        contentBlocks.add(Map.of(
            "type", "audio_url",
            "audio_url", Map.of("url", audioUrl)
        ));

        List<Map<String, Object>> messages = new ArrayList<>();
        messages.add(new LinkedHashMap<String, Object>() {{
            put("role", "user");
            put("content", contentBlocks);
        }});

        Map<String, Object> requestBody = new LinkedHashMap<>();
        requestBody.put("model", "xfyun-asr");
        requestBody.put("messages", messages);
        requestBody.put("max_tokens", 10000);
        requestBody.put("temperature", 0.01);

        Request request = new Request.Builder()
            .url(endpoint)
            .header("Authorization", "Bearer " + apiKey)
            .header("Content-Type", "application/json")
            .post(RequestBody.create(
                MediaType.parse("application/json"),
                mapper.writeValueAsString(requestBody)))
            .build();

        try (Response response = httpClient.newCall(request).execute()) {
            if (response.isSuccessful() && response.body() != null) {
                Map result = mapper.readValue(response.body().string(), Map.class);
                List<Map> choices = (List<Map>) result.get("choices");
                if (choices != null && !choices.isEmpty()) {
                    Map message = (Map) choices.get(0).get("message");
                    System.out.println("转写结果: " + message.get("content"));
                }
            } else {
                System.err.println("Error: " + response.code() + " " + response.message());
            }
        }
    }

    private static String getMimeType(String filename) {
        String lower = filename.toLowerCase();
        if (lower.endsWith(".wav")) return "audio/wav";
        if (lower.endsWith(".mp3")) return "audio/mpeg";
        if (lower.endsWith(".m4a")) return "audio/mp4";
        if (lower.endsWith(".webm")) return "audio/webm";
        if (lower.endsWith(".flac")) return "audio/flac";
        return "audio/wav";
    }
}
```

> **依赖（Maven）**：
> ```xml
> <dependency>
>     <groupId>com.squareup.okhttp3</groupId>
>     <artifactId>okhttp</artifactId>
>     <version>4.12.0</version>
> </dependency>
> <dependency>
>     <groupId>com.fasterxml.jackson.core</groupId>
>     <artifactId>jackson-databind</artifactId>
>     <version>2.16.0</version>
> </dependency>
> ```

### 5.3 curl

```bash
# 短音频转写（请将 audio.wav 替换为实际文件路径）
curl -X POST http://10.255.87.24:30000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-YOUR-API-KEY" \
  -d '{
    "model": "xfyun-asr",
    "messages": [{
      "role": "user",
      "content": [{
        "type": "audio_url",
        "audio_url": {
          "url": "data:audio/wav;base64,'"$(base64 audio.wav)"'"
        }
      }]
    }],
    "max_tokens": 10000,
    "temperature": 0.01
  }'
```

---

## 六、长音频分块处理

### 6.1 何时需要分块

> **注意**：`Qwen3-ASR-1.7B` 最大仅支持 150 秒音频，超出必须使用 `xfyun-asr`。

| 音频时长 | 推荐模型 | 处理方式 |
|---------|---------|---------|
| ≤ 150 秒 | `Qwen3-ASR-1.7B`（推荐）或 `xfyun-asr` | 直接调用，无需分块 |
| 150 秒 ~ 5 小时 | `xfyun-asr` | 直接调用，无需分块 |
| > 5 小时 | `xfyun-asr` | 必须分块，每块不超过 5 小时 |

### 6.2 分块参数配置

| 参数 | 推荐值 | 说明 |
|------|--------|------|
| `MAX_CHUNK_SEC` | 3600 | 最大分块时长（秒），不宜超过 1 小时 |
| `MIN_CHUNK_SEC` | 10 | 最小分块时长（秒），避免过短片段 |
| `SILENCE_THRESHOLD_DB` | -40 | 静音检测阈值（dB），越小越严格 |
| `MIN_SILENCE_DURATION` | 0.3 | 最小静音持续时间（秒） |

### 6.3 Python（OpenAI SDK）— 长音频静音检测分块

```python
import base64
import os
import tempfile
import time
import numpy as np
import soundfile as sf
from openai import OpenAI

client = OpenAI(
    api_key="sk-YOUR-API-KEY",
    base_url="http://10.255.87.24:30000/v1"
)

MAX_CHUNK_SEC = 3600   # 最大分块时长（秒）
MIN_CHUNK_SEC = 10    # 最小分块时长（秒）
SILENCE_THRESHOLD_DB = -40
MIN_SILENCE_DURATION = 0.3


def find_silence_regions(audio_data, samplerate, threshold_db=-40, min_duration=0.3):
    """检测音频中的静音区域，返回 [(start_sample, end_sample), ...]"""
    amplitude = np.abs(audio_data)
    amplitude = np.maximum(amplitude, 1e-10)
    db = 20 * np.log10(amplitude)
    is_silence = db < threshold_db
    silence_regions = []
    min_samples = int(min_duration * samplerate)
    i = 0
    while i < len(is_silence):
        if is_silence[i]:
            start = i
            while i < len(is_silence) and is_silence[i]:
                i += 1
            end = i
            if end - start >= min_samples:
                silence_regions.append((start, end))
        i += 1
    return silence_regions


def find_optimal_split_points(audio_data, samplerate, max_chunk_sec=3600, min_chunk_sec=10):
    """基于静音检测找到最佳分切点，返回 [split_sample_1, split_sample_2, ...]"""
    total_samples = len(audio_data)
    max_chunk_samples = int(max_chunk_sec * samplerate)
    min_chunk_samples = int(min_chunk_sec * samplerate)
    silence_regions = find_silence_regions(audio_data, samplerate)
    split_points = []
    current_pos = 0
    while current_pos + max_chunk_samples < total_samples:
        target_end = current_pos + max_chunk_samples
        best_split = None
        best_distance = float('inf')
        for silence_start, silence_end in silence_regions:
            silence_center = (silence_start + silence_end) // 2
            if silence_center >= current_pos + min_chunk_samples and silence_center <= target_end:
                distance = abs(silence_center - target_end)
                if distance < best_distance:
                    best_distance = distance
                    best_split = silence_center
        if best_split is not None:
            split_points.append(best_split)
            current_pos = best_split
        else:
            split_points.append(target_end)
            current_pos = target_end
    return split_points


def asr_transcribe_chunk(client, audio_chunk, samplerate):
    """转写单个音频块"""
    with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp:
        sf.write(tmp.name, audio_chunk, samplerate)
        tmp_path = tmp.name
    try:
        with open(tmp_path, "rb") as f:
            audio_b64 = base64.b64encode(f.read()).decode("utf-8")
        audio_url = f"data:audio/wav;base64,{audio_b64}"
        response = client.chat.completions.create(
            model="xfyun-asr",
            messages=[{
                "role": "user",
                "content": [{
                    "type": "audio_url",
                    "audio_url": {"url": audio_url}
                }]
            }],
            max_tokens=10000,
            temperature=0.01
        )
        return response.choices[0].message.content
    finally:
        os.unlink(tmp_path)


def asr_transcribe_long(audio_path):
    """长音频转写主函数"""
    audio_data, samplerate = sf.read(audio_path, dtype="float32")
    total_duration = len(audio_data) / samplerate
    print(f"音频时长: {total_duration:.1f} 秒 ({total_duration / 60:.1f} 分钟)")

    split_points = find_optimal_split_points(audio_data, samplerate)

    if not split_points:
        print("  音频较短，无需分块")
        return asr_transcribe_chunk(client, audio_data, samplerate)

    print(f"  检测到 {len(split_points)} 个分切点，将分为 {len(split_points) + 1} 块处理")
    all_text = []
    chunk_boundaries = [0] + split_points + [len(audio_data)]

    for i in range(len(chunk_boundaries) - 1):
        chunk = audio_data[chunk_boundaries[i]:chunk_boundaries[i + 1]]
        chunk_dur = len(chunk) / samplerate
        start = time.perf_counter()
        text = asr_transcribe_chunk(client, chunk, samplerate)
        elapsed = time.perf_counter() - start
        print(f"  [块 {i + 1}/{len(split_points) + 1}] {chunk_dur:.1f}s | 耗时 {elapsed:.2f}s | {len(text)} 字")
        all_text.append(text)

    return "".join(all_text)


if __name__ == "__main__":
    audio_file = "long_audio.mp3"
    transcript = asr_transcribe_long(audio_file)
    print(f"\n完整转写结果：\n{transcript}")
```

### 6.4 Java（OkHttp + Jackson）— 长音频静音检测分块

```java
import okhttp3.*;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.PropertyNamingStrategies;
import java.util.*;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.nio.file.Path;
import java.util.Base64;

public class AsrLongAudioExample {
    private static final double MAX_CHUNK_SEC = 3600;
    private static final double MIN_CHUNK_SEC = 10;
    private static final double SILENCE_THRESHOLD_DB = -40;
    private static final double MIN_SILENCE_DURATION = 0.3;

    private final String apiKey;
    private final String endpoint;
    private final ObjectMapper mapper;
    private final OkHttpClient httpClient;

    public AsrLongAudioExample() {
        this.apiKey = "sk-YOUR-API-KEY";
        this.endpoint = "http://10.255.87.24:30000/v1/chat/completions";
        this.mapper = new ObjectMapper();
        this.mapper.configure(
            com.fasterxml.jackson.databind.DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false);
        this.mapper.setPropertyNamingStrategy(PropertyNamingStrategies.SNAKE_CASE);
        this.httpClient = new OkHttpClient.Builder()
            .connectTimeout(java.time.Duration.ofSeconds(30))
            .readTimeout(java.time.Duration.ofSeconds(120))
            .writeTimeout(java.time.Duration.ofSeconds(120))
            .build();
    }

    /** 根据静音检测找到分切点，返回分切样本位置列表 */
    private List<Integer> findOptimalSplitPoints(double[] audioData, int sampleRate) {
        List<int[]> silenceRegions = findSilenceRegions(audioData, sampleRate);
        List<Integer> splitPoints = new ArrayList<>();
        int totalSamples = audioData.length;
        int maxChunkSamples = (int)(MAX_CHUNK_SEC * sampleRate);
        int minChunkSamples = (int)(MIN_CHUNK_SEC * sampleRate);
        int currentPos = 0;

        while (currentPos + maxChunkSamples < totalSamples) {
            int targetEnd = currentPos + maxChunkSamples;
            Integer bestSplit = null;
            double bestDistance = Double.MAX_VALUE;

            for (int[] region : silenceRegions) {
                int silenceCenter = (region[0] + region[1]) / 2;
                if (silenceCenter >= currentPos + minChunkSamples && silenceCenter <= targetEnd) {
                    double distance = Math.abs(silenceCenter - targetEnd);
                    if (distance < bestDistance) {
                        bestDistance = distance;
                        bestSplit = silenceCenter;
                    }
                }
            }

            if (bestSplit != null) {
                splitPoints.add(bestSplit);
                currentPos = bestSplit;
            } else {
                splitPoints.add(targetEnd);
                currentPos = targetEnd;
            }
        }
        return splitPoints;
    }

    /** 检测静音区域 */
    private List<int[]> findSilenceRegions(double[] audioData, int sampleRate) {
        List<int[]> regions = new ArrayList<>();
        int minSamples = (int)(MIN_SILENCE_DURATION * sampleRate);
        boolean[] isSilence = new boolean[audioData.length];
        for (int i = 0; i < audioData.length; i++) {
            double amplitude = Math.abs(audioData[i]);
            amplitude = Math.max(amplitude, 1e-10);
            double db = 20 * Math.log10(amplitude);
            isSilence[i] = db < SILENCE_THRESHOLD_DB;
        }
        int i = 0;
        while (i < isSilence.length) {
            if (isSilence[i]) {
                int start = i;
                while (i < isSilence.length && isSilence[i]) i++;
                int end = i;
                if (end - start >= minSamples) {
                    regions.add(new int[]{start, end});
                }
            }
            i++;
        }
        return regions;
    }

    /** 转写单个音频块 */
    private String transcribeChunk(byte[] audioBytes, String mimeType) throws Exception {
        String audioB64 = Base64.getEncoder().encodeToString(audioBytes);
        String audioUrl = "data:" + mimeType + ";base64," + audioB64;

        List<Object> contentBlocks = new ArrayList<>();
        contentBlocks.add(Map.of(
            "type", "audio_url",
            "audio_url", Map.of("url", audioUrl)
        ));

        List<Map<String, Object>> messages = new ArrayList<>();
        messages.add(new LinkedHashMap<String, Object>() {{
            put("role", "user");
            put("content", contentBlocks);
        }});

        Map<String, Object> requestBody = new LinkedHashMap<>();
        requestBody.put("model", "xfyun-asr");
        requestBody.put("messages", messages);
        requestBody.put("max_tokens", 10000);
        requestBody.put("temperature", 0.01);

        Request request = new Request.Builder()
            .url(endpoint)
            .header("Authorization", "Bearer " + apiKey)
            .header("Content-Type", "application/json")
            .post(RequestBody.create(
                MediaType.parse("application/json"),
                mapper.writeValueAsString(requestBody)))
            .build();

        try (Response response = httpClient.newCall(request).execute()) {
            if (response.isSuccessful() && response.body() != null) {
                Map result = mapper.readValue(response.body().string(), Map.class);
                List<Map> choices = (List<Map>) result.get("choices");
                if (choices != null && !choices.isEmpty()) {
                    Map message = (Map) choices.get(0).get("message");
                    return (String) message.get("content");
                }
            }
        }
        return "";
    }

    /** 长音频转写主函数（需要第三方音频库读取音频） */
    public String transcribeLong(String audioPath) throws Exception {
        System.out.println("长音频转写需要使用第三方库（如 Tritonus 或 javax.sound.sampled）读取音频文件，");
        System.out.println("分块逻辑与 Python 版本一致，可对应迁移。");
        // 伪代码示意：
        // double[] audioData = readAudioFile(audioPath);
        // int sampleRate = getSampleRate(audioPath);
        // double totalDuration = audioData.length / (double) sampleRate;
        // List<Integer> splitPoints = findOptimalSplitPoints(audioData, sampleRate);
        // ...
        return "";
    }

    public static void main(String[] args) throws Exception {
        AsrLongAudioExample example = new AsrLongAudioExample();
        String audioFile = "long_audio.mp3";
        String result = example.transcribeLong(audioFile);
        System.out.println("\n完整转写结果：\n" + result);
    }
}
```

> **注意**：Java 标准库不包含音频处理能力，建议使用 Python 进行长音频分块处理，或集成 [TarsosSampled](https://github.com/JorenSix/TarsosSampled)、[Tritonus](https://tritonus.sourceforge.net/) 等音频处理库。
>
> **依赖**：同上（OkHttp 4.12.0 + Jackson 2.16.0）

---

## 七、成功响应

### 7.1 响应示例

```json
{
  "id": "chatcmpl-xxxxxxxxxxxx",
  "object": "chat.completion",
  "created": 1750000000,
  "model": "xfyun-asr",
  "choices": [{
    "index": 0,
    "message": {
      "role": "assistant",
      "content": "这是转写后的文本内容"
    },
    "finish_reason": "stop"
  }],
  "usage": {
    "prompt_tokens": 0,
    "completion_tokens": 0,
    "total_tokens": 0
  }
}
```

### 7.2 响应参数说明

| 参数名 | 类型 | 说明 |
|--------|------|------|
| `id` | string | 请求唯一标识 |
| `object` | string | 对象类型：`chat.completion` |
| `created` | integer | 请求创建时间戳（Unix 秒） |
| `model` | string | 模型标识：`xfyun-asr` |
| `choices[0].message.content` | string | **转写文本**（即为所需结果） |
| `choices[0].finish_reason` | string | 停止原因，通常为 `stop` |
| `usage` | object | Token 使用量（ASR 场景通常为 0） |

---

## 八、异常响应

### 8.1 格式

```json
{
  "error": {
    "message": "错误描述信息",
    "code": "语义化错误码",
    "type": "错误大类"
  }
}
```

### 8.2 错误码对照表

| HTTP 状态码 | error.code | error.type | 说明 | 处理建议 |
|------------|------------|------------|------|---------|
| 400 | `missing_required_param` | `invalid_request_error` | 缺少必填参数 | 检查 `model` 和 `messages` 格式 |
| 400 | `empty_audio_file` | `invalid_request_error` | 音频数据为空 | 检查音频文件路径和读取是否正常 |
| 400 | `invalid_audio_format` | `invalid_request_error` | 不支持的音频格式 | 转为 `wav` / `mp3` / `m4a` / `webm` / `flac` |
| 400 | `corrupted_audio` | `invalid_request_error` | 音频损坏或无法解码 | 重新准备音频文件 |
| 400 | `audio_duration_exceeded` | `invalid_request_error` | 音频时长超过限制（`Qwen3-ASR-1.7B` 最大 150s，`xfyun-asr` 最大 5h） | 缩短音频或使用静音检测分块策略 |
| 400 | `no_speech_detected` | `invalid_request_error` | 未检测到有效人声 | 检查音频内容是否包含语音 |
| 413 | `audio_too_large` | `invalid_request_error` | 文件过大 | 压缩采样率或分段上传 |
| 401 | `invalid_api_key` | `authentication_error` | API Key 无效 | 检查 Authorization 头 |
| 403 | `model_access_denied` | `permission_error` | 服务权限不足 | 联系管理员 |
| 429 | `rate_limit_exceeded` | `rate_limit_error` | 调用频率超限 | 降低请求频率，等待后重试 |
| 500 | `audio_upload_failed` | `api_error` | 音频上传失败 | 稍后重试 |
| 500 | `transcription_failed` | `api_error` | 识别引擎失败 | 稍后重试 |
| 500 | `internal_error` | `api_error` | 服务内部错误 | 稍后重试 |
| 504 | `processing_timeout` | `api_error` | 处理超时 | 减小音频文件大小或分片 |

> 完整错误码列表请参阅：[ASR 错误码参考](error-codes)

---

## 九、重试策略

| 错误类型 | HTTP 状态码 | 重试 | 策略 |
|---------|------------|------|------|
| 参数错误 | 400 | 否 | 修正参数后重发 |
| 认证错误 | 401 | 否 | 检查 API Key，不重试 |
| 权限错误 | 403 | 否 | 联系管理员，不重试 |
| 限流 | 429 | 是 | 指数退避重试，最多 5 次 |
| 服务端错误 | 500 / 504 | 是 | 指数退避重试，最多 3 次 |
