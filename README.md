# 谛听

本机语音记录控制台。Go 单进程完成麦克风采集、Silero VAD 人声检测、流式语音识别、声纹说话人标注、按天分目录的压缩分段录音、JSONL 文本持久化，并通过本机 LM Studio 异步把同一说话人的连续语句合并为更长的语义段落，最终在内嵌 Web 页面实时展示与导出 Markdown 文稿。

## 功能

- 只在检测到人声时保存录音，附带约 500ms 前置音频，并按 VAD 绝对采样位置去除重叠。
- 说话停顿不足约 1 秒时继续合并，静音达到约 1 秒后才结束当前分段。
- 连续讲话每约 30 秒强制分段，避免 VAD 缓冲持续增长。
- 说话过程中通过 SSE 推送 4 路 beam search 的流式临时文本，语句结束后使用本地 SenseVoice 对完整录音精校并写入带标点的最终记录。对不超过 30 字、没有内部标点的中文短句，若两个独立流式模型正文一致且与精校最多有两处编辑差异，则采用一致的流式正文并保留精校标点；辅助模型不可用或结果不一致时，仅在一处编辑差异时采用主流式结果，其余情况始终保留精校结果。
- 使用 3D-Speaker CAM++ 声纹模型为每个片段标注主导说话人（说话人1、说话人2…），声纹中心持久化到 `data/speakers.json`，跨重启沿用编号。
- 说话人保留稳定标签 `说话人N`，可在控制台点击标签设置人名别名（如”章总”）；别名只影响控制台、SSE 与 Markdown 显示，不改写历史记录。
- 支持将多个说话人标签合并为同一人（`mergedInto` 链）；合并后段落内容自动拼接，显示名统一为根标签的别名。设置相同别名时自动触发合并。合并文稿中的说话人名可点击标注。
- 内置声纹评估基础设施：从已标注录音导出测试集（`-export-testset`），用任意模型跑 EER / 准确率 / 分数分布（`-eval-speakers`），支持多模型横向对比和阈值推荐。
- 新录音默认用 macOS 自带 `/usr/bin/afconvert` 编码为 AAC-LC 32 kbps 单声道 `.m4a`，比 WAV 小一个数量级；编码失败或系统没有 `afconvert` 时自动保留 WAV。历史 WAV 不迁移，仍可回放与重转写。
- 录音按本地日期保存在 `data/audio/YYYYMMDD/`；左侧日历按天展示归档，格子颜色深浅对应当天录音体积，可把整天录音目录移到废纸篓并删除对应转写。
- 支持场景术语映射：在控制台维护 `错词=正词`（如“张总=章总”），只作用于之后生成的最终文本；改词表不会静默改写历史，`-retranscribe` 时才会重新应用。
- 支持 `-retranscribe` 用当前模型全量重跑历史录音，修复旧版本产生的文本并重算说话人标签；音频文件不会被删除或改写，WAV 与 M4A 都可解码。
- 可选接入本机 LM Studio，异步判断同一说话人连续语句的语义段落边界；模型只决定分段，文本逐字拼接，不润色、不改写。每收到一条新的最终语句就立即重判当前段落，不需要等待空闲；启动后后台自动回填全部历史，录音、识别和 SSE 不被阻塞。
- 段落层写入派生文件 `data/paragraphs.jsonl`，只用于 Markdown 预览与导出；左侧历史仍逐条展示，每条录音仍可单独回放。同一说话人只有换人、单 run 达到 20 段或 3000 字才切分。模型不可用时先按整 run 拼接，后续新语句、下次启动或 `-reparagraph` 会自动重试。
- 控制台启停麦克风、查看历史、回放和下载录音，查看按天归档并删除整天数据。
- 右侧实时合并转录为 Markdown 文稿预览，正在识别的临时文本也会即时跟随；可隐藏，并支持下载完整 `.md` 文件。
- 所有识别和存储都在本机，不调用云 ASR，也不上传音频或文本。
- 服务重启后恢复最近历史；磁盘上的 JSONL 和音频不会被启动过程清空。

## 环境

- macOS Apple Silicon（当前验证环境）。
- Go 1.25+，CGO 开启。
- 首次运行需要下载官方模型；语义段落合并需要本机 LM Studio（可选，模型不可用时自动降级为整段拼接）。
- 压缩录音依赖 macOS 自带的 `/usr/bin/afconvert`；缺失时自动回退 WAV，不影响功能。

