# Spec: 谛听（Diting）

## Objective

为 macOS 本机提供 Go 编写的 7×24 小时语音记录服务。麦克风音频持续进入 Silero VAD 和流式 ASR：只在检测到人声时保存按天分目录的压缩音频分段（默认 AAC/M4A，失败回退 WAV），同时向内置 Web 控制台推送临时识别文本；语句结束后保存最终文本、时间戳和录音路径。后台再通过本机 LM Studio 判断同一说话人连续语句的语义段落边界，把原文逐字拼接为更长的段落，供 Markdown 文稿使用。控制台还提供按天归档日历与废纸篓删除、声纹人名别名和场景术语表。

成功标准：启动后可从 `http://127.0.0.1:8080` 启停监听，实时看到临时文本，语句结束后看到最终文本并回放/下载录音；刷新页面不丢历史记录；停止后不再写入录音；所有音频和文本均留在本机；可按天把录音目录移到废纸篓并删除对应转写。

## Tech Stack

- Go 1.25.6，CGO enabled，macOS arm64。
- `gen2brain/malgo`：CoreAudio 麦克风采集，16kHz、单声道、S16 PCM。
- `k2-fsa/sherpa-onnx-go/v2`：Silero VAD、OnlineRecognizer 流式识别与 SenseVoice 完整分段精校；短句在两个独立流式模型结果一致时允许最多两处编辑的保守校正，辅助模型不可用时仅允许一处编辑。
- Go 标准库 `net/http`、`embed`：本地 API、SSE 和内嵌静态控制台。
- 原生 HTML/CSS/JavaScript，无前端构建步骤和外部 CDN。
- 本机 LM Studio OpenAI 兼容接口（可选）：`/v1/chat/completions`，只允许回环地址，用于段落边界判断；不可用时降级为整段拼接。
- macOS 自带 `/usr/bin/afconvert`：把 WAV 编码为 AAC-LC 32 kbps 单声道 M4A，并在重转写时把 M4A 解码为 16 kHz WAV；不可用时退回 WAV。

## Commands

```bash
./scripts/download-models.sh
go build -o bin/diting ./cmd/diting
./bin/diting -listen 127.0.0.1:8080 -models models -data data
go vet ./...
go test ./...
```

## Project Structure

