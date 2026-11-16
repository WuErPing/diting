"use strict";

const elements = {
  console: document.querySelector(".console"),
  connection: document.querySelector("#connection"),
  connectionText: document.querySelector("#connectionText"),
  listenerPanel: document.querySelector(".listener-panel"),
  stateLabel: document.querySelector("#stateLabel"),
  listenerTitle: document.querySelector("#listenerTitle"),
  stateHint: document.querySelector("#stateHint"),
  toggleButton: document.querySelector("#toggleButton"),
  errorMessage: document.querySelector("#errorMessage"),
  uptime: document.querySelector("#uptime"),
  segmentCount: document.querySelector("#segmentCount"),
  droppedChunks: document.querySelector("#droppedChunks"),
  technicalNote: document.querySelector("#technicalNote"),
  privacyTitle: document.querySelector("#privacyTitle"),
  privacyText: document.querySelector("#privacyText"),
  archiveFiles: document.querySelector("#archiveFiles"),
  archiveSize: document.querySelector("#archiveSize"),
  archiveChars: document.querySelector("#archiveChars"),
  interimText: document.querySelector("#interimText"),
  liveTime: document.querySelector("#liveTime"),
  previewPanel: document.querySelector("#previewPanel"),
  previewNote: document.querySelector("#previewNote"),
  splitParagraphsButton: document.querySelector("#splitParagraphsButton"),
  markdownScroll: document.querySelector("#markdownScroll"),
  markdownPreview: document.querySelector("#markdownPreview"),
  markdownLive: document.querySelector("#markdownLive"),
  markdownLiveText: document.querySelector("#markdownLiveText"),
  showPreviewButton: document.querySelector("#showPreviewButton"),
  hidePreviewButton: document.querySelector("#hidePreviewButton"),
  topicList: document.querySelector("#topicList"),
  topicsEmpty: document.querySelector("#topicsEmpty"),
  calendarPrev: document.querySelector("#calendarPrev"),
  calendarNext: document.querySelector("#calendarNext"),
  calendarMonth: document.querySelector("#calendarMonth"),
  calendarGrid: document.querySelector("#calendarGrid"),
  calendarDetail: document.querySelector("#calendarDetail"),
  calendarDetailDate: document.querySelector("#calendarDetailDate"),
  calendarDetailSegments: document.querySelector("#calendarDetailSegments"),
  calendarDetailChars: document.querySelector("#calendarDetailChars"),
  calendarDetailFiles: document.querySelector("#calendarDetailFiles"),
  calendarDetailSize: document.querySelector("#calendarDetailSize"),
  calendarDeleteButton: document.querySelector("#calendarDeleteButton"),
  deleteDialog: document.querySelector("#deleteDialog"),
  deleteForm: document.querySelector("#deleteForm"),
  deleteDialogText: document.querySelector("#deleteDialogText"),
  deleteDialogError: document.querySelector("#deleteDialogError"),
  deleteCancel: document.querySelector("#deleteCancel"),
  deleteConfirm: document.querySelector("#deleteConfirm"),
  speakerDialog: document.querySelector("#speakerDialog"),
  speakerForm: document.querySelector("#speakerForm"),
  speakerDialogLabel: document.querySelector("#speakerDialogLabel"),
  speakerAliasInput: document.querySelector("#speakerAliasInput"),
  speakerMergeTarget: document.querySelector("#speakerMergeTarget"),
  speakerMergeHint: document.querySelector("#speakerMergeHint"),
  speakerDialogError: document.querySelector("#speakerDialogError"),
  speakerCancel: document.querySelector("#speakerCancel"),
  speakerSave: document.querySelector("#speakerSave"),
  lexiconButton: document.querySelector("#lexiconButton"),
  lexiconDialog: document.querySelector("#lexiconDialog"),
  lexiconForm: document.querySelector("#lexiconForm"),
  lexiconInput: document.querySelector("#lexiconInput"),
  lexiconError: document.querySelector("#lexiconError"),
  lexiconCancel: document.querySelector("#lexiconCancel"),
  lexiconSave: document.querySelector("#lexiconSave"),
  settingsButton: document.querySelector("#settingsButton"),
  settingsDialog: document.querySelector("#settingsDialog"),
  settingsForm: document.querySelector("#settingsForm"),
  settingsModel: document.querySelector("#settingsModel"),
  settingsASR: document.querySelector("#settingsASR"),
  settingsASRHint: document.querySelector("#settingsASRHint"),
  settingsCorrect: document.querySelector("#settingsCorrect"),
  settingsError: document.querySelector("#settingsError"),
  settingsCancel: document.querySelector("#settingsCancel"),
  settingsSave: document.querySelector("#settingsSave"),
};

const calendarDays = new Map();
const speakerViews = new Map();
let currentStatus = { state: "STOPPED", startedAt: null };
let requestPending = false;
let eventSource;
let calendarCursor = startOfMonth(new Date());
let selectedDay = "";
let activeSpeakerLabel = "";

const previewStorageKey = "diting.previewVisible";
const previewLimit = 500;
let previewVisible = readPreviewPreference();
let previewLoaded = false;
let previewTimer;
let previewRequest = 0;
let correctingPreview = false;
let splittingPreview = false;
let retranscribingPreview = false;
let summarizingPreview = false;
let paragraphCorrectionAvailable = false;

function readPreviewPreference() {
  try {
    return localStorage.getItem(previewStorageKey) !== "false";
  } catch {
    return true;
  }
}

function savePreviewPreference(visible) {
  try {
    localStorage.setItem(previewStorageKey, String(visible));
  } catch {
    // Storage can be unavailable; the toggle still works for this session.
  }
}

async function api(path, options = {}) {
  const response = await fetch(path, options);
  const body = await response.json();
  if (!response.ok) {
    throw new Error(body.error?.message || "本地服务请求失败");
  }
  return body;
}

function showError(message) {
  elements.errorMessage.textContent = message;
  elements.errorMessage.hidden = !message;
}

