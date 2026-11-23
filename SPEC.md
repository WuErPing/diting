# Spec: 谛听（Diting）

<p align="center">
  <a href="#中文">中文</a> | <a href="#english">English</a>
</p>

## 中文

### Objective

为 macOS 本机提供 Go 编写的 7×24 小时语音记录服务。麦克风音频持续进入 Silero VAD 和流式 ASR：只在检测到人声时保存按天分目录的压缩音频分段（默认 AAC/M4A，失败回退 WAV），同时向内置 Web 控制台推送临时识别文本；语句结束后保存最终文本、时间戳和录音路径。后台再通过可配置的 OpenAI 兼容模型 API 判断跨说话人的完整会议议题，把原文逐字拼接为议题段落，并过滤不具讨论价值的寒暄与闲聊，供 Markdown 文稿和主题索引用。控制台还提供按天归档日历与废纸篓删除、声纹人名别名和场景术语表。

成功标准：启动后可从 `http://127.0.0.1:8080` 启停监听，实时看到临时文本，语句结束后看到最终文本并回放/下载录音；刷新页面不丢历史记录；停止后不再写入录音；默认音频始终留在本机，只有用户明确选择 MiMo ASR 时才把最终 WAV 分段发送到该服务，转写文本只发送给明确配置的议题模型 API；可按天把录音目录移到废纸篓并删除对应转写。

### Tech Stack

- Go 1.25.6，CGO enabled，macOS arm64。
- `gen2brain/malgo`：CoreAudio 麦克风采集，16kHz、单声道、S16 PCM。
- `k2-fsa/sherpa-onnx-go/v2`：Silero VAD、OnlineRecognizer 本地流式识别与默认的 SenseVoice 完整分段精校；短句在两个独立流式模型结果一致时允许最多两处编辑的保守校正，辅助模型不可用时仅允许一处编辑。
- 小米 `mimo-v2.5-asr`：可选的最终分段识别，通过 Token Plan OpenAI 兼容 `/chat/completions` 上传 Base64 WAV（`input_audio`），失败时回退本地 SenseVoice；默认关闭。
- Go 标准库 `net/http`、`embed`：本地 API、SSE 和内嵌静态控制台。
- 原生 HTML/CSS/JavaScript，无前端构建步骤和外部 CDN。
- OpenAI 兼容模型 API：默认 `https://token-plan-cn.xiaomimimo.com/v1` 与 `mimo-v2.6-flash`，支持 API Key、远程 HTTPS 和本机 LM Studio，用于会议议题边界、价值判断与语义修正；不可用时降级为整段拼接并暂不生成新主题。
- macOS 自带 `/usr/bin/afconvert`：把 WAV 编码为 AAC-LC 32 kbps 单声道 M4A，并在重转写时把 M4A 解码为 16 kHz WAV；不可用时退回 WAV。

### Commands

```bash
./scripts/download-models.sh
go build -o bin/diting ./cmd/diting
./bin/diting -listen 127.0.0.1:8080 -models models -data data
go vet ./...
go test ./...
```

### Project Structure

```text
cmd/diting/       → 进程入口、配置和优雅退出
internal/audio/   → 麦克风采集与 PCM 转换
internal/engine/  → VAD、流式识别、分段生命周期、术语表收口
internal/paragraph/ → 会议议题切分、价值过滤、原子快照、OpenAI 兼容客户端、异步 worker
internal/speaker/ → 声纹注册表、人名别名
internal/lexicon/ → 场景术语表与单趟最长匹配
internal/store/   → 音频编码与 JSONL 持久化、按天归档、废纸篓
internal/server/  → HTTP API、SSE、安全边界
web/              → 内嵌控制台 HTML/CSS/JS
models/           → 本地模型（不入库）
data/             → 录音与文本（不入库）
scripts/          → 官方模型下载脚本
tasks/            → 实施计划和检查点
```

### Code Style

```go
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return nil
	}
	return e.startLocked(ctx)
}
```

