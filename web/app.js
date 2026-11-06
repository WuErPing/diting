"use strict";

const elements = {
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
};

const segments = new Map();
let currentStatus = { state: "STOPPED", startedAt: null };
let nextBefore = "";
let requestPending = false;
let eventSource;

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

function renderSegments() {
  const ordered = [...segments.values()].sort((a, b) => new Date(b.startedAt) - new Date(a.startedAt));
  const fragment = document.createDocumentFragment();
  for (const segment of ordered) {
    const item = elements.segmentTemplate.content.firstElementChild.cloneNode(true);
    const time = item.querySelector("time");
    time.dateTime = segment.startedAt;
    time.textContent = formatTime(segment.startedAt);
    item.querySelector(".segment-duration").textContent = formatDuration(segment.durationMs);
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
  });
  eventSource.addEventListener("segment", (event) => {
    const segment = JSON.parse(event.data);
    segments.set(segment.id, segment);
    renderSegments();
    elements.interimText.textContent = "当前语句已保存，继续聆听…";
    elements.liveTime.textContent = "";
    loadStatus();
  });
}

elements.toggleButton.addEventListener("click", toggleCapture);
elements.refreshButton.addEventListener("click", () => loadHistory());
elements.loadMoreButton.addEventListener("click", () => loadHistory(true));
setInterval(() => {
  if (currentStatus.state === "RUNNING") {
    elements.uptime.textContent = formatUptime(currentStatus.startedAt);
  }
}, 1000);

Promise.all([loadStatus(), loadHistory()]).finally(connectEvents);