function formatUptime(startedAt) {
  if (!startedAt) return "00:00:00";
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(startedAt).getTime()) / 1000));
  const hours = String(Math.floor(seconds / 3600)).padStart(2, "0");
  const minutes = String(Math.floor((seconds % 3600) / 60)).padStart(2, "0");
  const remaining = String(seconds % 60).padStart(2, "0");
  return `${hours}:${minutes}:${remaining}`;
}

function updateStatus(status) {
  currentStatus = status;
  const running = status.state === "RUNNING";
  const stopping = status.state === "STOPPING";
  elements.listenerPanel.classList.toggle("is-running", running);
  elements.toggleButton.classList.toggle("is-running", running);
  elements.toggleButton.disabled = stopping || requestPending;
  elements.toggleButton.textContent = running ? "停止聆听" : stopping ? "正在停止…" : "开始聆听";
  elements.stateLabel.textContent = running ? "ON AIR" : stopping ? "STOPPING" : "STANDBY";
  elements.listenerTitle.textContent = running ? "正在聆听" : stopping ? "正在收尾" : "静候人声";
  elements.stateHint.textContent = running
    ? "保持麦克风开启。检测到说话时会显示文字并保存分段。"
    : stopping
      ? "正在保存最后一句话，完成后停止麦克风。"
      : "启动后，只有检测到说话时才保存录音。";
  elements.segmentCount.textContent = status.segmentCount || 0;
  elements.droppedChunks.textContent = status.droppedChunks || 0;
  if (!running && stopping === false) elements.uptime.textContent = "00:00:00";
  showError(status.lastError || "");
  if (!running && !stopping) {
    elements.interimText.textContent = "麦克风已停止。开始聆听后即可实时转写。";
    elements.liveTime.textContent = "";
    hideLivePreview();
  } else if (running && !elements.liveTime.textContent) {
    elements.interimText.textContent = "麦克风已开启，等待人声…";
  }
}

function formatTime(value) {
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).format(new Date(value));
}

const numberFormat = new Intl.NumberFormat("zh-CN");

function formatBytes(bytes) {
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${unit === 0 ? value : value.toFixed(1)} ${units[unit]}`;
}

function updateStats(stats) {
  elements.archiveFiles.textContent = numberFormat.format(stats.audioFiles || 0);
  elements.archiveSize.textContent = formatBytes(stats.audioBytes);
  elements.archiveChars.textContent = numberFormat.format(stats.transcriptChars || 0);
}

async function loadStats() {
  try {
    updateStats(await api("/api/stats"));
  } catch {
    // Archive totals are supplementary; keep the last known values.
  }
}

const monthFormat = new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "long" });
const dayFormat = new Intl.DateTimeFormat("zh-CN", {
  year: "numeric",
  month: "long",
  day: "numeric",
  weekday: "short",
});
const weekdays = ["一", "二", "三", "四", "五", "六", "日"];

function startOfMonth(date) {
  return new Date(date.getFullYear(), date.getMonth(), 1);
}

function dateKey(date) {
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${date.getFullYear()}-${month}-${day}`;
}

function parseDayKey(key) {
  const [year, month, day] = key.split("-").map(Number);
  return new Date(year, month - 1, day);
}

function formatDayLabel(key) {
  return dayFormat.format(parseDayKey(key));
}

// Log scale keeps a few large days from flattening everything below them.
function calendarLevel(bytes, maximum) {
  if (!bytes || !maximum) return 0;
  const level = Math.ceil((Math.log1p(bytes) / Math.log1p(maximum)) * 5);
  return Math.min(5, Math.max(1, level));
}

async function loadDays() {
  try {
    const { days } = await api("/api/days");
    calendarDays.clear();
    for (const day of days) calendarDays.set(day.date, day);
    if (selectedDay && !calendarDays.has(selectedDay)) selectedDay = "";
    renderCalendar();
    renderDayDetail();
  } catch {
    // The calendar is supplementary; keep the last known view.
  }
}

function renderCalendar() {
  const year = calendarCursor.getFullYear();
  const month = calendarCursor.getMonth();
  elements.calendarMonth.textContent = monthFormat.format(calendarCursor);

  let maxBytes = 0;
  for (const day of calendarDays.values()) {
    maxBytes = Math.max(maxBytes, day.audioBytes || 0);
  }

  const first = new Date(year, month, 1);
  const offset = (first.getDay() + 6) % 7;
  const start = new Date(year, month, 1 - offset);
  const today = dateKey(new Date());

  const fragment = document.createDocumentFragment();
  for (const name of weekdays) {
    const label = document.createElement("span");
    label.className = "calendar-weekday";
    label.textContent = name;
    fragment.append(label);
  }
  for (let i = 0; i < 42; i += 1) {
    const date = new Date(start.getFullYear(), start.getMonth(), start.getDate() + i);
    const key = dateKey(date);
    const day = calendarDays.get(key);
    const cell = document.createElement("button");
    cell.type = "button";
    cell.className = "calendar-cell";
    cell.dataset.date = key;
    cell.textContent = String(date.getDate());
    if (date.getMonth() !== month) cell.classList.add("is-outside");
    if (key === today) cell.classList.add("is-today");
    if (key === selectedDay) cell.classList.add("is-selected");
    if (day) {
      cell.classList.add("has-data", `level-${calendarLevel(day.audioBytes, maxBytes)}`);
      const parts = [key];
      if (day.segments) parts.push(`${numberFormat.format(day.segments)} 段`);
      if (day.audioFiles) parts.push(`${numberFormat.format(day.audioFiles)} 个录音`);
      parts.push(formatBytes(day.audioBytes));
      cell.title = parts.join(" · ");
      cell.setAttribute("aria-label", parts.join("，"));
    } else {
      cell.disabled = true;
      cell.classList.add("is-empty");
    }
    fragment.append(cell);
  }
  elements.calendarGrid.replaceChildren(fragment);
}

