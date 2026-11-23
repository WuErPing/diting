# 谛听使用说明

本文介绍谛听的安装、配置、日常使用和数据维护。项目概览见 [README](../README.md)，版本变化见[更新日志](../CHANGELOG.md)。

## 环境与安装

- macOS Apple Silicon（当前验证环境）。
- Go 1.25+，CGO 开启。
- 首次运行需要下载官方识别模型。
- 压缩录音依赖 macOS 自带的 `/usr/bin/afconvert`；缺失时自动回退 WAV，不影响功能。

```bash
./scripts/download-models.sh
go build -o bin/diting ./cmd/diting
./bin/diting
```

下载脚本默认拉取 CAM++ 声纹模型。如需下载其他声纹模型用于对比：

```bash
./scripts/download-models.sh models --speaker-model=eres2net-large
```

## 启动与停止

打开 <http://127.0.0.1:8080>。首次启动麦克风时，macOS 可能要求允许当前终端或打包应用使用麦克风；请在“系统设置 → 隐私与安全性 → 麦克风”中授权。

默认启动即开始监听。若希望先打开控制台、再手动启动：

```bash
./bin/diting -autostart=false
```

停止服务按 `Ctrl+C`。程序会关闭麦克风、冲刷当前语音分段，再停止 HTTP 服务。

### 命令行参数

```text
-listen 127.0.0.1:8080   本地 HTTP 地址，只允许回环地址
-models models          模型目录
-data data              录音与文本目录
-autostart=true         进程启动后自动打开麦克风
-speaker-model FILE     声纹模型，默认 <models>/3dspeaker_speech_campplus_sv_zh-cn_16k-common.onnx
-speaker-threshold 0.45 说话人匹配的余弦阈值，调高更易拆分，调低更易合并
-verify-wav FILE        用 WAV 验证真实模型识别后退出
-retranscribe           用当前模型全量重跑历史记录后退出
-llm-url https://token-plan-cn.xiaomimimo.com/v1  OpenAI 兼容模型 API；远程地址必须用 HTTPS
-llm-api-key KEY        模型 API 凭证；默认读取环境变量、~/code/keys/xiaomi 或 OpenCode 的 MiMo 凭证
-llm-model mimo-v2.6-flash  会议议题切分模型
-llm-correct=true       使用模型修正派生段落；原始逐句转写不变
-asr local              最终语音识别：local 或 mimo（mimo-v2.5-asr）
-merge=true             启用异步议题切分；false 时 Markdown 退回逐条格式
-reparagraph            用现有转写全量重建段落层后退出
-restore-day DATE       从最新快照恢复某天转写（YYYY-MM-DD），音频需从废纸篓手动拖回
-eval-speakers          用测试集评估声纹模型后退出
-export-testset         从已标注录音导出测试集后退出
-testset FILE           测试集 JSONL 路径，默认 <data>/testset.jsonl
```

### 环境变量

```text
DITING_AUDIO_FORMAT=wav|m4a   强制新录音格式，默认有 afconvert 时用 m4a
DITING_TRASH_DIR=DIR          按天删除时使用的废纸篓目录，默认 ~/.Trash
DITING_LLM_API_KEY=KEY        模型 API 凭证，优先级最高
MIMO_API_KEY=KEY              小米模型 API 凭证，优先级次之
DITING_LOG_LEVEL=LEVEL        日志级别：debug、info、warn、error，默认 info
```

## 模型、识别与隐私

最终识别默认使用本地 SenseVoice。配置页可选择 `mimo-v2.5-asr`，将该段 WAV 上传到 Token Plan 端点生成最终文本；云端失败时自动回退本地识别。本地模式会对部分中文短句保守比对两个独立流式模型的结果，以减少漏字，同时尽量保留精校标点。

