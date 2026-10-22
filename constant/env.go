package constant

// DefaultErrorLogEnabled 是 ERROR_LOG_ENABLED 未显式设置时的默认行为：开启。
// 默认部署下若不记失败请求的错误日志，失败请求在日志里就是一片空白（观测
// 黑洞）；需要关闭的部署显式设置 ERROR_LOG_ENABLED=false。
const DefaultErrorLogEnabled = true

var StreamingTimeout int
var DifyDebug bool
var MaxFileDownloadMB int
var StreamScannerMaxBufferMB int
var ForceStreamOption bool
var CountToken bool
var GetMediaToken bool
var GetMediaTokenNotStream bool
var UpdateTask bool
var MaxRequestBodyMB int
var AnonymousRequestBodyLimitKB int
var AzureDefaultAPIVersion string
var NotifyLimitCount int
var NotificationLimitDurationMinute int
var GenerateDefaultToken bool
var ErrorLogEnabled bool
var TaskQueryLimit int
var TaskTimeoutMinutes int
var TaskPollMaxFailures = 20
var TaskPluginProtocolTimeoutSeconds int
var TaskPluginProtocolTickMilliseconds int
var TaskPluginProtocolTickJitterMilliseconds int
var TaskPluginProtocolHeartbeatSeconds int

// temporary variable for sora patch, will be removed in future
var TaskPricePatches []string

// TrustedRedirectDomains is a list of trusted domains for redirect URL validation.
// Domains support subdomain matching (e.g., "example.com" matches "sub.example.com").
var TrustedRedirectDomains []string
