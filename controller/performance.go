package controller

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/gin-gonic/gin"
)

// PerformanceStats 性能统计信息
type PerformanceStats struct {
	// 缓存统计
	CacheStats common.DiskCacheStats `json:"cache_stats"`
	// 系统内存统计
	MemoryStats MemoryStats `json:"memory_stats"`
	// 磁盘缓存目录信息
	DiskCacheInfo DiskCacheInfo `json:"disk_cache_info"`
	// 磁盘空间信息
	DiskSpaceInfo common.DiskSpaceInfo `json:"disk_space_info"`
	// 配置信息
	Config PerformanceConfig `json:"config"`
}

// MemoryStats 内存统计
type MemoryStats struct {
	// 已分配内存（字节）
	Alloc uint64 `json:"alloc"`
	// 总分配内存（字节）
	TotalAlloc uint64 `json:"total_alloc"`
	// 系统内存（字节）
	Sys uint64 `json:"sys"`
	// GC 次数
	NumGC uint32 `json:"num_gc"`
	// Goroutine 数量
	NumGoroutine int `json:"num_goroutine"`
}

// DiskCacheInfo 磁盘缓存目录信息
type DiskCacheInfo struct {
	// 缓存目录路径
	Path string `json:"path"`
	// 目录是否存在
	Exists bool `json:"exists"`
	// 文件数量
	FileCount int `json:"file_count"`
	// 总大小（字节）
	TotalSize int64 `json:"total_size"`
}

// PerformanceConfig 性能配置
type PerformanceConfig struct {
	// 是否启用磁盘缓存
	DiskCacheEnabled bool `json:"disk_cache_enabled"`
	// 磁盘缓存阈值（MB）
	DiskCacheThresholdMB int `json:"disk_cache_threshold_mb"`
	// 磁盘缓存最大大小（MB）
	DiskCacheMaxSizeMB int `json:"disk_cache_max_size_mb"`
	// 磁盘缓存路径
	DiskCachePath string `json:"disk_cache_path"`
	// 是否在容器中运行
	IsRunningInContainer bool `json:"is_running_in_container"`

	// MonitorEnabled 是否启用性能监控
	MonitorEnabled bool `json:"monitor_enabled"`
	// MonitorCPUThreshold CPU 使用率阈值（%）
	MonitorCPUThreshold int `json:"monitor_cpu_threshold"`
	// MonitorMemoryThreshold 内存使用率阈值（%）
	MonitorMemoryThreshold int `json:"monitor_memory_threshold"`
	// MonitorDiskThreshold 磁盘使用率阈值（%）
	MonitorDiskThreshold int `json:"monitor_disk_threshold"`
}

// GetPerformanceStats 获取性能统计信息
func GetPerformanceStats(c *gin.Context) {
	// 不再每次获取统计都全量扫描磁盘，依赖原子计数器保证性能
	// 仅在系统启动或显式清理时同步
	cacheStats := common.GetDiskCacheStats()

	// 获取内存统计
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// 获取磁盘缓存目录信息
	diskCacheInfo := getDiskCacheInfo()

	// 获取配置信息
	diskConfig := common.GetDiskCacheConfig()
	monitorConfig := common.GetPerformanceMonitorConfig()
	config := PerformanceConfig{
		DiskCacheEnabled:       diskConfig.Enabled,
		DiskCacheThresholdMB:   diskConfig.ThresholdMB,
		DiskCacheMaxSizeMB:     diskConfig.MaxSizeMB,
		DiskCachePath:          diskConfig.Path,
		IsRunningInContainer:   common.IsRunningInContainer(),
		MonitorEnabled:         monitorConfig.Enabled,
		MonitorCPUThreshold:    monitorConfig.CPUThreshold,
		MonitorMemoryThreshold: monitorConfig.MemoryThreshold,
		MonitorDiskThreshold:   monitorConfig.DiskThreshold,
	}

	// 获取磁盘空间信息
	// 使用缓存的系统状态，避免频繁调用系统 API
	systemStatus := common.GetSystemStatus()
	diskSpaceInfo := common.DiskSpaceInfo{
		UsedPercent: systemStatus.DiskUsage,
	}
	// 如果需要详细信息，可以按需获取，或者扩展 SystemStatus
	// 这里为了保持接口兼容性，我们仍然调用 GetDiskSpaceInfo，但注意这可能会有性能开销
	// 考虑到 GetPerformanceStats 是管理接口，频率较低，直接调用是可以接受的
	// 但为了一致性，我们也可以考虑从 SystemStatus 中获取部分信息
	diskSpaceInfo = common.GetDiskSpaceInfo()

	stats := PerformanceStats{
		CacheStats: cacheStats,
		MemoryStats: MemoryStats{
			Alloc:        memStats.Alloc,
			TotalAlloc:   memStats.TotalAlloc,
			Sys:          memStats.Sys,
			NumGC:        memStats.NumGC,
			NumGoroutine: runtime.NumGoroutine(),
		},
		DiskCacheInfo: diskCacheInfo,
		DiskSpaceInfo: diskSpaceInfo,
		Config:        config,
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}

// ClearDiskCache 清理不活跃的磁盘缓存
func ClearDiskCache(c *gin.Context) {
	// 清理超过 10 分钟未使用的缓存文件
	// 10 分钟是一个安全的阈值，确保正在进行的请求不会被误删
	err := common.CleanupOldDiskCacheFiles(10 * time.Minute)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccessT(c, "Inactive disk cache cleared", nil)
}

// ResetPerformanceStats 重置性能统计
func ResetPerformanceStats(c *gin.Context) {
	common.ResetDiskCacheStats()

	common.ApiSuccessT(c, "Statistics reset", nil)
}

// ForceGC 强制执行 GC
func ForceGC(c *gin.Context) {
	runtime.GC()

	common.ApiSuccessT(c, "GC executed", nil)
}

// LogFileInfo 日志文件信息
type LogFileInfo struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// LogFilesResponse 日志文件列表响应
type LogFilesResponse struct {
	LogDir     string        `json:"log_dir"`
	Enabled    bool          `json:"enabled"`
	FileCount  int           `json:"file_count"`
	TotalSize  int64         `json:"total_size"`
	OldestTime *time.Time    `json:"oldest_time,omitempty"`
	NewestTime *time.Time    `json:"newest_time,omitempty"`
	Files      []LogFileInfo `json:"files"`
}

// getLogFiles 读取日志目录中的日志文件列表
func getLogFiles() ([]LogFileInfo, error) {
	if *common.LogDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(*common.LogDir)
	if err != nil {
		return nil, err
	}
	var files []LogFileInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "oneapi-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, LogFileInfo{
			Name:    name,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	// 按文件名降序排列（最新在前）
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name > files[j].Name
	})
	return files, nil
}

