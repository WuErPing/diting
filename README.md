# 谛听

本机语音记录控制台。Go 单进程完成麦克风采集、Silero VAD 人声检测、流式语音识别、声纹说话人标注、WAV 分段录音、JSONL 文本持久化，并通过本机 LM Studio 异步把同一说话人的连续语句合并为更长的语义段落，最终在内嵌 Web 页面实时展示与导出 Markdown 文稿。

## 功能

- 只在检测到人声时保存录音，附带约 500ms 前置音频，并按 VAD 绝对采样位置去除重叠。
- 说话停顿不足约 1 秒时继续合并，静音达到约 1 秒后才结束当前分段。
- 连续讲话每约 30 秒强制分段，避免 VAD 缓冲持续增长。
- 说话过程中通过 SSE 推送流式临时文本，语句结束后使用本地 SenseVoice 对完整录音精校并写入带标点的最终记录。
- 使用 3D-Speaker CAM++ 声纹模型为每个片段标注主导说话人（说话人1、说话人2…），声纹中心持久化到 `data/speakers.json`，跨重启沿用编号。
- 支持 `-retranscribe` 用当前模型全量重跑历史录音，修复旧版本产生的文本并重算说话人标签；WAV 不会被修改。
- 可选接入本机 LM Studio，异步判断同一说话人连续语句的语义段落边界；模型只决定分段，文本逐字拼接，不润色、不改写。启动后后台自动回填全部历史，录音、识别和 SSE 不被阻塞。
- 段落层写入派生文件 `data/paragraphs.jsonl`，只用于 Markdown 预览与导出；左侧历史仍逐条展示，每条 WAV 仍可单独回放。模型不可用时先按整 run 拼接，下次启动或 `-reparagraph` 自动重试。
- 控制台启停麦克风、查看历史、回放和下载 WAV。
- 右侧实时合并转录为 Markdown 文稿预览，正在识别的临时文本也会即时跟随；可隐藏，并支持下载完整 `.md` 文件。
- 所有识别和存储都在本机，不调用云 ASR，也不上传音频或文本。
- 服务重启后恢复最近历史；磁盘上的 JSONL 和 WAV 不会被启动过程清空。

## 环境

- macOS Apple Silicon（当前验证环境）。
- Go 1.25+，CGO 开启。
- 首次运行需要下载官方模型；语义段落合并需要本机 LM Studio（可选，模型不可用时自动降级为整段拼接）。

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
-llm-url http://127.0.0.1:1234/v1  LM Studio OpenAI 兼容地址，只允许回环地址
-llm-model qwen3.6-35b-a3b-splash  段落切分模型
-merge=true             启用异步段落合并；false 时 Markdown 退回逐条格式
-reparagraph            用现有转写全量重建段落层后退出
```

停止服务按 `Ctrl+C`。程序会关闭麦克风、冲刷当前语音分段，再停止 HTTP 服务。

段落合并要求 LM Studio 在 `http://127.0.0.1:1234` 提供 OpenAI 兼容接口并已加载指定模型。启动时只做一次健康检查，不可达不会阻止启动；为避免转写文本离开本机，`-llm-url` 只接受 `127.0.0.1`、`localhost` 或 `::1`。

## 控制台

页面分为三个区域：

- 左侧：运行状态、麦克风启停、最近记录（回放、下载 WAV）。
- 中间：实时转录。讲话过程中显示青灰色临时文本，语句结束后替换为带标点的最终段落，并附说话人标签。
- 右侧：Markdown 合并文稿预览。同一说话人的连续语句会异步合并为带起止时间的段落，默认展示最近 500 条记录并滚动到最新一条，面板高度随窗口自适应；顶部可下载完整 `.md` 或隐藏面板。隐藏状态保存在浏览器本地存储中，刷新后保持；窄屏时预览自动移到正文下方。

## 说话人标注

- 每个 VAD 片段只标注一个主导说话人；多人同时抢话的长片段只会标出主要说话人。
- 片段短于 0.5 秒不提取声纹；短于 2 秒只参与已有说话人匹配，不新建说话人，避免“嗯”“啊”之类短音产生噪声聚类；匹配失败且不短于 2 秒才新建 `说话人N`。
- 匹配基于余弦相似度和质心漂移：命中后质心按历史次数加权更新，因此说话人特征会随录音逐渐稳定。
- `-speaker-threshold` 默认 `0.45`：调高更易拆出新说话人，调低更易合并，建议按实际录音调整。
- 声纹不是身份认证。更换麦克风或环境后如果编号混乱，停止服务、删除 `data/speakers.json`，再执行一次 `-retranscribe` 即可重新聚类。
- 声纹模型缺失或加载失败不会阻塞启动，服务照常运行，只是记录不带说话人标签。

## 数据

```text
data/
├── .lock                   # 单进程占用锁，自动创建
├── transcripts.jsonl
├── transcripts.jsonl.bak   # 仅在 -retranscribe 后生成
├── paragraphs.jsonl        # 派生段落层，可随时删除重建
├── speakers.json           # 声纹中心，可删除以重新聚类
└── audio/
    └── YYYYMMDD/
        └── <segment-id>.wav
```

每条 JSONL 记录的字段：

```text
id          分段 ID，同时是 WAV 文件名
startedAt   分段开始时间（RFC3339Nano）
endedAt     分段结束时间
durationMs  录音时长
text        最终精校文本
speaker     主导说话人标签，声纹不可用时省略
audioPath   相对 data 目录的 WAV 路径
```