function renderDayDetail() {
  const day = selectedDay ? calendarDays.get(selectedDay) : undefined;
  elements.calendarDetail.hidden = !day;
  for (const cell of elements.calendarGrid.querySelectorAll(".calendar-cell")) {
    cell.classList.toggle("is-selected", cell.dataset.date === selectedDay);
  }
  if (!day) return;
  elements.calendarDetailDate.textContent = formatDayLabel(day.date);
  elements.calendarDetailSegments.textContent = numberFormat.format(day.segments || 0);
  elements.calendarDetailChars.textContent = numberFormat.format(day.chars || 0);
  elements.calendarDetailFiles.textContent = numberFormat.format(day.audioFiles || 0);
  elements.calendarDetailSize.textContent = formatBytes(day.audioBytes);
  elements.calendarDeleteButton.disabled = !day.segments && !day.audioFiles;
}

function openDeleteDialog() {
  const day = calendarDays.get(selectedDay);
  if (!day) return;
  const summary = `${day.segments || 0} 条记录、${day.audioFiles || 0} 个录音（${formatBytes(day.audioBytes)}）`;
  elements.deleteDialogText.textContent = `将删除 ${formatDayLabel(day.date)} 的 ${summary}。`;
  elements.deleteDialogError.hidden = true;
  elements.deleteDialog.showModal();
}

async function submitDelete(event) {
  event.preventDefault();
  const date = selectedDay;
  if (!date) return;
  elements.deleteConfirm.disabled = true;
  try {
    await api(`/api/days/${encodeURIComponent(date)}`, { method: "DELETE" });
    elements.deleteDialog.close();
    selectedDay = "";
    await Promise.all([loadDays(), loadStats()]);
    schedulePreviewRefresh();
  } catch (error) {
    elements.deleteDialogError.textContent = error.message;
    elements.deleteDialogError.hidden = false;
  } finally {
    elements.deleteConfirm.disabled = false;
  }
}

async function loadSpeakers() {
  try {
    const { data } = await api("/api/speakers");
    speakerViews.clear();
    for (const view of data) speakerViews.set(view.label, view);
  } catch {
    // Speaker names only affect labels; fall back to stable tags.
  }
}

function speakerDisplayName(label) {
  const view = speakerViews.get(label);
  return view ? (view.name || label) : label;
}

function isMerged(label) {
  const view = speakerViews.get(label);
  return view && !!view.mergedInto;
}

function openSpeakerDialog(label) {
  if (!label) return;
  activeSpeakerLabel = label;
  const view = speakerViews.get(label);
  const name = view ? (view.name || label) : label;
  elements.speakerDialogLabel.textContent = label;
  elements.speakerAliasInput.value = name === label ? "" : name;
  elements.speakerAliasInput.disabled = isMerged(label);

  const select = elements.speakerMergeTarget;
  select.replaceChildren();
  const noneOption = document.createElement("option");
  noneOption.value = "";
  noneOption.textContent = "不合并";
  select.append(noneOption);
  for (const [otherLabel, otherView] of speakerViews) {
    if (otherLabel === label) continue;
    if (otherView.mergedInto) continue;
    const option = document.createElement("option");
    option.value = otherLabel;
    const displayName = otherView.name || otherLabel;
    option.textContent = displayName === otherLabel ? otherLabel : `${displayName}（${otherLabel}）`;
    select.append(option);
  }
  select.value = view?.mergedInto || "";
  select.disabled = false;

  elements.speakerMergeHint.textContent = view?.mergedInto
    ? `当前已合并到 ${speakerDisplayName(view.mergedInto)}。选择"不合并"可取消。`
    : "将多个声纹标签合并为同一个人，合并后内容会重新分段。留空可取消合并。";

  elements.speakerDialogError.hidden = true;
  elements.speakerDialog.showModal();
  if (isMerged(label)) {
    select.focus();
  } else {
    elements.speakerAliasInput.focus();
    elements.speakerAliasInput.select();
  }
}