```text
cmd/diting/       → 进程入口、配置和优雅退出
internal/audio/   → 麦克风采集与 PCM 转换
internal/engine/  → VAD、流式识别、分段生命周期、术语表收口
internal/paragraph/ → 段落切分规则、原子快照、LM Studio 客户端、异步 worker
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

## Code Style

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

## Testing Strategy

单元测试覆盖说话人注册表（含别名与声纹更新规则）、存储重写/锁/按天删除/音频编码、Markdown 生成、术语表匹配、段落切分与异步合并（fake Clock/Grouper、httptest 模拟 LM Studio）。每个增量执行 `gofmt`、`go vet`、`go test ./...` 和 `go build`。使用官方示例 WAV 驱动真实 VAD/ASR 路径；通过 curl 验证 API/SSE，通过真实浏览器验证控制台、移动端布局和控制台错误。删除、日历、别名和术语表在 `/tmp` 数据副本与临时废纸篓目录中做端到端验证，不修改生产数据。

## API Contract

- `GET /api/status`：返回监听状态、启动时间、分段数和最近错误。
- `GET /api/stats`：返回全量归档统计（音频文件数、总字节数、转写记录数和字符数），覆盖完整历史并短暂缓存。
- `GET /api/days`：按本地日期返回段数、字数、音频文件数和字节数；包含没有转写记录的孤儿音频。
- `DELETE /api/days/{date}`：校验 `YYYY-MM-DD` 与同源请求；删除当天转写及相交段落，把 `audio/YYYYMMDD` 移到废纸篓；返回删除计数、废纸篓路径，并广播 SSE `archive`。
- `GET /api/speakers`：返回说话人稳定标签、显示名、计数与更新时间。
- `PATCH /api/speakers/{label}`，`{"alias":"章总"}`：设置显示名，空串清除；别名最长 32 字，不得与已有别名或稳定标签重名。
- `GET /api/lexicon` / `PUT /api/lexicon`，`{"rules":[{"from":"张总","to":"章总"}]}`：读取或原子替换场景术语表；`from` 1–20 字、`to` 0–40 字，最多 200 条。
- `PUT /api/capture`，`{"state":"RUNNING"}` 或 `{"state":"STOPPED"}`：幂等启停。
- `GET /api/segments?limit=50&before=<RFC3339>`：倒序历史，限制 `limit` 为 1–100；每条附带 `speakerName` 显示名，`speaker` 仍为稳定标签。
- `GET /api/segments/{id}/audio`：只按已保存 ID 读取音频，不接受文件路径；按 `.m4a`/`.wav` 返回对应 MIME 与实际文件名。
- `GET /api/markdown?limit=500&download=1`：把转写合并为 Markdown；说话人输出显示名；启用段落合并时按段落输出起止时间，窗口边界内的段落整段保留，未覆盖分段沿用逐条格式；`limit` 截取最近 N 条用于预览，`download=1` 返回完整文件。
- `GET /api/events`：SSE，事件为 `status`、`interim`、`segment`、`paragraph`、`archive`、`error`；`segment` 携带 `speakerName`，`paragraph` 只携带段落元信息，不携带正文。
- 错误统一为 `{"error":{"code":"...","message":"..."}}`。

## Boundaries

- Always：只绑定回环地址；限制 HTTP 请求体和分页大小；数据目录权限 0700、文件 0600；动态文本用 `textContent` 渲染，Markdown 预览先转义文本再插入固定标签；验证 Origin/Host；设置 CSP 等安全头。
- Always：所有新增、修改、删除接口都要求同源；按天删除是破坏性操作，页面必须先二次确认；音频与转写只落在 `data/` 和废纸篓目录。
- Always：LLM 地址必须是回环地址（`127.0.0.1`/`localhost`/`::1`），段落层只读写 `data/` 下的派生文件；运行 `gofmt`、`go vet`、构建和真实推理验证；不记录完整转写文本到日志。
- Ask first：开放局域网访问、增加鉴权、接入云 ASR、把转写文本发送给非回环或外部模型、定时自动清理历史。
- Never：上传音频或文本；提交模型、录音、转写和秘密；提供任意文件读取；在音频回调中执行阻塞操作。

## Success Criteria

- VAD 静音时不产生录音；静音约 1 秒后才结束当前分段，人声结束后生成 16kHz 单声道音频（默认 M4A，失败回退 WAV）和 JSONL 最终记录。
- 说话过程中 Web 控制台收到 interim，结束后收到 final，无需刷新。
- 启停操作幂等，重复启动不会打开多个麦克风设备。
- 重启服务后历史最终记录仍可查看，录音可回放和下载；历史 WAV 与新旧 M4A 均可播放和重转写。
- 右侧 Markdown 合并文稿可显示/隐藏并记住选择；临时识别文本实时跟随，分段写入后替换为最终文本，可下载完整 `.md`。
- 按天日历按本地日期展示段数与体积，点击日期可查看统计并把整天数据移到废纸篓；删除后日历、归档统计、历史与 Markdown 同步更新，其他标签页收到 `archive` 后刷新。
- 同一说话人的连续语句持续合并为语义段落，只有换人、单 run 达到 20 段或 3000 字才切分；每收到一条新的最终语句就异步重判当前段落，录音、识别和 SSE 不被阻塞，重叠覆盖为零；模型不可用时整段拼接并可重试。
- 说话人始终保存稳定标签，别名只影响显示；术语表只作用于最终文本，二者都不静默改写历史记录。
- 页面在 320px、768px、1440px 可用，键盘可操作，SSE 断线自动重连并有状态提示。
- Ctrl+C 后停止设备、冲刷当前分段并关闭 HTTP 服务。

## Open Questions

默认保留所有本地历史，不做定时自动清理，但可在控制台按本地日期把整天录音目录移到废纸篓并删除对应转写。按 VAD 片段标注主导说话人（CAM++ 声纹中心与别名持久化在 `data/speakers.json`）；短片段只匹配不更新，明显不同且不短于 3 秒才新建说话人。段落合并只调用本机 LM Studio 且只决定边界，文本逐字拼接，不改写内容。场景术语表保存在 `data/lexicon.json`，只在最终文本收口处应用。`-retranscribe` 可显式全量重跑历史录音，重写前自动备份 `transcripts.jsonl`，保持音频不变，并重建段落层；`-reparagraph` 可单独重建派生段落文件，不重新应用术语表。
