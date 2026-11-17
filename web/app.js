"use strict";

const i18n = window.DitingI18n;
const t = (key, variables) => i18n.t(key, variables);

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
  markdownDownload: document.querySelector("#markdownDownload"),
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
  calendarSummaryButton: document.querySelector("#calendarSummaryButton"),
  calendarDeleteButton: document.querySelector("#calendarDeleteButton"),
  minutesDialog: document.querySelector("#minutesDialog"),
  minutesDialogDate: document.querySelector("#minutesDialogDate"),
  minutesStatus: document.querySelector("#minutesStatus"),
  minutesError: document.querySelector("#minutesError"),
  minutesContent: document.querySelector("#minutesContent"),
  minutesPreview: document.querySelector("#minutesPreview"),
  minutesSource: document.querySelector("#minutesSource"),
  minutesDownload: document.querySelector("#minutesDownload"),
  minutesCloseIcon: document.querySelector("#minutesCloseIcon"),
  minutesCloseButton: document.querySelector("#minutesCloseButton"),
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
};

for (const element of [
  elements.connectionText,
  elements.stateLabel,
  elements.listenerTitle,
  elements.stateHint,
  elements.toggleButton,
  elements.privacyTitle,
  elements.privacyText,
  elements.interimText,
  elements.speakerMergeHint,
  elements.splitParagraphsButton,
  elements.topicsEmpty,
  elements.calendarSummaryButton,
  elements.minutesStatus,
]) {
  element.removeAttribute("data-i18n");
}

const calendarDays = new Map();
const speakerViews = new Map();
let currentStatus = { state: "STOPPED", startedAt: null };
let requestPending = false;
let eventSource;
let calendarCursor = startOfMonth(new Date());
let selectedDay = "";
let activeSpeakerLabel = "";
let currentASR = "local";
let currentParagraphs = [];
let archiveStats = { audioFiles: 0, audioBytes: 0, transcriptChars: 0 };

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
let summarizingDay = false;
let activeMinutesDate = "";
let minutesDownloadURL = "";

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
    throw new Error(i18n.apiError(body.error));
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

function updateStatus(status, preserveInterim = false) {
  currentStatus = status;
  const running = status.state === "RUNNING";
  const stopping = status.state === "STOPPING";
  elements.listenerPanel.classList.toggle("is-running", running);
  elements.toggleButton.classList.toggle("is-running", running);
  elements.toggleButton.disabled = stopping || requestPending;
  elements.toggleButton.textContent = running ? t("action.stopListening") : stopping ? t("action.stopping") : t("action.startListening");
  elements.stateLabel.textContent = running ? t("state.onAir") : stopping ? t("state.stopping") : t("state.standby");
  elements.listenerTitle.textContent = running ? t("listener.runningTitle") : stopping ? t("listener.stoppingTitle") : t("listener.idleTitle");
  elements.stateHint.textContent = running
    ? t("listener.runningHint")
    : stopping
      ? t("listener.stoppingHint")
      : t("listener.idleHint");
  elements.segmentCount.textContent = status.segmentCount || 0;
  elements.droppedChunks.textContent = status.droppedChunks || 0;
  if (!running && stopping === false) elements.uptime.textContent = "00:00:00";
  showError(status.lastError || "");
  if (preserveInterim) return;
  if (!running && !stopping) {
    elements.interimText.textContent = t("live.stopped");
    elements.liveTime.textContent = "";
    hideLivePreview();
  } else if (running && !elements.liveTime.textContent) {
    elements.interimText.textContent = t("live.waiting");
  }
}

