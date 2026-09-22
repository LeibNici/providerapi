export function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n % 1_000_000 === 0 ? 0 : 1)}M`;
  if (n >= 1_000) return `${Math.round(n / 1000)}K`;
  return String(n);
}

export function formatContext(length: number, source: string): string {
  return `${formatTokens(length)} · ${source}`;
}

export function formatDuration(ms: number): string {
  if (ms <= 0) return "—";
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
}

export function formatTime(ms: number): string {
  if (!ms) return "—";
  return new Date(ms).toLocaleString();
}

export function statusClass(code: number): string {
  if (code >= 500) return "s5";
  if (code >= 400) return "s4";
  if (code >= 200) return "s2";
  return "";
}

export function timelineLabel(type: string): string {
  const map: Record<string, string> = {
    client_raw: "Client Request",
    normalized: "Normalized",
    plugin_request: "Plugin Request",
    provider_request: "Provider Request",
    provider_response: "Provider Response",
    first_chunk: "First Token",
    stream_started: "Stream Started",
    stream_finished: "Stream Finished",
    tool_call: "Tool Call",
    routing: "Routing",
    usage: "Usage",
    finish: "Completed",
    client_response: "Client Response",
  };
  return map[type] ?? type;
}
