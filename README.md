# 谛听

<p align="center">
  <img src="web/logo.svg" alt="谛听 Logo" width="96" height="96">
</p>

<p align="center">
  <a href="#中文">中文</a> | <a href="#english">English</a>
</p>

## 中文

谛听是一个 macOS 本机语音记录控制台。Go 单进程完成麦克风采集、人声检测、流式语音识别、说话人标注和录音持久化，并通过内嵌 Web 页面提供实时文稿、主题索引、会议纪要与 Markdown 导出。

### 核心功能

- **本地语音记录**：Silero VAD 检测人声，按天保存压缩录音和 JSONL 转写，重启后恢复历史。
- **流式与精校识别**：实时显示临时文本，语句结束后使用本地 SenseVoice 精校；可选 MiMo ASR，云端失败时回退本地。
- **说话人管理**：基于 CAM++ 声纹标注说话人，支持人名别名、多人标签合并和声纹模型评估。
- **文稿与议题**：跨说话人生成议题段落、层级主题、语义修正和段落摘要，支持按天生成会议纪要。
- **Web 控制台**：实时预览、录音回放与下载、按天归档、单条或批量删除、Markdown 导出，支持中英文。
- **个性化识别**：维护场景术语表，可全量重转写历史录音或仅重建派生段落。
- **隐私优先**：默认识别和存储均在本机；只有明确启用 MiMo ASR 才上传音频，文本仅发送给已配置的议题模型。

### 快速开始

环境要求：macOS Apple Silicon、Go 1.25+、CGO。首次运行需下载识别模型。

```bash
./scripts/download-models.sh
go build -o bin/diting ./cmd/diting
./bin/diting
```

打开 <http://127.0.0.1:8080>。首次使用麦克风时，请在 macOS“系统设置 → 隐私与安全性 → 麦克风”中授权。

默认启动即开始监听；如需手动启动：

```bash
./bin/diting -autostart=false
```

按 `Ctrl+C` 停止服务。完整参数、模型配置、控制台操作、数据管理和 API 说明见[使用说明](docs/USAGE.md)。

### 文档

- [使用说明](docs/USAGE.md)：安装启动、控制台、命令行、数据维护与 API。
- [更新日志](CHANGELOG.md)：按功能主题汇总用户可见变更。
- [技术规范](SPEC.md)：架构、边界、接口契约与验收标准。
- [OpenAPI 契约](api/openapi.yaml)：HTTP API 的机器可读定义。

### 开发验证

```bash
gofmt -w cmd internal
go vet ./...
go test ./...
go build -o bin/diting ./cmd/diting
```

前端资源通过 `go:embed` 内嵌；修改 `web/` 后必须重新构建并重启 `bin/diting`。

### 当前边界

仅绑定本机回环地址，不提供云端同步或局域网访问。历史数据默认保留，可在控制台按天移到废纸篓。说话人标注是辅助功能而非身份认证，多人同时说话、远场拾音和噪声仍可能影响识别结果。

## English

Diting is a local voice recording console for macOS. A single Go process handles microphone capture, voice activity detection, streaming speech recognition, speaker labeling, and recording persistence. Its embedded web interface provides live transcripts, topic navigation, meeting minutes, and Markdown export.

### Features

- **Local voice recording**: Silero VAD detects speech and saves compressed audio and JSONL transcripts by day, with history restored after restart.
- **Streaming and refined recognition**: Interim text appears in real time, followed by local SenseVoice refinement. Optional MiMo ASR automatically falls back to local recognition if the cloud service fails.
- **Speaker management**: CAM++ voiceprints provide speaker labels, name aliases, speaker merging, and voiceprint model evaluation.
- **Transcripts and topics**: Generate semantic paragraphs across speakers, hierarchical topics, semantic corrections, paragraph summaries, and daily meeting minutes.
- **Web console**: Live preview, audio playback and download, daily archives, single or batch deletion, Markdown export, and Chinese/English interfaces.
- **Custom vocabulary**: Maintain domain-specific term mappings, retranscribe all historical recordings, or rebuild only derived paragraphs.
- **Privacy first**: Recognition and storage stay local by default. Audio is uploaded only when MiMo ASR is explicitly enabled, and transcript text is sent only to the configured topic model.

### Quick Start

Requirements: macOS on Apple Silicon, Go 1.25+, and CGO. Download the recognition models before the first run.

```bash
./scripts/download-models.sh
go build -o bin/diting ./cmd/diting
./bin/diting
```

Open <http://127.0.0.1:8080>. When using the microphone for the first time, grant access under macOS **System Settings → Privacy & Security → Microphone**.

Listening starts automatically by default. To start it manually instead:

```bash
./bin/diting -autostart=false
```

Press `Ctrl+C` to stop the service. See the [Usage Guide](docs/USAGE.md) for all options, model configuration, console operations, data management, and API details.

### Documentation

- [Usage Guide](docs/USAGE.md): Installation, startup, console, CLI, data maintenance, and APIs.
- [Changelog](CHANGELOG.md): User-visible changes grouped by feature area.
- [Technical Specification](SPEC.md): Architecture, boundaries, API contracts, and acceptance criteria.
- [OpenAPI Specification](api/openapi.yaml): Machine-readable HTTP API definition.

### Development Checks

```bash
gofmt -w cmd internal
go vet ./...
go test ./...
go build -o bin/diting ./cmd/diting
```

Frontend assets are embedded with `go:embed`. After changing files under `web/`, rebuild and restart `bin/diting`.

### Current Limitations

The service binds only to a local loopback address and does not provide cloud sync or LAN access. History is retained by default and can be moved to the Trash by day from the console. Speaker labels are an assistance feature, not identity authentication; overlapping speech, distant microphones, and background noise can still affect recognition accuracy.