议题分析默认调用 Token Plan 端点 `https://token-plan-cn.xiaomimimo.com/v1`，模型为 `mimo-v2.6-flash`。未显式传入 `-llm-api-key` 时，依次读取 `DITING_LLM_API_KEY`、`MIMO_API_KEY`、`~/code/keys/xiaomi`、`~/.config/opencode/opencode.json` 的 `provider.mimo.apiKey` 和旧版 `~/.local/share/opencode/auth.json` 中已有的 Xiaomi 凭证。凭证只用于请求头，不写入 `data/settings.json`、页面或日志。

启动时只做一次文本模型健康检查，不可达不会阻止启动。远程模型必须使用 HTTPS；本机 LM Studio 可使用：

```bash
./bin/diting -llm-url http://127.0.0.1:1234/v1 -llm-model '<模型ID>'
```

本机模型无需 API Key。模型不可用时，段落功能自动降级为整段拼接，后续可自动重试。

默认语音识别和存储都在本机，不上传音频。只有明确选择 MiMo ASR 后才上传最终 WAV 分段；议题分析和语义修正会把必要的转写文本发送到所配置的模型 API。若不希望文本离开本机，请将议题模型配置为本机 LM Studio。

## Web 控制台

首页、声音记录页与配置页统一使用内嵌的品牌标志和 favicon，无需额外静态文件服务。页面默认按浏览器语言选择中文或英文，也可在右上角手动切换；选择保存在浏览器本机。

### 首页

- **左侧**：运行状态、麦克风启停、全量归档统计和按天日历。日历格子的颜色深浅按当天录音体积分级；点击日期可筛选当天文稿、查看统计、生成或重新打开会议纪要，并把整天数据移到废纸篓。
- **中间**：Markdown 合并文稿，讲话时在末尾显示临时文本，品牌旁动画区分等待人声与正在说话。可进入声音记录页、按主题重新分段、下载完整 `.md` 或隐藏文稿。
- **右侧**：主题按“大时间段 + 大主题 → 小主题”归组，显示时段、简短标题和支持 Markdown 的总结。默认只展开最新大类，其余折叠，手动展开状态在刷新时保留；点击小主题可定位到对应文稿段落。

跨说话人的连续发言会异步合并为带起止时间的议题段落，默认展示最近 500 条记录。寒暄、闲聊、语气词和无信息过渡保留在文稿中，但不进入主题索引。段落正文逐字拼接，不润色、不改写原始逐句转写。

每个派生段落提供以下操作：

- **语义修正**：只修改派生文本，不改写原始转写。
- **ASR**：用当前本地或 MiMo 模型重新识别该段全部录音，并同步更新原始转写和该段落；任一录音不可用时整段不改。
- **总结**：生成独立摘要并显示在正文下方，不改写原文、主题名称或主题简介。

选择日期后，可用当前段落模型生成当天会议纪要，查看渲染结果与 Markdown 原文并下载 `.md`。会议纪要保存在 `data/minutes/`，之后可重新打开；生成纪要不会改写原始转写或派生段落。

### 声音记录与配置

声音记录页逐条展示历史，支持分页、录音播放下载、说话人标注、单条删除、多选和批量删除。删除需确认；转写被移除、录音移到废纸篓，相关派生段落和合并文稿自动重建。

配置页统一管理模型与识别、语义修正开关和场景术语表，两类配置分别保存并显示结果。

## 说话人标注

每个 VAD 片段只标注一个主导说话人。稳定标签 `说话人N` 跨重启保留，人名别名只影响控制台、SSE 与 Markdown 显示，不改写历史记录。

- 片段短于 0.5 秒不提取声纹；0.5–2 秒的片段只参与匹配，不更新声纹中心。
- 不短于 4 秒的片段会提取重叠的 3 秒窗口声纹，检测多人抢话和窗口分歧；低一致性片段不新建说话人，也不更新质心。
- 只有匹配质量、窗口质量和候选区分度均满足要求时才更新质心，减少误匹配污染。
- 得分落在 `[阈值-0.08, 阈值)` 时复用得分最高的说话人但不更新中心；只有明显低于阈值、窗口质量合格且时长不短于 3 秒才新建标签。
- `-speaker-threshold` 默认 `0.45`：调高更易拆出新说话人，调低更易合并，建议用人工确认过的测试集评估后调整。
- 声纹模型缺失或加载失败不会阻塞启动，只是记录不带说话人标签。

