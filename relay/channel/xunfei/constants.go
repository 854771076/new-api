package xunfei

import "github.com/QuantumNous/new-api/constant"

// ASRModelName 是讯飞离线转写（IFASR 录音文件转写大模型）的模型名。
//
// 模型名集中定义在 constant 包，这里保留导出名供本包的模型判定与既有测试使用。
// 形态约束见 constant.XunfeiASRModelName 的说明。
const ASRModelName = constant.XunfeiASRModelName

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