## 启动

```bash
./scripts/download-models.sh
go build -o bin/diting ./cmd/diting
./bin/diting
```

下载脚本默认拉取 CAM++ 声纹模型；如需对比其他模型，可追加 `--speaker-model=NAME`（可选值见"声纹评估"小节）：

```bash
./scripts/download-models.sh models --speaker-model=eres2net-large
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
-eval-speakers          用测试集评估声纹模型后退出
-export-testset         从已标注录音导出测试集后退出
-testset FILE           测试集 JSONL 路径，默认 <data>/testset.jsonl
```

停止服务按 `Ctrl+C`。程序会关闭麦克风、冲刷当前语音分段，再停止 HTTP 服务。

环境变量：

```text
DITING_AUDIO_FORMAT=wav|m4a   强制新录音格式，默认有 afconvert 时用 m4a
DITING_TRASH_DIR=DIR          按天删除时使用的废纸篓目录，默认 ~/.Trash
```

段落合并要求 LM Studio 在 `http://127.0.0.1:1234` 提供 OpenAI 兼容接口并已加载指定模型。启动时只做一次健康检查，不可达不会阻止启动；为避免转写文本离开本机，`-llm-url` 只接受 `127.0.0.1`、`localhost` 或 `::1`。

## 控制台

页面分为三个区域：

- 左侧：运行状态、麦克风启停、“全部归档”统计（录音文件数、占用空间、转写文字数），以及按天日历。日历格子的颜色深浅按当天录音体积取对数分级；点击有数据的日期可查看当天段数、字数、录音数和大小，并把整天数据移到废纸篓。
- 中间：实时转录。讲话过程中显示青灰色临时文本，语句结束后替换为带标点的最终段落，并附说话人标签。点击说话人标签可设置人名或合并到其他说话人；顶部”术语表”可维护场景替换规则。
- 右侧：Markdown 合并文稿预览。同一说话人的连续语句会异步合并为带起止时间的段落，默认展示最近 500 条记录并滚动到最新一条，面板高度随窗口自适应；顶部可下载完整 `.md` 或隐藏面板。隐藏状态保存在浏览器本地存储中，刷新后保持；窄屏时预览自动移到正文下方。合并文稿中的说话人名可点击直接标注。

## 说话人标注

- 每个 VAD 片段只标注一个主导说话人；多人同时抢话的长片段只会标出主要说话人。
- 片段短于 0.5 秒不提取声纹；达到 0.5 秒但短于 2 秒时只参与已有说话人匹配，不更新声纹中心，避免“嗯”“啊”之类短音污染质心。
- 不短于 4 秒的片段会额外提取重叠的 3 秒窗口声纹，检测多人抢话和窗口分歧；一致时保留完整片段声纹，不一致时只采用主导窗口组，低一致性片段不新建说话人也不更新质心。
- 命中且不短于 2 秒时才可能更新质心，同时要求窗口质量合格、得分高于阈值 0.03，并与第二候选拉开 0.03，减少误匹配污染；更新权重按历史次数封顶 20。
- 得分落在 `[阈值-0.08, 阈值)` 时复用得分最高的说话人但不更新中心，减少同一人被拆成多个编号；只有明显低于阈值、窗口质量合格且时长不短于 3 秒才新建 `说话人N`。
- `-speaker-threshold` 默认 `0.45`：调高更易拆出新说话人，调低更易合并，建议用人工确认过的测试集评估后调整。
- 稳定标签 `说话人N` 不变，人名别名只用于显示；可在控制台点击标签修改，也可通过 `PATCH /api/speakers/{label}` 设置。别名最长 32 字，不能与其他别名或稳定标签重名。
- 支持将多个标签合并为同一人：在控制台说话人对话框选择"合并到"目标，或调用 `PUT /api/speakers/{label}/merge`。合并后显示名统一解析到根标签的别名，段落内容自动重建拼接。为两个标签设置相同别名时自动触发合并。取消合并（target 置空）后恢复独立。
- 声纹不是身份认证。更换麦克风或环境后如果编号混乱，停止服务、删除 `data/speakers.json`，再执行一次 `-retranscribe` 即可重新聚类；别名也会随该文件清除。
- 声纹模型缺失或加载失败不会阻塞启动，服务照常运行，只是记录不带说话人标签。

## 声纹评估

