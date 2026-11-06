# Implementation Plan: 谛听

## Overview

实现一个单二进制本地服务：同一路 PCM 同时进入流式 ASR 和 Silero VAD。ASR 输出临时文本，VAD 输出带语音边界的 PCM 分段；结束后将二者合并为最终记录，经 JSONL、SSE 和内置控制台展示。

## Architecture Decisions

- 使用 OnlineRecognizer 输出低延迟 interim；VAD 分段结束后用本地 SenseVoice 对完整 PCM 精校 final，解决小型流式模型错词和无标点问题。
- 使用 sherpa VAD 的 speech padding、最短语音、静音结束和最长语音参数，不自行训练或切块模拟 VAD。
- 音频回调只复制 PCM 到有界 channel；识别和 VAD 在独立 goroutine 执行，保证录音不被 Web 或磁盘阻塞。
- 以 VAD 分段作为 final 边界，对该段完整 PCM 运行 OfflineRecognizer，保存精校文本后 Reset 流。
- 默认绑定 `127.0.0.1`，不开放 CORS，不增加登录流程；静态资源使用 `embed`。
- 数据写入 JSONL 和按日期分目录的 WAV；内存只保留有限最近历史，避免 7×24 小时无限增长。

## Task List

### Phase 1: Foundation and highest-risk integration

1. 建立 Go 项目、模型下载脚本和最小 sherpa 实时识别程序，确认 macOS 动态库、模型和 CGO 可用。
2. 实现持久化类型与 WAV/JSONL 写入，确认进程重启后能读取最近历史。

Checkpoint: 真实模型完成一次识别；`go build ./...` 通过。

### Phase 2: Voice vertical slice

3. 实现 malgo 采集、有界 PCM channel、VAD 分段、OnlineRecognizer interim/final 和幂等启停。
4. 实现本地状态/历史/音频 API、幂等 capture 控制和 SSE。

Checkpoint: 用真实 PCM 验证 VAD 只保留语音分段，curl 收到 interim/final 事件并能下载 WAV。

### Phase 3: Built-in console

5. 实现内嵌 HTML/CSS/JS 控制台：状态、启停、实时文本、历史、回放、下载、错误与重连状态。
6. 完成响应式、键盘、浏览器、端到端和关闭流程验证，补齐运行文档。

Checkpoint: `go vet ./...`、`go test ./...`、`go build` 全通过；真实浏览器无控制台错误；最终运行路径验证完成。

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| sherpa Go 动态库在 macOS 加载失败 | 阻塞全部功能 | 第一任务先编译并加载真实模型；使用官方 v2 包与平台库布局 |
| 音频回调被推理阻塞导致丢帧 | 录音不完整 | 回调仅复制到有界 channel，独立 goroutine 推理；记录队列溢出状态 |
| VAD 与 ASR 时间边界不一致 | final 文本串段 | 只由 VAD 决定 final，final 后立即 Reset ASR stream |
| 慢 SSE 客户端积累内存 | 常驻服务内存增长 | 每客户端有界队列，慢客户端断开；历史列表分页 |
| 本地 HTTP 被恶意网站利用 | 录音或文本泄露 | 回环绑定、Host/Origin 校验、无 CORS、CSP、固定 ID 音频路由 |

## Open Questions

没有阻塞实现的问题。默认全本地、单机单用户、永久保留历史、不区分说话人。