// GetLogFiles 获取日志文件列表
func GetLogFiles(c *gin.Context) {
	if *common.LogDir == "" {
		common.ApiSuccess(c, LogFilesResponse{Enabled: false})
		return
	}
	files, err := getLogFiles()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var totalSize int64
	var oldest, newest time.Time
	for i, f := range files {
		totalSize += f.Size
		if i == 0 || f.ModTime.Before(oldest) {
			oldest = f.ModTime
		}
		if i == 0 || f.ModTime.After(newest) {
			newest = f.ModTime
		}
	}
	resp := LogFilesResponse{
		LogDir:    *common.LogDir,
		Enabled:   true,
		FileCount: len(files),
		TotalSize: totalSize,
		Files:     files,
	}
	if len(files) > 0 {
		resp.OldestTime = &oldest
		resp.NewestTime = &newest
	}
	common.ApiSuccess(c, resp)
}

// CleanupLogFiles 清理过期日志文件
func CleanupLogFiles(c *gin.Context) {
	mode := c.Query("mode")
	valueStr := c.Query("value")
	if mode != "by_count" && mode != "by_days" {
		common.ApiErrorMsg(c, "invalid mode, must be by_count or by_days")
		return
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil || value < 1 {
		common.ApiErrorMsg(c, "invalid value, must be a positive integer")
		return
	}
	if *common.LogDir == "" {
		common.ApiErrorMsg(c, "log directory not configured")
		return
	}

	files, err := getLogFiles()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	activeLogPath := logger.GetCurrentLogPath()
	var toDelete []LogFileInfo

	switch mode {
	case "by_count":
		// files 已按名称降序（最新在前），保留前 value 个
		for i, f := range files {
			if i < value {
				continue
			}
			fullPath := filepath.Join(*common.LogDir, f.Name)
			if fullPath == activeLogPath {
				continue
			}
			toDelete = append(toDelete, f)
		}
	case "by_days":
		cutoff := time.Now().AddDate(0, 0, -value)
		for _, f := range files {
			if f.ModTime.Before(cutoff) {
				fullPath := filepath.Join(*common.LogDir, f.Name)
				if fullPath == activeLogPath {
					continue
				}
				toDelete = append(toDelete, f)
			}
		}
	}

	var deletedCount int
	var freedBytes int64
	var failedFiles []string
	for _, f := range toDelete {
		fullPath := filepath.Join(*common.LogDir, f.Name)
		if err := os.Remove(fullPath); err != nil {
			failedFiles = append(failedFiles, f.Name)
			continue
		}
		deletedCount++
		freedBytes += f.Size
	}

	result := gin.H{
		"deleted_count": deletedCount,
		"freed_bytes":   freedBytes,
		"failed_files":  failedFiles,
	}

	if len(failedFiles) > 0 {
		msg := common.NewMessage("Some files could not be deleted ({{failed}}/{{total}})", map[string]any{
			"failed": len(failedFiles),
			"total":  len(toDelete),
		})
		common.ApiErrorStatus(c, http.StatusOK, msg, gin.H{"data": result})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    result,
	})
}

