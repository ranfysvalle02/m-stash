import { useEffect, useRef } from "react";
import { Activity, ShieldCheck, User, Users } from "lucide-react";
import type { VibeApiEvent, VibeHandles, VibePersona } from "./previewBridge";

const PERSONAS: { id: VibePersona; label: string; fallback: string }[] = [
  { id: "visitor", label: "Public visitor", fallback: "No login" },
  { id: "customerA", label: "Customer A", fallback: "Alex" },
  { id: "customerB", label: "Customer B", fallback: "Sam" },
  { id: "owner", label: "App owner", fallback: "Admin" },
];

function personaDetail(id: VibePersona, handles: VibeHandles) {
  if (id === "customerA") return handles.a ?? "Alex";
  if (id === "customerB") return handles.b ?? "Sam";
  if (id === "owner") return handles.owner ?? "Admin";
  return "No login";
}

function personaGlyph(id: VibePersona) {
  if (id === "owner") return <ShieldCheck aria-hidden size={15} />;
  if (id === "visitor") return <Users aria-hidden size={15} />;
  return <User aria-hidden size={15} />;
}

export function PersonaBar({
  persona,
  onSelect,
  handles,
}: {
  persona: VibePersona;
  onSelect: (persona: VibePersona) => void;
  handles: VibeHandles;
}) {
  return (
    <div className="persona-bar">
      <div className="persona-pills" role="group" aria-label="Preview the live app as">
        {PERSONAS.map((option) => (
          <button
            aria-pressed={persona === option.id}
            className={`persona-pill${persona === option.id ? " active" : ""}`}
            key={option.id}
            onClick={() => onSelect(option.id)}
            type="button"
          >
            <span className="persona-glyph">{personaGlyph(option.id)}</span>
            <span className="persona-text">
              <span className="persona-label">{option.label}</span>
              <span className="persona-sub">{personaDetail(option.id, handles)}</span>
            </span>
          </button>
        ))}
      </div>
      <p className="persona-note">
        {persona === "customerA" || persona === "customerB"
          ? "You're acting as one signed-in customer. Switch to the other and their private records vanish — the server scopes every /v1/me call to the JWT it receives."
          : persona === "owner"
            ? "The owner writes the shared data every visitor can read. Only this admin identity can change it."
            : "This is the public view — no login. Switch to a customer to watch private, isolated data appear."}
      </p>
    </div>
  );
}

function statusClass(status: number) {
  if (status === 0) return "err";
  if (status >= 500) return "err";
  if (status >= 400) return "warn";
  return "ok";
}

export function Wiretap({ events }: { events: VibeApiEvent[] }) {
  const logRef = useRef<HTMLUListElement | null>(null);
  useEffect(() => {
    const node = logRef.current;
    if (node) node.scrollTop = node.scrollHeight;
  }, [events]);

  return (
    <div className="vibe-wiretap">
      <div className="wiretap-head">
        <Activity aria-hidden size={14} /> Live API wiretap
      </div>
      {events.length === 0 ? (
        <p className="wiretap-empty">
          Click around the preview — every authenticated REST call to your sandbox shows up here.
        </p>
      ) : (
        <ul className="wiretap-log" ref={logRef}>
          {events.map((event) => (
            <li className={`wiretap-row is-${statusClass(event.status)}`} key={event.id}>
              <span className="wiretap-method">{event.method}</span>
              <span className="wiretap-path" title={event.path}>
                {event.path}
              </span>
              <span className="wiretap-status">{event.status || "—"}</span>
              <span className="wiretap-ms">{event.durationMs}ms</span>
              <span className="wiretap-persona" title={event.persona}>
                {event.persona}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
