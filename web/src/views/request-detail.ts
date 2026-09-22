import { fetchRequestDetail, type RequestDetail } from "../api";
import { formatDuration, formatTime, statusClass, timelineLabel } from "../format";

export function createRequestDetailView(id: string, onBack: () => void): HTMLElement {
  const root = document.createElement("div");
  root.className = "request-detail-view";

  const back = document.createElement("button");
  back.className = "btn";
  back.textContent = "← Requests";
  back.addEventListener("click", onBack);
  root.append(back);

  const content = document.createElement("div");
  root.append(content);

  fetchRequestDetail(id)
    .then((detail) => renderDetail(content, detail))
    .catch((err) => {
      content.textContent = err instanceof Error ? err.message : "Failed to load request";
    });

  return root;
}

function renderDetail(container: HTMLElement, detail: RequestDetail): void {
  container.replaceChildren();

  const header = document.createElement("dl");
  header.className = "detail-header panel";
  const code = detail.http_status || 0;
  const fields: [string, string][] = [
    ["ID", detail.request_id],
    ["HTTP", String(code)],
    ["Started", formatTime(detail.started_at)],
    ["Alias", detail.client?.model ?? ""],
    ["Provider", detail.routing?.provider ?? ""],
    ["Reasoning", detail.normalized?.reasoning ?? ""],
    ["Duration", formatDuration(detail.usage?.duration_ms ?? 0)],
    ["Input", String(detail.usage?.input_tokens ?? 0)],
    ["Output", String(detail.usage?.output_tokens ?? 0)],
    ["TTFT", formatDuration(detail.usage?.ttft_ms ?? 0)],
    ["Plugin", detail.routing?.plugin ?? ""],
  ];
  for (const [label, value] of fields) {
    const dt = document.createElement("dt");
    dt.textContent = label;
    const dd = document.createElement("dd");
    if (label === "HTTP") {
      dd.innerHTML = `<span class="status-code ${statusClass(code)}">${value}</span>`;
    } else {
      dd.textContent = value;
    }
    header.append(dt, dd);
  }
  container.append(header);

  const routePanel = document.createElement("div");
  routePanel.className = "panel";
  routePanel.innerHTML = `<div class="muted" style="margin-bottom:0.35rem">Route</div>`;
  const chain = document.createElement("div");
  chain.className = "route-chain";
  const alias = detail.client?.model ?? "";
  const reasoning = detail.normalized?.reasoning;
  const aliasStep =
    reasoning && reasoning !== "default"
      ? `${alias} (${reasoning})`
      : alias;
  const steps = [
    "Cursor",
    aliasStep,
    detail.routing?.provider ?? "",
    detail.routing?.upstream_model ?? "",
  ].filter(Boolean);
  steps.forEach((step, i) => {
    if (i > 0) {
      const arrow = document.createElement("span");
      arrow.className = "arrow";
      arrow.textContent = "→";
      chain.append(arrow);
    }
    const span = document.createElement("span");
    span.className = "step";
    span.textContent = step;
    chain.append(span);
  });
  routePanel.append(chain);
  container.append(routePanel);

  if (detail.error) {
    const err = document.createElement("div");
    err.className = "panel";
    err.style.borderColor = "var(--err)";
    err.innerHTML = `<strong>Error</strong> <span class="mono">${escapeHtml(detail.error.code)}</span>: ${escapeHtml(detail.error.message)}`;
    container.append(err);
  }

  const tabs = document.createElement("div");
  tabs.className = "tabs";
  const tabSummary = makeTab("Summary", true);
  const tabTimeline = makeTab("Timeline", false);
  const tabRaw = makeTab("Raw", false);
  const hasFull = detail.full !== undefined && detail.full !== null;
  if (!hasFull) {
    tabRaw.style.display = "none";
  }
  tabs.append(tabSummary, tabTimeline, tabRaw);

  const panes = document.createElement("div");
  const summaryPane = document.createElement("div");
  summaryPane.textContent = "Request completed with the routing and usage shown above.";
  const timelinePane = document.createElement("div");
  timelinePane.style.display = "none";
  timelinePane.append(renderTimeline(detail.timeline ?? []));
  const rawPane = document.createElement("div");
  rawPane.style.display = "none";
  if (hasFull) {
    const label = document.createElement("div");
    label.className = "muted";
    label.textContent = "Full trace";
    const pre = document.createElement("pre");
    pre.className = "mono";
    pre.textContent = JSON.stringify(detail.full, null, 2);
    rawPane.append(label, pre);
  }
  panes.append(summaryPane, timelinePane, rawPane);

  function activate(which: "summary" | "timeline" | "raw") {
    tabSummary.classList.toggle("active", which === "summary");
    tabTimeline.classList.toggle("active", which === "timeline");
    tabRaw.classList.toggle("active", which === "raw");
    summaryPane.style.display = which === "summary" ? "block" : "none";
    timelinePane.style.display = which === "timeline" ? "block" : "none";
    rawPane.style.display = which === "raw" ? "block" : "none";
  }
  tabSummary.addEventListener("click", () => activate("summary"));
  tabTimeline.addEventListener("click", () => activate("timeline"));
  tabRaw.addEventListener("click", () => activate("raw"));

  container.append(tabs, panes);
}

function makeTab(label: string, active: boolean): HTMLButtonElement {
  const b = document.createElement("button");
  b.type = "button";
  b.className = "tab" + (active ? " active" : "");
  b.textContent = label;
  return b;
}

function renderTimeline(events: { seq: number; ts: number; type: string; payload: unknown }[]): HTMLElement {
  const ul = document.createElement("ul");
  ul.className = "timeline";
  const sorted = [...events].sort((a, b) => a.seq - b.seq);
  for (const ev of sorted) {
    const li = document.createElement("li");
    const details = document.createElement("details");
    const summary = document.createElement("summary");
    summary.textContent = `${timelineLabel(ev.type)} · ${formatTime(ev.ts)}`;
    details.append(summary);
    if (ev.payload !== null && ev.payload !== undefined) {
      const pre = document.createElement("pre");
      pre.textContent = JSON.stringify(ev.payload, null, 2);
      details.append(pre);
    }
    li.append(details);
    ul.append(li);
  }
  if (sorted.length === 0) {
    const p = document.createElement("p");
    p.className = "muted";
    p.textContent = "No timeline events.";
    return p;
  }
  return ul;
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
