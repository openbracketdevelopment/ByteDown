import { defineConfig, type ProxyOptions } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
const proxy: ProxyOptions = {
  target: "http://127.0.0.1:8080",
  changeOrigin: true,
  configure(proxy) {
    proxy.on("proxyReq", (outgoing, request) => {
      if (request.headers.origin === `http://${request.headers.host}`)
        outgoing.setHeader("Origin", "http://127.0.0.1:8080");
    });
  },
};
export default defineConfig({
  plugins: [svelte()],
  ...(process.env.VITEST ? { resolve: { conditions: ["browser"] } } : {}),
  server: {
    proxy: Object.fromEntries(
      ["/ui", "/v1", "/media", "/api-docs", "/docs-assets", "/openapi.yaml"].map((path) => [path, proxy]),
    ),
  },
});
