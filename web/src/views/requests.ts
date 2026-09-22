import { fetchRequests, type RequestRow } from "../api";
import { formatDuration, formatTime, statusClass } from "../format";

export interface RequestsViewOpts {
  onOpen: (id: string) => void;
}

export function createRequestsView(opts: RequestsViewOpts): HTMLElement {
  const root = document.createElement("div");
  root.className = "requests-view";

  const filters = document.createElement("div");
  filters.className = "filters";
  const idFilter = document.createElement("input");
  idFilter.placeholder = "Filter id";
  const modelFilter = document.createElement("input");
  modelFilter.placeholder = "Filter model";
  const statusFilter = document.createElement("input");
  statusFilter.placeholder = "Filter status";
  const providerFilter = document.createElement("input");
  providerFilter.placeholder = "Filter provider";
  filters.append(idFilter, modelFilter, statusFilter, providerFilter);

  const tableWrap = document.createElement("div");
  root.append(filters, tableWrap);

  let rows: RequestRow[] = [];
  let pollTimer: number | undefined;

  function filtered(): RequestRow[] {
    const idQ = idFilter.value.trim().toLowerCase();
    const modelQ = modelFilter.value.trim().toLowerCase();
    const statusQ = statusFilter.value.trim().toLowerCase();
    const provQ = providerFilter.value.trim().toLowerCase();
    return rows.filter((r) => {
      if (idQ && !r.id.toLowerCase().includes(idQ)) return false;
      if (modelQ && !r.client_model.toLowerCase().includes(modelQ)) return false;
      if (statusQ) {
        const st = String(r.http_status || r.status).toLowerCase();
        if (!st.includes(statusQ) && !r.status.toLowerCase().includes(statusQ)) return false;
      }
      if (provQ && !r.provider.toLowerCase().includes(provQ)) return false;
      return true;
    });
  }

  function renderTable() {
    const list = filtered();
    const table = document.createElement("table");
    table.className = "data";
    table.innerHTML = `<thead><tr>
      <th>Status</th><th>Time</th><th>Model</th><th>Provider</th>
      <th>Tokens</th><th>TTFT</th><th>Duration</th><th>Plugin</th>
    </tr></thead>`;
    const tbody = document.createElement("tbody");
    for (const r of list) {
      const tr = document.createElement("tr");
      tr.className = "clickable";
      const code = r.http_status || 0;
      tr.innerHTML = `
        <td><span class="status-code ${statusClass(code)}">${code || "—"}</span></td>
        <td class="mono">${formatTime(r.started_at)}</td>
        <td class="mono">${escapeHtml(r.client_model)}</td>
        <td>${escapeHtml(r.provider)}</td>
        <td class="mono">${r.input_tokens}/${r.output_tokens}</td>
        <td class="mono">${formatDuration(r.ttft_ms)}</td>
        <td class="mono">${formatDuration(r.duration_ms)}</td>
        <td class="mono muted">${escapeHtml(r.plugin_id)}</td>`;
      tr.addEventListener("click", () => opts.onOpen(r.id));
      tbody.append(tr);
    }
    table.append(tbody);
    tableWrap.replaceChildren(table);
    if (list.length === 0) {
      const empty = document.createElement("p");
      empty.className = "muted";
      empty.textContent = rows.length === 0 ? "No requests yet." : "No rows match filters.";
      tableWrap.append(empty);
    }
  }

  async function refresh() {
    try {
      rows = await fetchRequests();
      renderTable();
    } catch {
      /* shell handles auth */
    }
  }

  function schedulePoll() {
    if (pollTimer) window.clearInterval(pollTimer);
    pollTimer = window.setInterval(() => {
      if (document.visibilityState === "visible") refresh();
    }, 2000);
  }

  [idFilter, modelFilter, statusFilter, providerFilter].forEach((el) =>
    el.addEventListener("input", renderTable),
  );

  refresh();
  schedulePoll();

  return root;
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