使用 `gofmt`；错误附加上下文；音频回调不做推理或文件 I/O；共享状态由 mutex 或单一 goroutine 管理；公开 JSON 字段使用 camelCase。

### Testing Strategy

单元测试覆盖说话人注册表（含别名与声纹更新规则）、存储重写/锁/按天删除/音频编码、Markdown 生成、术语表匹配、跨说话人议题切分与价值过滤、异步合并（fake Clock/Grouper、httptest 模拟模型 API）。每个增量执行 `gofmt`、`go vet`、`go test ./...` 和 `go build`。使用官方示例 WAV 驱动真实 VAD/ASR 路径；通过 curl 验证 API/SSE，通过真实浏览器验证控制台、移动端布局和控制台错误。删除、日历、别名和术语表在 `/tmp` 数据副本与临时废纸篓目录中做端到端验证，不修改生产数据。

### API Contract

- `GET /api/status`：返回监听状态、启动时间、分段数和最近错误。
- `GET /api/stats`：返回全量归档统计（音频文件数、总字节数、转写记录数和字符数），覆盖完整历史并短暂缓存。
- `GET /api/days`：按本地日期返回段数、字数、音频文件数和字节数；包含没有转写记录的孤儿音频。
- `DELETE /api/days/{date}?dryRun=1`：校验 `YYYY-MM-DD` 与同源请求；`dryRun` 时只返回影响面预览。真实删除前把当天转写快照写入 `data/backups/`，随后删除当天转写及相交段落，把 `audio/YYYYMMDD` 移到废纸篓；返回删除计数、废纸篓路径、快照路径，并广播 SSE `archive`。
- `POST /api/days/{date}/minutes`：读取本地日期属于当天的全部非空转写，附带时间和说话人显示名，用当前段落模型生成 Markdown 会议纪要；返回日期、转写段数和 Markdown 正文，不改写原始转写或派生段落。日历选择日期后可生成、预览并下载 `.md` 文件。
- `GET /api/settings`：返回当前段落模型、语义修正开关与 ASR 选择；`PUT /api/settings` 接收 `{"model":"mimo-v2.6-flash","correct":false,"asr":"local|mimo"}` 并原子持久化，实时应用后续识别与段落处理。
- `GET /api/models`：返回当前 OpenAI 兼容端点的模型 ID 列表。
- `GET /api/speakers`：返回说话人稳定标签、显示名、计数与更新时间。
- `GET /api/speakers/samples`：按注册表顺序返回每个稳定声纹标签最近一条具有非空转写的样板句子及录音元信息；没有可用样板时省略 `sample`，空白转写与未登记标签不作为样板。
- `PATCH /api/speakers/{label}`，`{"alias":"章总"}`：设置显示名，空串清除；别名最长 32 字，不得与已有别名或稳定标签重名。
- `GET /api/lexicon` / `PUT /api/lexicon`，`{"rules":[{"from":"张总","to":"章总"}]}`：读取或原子替换场景术语表；`from` 1–20 字、`to` 0–40 字，最多 200 条。
- `PUT /api/capture`，`{"state":"RUNNING"}` 或 `{"state":"STOPPED"}`：幂等启停。
- `GET /api/segments?limit=50&before=<RFC3339>`：倒序历史，限制 `limit` 为 1–100；每条附带 `speakerName` 显示名，`speaker` 仍为稳定标签。
- `GET /api/segments/{id}/audio`：只按已保存 ID 读取音频，不接受文件路径；按 `.m4a`/`.wav` 返回对应 MIME 与实际文件名。
- `DELETE /api/segments/{id}`：校验录音 ID 与同源请求，删除指定转写并把对应录音移到废纸篓；移除与该分段相交的派生段落，触发历史重建、统计失效和 SSE `archive`，使合并文稿不再包含该记录。声音记录页逐条提供带确认的删除按钮。
- `GET /api/markdown?limit=500&date=YYYY-MM-DD&download=1`：把转写合并为 Markdown；说话人输出显示名；启用段落合并时按段落输出起止时间，窗口边界内的段落整段保留，未覆盖分段沿用逐条格式；`limit` 截取最近 N 条用于预览，`date` 改为仅显示本地开始时间属于当天的完整段落且不受 `limit` 截断，`download=1` 返回完整文件。
- `GET /api/paragraphs?limit=500&date=YYYY-MM-DD`：返回与最近 N 条转写相交的段落 ID、起止时间、大主题 `category`、小主题名称、支持行内 Markdown 的简明总结和 `hasTopic`，供主题索引与逐段操作使用；`date` 改为返回本地开始时间属于当天的全部段落且不受 `limit` 截断；模型判定为低信息内容的 `topicExcluded` 段落及空白、纯标点、纯语气词和笑声标记为无主题，旧段落缺少议题字段时按原文回退生成。
- `POST /api/paragraphs/split`，`{"limit":500}`：用当前模型按会议议题重新划分与最近 N 条转写相交的完整 run；跨说话人判断问答、讨论、决策和行动项，只更新派生段落，不改写原始转写，返回 run、模型切分、单句、降级和段落计数。
- `POST /api/paragraphs/correct`，`{"limit":500}`：用当前模型手动修正与最近 N 条转写相交的已保存段落，即使自动修正暂停也可执行；返回检查段落数与实际变更数，不改写原始转写。
- `POST /api/paragraphs/{id}/correct`：用当前模型只修正指定段落，即使自动修正暂停也可执行；返回检查段落数与实际变更数，不改写原始转写或其他段落。
- `POST /api/paragraphs/{id}/asr`：用当前选择的本地 / MiMo ASR 重新识别指定段落的全部录音，原子更新原始逐句转写，再按原边界刷新该派生段落；任一录音缺失或解码失败则整段不改，返回录音数与文本变更数。
- `POST /api/paragraphs/{id}/summarize`：用当前段落模型总结指定段落，把独立摘要保存到 `digest` 并显示在正文下方；不改写原始转写、段落正文、议题名称或主题简介。右侧主题链接按段落 ID 滚动定位并高亮对应文稿。
- `GET /api/events`：SSE，事件为 `status`、`interim`、`segment`、`paragraph`、`archive`、`error`；`segment` 携带 `speakerName`，`paragraph` 只携带段落元信息，不携带正文。
- `GET /health`：存活探针，返回 `{"status":"ok"}`。
- `GET /ready`：就绪探针，停止中返回 503，否则返回当前状态。
- `GET /metrics`：Prometheus 文本格式指标，包含分段保存数、LLM 请求/失败数、SSE 连接数、音频丢弃数等。
- 所有 `/api/` 端点同时暴露在 `/api/v1/` 前缀下；新消费者应使用 `/api/v1/`。OpenAPI 3.0 契约见 `api/openapi.yaml`。
- 错误统一为 `{"error":{"code":"...","message":"..."}}`。

