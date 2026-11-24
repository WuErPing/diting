"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { voicePresentation } = require("./voice-state.js");

test("voice indicator and capture button share a compact masthead block", () => {
  const index = fs.readFileSync(path.join(__dirname, "index.html"), "utf8");
  const app = fs.readFileSync(path.join(__dirname, "app.js"), "utf8");
  const panel = index.match(/<section class="listener-panel"[^>]*>([\s\S]*?)<\/section>/);
  assert.ok(panel);
  assert.match(panel[1], /id="voiceState"/);
  assert.match(panel[1], /<button class="icon-button listener-toggle" id="toggleButton"/);
  assert.match(panel[1], /data-i18n-aria-label="action.startListening"/);
  assert.match(panel[1], /<svg[^>]*aria-hidden="true"/);
  assert.doesNotMatch(panel[1], /id="(?:statusDot|stateLabel|listenerTitle|stateHint)"/);
  assert.doesNotMatch(app, /elements\.(?:stateLabel|listenerTitle|stateHint)/);
  assert.doesNotMatch(app, /toggleButton\.textContent/);
});

test("voice indicator stays hidden whenever capture is not running", () => {
  for (const state of ["STOPPED", "STOPPING"]) {
    for (const speaking of [false, true]) {
      assert.deepEqual(voicePresentation(state, speaking), { hidden: true, mode: "idle", labelKey: null });
    }
  }
});

test("running capture waits for speech with a quiet animation", () => {
  assert.deepEqual(voicePresentation("RUNNING", false), {
    hidden: false,
    mode: "waiting",
    labelKey: "voice.waiting",
  });
});

test("interim speech switches the brand indicator to the speaking animation", () => {
  assert.deepEqual(voicePresentation("RUNNING", true), {
    hidden: false,
    mode: "speaking",
    labelKey: "voice.speaking",
  });
});
