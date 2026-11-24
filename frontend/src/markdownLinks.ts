// inAppPath returns the SPA route for a Markdown link that should stay inside
// the app, or null for links that must be real navigations: other origins,
// protocol-relative URLs, API routes (/v1/...), and raw files (/mstash.js,
// /skill.md, /llms.txt) that the router doesn't serve.
export function inAppPath(href: string, origin: string): string | null {
  const sameOrigin = href === origin || href.startsWith(origin + "/")
  const path = sameOrigin ? href.slice(origin.length) || "/" : href
  if (!path.startsWith("/") || path.startsWith("//") || path.startsWith("/v1/")) return null
  const pathname = path.split(/[?#]/, 1)[0]
  if (/\.[a-z0-9]+$/i.test(pathname)) return null
  return path
}
