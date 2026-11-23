"use strict";

(function initTopicsModule(root, factory) {
  const moduleApi = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = moduleApi;
    return;
  }
  root.DitingTopics = moduleApi;
})(typeof globalThis !== "undefined" ? globalThis : this, function createModule() {
  const maxSilenceMs = 10 * 60 * 1000;

  function groupTopics(paragraphs, fallbackCategory) {
    const groups = [];
    const byPeriodAndCategory = new Map();
    let period = 0;
    let previousEndedAt = "";
    for (const paragraph of paragraphs) {
      const start = Date.parse(paragraph.startedAt || "");
      const previousEnd = Date.parse(previousEndedAt);
      if (Number.isFinite(start) && Number.isFinite(previousEnd) && start - previousEnd >= maxSilenceMs) {
        period += 1;
      }
      previousEndedAt = paragraph.endedAt || paragraph.startedAt || previousEndedAt;
      if (paragraph.hasTopic === false) continue;
      const category = (paragraph.category || "").trim() || fallbackCategory;
      const key = `${period}|${category}`;
      let group = byPeriodAndCategory.get(key);
      if (!group) {
        group = {
          key,
          category,
          startedAt: paragraph.startedAt,
          endedAt: paragraph.endedAt,
          topics: [],
        };
        byPeriodAndCategory.set(key, group);
        groups.push(group);
      }
      group.endedAt = paragraph.endedAt || group.endedAt;
      group.topics.push(paragraph);
    }
    return groups;
  }

  function topicGroupOpen(key, isLatest, collapsed, expanded) {
    if (collapsed.has(key)) return false;
    return isLatest || expanded.has(key);
  }

  return { groupTopics, topicGroupOpen };
});