支持用标注数据量化声纹模型效果，对比不同模型并推荐阈值。

测试集格式（`data/testset.jsonl`，每行一条 JSON）：

```json
{"audio": "audio/20261106/20261106-140000.000000000.m4a", "speaker": "Andy"}
{"audio": "/absolute/path/to/independent.wav", "speaker": "Bob", "offset": 1.0, "limit": 5.0}
```

- `audio`：相对 data 目录或绝对路径
- `speaker`：真实说话人标签（ground truth）
- `offset`/`limit`（可选）：截取音频片段（秒）

从已标注录音导出测试集：

```bash
./bin/diting -export-testset
```

导出使用当前别名和合并关系作为 ground truth；导出后请手动检查 `data/testset.jsonl`，修正错误标注。

评估当前模型：

```bash
./bin/diting -eval-speakers
```

输出 EER（等错误率）、当前阈值准确率、正/负对分数分布和建议阈值。

对比不同模型：

```bash
./scripts/download-models.sh models --speaker-model=eres2net-large
./bin/diting -eval-speakers -speaker-model models/3dspeaker_speech_eres2net_large_sv_zh-cn_3dspeaker_16k.onnx
```

可用模型（`--speaker-model=NAME`）：

| 名称 | 文件 | 维度 |
|------|------|------|
| campplus | 3dspeaker_speech_campplus_sv_zh-cn_16k-common.onnx | 192 |
| eres2net | 3dspeaker_speech_eres2net_sv_zh-cn_16k-common.onnx | 192 |
| eres2net-base | 3dspeaker_speech_eres2net_base_200k_sv_zh-cn_16k-common.onnx | 192 |
| eres2net-large | 3dspeaker_speech_eres2net_large_sv_zh-cn_3dspeaker_16k.onnx | 512 |
| wespeaker-r34 | wespeaker_cnceleb_resnet34.onnx | 256 |

切换到新模型后，若维度与旧 `speakers.json` 不同，需删除该文件重新聚类：

```bash
rm data/speakers.json
./bin/diting -speaker-model models/3dspeaker_speech_eres2net_large_sv_zh-cn_3dspeaker_16k.onnx -retranscribe
```

## 场景术语表

- 规则保存在 `data/lexicon.json`：

```json
{"version":1,"rules":[{"from":"张总","to":"章总"}]}
```

- 只在最终文本收口处应用，实时临时文本保持模型原始输出；实时保存、`-verify-wav`、`-retranscribe` 都会应用当前词表。
- 按 `from` 长度降序做单趟最长匹配，不级联替换；例如同时存在 `张总=章总` 与 `章总=张总` 时不会来回替换。
- `from` 为 1–20 字，`to` 为 0–40 字（留空表示删除该词），最多 200 条；控制台每行写一条 `错词=正词`，`#` 开头的行会被忽略。
- 修改词表不会静默改写已保存文本；需要重跑历史时使用 `-retranscribe`，`-reparagraph` 不会重新应用词表。

## 数据

```text
data/
├── .lock                   # 单进程占用锁，自动创建
├── transcripts.jsonl
├── transcripts.jsonl.bak   # 原子重写或 -retranscribe 后生成
├── paragraphs.jsonl        # 派生段落层，可随时删除重建
├── speakers.json           # 声纹中心、人名别名与合并关系，可删除以重新聚类
├── lexicon.json            # 场景术语表，不存在视为空
├── testset.jsonl           # 声纹评估测试集（-export-testset 生成），可手动编辑
└── audio/
    └── YYYYMMDD/
        └── <segment-id>.m4a   # 新录音；afconvert 不可用或编码失败时为 .wav
```

`audio/` 目录名等于分段 `startedAt` 的本地日期。历史 WAV 不会被自动迁移；播放、下载和重转写会按实际扩展名处理。

每条 JSONL 记录的字段：

```text
id          分段 ID，同时是音频文件名
startedAt   分段开始时间（RFC3339Nano）
endedAt     分段结束时间
durationMs  录音时长
text        最终精校文本
speaker     主导说话人的稳定标签，声纹不可用时省略
audioPath   相对 data 目录的音频路径（.m4a 或 .wav）
```

`speaker` 始终保存稳定标签；人名别名和合并关系存在 `speakers.json` 的 `alias` / `mergedInto` 字段中，只在 API、SSE 和 Markdown 输出时解析，不写回历史 JSONL。

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

