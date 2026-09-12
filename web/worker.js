// readbackcli.dev: the install script and a pointer to the repo. No data, no logs.
const REPO = "https://github.com/joshduffy/readback";
const RAW_INSTALL = "https://raw.githubusercontent.com/joshduffy/readback/main/scripts/install.sh";

export default {
  async fetch(request) {
    const url = new URL(request.url);
    if (url.pathname === "/install" || url.pathname === "/install.sh") {
      const upstream = await fetch(RAW_INSTALL, { cf: { cacheTtl: 300 } });
      if (!upstream.ok) {
        return new Response("readback v0.1 is not released yet; check " + REPO + "\n", {
          status: 503,
          headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
        });
      }
      return new Response(upstream.body, {
        status: 200,
        headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "public, max-age=300" },
      });
    }
    if (url.pathname === "/healthz") {
      return new Response(JSON.stringify({ status: "ok" }), { headers: { "content-type": "application/json" } });
    }
    return Response.redirect(REPO, 302);
  },
};