async function submitSpeaker(event) {
  event.preventDefault();
  const label = activeSpeakerLabel;
  if (!label) return;
  elements.speakerSave.disabled = true;
  try {
    const mergeTarget = elements.speakerMergeTarget.value;
    const alias = elements.speakerAliasInput.value.trim();

    if (!isMerged(label) && alias !== (speakerDisplayName(label) === label ? "" : speakerDisplayName(label))) {
      await api(`/api/speakers/${encodeURIComponent(label)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ alias }),
      });
    }

    const currentMerge = speakerViews.get(label)?.mergedInto || "";
    if (mergeTarget !== currentMerge) {
      await api(`/api/speakers/${encodeURIComponent(label)}/merge`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ target: mergeTarget }),
      });
    }

    await loadSpeakers();
    elements.speakerDialog.close();
    schedulePreviewRefresh();
  } catch (error) {
    elements.speakerDialogError.textContent = error.message;
    elements.speakerDialogError.hidden = false;
  } finally {
    elements.speakerSave.disabled = false;
  }
}

async function openLexiconDialog() {
  elements.lexiconError.hidden = true;
  try {
    const { rules } = await api("/api/lexicon");
    elements.lexiconInput.value = rules.map((rule) => `${rule.from}=${rule.to}`).join("\n");
    elements.lexiconDialog.showModal();
    elements.lexiconInput.focus();
  } catch (error) {
    showError(error.message);
  }
}

function parseLexicon(text) {
  const rules = [];
  const lines = text.split(/\r?\n/);
  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i].trim();
    if (!line || line.startsWith("#")) continue;
    const index = line.indexOf("=");
    if (index < 0) throw new Error(`第 ${i + 1} 行缺少“=”`);
    const from = line.slice(0, index).trim();
    if (!from) throw new Error(`第 ${i + 1} 行的识别词为空`);
    rules.push({ from, to: line.slice(index + 1).trim() });
  }
  return rules;
}

async function submitLexicon(event) {
  event.preventDefault();
  let rules;
  try {
    rules = parseLexicon(elements.lexiconInput.value);
  } catch (error) {
    elements.lexiconError.textContent = error.message;
    elements.lexiconError.hidden = false;
    return;
  }
  elements.lexiconSave.disabled = true;
  try {
    await api("/api/lexicon", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ rules }),
    });
    elements.lexiconDialog.close();
    schedulePreviewRefresh();
  } catch (error) {
    elements.lexiconError.textContent = error.message;
    elements.lexiconError.hidden = false;
  } finally {
    elements.lexiconSave.disabled = false;
  }
}

function updateASRHint(provider) {
  elements.settingsASRHint.textContent = provider === "mimo"
    ? "最终 WAV 分段将上传到小米 MiMo；失败时自动使用本地识别。实时临时文本仍只在本地生成。"
    : "VAD 分段和实时临时文本始终在本地处理；这里选择语句结束后的最终识别方式。";
}

function updateASRPresentation(provider) {
  const cloud = provider === "mimo";
  elements.technicalNote.textContent = cloud ? "16 kHz  ·  MONO  ·  MIMO API" : "16 kHz  ·  MONO  ·  ON DEVICE";
  elements.privacyTitle.textContent = cloud ? "云端最终识别" : "声音不出本机";
  elements.privacyText.textContent = cloud
    ? "实时临时文本在本地生成；语句结束后的 WAV 分段会上传到小米 MiMo ASR。议题分析也会发送转写文本。"
    : "语音识别和音频保存都在本机，不上传音频。议题分析会把转写文本发送到所配置的模型 API（默认小米 MiMo）。";
  updateASRHint(provider);
}

async function loadSettingsButton() {
  const previous = paragraphCorrectionAvailable;
  try {
    const settings = await api("/api/settings");
    updateASRPresentation(settings.asr);
    elements.settingsButton.hidden = !settings.available;
    elements.splitParagraphsButton.hidden = !settings.available;
    paragraphCorrectionAvailable = Boolean(settings.available);
  } catch {
    elements.settingsButton.hidden = true;
    elements.splitParagraphsButton.hidden = true;
    paragraphCorrectionAvailable = false;
  }
  if (previewVisible && previous !== paragraphCorrectionAvailable) {
    await loadMarkdown();
  }
}

function fillModelOptions(models, current) {
  const list = models && models.length ? models.slice() : [];
  if (current && !list.includes(current)) list.unshift(current);
  elements.settingsModel.textContent = "";
  const source = list.length ? list : [current || ""];
  for (const model of source) {
    const option = document.createElement("option");
    option.value = model;
    option.textContent = model || "（无可用模型）";
    if (model === current) option.selected = true;
    elements.settingsModel.append(option);
  }
}

async function openSettingsDialog() {
  elements.settingsError.hidden = true;
  let settings;
  try {
    settings = await api("/api/settings");
  } catch (error) {
    showError(error.message);
    return;
  }
  if (!settings.available) {
    showError("段落合并未启用，无法修改模型设置");
    return;
  }
  let models = [];
  try {
    const available = await api("/api/models");
    models = available.models || [];
  } catch {
    // 模型服务可能暂不可用；仍允许保留当前模型并切换修正开关。
  }
  fillModelOptions(models, settings.model);
  elements.settingsASR.value = settings.asr || "local";
  updateASRPresentation(elements.settingsASR.value);
  elements.settingsCorrect.checked = Boolean(settings.correct);
  elements.settingsDialog.showModal();
}

async function submitSettings(event) {
  event.preventDefault();
  const model = elements.settingsModel.value.trim();
  if (!model) {
    elements.settingsError.textContent = "请选择一个模型";
    elements.settingsError.hidden = false;
    return;
  }
  elements.settingsSave.disabled = true;
  try {
    const settings = await api("/api/settings", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ model, correct: elements.settingsCorrect.checked, asr: elements.settingsASR.value }),
    });
    updateASRPresentation(settings.asr);
    elements.settingsDialog.close();
  } catch (error) {
    elements.settingsError.textContent = error.message;
    elements.settingsError.hidden = false;
  } finally {
    elements.settingsSave.disabled = false;
  }
}

function escapeHTML(value) {
  return value.replace(/[&<>"']/g, (character) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  })[character]);
}

function renderInline(source) {
  const escaped = [];
  const protectedSource = source.replace(/\\([\\`*_[\]<>])/g, (_, character) => {
    escaped.push(character);
    return `\u0000${escaped.length - 1}\u0000`;
  });
  let html = escapeHTML(protectedSource);
  html = html.replace(/`([^`]+)`/g, "<code>$1</code>");
  html = html.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  html = html.replace(/\u0000(\d+)\u0000/g, (_, index) => escapeHTML(escaped[Number(index)]));
  return html;
}

function renderMarkdown(source) {
  const lines = source.replace(/\r\n/g, "\n").split("\n");
  let html = "";
  let inList = false;
  let inQuote = false;
  let paragraph = [];

  const flushParagraph = () => {
    if (!paragraph.length) return;
    html += `<p>${paragraph.map(renderInline).join("<br>")}</p>`;
    paragraph = [];
  };
  const closeList = () => {
    if (inList) {
      html += "</ul>";
      inList = false;
    }
  };
  const closeQuote = () => {
    if (inQuote) {
      html += "</blockquote>";
      inQuote = false;
    }
  };

  for (const raw of lines) {
    const line = raw.trimEnd();
    if (!line.trim()) {
      flushParagraph();
      closeList();
      closeQuote();
      continue;
    }
    if (line.startsWith("### ")) {
      flushParagraph(); closeList(); closeQuote();
      html += `<h3>${renderInline(line.slice(4))}</h3>`;
      continue;
    }
    if (line.startsWith("## ")) {
      flushParagraph(); closeList(); closeQuote();
      html += `<h2>${renderInline(line.slice(3))}</h2>`;
      continue;
    }
    if (line.startsWith("# ")) {
      flushParagraph(); closeList(); closeQuote();
      html += `<h1>${renderInline(line.slice(2))}</h1>`;
      continue;
    }
    if (line.startsWith("> ")) {
      flushParagraph(); closeList();
      if (!inQuote) {
        html += "<blockquote>";
        inQuote = true;
      }
      html += `<p>${renderInline(line.slice(2))}</p>`;
      continue;
    }
    if (line.startsWith("- ")) {
      flushParagraph(); closeQuote();
      if (!inList) {
        html += "<ul>";
        inList = true;
      }
      html += `<li>${renderInline(line.slice(2))}</li>`;
      continue;
    }
    if (/^---+$/.test(line.trim())) {
      flushParagraph(); closeList(); closeQuote();
      html += "<hr>";
      continue;
    }
    closeList();
    closeQuote();
    paragraph.push(line);
  }

  flushParagraph();
  closeList();
  closeQuote();
  return html;
}

function previewQuery() {
  return selectedDay ? `date=${encodeURIComponent(selectedDay)}` : `limit=${previewLimit}`;
}

function applyPreviewVisibility(scrollToStart = false) {
  elements.console.classList.toggle("is-preview-hidden", !previewVisible);
  elements.previewPanel.hidden = !previewVisible;
  elements.showPreviewButton.hidden = previewVisible;
  return loadMarkdown(scrollToStart);
}

async function loadMarkdown(scrollToStart = false) {
  const request = ++previewRequest;
  const query = previewQuery();
  if (!previewVisible) {
    try {
      const paragraphData = await api(`/api/paragraphs?${query}`);
      if (request !== previewRequest) return false;
      renderTopics(paragraphData.data || []);
      return true;
    } catch (error) {
      if (request !== previewRequest) return false;
      elements.topicsEmpty.hidden = false;
      elements.topicsEmpty.textContent = `主题加载失败：${error.message}`;
      return false;
    }
  }
  const scroll = elements.markdownScroll;
  const nearBottom = scroll.scrollHeight - scroll.scrollTop - scroll.clientHeight < 120;
  const previousTop = scroll.scrollTop;
  try {
    const [response, paragraphData] = await Promise.all([
      fetch(`/api/markdown?${query}`),
      api(`/api/paragraphs?${query}`),
    ]);
    if (!response.ok) throw new Error("生成合并文稿失败");
    const source = await response.text();
    if (request !== previewRequest) return false;
    renderTopics(paragraphData.data || []);
    // renderMarkdown escapes every text fragment before inserting its fixed tags.
    elements.markdownPreview.innerHTML = renderMarkdown(source);
    linkifyPreviewSpeakers();
    addParagraphCorrectionButtons(paragraphData.data || []);
    if (scrollToStart) {
      scroll.scrollTop = 0;
      requestAnimationFrame(() => {
        if (request !== previewRequest) return;
        scroll.scrollTop = 0;
      });
    } else if (!previewLoaded || nearBottom) {
      scroll.scrollTop = scroll.scrollHeight;
      // Re-run after layout settles so the first paint already shows the newest entry.
      requestAnimationFrame(() => {
        if (request !== previewRequest) return;
        scroll.scrollTop = scroll.scrollHeight;
      });
    } else {
      scroll.scrollTop = previousTop;
    }
    previewLoaded = true;
    if (!correctingPreview && !splittingPreview && !retranscribingPreview && !summarizingPreview) {
      elements.previewNote.textContent = selectedDay
        ? `${formatDayLabel(selectedDay)} · 当天开始的段落`
        : `最近 ${previewLimit} 条 · 模型议题`;
      elements.previewNote.classList.remove("is-error");
    }
    return true;
  } catch (error) {
    if (request !== previewRequest) return false;
    elements.markdownPreview.textContent = `预览加载失败：${error.message}`;
    return false;
  }
}

function paragraphTime(value) {
  const date = new Date(value);
  return [date.getHours(), date.getMinutes(), date.getSeconds()]
    .map((part) => String(part).padStart(2, "0"))
    .join(":");
}

async function jumpToParagraph(id) {
  if (!previewVisible) {
    previewVisible = true;
    savePreviewPreference(true);
    await applyPreviewVisibility();
  }
  let target = document.getElementById(`paragraph-${id}`);
  if (!target) {
    await loadMarkdown();
    target = document.getElementById(`paragraph-${id}`);
  }
  if (!target) {
    elements.previewNote.textContent = "未找到对应段落，请刷新后重试";
    elements.previewNote.classList.add("is-error");
    return;
  }
  elements.markdownPreview.querySelectorAll(".is-targeted").forEach((item) => item.classList.remove("is-targeted"));
  target.classList.add("is-targeted");
  target.scrollIntoView({ behavior: "smooth", block: "center" });
  target.focus({ preventScroll: true });
  history.replaceState(null, "", `#paragraph-${id}`);
}