### Boundaries

- Always：只绑定回环地址；限制 HTTP 请求体和分页大小；数据目录权限 0700、文件 0600；动态文本用 `textContent` 渲染，Markdown 预览先转义文本再插入固定标签；验证 Origin/Host；设置 CSP 等安全头。
- Always：所有新增、修改、删除接口都要求同源；按天删除是破坏性操作，页面必须先二次确认；音频与转写只落在 `data/` 和废纸篓目录。
- Always：本机 LLM 地址允许回环 HTTP，远程 LLM 必须使用 HTTPS；API Key 只从命令行、环境变量、`~/code/keys/xiaomi` 或用户已有的 OpenCode MiMo 凭证读取，不写入设置文件、页面或日志；段落层只读写 `data/` 下的派生文件；运行 `gofmt`、`go vet`、构建和真实推理验证；不记录完整转写文本到日志。
- Always：ASR 默认 `local`；只有用户在设置中明确选择 `mimo` 或传入 `-asr=mimo`，才把当前最终 WAV 分段上传到已配置的 MiMo 端点，并在界面持续显示云端上传提示。云端失败时回退本地识别，不丢弃录音或已有文本。
- Ask first：开放局域网访问、增加鉴权、接入用户未明确配置的其他云 ASR、把转写文本发送到用户未明确配置的其他外部服务、定时自动清理历史。
- Never：在默认本地模式上传音频；提交模型、录音、转写和秘密；提供任意文件读取；在音频回调中执行阻塞操作。议题分析和语义修正仅把必要转写文本发送到当前配置的模型 API。

