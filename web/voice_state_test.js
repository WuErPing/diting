"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { voicePresentation } = require("./voice-state.js");

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
