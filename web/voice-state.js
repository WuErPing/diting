"use strict";

(function initVoiceStateModule(root, factory) {
  const moduleApi = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = moduleApi;
    return;
  }
  root.DitingVoiceState = moduleApi;
})(typeof globalThis !== "undefined" ? globalThis : this, function createModule() {
  function voicePresentation(captureState, speaking) {
    if (captureState !== "RUNNING") {
      return { hidden: true, mode: "idle", labelKey: null };
    }
    return {
      hidden: false,
      mode: speaking ? "speaking" : "waiting",
      labelKey: speaking ? "voice.speaking" : "voice.waiting",
    };
  }

  return { voicePresentation };
});