### Success Criteria

- VAD 静音时不产生录音；静音约 1 秒后才结束当前分段，人声结束后生成 16kHz 单声道音频（默认 M4A，失败回退 WAV）和 JSONL 最终记录。
- 说话过程中 Web 控制台收到本地 interim，结束后收到 final，无需刷新；首页通过“配置”按钮进入独立配置页，统一配置模型与识别、语义修正和术语表；可在本地 SenseVoice 与 MiMo ASR 间切换最终识别，选择持久化且云端失败回退本地。
- 启停操作幂等，重复启动不会打开多个麦克风设备。
- 重启服务后历史最终记录仍可查看，录音可回放和下载；历史 WAV 与新旧 M4A 均可播放和重转写。
- 首页中间直接展示 Markdown 合并文稿，可显示/隐藏并记住选择；临时识别文本在文稿末尾实时跟随，分段写入后替换为最终文本，可下载完整 `.md`。左上角品牌旁以动画区分等待人声与正在说话，麦克风停止时隐藏。最右侧主题分类按“大时间段 + 大主题 → 小主题”树状归组，同名大主题不跨 10 分钟以上静音分隔的时间段合并，显示对应时段，默认只展开最新大类，其余自动折叠且保留手动折叠状态，同步列出简短主题名称与支持 Markdown 的简明总结，旧数据归入“其他主题”；声音记录通过链接进入独立页面。
- 按天日历按本地日期展示段数与体积，点击日期可查看统计、只显示开始时间属于当天的段落并跳转到文稿开始处，再次点击取消筛选；可把整天数据移到废纸篓，删除后日历、归档统计、历史与 Markdown 同步更新，其他标签页收到 `archive` 后刷新。
- 跨说话人的连续发言按完整会议议题合并，说话人变化不强制切分，静音达到 10 分钟时开启新的大时间段，单 run 达到 20 段或 3000 字也会切分；模型只把有实质讨论价值的段落送入主题索引，每个大时间段内根据小主题内容二次归并近义大类，寒暄、闲聊和无信息过渡保留在文稿但不生成主题；每收到一条新的最终语句就异步重判当前段落，录音、识别和 SSE 不被阻塞，重叠覆盖为零；模型不可用时整段拼接并可重试。
- 说话人始终保存稳定标签，别名只影响显示；独立声纹配置页按标签展示最近样板句子和原音，可内联修改人名、合并或取消合并重复声纹，保存后重新读取标签与样板；术语表只作用于最终文本，二者都不静默改写历史记录。
- 页面在 320px、768px、1440px 可用，键盘可操作，SSE 断线自动重连并有状态提示。
- Ctrl+C 后停止设备、冲刷当前分段并关闭 HTTP 服务。

### Open Questions

默认保留所有本地历史，不做定时自动清理，但可在控制台按本地日期把整天录音目录移到废纸篓并删除对应转写。按 VAD 片段标注主导说话人（CAM++ 声纹中心与别名持久化在 `data/speakers.json`）；短片段只匹配不更新，明显不同且不短于 3 秒才新建说话人。议题分析默认调用小米 MiMo API，由模型跨说话人决定边界、判断讨论价值并生成议题名称与简介，正文逐字拼接，不改写内容；可配置为本机模型以避免转写文本离开本机。场景术语表保存在 `data/lexicon.json`，只在最终文本收口处应用。`-retranscribe` 可显式全量重跑历史录音，重写前自动备份 `transcripts.jsonl`，保持音频不变，并重建段落层；`-reparagraph` 可单独重建派生段落文件，不重新应用术语表。

## English

### Objective

