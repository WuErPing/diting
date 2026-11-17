"use strict";

const i18n = window.DitingI18n;
const t = (key, variables) => i18n.t(key, variables);

const elements = {
  connection: document.querySelector("#connection"),
  connectionText: document.querySelector("#connectionText"),
  errorMessage: document.querySelector("#errorMessage"),
  refreshButton: document.querySelector("#refreshButton"),
  emptyState: document.querySelector("#emptyState"),
  segmentList: document.querySelector("#segmentList"),
  loadMoreButton: document.querySelector("#loadMoreButton"),
  segmentTemplate: document.querySelector("#segmentTemplate"),
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

for (const element of [elements.connectionText, elements.speakerMergeHint]) {
  element.removeAttribute("data-i18n");
}

const segments = new Map();
const speakerViews = new Map();
let nextBefore = "";
let activeSpeakerLabel = "";
let eventSource;

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

function formatDuration(milliseconds) {
  return t("records.seconds", { seconds: (milliseconds / 1000).toFixed(1) });
}

function speakerDisplayName(label) {
  const view = speakerViews.get(label);
  return i18n.speakerLabel(view ? (view.name || label) : label);
}

function isMerged(label) {
  const view = speakerViews.get(label);
  return view && !!view.mergedInto;
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
      speaker.textContent = speakerDisplayName(segment.speaker);
      speaker.hidden = false;
      speaker.title = t("speaker.annotate", { label: i18n.speakerLabel(segment.speaker) });
      speaker.dataset.label = segment.speaker;
      speaker.addEventListener("click", () => openSpeakerDialog(segment.speaker));
      const canonical = speakerViews.get(segment.speaker)?.mergedInto || segment.speaker;
      const match = /(\d+)$/.exec(canonical);
      const color = match ? ((Number(match[1]) - 1) % 6) + 1 : 1;
      speaker.classList.add(`speaker-${color}`);
      speaker.classList.toggle("is-merged", isMerged(segment.speaker));
    }
    const text = item.querySelector(".segment-text");
    text.textContent = segment.text || t("records.emptyTranscript");
    text.classList.toggle("is-empty", !segment.text);
    const audioURL = `/api/segments/${encodeURIComponent(segment.id)}/audio`;
    item.querySelector("audio").src = audioURL;
    const download = item.querySelector(".download-link");
    download.href = audioURL;
    download.download = segment.audioPath
      ? segment.audioPath.split("/").pop()
      : `${segment.id}.wav`;
    download.textContent = t("records.download");
    download.setAttribute("aria-label", t("records.downloadLabel", { time: formatTime(segment.startedAt) }));
    fragment.append(item);
  }
  elements.segmentList.replaceChildren(fragment);
  elements.emptyState.hidden = ordered.length > 0;
}

async function loadSpeakers() {
  try {
    const { data } = await api("/api/speakers");
    speakerViews.clear();
    for (const view of data) speakerViews.set(view.label, view);
    renderSegments();
  } catch {
    // Stable labels remain usable when speaker metadata cannot be loaded.
  }
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
    if (otherLabel === label || otherView.mergedInto) continue;
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
    renderSegments();
  } catch (error) {
    elements.speakerDialogError.textContent = error.message;
    elements.speakerDialogError.hidden = false;
  } finally {
    elements.speakerSave.disabled = false;
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

function connectEvents() {
  eventSource?.close();
  eventSource = new EventSource("/api/events");
  eventSource.onopen = () => {
    elements.connection.classList.add("is-connected");
    elements.connectionText.textContent = t("connection.connected");
    loadHistory();
    loadSpeakers();
  };
  eventSource.onerror = (event) => {
    if (event.data) return;
    elements.connection.classList.remove("is-connected");
    elements.connectionText.textContent = t("connection.reconnecting");
  };
  eventSource.addEventListener("archive", () => {
    loadHistory();
    loadSpeakers();
  });
  eventSource.addEventListener("segment", (event) => {
    const segment = JSON.parse(event.data);
    segments.set(segment.id, segment);
    renderSegments();
  });
}

i18n.subscribe(() => {
  renderSegments();
  elements.connectionText.textContent = t(elements.connection.classList.contains("is-connected") ? "connection.connected" : "connection.reconnecting");
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
});

elements.refreshButton.addEventListener("click", () => loadHistory());
elements.loadMoreButton.addEventListener("click", () => loadHistory(true));
elements.speakerCancel.addEventListener("click", () => elements.speakerDialog.close());
elements.speakerForm.addEventListener("submit", submitSpeaker);
Promise.all([loadHistory(), loadSpeakers()]).finally(connectEvents);