function renderTopics(paragraphs) {
  const topics = paragraphs.filter((paragraph) => paragraph.hasTopic !== false);
  const fragment = document.createDocumentFragment();
  for (const paragraph of topics) {
    const item = document.createElement("li");
    item.className = "topic-item";
    const time = document.createElement("time");
    time.dateTime = paragraph.startedAt;
    const start = paragraphTime(paragraph.startedAt);
    const end = paragraphTime(paragraph.endedAt);
    time.textContent = start === end ? start : `${start}–${end}`;
    const title = document.createElement("h3");
    title.textContent = paragraph.title || "未命名主题";
    const summary = document.createElement("p");
    summary.textContent = paragraph.summary || "暂无简介";
    const link = document.createElement("a");
    link.className = "topic-link";
    link.href = `#paragraph-${paragraph.id}`;
    link.dataset.paragraphId = paragraph.id;
    link.append(time, title, summary);
    item.append(link);
    fragment.append(item);
  }
  elements.topicList.replaceChildren(fragment);
  elements.topicsEmpty.hidden = topics.length > 0;
  elements.topicsEmpty.textContent = "还没有主题。完成录音后，可使用“按主题分段”生成。";
}

function addParagraphCorrectionButtons(paragraphs) {
  if (!paragraphCorrectionAvailable) return;
  const byTimeRange = new Map();
  for (const paragraph of paragraphs) {
    const start = paragraphTime(paragraph.startedAt);
    const end = paragraphTime(paragraph.endedAt);
    const key = start === end ? start : `${start}–${end}`;
    if (!byTimeRange.has(key)) byTimeRange.set(key, []);
    byTimeRange.get(key).push(paragraph);
  }
  for (const item of elements.markdownPreview.querySelectorAll("li")) {
    const strong = item.querySelector("strong");
    const candidates = strong && byTimeRange.get(strong.textContent);
    const paragraph = candidates?.shift();
    if (!paragraph) continue;
    item.id = `paragraph-${paragraph.id}`;
    item.dataset.paragraphId = paragraph.id;
    item.tabIndex = -1;
    const button = document.createElement("button");
    button.type = "button";
    button.className = "md-correct";
    button.dataset.paragraphId = paragraph.id;
    button.textContent = "语义修正";
    button.disabled = correctingPreview || splittingPreview || retranscribingPreview || summarizingPreview;
    button.title = "只修正这一段，原始转写不变";
    button.setAttribute("aria-label", `语义修正 ${strong.textContent} 的段落`);
    const asrButton = document.createElement("button");
    asrButton.type = "button";
    asrButton.className = "md-asr";
    asrButton.dataset.paragraphId = paragraph.id;
    asrButton.textContent = "ASR";
    asrButton.disabled = correctingPreview || splittingPreview || retranscribingPreview || summarizingPreview;
    asrButton.title = "使用当前选择的模型重新识别该段录音，原始逐句转写会更新";
    asrButton.setAttribute("aria-label", `重新识别 ${strong.textContent} 的段落录音`);
    const summaryButton = document.createElement("button");
    summaryButton.type = "button";
    summaryButton.className = "md-summary";
    summaryButton.dataset.paragraphId = paragraph.id;
    summaryButton.textContent = "总结";
    summaryButton.disabled = correctingPreview || splittingPreview || retranscribingPreview || summarizingPreview;
    summaryButton.title = "使用当前段落模型总结该段内容，原文不变";
    summaryButton.setAttribute("aria-label", `总结 ${strong.textContent} 的段落`);
    item.append(" ", button, " ", asrButton, " ", summaryButton);
  }
}