Provide a 7×24 voice recording service written in Go for local macOS use. Microphone audio flows continuously into Silero VAD and streaming ASR: compressed audio segments (AAC/M4A by default, WAV fallback) are saved into per-day directories only when speech is detected, while interim recognition text is pushed to the built-in web console; after each utterance ends, the final text, timestamps, and recording path are saved. In the background, a configurable OpenAI-compatible model API identifies complete meeting topics across speakers, concatenates the original text verbatim into topic paragraphs, and filters out greetings and small talk with no discussion value, for use in the Markdown transcript and topic index. The console also provides a daily archive calendar with Trash deletion, voiceprint name aliases, and a domain lexicon.

Success criteria: after startup, listening can be started and stopped from `http://127.0.0.1:8080`, interim text appears in real time, and final text appears after each utterance with playback/download; refreshing the page does not lose history; no recordings are written after stopping; audio stays local by default — final WAV segments are sent to MiMo ASR only when the user explicitly selects it, and transcript text is sent only to the explicitly configured topic model API; a day's recording directory can be moved to the Trash with the corresponding transcripts deleted.

### Tech Stack

- Go 1.25.6, CGO enabled, macOS arm64.
- `gen2brain/malgo`: CoreAudio microphone capture, 16 kHz, mono, S16 PCM.
- `k2-fsa/sherpa-onnx-go/v2`: Silero VAD, OnlineRecognizer local streaming recognition, and default SenseVoice full-segment refinement; short utterances allow conservative corrections of at most two edits when two independent streaming models agree, or only one edit when the auxiliary model is unavailable.
- Xiaomi `mimo-v2.5-asr`: optional final-segment recognition via the Token Plan OpenAI-compatible `/chat/completions` endpoint, uploading Base64 WAV (`input_audio`), falling back to local SenseVoice on failure; disabled by default.
- Go standard library `net/http`, `embed`: local API, SSE, and embedded static console.
- Native HTML/CSS/JavaScript, no frontend build step or external CDN.
- OpenAI-compatible model API: default `https://token-plan-cn.xiaomimimo.com/v1` with `mimo-v2.6-flash`; supports API keys, remote HTTPS, and local LM Studio; used for meeting topic boundaries, value judgments, and semantic correction; degrades to whole-run concatenation without new topics when unavailable.
- macOS built-in `/usr/bin/afconvert`: encodes WAV to AAC-LC 32 kbps mono M4A and decodes M4A back to 16 kHz WAV during retranscription; falls back to WAV when unavailable.

### Commands

```bash
./scripts/download-models.sh
go build -o bin/diting ./cmd/diting
./bin/diting -listen 127.0.0.1:8080 -models models -data data
go vet ./...
go test ./...
```

### Project Structure

```text
cmd/diting/       → process entry point, configuration, graceful shutdown
internal/audio/   → microphone capture and PCM conversion
internal/engine/  → VAD, streaming recognition, segment lifecycle, lexicon finalization
internal/paragraph/ → meeting topic splitting, value filtering, atomic snapshots, OpenAI-compatible client, async worker
internal/speaker/ → voiceprint registry, name aliases
internal/lexicon/ → domain lexicon and single-pass longest match
internal/store/   → audio encoding and JSONL persistence, daily archives, Trash
internal/server/  → HTTP API, SSE, security boundaries
web/              → embedded console HTML/CSS/JS
models/           → local models (not committed)
data/             → recordings and text (not committed)
scripts/          → official model download scripts
tasks/            → implementation plans and checkpoints
```

### Code Style

```go
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return nil
	}
	return e.startLocked(ctx)
}
```

Use `gofmt`; wrap errors with context; no inference or file I/O in audio callbacks; shared state is guarded by a mutex or owned by a single goroutine; public JSON fields use camelCase.

### Testing Strategy

