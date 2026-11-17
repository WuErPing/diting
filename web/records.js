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
  selectionBar: document.querySelector("#selectionBar"),
  selectAllCheckbox: document.querySelector("#selectAllCheckbox"),
  selectionCount: document.querySelector("#selectionCount"),
  deleteSelectedButton: document.querySelector("#deleteSelectedButton"),
  deleteRecordDialog: document.querySelector("#deleteRecordDialog"),
  deleteRecordForm: document.querySelector("#deleteRecordForm"),
  deleteRecordTitle: document.querySelector("#deleteRecordTitle"),
  deleteRecordDescription: document.querySelector("#deleteRecordDescription"),
  deleteRecordHint: document.querySelector("#deleteRecordHint"),
  deleteRecordError: document.querySelector("#deleteRecordError"),
  deleteRecordCancel: document.querySelector("#deleteRecordCancel"),
  deleteRecordConfirm: document.querySelector("#deleteRecordConfirm"),
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

for (const element of [elements.connectionText, elements.speakerMergeHint, elements.deleteRecordConfirm, elements.deleteRecordTitle, elements.deleteRecordHint]) {
  element.removeAttribute("data-i18n");
}

const segments = new Map();
const speakerViews = new Map();
const selected = new Set();
let nextBefore = "";
let activeSpeakerLabel = "";
let activeSegmentID = "";
let batchDelete = false;
let deletingRecord = false;
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

function updateSelectionUI() {
  elements.selectionBar.hidden = segments.size === 0;
  elements.selectionCount.textContent = t("records.selectedCount", { count: selected.size });
  elements.deleteSelectedButton.disabled = selected.size === 0 || deletingRecord;
  elements.selectAllCheckbox.disabled = deletingRecord;
  elements.selectAllCheckbox.checked = segments.size > 0 && selected.size === segments.size;
  elements.selectAllCheckbox.indeterminate = selected.size > 0 && selected.size < segments.size;
}

