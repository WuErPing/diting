"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { createI18n, detectLocale, messages } = require("./i18n.js");

test("detectLocale defaults to English", () => {
  assert.equal(detectLocale([]), "en");
  assert.equal(detectLocale(["fr-FR", "en-US"]), "en");
  assert.equal(detectLocale(undefined), "en");
});

test("detectLocale selects Chinese for any preferred Chinese locale", () => {
  assert.equal(detectLocale(["zh-CN"]), "zh");
  assert.equal(detectLocale(["en-US", "zh-Hant-TW"]), "zh");
});

test("saved manual choice overrides browser languages", () => {
  const values = new Map([["diting.locale", "en"]]);
  const storage = {
    getItem: (key) => values.get(key) || null,
    setItem: (key, value) => values.set(key, value),
  };
  const i18n = createI18n({ storage, languages: ["zh-CN"] });
  assert.equal(i18n.locale, "en");
  i18n.setLocale("zh");
  assert.equal(i18n.locale, "zh");
  assert.equal(values.get("diting.locale"), "zh");
});

test("invalid saved choice falls back to automatic detection", () => {
  const storage = { getItem: () => "invalid", setItem: () => {} };
  assert.equal(createI18n({ storage, languages: ["zh-HK"] }).locale, "zh");
  assert.equal(createI18n({ storage, languages: ["en-GB"] }).locale, "en");
});

test("translation falls back to English and interpolates values", () => {
  const i18n = createI18n({ storage: null, languages: ["en-US"] });
  assert.equal(i18n.t("preview.recent", { limit: 500 }), "Last 500 records · Model topics");
  i18n.setLocale("zh");
  assert.equal(i18n.t("preview.recent", { limit: 500 }), "最近 500 条 · 模型议题");
  assert.equal(i18n.t("missing.key"), "missing.key");
});

test("API errors use translated stable error codes", () => {
  const i18n = createI18n({ storage: null, languages: ["en-US"] });
  assert.equal(i18n.apiError({ code: "INVALID_JSON", message: "请求内容不是有效 JSON" }), "The request body is not valid JSON");
  assert.equal(i18n.apiError({ message: "details" }), "details");
  assert.equal(i18n.apiError(null), "Local service request failed");
});

test("stable speaker labels are localized without changing their identity", () => {
  const i18n = createI18n({ storage: null, languages: ["en-US"] });
  assert.equal(i18n.speakerLabel("说话人12"), "Speaker 12");
  assert.equal(i18n.speakerLabel("Andy"), "Andy");
  i18n.setLocale("zh");
  assert.equal(i18n.speakerLabel("说话人12"), "说话人12");
});

test("both locales define exactly the same translation keys", () => {
  assert.deepEqual(Object.keys(messages.zh).sort(), Object.keys(messages.en).sort());
});

test("every static and dynamic translation reference is defined", () => {
  const files = ["index.html", "records.html", "app.js", "records.js"];
  const references = new Set();
  for (const file of files) {
    const source = fs.readFileSync(path.join(__dirname, file), "utf8");
    for (const match of source.matchAll(/data-i18n(?:-[\w-]+)?="([^"]+)"/g)) references.add(match[1]);
    for (const match of source.matchAll(/\bt\("([^"]+)"/g)) references.add(match[1]);
  }
  const missing = [...references].filter((key) => !Object.hasOwn(messages.en, key));
  assert.deepEqual(missing, []);
});
