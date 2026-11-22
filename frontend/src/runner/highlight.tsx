// A tiny, dependency-free syntax highlighter for the three snippet shapes the
// runner shows: JSON payloads/responses, curl commands, and fetch() TypeScript.
// It tokenizes with a single regex per shape and maps tokens to classed spans —
// no runtime dependency, no build weight, just a crisp, readable code surface.

import type { ReactElement } from "react"
import "./highlight.css"

export type CodeLang = "json" | "curl" | "fetch"

interface Token {
  text: string
  cls: string
}

// --- JSON ----------------------------------------------------------------

function jsonTokens(code: string): Token[] {
  const tokens: Token[] = []
  const re = /"(?:\\.|[^"\\])*"|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|\btrue\b|\bfalse\b|\bnull\b|[{}[\],:]|\s+|[^\s{}[\],:"]+/g
  let match: RegExpExecArray | null
  while ((match = re.exec(code)) !== null) {
    const text = match[0]
    let cls = "tok-plain"
    if (text.startsWith('"')) {
      // A string is a key when the next non-space character is a colon.
      const rest = code.slice(re.lastIndex).replace(/^\s*/, "")
      cls = rest.startsWith(":") ? "tok-key" : "tok-str"
    } else if (/^-?\d/.test(text)) {
      cls = "tok-num"
    } else if (text === "true" || text === "false" || text === "null") {
      cls = "tok-bool"
    } else if (/^[{}[\],:]$/.test(text)) {
      cls = "tok-punc"
    } else if (/^\s+$/.test(text)) {
      cls = "tok-ws"
    }
    tokens.push({ text, cls })
  }
  return tokens
}

// --- Generic (curl + fetch) ----------------------------------------------

interface LangSpec {
  comment: "//" | "#" | null
  keywords: Set<string>
}

const curlSpec: LangSpec = {
  comment: "#",
  keywords: new Set(["curl", "npx", "npm", "cd", "export"]),
}

const fetchSpec: LangSpec = {
  comment: "//",
  keywords: new Set([
    "const", "let", "await", "async", "return", "new", "function",
    "export", "try", "catch", "throw", "if", "else", "for", "of", "true", "false", "null",
  ]),
}

function genericTokens(code: string, spec: LangSpec): Token[] {
  const commentAlt = spec.comment === "//" ? "\\/\\/[^\\n]*" : spec.comment === "#" ? "#[^\\n]*" : null
  const parts = [
    "https?:\\/\\/[^\\s\"'`]+", // url (before comment so https:// isn't a comment)
    ...(commentAlt ? [commentAlt] : []),
    "`(?:\\\\.|[^`\\\\])*`", // template string
    "'(?:\\\\.|[^'\\\\])*'", // single-quoted string
    '"(?:\\\\.|[^"\\\\])*"', // double-quoted string
    "\\$[A-Za-z_]+", // $VAR
    "-{1,2}[A-Za-z][\\w-]*", // -X / --header
    "[A-Za-z_$][\\w$]*", // identifier
    "\\d+(?:\\.\\d+)?", // number
    "\\s+", // whitespace
    "[^\\s]", // any other single char (punctuation)
  ]
  const re = new RegExp(parts.join("|"), "g")
  const tokens: Token[] = []
  let match: RegExpExecArray | null
  while ((match = re.exec(code)) !== null) {
    const text = match[0]
    let cls = "tok-plain"
    if (/^https?:\/\//.test(text)) {
      cls = "tok-url"
    } else if ((spec.comment === "//" && text.startsWith("//")) || (spec.comment === "#" && text.startsWith("#"))) {
      cls = "tok-comment"
    } else if (text.startsWith("`") || text.startsWith("'") || text.startsWith('"')) {
      cls = "tok-str"
    } else if (text.startsWith("$")) {
      cls = "tok-var"
    } else if (/^-/.test(text)) {
      cls = "tok-flag"
    } else if (spec.keywords.has(text)) {
      cls = "tok-key"
    } else if (/^\d/.test(text)) {
      cls = "tok-num"
    } else if (/^\s+$/.test(text)) {
      cls = "tok-ws"
    }
    tokens.push({ text, cls })
  }
  return tokens
}

function tokenize(code: string, lang: CodeLang): Token[] {
  if (lang === "json") return jsonTokens(code)
  return genericTokens(code, lang === "curl" ? curlSpec : fetchSpec)
}

// Highlighted renders a syntax-colored <code> block for one of the runner's
// snippet shapes. Wrap it yourself in <pre> for block layout.
export function Highlighted({ code, lang, className }: { code: string; lang: CodeLang; className?: string }): ReactElement {
  const tokens = tokenize(code, lang)
  return (
    <code className={className}>
      {tokens.map((token, index) => (
        <span key={index} className={token.cls}>
          {token.text}
        </span>
      ))}
    </code>
  )
}