数据目录创建为 `0700`，文件创建为 `0600`。默认不自动清理历史；可在控制台按天把整天数据移到废纸篓。

按天删除的语义：

- 先原子重写 `transcripts.jsonl` 去掉当天记录，并删除与这些记录相交的派生段落；随后把 `audio/YYYYMMDD` 整个目录移到 `~/.Trash`（可用 `DITING_TRASH_DIR` 覆盖），目录名为 `diting-YYYYMMDD-HHMMSS`。
- 跨卷时自动退化为复制后删除源目录；不写入 Finder 的“放回原处”元数据，恢复需手动从废纸篓拖回。
- 若录音目录移动失败，转写已删除，占用空间会以孤儿音频的形式继续显示在日历中，可再次删除该天重试。
- 删除不回收 `speakers.json` 中的声纹档案与别名，`count` 可能偏大，但不影响匹配与显示。

同一数据目录只允许一个进程使用（`data/.lock`）。服务运行期间执行 `-retranscribe` 会被拒绝，请先停止服务。

## API

```text
GET /api/status
GET /api/stats               全量统计：录音数、总大小、转写字数
GET /api/days                按天统计：段数、字数、录音数、占用
DELETE /api/days/{date}      删除某天转写，并把 audio/YYYYMMDD 移到废纸篓
GET /api/speakers            说话人稳定标签、显示名与计数
PATCH /api/speakers/{label}  {"alias":"章总"}；空串清除别名
PUT /api/speakers/{label}/merge  {"target":"说话人1"}；空串取消合并
GET /api/lexicon             场景术语表规则
PUT /api/lexicon             {"rules":[{"from":"张总","to":"章总"}]}
PUT /api/capture             {"state":"RUNNING"} 或 {"state":"STOPPED"}
GET /api/segments?limit=50   可选 before=RFC3339Nano 向前翻页；limit 范围 1–100
GET /api/segments/{id}/audio
GET /api/markdown?limit=500  Markdown 合并文稿；download=1 导出完整文件
GET /api/events              SSE: status / interim / segment / paragraph / archive / error
```

`/api/stats` 从 `transcripts.jsonl` 与 `data/audio` 汇总，覆盖全部历史而非最近 500 条窗口；结果缓存 3 秒，新分段写入后控制台会自动刷新。

`/api/days` 按本地日期聚合完整转写，并单独统计 `audio/` 下的 `.wav` 与 `.m4a`，包含没有转写记录的孤儿音频。`DELETE /api/days/{date}` 只接受 `YYYY-MM-DD`，要求同源请求；返回删除的段数、段落数、录音数、大小和废纸篓路径，并向 SSE 广播 `archive` 事件供其他标签页刷新。

`/api/segments` 与 SSE `segment` 事件在记录字段之外附带 `speakerName`（当前显示名）；`speaker` 仍是稳定标签。Markdown 输出同样使用显示名。

Markdown 文稿按请求从内存即时生成：启用段落合并时按段落输出起止时间，窗口边界内的段落整段保留，未覆盖的分段沿用单时间格式；`-merge=false` 时与旧版逐条输出一致。`limit` 只影响预览（上限 5000），下载时省略即可拿到全部记录。`paragraph` 事件只包含段落元信息（ID、起止时间、说话人、成员数和来源），不重复推送正文。

## 验证

```bash
gofmt -w cmd internal
go vet ./...
go test ./...
go build -o bin/diting ./cmd/diting
```

测试覆盖声纹注册表（匹配/新建/持久化、短片段抑制更新、多窗口一致性、低质量与歧义更新抑制、不确定区间复用、质心权重封顶、人名别名校验、合并链与取消合并）、声纹评估（测试集加载、M4A 解码、EER 计算、准确率、分数分布、阈值推荐）、JSONL 存储（顺序、备份、重写、锁竞争、归档统计、按天删除与废纸篓、AAC 编码与权限、WAV 回退、超出内存窗口的音频查找）、全量重跑（保留旧文本、缺失音频、说话人重算、压缩音频解码与 WAVE_FORMAT_EXTENSIBLE 头规范化）、Markdown 生成与接口参数校验、按天/说话人/术语表接口、段落切分（换人/上限、忽略停顿、逐字拼接、边界校验、原子快照、LLM 请求与解析、异步 worker 的逐句重判、回填、删除后重载、降级、冷却与关闭取消）、术语表（最长匹配、不级联、校验、并发替换）。

