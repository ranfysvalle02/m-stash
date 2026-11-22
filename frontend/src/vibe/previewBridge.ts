import { useCallback, useEffect, useRef, useState } from "react";
import type { RefObject } from "react";

// The persona switcher and API wiretap talk to the sandboxed preview iframe over
// postMessage. The iframe runs with an opaque origin (no allow-same-origin), so:
//   - inbound messages are trusted by matching event.source to the iframe window
//     (never by origin, which is the string "null"); and
//   - outbound persona switches target "*" because the opaque origin can't be
//     named. Messages NEVER carry tokens, passwords, or request bodies.

export type VibePersona = "visitor" | "customerA" | "customerB" | "owner";

export interface VibeApiEvent {
  id: number;
  method: string;
  path: string;
  status: number;
  durationMs: number;
  persona: string;
  scope: string;
}

export interface VibeHandles {
  a: string | null;
  b: string | null;
  owner: string | null;
}

interface VibeBridge {
  persona: VibePersona;
  setPersona: (persona: VibePersona) => void;
  events: VibeApiEvent[];
  handles: VibeHandles;
}

export function useVibePreviewBridge(
  iframeRef: RefObject<HTMLIFrameElement | null>,
  enabled: boolean,
): VibeBridge {
  const [persona, setPersonaState] = useState<VibePersona>("visitor");
  const [events, setEvents] = useState<VibeApiEvent[]>([]);
  const [handles, setHandles] = useState<VibeHandles>({ a: null, b: null, owner: null });
  const counter = useRef(0);

  useEffect(() => {
    if (!enabled) return;
    function onMessage(event: MessageEvent) {
      const frame = iframeRef.current;
      if (!frame || event.source !== frame.contentWindow) return;
      const data = event.data as Record<string, unknown> | null;
      if (!data || typeof data !== "object") return;
      if (data.type === "m-stash-vibe:api") {
        const next: VibeApiEvent = {
          id: counter.current++,
          method: String(data.method ?? "GET"),
          path: String(data.path ?? ""),
          status: Number(data.status) || 0,
          durationMs: Number(data.durationMs) || 0,
          persona: String(data.persona ?? ""),
          scope: String(data.scope ?? ""),
        };
        setEvents((prev) => [...prev.slice(-11), next]);
      } else if (data.type === "m-stash-vibe:state") {
        if (typeof data.persona === "string") setPersonaState(data.persona as VibePersona);
        const incoming = data.handles as Partial<VibeHandles> | undefined;
        if (incoming) {
          setHandles({
            a: incoming.a ?? null,
            b: incoming.b ?? null,
            owner: incoming.owner ?? null,
          });
        }
      }
    }
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [enabled, iframeRef]);

  const setPersona = useCallback(
    (next: VibePersona) => {
      setPersonaState(next);
      const target = iframeRef.current?.contentWindow;
      if (target) target.postMessage({ type: "m-stash-vibe:persona", persona: next }, "*");
    },
    [iframeRef],
  );

  return { persona, setPersona, events, handles };
}
