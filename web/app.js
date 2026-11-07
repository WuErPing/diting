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
  archiveFiles: document.querySelector("#archiveFiles"),
  archiveSize: document.querySelector("#archiveSize"),
  archiveChars: document.querySelector("#archiveChars"),
  interimText: document.querySelector("#interimText"),
  liveTime: document.querySelector("#liveTime"),
  refreshButton: document.querySelector("#refreshButton"),
  emptyState: document.querySelector("#emptyState"),
  segmentList: document.querySelector("#segmentList"),
  loadMoreButton: document.querySelector("#loadMoreButton"),
  segmentTemplate: document.querySelector("#segmentTemplate"),
  previewPanel: document.querySelector("#previewPanel"),
  previewNote: document.querySelector("#previewNote"),
  markdownScroll: document.querySelector("#markdownScroll"),
  markdownPreview: document.querySelector("#markdownPreview"),
  markdownLive: document.querySelector("#markdownLive"),
  markdownLiveText: document.querySelector("#markdownLiveText"),
  showPreviewButton: document.querySelector("#showPreviewButton"),
  hidePreviewButton: document.querySelector("#hidePreviewButton"),
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
};

const segments = new Map();
const calendarDays = new Map();
const speakerNames = new Map();
let currentStatus = { state: "STOPPED", startedAt: null };
let nextBefore = "";
let requestPending = false;
let eventSource;
let calendarCursor = startOfMonth(new Date());
let selectedDay = "";
let activeSpeakerLabel = "";

const previewStorageKey = "diting.previewVisible";
const previewLimit = 500;
const previewTopGap = 24;
let previewVisible = readPreviewPreference();
let previewLoaded = false;
let previewTimer;
let previewRequest = 0;
let previewResizeFrame;

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

function formatDuration(milliseconds) {
  return `${(milliseconds / 1000).toFixed(1)} 秒`;
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
    segments.clear();
    await Promise.all([loadDays(), loadHistory(), loadStats()]);
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
    speakerNames.clear();
    for (const view of data) speakerNames.set(view.label, view.name || view.label);
    renderSegments();
  } catch {
    // Speaker names only affect labels; fall back to stable tags.
  }
}

function openSpeakerDialog(label) {
  if (!label) return;
  activeSpeakerLabel = label;
  const name = speakerNames.get(label) || "";
  elements.speakerDialogLabel.textContent = label;
  elements.speakerAliasInput.value = name === label ? "" : name;
  elements.speakerDialogError.hidden = true;
  elements.speakerDialog.showModal();
  elements.speakerAliasInput.focus();
  elements.speakerAliasInput.select();
}

