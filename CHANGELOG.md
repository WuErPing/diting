# 更新日志

<p align="center">
  <a href="#中文">中文</a> | <a href="#english">English</a>
</p>

## 中文

本文件基于 Git 提交历史，按用户可感知的功能主题汇聚变更；同一主题的多个提交合并记录，不逐条罗列 commit。当前仓库尚无版本标签，因此现有内容统一归入“未发布”。

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。

### [未发布]

#### 新增

- **本机语音记录**：完成麦克风采集、Silero VAD 人声检测、流式识别、按天录音归档、JSONL 持久化，以及基于 SSE 的实时 Web 控制台。
- **双路径语音识别**：语句结束后使用本地 SenseVoice 精校；支持可配置的 MiMo ASR，云端识别失败时自动回退本地，不改变默认不上传音频的行为。
- **说话人管理**：加入 CAM++ 声纹标签、跨重启持久化、人名别名、多标签合并与取消合并，以及全量历史重转写。
- **声纹评估工具**：支持从标注录音导出测试集，计算 EER、准确率与分数分布，推荐匹配阈值并横向比较不同声纹模型。
- **议题文稿**：将同一说话人的连续发言组织为语义段落，生成层级主题、简明总结和独立段落摘要；支持语义修正、指定段落重识别与 Markdown 导出。
- **按天工作流**：提供归档日历、日期筛选、整天删除与快照恢复；可用当前模型按天重新分类主题，并可生成、保存、重新打开和下载每日会议纪要。
- **录音管理**：独立声音记录页支持分页、回放、下载、说话人标注、单条删除、多选和批量删除，删除后自动重建相关文稿。
- **统一配置**：配置页集中管理议题模型、本地或云端 ASR、语义修正和场景术语表；支持 OpenAI 兼容端点及本机 LM Studio。
- **中英文界面**：控制台、声音记录页和配置页支持中英文切换，并记住浏览器中的语言选择。
- **开发与运维接口**：补充 OpenAPI 3.0 契约、版本化 API 前缀、健康与就绪探针、Prometheus 指标、结构化日志和 token 用量记录及导出。

#### 改进

- **识别质量**：流式识别采用 4 路 beam search；对中文短句使用双模型一致性校验和编辑距离约束，在减少漏字的同时保留精校标点，拒绝不确定替换。
- **声纹稳定性**：加入多窗口一致性检查、短片段抑制、低质量与歧义更新保护、不确定区间复用及质心权重上限，降低错误匹配和声纹污染。
- **录音体积**：新录音默认编码为 AAC-LC 32 kbps 单声道 M4A，编码不可用时回退 WAV；历史 WAV 保持兼容，不强制迁移。
- **议题组织**：按 15 分钟静音划分大时间段，每个时间段只保留一个大主题，具体差异由小主题承载；主题索引支持层级折叠，默认展开最新分组并保留手动展开状态。
- **段落处理**：新语句到达后异步重判当前议题，支持历史回填和失败重试；只合并同一说话人的连续发言，说话人变化时强制拆分；语义修正增加编辑预算，避免模型过度改写原文。
- **实时界面**：以人声状态动画和文稿内联临时文本取代独立实时转录面板；统一使用小篆风格品牌标志和 favicon。
- **接口与日志**：完善归档、文稿、段落、设置和说话人接口的查询能力，应用及 worker 日志统一为英文。

#### 修复

- 修复流式结果丢失句首音节时误覆盖精校文本的问题，并保留更可靠的离线标点。
- 修复声纹评估读取压缩录音时的格式规范化问题，确保 M4A 与 WAV 走一致的解码路径。
- 修复议题分组重复覆盖和模型返回空选择时的处理问题，无法修复的边界会安全降级并允许后续重试。
- 完善录音删除、按天归档和派生段落重建之间的数据同步，避免已删除内容继续出现在合并文稿中。

## English

This file aggregates user-visible changes from the Git commit history by feature area; multiple commits on the same topic are merged into one entry instead of being listed commit by commit. The repository has no version tags yet, so all current entries fall under "Unreleased".

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

### [Unreleased]

#### Added

- **Local voice recording**: Microphone capture, Silero VAD speech detection, streaming recognition, daily audio archives, JSONL persistence, and a real-time web console over SSE.
- **Dual-path speech recognition**: Local SenseVoice refinement after each utterance; configurable MiMo ASR with automatic fallback to local recognition when the cloud service fails, leaving the default no-upload behavior unchanged.
- **Speaker management**: CAM++ voiceprint labels, persistence across restarts, name aliases, merging and unmerging labels, and full-history retranscription.
- **Voiceprint evaluation tools**: Export test sets from labeled recordings, compute EER, accuracy, and score distributions, recommend matching thresholds, and compare voiceprint models side by side.
- **Topic transcripts**: Organize continuous speech from each speaker into semantic paragraphs with hierarchical topics, concise summaries, and standalone paragraph digests; semantic correction, per-paragraph re-recognition, and Markdown export.
- **Daily workflow**: Archive calendar, date filtering, whole-day deletion with snapshot restore, daily topic reclassification with the current model, plus generating, saving, reopening, and downloading daily meeting minutes.
- **Recording management**: A dedicated records page with pagination, playback, download, speaker labels, single deletion, multi-select, and batch deletion; related transcripts rebuild automatically after deletion.
- **Unified settings**: One settings page for the topic model, local or cloud ASR, semantic correction, and the domain lexicon; supports OpenAI-compatible endpoints and local LM Studio.
- **Chinese/English interface**: The console, records page, and settings page support language switching and remember the choice in the browser.
- **Development and operations interfaces**: OpenAPI 3.0 contract, versioned API prefix, health and readiness probes, Prometheus metrics, structured logging, and token usage recording and export.

#### Changed

- **Recognition quality**: Streaming recognition uses 4-beam search; short Chinese utterances go through dual-model consistency checks with edit-distance constraints, reducing dropped characters while preserving refined punctuation and rejecting uncertain replacements.
- **Voiceprint stability**: Multi-window consistency checks, short-clip suppression, low-quality and ambiguity update guards, uncertain-range reuse, and centroid weight caps reduce mismatches and voiceprint contamination.
- **Recording size**: New recordings default to AAC-LC 32 kbps mono M4A with WAV fallback when encoding is unavailable; historical WAV files remain compatible without forced migration.
- **Topic organization**: Major time periods split after 15 minutes of silence, with one major topic per period and specific differences retained in minor topics; the topic index supports hierarchical collapsing, expands the latest group by default, and preserves manual expansion state.
- **Paragraph processing**: The current topic is re-evaluated asynchronously as new utterances arrive, with history backfill and failure retries; only consecutive speech from the same speaker is merged, with forced splits at speaker changes; semantic correction gained an edit budget to prevent the model from over-rewriting the original text.
- **Real-time interface**: A voice-state animation and inline interim text in the transcript replace the separate live transcription panel; a unified seal-script brand logo and favicon.
- **APIs and logging**: Improved query capabilities for archive, transcript, paragraph, settings, and speaker endpoints; application and worker logs standardized in English.

#### Fixed

- Fixed streaming results wrongly overwriting refined text when the leading syllable was lost, while keeping the more reliable offline punctuation.
- Fixed format normalization when voiceprint evaluation reads compressed recordings, so M4A and WAV share a consistent decoding path.
- Fixed repeated group coverage in topic grouping and handling of empty model choices; unrepairable boundaries degrade safely and allow later retries.
- Improved data synchronization between recording deletion, daily archiving, and derived paragraph rebuilds so deleted content no longer appears in merged transcripts.