`paragraphs.jsonl` 是全量派生快照，每次更新都原子重写；一个分段只属于一个段落，同一分段不会被重复覆盖。字段：

```text
id          段落 ID，取首段 segment ID，重新切分后保持稳定
startedAt   段落开始时间
endedAt     段落结束时间
speaker     段落主导说话人
text        段内原始转写逐字拼接，不润色
segmentIds  成员分段 ID，按时间顺序
source      llm / single / fallback
model       产出该段落的模型
updatedAt   最近写入时间
```

删除 `paragraphs.jsonl` 不影响转写与录音；服务启动时会按现有 `transcripts.jsonl` 重新回填。

数据目录创建为 `0700`，文件创建为 `0600`。默认不自动删除历史，请按自己的保留周期清理磁盘。

同一数据目录只允许一个进程使用（`data/.lock`）。服务运行期间执行 `-retranscribe` 会被拒绝，请先停止服务。

## API

```text
GET /api/status
PUT /api/capture             {"state":"RUNNING"} 或 {"state":"STOPPED"}
GET /api/segments?limit=50   可选 before=RFC3339Nano 向前翻页；limit 范围 1–100
GET /api/segments/{id}/audio
GET /api/markdown?limit=500  Markdown 合并文稿；download=1 导出完整文件
GET /api/events              SSE: status / interim / segment / paragraph / error
```

Markdown 文稿按请求从内存即时生成：启用段落合并时按段落输出起止时间，窗口边界内的段落整段保留，未覆盖的分段沿用单时间格式；`-merge=false` 时与旧版逐条输出一致。`limit` 只影响预览（上限 5000），下载时省略即可拿到全部记录。`paragraph` 事件只包含段落元信息（ID、起止时间、说话人、成员数和来源），不重复推送正文。

## 验证

```bash
gofmt -w cmd internal web
go vet ./...
go test ./...
go build -o bin/diting ./cmd/diting
```

测试覆盖声纹注册表（匹配/新建/持久化）、JSONL 存储（顺序、备份、重写、锁竞争）、全量重跑（保留旧文本、缺失 WAV、说话人重算）、Markdown 生成与接口参数校验、段落切分（停顿/换人/上限、逐字拼接、边界校验、原子快照、LLM 请求与解析、异步 worker 的空闲合并、回填、降级、冷却与关闭取消）。

模型下载后，可用模型目录中的示例 WAV 验证实际推理。该命令会通过完整引擎路径在 `data` 写入真实记录，并输出整段精校对照、最终文本和 interim/final 事件数量：

```bash
./bin/diting -verify-wav \
  models/sherpa-onnx-streaming-zipformer-zh-int8-2025-06-30/test_wavs/0.wav
```

全量重跑历史录音（建议先备份或在数据副本上验证）：

```bash
./bin/diting -retranscribe
```

该命令会重写 `data/transcripts.jsonl`，并把原文件备份为 `data/transcripts.jsonl.bak`。WAV 缺失的记录原样保留并计数；解码为空的记录保留旧文本。WAV 本身不会改动；如需回滚，执行 `mv data/transcripts.jsonl.bak data/transcripts.jsonl`。删除 `data/speakers.json` 后重跑会重新聚类说话人编号。重跑会同时清空段落层；`-merge=true` 时接着用同一批转写同步重建 `paragraphs.jsonl`。

只重建段落层（不修改转写与 WAV）：

```bash
./bin/diting -reparagraph
```

该命令读取现有 `transcripts.jsonl`，按当前模型重新生成 `paragraphs.jsonl` 后退出；服务运行期间会因数据锁被拒绝，请先停止服务。LM Studio 不可达时会快速回退为整段拼接。

## 常驻运行说明

程序本身用于长时间驻留：音频回调只写入有界队列，识别在独立 goroutine，慢 SSE 客户端会被断开，内存历史限制为最近 500 条。开机自启和进程崩溃后的自动拉起应由 macOS `launchd` 管理，本项目不会擅自安装系统服务。

## 项目结构

```text
cmd/diting/          命令行入口、参数解析、verify/retranscribe/reparagraph 子流程
internal/audio/      麦克风采集
internal/engine/     VAD、流式识别、SenseVoice 精校、分段会话、声纹推理、全量重跑
internal/speaker/    声纹注册表（余弦质心 + JSON 持久化）
internal/paragraph/  段落切分规则、原子快照、LM Studio 客户端与异步 worker
internal/store/      JSONL 存储、WAV 写入、单进程锁、原子重写
internal/server/     回环 HTTP API、SSE、Markdown 文稿生成
web/                 内嵌控制台页面（go:embed）
models/              本地模型文件
data/                录音与转录数据
```

## 已知限制

当前版本不包含云端同步、局域网访问和自动清理。说话人按 VAD 片段标注主导说话人，多人快速抢话的片段只会标出主要说话人；声纹不是身份认证，更换麦克风或环境后可能需要删除 `data/speakers.json` 重新聚类。临时文本在语句结束前可能变化；最终文本仍可能受远场拾音、环境噪声和多人同时说话影响。30 秒强制分段可能截断句中停顿，段落合并会按语义把相邻语句重新拼接。段落生成有延迟：最后一句话后最多等约 60 秒空闲再调用模型；模型给出非法边界时按整 run 拼接为 `fallback`，下次启动或 `-reparagraph` 会重试；单 run 上限 20 段、3000 字。
