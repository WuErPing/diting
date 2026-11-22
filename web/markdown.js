"use strict";

(function initMarkdownModule(root, factory) {
  const moduleApi = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = moduleApi;
    return;
  }
  root.DitingMarkdown = moduleApi;
})(typeof globalThis !== "undefined" ? globalThis : this, function createModule() {
  function escapeHTML(value) {
    return value.replace(/[&<>"']/g, (character) => ({
      "&": "&amp;",
      "<": "&lt;",
      ">": "&gt;",
      '"': "&quot;",
      "'": "&#39;",
    })[character]);
  }

  function renderInline(source) {
    const escaped = [];
    const protectedSource = source.replace(/\\([\\`*_[\]<>])/g, (_, character) => {
      escaped.push(character);
      return `\u0000${escaped.length - 1}\u0000`;
    });
    let html = escapeHTML(protectedSource);
    html = html.replace(/`([^`]+)`/g, "<code>$1</code>");
    html = html.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
    html = html.replace(/\u0000(\d+)\u0000/g, (_, index) => escapeHTML(escaped[Number(index)]));
    return html;
  }

  function renderMarkdown(source) {
    const lines = source.replace(/\r\n/g, "\n").split("\n");
    let html = "";
    let inList = false;
    let inQuote = false;
    let paragraph = [];

    const flushParagraph = () => {
      if (!paragraph.length) return;
      html += `<p>${paragraph.map(renderInline).join("<br>")}</p>`;
      paragraph = [];
    };
    const closeList = () => {
      if (inList) {
        html += "</ul>";
        inList = false;
      }
    };
    const closeQuote = () => {
      if (inQuote) {
        html += "</blockquote>";
        inQuote = false;
      }
    };

    for (const raw of lines) {
      const line = raw.trimEnd();
      if (!line.trim()) {
        flushParagraph();
        closeList();
        closeQuote();
        continue;
      }
      if (line.startsWith("### ")) {
        flushParagraph(); closeList(); closeQuote();
        html += `<h3>${renderInline(line.slice(4))}</h3>`;
        continue;
      }
      if (line.startsWith("## ")) {
        flushParagraph(); closeList(); closeQuote();
        html += `<h2>${renderInline(line.slice(3))}</h2>`;
        continue;
      }
      if (line.startsWith("# ")) {
        flushParagraph(); closeList(); closeQuote();
        html += `<h1>${renderInline(line.slice(2))}</h1>`;
        continue;
      }
      if (line.startsWith("> ")) {
        flushParagraph(); closeList();
        if (!inQuote) {
          html += "<blockquote>";
          inQuote = true;
        }
        html += `<p>${renderInline(line.slice(2))}</p>`;
        continue;
      }
      if (line.startsWith("- ")) {
        flushParagraph(); closeQuote();
        if (!inList) {
          html += "<ul>";
          inList = true;
        }
        html += `<li>${renderInline(line.slice(2))}</li>`;
        continue;
      }
      if (/^---+$/.test(line.trim())) {
        flushParagraph(); closeList(); closeQuote();
        html += "<hr>";
        continue;
      }
      closeList();
      closeQuote();
      paragraph.push(line);
    }

    flushParagraph();
    closeList();
    closeQuote();
    return html;
  }

  return { renderInline, renderMarkdown };
});
