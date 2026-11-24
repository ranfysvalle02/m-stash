// A single, passive pointer listener that tells the card under the cursor where
// the pointer is (--mx / --my, in px), so CSS can light it from that spot. One
// listener for the whole app, throttled to animation frames, and skipped on
// touch-only devices where there is no hover to light.

export const SPOTLIGHT_SELECTOR = [
  ".recipe-card",
  ".recipe-chip",
  ".tutorial-card",
  ".console-app-card",
  ".vibe-keep-card",
  ".vibe-owner-card",
  ".console-claim",
  ".stash-stats",
].join(", ")

export function installSpotlight(root: Document = document): () => void {
  if (typeof window === "undefined" || window.matchMedia?.("(hover: none)").matches) return () => {}
  let frame = 0
  let pending: PointerEvent | null = null

  function paint() {
    frame = 0
    const event = pending
    pending = null
    const target = event?.target instanceof Element ? event.target.closest<HTMLElement>(SPOTLIGHT_SELECTOR) : null
    if (!event || !target) return
    const rect = target.getBoundingClientRect()
    target.style.setProperty("--mx", `${Math.round(event.clientX - rect.left)}px`)
    target.style.setProperty("--my", `${Math.round(event.clientY - rect.top)}px`)
  }

  function onMove(event: PointerEvent) {
    pending = event
    if (!frame) frame = requestAnimationFrame(paint)
  }

  root.addEventListener("pointermove", onMove, { passive: true })
  return () => {
    root.removeEventListener("pointermove", onMove)
    if (frame) cancelAnimationFrame(frame)
  }
}
