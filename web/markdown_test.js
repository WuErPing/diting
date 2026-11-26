"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { transcriptParagraphHidden } = require("./markdown.js");

test("filler-only paragraphs are hidden by default and can be restored", () => {
  const filler = { fillerOnly: true };
  assert.equal(transcriptParagraphHidden(filler, false), true);
  assert.equal(transcriptParagraphHidden(filler, true), false);
});

test("substantive and non-topic paragraphs stay visible", () => {
  for (const paragraph of [
    { fillerOnly: false },
    { fillerOnly: false, hasTopic: false },
    { hasTopic: false },
  ]) {
    assert.equal(transcriptParagraphHidden(paragraph, false), false);
  }
});
