"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { speakerChanges } = require("./voiceprints.js");

test("unchanged speaker drafts do not issue updates", () => {
  assert.deepEqual(speakerChanges({ label: "说话人1", name: "说话人1" }, { alias: "", mergedInto: "" }), {});
  assert.deepEqual(speakerChanges({ label: "说话人2", name: "Andy", mergedInto: "说话人1" }, { alias: "Andy", mergedInto: "说话人1" }), {});
});

test("speaker drafts update only the changed alias or merge", () => {
  assert.deepEqual(speakerChanges({ label: "说话人1", name: "说话人1" }, { alias: "Andy", mergedInto: "" }), { alias: "Andy" });
  assert.deepEqual(speakerChanges({ label: "说话人2", name: "说话人2" }, { alias: "", mergedInto: "说话人1" }), { mergedInto: "说话人1" });
  assert.deepEqual(speakerChanges({ label: "说话人2", name: "Andy" }, { alias: "", mergedInto: "" }), { alias: "" });
});

test("merged speakers can unmerge without overwriting the root alias", () => {
  assert.deepEqual(
    speakerChanges({ label: "说话人2", name: "Andy", mergedInto: "说话人1" }, { alias: "Andy", mergedInto: "" }),
    { mergedInto: "" },
  );
});
