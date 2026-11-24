"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { groupTopics, topicGroupOpen } = require("./topics.js");
const { renderInline, renderMarkdown } = require("./markdown.js");

test("topics form an ordered two-level tree and exclude low-value paragraphs", () => {
  const paragraphs = [
    { id: "a", category: "产品交付", title: "发布安排", hasTopic: true },
    { id: "b", category: "产品交付", title: "验收标准" },
    { id: "ignored", category: "产品交付", hasTopic: false },
    { id: "c", category: " 团队协作 ", title: "责任分工" },
    { id: "d", category: "产品交付", title: "灰度计划" },
  ];

  const groups = groupTopics(paragraphs, "未分类");

  assert.deepEqual(groups.map((group) => ({
    category: group.category,
    ids: group.topics.map((topic) => topic.id),
  })), [
    { category: "产品交付", ids: ["a", "b", "d"] },
    { category: "团队协作", ids: ["c"] },
  ]);
});

test("same-category topics stay in separate large time periods", () => {
  const groups = groupTopics([
    { id: "a", category: "薪酬绩效", startedAt: "2026-11-22T16:00:00+08:00", endedAt: "2026-11-22T16:02:00+08:00" },
    { id: "b", category: "薪酬绩效", startedAt: "2026-11-22T16:16:59+08:00", endedAt: "2026-11-22T16:17:30+08:00" },
    { id: "c", category: "薪酬绩效", startedAt: "2026-11-22T16:32:30+08:00", endedAt: "2026-11-22T16:33:00+08:00" },
  ], "其他主题");

  assert.deepEqual(groups.map((group) => ({
    key: group.key,
    ids: group.topics.map((topic) => topic.id),
    startedAt: group.startedAt,
    endedAt: group.endedAt,
  })), [
    { key: "0|薪酬绩效", ids: ["a", "b"], startedAt: "2026-11-22T16:00:00+08:00", endedAt: "2026-11-22T16:17:30+08:00" },
    { key: "1|薪酬绩效", ids: ["c"], startedAt: "2026-11-22T16:32:30+08:00", endedAt: "2026-11-22T16:33:00+08:00" },
  ]);
});

test("only the latest category opens automatically while manual choices win", () => {
  const collapsed = new Set(["latest"]);
  const expanded = new Set(["older"]);

  assert.equal(topicGroupOpen("older", false, collapsed, expanded), true);
  assert.equal(topicGroupOpen("latest", true, collapsed, expanded), false);
  assert.equal(topicGroupOpen("other", false, collapsed, expanded), false);
  assert.equal(topicGroupOpen("newest", true, collapsed, expanded), true);
});

test("legacy topics without a category use the localized fallback", () => {
  const groups = groupTopics([
    { id: "a", title: "发布安排" },
    { id: "b", category: "  ", title: "验收标准" },
  ], "其他主题");

  assert.equal(groups.length, 1);
  assert.equal(groups[0].category, "其他主题");
  assert.deepEqual(groups[0].topics.map((topic) => topic.id), ["a", "b"]);
});

test("inline Markdown emphasizes concise summaries without allowing raw HTML", () => {
  assert.equal(
    renderInline("确定 **周五** 发布 `v1` <script>alert(1)</script>"),
    "确定 <strong>周五</strong> 发布 <code>v1</code> &lt;script&gt;alert(1)&lt;/script&gt;",
  );
});

test("block Markdown remains available to the existing transcript preview", () => {
  assert.equal(
    renderMarkdown("## 结论\n\n- **按时发布**\n- 继续观察"),
    "<h2>结论</h2><ul><li><strong>按时发布</strong></li><li>继续观察</li></ul>",
  );
});