async function splitParagraphs() {
  if (correctingPreview || splittingPreview || retranscribingPreview || summarizingPreview) return;
  splittingPreview = true;
  elements.splitParagraphsButton.disabled = true;
  elements.splitParagraphsButton.textContent = "分段中…";
  for (const item of elements.markdownPreview.querySelectorAll(".md-correct, .md-asr, .md-summary")) {
    item.disabled = true;
  }
  elements.previewNote.textContent = `正在按主题重新划分最近 ${previewLimit} 条记录…`;
  elements.previewNote.classList.remove("is-error");
  try {
    const result = await api("/api/paragraphs/split", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ limit: previewLimit }),
    });
    const refreshed = await loadMarkdown();
    if (!refreshed) {
      elements.previewNote.textContent = "分段已保存，但文稿刷新失败，请重试";
      elements.previewNote.classList.add("is-error");
    } else if (!result.paragraphs) {
      elements.previewNote.textContent = "没有可分段的记录";
    } else if (result.fallback) {
      elements.previewNote.textContent = `已生成 ${result.paragraphs} 段 · ${result.fallback} 组保留整段拼接`;
    } else {
      elements.previewNote.textContent = `已按主题划分为 ${result.paragraphs} 段 · 原始转写不变`;
    }
  } catch (error) {
    elements.previewNote.textContent = `自动分段失败：${error.message}`;
    elements.previewNote.classList.add("is-error");
  } finally {
    splittingPreview = false;
    elements.splitParagraphsButton.disabled = false;
    elements.splitParagraphsButton.textContent = "按主题分段";
    for (const item of elements.markdownPreview.querySelectorAll(".md-correct, .md-asr, .md-summary")) {
      item.disabled = false;
    }
    elements.splitParagraphsButton.focus();
  }
}

async function correctParagraph(id, button) {
  if (correctingPreview || splittingPreview || retranscribingPreview || summarizingPreview) return;
  correctingPreview = true;
  elements.splitParagraphsButton.disabled = true;
  for (const item of elements.markdownPreview.querySelectorAll(".md-correct, .md-asr, .md-summary")) {
    item.disabled = true;
  }
  button.textContent = "修正中…";
  elements.previewNote.textContent = "正在使用当前模型修正所选段落…";
  elements.previewNote.classList.remove("is-error");
  try {
    const result = await api(`/api/paragraphs/${encodeURIComponent(id)}/correct`, { method: "POST" });
    const refreshed = await loadMarkdown();
    if (!refreshed) {
      elements.previewNote.textContent = "修正已保存，但文稿刷新失败，请重试";
      elements.previewNote.classList.add("is-error");
    } else if (!result.changed) {
      elements.previewNote.textContent = "该段无需调整";
    } else {
      elements.previewNote.textContent = "该段已修正 · 原始转写不变";
    }
  } catch (error) {
    elements.previewNote.textContent = `修正失败：${error.message}`;
    elements.previewNote.classList.add("is-error");
  } finally {
    correctingPreview = false;
    elements.splitParagraphsButton.disabled = false;
    let restored;
    for (const item of elements.markdownPreview.querySelectorAll(".md-correct")) {
      item.disabled = false;
      item.textContent = "语义修正";
      if (item.dataset.paragraphId === id) restored = item;
    }
    restored?.focus();
  }
}