Unit tests cover the speaker registry (including aliases and voiceprint update rules), store rewrite/locking/daily deletion/audio encoding, Markdown generation, lexicon matching, cross-speaker topic splitting and value filtering, and async merging (fake Clock/Grouper, httptest-mocked model API). Every increment runs `gofmt`, `go vet`, `go test ./...`, and `go build`. Official sample WAVs drive the real VAD/ASR path; curl verifies API/SSE, and a real browser verifies the console, mobile layout, and console errors. Deletion, calendar, aliases, and the lexicon are verified end to end against a `/tmp` data copy and a temporary Trash directory, never against production data.

### API Contract

- `GET /api/status`: returns listening state, start time, segment count, and last error.
- `GET /api/stats`: returns full archive statistics (audio file count, total bytes, transcript record count, and character count), covering the entire history with a short-lived cache.
- `GET /api/days`: returns segment count, character count, audio file count, and bytes per local date; includes orphan audio without transcript records.
- `DELETE /api/days/{date}?dryRun=1`: validates `YYYY-MM-DD` and same-origin requests; with `dryRun` it only returns an impact preview. Before a real deletion, the day's transcript snapshot is written to `data/backups/`, then the day's transcripts and intersecting paragraphs are deleted and `audio/YYYYMMDD` is moved to the Trash; returns deletion counts, Trash path, snapshot path, and broadcasts SSE `archive`.
- `POST /api/days/{date}/minutes`: reads all non-empty transcripts whose local date falls on that day, with timestamps and speaker display names, and generates Markdown meeting minutes with the current paragraph model; returns the date, transcript segment count, and Markdown body without rewriting original transcripts or derived paragraphs. After selecting a date in the calendar, minutes can be generated, previewed, and downloaded as `.md`.
- `GET /api/settings`: returns the current paragraph model, semantic correction toggle, and ASR selection; `PUT /api/settings` accepts `{"model":"mimo-v2.6-flash","correct":false,"asr":"local|mimo"}`, persists atomically, and applies to subsequent recognition and paragraph processing in real time.
- `GET /api/models`: returns the model ID list of the current OpenAI-compatible endpoint.
- `GET /api/speakers`: returns speaker stable labels, display names, counts, and update times.
- `GET /api/speakers/samples`: returns, in registry order, the most recent sample sentence with a non-empty transcript for each stable voiceprint label plus recording metadata; `sample` is omitted when no sample is available, and blank transcripts and unregistered labels are not used as samples.
- `PATCH /api/speakers/{label}`, `{"alias":"章总"}`: sets the display name, empty string clears it; aliases are at most 32 characters and must not duplicate existing aliases or stable labels.
- `GET /api/lexicon` / `PUT /api/lexicon`, `{"rules":[{"from":"张总","to":"章总"}]}`: reads or atomically replaces the domain lexicon; `from` is 1–20 characters, `to` is 0–40 characters, at most 200 rules.
- `PUT /api/capture`, `{"state":"RUNNING"}` or `{"state":"STOPPED"}`: idempotent start/stop.
- `GET /api/segments?limit=50&before=<RFC3339>`: reverse-chronological history with `limit` constrained to 1–100; each entry carries a `speakerName` display name while `speaker` remains the stable label.
- `GET /api/segments/{id}/audio`: reads audio only by saved ID, never by file path; returns the matching MIME type and actual filename for `.m4a`/`.wav`.
- `DELETE /api/segments/{id}`: validates the recording ID and same-origin request, deletes the transcript and moves the recording to the Trash; removes derived paragraphs intersecting the segment, triggers history rebuild, stats invalidation, and SSE `archive` so the merged transcript no longer contains the record. The records page provides a per-entry delete button with confirmation.
- `GET /api/markdown?limit=500&date=YYYY-MM-DD&download=1`: merges transcripts into Markdown; speakers are output with display names; when paragraph merging is enabled, paragraphs are output with start/end times, paragraphs within the window boundary are kept whole, and uncovered segments fall back to per-entry format; `limit` truncates to the latest N entries for preview, `date` instead shows complete paragraphs whose local start time falls on that day without `limit` truncation, and `download=1` returns the full file.
- `GET /api/paragraphs?limit=500&date=YYYY-MM-DD`: returns paragraph IDs, start/end times, major topic `category`, minor topic title, a concise summary supporting inline Markdown, and `hasTopic` for paragraphs intersecting the latest N transcripts, for the topic index and per-paragraph actions; `date` instead returns all paragraphs whose local start time falls on that day without `limit` truncation; paragraphs the model judges as low-information (`topicExcluded`) plus blank, punctuation-only, filler-only, and laughter content are marked topic-less, and old paragraphs missing topic fields fall back to generation from the original text.
- `POST /api/paragraphs/split`, `{"limit":500}`: re-splits the complete runs intersecting the latest N transcripts by meeting topic with the current model; judges Q&A, discussion, decisions, and action items across speakers, updates only derived paragraphs without rewriting original transcripts, and returns run, model-split, single-sentence, degraded, and paragraph counts.
- `POST /api/paragraphs/correct`, `{"limit":500}`: manually corrects saved paragraphs intersecting the latest N transcripts with the current model, even when automatic correction is paused; returns checked and actually changed paragraph counts without rewriting original transcripts.
- `POST /api/paragraphs/{id}/correct`: corrects only the specified paragraph with the current model, even when automatic correction is paused; returns checked and actually changed counts without rewriting original transcripts or other paragraphs.
- `POST /api/paragraphs/{id}/asr`: re-recognizes all recordings of the specified paragraph with the currently selected local / MiMo ASR, atomically updates the original per-utterance transcripts, then refreshes the derived paragraph along its original boundaries; if any recording is missing or fails to decode, the whole paragraph is left unchanged; returns recording and text-change counts.
- `POST /api/paragraphs/{id}/summarize`: summarizes the specified paragraph with the current paragraph model, saves the standalone digest to `digest`, and displays it below the body; does not rewrite original transcripts, paragraph bodies, topic names, or topic summaries. Topic links on the right scroll to the paragraph ID and highlight the corresponding transcript.
- `GET /api/events`: SSE with events `status`, `interim`, `segment`, `paragraph`, `archive`, `error`; `segment` carries `speakerName`, and `paragraph` carries only paragraph metadata, not the body.
- `GET /health`: liveness probe, returns `{"status":"ok"}`.
- `GET /ready`: readiness probe, returns 503 while stopping, otherwise the current state.
- `GET /metrics`: Prometheus text-format metrics including saved segments, LLM requests/failures, SSE connections, dropped audio chunks, and more.
- All `/api/` endpoints are also exposed under the `/api/v1/` prefix; new consumers should use `/api/v1/`. The OpenAPI 3.0 contract is in `api/openapi.en.yaml` (Chinese version: `api/openapi.yaml`).
- Errors are uniformly `{"error":{"code":"...","message":"..."}}`.

