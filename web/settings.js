"use strict";

const i18n = window.DitingI18n;
const t = (key, variables) => i18n.t(key, variables);

const elements = {
  settingsForm: document.querySelector("#settingsForm"),
  settingsFields: document.querySelector("#settingsFields"),
  settingsASR: document.querySelector("#settingsASR"),
  settingsASRHint: document.querySelector("#settingsASRHint"),
  settingsModel: document.querySelector("#settingsModel"),
  settingsCorrect: document.querySelector("#settingsCorrect"),
  settingsError: document.querySelector("#settingsError"),
  settingsSaved: document.querySelector("#settingsSaved"),
  settingsSave: document.querySelector("#settingsSave"),
  lexiconForm: document.querySelector("#lexiconForm"),
  lexiconFields: document.querySelector("#lexiconFields"),
  lexiconInput: document.querySelector("#lexiconInput"),
  lexiconError: document.querySelector("#lexiconError"),
  lexiconSaved: document.querySelector("#lexiconSaved"),
  lexiconSave: document.querySelector("#lexiconSave"),
};

elements.settingsASRHint.removeAttribute("data-i18n");

async function api(path, options = {}) {
  const response = await fetch(path, options);
  const body = await response.json();
  if (!response.ok) throw new Error(i18n.apiError(body.error));
  return body;
}

function updateASRHint(provider) {
  elements.settingsASRHint.textContent = provider === "mimo"
    ? t("settings.asrCloudHint")
    : t("settings.asrLocalHint");
}

function fillModelOptions(models, current) {
  const list = models && models.length ? models.slice() : [];
  if (current && !list.includes(current)) list.unshift(current);
  elements.settingsModel.replaceChildren();
  const source = list.length ? list : [current || ""];
  for (const model of source) {
    const option = document.createElement("option");
    option.value = model;
    option.textContent = model || t("settings.noModels");
    option.selected = model === current;
    elements.settingsModel.append(option);
  }
}

async function loadSettings() {
  elements.settingsError.hidden = true;
  try {
    const settings = await api("/api/settings");
    if (!settings.available) throw new Error(t("settings.unavailable"));
    let models = [];
    try {
      const available = await api("/api/models");
      models = available.models || [];
    } catch {
    }
    fillModelOptions(models, settings.model);
    elements.settingsASR.value = settings.asr || "local";
    updateASRHint(elements.settingsASR.value);
    elements.settingsCorrect.checked = Boolean(settings.correct);
    elements.settingsFields.disabled = false;
  } catch (error) {
    elements.settingsError.textContent = error.message;
    elements.settingsError.hidden = false;
    elements.settingsFields.disabled = true;
  } finally {
    elements.settingsForm.setAttribute("aria-busy", "false");
  }
}

async function submitSettings(event) {
  event.preventDefault();
  elements.settingsError.hidden = true;
  elements.settingsSaved.hidden = true;
  const model = elements.settingsModel.value.trim();
  if (!model) {
    elements.settingsError.textContent = t("settings.modelRequired");
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
    elements.settingsASR.value = settings.asr;
    updateASRHint(settings.asr);
    elements.settingsSaved.hidden = false;
  } catch (error) {
    elements.settingsError.textContent = error.message;
    elements.settingsError.hidden = false;
  } finally {
    elements.settingsSave.disabled = false;
  }
}

function parseLexicon(text) {
  const rules = [];
  const lines = text.split(/\r?\n/);
  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i].trim();
    if (!line || line.startsWith("#")) continue;
    const index = line.indexOf("=");
    if (index < 0) throw new Error(t("lexicon.missingEquals", { line: i + 1 }));
    const from = line.slice(0, index).trim();
    if (!from) throw new Error(t("lexicon.emptySource", { line: i + 1 }));
    rules.push({ from, to: line.slice(index + 1).trim() });
  }
  return rules;
}

async function loadLexicon() {
  elements.lexiconError.hidden = true;
  try {
    const { rules } = await api("/api/lexicon");
    elements.lexiconInput.value = rules.map((rule) => `${rule.from}=${rule.to}`).join("\n");
    elements.lexiconFields.disabled = false;
  } catch (error) {
    elements.lexiconError.textContent = error.message;
    elements.lexiconError.hidden = false;
    elements.lexiconFields.disabled = true;
  } finally {
    elements.lexiconForm.setAttribute("aria-busy", "false");
  }
}

async function submitLexicon(event) {
  event.preventDefault();
  elements.lexiconError.hidden = true;
  elements.lexiconSaved.hidden = true;
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
    elements.lexiconSaved.hidden = false;
  } catch (error) {
    elements.lexiconError.textContent = error.message;
    elements.lexiconError.hidden = false;
  } finally {
    elements.lexiconSave.disabled = false;
  }
}

i18n.subscribe(() => updateASRHint(elements.settingsASR.value));
elements.settingsForm.addEventListener("submit", submitSettings);
elements.settingsForm.addEventListener("input", () => { elements.settingsSaved.hidden = true; });
elements.settingsASR.addEventListener("change", () => updateASRHint(elements.settingsASR.value));
elements.lexiconForm.addEventListener("submit", submitLexicon);
elements.lexiconForm.addEventListener("input", () => { elements.lexiconSaved.hidden = true; });

Promise.all([loadSettings(), loadLexicon()]);