async function retranscribeParagraph(id, button) {
  if (correctingPreview || splittingPreview || retranscribingPreview || summarizingPreview) return;
  retranscribingPreview = true;
  elements.splitParagraphsButton.disabled = true;
  for (const item of elements.markdownPreview.querySelectorAll(".md-correct, .md-asr, .md-summary")) {
    item.disabled = true;
  }
  button.textContent = "识别中…";
  elements.previewNote.textContent = "正在使用当前选择的模型重新识别该段录音…";
  elements.previewNote.classList.remove("is-error");
  try {
    const result = await api(`/api/paragraphs/${encodeURIComponent(id)}/asr`, { method: "POST" });
    const refreshed = await loadMarkdown();
    if (!refreshed) {
      elements.previewNote.textContent = "重识别已保存，但文稿刷新失败，请重试";
      elements.previewNote.classList.add("is-error");
    } else if (!result.changed) {
      elements.previewNote.textContent = `已重新识别 ${result.segments} 条录音 · 原始文本无变化`;
    } else {
      elements.previewNote.textContent = `已重新识别 ${result.segments} 条录音 · 更新 ${result.changed} 条文本`;
    }
  } catch (error) {
    elements.previewNote.textContent = `重识别失败：${error.message}`;
    elements.previewNote.classList.add("is-error");
  } finally {
    retranscribingPreview = false;
    elements.splitParagraphsButton.disabled = false;
    let restored;
    for (const item of elements.markdownPreview.querySelectorAll(".md-correct, .md-asr, .md-summary")) {
      item.disabled = false;
      if (item.classList.contains("md-asr")) item.textContent = "ASR";
      if (item.dataset.paragraphId === id && item.classList.contains("md-asr")) restored = item;
    }
    restored?.focus();
  }
}

async function summarizeParagraph(id, button) {
  if (correctingPreview || splittingPreview || retranscribingPreview || summarizingPreview) return;
  summarizingPreview = true;
  elements.splitParagraphsButton.disabled = true;
  for (const item of elements.markdownPreview.querySelectorAll(".md-correct, .md-asr, .md-summary")) {
    item.disabled = true;
  }
  button.textContent = "总结中…";
  elements.previewNote.textContent = "正在使用当前段落模型生成总结…";
  elements.previewNote.classList.remove("is-error");
  try {
    await api(`/api/paragraphs/${encodeURIComponent(id)}/summarize`, { method: "POST" });
    const refreshed = await loadMarkdown();
    if (!refreshed) {
      elements.previewNote.textContent = "总结已保存，但文稿刷新失败，请重试";
      elements.previewNote.classList.add("is-error");
    } else {
      elements.previewNote.textContent = "段落总结已生成 · 原始转写不变";
    }
  } catch (error) {
    elements.previewNote.textContent = `总结失败：${error.message}`;
    elements.previewNote.classList.add("is-error");
  } finally {
    summarizingPreview = false;
    elements.splitParagraphsButton.disabled = false;
    let restored;
    for (const item of elements.markdownPreview.querySelectorAll(".md-correct, .md-asr, .md-summary")) {
      item.disabled = false;
      if (item.classList.contains("md-summary")) item.textContent = "总结";
      if (item.dataset.paragraphId === id && item.classList.contains("md-summary")) restored = item;
    }
    restored?.focus();
  }
}

function linkifyPreviewSpeakers() {
  if (speakerViews.size === 0) return;
  const nameToLabel = new Map();
  for (const [label, view] of speakerViews) {
    const display = view.name || label;
    if (!nameToLabel.has(display)) nameToLabel.set(display, label);
    if (!nameToLabel.has(label)) nameToLabel.set(label, label);
  }
  for (const li of elements.markdownPreview.querySelectorAll("li")) {
    // Structure: <strong>time</strong> · speaker · text
    // Find the text node after <strong> that contains " · speaker · "
    const strong = li.querySelector("strong");
    if (!strong || !strong.nextSibling) continue;
    const textNode = strong.nextSibling;
    if (textNode.nodeType !== Node.TEXT_NODE) continue;
    const parts = textNode.textContent.split(" · ");
    if (parts.length < 3) continue;
    const speakerText = parts[1].trim();
    const label = nameToLabel.get(speakerText);
    if (!label) continue;

    const button = document.createElement("button");
    button.type = "button";
    button.className = "md-speaker";
    button.textContent = speakerText;
    button.dataset.label = label;
    button.title = `标注 ${label}`;

    const before = document.createTextNode(parts[0] + " · ");
    const after = document.createTextNode(" · " + parts.slice(2).join(" · "));
    const parent = textNode.parentNode;
    parent.insertBefore(before, textNode);
    parent.insertBefore(button, textNode);
    parent.insertBefore(after, textNode);
    parent.removeChild(textNode);
  }
}

function updateLivePreview(interim) {
  const text = (interim.text || "").trim();
  if (!text) {
    hideLivePreview();
    return;
  }
  const scroll = elements.markdownScroll;
  const nearBottom = scroll.scrollHeight - scroll.scrollTop - scroll.clientHeight < 120;
  elements.markdownLiveText.textContent = text;
  elements.markdownLive.hidden = false;
  if (previewVisible && nearBottom) {
    scroll.scrollTop = scroll.scrollHeight;
  }
}

function hideLivePreview() {
  elements.markdownLive.hidden = true;
  elements.markdownLiveText.textContent = "";
}

function schedulePreviewRefresh() {
  clearTimeout(previewTimer);
  previewTimer = setTimeout(loadMarkdown, 250);
}

async function loadStatus() {
  try {
    updateStatus(await api("/api/status"));
  } catch (error) {
    showError(error.message);
  }
}

async function toggleCapture() {
  requestPending = true;
  updateStatus(currentStatus);
  showError("");
  try {
    const state = currentStatus.state === "RUNNING" ? "STOPPED" : "RUNNING";
    const status = await api("/api/capture", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ state }),
    });
    requestPending = false;
    updateStatus(status);
  } catch (error) {
    requestPending = false;
    updateStatus(currentStatus);
    showError(error.message);
  }
}