### Boundaries

- Always: bind only to loopback addresses; limit HTTP request body and pagination sizes; data directory permissions 0700, files 0600; render dynamic text with `textContent`, and escape text before inserting fixed tags in Markdown previews; validate Origin/Host; set CSP and other security headers.
- Always: all create, update, and delete endpoints require same-origin requests; daily deletion is destructive and the page must confirm twice first; audio and transcripts land only in `data/` and the Trash directory.
- Always: local LLM addresses may use loopback HTTP, remote LLMs must use HTTPS; API keys are read only from the command line, environment variables, `~/code/keys/xiaomi`, or the user's existing OpenCode MiMo credentials, and are never written to settings files, pages, or logs; the paragraph layer reads and writes only derived files under `data/`; run `gofmt`, `go vet`, builds, and real-inference verification; never log full transcript text.
- Always: ASR defaults to `local`; only when the user explicitly selects `mimo` in settings or passes `-asr=mimo` are final WAV segments uploaded to the configured MiMo endpoint, with a persistent cloud-upload notice in the interface. On cloud failure, fall back to local recognition without dropping recordings or existing text.
- Ask first: opening LAN access, adding authentication, integrating other cloud ASR services the user has not explicitly configured, sending transcript text to other external services the user has not explicitly configured, scheduled automatic history cleanup.
- Never: upload audio in the default local mode; commit models, recordings, transcripts, or secrets; provide arbitrary file reads; perform blocking operations in audio callbacks. Topic analysis and semantic correction send only the necessary transcript text to the currently configured model API.