// LogFileContentResponse 日志文件末尾内容响应。
type LogFileContentResponse struct {
	File      string   `json:"file"`
	LineCount int      `json:"line_count"`
	Truncated bool     `json:"truncated"`
	Lines     []string `json:"lines"`
}

// LogSearchMatch 一条命中的日志行。
type LogSearchMatch struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// LogSearchResponse 日志关键词检索结果。
type LogSearchResponse struct {
	Keyword      string           `json:"keyword"`
	Since        string           `json:"since"`
	FilesScanned int              `json:"files_scanned"`
	Matched      int              `json:"matched"`
	Truncated    bool             `json:"truncated"`
	Lines        []LogSearchMatch `json:"lines"`
}

// maxLogContentBytes / maxLogSearchBytes 限定单次读取日志文件的字节上限，避免读取
// 超大文件占用过多内存。日志行通常很短，8/32 MB 已足够覆盖排查所需内容。
const (
	maxLogContentBytes = 8 << 20
	maxLogSearchBytes  = 32 << 20
)

// readLogTail 读取 path 末尾至多 maxBytes 字节并返回其中的行。truncated 表示文件
// 超过了窗口、起始部分被跳过；返回的行会去掉因 seek 造成的残缺首行与末尾换行
// 产生的空行。
func readLogTail(path string, maxBytes int64) ([]string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	truncated := false
	offset := int64(0)
	if info.Size() > maxBytes {
		offset = info.Size() - maxBytes
		truncated = true
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, false, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false, err
	}

	lines := strings.Split(string(data), "\n")
	if truncated && len(lines) > 0 {
		lines = lines[1:]
	}
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, truncated, nil
}

// GetLogFileContent 读取指定日志文件的末尾若干行。
func GetLogFileContent(c *gin.Context) {
	name := c.Query("file")
	if name == "" {
		common.ApiErrorMsg(c, "file parameter is required")
		return
	}
	if *common.LogDir == "" {
		common.ApiErrorMsg(c, "log directory not configured")
		return
	}
	files, err := getLogFiles()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 只允许读取日志目录中真实存在的文件，天然阻止路径穿越。
	found := false
	for _, f := range files {
		if f.Name == name {
			found = true
			break
		}
	}
	if !found {
		common.ApiErrorMsg(c, "log file not found")
		return
	}

	lines := 200
	if raw := c.Query("lines"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			common.ApiErrorMsg(c, "lines must be a positive integer")
			return
		}
		if n > 5000 {
			n = 5000
		}
		lines = n
	}

	content, truncated, err := readLogTail(filepath.Join(*common.LogDir, name), maxLogContentBytes)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if len(content) > lines {
		content = content[len(content)-lines:]
	}
	common.ApiSuccess(c, LogFileContentResponse{
		File:      name,
		LineCount: len(content),
		Truncated: truncated,
		Lines:     content,
	})
}

// SearchLogFiles 在最近时间段内的日志文件中按关键词检索。
func SearchLogFiles(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("keyword"))
	if keyword == "" {
		common.ApiErrorMsg(c, "keyword parameter is required")
		return
	}
	sinceStr := c.DefaultQuery("since", "24h")
	since, err := time.ParseDuration(sinceStr)
	if err != nil || since <= 0 {
		common.ApiErrorMsg(c, "since must be a positive Go duration such as 1h or 30m")
		return
	}
	limit := 200
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			common.ApiErrorMsg(c, "limit must be a positive integer")
			return
		}
		if n > 1000 {
			n = 1000
		}
		limit = n
	}

	resp := LogSearchResponse{Keyword: keyword, Since: sinceStr, Lines: []LogSearchMatch{}}
	if *common.LogDir == "" {
		common.ApiSuccess(c, resp)
		return
	}
	files, err := getLogFiles()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	cutoff := time.Now().Add(-since)
	lower := strings.ToLower(keyword)
search:
	for _, f := range files {
		if f.ModTime.Before(cutoff) {
			continue
		}
		resp.FilesScanned++
		lines, truncated, err := readLogTail(filepath.Join(*common.LogDir, f.Name), maxLogSearchBytes)
		if err != nil {
			continue
		}
		if truncated {
			resp.Truncated = true
		}
		for i, line := range lines {
			if !strings.Contains(strings.ToLower(line), lower) {
				continue
			}
			resp.Lines = append(resp.Lines, LogSearchMatch{File: f.Name, Line: i + 1, Text: line})
			resp.Matched++
			if resp.Matched >= limit {
				resp.Truncated = true
				break search
			}
		}
	}
	common.ApiSuccess(c, resp)
}

// getDiskCacheInfo 获取磁盘缓存目录信息
func getDiskCacheInfo() DiskCacheInfo {
	// 使用统一的缓存目录
	dir := common.GetDiskCacheDir()

	info := DiskCacheInfo{
		Path:   dir,
		Exists: false,
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return info
	}

	info.Exists = true
	info.FileCount = 0
	info.TotalSize = 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info.FileCount++
		if fileInfo, err := entry.Info(); err == nil {
			info.TotalSize += fileInfo.Size()
		}
	}

	return info
}