function connectEvents() {
  eventSource?.close();
  eventSource = new EventSource("/api/events");
  eventSource.onopen = () => {
    elements.connection.classList.add("is-connected");
    elements.connectionText.textContent = "实时已连接";
    loadStatus();
    loadDays();
    loadSpeakers();
    loadMarkdown();
  };
  eventSource.onerror = (event) => {
    if (event.data) return;
    elements.connection.classList.remove("is-connected");
    elements.connectionText.textContent = "连接中断，正在重连";
  };
  eventSource.addEventListener("status", (event) => updateStatus(JSON.parse(event.data)));
  eventSource.addEventListener("error", (event) => {
    if (event.data) updateStatus(JSON.parse(event.data));
  });
  eventSource.addEventListener("interim", (event) => {
    const interim = JSON.parse(event.data);
    elements.interimText.textContent = interim.text || "正在完成当前语句…";
    elements.liveTime.textContent = interim.startedAt ? formatTime(interim.startedAt) : "";
    updateLivePreview(interim);
  });
  eventSource.addEventListener("paragraph", () => schedulePreviewRefresh());
  eventSource.addEventListener("archive", () => {
    loadDays();
    loadStats();
    loadSpeakers();
    schedulePreviewRefresh();
  });
  eventSource.addEventListener("segment", () => {
    elements.interimText.textContent = "当前语句已保存，继续聆听…";
    elements.liveTime.textContent = "";
    hideLivePreview();
    loadStatus();
    loadStats();
    schedulePreviewRefresh();
  });
}

async function showCalendarSelection() {
  const scrollToStart = Boolean(selectedDay);
  if (scrollToStart) {
    previewVisible = true;
    savePreviewPreference(true);
  } else {
    previewLoaded = false;
  }
  const loaded = await applyPreviewVisibility(scrollToStart);
  if (scrollToStart && loaded) {
    elements.previewPanel.scrollIntoView({ behavior: "smooth", block: "start" });
  }
}

elements.toggleButton.addEventListener("click", toggleCapture);
elements.calendarPrev.addEventListener("click", () => {
  const hadSelection = Boolean(selectedDay);
  calendarCursor = new Date(calendarCursor.getFullYear(), calendarCursor.getMonth() - 1, 1);
  selectedDay = "";
  renderCalendar();
  renderDayDetail();
  if (hadSelection) showCalendarSelection();
});
elements.calendarNext.addEventListener("click", () => {
  const hadSelection = Boolean(selectedDay);
  calendarCursor = new Date(calendarCursor.getFullYear(), calendarCursor.getMonth() + 1, 1);
  selectedDay = "";
  renderCalendar();
  renderDayDetail();
  if (hadSelection) showCalendarSelection();
});
elements.calendarGrid.addEventListener("click", (event) => {
  const cell = event.target.closest(".calendar-cell");
  if (!cell || cell.disabled) return;
  selectedDay = selectedDay === cell.dataset.date ? "" : cell.dataset.date;
  renderDayDetail();
  showCalendarSelection();
});
elements.calendarDeleteButton.addEventListener("click", openDeleteDialog);
elements.deleteCancel.addEventListener("click", () => elements.deleteDialog.close());
elements.deleteForm.addEventListener("submit", submitDelete);
elements.speakerCancel.addEventListener("click", () => elements.speakerDialog.close());
elements.speakerForm.addEventListener("submit", submitSpeaker);
elements.markdownPreview.addEventListener("click", (event) => {
  const summary = event.target.closest(".md-summary");
  if (summary && summary.dataset.paragraphId) {
    summarizeParagraph(summary.dataset.paragraphId, summary);
    return;
  }
  const asr = event.target.closest(".md-asr");
  if (asr && asr.dataset.paragraphId) {
    retranscribeParagraph(asr.dataset.paragraphId, asr);
    return;
  }
  const correction = event.target.closest(".md-correct");
  if (correction && correction.dataset.paragraphId) {
    correctParagraph(correction.dataset.paragraphId, correction);
    return;
  }
  const speaker = event.target.closest(".md-speaker");
  if (speaker && speaker.dataset.label) openSpeakerDialog(speaker.dataset.label);
});
elements.topicList.addEventListener("click", (event) => {
  const link = event.target.closest(".topic-link");
  if (!link || !link.dataset.paragraphId) return;
  event.preventDefault();
  jumpToParagraph(link.dataset.paragraphId);
});
elements.lexiconButton.addEventListener("click", openLexiconDialog);
elements.lexiconCancel.addEventListener("click", () => elements.lexiconDialog.close());
elements.lexiconForm.addEventListener("submit", submitLexicon);
elements.settingsButton.addEventListener("click", openSettingsDialog);
elements.splitParagraphsButton.addEventListener("click", splitParagraphs);
elements.settingsCancel.addEventListener("click", () => elements.settingsDialog.close());
elements.settingsForm.addEventListener("submit", submitSettings);
elements.settingsASR.addEventListener("change", () => updateASRHint(elements.settingsASR.value));
elements.hidePreviewButton.addEventListener("click", () => {
  previewVisible = false;
  savePreviewPreference(false);
  applyPreviewVisibility();
  elements.showPreviewButton.focus();
});
elements.showPreviewButton.addEventListener("click", () => {
  previewVisible = true;
  savePreviewPreference(true);
  applyPreviewVisibility();
  elements.previewPanel.scrollIntoView({ behavior: "smooth", block: "start" });
});
setInterval(() => {
  if (currentStatus.state === "RUNNING") {
    elements.uptime.textContent = formatUptime(currentStatus.startedAt);
  }
}, 1000);

applyPreviewVisibility();
renderCalendar();
Promise.all([loadStatus(), loadDays(), loadSpeakers(), loadSettingsButton()]).finally(connectEvents);
