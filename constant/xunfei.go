package constant

// XunfeiASRModelName 是讯飞离线转写（IFASR 录音文件转写大模型）的模型名。
//
// 该模型名同时被讯飞渠道适配器（模型守卫）、端点类型声明（按模型分流）、渠道测试
// （音频转写端点的固定模型）三处引用，因此集中定义在 constant 包，避免多处字面量重复。
//
// 形态约束：星火对话路径的 getAPIVersion 会按 "-" 切分模型名，恰好切成两段时把第二段当作
// API 版本（两段名如 SparkDesk-ASR 会被误解析成版本 ASR）。本模型名恰好三段，因此不会
// 被星火路径误解析。
const XunfeiASRModelName = "ifasr-llm-v1"
