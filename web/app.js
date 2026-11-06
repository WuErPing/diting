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
};

const segments = new Map();
let currentStatus = { state: "STOPPED", startedAt: null };
let nextBefore = "";
let requestPending = false;
let eventSource;

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
      speaker.textContent = segment.speaker;
      speaker.hidden = false;
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
    download.download = `${segment.id}.wav`;
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
  eventSource.addEventListener("segment", (event) => {
    const segment = JSON.parse(event.data);
    segments.set(segment.id, segment);
    renderSegments();
    elements.interimText.textContent = "当前语句已保存，继续聆听…";
    elements.liveTime.textContent = "";
    hideLivePreview();
    loadStatus();
    schedulePreviewRefresh();
  });
}

elements.toggleButton.addEventListener("click", toggleCapture);
elements.refreshButton.addEventListener("click", () => loadHistory());
elements.loadMoreButton.addEventListener("click", () => loadHistory(true));
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
Promise.all([loadStatus(), loadHistory()]).finally(connectEvents);
