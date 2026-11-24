// The markdown renderer and its plugin stack (react-markdown + remark-gfm +
// rehype-sanitize) are by far the heaviest third-party dependency in the app and
// are only needed on content pages (guide, readers). Keeping it in its own module
// lets App.tsx lazy-load it, so /discover and the console don't pay for it.

import { useRef, useState, type ComponentPropsWithoutRef } from "react"
import ReactMarkdown, { type Components } from "react-markdown"
import { Link } from "react-router-dom"
import rehypeSanitize from "rehype-sanitize"
import remarkGfm from "remark-gfm"

// CopyablePre adds a copy button to a code block. It copies the rendered text,
// so what lands on the clipboard is exactly what the reader sees.
function CopyablePre(props: ComponentPropsWithoutRef<"pre">) {
  const ref = useRef<HTMLPreElement>(null)
  const [copied, setCopied] = useState(false)
  async function copy() {
    try {
      await navigator.clipboard.writeText(ref.current?.innerText ?? "")
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      setCopied(false)
    }
  }
  return (
    <div className="markdown-code">
      <button className="markdown-copy" onClick={() => void copy()} type="button">
        {copied ? "Copied" : "Copy"}
      </button>
      <pre ref={ref} {...props} />
    </div>
  )
}

// SiteLink keeps in-app links (/tutorials/...) inside the single-page app and
// opens everything else in a new tab.
function SiteLink({ href = "", children, ...rest }: ComponentPropsWithoutRef<"a">) {
  const origin = window.location.origin
  const sameOrigin = href === origin || href.startsWith(origin + "/")
  const path = sameOrigin ? href.slice(origin.length) || "/" : href
  // API and raw-file URLs (/v1/..., /mstash.js, /skill.md, /llms.txt) are not
  // app routes, so they must be real navigations.
  const isAppRoute = path.startsWith("/") && !path.startsWith("//") && !path.startsWith("/v1/") && !/\.[a-z]+$/i.test(path)
  if (isAppRoute) {
    return <Link to={path}>{children}</Link>
  }
  return (
    <a href={href} rel="noreferrer" target={href.startsWith("#") ? undefined : "_blank"} {...rest}>
      {children}
    </a>
  )
}

const interactiveComponents: Components = { pre: CopyablePre, a: SiteLink }

export default function Markdown({ content, interactive = false }: { content: string; interactive?: boolean }) {
  return (
    <ReactMarkdown
      components={interactive ? interactiveComponents : undefined}
      rehypePlugins={[rehypeSanitize]}
      remarkPlugins={[remarkGfm]}
    >
      {content}
    </ReactMarkdown>
  )
}
