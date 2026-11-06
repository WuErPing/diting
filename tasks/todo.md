# Tasks

## [x] Task 1: 验证 Go、sherpa 和模型主链路

**Acceptance criteria:**
- 官方模型下载到忽略目录，最小程序可加载 VAD 和 OnlineRecognizer。
- macOS arm64 构建和动态库加载成功。

**Verification:** `go build ./...`；真实 WAV 推理产生文本。
**Dependencies:** None
**Files:** `go.mod`、`scripts/download-models.sh`、`cmd/diting/main.go`、`.gitignore`
**Scope:** M

## [x] Task 2: 实现录音与文本持久化

**Acceptance criteria:**
- WAV 为 16kHz 单声道；JSONL 包含 ID、文本、起止时间和音频路径。
- 重启后读取有限最近历史，文件权限受限。

**Verification:** 构建/vet；真实分段落盘并重新读取。
**Dependencies:** Task 1
**Files:** `internal/store/store.go`、`internal/store/wav.go`
**Scope:** S

## [x] Task 3: 实现流式识别和 VAD 录音

**Acceptance criteria:**
- 同一路 PCM 输出 interim 事件并在 VAD 结束时保存 final/WAV。
- Start/Stop 幂等；回调无推理和磁盘 I/O；停止时冲刷当前语音。

**Verification:** 构建/vet；真实 PCM 走完整识别和录音路径。
**Dependencies:** Tasks 1–2
**Files:** `internal/audio/capture.go`、`internal/audio/pcm.go`、`internal/engine/engine.go`
**Scope:** M

## [x] Task 4: 实现本地 API 和 SSE

**Acceptance criteria:**
- 实现 SPEC 中全部端点和统一错误结构，限制分页和请求体。
- 慢客户端不阻塞引擎；外站 Origin 和非常规 Host 被拒绝。

**Verification:** curl 验证状态、幂等启停、历史、音频、SSE、安全头。
**Dependencies:** Tasks 2–3
**Files:** `internal/server/server.go`、`internal/server/events.go`
**Scope:** M

## [x] Task 5: 实现内嵌 Web 控制台

**Acceptance criteria:**
- 启停、状态、interim、final、历史、回放和下载可用。
- 320px/768px/1440px 响应式，键盘可用，空态/错误/重连明确，无外部资源。

**Verification:** 真实浏览器检查交互、布局、可访问性和控制台错误。
**Dependencies:** Task 4
**Files:** `web/index.html`、`web/app.css`、`web/app.js`、`internal/server/assets.go`
**Scope:** M

## [x] Task 6: 完整验证与运行文档

**Acceptance criteria:**
- 全部标准命令通过，模型到 Web 的路径完成真实验证。
- 启动、权限、数据位置、控制台和限制写入 README。

**Verification:** `gofmt`、`go vet ./...`、`go test ./...`、`go build`；浏览器与 curl 端到端检查；`git status` 清理运行产物。
**Dependencies:** Tasks 1–5
**Files:** `README.md`、必要的实现修正
**Scope:** S

## [x] Task 7: 使用真实录音改进转写质量

**Acceptance criteria:**
- 新版中文流式模型提供 interim，SenseVoice 对 VAD 完整分段精校 final。
- 使用现有真实 WAV 对照旧结果，保留原始历史，不上传音频或文本。
- 构建、vet、普通/race 编译检查与端到端识别验证通过。

**Verification:** 三段真实录音的新旧文本对照；完整引擎、真实麦克风与 SSE 启停验证。
**Dependencies:** Tasks 1–6
**Files:** `internal/engine/`、`scripts/download-models.sh`、`README.md`
**Scope:** M

## Checkpoint: Complete

- [x] 真实 VAD/ASR 路径工作：官方中文 WAV 产生 13 条 interim 和 1 条 final。
- [x] API/SSE/Web 控制台工作：真实启停、历史、回放下载、状态事件及四种宽度通过。
- [x] 构建、vet、普通/race 编译检查通过；govulncheck 无已知漏洞。
- [x] 无运行产物或秘密进入版本库；示例数据与截图在验收后清理。
