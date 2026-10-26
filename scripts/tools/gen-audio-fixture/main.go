// 命令 gen-audio-fixture 生成渠道后台测试使用的音频夹具。
//
// 夹具是合成的确定性信号，不含任何真实录音：基频随时间缓慢起伏的谐波叠加，
// 辅以共振峰加权的频谱包络与音节级幅度包络，再加一层固定种子的低幅噪声。
//
// 用法（在仓库根目录执行）：
//
//	go run ./scripts/tools/gen-audio-fixture
//
// 输出固定为 controller/audiofixture/channel-test-sample.wav。
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

const (
	// sampleRate 是讯飞 IFASR 要求的采样率（16kHz / 8kHz）。
	sampleRate = 16000
	// bitDepth 是讯飞 IFASR 要求的位深（16bit）。
	bitDepth = 16
	// channels 是讯飞 IFASR 要求的声道数（单声道）。
	channels = 1
	// durationSeconds 是夹具时长，落在 1~3 秒区间内。
	durationSeconds = 2
	// outputPath 是夹具的输出路径（相对仓库根目录）。
	outputPath = "controller/audiofixture/channel-test-sample.wav"
)

func main() {
	audio := buildWAV()
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "创建输出目录失败: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outputPath, audio, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "写入夹具失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("已生成 %s（%d 字节）\n", outputPath, len(audio))
}

// buildWAV 合成一段确定性的单声道 16kHz/16bit 音频并封装成 WAV。
func buildWAV() []byte {
	sampleCount := sampleRate * durationSeconds
	pcm := make([]byte, sampleCount*2)

	// 线性同余发生器：固定种子，保证每次生成的字节完全一致。
	noiseState := uint32(20240517)
	nextNoise := func() float64 {
		noiseState = noiseState*1664525 + 1013904223
		return float64(noiseState>>8)/float64(1<<24)*2 - 1
	}

	phase := 0.0
	for i := range sampleCount {
		t := float64(i) / sampleRate
		// 基频在 110~160Hz 之间缓慢起伏，模拟浊音的音高变化。
		f0 := 135 + 25*math.Sin(2*math.Pi*0.7*t)
		phase += 2 * math.Pi * f0 / sampleRate

		// 谐波叠加：共振峰权重让频谱接近人声，而不是单一纯音。
		value := 0.0
		for k := 1; k <= 12; k++ {
			value += formantGain(f0*float64(k)) * math.Sin(phase*float64(k))
		}
		value /= 3

		// 音节级幅度包络，避免整段恒定音量被上游判为非语音。
		value *= 0.35 + 0.65*math.Abs(math.Sin(math.Pi*2.2*t))
		value += 0.02 * nextNoise()

		sample := int16(math.Round(value * 12000))
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(sample))
	}
	return wavContainer(pcm)
}

// formantGain 返回某个频率上的共振峰增益。
func formantGain(freq float64) float64 {
	gain := 0.0
	for _, formant := range []struct {
		center float64
		width  float64
		amp    float64
	}{
		{center: 520, width: 120, amp: 1.0},
		{center: 1450, width: 200, amp: 0.6},
		{center: 2600, width: 300, amp: 0.3},
	} {
		distance := (freq - formant.center) / formant.width
		gain += formant.amp * math.Exp(-distance*distance)
	}
	return gain
}

// wavContainer 把 PCM 数据封装成 44 字节头的 WAV 文件。
func wavContainer(pcm []byte) []byte {
	var buf bytes.Buffer
	buf.Grow(44 + len(pcm))

	byteRate := sampleRate * channels * bitDepth / 8
	blockAlign := channels * bitDepth / 8

	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36+len(pcm)))
	buf.WriteString("WAVE")

	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	_ = binary.Write(&buf, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(bitDepth))

	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(pcm)))
	buf.Write(pcm)
	return buf.Bytes()
}
