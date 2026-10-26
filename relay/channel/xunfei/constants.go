package xunfei

// ASRModelName 是讯飞离线转写（IFASR 录音文件转写大模型）的模型名。
//
// 形态约束：星火对话路径的 getAPIVersion 会按 "-" 切分模型名，恰好切成两段时把第二段当作
// API 版本（两段名如 SparkDesk-ASR 会被误解析成版本 ASR）。本模型名恰好三段，因此不会
// 被星火路径误解析。
const ASRModelName = "ifasr-llm-v1"

var ModelList = []string{
	"SparkDesk",
	"SparkDesk-v1.1",
	"SparkDesk-v2.1",
	"SparkDesk-v3.1",
	"SparkDesk-v3.5",
	"SparkDesk-v4.0",
	ASRModelName,
}

var ChannelName = "xunfei"

// IsASRModel 判断模型名是否命中讯飞离线转写模型。
//
// 判定是精确相等：分支点必须明确且与模型名形态无关，精确相等是最明确的判据，
// 不会因为模型名恰好被切成两段或三段而误判。
func IsASRModel(model string) bool {
	return model == ASRModelName
}
