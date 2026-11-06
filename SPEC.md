# Spec: 谛听（Diting）

## Objective

为 macOS 本机提供 Go 编写的 7×24 小时语音记录服务。麦克风音频持续进入 Silero VAD 和流式 ASR：只在检测到人声时保存 WAV 分段，同时向内置 Web 控制台推送临时识别文本；语句结束后保存最终文本、时间戳和录音路径。后台再通过本机 LM Studio 判断同一说话人连续语句的语义段落边界，把原文逐字拼接为更长的段落，供 Markdown 文稿使用。

成功标准：启动后可从 `http://127.0.0.1:8080` 启停监听，实时看到临时文本，语句结束后看到最终文本并回放/下载 WAV；刷新页面不丢历史记录；停止后不再写入录音；所有音频和文本均留在本机。

## Tech Stack

- Go 1.25.6，CGO enabled，macOS arm64。
- `gen2brain/malgo`：CoreAudio 麦克风采集，16kHz、单声道、S16 PCM。
- `k2-fsa/sherpa-onnx-go/v2`：Silero VAD、OnlineRecognizer 流式识别与 SenseVoice 完整分段精校。
- Go 标准库 `net/http`、`embed`：本地 API、SSE 和内嵌静态控制台。
- 原生 HTML/CSS/JavaScript，无前端构建步骤和外部 CDN。
- 本机 LM Studio OpenAI 兼容接口（可选）：`/v1/chat/completions`，只允许回环地址，用于段落边界判断；不可用时降级为整段拼接。

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
internal/engine/  → VAD、流式识别、分段生命周期
internal/paragraph/ → 段落切分规则、原子快照、LM Studio 客户端、异步 worker
internal/store/   → WAV 与 JSONL 持久化、历史读取
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

单元测试覆盖说话人注册表、存储重写/锁、Markdown 生成、段落切分与异步合并（fake Clock/Grouper、httptest 模拟 LM Studio）。每个增量执行 `gofmt`、`go vet`、`go test ./...` 和 `go build`。使用官方示例 WAV 驱动真实 VAD/ASR 路径；通过 curl 验证 API/SSE，通过真实浏览器验证控制台、移动端布局和控制台错误。段落层使用 `data/transcripts.jsonl` 副本做端到端回填校验，不修改生产数据。

## API Contract

- `GET /api/status`：返回监听状态、启动时间、分段数和最近错误。
- `PUT /api/capture`，`{"state":"RUNNING"}` 或 `{"state":"STOPPED"}`：幂等启停。
- `GET /api/segments?limit=50&before=<RFC3339>`：倒序历史，限制 `limit` 为 1–100。
- `GET /api/segments/{id}/audio`：只按已保存 ID 读取 WAV，不接受文件路径。
- `GET /api/markdown?limit=500&download=1`：把转写合并为 Markdown；启用段落合并时按段落输出起止时间，窗口边界内的段落整段保留，未覆盖分段沿用逐条格式；`limit` 截取最近 N 条用于预览，`download=1` 返回完整文件。
- `GET /api/events`：SSE，事件为 `status`、`interim`、`segment`、`paragraph`、`error`；`paragraph` 只携带段落元信息，不携带正文。
- 错误统一为 `{"error":{"code":"...","message":"..."}}`。

## Boundaries

- Always：只绑定回环地址；限制 HTTP 请求体和分页大小；数据目录权限 0700、文件 0600；动态文本用 `textContent` 渲染，Markdown 预览先转义文本再插入固定标签；验证 Origin/Host；设置 CSP 等安全头。
- Always：LLM 地址必须是回环地址（`127.0.0.1`/`localhost`/`::1`），段落层只读写 `data/` 下的派生文件；运行 `gofmt`、`go vet`、构建和真实推理验证；不记录完整转写文本到日志。
- Ask first：开放局域网访问、增加鉴权、接入云 ASR、把转写文本发送给非回环或外部模型、自动删除历史。
- Never：上传音频或文本；提交模型、录音、转写和秘密；提供任意文件读取；在音频回调中执行阻塞操作。

## Success Criteria

- VAD 静音时不产生录音；静音约 1 秒后才结束当前分段，人声结束后生成 16kHz 单声道 WAV 和 JSONL 最终记录。
- 说话过程中 Web 控制台收到 interim，结束后收到 final，无需刷新。
- 启停操作幂等，重复启动不会打开多个麦克风设备。
- 重启服务后历史最终记录仍可查看，录音可回放和下载。
- 右侧 Markdown 合并文稿可显示/隐藏并记住选择；临时识别文本实时跟随，分段写入后替换为最终文本，可下载完整 `.md`。
- 同一说话人的连续语句在空闲约 60 秒或换人后异步合并为语义段落；录音、识别和 SSE 不被阻塞，重叠覆盖为零；模型不可用时整段拼接并可重试。
- 页面在 320px、768px、1440px 可用，键盘可操作，SSE 断线自动重连并有状态提示。
- Ctrl+C 后停止设备、冲刷当前分段并关闭 HTTP 服务。

## Open Questions

默认保留所有本地历史，不做自动清理；按 VAD 片段标注主导说话人（CAM++ 声纹中心持久化在 `data/speakers.json`）。段落合并只调用本机 LM Studio 且只决定边界，文本逐字拼接，不改写内容。`-retranscribe` 可显式全量重跑历史录音，重写前自动备份 `transcripts.jsonl`，保持 WAV 不变，并重建段落层；`-reparagraph` 可单独重建派生段落文件。
