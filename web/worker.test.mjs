import assert from "node:assert/strict";
import { test } from "node:test";
import worker from "./worker.js";

const siteURL = "https://readbackcli.dev";
const sha = "0123456789abcdef0123456789abcdef01234567";

test("the homepage uses the asset binding and keeps its security headers", async () => {
  const request = new Request(siteURL);
  const response = await worker.fetch(request, {
    ASSETS: { async fetch(received) {
      assert.equal(received, request);
      return new Response("<h1>Readback</h1>", { headers: {
        "content-type": "text/html",
        "cache-control": "public, max-age=0, must-revalidate",
        etag: '"asset"',
      } });
    } },
  });
  assert.equal(response.status, 200);
  assert.equal(response.headers.get("location"), null);
  assert.equal(response.headers.get("etag"), '"asset"');
  assert.equal(response.headers.get("cache-control"), "public, max-age=0, must-revalidate, no-transform");
  assert.match(response.headers.get("content-security-policy"), /frame-ancestors 'none'/);
  assert.equal(response.headers.get("x-content-type-options"), "nosniff");
  assert.equal(await response.text(), "<h1>Readback</h1>");
});

test("missing assets keep the binding's 404 instead of redirecting to GitHub", async () => {
  const response = await worker.fetch(new Request(siteURL + "/missing"), {
    ASSETS: { async fetch() { return new Response("Not found", { status: 404 }); } },
  });
  assert.equal(response.status, 404);
  assert.equal(response.headers.get("location"), null);
});

test("write methods never reach the installer or asset binding", async (t) => {
  t.mock.method(globalThis, "fetch", () => { assert.fail("must not fetch upstream"); });
  for (const path of ["/", "/install", "/install.sh", "/healthz"]) {
    const response = await worker.fetch(new Request(siteURL + path, { method: "POST" }), {});
    assert.equal(response.status, 405);
    assert.equal(response.headers.get("allow"), "GET, HEAD");
  }
});

test("health reports only a full Git SHA as commit_sha from version metadata and is not cached", async () => {
  for (const tag of [sha, undefined, "local-preview"]) {
    const response = await worker.fetch(new Request(siteURL + "/healthz"), { VERSION: { tag } });
    assert.equal(response.headers.get("cache-control"), "no-store");
    // commit_sha is the field readback's health rung verifies; any other name reads as a mismatch.
    assert.deepEqual(await response.json(), { status: "ok", commit_sha: tag === sha ? sha : null });
  }
});

test("both install URLs stream the known script with a bounded fetch", async (t) => {
  t.mock.method(globalThis, "fetch", async (url, options) => {
    assert.equal(url, "https://raw.githubusercontent.com/joshduffy/readback/main/scripts/install.sh");
    assert.equal(options.method, "GET");
    assert.ok(options.signal instanceof AbortSignal);
    assert.equal(options.cf.cacheTtl, 300);
    return new Response("#!/bin/sh\necho fixture\n");
  });
  for (const path of ["/install", "/install.sh"]) {
    const response = await worker.fetch(new Request(siteURL + path), {});
    assert.equal(response.status, 200);
    assert.match(response.headers.get("content-type"), /^text\/plain/);
    assert.equal(await response.text(), "#!/bin/sh\necho fixture\n");
  }
});

test("HTTP failures and network errors produce a non-cacheable installer failure", async (t) => {
  const mock = t.mock.method(globalThis, "fetch");
  for (const upstream of [() => new Response("unavailable", { status: 500 }), () => { throw new Error("offline"); }]) {
    mock.mock.mockImplementation(upstream);
    const response = await worker.fetch(new Request(siteURL + "/install"), {});
    assert.equal(response.status, 503);
    assert.equal(response.headers.get("cache-control"), "no-store");
    assert.match(await response.text(), /temporarily unavailable/);
  }
});

test("HEAD returns headers without a body for the site, health, and installer", async (t) => {
  t.mock.method(globalThis, "fetch", async (_url, options) => {
    assert.equal(options.method, "HEAD");
    return new Response(null);
  });
  for (const path of ["/", "/healthz", "/install", "/install.sh"]) {
    const response = await worker.fetch(new Request(siteURL + path, { method: "HEAD" }), {
      VERSION: { tag: sha },
      ASSETS: { async fetch() { return new Response(null); } },
    });
    assert.equal(response.status, 200);
    assert.equal(await response.text(), "");
  }
});