function renderSegments() {
  for (const id of selected) {
    if (!segments.has(id)) selected.delete(id);
  }
  const ordered = [...segments.values()].sort((a, b) => new Date(b.startedAt) - new Date(a.startedAt));
  const fragment = document.createDocumentFragment();
  for (const segment of ordered) {
    const item = elements.segmentTemplate.content.firstElementChild.cloneNode(true);
    item.dataset.segmentId = segment.id;
    const time = item.querySelector("time");
    time.dateTime = segment.startedAt;
    time.textContent = formatTime(segment.startedAt);
    const select = item.querySelector(".segment-select");
    select.checked = selected.has(segment.id);
    select.disabled = deletingRecord;
    select.setAttribute("aria-label", t("records.selectLabel", { time: formatTime(segment.startedAt) }));
    select.addEventListener("change", () => {
      if (select.checked) {
        selected.add(segment.id);
      } else {
        selected.delete(segment.id);
      }
      item.classList.toggle("is-selected", select.checked);
      updateSelectionUI();
    });
    item.classList.toggle("is-selected", select.checked);
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
    const deleteButton = item.querySelector(".segment-delete");
    deleteButton.dataset.segmentId = segment.id;
    deleteButton.disabled = deletingRecord;
    deleteButton.textContent = t("records.delete");
    deleteButton.setAttribute("aria-label", t("records.deleteLabel", { time: formatTime(segment.startedAt) }));
    deleteButton.addEventListener("click", () => openRecordDeleteDialog(segment.id));
    fragment.append(item);
  }
  elements.segmentList.replaceChildren(fragment);
  elements.emptyState.hidden = ordered.length > 0;
  updateSelectionUI();
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

function updateRecordDeleteDescription() {
  if (batchDelete) {
    elements.deleteRecordTitle.textContent = t("records.deleteSelectedTitle", { count: selected.size });
    elements.deleteRecordDescription.textContent = t("records.deleteSelectedDescription", { count: selected.size });
    elements.deleteRecordHint.textContent = t("records.deleteSelectedHint");
    return;
  }
  elements.deleteRecordTitle.textContent = t("records.deleteTitle");
  elements.deleteRecordHint.textContent = t("records.deleteHint");
  const segment = segments.get(activeSegmentID);
  elements.deleteRecordDescription.textContent = segment
    ? t("records.deleteDescription", { time: formatTime(segment.startedAt) })
    : "";
}

function openRecordDeleteDialog(id) {
  if (deletingRecord || !segments.has(id)) return;
  batchDelete = false;
  activeSegmentID = id;
  elements.deleteRecordError.hidden = true;
  elements.deleteRecordConfirm.disabled = false;
  elements.deleteRecordConfirm.textContent = t("records.delete");
  updateRecordDeleteDescription();
  elements.deleteRecordDialog.showModal();
  elements.deleteRecordCancel.focus();
}

function openBatchDeleteDialog() {
  if (deletingRecord || selected.size === 0) return;
  batchDelete = true;
  activeSegmentID = "";
  elements.deleteRecordError.hidden = true;
  elements.deleteRecordConfirm.disabled = false;
  elements.deleteRecordConfirm.textContent = t("records.delete");
  updateRecordDeleteDescription();
  elements.deleteRecordDialog.showModal();
  elements.deleteRecordCancel.focus();
}

async function submitRecordDelete(event) {
  event.preventDefault();
  const ids = batchDelete ? [...selected] : [activeSegmentID];
  if (ids.length === 0 || !ids[0] || deletingRecord) return;
  deletingRecord = true;
  elements.deleteRecordError.hidden = true;
  elements.deleteRecordConfirm.disabled = true;
  elements.deleteRecordConfirm.textContent = t("records.deleting");
  for (const button of elements.segmentList.querySelectorAll(".segment-delete")) button.disabled = true;
  for (const input of elements.segmentList.querySelectorAll(".segment-select")) input.disabled = true;
  updateSelectionUI();
  try {
    if (batchDelete) {
      await api("/api/segments/delete", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ids }),
      });
    } else {
      await api(`/api/segments/${encodeURIComponent(ids[0])}`, { method: "DELETE" });
    }
    for (const id of ids) {
      segments.delete(id);
      selected.delete(id);
    }
    renderSegments();
    elements.deleteRecordDialog.close();
  } catch (error) {
    elements.deleteRecordError.textContent = error.message;
    elements.deleteRecordError.hidden = false;
  } finally {
    deletingRecord = false;
    elements.deleteRecordConfirm.disabled = false;
    elements.deleteRecordConfirm.textContent = t("records.delete");
    for (const button of elements.segmentList.querySelectorAll(".segment-delete")) button.disabled = false;
    for (const input of elements.segmentList.querySelectorAll(".segment-select")) input.disabled = false;
    updateSelectionUI();
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
  if (elements.deleteRecordDialog.open) {
    updateRecordDeleteDescription();
    elements.deleteRecordConfirm.textContent = t(deletingRecord ? "records.deleting" : "records.delete");
  }
});

elements.refreshButton.addEventListener("click", () => loadHistory());
elements.loadMoreButton.addEventListener("click", () => loadHistory(true));
elements.selectAllCheckbox.addEventListener("change", () => {
  selected.clear();
  if (elements.selectAllCheckbox.checked) {
    for (const id of segments.keys()) selected.add(id);
  }
  renderSegments();
});
elements.deleteSelectedButton.addEventListener("click", openBatchDeleteDialog);
elements.deleteRecordCancel.addEventListener("click", () => elements.deleteRecordDialog.close());
elements.deleteRecordForm.addEventListener("submit", submitRecordDelete);
elements.speakerCancel.addEventListener("click", () => elements.speakerDialog.close());
elements.speakerForm.addEventListener("submit", submitSpeaker);
Promise.all([loadHistory(), loadSpeakers()]).finally(connectEvents);