### Success Criteria

- No recordings are produced during VAD silence; the current segment ends only after about 1 second of silence, and after speech ends a 16 kHz mono audio file (M4A by default, WAV fallback) and a final JSONL record are generated.
- While speaking, the web console receives local interim results, and final results after the utterance ends, without refreshing; the home page's "Settings" button opens a dedicated settings page unifying model and recognition, semantic correction, and lexicon configuration; final recognition can switch between local SenseVoice and MiMo ASR, the choice persists, and cloud failure falls back to local.
- Start/stop operations are idempotent; repeated starts do not open multiple microphone devices.
- After a service restart, historical final records remain viewable and recordings can be played back and downloaded; historical WAV and both old and new M4A files can be played and retranscribed.
- The home page center directly shows the merged Markdown transcript, which can be shown/hidden with the choice remembered; interim recognition text follows the end of the transcript in real time and is replaced by final text once the segment is written; the full `.md` can be downloaded. Next to the brand mark in the top-left corner, an animation distinguishes waiting for speech from active speaking, hidden when the microphone is stopped. The rightmost topic panel groups topics in a "major time period + major topic → minor topic" tree; identically named major topics are not merged across time periods separated by more than 10 minutes of silence, the corresponding time range is shown, only the latest group is expanded by default with the rest collapsed and manual collapse state preserved, and short topic titles plus concise Markdown-capable summaries are listed alongside; old data falls under "Other topics"; records are reached via a link to a dedicated page.
- The daily calendar shows segment counts and volume per local date; clicking a date shows statistics, displays only paragraphs whose start time falls on that day, and jumps to the start of the transcript; clicking again clears the filter; a whole day's data can be moved to the Trash, after which the calendar, archive stats, history, and Markdown update in sync, and other tabs refresh upon receiving `archive`.
- Continuous cross-speaker speech is merged by complete meeting topic; speaker changes do not force splits, 10 minutes of silence starts a new major time period, and a single run also splits at 20 segments or 3000 characters; the model sends only paragraphs with substantive discussion value to the topic index, synonymous major categories are merged a second time within each time period based on minor topic content, and greetings, small talk, and low-information transitions stay in the transcript without generating topics; each new final utterance triggers an asynchronous re-evaluation of the current paragraph without blocking recording, recognition, or SSE, with zero overlapping coverage; when the model is unavailable, whole-run concatenation is used with retries possible later.
- Speakers always keep stable labels; aliases affect display only; the dedicated voiceprint settings page shows the latest sample sentence and original audio per label, supports inline name edits and merging or unmerging duplicate voiceprints, and reloads labels and samples after saving; the lexicon applies only to final text, and neither silently rewrites historical records.
- Pages work at 320px, 768px, and 1440px, are keyboard-operable, and SSE reconnects automatically with a status indicator.
- After Ctrl+C, the device stops, the current segment is flushed, and the HTTP service shuts down.

### Open Questions

All local history is retained by default with no scheduled automatic cleanup, but the console can move a whole day's recording directory to the Trash by local date and delete the corresponding transcripts. The dominant speaker is labeled per VAD segment (CAM++ voiceprint centroids and aliases persist in `data/speakers.json`); short segments only match without updating, and a new speaker is created only when clearly different and at least 3 seconds long. Topic analysis calls the Xiaomi MiMo API by default, with the model deciding boundaries across speakers, judging discussion value, and generating topic names and summaries; bodies are concatenated verbatim without rewriting content; a local model can be configured to keep transcript text on the machine. The domain lexicon is saved in `data/lexicon.json` and applied only at final-text finalization. `-retranscribe` explicitly re-runs all historical recordings, automatically backing up `transcripts.jsonl` before rewriting, keeping audio unchanged, and rebuilding the paragraph layer; `-reparagraph` rebuilds only the derived paragraph file without re-applying the lexicon.