const REPO = "https://github.com/joshduffy/readback";
const RAW_INSTALL = "https://raw.githubusercontent.com/joshduffy/readback/main/scripts/install.sh";

const SECURITY_HEADERS = {
  "content-security-policy": "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'none'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'",
  "referrer-policy": "no-referrer",
  "x-content-type-options": "nosniff",
  "permissions-policy": "camera=(), microphone=(), geolocation=()",
};

export default {
  async fetch(request, env) {
    let response;
    const url = new URL(request.url);

    if (request.method !== "GET" && request.method !== "HEAD") {
      response = new Response("Use GET or HEAD.\n", {
        status: 405,
        headers: { allow: "GET, HEAD", "content-type": "text/plain; charset=utf-8" },
      });
    } else if (url.pathname === "/install" || url.pathname === "/install.sh") {
      response = await installScript(request.method);
    } else if (url.pathname === "/healthz") {
      const tag = env.VERSION?.tag;
      response = Response.json({
        status: "ok",
        sha: /^[a-f0-9]{40}$/.test(tag ?? "") ? tag : null,
      }, { headers: { "cache-control": "no-store" } });
    } else {
      response = await env.ASSETS.fetch(request);
    }

    const headers = new Headers(response.headers);
    for (const [name, value] of Object.entries(SECURITY_HEADERS)) {
      headers.set(name, value);
    }
    return new Response(request.method === "HEAD" ? null : response.body, {
      status: response.status,
      headers,
    });
  },
};

async function installScript(method) {
  try {
    const upstream = await fetch(RAW_INSTALL, {
      method,
      signal: AbortSignal.timeout(8000),
      cf: { cacheTtl: 300 },
    });
    if (upstream.ok) {
      return new Response(upstream.body, {
        headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "public, max-age=300" },
      });
    }
    await upstream.body?.cancel();
  } catch {
    // The installer must fail clearly when GitHub is unavailable.
  }
  return new Response("The install script is temporarily unavailable; see " + REPO + "\n", {
    status: 503,
    headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
  });
}
