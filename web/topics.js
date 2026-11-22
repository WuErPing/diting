"use strict";

(function initTopicsModule(root, factory) {
  const moduleApi = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = moduleApi;
    return;
  }
  root.DitingTopics = moduleApi;
})(typeof globalThis !== "undefined" ? globalThis : this, function createModule() {
  function groupTopics(paragraphs, fallbackCategory) {
    const groups = [];
    const byCategory = new Map();
    for (const paragraph of paragraphs) {
      if (paragraph.hasTopic === false) continue;
      const category = (paragraph.category || "").trim() || fallbackCategory;
      let group = byCategory.get(category);
      if (!group) {
        group = { category, topics: [] };
        byCategory.set(category, group);
        groups.push(group);
      }
      group.topics.push(paragraph);
    }
    return groups;
  }

  return { groupTopics };
});
