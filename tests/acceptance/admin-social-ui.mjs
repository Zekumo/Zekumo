import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";

function classList() {
  const values = new Set();
  return {
    add: (...names) => names.forEach((name) => values.add(name)),
    remove: (...names) => names.forEach((name) => values.delete(name)),
    contains: (name) => values.has(name),
  };
}

const elements = new Map();
const makeElement = (id) => ({
  id,
  classList: classList(),
  textContent: "",
  disabled: false,
  isConnected: true,
  setAttribute(name, value) { this[name] = value; },
  removeAttribute(name) { delete this[name]; },
  querySelector() { return null; },
});

const host = makeElement("dialogHost");
Object.defineProperty(host, "innerHTML", {
  get() { return this._html || ""; },
  set(value) {
    this._html = value;
    for (const id of ["dlgConfirm", "dlgCancel"]) {
      if (elements.has(id)) elements.get(id).isConnected = false;
      elements.delete(id);
    }
    if (value.includes('id="dlgConfirm"')) {
      const confirm = makeElement("dlgConfirm");
      confirm.textContent = value.match(/id="dlgConfirm"[^>]*>([^<]*)</)?.[1] || "确定";
      elements.set("dlgConfirm", confirm);
      elements.set("dlgCancel", makeElement("dlgCancel"));
    }
  },
});
elements.set("dialogHost", host);
elements.set("scrim", makeElement("scrim"));
elements.set("toast", makeElement("toast"));
elements.set("navRail", makeElement("navRail"));
elements.set("gameSwitcher", makeElement("gameSwitcher"));
elements.set("page", makeElement("page"));

const document = {
  getElementById: (id) => elements.get(id) || null,
  createElement: () => {
    let value = "";
    return {
      set textContent(text) { value = String(text ?? ""); },
      get innerHTML() {
        return value.replaceAll("&", "&amp;").replaceAll("<", "&lt;")
          .replaceAll(">", "&gt;").replaceAll('"', "&quot;").replaceAll("'", "&#39;");
      },
    };
  },
};

const window = {addEventListener() {}};
const location = {hash: "#/g/11111111-1111-1111-1111-111111111111/achievements"};
const context = vm.createContext({
  document,
  window,
  location,
  localStorage: {getItem: () => "", setItem() {}, removeItem() {}},
  setTimeout: () => 1,
  clearTimeout() {},
  AbortController,
  console,
});
vm.runInContext(fs.readFileSync("web/admin/core.js", "utf8"), context);
vm.runInContext(fs.readFileSync("web/admin/page-announcements.js", "utf8"), context);
vm.runInContext(fs.readFileSync("web/admin/page-achievements.js", "utf8"), context);
vm.runInContext(fs.readFileSync("web/admin/nav.js", "utf8"), context);

let calls = 0;
let release;
const pending = new Promise((resolve) => { release = resolve; });
context.openDialog({
  title: "发布公告",
  body: "",
  confirmText: "发布",
  onConfirm: async () => { calls += 1; await pending; },
});
const submit = elements.get("dlgConfirm");
const first = submit.onclick();
const repeated = submit.onclick();
assert.equal(calls, 1, "repeated clicks must trigger one mutation");
assert.equal(submit.disabled, true, "submit stays disabled while the request is pending");
assert.equal(elements.get("dlgCancel").disabled, true, "cancel is disabled while commit outcome is unknown");
release();
await Promise.all([first, repeated]);
assert.equal(host.innerHTML, "", "successful confirmation closes the dialog");

context.openDialog({
  title: "发布公告",
  body: "",
  confirmText: "发布",
  onConfirm: async () => { throw new Error("network down"); },
});
const failed = elements.get("dlgConfirm");
await failed.onclick();
assert.equal(failed.disabled, false, "failed confirmation can be retried");
assert.equal(failed.textContent, "发布", "failed confirmation restores its label");
assert.equal(elements.get("toast").textContent, "network down");

context.fetch = async () => { throw new Error("offline"); };
vm.runInContext(`state.games = [{
  id: "11111111-1111-1111-1111-111111111111",
  name: "Fixture game"
}]`, context);
context.openDialog({title: "编辑", body: "", onConfirm: async () => {}});
await context.route();
assert.equal(host.innerHTML, "", "route/back navigation dismisses the old modal");
assert.match(elements.get("page").innerHTML, /加载失败/);
assert.match(elements.get("page").innerHTML, />重试<\//, "load failures expose a retry action");

const markdown = context.markdownPreview(
  "# 更新内容\n- **好友榜**\n- `scope=friends`\n[文档](https://example.com/a?x=1&y=2)\n<img src=x onerror=alert(1)>\n[bad](javascript:alert(1))",
);
assert.match(markdown, /<h1>更新内容<\/h1>/);
assert.match(markdown, /<ul>/);
assert.match(markdown, /href="https:\/\/example\.com\/a\?x=1&amp;y=2"/);
assert.doesNotMatch(markdown, /<img/);
assert.doesNotMatch(markdown, /href="javascript:/);

console.log("Admin social UI acceptance OK: double-submit, navigation retry, and safe Markdown preview");