可在控制台点击标签设置别名，也可通过 `PATCH /api/speakers/{label}` 设置。别名最长 32 字，不能与其他别名或稳定标签重名。

多个标签可合并为同一人：在控制台说话人对话框选择“合并到”目标，或调用 `PUT /api/speakers/{label}/merge`。合并后显示名统一解析到根标签的别名，段落内容自动重建；设置相同别名也会触发合并。取消合并后恢复独立。

声纹不是身份认证。更换麦克风或环境后如果编号混乱，可停止服务、删除 `data/speakers.json`，再执行一次 `-retranscribe` 重新聚类；别名、合并关系和计数也会随该文件清除。

## 声纹评估

测试集默认保存为 `data/testset.jsonl`，每行一条 JSON：

```json
{"audio": "audio/20261106/20261106-140000.000000000.m4a", "speaker": "Andy"}
{"audio": "/absolute/path/to/independent.wav", "speaker": "Bob", "offset": 1.0, "limit": 5.0}
```

- `audio`：相对 data 目录或绝对路径。
- `speaker`：真实说话人标签。
- `offset` / `limit`：可选，截取音频片段的偏移和时长（秒）。

从已标注录音导出测试集：

```bash
./bin/diting -export-testset
```

导出使用当前别名和合并关系作为真实标签；导出后请手动检查并修正错误标注。

评估当前模型：

```bash
./bin/diting -eval-speakers
```

输出 EER（等错误率）、当前阈值准确率、正/负对分数分布和建议阈值。

对比其他模型：

```bash
./scripts/download-models.sh models --speaker-model=eres2net-large
./bin/diting -eval-speakers -speaker-model models/3dspeaker_speech_eres2net_large_sv_zh-cn_3dspeaker_16k.onnx
```

下载脚本支持的 `--speaker-model=NAME`：

| 名称 | 文件 | 维度 |
|------|------|------|
| campplus | 3dspeaker_speech_campplus_sv_zh-cn_16k-common.onnx | 192 |
| eres2net | 3dspeaker_speech_eres2net_sv_zh-cn_16k-common.onnx | 192 |
| eres2net-base | 3dspeaker_speech_eres2net_base_200k_sv_zh-cn_16k-common.onnx | 192 |
| eres2net-large | 3dspeaker_speech_eres2net_large_sv_zh-cn_3dspeaker_16k.onnx | 512 |
| wespeaker-r34 | wespeaker_cnceleb_resnet34.onnx | 256 |

切换到不同维度的模型后，需删除旧 `speakers.json` 并重新聚类：

```bash
rm data/speakers.json
./bin/diting -speaker-model models/3dspeaker_speech_eres2net_large_sv_zh-cn_3dspeaker_16k.onnx -retranscribe
```

## 场景术语表

规则保存在 `data/lexicon.json`：

```json
{"version":1,"rules":[]}
```

配置页每行写一条 `错词=正词`，`#` 开头的行会被忽略。`from` 为 1–20 字，`to` 为 0–40 字（留空表示删除该词），最多 200 条。

术语表只在最终文本收口处应用，实时临时文本保持模型原始输出。匹配按 `from` 长度降序单趟执行，不级联替换。修改词表不会改写已保存文本；需要重跑历史时使用 `-retranscribe`，`-reparagraph` 不会重新应用词表。

## 数据与恢复

