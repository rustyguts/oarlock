// Pure SPA: no SSR, no prerendering of individual routes. adapter-static
// emits a single fallback index.html; the Go binary serves it for any
// non-API path and the client router takes over.
export const ssr = false;
export const prerender = false;
