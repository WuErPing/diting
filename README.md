# 谛听

本机语音记录控制台。Go 单进程完成麦克风采集、Silero VAD 人声检测、流式语音识别、WAV 分段录音、JSONL 文本持久化，并通过内嵌 Web 页面实时展示。

## 功能

- 只在检测到人声时保存录音，附带约 500ms 前置音频，并按 VAD 绝对采样位置去除重叠。
- 连续讲话每约 30 秒强制分段，避免 VAD 缓冲持续增长。
- 说话过程中通过 SSE 推送流式临时文本，语句结束后使用本地 SenseVoice 对完整录音精校并写入带标点的最终记录。
- 使用 3D-Speaker CAM++ 声纹模型为每个片段标注主导说话人（说话人1、说话人2…），声纹中心持久化到 `data/speakers.json`，跨重启沿用编号。
- 支持 `-retranscribe` 用当前模型全量重跑历史录音，修复旧版本产生的文本并补齐说话人标签；WAV 不会被修改。
- 控制台启停麦克风、查看历史、回放和下载 WAV。
- 所有识别和存储都在本机，不调用云 ASR，也不上传音频或文本。
- 服务重启后恢复最近历史；磁盘上的 JSONL 和 WAV 不会被启动过程清空。

## 环境

- macOS Apple Silicon（当前验证环境）。
- Go 1.25+，CGO 开启。
- 首次运行需要下载官方模型；运行时不依赖 LM Studio。

## 启动

```bash
./scripts/download-models.sh
go build -o bin/diting ./cmd/diting
./bin/diting
```

打开 <http://127.0.0.1:8080>。首次启动麦克风时，macOS 可能要求允许当前终端或打包应用使用麦克风；请在“系统设置 → 隐私与安全性 → 麦克风”中授权。

默认启动即开始监听。若希望先打开控制台、再手动启动：

```bash
./bin/diting -autostart=false
```

可选参数：

```text
-listen 127.0.0.1:8080   本地 HTTP 地址，只允许回环地址
-models models          模型目录
-data data              录音与文本目录
-autostart=true         进程启动后自动打开麦克风
-speaker-model FILE     声纹模型，默认 <models>/3dspeaker_speech_campplus_sv_zh-cn_16k-common.onnx
-speaker-threshold 0.45 说话人匹配的余弦阈值，调高更易拆分，调低更易合并
-verify-wav FILE        用 WAV 验证真实模型识别后退出
-retranscribe           用当前模型全量重跑历史记录后退出
```

停止服务按 `Ctrl+C`。程序会关闭麦克风、冲刷当前语音分段，再停止 HTTP 服务。

## 数据

```text
data/
├── transcripts.jsonl
├── transcripts.jsonl.bak   # 仅在 -retranscribe 后生成
├── speakers.json           # 声纹中心，可删除以重新聚类
└── audio/
    └── YYYYMMDD/
        └── <segment-id>.wav
```

每条 JSONL 包含分段 ID、开始/结束时间、时长、最终文本、说话人标签（可能为空）和相对录音路径。数据目录创建为 `0700`，文件创建为 `0600`。默认不自动删除历史，请按自己的保留周期清理磁盘。

同一数据目录只允许一个进程使用（`data/.lock`）。服务运行期间执行 `-retranscribe` 会被拒绝，请先停止服务。

API：

```text
GET /api/status
PUT /api/capture             {"state":"RUNNING"} 或 {"state":"STOPPED"}
GET /api/segments?limit=50
GET /api/segments/{id}/audio
GET /api/events              SSE: status / interim / segment / error
```

## 验证

```bash
gofmt -w cmd internal web
go vet ./...
go test ./...
go build -o bin/diting ./cmd/diting
```

模型下载后，可用模型目录中的示例 WAV 验证实际推理。该命令会通过完整引擎路径在 `data` 写入真实记录，并输出整段精校对照、最终文本和 interim/final 事件数量：

```bash
./bin/diting -verify-wav \
  models/sherpa-onnx-streaming-zipformer-zh-int8-2025-06-30/test_wavs/0.wav
```

全量重跑历史录音（建议先备份或在数据副本上验证）：

```bash
./bin/diting -retranscribe
```

该命令会重写 `data/transcripts.jsonl`，并把原文件备份为 `data/transcripts.jsonl.bak`。WAV 不会改动；如需回滚，执行 `mv data/transcripts.jsonl.bak data/transcripts.jsonl`。删除 `data/speakers.json` 后重跑会重新聚类说话人编号。

## 常驻运行说明

程序本身用于长时间驻留：音频回调只写入有界队列，识别在独立 goroutine，慢 SSE 客户端会被断开，内存历史限制为最近 500 条。开机自启和进程崩溃后的自动拉起应由 macOS `launchd` 管理，本项目不会擅自安装系统服务。

当前版本不包含云端同步、局域网访问和自动清理。说话人按 VAD 片段标注主导说话人，多人快速抢话的片段只会标出主要说话人；声纹不是身份认证，更换麦克风或环境后可能需要删除 `data/speakers.json` 重新聚类。临时文本在语句结束前可能变化；最终文本仍可能受远场拾音、环境噪声和多人同时说话影响。