async function submitSpeaker(event) {
  event.preventDefault();
  const label = activeSpeakerLabel;
  if (!label) return;
  elements.speakerSave.disabled = true;
  try {
    const view = await api(`/api/speakers/${encodeURIComponent(label)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ alias: elements.speakerAliasInput.value.trim() }),
    });
    const name = view.name || label;
    speakerNames.set(label, name);
    for (const segment of segments.values()) {
      if (segment.speaker === label) segment.speakerName = name;
    }
    elements.speakerDialog.close();
    renderSegments();
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

// Keep the panel inside the viewport: before it sticks, its static top is
// below the masthead, so a full 100vh height would be clipped at page top.
function syncPreviewHeight() {
  const panel = elements.previewPanel;
  if (!previewVisible || panel.hidden) return;
  const top = Math.max(previewTopGap, panel.getBoundingClientRect().top);
  const available = window.innerHeight - top - previewTopGap;
  panel.style.setProperty("--preview-max-height", `${Math.max(240, available)}px`);
}

function schedulePreviewResize() {
  if (previewResizeFrame) return;
  previewResizeFrame = requestAnimationFrame(() => {
    previewResizeFrame = 0;
    syncPreviewHeight();
  });
}

function applyPreviewVisibility() {
  elements.console.classList.toggle("is-preview-hidden", !previewVisible);
  elements.previewPanel.hidden = !previewVisible;
  elements.showPreviewButton.hidden = previewVisible;
  if (previewVisible) {
    syncPreviewHeight();
    loadMarkdown();
  }
}

async function loadMarkdown() {
  if (!previewVisible) return;
  const request = ++previewRequest;
  const scroll = elements.markdownScroll;
  const nearBottom = scroll.scrollHeight - scroll.scrollTop - scroll.clientHeight < 120;
  const previousTop = scroll.scrollTop;
  try {
    const response = await fetch(`/api/markdown?limit=${previewLimit}`);
    if (!response.ok) throw new Error("生成合并文稿失败");
    const source = await response.text();
    if (request !== previewRequest) return;
    // renderMarkdown escapes every text fragment before inserting its fixed tags.
    elements.markdownPreview.innerHTML = renderMarkdown(source);
    if (!previewLoaded || nearBottom) {
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
    elements.previewNote.textContent = `最近 ${previewLimit} 条 · 本地生成`;
  } catch (error) {
    if (request !== previewRequest) return;
    elements.markdownPreview.textContent = `预览加载失败：${error.message}`;
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
  if (!previewVisible) return;
  clearTimeout(previewTimer);
  previewTimer = setTimeout(loadMarkdown, 250);
}

function renderSegments() {
  const ordered = [...segments.values()].sort((a, b) => new Date(b.startedAt) - new Date(a.startedAt));
  const fragment = document.createDocumentFragment();
  for (const segment of ordered) {
    const item = elements.segmentTemplate.content.firstElementChild.cloneNode(true);
    const time = item.querySelector("time");
    time.dateTime = segment.startedAt;
    time.textContent = formatTime(segment.startedAt);
    item.querySelector(".segment-duration").textContent = formatDuration(segment.durationMs);
    const speaker = item.querySelector(".segment-speaker");
    if (segment.speaker) {
      speaker.textContent = speakerNames.get(segment.speaker) || segment.speakerName || segment.speaker;
      speaker.hidden = false;
      speaker.title = `标注 ${segment.speaker}`;
      speaker.dataset.label = segment.speaker;
      speaker.addEventListener("click", () => openSpeakerDialog(segment.speaker));
      const match = /(\d+)$/.exec(segment.speaker);
      const color = match ? ((Number(match[1]) - 1) % 6) + 1 : 1;
      speaker.classList.add(`speaker-${color}`);
    }
    const text = item.querySelector(".segment-text");
    text.textContent = segment.text || "未识别出文字，录音已保留";
    text.classList.toggle("is-empty", !segment.text);
    const audioURL = `/api/segments/${encodeURIComponent(segment.id)}/audio`;
    item.querySelector("audio").src = audioURL;
    const download = item.querySelector(".download-link");
    download.href = audioURL;
    download.download = segment.audioPath
      ? segment.audioPath.split("/").pop()
      : `${segment.id}.wav`;
    download.setAttribute("aria-label", `下载 ${formatTime(segment.startedAt)} 的录音`);
    fragment.append(item);
  }
  elements.segmentList.replaceChildren(fragment);
  elements.emptyState.hidden = ordered.length > 0;
}

async function loadStatus() {
  try {
    updateStatus(await api("/api/status"));
  } catch (error) {
    showError(error.message);
  }
}

async function loadHistory(append = false) {
  elements.refreshButton.disabled = true;
  elements.loadMoreButton.disabled = true;
  try {
    const query = append ? `?limit=50&before=${encodeURIComponent(nextBefore)}` : "?limit=50";
    const response = await api(`/api/segments${query}`);
    if (!append) segments.clear();
    for (const segment of response.data) segments.set(segment.id, segment);
    nextBefore = response.nextBefore || "";
    elements.loadMoreButton.hidden = !nextBefore;
    renderSegments();
    loadStats();
  } catch (error) {
    showError(error.message);
  } finally {
    elements.refreshButton.disabled = false;
    elements.loadMoreButton.disabled = false;
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
    loadHistory();
    loadDays();
    loadSpeakers();
    if (previewVisible) loadMarkdown();
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
    loadHistory();
    loadStats();
    loadSpeakers();
    schedulePreviewRefresh();
  });
  eventSource.addEventListener("segment", (event) => {
    const segment = JSON.parse(event.data);
    segments.set(segment.id, segment);
    renderSegments();
    elements.interimText.textContent = "当前语句已保存，继续聆听…";
    elements.liveTime.textContent = "";
    hideLivePreview();
    loadStatus();
    loadStats();
    schedulePreviewRefresh();
  });
}

elements.toggleButton.addEventListener("click", toggleCapture);
elements.refreshButton.addEventListener("click", () => loadHistory());
elements.loadMoreButton.addEventListener("click", () => loadHistory(true));
elements.calendarPrev.addEventListener("click", () => {
  calendarCursor = new Date(calendarCursor.getFullYear(), calendarCursor.getMonth() - 1, 1);
  selectedDay = "";
  renderCalendar();
  renderDayDetail();
});
elements.calendarNext.addEventListener("click", () => {
  calendarCursor = new Date(calendarCursor.getFullYear(), calendarCursor.getMonth() + 1, 1);
  selectedDay = "";
  renderCalendar();
  renderDayDetail();
});
elements.calendarGrid.addEventListener("click", (event) => {
  const cell = event.target.closest(".calendar-cell");
  if (!cell || cell.disabled) return;
  selectedDay = selectedDay === cell.dataset.date ? "" : cell.dataset.date;
  renderDayDetail();
});
elements.calendarDeleteButton.addEventListener("click", openDeleteDialog);
elements.deleteCancel.addEventListener("click", () => elements.deleteDialog.close());
elements.deleteForm.addEventListener("submit", submitDelete);
elements.speakerCancel.addEventListener("click", () => elements.speakerDialog.close());
elements.speakerForm.addEventListener("submit", submitSpeaker);
elements.lexiconButton.addEventListener("click", openLexiconDialog);
elements.lexiconCancel.addEventListener("click", () => elements.lexiconDialog.close());
elements.lexiconForm.addEventListener("submit", submitLexicon);
window.addEventListener("scroll", schedulePreviewResize, { passive: true });
window.addEventListener("resize", schedulePreviewResize);
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
Promise.all([loadStatus(), loadHistory(), loadDays(), loadSpeakers()]).finally(connectEvents);