function formatTime(value) {
  return new Intl.DateTimeFormat(i18n.localeTag, {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).format(new Date(value));
}

let numberFormat = new Intl.NumberFormat(i18n.localeTag);

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
  archiveStats = stats;
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

let monthFormat = new Intl.DateTimeFormat(i18n.localeTag, { year: "numeric", month: "long" });
let dayFormat = new Intl.DateTimeFormat(i18n.localeTag, {
  year: "numeric",
  month: "long",
  day: "numeric",
  weekday: "short",
});

function updateLocaleFormats() {
  numberFormat = new Intl.NumberFormat(i18n.localeTag);
  monthFormat = new Intl.DateTimeFormat(i18n.localeTag, { year: "numeric", month: "long" });
  dayFormat = new Intl.DateTimeFormat(i18n.localeTag, {
    year: "numeric",
    month: "long",
    day: "numeric",
    weekday: "short",
  });
}

function weekdayNames() {
  const format = new Intl.DateTimeFormat(i18n.localeTag, { weekday: "short" });
  return Array.from({ length: 7 }, (_, index) => format.format(new Date(2024, 0, 1 + index)));
}

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
  for (const name of weekdayNames()) {
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
      if (day.segments) parts.push(t("calendar.segmentSummary", { count: numberFormat.format(day.segments) }));
      if (day.audioFiles) parts.push(t("calendar.recordingSummary", { count: numberFormat.format(day.audioFiles) }));
      parts.push(formatBytes(day.audioBytes));
      cell.title = parts.join(" · ");
      cell.setAttribute("aria-label", parts.join(", "));
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
  elements.calendarSummaryButton.disabled = summarizingDay || !day.segments || !paragraphCorrectionAvailable;
  elements.calendarSummaryButton.textContent = t(summarizingDay ? "minutes.generating" : "minutes.generate");
  elements.calendarSummaryButton.title = paragraphCorrectionAvailable ? "" : t("minutes.unavailable");
  elements.calendarDeleteButton.disabled = summarizingDay || (!day.segments && !day.audioFiles);
}

function openDeleteDialog() {
  const day = calendarDays.get(selectedDay);
  if (!day) return;
  elements.deleteDialogText.textContent = t("delete.description", {
    date: formatDayLabel(day.date),
    segments: numberFormat.format(day.segments || 0),
    recordings: numberFormat.format(day.audioFiles || 0),
    size: formatBytes(day.audioBytes),
  });
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

function updateMinutesDownload(date, markdown) {
  if (minutesDownloadURL) URL.revokeObjectURL(minutesDownloadURL);
  minutesDownloadURL = URL.createObjectURL(new Blob([markdown], { type: "text/markdown;charset=utf-8" }));
  elements.minutesDownload.href = minutesDownloadURL;
  elements.minutesDownload.download = `diting-${date}-minutes.md`;
}

async function generateDayMinutes() {
  const date = selectedDay;
  if (!date || summarizingDay || !paragraphCorrectionAvailable) return;
  summarizingDay = true;
  activeMinutesDate = date;
  renderDayDetail();

  elements.minutesDialogDate.textContent = formatDayLabel(date);
  elements.minutesStatus.textContent = t("minutes.working");
  elements.minutesStatus.hidden = false;
  elements.minutesError.hidden = true;
  elements.minutesContent.hidden = true;
  elements.minutesDownload.hidden = true;
  elements.minutesPreview.replaceChildren();
  elements.minutesSource.textContent = "";
  if (!elements.minutesDialog.open) elements.minutesDialog.showModal();
  elements.minutesCloseIcon.focus();

  try {
    const result = await api(`/api/days/${encodeURIComponent(date)}/minutes`, { method: "POST" });
    elements.minutesPreview.innerHTML = renderMarkdown(result.markdown);
    elements.minutesSource.textContent = result.markdown;
    updateMinutesDownload(date, result.markdown);
    elements.minutesStatus.hidden = true;
    elements.minutesContent.hidden = false;
    elements.minutesDownload.hidden = false;
    elements.minutesDownload.focus();
  } catch (error) {
    elements.minutesStatus.hidden = true;
    elements.minutesError.textContent = t("minutes.failed", { message: error.message });
    elements.minutesError.hidden = false;
  } finally {
    summarizingDay = false;
    renderDayDetail();
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
  return i18n.speakerLabel(view ? (view.name || label) : label);
}

function isMerged(label) {
  const view = speakerViews.get(label);
  return view && !!view.mergedInto;
}

function updateSpeakerMergeHint() {
  const mergedInto = speakerViews.get(activeSpeakerLabel)?.mergedInto;
  elements.speakerMergeHint.textContent = mergedInto
    ? t("speaker.mergeHintCurrent", { name: speakerDisplayName(mergedInto) })
    : t("speaker.mergeHint");
}

function openSpeakerDialog(label) {
  if (!label) return;
  activeSpeakerLabel = label;
  const view = speakerViews.get(label);
  const name = view ? (view.name || label) : label;
  elements.speakerDialogLabel.textContent = i18n.speakerLabel(label);
  elements.speakerAliasInput.value = name === label ? "" : name;
  elements.speakerAliasInput.disabled = isMerged(label);

  const select = elements.speakerMergeTarget;
  select.replaceChildren();
  const noneOption = document.createElement("option");
  noneOption.value = "";
  noneOption.textContent = t("speaker.mergeNone");
  select.append(noneOption);
  for (const [otherLabel, otherView] of speakerViews) {
    if (otherLabel === label) continue;
    if (otherView.mergedInto) continue;
    const option = document.createElement("option");
    option.value = otherLabel;
    const displayName = i18n.speakerLabel(otherView.name || otherLabel);
    const stableLabel = i18n.speakerLabel(otherLabel);
    option.textContent = displayName === stableLabel ? stableLabel : `${displayName} (${stableLabel})`;
    select.append(option);
  }
  select.value = view?.mergedInto || "";
  select.disabled = false;

  updateSpeakerMergeHint();

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

function updateASRPresentation(provider) {
  currentASR = provider;
  const cloud = provider === "mimo";
  elements.technicalNote.textContent = cloud ? "16 kHz  ·  MONO  ·  MIMO API" : "16 kHz  ·  MONO  ·  ON DEVICE";
  elements.privacyTitle.textContent = cloud ? t("privacy.cloudTitle") : t("privacy.localTitle");
  elements.privacyText.textContent = cloud ? t("privacy.cloudText") : t("privacy.localText");
}

async function loadSettingsButton() {
  const previous = paragraphCorrectionAvailable;
  try {
    const settings = await api("/api/settings");
    updateASRPresentation(settings.asr);
    elements.splitParagraphsButton.hidden = !settings.available;
    paragraphCorrectionAvailable = Boolean(settings.available);
  } catch {
    elements.splitParagraphsButton.hidden = true;
    paragraphCorrectionAvailable = false;
  }
  renderDayDetail();
  if (previewVisible && previous !== paragraphCorrectionAvailable) {
    await loadMarkdown();
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
  const selection = selectedDay ? `date=${encodeURIComponent(selectedDay)}` : `limit=${previewLimit}`;
  return `${selection}&lang=${i18n.locale}`;
}

function updateMarkdownDownload() {
  elements.markdownDownload.href = `/api/markdown?download=1&lang=${i18n.locale}`;
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
      currentParagraphs = paragraphData.data || [];
      renderTopics(currentParagraphs);
      return true;
    } catch (error) {
      if (request !== previewRequest) return false;
      elements.topicsEmpty.hidden = false;
      elements.topicsEmpty.textContent = t("topics.loadFailed", { message: error.message });
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
    if (!response.ok) throw new Error(t("preview.markdownFailed"));
    const source = await response.text();
    if (request !== previewRequest) return false;
    currentParagraphs = paragraphData.data || [];
    renderTopics(currentParagraphs);
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
        ? t("preview.date", { date: formatDayLabel(selectedDay) })
        : t("preview.recent", { limit: previewLimit });
      elements.previewNote.classList.remove("is-error");
    }
    return true;
  } catch (error) {
    if (request !== previewRequest) return false;
    elements.markdownPreview.textContent = t("preview.loadFailed", { message: error.message });
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
    elements.previewNote.textContent = t("preview.notFound");
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
    title.textContent = paragraph.title || t("topics.untitled");
    const summary = document.createElement("p");
    summary.textContent = paragraph.summary || t("topics.noSummary");
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
  elements.topicsEmpty.textContent = t("topics.empty");
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
    button.textContent = t("paragraph.correct");
    button.disabled = correctingPreview || splittingPreview || retranscribingPreview || summarizingPreview;
    button.title = t("paragraph.correctTitle");
    button.setAttribute("aria-label", t("paragraph.correctLabel", { time: strong.textContent }));
    const asrButton = document.createElement("button");
    asrButton.type = "button";
    asrButton.className = "md-asr";
    asrButton.dataset.paragraphId = paragraph.id;
    asrButton.textContent = "ASR";
    asrButton.disabled = correctingPreview || splittingPreview || retranscribingPreview || summarizingPreview;
    asrButton.title = t("paragraph.asrTitle");
    asrButton.setAttribute("aria-label", t("paragraph.asrLabel", { time: strong.textContent }));
    const summaryButton = document.createElement("button");
    summaryButton.type = "button";
    summaryButton.className = "md-summary";
    summaryButton.dataset.paragraphId = paragraph.id;
    summaryButton.textContent = t("paragraph.summary");
    summaryButton.disabled = correctingPreview || splittingPreview || retranscribingPreview || summarizingPreview;
    summaryButton.title = t("paragraph.summaryTitle");
    summaryButton.setAttribute("aria-label", t("paragraph.summaryLabel", { time: strong.textContent }));
    item.append(" ", button, " ", asrButton, " ", summaryButton);
  }
}

async function splitParagraphs() {
  if (correctingPreview || splittingPreview || retranscribingPreview || summarizingPreview) return;
  splittingPreview = true;
  elements.splitParagraphsButton.disabled = true;
  elements.splitParagraphsButton.textContent = t("paragraph.splitting");
  for (const item of elements.markdownPreview.querySelectorAll(".md-correct, .md-asr, .md-summary")) {
    item.disabled = true;
  }
  elements.previewNote.textContent = t("paragraph.splitWorking", { limit: previewLimit });
  elements.previewNote.classList.remove("is-error");
  try {
    const result = await api("/api/paragraphs/split", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ limit: previewLimit }),
    });
    const refreshed = await loadMarkdown();
    if (!refreshed) {
      elements.previewNote.textContent = t("paragraph.splitRefreshFailed");
      elements.previewNote.classList.add("is-error");
    } else if (!result.paragraphs) {
      elements.previewNote.textContent = t("paragraph.noRecords");
    } else if (result.fallback) {
      elements.previewNote.textContent = t("paragraph.splitFallback", { count: result.paragraphs, fallback: result.fallback });
    } else {
      elements.previewNote.textContent = t("paragraph.splitDone", { count: result.paragraphs });
    }
  } catch (error) {
    elements.previewNote.textContent = t("paragraph.splitFailed", { message: error.message });
    elements.previewNote.classList.add("is-error");
  } finally {
    splittingPreview = false;
    elements.splitParagraphsButton.disabled = false;
    elements.splitParagraphsButton.textContent = t("preview.split");
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
  button.textContent = t("paragraph.correcting");
  elements.previewNote.textContent = t("paragraph.correctWorking");
  elements.previewNote.classList.remove("is-error");
  try {
    const result = await api(`/api/paragraphs/${encodeURIComponent(id)}/correct`, { method: "POST" });
    const refreshed = await loadMarkdown();
    if (!refreshed) {
      elements.previewNote.textContent = t("paragraph.correctRefreshFailed");
      elements.previewNote.classList.add("is-error");
    } else if (!result.changed) {
      elements.previewNote.textContent = t("paragraph.correctUnchanged");
    } else {
      elements.previewNote.textContent = t("paragraph.correctDone");
    }
  } catch (error) {
    elements.previewNote.textContent = t("paragraph.correctFailed", { message: error.message });
    elements.previewNote.classList.add("is-error");
  } finally {
    correctingPreview = false;
    elements.splitParagraphsButton.disabled = false;
    let restored;
    for (const item of elements.markdownPreview.querySelectorAll(".md-correct")) {
      item.disabled = false;
      item.textContent = t("paragraph.correct");
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
  button.textContent = t("paragraph.recognizing");
  elements.previewNote.textContent = t("paragraph.asrWorking");
  elements.previewNote.classList.remove("is-error");
  try {
    const result = await api(`/api/paragraphs/${encodeURIComponent(id)}/asr`, { method: "POST" });
    const refreshed = await loadMarkdown();
    if (!refreshed) {
      elements.previewNote.textContent = t("paragraph.asrRefreshFailed");
      elements.previewNote.classList.add("is-error");
    } else if (!result.changed) {
      elements.previewNote.textContent = t("paragraph.asrUnchanged", { count: result.segments });
    } else {
      elements.previewNote.textContent = t("paragraph.asrDone", { count: result.segments, changed: result.changed });
    }
  } catch (error) {
    elements.previewNote.textContent = t("paragraph.asrFailed", { message: error.message });
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
  button.textContent = t("paragraph.summarizing");
  elements.previewNote.textContent = t("paragraph.summaryWorking");
  elements.previewNote.classList.remove("is-error");
  try {
    await api(`/api/paragraphs/${encodeURIComponent(id)}/summarize`, { method: "POST" });
    const refreshed = await loadMarkdown();
    if (!refreshed) {
      elements.previewNote.textContent = t("paragraph.summaryRefreshFailed");
      elements.previewNote.classList.add("is-error");
    } else {
      elements.previewNote.textContent = t("paragraph.summaryDone");
    }
  } catch (error) {
    elements.previewNote.textContent = t("paragraph.summaryFailed", { message: error.message });
    elements.previewNote.classList.add("is-error");
  } finally {
    summarizingPreview = false;
    elements.splitParagraphsButton.disabled = false;
    let restored;
    for (const item of elements.markdownPreview.querySelectorAll(".md-correct, .md-asr, .md-summary")) {
      item.disabled = false;
      if (item.classList.contains("md-summary")) item.textContent = t("paragraph.summary");
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
    button.textContent = speakerDisplayName(label);
    button.dataset.label = label;
    button.title = t("speaker.annotate", { label: i18n.speakerLabel(label) });

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
    elements.connectionText.textContent = t("connection.connected");
    loadStatus();
    loadDays();
    loadSpeakers();
    loadMarkdown();
  };
  eventSource.onerror = (event) => {
    if (event.data) return;
    elements.connection.classList.remove("is-connected");
    elements.connectionText.textContent = t("connection.reconnecting");
  };
  eventSource.addEventListener("status", (event) => updateStatus(JSON.parse(event.data)));
  eventSource.addEventListener("error", (event) => {
    if (event.data) updateStatus(JSON.parse(event.data));
  });
  eventSource.addEventListener("interim", (event) => {
    const interim = JSON.parse(event.data);
    elements.interimText.textContent = interim.text || t("live.finishing");
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
    elements.interimText.textContent = t("live.saved");
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

i18n.subscribe(() => {
  updateLocaleFormats();
  updateStatus(currentStatus, currentStatus.state === "RUNNING");
  updateStats(archiveStats);
  updateMarkdownDownload();
  updateASRPresentation(currentASR);
  renderCalendar();
  renderDayDetail();
  renderTopics(currentParagraphs);
  if (elements.minutesDialog.open) {
    elements.minutesDialogDate.textContent = activeMinutesDate ? formatDayLabel(activeMinutesDate) : "";
    if (summarizingDay) elements.minutesStatus.textContent = t("minutes.working");
  }
  elements.connectionText.textContent = t(elements.connection.classList.contains("is-connected") ? "connection.connected" : "connection.reconnecting");
  elements.splitParagraphsButton.textContent = t(splittingPreview ? "paragraph.splitting" : "preview.split");
  if (elements.speakerDialog.open) {
    elements.speakerDialogLabel.textContent = i18n.speakerLabel(activeSpeakerLabel);
    updateSpeakerMergeHint();
    for (const option of elements.speakerMergeTarget.options) {
      if (!option.value) {
        option.textContent = t("speaker.mergeNone");
        continue;
      }
      const displayName = speakerDisplayName(option.value);
      const stableLabel = i18n.speakerLabel(option.value);
      option.textContent = displayName === stableLabel ? stableLabel : `${displayName} (${stableLabel})`;
    }
  }
  loadMarkdown();
});

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
elements.calendarSummaryButton.addEventListener("click", generateDayMinutes);
elements.calendarDeleteButton.addEventListener("click", openDeleteDialog);
elements.minutesCloseIcon.addEventListener("click", () => elements.minutesDialog.close());
elements.minutesCloseButton.addEventListener("click", () => elements.minutesDialog.close());
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
elements.splitParagraphsButton.addEventListener("click", splitParagraphs);
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

updateMarkdownDownload();
applyPreviewVisibility();
renderCalendar();
Promise.all([loadStatus(), loadDays(), loadSpeakers(), loadSettingsButton()]).finally(connectEvents);
