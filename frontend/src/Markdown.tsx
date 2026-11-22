// The markdown renderer and its plugin stack (react-markdown + remark-gfm +
// rehype-sanitize) are by far the heaviest third-party dependency in the app and
// are only needed on content pages (guide, readers). Keeping it in its own module
// lets App.tsx lazy-load it, so /discover and the console don't pay for it.

import ReactMarkdown from "react-markdown"
import rehypeSanitize from "rehype-sanitize"
import remarkGfm from "remark-gfm"

export default function Markdown({ content }: { content: string }) {
  return (
    <ReactMarkdown rehypePlugins={[rehypeSanitize]} remarkPlugins={[remarkGfm]}>
      {content}
    </ReactMarkdown>
  )
}