```text
data/
├── .lock                   # 单进程占用锁，自动创建
├── transcripts.jsonl
├── transcripts.jsonl.bak   # 原子重写或 -retranscribe 后生成
├── paragraphs.jsonl        # 派生段落层，可删除重建
├── speakers.json           # 声纹中心、人名别名与合并关系
├── lexicon.json            # 场景术语表，不存在视为空
├── settings.json           # 模型、识别与修正设置
├── testset.jsonl           # 声纹评估测试集，可手动编辑
├── tokusage.jsonl          # 模型 token 用量记录
├── minutes/                # 按天保存的会议纪要
├── backups/                # 按天删除前的转写快照
└── audio/
    └── YYYYMMDD/
        └── <segment-id>.m4a   # afconvert 不可用或编码失败时为 .wav
```

录音目录名等于分段开始时间的本地日期。新录音默认使用 AAC-LC 32 kbps 单声道 M4A；历史 WAV 不会自动迁移，仍可播放、下载和重转写。

每条转写 JSONL 记录包含：

```text
id          分段 ID，同时是音频文件名
startedAt   分段开始时间（RFC3339Nano）
endedAt     分段结束时间
durationMs  录音时长
text        最终精校文本
speaker     主导说话人的稳定标签，声纹不可用时省略
audioPath   相对 data 目录的音频路径（.m4a 或 .wav）
```

`paragraphs.jsonl` 是全量派生快照，包含段落起止时间、正文、大主题、小主题、总结、独立摘要、成员分段 ID、来源和模型等字段。删除该文件不影响转写与录音；服务启动时会按现有转写重新回填。

数据目录权限为 `0700`，文件权限为 `0600`。默认不自动清理历史。同一数据目录只允许一个进程使用；服务运行期间执行维护命令会被拒绝，请先停止服务。

### 按天删除与恢复

- 删除前把当天转写快照保存到 `data/backups/YYYYMMDD-HHMMSS/transcripts.jsonl`。
- 程序先移除当天转写及相交派生段落，再把 `audio/YYYYMMDD` 移到 `~/.Trash`，目录名为 `diting-YYYYMMDD-HHMMSS`。
- 跨卷时自动退化为复制后删除源目录；不写入 Finder 的“放回原处”元数据，音频需手动从废纸篓拖回。
- 若录音目录移动失败，转写已删除，孤儿音频仍会显示在日历中，可再次删除该天重试。
- 删除不会回收 `speakers.json` 中的声纹档案与别名。

从最新快照恢复某天转写：

```bash
./bin/diting -restore-day YYYY-MM-DD
```

已存在的分段 ID 会跳过，音频需手动从废纸篓拖回对应目录。

## 历史重建与真实识别验证

全量重跑历史录音会重写 `data/transcripts.jsonl`，并把原文件备份为 `data/transcripts.jsonl.bak`。音频缺失的记录原样保留；解码为空的记录保留旧文本。音频本身不会改动，WAV 与 M4A 均可处理。

```bash
./bin/diting -retranscribe
```

该命令会重新应用当前术语表、重算说话人并重建段落层。建议先备份或在数据副本上验证。如需回滚：

```bash
mv data/transcripts.jsonl.bak data/transcripts.jsonl
```

只重建段落层，不修改转写、音频，也不重新应用术语表：

```bash
./bin/diting -reparagraph
```

模型 API 不可达时会回退为整段拼接，并暂不生成新主题。

模型下载后，可用示例 WAV 验证完整识别路径：

```bash
./bin/diting -verify-wav \
  models/sherpa-onnx-streaming-zipformer-zh-int8-2025-06-30/test_wavs/0.wav
```

该命令会在 `data` 写入真实记录，并输出整段精校对照、最终文本和 interim/final 事件数量。

## API

所有 `/api/` 端点同时暴露在 `/api/v1/` 前缀下，两者行为一致；新消费者应使用 `/api/v1/`。完整 OpenAPI 3.0 契约见 [`api/openapi.yaml`](../api/openapi.yaml)。

