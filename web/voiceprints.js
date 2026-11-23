"use strict";

(function initVoiceprintsModule(root, factory) {
  const moduleApi = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = moduleApi;
    return;
  }
  root.DitingVoiceprints = moduleApi;
})(typeof globalThis !== "undefined" ? globalThis : this, function createModule() {
  function speakerChanges(view, draft) {
    const changes = {};
    const currentAlias = view.name && view.name !== view.label ? view.name : "";
    if (!view.mergedInto && draft.alias !== currentAlias) changes.alias = draft.alias;
    if (draft.mergedInto !== (view.mergedInto || "")) changes.mergedInto = draft.mergedInto;
    return changes;
  }

  return { speakerChanges };
});
