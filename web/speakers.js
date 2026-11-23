"use strict";

const i18n = window.DitingI18n;
const t = (key, variables) => i18n.t(key, variables);
const { speakerChanges } = window.DitingVoiceprints;

const elements = {
  error: document.querySelector("#voiceprintsError"),
  saved: document.querySelector("#voiceprintsSaved"),
  empty: document.querySelector("#voiceprintsEmpty"),
  list: document.querySelector("#voiceprintList"),
  template: document.querySelector("#voiceprintTemplate"),
  refresh: document.querySelector("#refreshVoiceprintsButton"),
};

let speakerViews = [];
let speakerSamples = new Map();
let savingLabel = "";

async function api(path, options = {}) {
  const response = await fetch(path, options);
  const body = await response.json();
  if (!response.ok) throw new Error(i18n.apiError(body.error));
  return body;
}

function speakerView(label) {
  return speakerViews.find((view) => view.label === label);
}

function speakerDisplayName(label) {
  const view = speakerView(label);
  return i18n.speakerLabel(view ? (view.name || label) : label);
}

function formatTime(value) {
  return new Intl.DateTimeFormat(i18n.localeTag, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(new Date(value));
}

function fillMergeOptions(select, current) {
  const noneOption = document.createElement("option");
  noneOption.value = "";
  noneOption.textContent = t("speaker.mergeNone");
  select.append(noneOption);
  for (const view of speakerViews) {
    if (view.label === current.label || view.mergedInto) continue;
    const option = document.createElement("option");
    option.value = view.label;
    const displayName = speakerDisplayName(view.label);
    const stableLabel = i18n.speakerLabel(view.label);
    option.textContent = displayName === stableLabel ? stableLabel : `${displayName} (${stableLabel})`;
    select.append(option);
  }
  select.value = current.mergedInto || "";
}

function renderVoiceprints() {
  const fragment = document.createDocumentFragment();
  speakerViews.forEach((view, index) => {
    const item = elements.template.content.firstElementChild.cloneNode(true);
    item.dataset.label = view.label;
    item.classList.add(`speaker-${(index % 6) + 1}`);
    const number = /(\d+)$/.exec(view.label);
    item.querySelector(".voiceprint-number").textContent = number ? number[1] : "–";
    item.querySelector(".voiceprint-name").textContent = speakerDisplayName(view.label);
    item.querySelector(".voiceprint-label").textContent = t("voiceprints.stableLabel", { label: i18n.speakerLabel(view.label) });
    const merged = item.querySelector(".voiceprint-merged");
    merged.hidden = !view.mergedInto;
    if (view.mergedInto) merged.textContent = t("voiceprints.mergedInto", { name: speakerDisplayName(view.mergedInto) });

    const sample = speakerSamples.get(view.label);
    const sampleSection = item.querySelector(".voiceprint-sample");
    sampleSection.setAttribute("aria-label", t("voiceprints.sampleFor", { name: speakerDisplayName(view.label) }));
    item.querySelector(".voiceprint-sample-label").textContent = t("voiceprints.sample");
    const sampleText = item.querySelector(".voiceprint-sample-text");
    sampleText.textContent = sample ? sample.text : t("voiceprints.noSample");
    sampleText.classList.toggle("is-empty", !sample);
    const sampleMeta = item.querySelector(".voiceprint-sample-meta");
    sampleMeta.hidden = !sample;
    const audio = item.querySelector("audio");
    audio.hidden = !sample;
    if (sample) {
      const time = item.querySelector("time");
      time.dateTime = sample.startedAt;
      time.textContent = formatTime(sample.startedAt);
      item.querySelector(".voiceprint-sample-duration").textContent = t("records.seconds", { seconds: (sample.durationMs / 1000).toFixed(1) });
      audio.src = `/api/segments/${encodeURIComponent(sample.id)}/audio`;
      audio.setAttribute("aria-label", t("voiceprints.sampleAudio", { name: speakerDisplayName(view.label) }));
    } else {
      audio.removeAttribute("src");
    }

    const form = item.querySelector(".voiceprint-form");
    form.dataset.label = view.label;
    const fields = item.querySelector(".voiceprint-fields");
    fields.disabled = savingLabel === view.label;
    item.querySelector(".voiceprint-alias-label").textContent = t("speaker.aliasLabel");
    const alias = item.querySelector(".voiceprint-alias");
    alias.value = view.name && view.name !== view.label ? view.name : "";
    alias.placeholder = t("speaker.aliasPlaceholder");
    alias.disabled = Boolean(view.mergedInto);
    alias.setAttribute("aria-label", t("voiceprints.aliasFor", { label: i18n.speakerLabel(view.label) }));
    item.querySelector(".voiceprint-merge-label").textContent = t("speaker.mergeLabel");
    const merge = item.querySelector(".voiceprint-merge");
    merge.setAttribute("aria-label", t("voiceprints.mergeFor", { label: i18n.speakerLabel(view.label) }));
    fillMergeOptions(merge, view);
    item.querySelector(".voiceprint-form-hint").textContent = view.mergedInto
      ? t("voiceprints.unmergeHint")
      : t("voiceprints.formHint");
    const save = item.querySelector("button[type=submit]");
    save.textContent = savingLabel === view.label ? t("voiceprints.saving") : t("action.save");
    fragment.append(item);
  });
  elements.list.replaceChildren(fragment);
  elements.empty.hidden = speakerViews.length > 0;
}

async function loadVoiceprints() {
  elements.error.hidden = true;
  elements.saved.hidden = true;
  elements.list.setAttribute("aria-busy", "true");
  try {
    const [speakers, samples] = await Promise.all([
      api("/api/speakers"),
      api("/api/speakers/samples"),
    ]);
    speakerViews = speakers.data || [];
    speakerSamples = new Map((samples.data || []).filter((entry) => entry.sample).map((entry) => [entry.label, entry.sample]));
    renderVoiceprints();
  } catch (error) {
    elements.error.textContent = error.message;
    elements.error.hidden = false;
  } finally {
    elements.list.setAttribute("aria-busy", "false");
  }
}

async function saveSpeaker(form) {
  const label = form.dataset.label;
  const view = speakerView(label);
  if (!view || savingLabel) return;
  const errorElement = form.querySelector(".voiceprint-form-error");
  errorElement.hidden = true;
  const draft = {
    alias: form.querySelector(".voiceprint-alias").value.trim(),
    mergedInto: form.querySelector(".voiceprint-merge").value,
  };
  const changes = speakerChanges(view, draft);
  if (!Object.keys(changes).length) return;

  savingLabel = label;
  const fields = form.querySelector(".voiceprint-fields");
  const save = form.querySelector("button[type=submit]");
  fields.disabled = true;
  save.textContent = t("voiceprints.saving");
  try {
    if (Object.hasOwn(changes, "alias")) {
      await api(`/api/speakers/${encodeURIComponent(label)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ alias: changes.alias }),
      });
    }
    if (Object.hasOwn(changes, "mergedInto")) {
      await api(`/api/speakers/${encodeURIComponent(label)}/merge`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ target: changes.mergedInto }),
      });
    }
    savingLabel = "";
    await loadVoiceprints();
    elements.saved.hidden = false;
  } catch (error) {
    errorElement.textContent = error.message;
    errorElement.hidden = false;
  } finally {
    savingLabel = "";
    if (form.isConnected) {
      fields.disabled = false;
      save.textContent = t("action.save");
    }
  }
}

i18n.subscribe(renderVoiceprints);
elements.refresh.addEventListener("click", loadVoiceprints);
elements.list.addEventListener("submit", (event) => {
  event.preventDefault();
  if (event.target.matches(".voiceprint-form")) saveSpeaker(event.target);
});
elements.list.addEventListener("input", (event) => {
  const error = event.target.closest("form")?.querySelector(".voiceprint-form-error");
  if (error) error.hidden = true;
});

loadVoiceprints();