模型下载后，可用模型目录中的示例 WAV 验证实际推理。该命令会通过完整引擎路径在 `data` 写入真实记录（默认按新格式生成 `.m4a`，并应用当前术语表），并输出整段精校对照、最终文本和 interim/final 事件数量：

```bash
./bin/diting -verify-wav \
  models/sherpa-onnx-streaming-zipformer-zh-int8-2025-06-30/test_wavs/0.wav
```

全量重跑历史录音（建议先备份或在数据副本上验证）：

```bash
./bin/diting -retranscribe
```

该命令会重写 `data/transcripts.jsonl`，并把原文件备份为 `data/transcripts.jsonl.bak`。音频缺失的记录原样保留并计数；解码为空的记录保留旧文本。`.wav` 直接读取，`.m4a` 先用 `afconvert` 解码为临时 16 kHz WAV 再识别；音频本身不会改动。如需回滚，执行 `mv data/transcripts.jsonl.bak data/transcripts.jsonl`。删除 `data/speakers.json` 后重跑会重新聚类说话人编号。重跑同时会重新应用当前 `data/lexicon.json` 术语表，并清空段落层；`-merge=true` 时接着用同一批转写同步重建 `paragraphs.jsonl`。

只重建段落层（不修改转写与音频）：

```bash
./bin/diting -reparagraph
```

该命令读取现有 `transcripts.jsonl`，按当前模型重新生成 `paragraphs.jsonl` 后退出；服务运行期间会因数据锁被拒绝，请先停止服务。LM Studio 不可达时会快速回退为整段拼接。该命令不会重新应用术语表。

## 常驻运行说明

程序本身用于长时间驻留：音频回调只写入有界队列，识别在独立 goroutine，慢 SSE 客户端会被断开，内存历史限制为最近 500 条。开机自启和进程崩溃后的自动拉起应由 macOS `launchd` 管理，本项目不会擅自安装系统服务。

## 项目结构

```text
cmd/diting/          命令行入口、参数解析、verify/retranscribe/reparagraph/eval-speakers/export-testset 子流程
internal/audio/      麦克风采集
internal/engine/     VAD、流式识别、SenseVoice 精校、分段会话、声纹推理、全量重跑
internal/speaker/    声纹注册表（余弦质心 + 人名别名 + 合并链 + JSON 持久化）、评估（EER / 准确率 / 阈值推荐）
internal/paragraph/  段落切分规则、原子快照、LM Studio 客户端与异步 worker
internal/lexicon/    场景术语表（原子 JSON 持久化 + 单趟最长匹配）
internal/store/      JSONL 存储、音频编码（AAC/WAV）、按天归档、废纸篓、单进程锁
internal/server/     回环 HTTP API、SSE、Markdown 文稿生成
web/                 内嵌控制台页面（go:embed）
models/              本地模型文件
data/                录音与转录数据
```

## 已知限制

当前版本不包含云端同步和局域网访问；历史清理需要手动按天删除，没有自动保留策略。新录音默认压缩为 M4A，但历史 WAV 不会自动迁移；如需回收空间，可另行做批量转码。按天删除到废纸篓不会写入“放回原处”元数据，恢复后也不会自动恢复已删除的转写。说话人按 VAD 片段标注主导说话人，多人快速抢话的片段只会标出主要说话人；不确定区间会复用得分最高的说话人，可能误标，可用人名别名或合并功能人工纠正。声纹不是身份认证，更换麦克风或环境后可能需要删除 `data/speakers.json` 重新聚类（别名、合并关系与计数会一并清除）。术语表是字面替换，可能过度替换，建议使用足够具体的词；它只影响之后生成的文本，历史需要 `-retranscribe` 重建。临时文本在语句结束前可能变化；最终文本仍可能受远场拾音、环境噪声和多人同时说话影响。30 秒强制分段可能截断句中停顿，段落合并会按语义把相邻语句重新拼接。段落生成是异步的：每条新语句完成即触发当前段落重判，期间边界可能小幅调整；模型漏掉个别句子时会自动把缺口并入相邻段落，只有重叠、越界等无法修复的边界才按整 run 拼接为 `fallback`（后续新语句、下次启动或 `-reparagraph` 会重试）；单 run 上限 20 段、3000 字。