```text
GET /api/status
GET /api/stats               全量统计：录音数、总大小、转写字数（带缓存）
GET /api/days                按天统计：段数、字数、录音数、占用（带缓存）
DELETE /api/days/{date}      删除某天转写，并把 audio/YYYYMMDD 移到废纸篓
                             支持 ?dryRun=1 预览影响面；删除前自动快照
POST /api/days/{date}/minutes 生成并保存当天 Markdown 会议纪要
GET /api/days/{date}/minutes  读取已保存的当天会议纪要
GET /api/settings            当前段落模型、语义修正与 ASR 选择
PUT /api/settings            {"model":"mimo-v2.6-flash","correct":false,"asr":"local|mimo"}
GET /api/models              当前 OpenAI 兼容端点的模型列表
GET /api/speakers            说话人稳定标签、显示名与计数
PATCH /api/speakers/{label}  {"alias":"章总"}；空串清除别名
PUT /api/speakers/{label}/merge  {"target":"说话人1"}；空串取消合并
GET /api/lexicon             场景术语表规则
PUT /api/lexicon             {"rules":[{"from":"错词","to":"正词"}]}
PUT /api/capture             {"state":"RUNNING"} 或 {"state":"STOPPED"}
GET /api/segments?limit=50   可选 before=RFC3339Nano 向前翻页；limit 范围 1–100
POST /api/segments/delete    批量删除指定分段
GET /api/segments/{id}/audio
DELETE /api/segments/{id}    删除单条记录并重建相关合并文稿
GET /api/markdown?limit=500  可选 date=YYYY-MM-DD；download=1 导出完整文件
GET /api/paragraphs?limit=500 可选 date=YYYY-MM-DD，返回预览段落及主题元信息
POST /api/paragraphs/split {"limit":500}；按主题重新划分最近窗口涉及的完整 run
POST /api/paragraphs/correct {"limit":500}；手动修正最近窗口内的派生段落
POST /api/paragraphs/{id}/correct 只手动修正指定段落
POST /api/paragraphs/{id}/asr 用当前 ASR 重新识别指定段落的全部录音
POST /api/paragraphs/{id}/summarize 用当前段落模型生成独立摘要
GET /api/events              SSE: status / interim / segment / paragraph / archive / error
GET /health                  存活探针
GET /ready                   就绪探针
GET /metrics                 Prometheus 格式指标
```

`limit` 只影响文稿预览，下载时省略即可拿到全部记录。`date` 用于返回指定本地日期的完整内容，不受 `limit` 截断。段落事件只包含元信息，不重复推送正文。错误统一返回 `{"error":{"code":"...","message":"..."}}`。

## 开发验证与常驻运行

```bash
gofmt -w cmd internal
go vet ./...
go test ./...
go build -o bin/diting ./cmd/diting
```

前端资源通过 `go:embed` 内嵌；修改 `web/` 后必须重新构建并重启 `bin/diting`。

程序用于长时间驻留：音频回调只写入有界队列，识别在独立 goroutine，慢 SSE 客户端会被断开，内存历史限制为最近 500 条。开机自启和崩溃后的自动拉起应由 macOS `launchd` 管理，本项目不会擅自安装系统服务。

## 已知限制

- 不提供云端同步、局域网访问或自动保留策略；历史清理需手动执行。
- 历史 WAV 不会自动迁移；按天删除不会写入 Finder“放回原处”元数据。
- 多人快速抢话时，一个 VAD 片段只会标出主导说话人；声纹结果可能需要人工用别名或合并功能纠正。
- 术语表是字面替换，可能过度替换，只影响之后生成的文本，历史需显式重转写。
- 临时文本在语句结束前可能变化；最终文本仍可能受远场拾音、噪声和多人同时说话影响。
- 连续讲话约 30 秒会强制分段；段落合并在后台异步进行，期间边界可能调整。模型不可用时会降级，之后可通过新语句、重启或 `-reparagraph` 重试。
